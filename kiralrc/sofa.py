"""SOFA（Singing-Oriented Forced Aligner）歌声对齐。

流程：整句切片 → 拼音 .lab → 主环境纯 PyTorch 原生推理（CUDA/CPU）→ 读回逐音节时间。

为什么按句切片而不是整首：SOFA 在 opencpop 的**乐句**上训练，整首 3 分钟会塌到开头。
为什么按字数限长：否则遇到长间奏，模型会把一句摊到几十秒里。
"""
from __future__ import annotations

import json
import re
import sys
import warnings
from pathlib import Path

import numpy as np

from . import audio as au
from . import config, format as fmt, text as tx

HAN = re.compile(r"[\u4e00-\u9fff]")
TS = re.compile(r"^\[(\d{2}):(\d{2}\.\d{2,3})\](.*)$")
SPEAKER_TAG_RE = re.compile(r"【[^】]*】|^\s*[^\s：:，,。！？!?]{1,8}[：:]\s*(?=\S)")

# 中文歌词中混入的常见数字/大写字母缩写 → SOFA 词典合法近似拼音（单字符对应单音节，如 G20 → ji er shi）
_DIGIT_PINYIN = {
    "0": "ling", "1": "yi", "2": "er", "3": "san", "4": "si",
    "5": "wu", "6": "liu", "7": "qi", "8": "ba", "9": "jiu",
}
_LETTER_PINYIN = {
    "A": "ei", "B": "bi", "C": "xi", "D": "di", "E": "yi", "F": "ai",
    "G": "ji", "H": "ai", "I": "ai", "J": "jie", "K": "ke", "L": "ai",
    "M": "ai", "N": "en", "O": "ou", "P": "pi", "Q": "qiu", "R": "a",
    "S": "ai", "T": "ti", "U": "you", "V": "wei", "W": "da", "X": "ai",
    "Y": "wai", "Z": "zei",
}


def _line_tokens_and_syls(text: str, tag_mask: list[bool],
                          valid_keys: set[str] | None = None) -> tuple[list[int], list[str], list[str]]:
    """提取一句中所有可唱字符的索引、字符及 SOFA 拼音（支持汉字 + 混排数字/大写缩写字母如 G20、KTV、520）。"""
    # 先用整句（去标签）算一遍带多音字词表的汉字拼音
    sung_only = "".join(ch for i, ch in enumerate(text) if not tag_mask[i])
    han_syls = tx.pinyin_syllables(sung_only)
    han_iter = iter(han_syls)

    indices: list[int] = []
    chars: list[str] = []
    syls: list[str] = []
    n = len(text)
    for i, ch in enumerate(text):
        if tag_mask[i]:
            continue
        if HAN.match(ch):
            indices.append(i)
            chars.append(ch)
            syls.append(next(han_iter, ""))
        elif ch.isdigit():
            # 整十两位数字的末尾 0（如 20/30/50）在中文里读作「十」(shi)
            prev_is_d = i > 0 and text[i - 1].isdigit() and not tag_mask[i - 1]
            next_is_d = i + 1 < n and text[i + 1].isdigit() and not tag_mask[i + 1]
            prev2_is_d = i > 1 and text[i - 2].isdigit() and not tag_mask[i - 2]
            if ch == "0" and prev_is_d and not next_is_d and not prev2_is_d and text[i - 1] != "0":
                py = "shi"
            else:
                py = _DIGIT_PINYIN[ch]
            if valid_keys is None or py in valid_keys:
                indices.append(i)
                chars.append(ch)
                syls.append(py)
        elif "A" <= ch <= "Z":
            # 仅对独立缩写/大写字母（非普通英文长单词，如 G20/KTV/OK/MV）做近似拼音映射
            prev_lower = i > 0 and "a" <= text[i - 1] <= "z"
            next_lower = i + 1 < n and "a" <= text[i + 1] <= "z"
            if not prev_lower and not next_lower:
                py = _LETTER_PINYIN.get(ch, "")
                if py and (valid_keys is None or py in valid_keys):
                    indices.append(i)
                    chars.append(ch)
                    syls.append(py)
    return indices, chars, syls


def _speaker_tag_mask(text: str) -> list[bool]:
    mask = [False] * len(text)
    for m in SPEAKER_TAG_RE.finditer(text):
        for i in range(m.start(), m.end()):
            mask[i] = True
    return mask


def _trim_short_line_silence(env: np.ndarray, hop: float, t0: float,
                             end: float, n_chars: int) -> float:
    """唱完后若出现持续彻底静音（短句 <=4 字为 >=0.8s，长句为 >=1.5s 进间奏），
    提前截断切片，避免把后方间奏/背景和声吸进本句拉长行尾。"""
    is_short = n_chars <= 4
    if (is_short and (end - t0) <= 2.2) or (not is_short and (end - t0) <= max(4.5, n_chars * 0.35)) or len(env) == 0:
        return end
    i0 = max(0, int(max(0.0, t0 - 0.2) / hop))
    i1 = min(len(env), int(end / hop))
    if i1 - i0 < int(1.2 / hop):
        return end

    seg = env[i0:i1]
    peak = float(seg.max())
    if peak < 0.03:
        return end
    voice_th = max(0.025, peak * 0.15)
    sil_th = max(0.012, peak * 0.06)

    voiced_frames = 0
    sil_run = 0
    sil_start_idx = -1
    min_voiced = max(1, int((0.20 if is_short else max(1.0, n_chars * 0.22)) / hop))
    min_sil = max(1, int((0.80 if is_short else 1.40) / hop))
    for k, val in enumerate(seg):
        if val >= voice_th:
            voiced_frames += 1
            sil_run = 0
            sil_start_idx = -1
        elif voiced_frames >= min_voiced and val <= sil_th:
            if sil_run == 0:
                sil_start_idx = k
            sil_run += 1
            if sil_run >= min_sil:
                cut_t = (i0 + sil_start_idx) * hop + 0.25
                return min(end, max(t0 + (0.6 if is_short else n_chars * 0.25), cut_t))
        elif voiced_frames >= min_voiced:
            sil_run = 0
            sil_start_idx = -1
    return end


def prep_segments(vocal_wav: Path, lines: list[tuple[float, str]], out_dir: Path,
                  total: float, *, reference_ends: dict[tuple[float, str], float] | None = None
                  ) -> tuple[list[dict], list[tuple[str, list[str] | str]]]:
    """按行切片写 wav + 拼音 lab，返回 meta。"""
    out_dir.mkdir(parents=True, exist_ok=True)
    # 清理上次残留切片，避免旧的空/无效 wav 混入 SOFA 推理
    for old in list(out_dir.glob("*.wav")) + list(out_dir.glob("*.lab")):
        try:
            old.unlink()
        except OSError:
            pass
    words_dir = out_dir / "htk" / "words"
    if words_dir.exists():
        for old in words_dir.glob("*.lab"):
            try:
                old.unlink()
            except OSError:
                pass

    keys = {ln.split("\t")[0].strip()
            for ln in open(config.path("sofa_dict"), encoding="utf-8") if ln.strip()}
    wav = au.decode(vocal_wav, 44100)
    eff_total = min(float(total), len(wav) / 44100.0)
    env = au.rms_envelope(wav, sr=44100, hop=0.02)

    meta, missing = [], []
    prev_end: float | None = None
    for i, (t0, line) in enumerate(lines):
        has_next = i + 1 < len(lines)
        nxt = lines[i + 1][0] if has_next else eff_total
        tag_mask = _speaker_tag_mask(line)
        tok_idx, chars, syls = _line_tokens_and_syls(line, tag_mask, valid_keys=keys)
        if not chars:
            continue

        start = au.find_valley_start(env, 0.02, t0, nxt, prev_end=prev_end, lead=0.35)
        if start >= eff_total - 0.05:
            continue
        min_dur = max(0.8, len(chars) * 0.28)
        next_cap = au.find_valley_end(env, 0.02, t0, nxt, eff_total, min_line_dur=min_dur) if has_next else eff_total
        end = min(eff_total, next_cap, t0 + max(3.0, len(chars) * 0.85) + 1.0)
        end = _trim_short_line_silence(env, 0.02, t0, end, len(chars))
        end = min(eff_total, max(end, start + 0.8))
        ref_end = reference_ends.get((t0, line)) if reference_ends else None
        end = au.protect_reference_end(end, ref_end, t0, nxt, eff_total)
        seg = wav[int(start * 44100): int(end * 44100)]
        if len(seg) < int(0.05 * 44100):
            continue
        prev_end = end

        bad = [s for s in syls if s not in keys]
        if bad:
            missing.append((line[:12], bad))
        if len(syls) != len(chars):
            missing.append((line[:12], f"拼音数 {len(syls)} != 音节数 {len(chars)}"))

        name = f"l{i + 1:02d}"
        au.write_wav(out_dir / f"{name}.wav", seg, 44100)
        (out_dir / f"{name}.lab").write_text(" ".join(syls), encoding="utf-8")
        meta.append({"name": name, "offset": start, "start": t0, "seg_end": end,
                     "text": line, "chars": chars, "syls": syls, "tok_idx": tok_idx})

    (out_dir / "meta.json").write_text(
        json.dumps(meta, ensure_ascii=False, indent=1), encoding="utf-8")
    return meta, missing


def _sofa_forward_pass_py(
    T: int,
    S: int,
    prob_log: np.ndarray,
    not_edge_prob_log: np.ndarray,
    edge_prob_log: np.ndarray,
    curr_ph_max_prob_log: np.ndarray,
    dp: np.ndarray,
    backtrack_s: np.ndarray,
    ph_seq_id: np.ndarray,
    prob3_pad_len: int,
):
    for t in range(1, T):
        prob1 = dp[t - 1, :] + prob_log[t, :] + not_edge_prob_log[t]

        prob2 = np.empty(S, dtype=np.float32)
        prob2[0] = -np.inf
        for i in range(1, S):
            prob2[i] = (
                dp[t - 1, i - 1]
                + prob_log[t, i - 1]
                + edge_prob_log[t]
                + curr_ph_max_prob_log[i - 1] * (T / S)
            )

        prob3 = np.empty(S, dtype=np.float32)
        for i in range(prob3_pad_len):
            prob3[i] = -np.inf
        for i in range(prob3_pad_len, S):
            if i - prob3_pad_len + 1 < S - 1 and ph_seq_id[i - prob3_pad_len + 1] != 0:
                prob3[i] = -np.inf
            else:
                prob3[i] = (
                    dp[t - 1, i - prob3_pad_len]
                    + prob_log[t, i - prob3_pad_len]
                    + edge_prob_log[t]
                    + curr_ph_max_prob_log[i - prob3_pad_len] * (T / S)
                )

        for i in range(S):
            v0, v1, v2 = prob1[i], prob2[i], prob3[i]
            max_idx = 0
            max_val = v0
            if v1 > max_val:
                max_val = v1
                max_idx = 1
            if v2 > max_val:
                max_val = v2
                max_idx = 2
            dp[t, i] = max_val
            backtrack_s[t, i] = max_idx

        for i in range(S):
            if backtrack_s[t, i] == 0:
                if prob_log[t, i] > curr_ph_max_prob_log[i]:
                    curr_ph_max_prob_log[i] = prob_log[t, i]
            elif backtrack_s[t, i] > 0:
                curr_ph_max_prob_log[i] = prob_log[t, i]

        for i in range(S):
            if ph_seq_id[i] == 0:
                curr_ph_max_prob_log[i] = 0.0

    return dp, backtrack_s, curr_ph_max_prob_log


try:
    import numba as _numba
    _sofa_forward_pass = _numba.njit(_sofa_forward_pass_py, cache=True)
except Exception:
    _sofa_forward_pass = _sofa_forward_pass_py


_SOFA_MODEL_CACHE: dict[tuple[str, str], dict] = {}


def _load_sofa_bundle(ckpt_path: Path, device: str | None = None) -> dict:
    """直接加载 SOFA 权重到主环境 PyTorch（不依赖 lightning / librosa / pandas / matplotlib）。"""
    import torch
    import torch.nn as nn
    import torchaudio
    import yaml

    dev = device or ("cuda" if torch.cuda.is_available() else "cpu")
    key = (str(ckpt_path.resolve()), dev)
    if key in _SOFA_MODEL_CACHE:
        return _SOFA_MODEL_CACHE[key]

    sofa_root = Path(config.path("sofa_root"))
    if str(sofa_root) not in sys.path:
        sys.path.insert(0, str(sofa_root))

    # 绕过 third_party/SOFA/modules/__init__.py 对 rmvpe/librosa/lightning 的急切导入，仅加载纯 PyTorch 网络层
    if "modules" not in sys.modules:
        import types
        pkg = types.ModuleType("modules")
        pkg.__path__ = [str(sofa_root / "modules")]
        sys.modules["modules"] = pkg

    from modules.layer.backbone.unet import UNetBackbone
    from modules.layer.block.resnet_block import ResidualBasicBlock
    from modules.layer.scaling.stride_conv import DownSampling, UpSampling

    ckpt = torch.load(str(ckpt_path), map_location="cpu", weights_only=False)
    hp = ckpt["hyper_parameters"]
    vocab = yaml.safe_load(hp["vocab_text"])
    mc = hp["model_config"]
    mel_cfg = hp["melspec_config"]

    backbone = UNetBackbone(
        mel_cfg["n_mels"],
        mc["hidden_dims"],
        mc["hidden_dims"],
        ResidualBasicBlock,
        DownSampling,
        UpSampling,
        down_sampling_factor=mc["down_sampling_factor"],
        down_sampling_times=mc["down_sampling_times"],
        channels_scaleup_factor=mc["channels_scaleup_factor"],
    )
    head = nn.Linear(mc["hidden_dims"], vocab["<vocab_size>"] + 2)

    sd = ckpt["state_dict"]
    backbone.load_state_dict(
        {k[len("backbone."):]: v for k, v in sd.items() if k.startswith("backbone.")},
        strict=True,
    )
    head.load_state_dict(
        {k[len("head."):]: v for k, v in sd.items() if k.startswith("head.")},
        strict=True,
    )
    backbone = backbone.to(dev).eval()
    head = head.to(dev).eval()

    n_fft = int(mel_cfg["n_fft"])
    win_length = int(mel_cfg["win_length"])
    with warnings.catch_warnings():
        warnings.simplefilter("ignore", UserWarning)
        mel_basis = torchaudio.functional.melscale_fbanks(
            n_freqs=n_fft // 2 + 1,
            f_min=float(mel_cfg["fmin"]),
            f_max=float(mel_cfg["fmax"]),
            n_mels=int(mel_cfg["n_mels"]),
            sample_rate=int(mel_cfg["sample_rate"]),
            norm="slaney",
            mel_scale="htk",
        ).T.float().to(dev)
    hann_window = torch.hann_window(win_length, device=dev)

    bundle = {
        "device": dev,
        "vocab": vocab,
        "mel_cfg": mel_cfg,
        "backbone": backbone,
        "head": head,
        "mel_basis": mel_basis,
        "hann_window": hann_window,
    }
    _SOFA_MODEL_CACHE[key] = bundle
    return bundle


def _load_dict(dict_path: Path) -> dict[str, list[str]]:
    out: dict[str, list[str]] = {}
    for ln in dict_path.read_text(encoding="utf-8").splitlines():
        ln = ln.strip()
        if not ln or "\t" not in ln:
            continue
        k, v = ln.split("\t", 1)
        out[k.strip()] = v.strip().split()
    return out


def _g2p_text(lab_text: str, dictionary: dict[str, list[str]]) -> tuple[list[str], list[str], list[int]]:
    word_seq: list[str] = []
    ph_seq: list[str] = ["SP"]
    ph_idx_to_word_idx: list[int] = [-1]
    word_idx = 0
    for word in lab_text.strip().split():
        if word == "SP":
            if ph_seq and ph_seq[-1] == "SP":
                ph_idx_to_word_idx[-1] = -2
            continue
        if word not in dictionary:
            continue
        word_seq.append(word)
        phones = dictionary[word]
        for i, ph in enumerate(phones):
            if (i == 0 or i == len(phones) - 1) and ph == "SP":
                continue
            ph_seq.append(ph)
            ph_idx_to_word_idx.append(word_idx)
        if ph_seq[-1] != "SP":
            ph_seq.append("SP")
            ph_idx_to_word_idx.append(-1)
        word_idx += 1
    return ph_seq, word_seq, ph_idx_to_word_idx


def _decode_viterbi(ph_seq_id: np.ndarray, ph_prob_log: np.ndarray, edge_prob: np.ndarray):
    T = ph_prob_log.shape[0]
    S = len(ph_seq_id)
    prob_log = np.ascontiguousarray(ph_prob_log[:, ph_seq_id], dtype=np.float32)
    edge_prob_log = np.log(edge_prob + 1e-6).astype(np.float32)
    not_edge_prob_log = np.log(1.0 - edge_prob + 1e-6).astype(np.float32)

    curr_ph_max_prob_log = np.full(S, -np.inf, dtype=np.float32)
    dp = np.full((T, S), -np.inf, dtype=np.float32)
    backtrack_s = np.full((T, S), -1, dtype=np.int32)

    dp[0, 0] = prob_log[0, 0]
    curr_ph_max_prob_log[0] = prob_log[0, 0]
    if ph_seq_id[0] == 0 and S > 1:
        dp[0, 1] = prob_log[0, 1]
        curr_ph_max_prob_log[1] = prob_log[0, 1]

    prob3_pad_len = 2 if S >= 2 else 1
    dp, backtrack_s, _ = _sofa_forward_pass(
        T, S, prob_log, not_edge_prob_log, edge_prob_log,
        curr_ph_max_prob_log, dp, backtrack_s, ph_seq_id.astype(np.int64), prob3_pad_len,
    )

    s = S - 2 if (S >= 2 and dp[-1, -2] > dp[-1, -1] and ph_seq_id[-1] == 0) else S - 1
    ph_idx_seq, ph_time_int = [], []
    for t in range(T - 1, -1, -1):
        step = int(backtrack_s[t, s])
        if step != 0:
            ph_idx_seq.append(s)
            ph_time_int.append(t)
            s -= step
    ph_idx_seq.reverse()
    ph_time_int.reverse()
    return np.array(ph_idx_seq, dtype=np.int64), np.array(ph_time_int, dtype=np.int64)


def _postprocess_words(word_seq: list[str], word_intervals: np.ndarray, wav_length: float):
    """等价于 SOFA post_processing：填补 <0.1s 微缝并补齐首尾 SP。"""
    if len(word_seq) == 0:
        return ["SP"], np.array([[0.0, wav_length]], dtype=np.float32)
    iv = word_intervals.copy()
    if 0.0 < iv[0, 0] < 0.1:
        iv[0, 0] = 0.0
    for idx in range(len(word_seq) - 1):
        gap = iv[idx + 1, 0] - iv[idx, 1]
        if 0.0 < gap < 0.1:
            mid = (iv[idx, 1] + iv[idx + 1, 0]) * 0.5
            iv[idx, 1] = mid
            iv[idx + 1, 0] = mid
    if iv[-1, 1] < wav_length and wav_length - iv[-1, 1] < 0.1:
        iv[-1, 1] = wav_length

    out_seq, out_iv = ["SP"], [[0.0, float(iv[0, 0])]]
    for w, (s, e) in zip(word_seq, iv):
        if out_iv[-1][1] < s:
            out_seq.append("SP")
            out_iv.append([out_iv[-1][1], float(s)])
        out_seq.append(w)
        out_iv.append([float(s), float(e)])
    if out_iv[-1][1] < wav_length:
        out_seq.append("SP")
        out_iv.append([out_iv[-1][1], float(wav_length)])
    if iv[0, 0] <= 0.0:
        out_seq = out_seq[1:]
        out_iv = out_iv[1:]
    return out_seq, out_iv


def run(segments_dir: Path, out_formats: str = "htk") -> None:
    """在主环境直接用纯 PyTorch 跑 SOFA 推理（无需独立 .venv-sofa，支持 CUDA 加速）。"""
    import torch
    import torch.nn.functional as F

    ckpt = config.path("sofa_ckpt")
    dic = config.path("sofa_dict")
    if not Path(ckpt).exists():
        raise RuntimeError(f"找不到 SOFA 模型: {ckpt}（运行 scripts/fetch_models.py）")
    if not Path(dic).exists():
        raise RuntimeError(f"找不到 SOFA 词典: {dic}")

    bundle = _load_sofa_bundle(Path(ckpt))
    dictionary = _load_dict(Path(dic))
    dev = bundle["device"]
    vocab = bundle["vocab"]
    mel_cfg = bundle["mel_cfg"]
    sr = int(mel_cfg["sample_rate"])
    n_fft = int(mel_cfg["n_fft"])
    win_length = int(mel_cfg["win_length"])
    hop_length = int(mel_cfg["hop_length"])
    scale_factor = int(mel_cfg["scale_factor"])
    clamp_val = float(mel_cfg["clamp"])
    frame_length = hop_length / float(sr * scale_factor)

    words_out = segments_dir / "htk" / "words"
    words_out.mkdir(parents=True, exist_ok=True)

    wav_paths = sorted(segments_dir.glob("*.wav"))
    if not wav_paths:
        raise RuntimeError(f"切片目录为空: {segments_dir}")

    pad_left = n_fft // 2
    pad_right = (n_fft + 1) // 2

    with torch.no_grad():
        for wav_path in wav_paths:
            lab_path = wav_path.with_suffix(".lab")
            if not lab_path.exists():
                continue
            lab_text = lab_path.read_text(encoding="utf-8").strip()
            ph_seq, word_seq, ph_idx_to_word_idx = _g2p_text(lab_text, dictionary)
            if not word_seq:
                continue
            if wav_path.stat().st_size <= 44:
                continue

            wav_np = au.decode(wav_path, sr)
            wav_length = len(wav_np) / float(sr)
            waveform = torch.from_numpy(wav_np).to(device=dev, dtype=torch.float32)

            x = F.pad(waveform.unsqueeze(0), (pad_left, pad_right))
            mag = torch.stft(
                x, n_fft=n_fft, hop_length=hop_length, win_length=win_length,
                window=bundle["hann_window"], center=False, return_complex=True,
            ).abs()
            melspec = torch.log(torch.clamp(torch.matmul(bundle["mel_basis"], mag), min=clamp_val))
            melspec = (melspec - melspec.mean()) / (melspec.std() + 1e-8)
            melspec = melspec.repeat_interleave(scale_factor, dim=-1)

            h = bundle["backbone"](melspec.transpose(1, 2))
            logits = bundle["head"](h)
            ph_frame_logits = logits[:, :, 2:]
            ph_edge_logits = logits[:, :, 0]

            num_frames = int((wav_length * scale_factor * sr + 0.5) / hop_length)
            ph_frame_logits = ph_frame_logits[:, :num_frames, :]
            ph_edge_logits = ph_edge_logits[:, :num_frames]

            ph_seq_id = np.array([vocab[ph] for ph in ph_seq], dtype=np.int64)
            ph_mask = np.zeros(vocab["<vocab_size>"], dtype=np.bool_)
            ph_mask[ph_seq_id] = True
            ph_mask[0] = True
            mask_t = (~torch.from_numpy(ph_mask).to(dev)).float().view(1, 1, -1) * 1e9

            ph_prob_log = torch.log_softmax(ph_frame_logits.float() - mask_t, dim=-1).squeeze(0).cpu().numpy().astype(np.float32)
            ph_edge_pred = ((torch.sigmoid(ph_edge_logits.float()) - 0.1) / 0.8).clamp(0.0, 1.0).squeeze(0).cpu().numpy().astype(np.float32)

            T = ph_prob_log.shape[0]
            edge_diff = np.concatenate((np.diff(ph_edge_pred, axis=0), [0.0]), axis=0)
            edge_prob = (ph_edge_pred + np.concatenate(([0.0], ph_edge_pred[:-1]))).clip(0.0, 1.0)

            ph_idx_seq, ph_time_int_pred = _decode_viterbi(ph_seq_id, ph_prob_log, edge_prob)
            ph_time_frac = (edge_diff[ph_time_int_pred] / 2.0).clip(-0.5, 0.5)
            ph_time_pred = frame_length * np.concatenate([ph_time_int_pred.astype(np.float32) + ph_time_frac, [float(T)]])
            ph_intervals = np.stack([ph_time_pred[:-1], ph_time_pred[1:]], axis=1)

            w_seq_pred: list[str] = []
            w_iv_pred: list[list[float]] = []
            last_w = -1
            for idx_i, ph_idx in enumerate(ph_idx_seq):
                if ph_seq[ph_idx] == "SP":
                    if ph_idx_to_word_idx[ph_idx] == -2:
                        w_seq_pred.append("BND")
                        w_iv_pred.append([float(ph_intervals[idx_i, 0]), float(ph_intervals[idx_i, 1])])
                        last_w = -1
                    continue
                w_idx = ph_idx_to_word_idx[ph_idx]
                if w_idx == last_w:
                    w_iv_pred[-1][1] = float(ph_intervals[idx_i, 1])
                else:
                    w_seq_pred.append(word_seq[w_idx])
                    w_iv_pred.append([float(ph_intervals[idx_i, 0]), float(ph_intervals[idx_i, 1])])
                    last_w = w_idx

            w_iv_arr = np.array(w_iv_pred, dtype=np.float32).clip(min=0.0) if w_iv_pred else np.zeros((0, 2), dtype=np.float32)
            out_seq, out_iv = _postprocess_words(w_seq_pred, w_iv_arr, wav_length)
            lines = [f"{int(round(s * 1e7))} {int(round(e * 1e7))} {w}" for w, (s, e) in zip(out_seq, out_iv)]
            (words_out / f"{wav_path.stem}.lab").write_text("\n".join(lines) + "\n", encoding="utf-8")


def clipped_start_ratio(words_dir: Path, threshold: float = 0.04) -> tuple[int, int]:
    """统计 SOFA 输出中首音节紧贴切片左边界（< threshold 秒）的句数与总句数。

    正常切片留有 0.35s 前置缓冲，若大量句子首音节被压在 0.00s 附近，
    说明参考 LRC 的时间戳整体滞后于音频，需要走 ASR 重定位。
    """
    clipped = 0
    total = 0
    if not words_dir.exists():
        return 0, 0
    for f in sorted(words_dir.glob("l*.lab")):
        words = read_words(f)
        if not words:
            continue
        total += 1
        if words[0][0] < threshold:
            clipped += 1
    return clipped, total


def read_words(path: Path) -> list[tuple[float, float, str]]:
    """htk/words/*.lab → [(start, end, token)]，时间相对切片起点。"""
    out = []
    for ln in open(path, encoding="utf-8"):
        p = ln.split()
        if len(p) >= 3:
            out.append((int(p[0]) / 1e7, int(p[1]) / 1e7, p[2]))
    return out


def _fix_collapsed_syllables(syl: list[tuple[float, float, str]]) -> list[tuple[float, float, str]]:
    """修复 SOFA 在重度修音/鬼畜/咬字含糊处将某个音节压缩到 1~3 帧（<35ms）、
    同时把其时长吞并到前/后相邻音节的畸形对齐。"""
    if len(syl) < 2:
        return syl
    durs = [e - s for s, e, _ in syl if (e - s) >= 0.05]
    if not durs:
        return syl
    med = float(np.clip(np.median(durs), 0.22, 0.42))

    out = [list(w) for w in syl]
    for j in range(len(out)):
        cur_dur = out[j][1] - out[j][0]
        if cur_dur >= 0.035:
            continue
        prev_dur = (out[j - 1][1] - out[j - 1][0]) if j > 0 else 0.0
        next_dur = (out[j + 1][1] - out[j + 1][0]) if j + 1 < len(out) else 0.0
        if j > 0 and prev_dur >= max(0.32, med * 1.2):
            total_pair = out[j][1] - out[j - 1][0]
            keep_prev = med if total_pair > med * 2.2 else total_pair * 0.5
            split_t = out[j - 1][0] + keep_prev
            out[j - 1][1] = split_t
            out[j][0] = split_t
        elif j + 1 < len(out) and next_dur >= max(0.36, med * 1.3):
            total_pair = out[j + 1][1] - out[j][0]
            keep_cur = med if total_pair > med * 2.2 else total_pair * 0.5
            split_t = out[j][0] + keep_cur
            out[j][1] = split_t
            out[j + 1][0] = split_t
    return [(s, e, w) for s, e, w in out]


def _trim_leading_silence(syl: list[tuple[float, float, str]], off: float,
                          env: np.ndarray | None, hop: float = 0.02) -> list[tuple[float, float, str]]:
    """清理句首落在静音里的字：
    1) 若从 syl[0] 起就是持续 >=0.14s 的彻底静音（歌手还没张嘴），直接跳过静音推到真正出声处；
    2) 若首字（或贴边坍塌前导字后的字）异常拉长横跨前导静音/上句残响，从强峰往左回溯到真正起音点。"""
    if not syl or env is None or len(env) == 0:
        return syl

    # 规则 1：句首直接是彻底静音（例如「恶 12ms」「鬼 26ms」「霸」前半段全在平直静音里）
    abs_s0 = off + syl[0][0]
    abs_end_all = off + syl[-1][1]
    i0 = max(0, int(abs_s0 / hop))
    i_max = min(len(env), int(min(abs_end_all - 0.2, abs_s0 + 2.0) / hop))
    if i_max > i0 + int(0.14 / hop):
        i_line_end = min(len(env), int(abs_end_all / hop))
        line_p80 = float(np.percentile(env[i0:i_line_end], 80)) if i_line_end > i0 + 5 else 0.1
        lead_sil_th = min(0.045, max(0.028, line_p80 * 0.22))
        k = i0
        while k < i_max and env[k] < lead_sil_th:
            k += 1
        sil_dur = (k - i0) * hop
        if sil_dur >= 0.14 and k < i_max:
            true_onset = k * hop
            # 找到最后一个受这段静音影响的字（终点 <= true_onset 或跨越 true_onset）
            end_idx = 0
            while end_idx < len(syl) - 1 and (off + syl[end_idx][1]) <= true_onset + 0.08:
                end_idx += 1
            # 若下一个字也异常偏长（如「王 1.07s」），一并纳入按能量谷重新分配
            if end_idx + 1 < len(syl) - 1 and (syl[end_idx + 1][1] - syl[end_idx + 1][0]) >= 0.70:
                end_idx += 1
            elif (off + syl[end_idx][1]) - true_onset < (end_idx + 1) * 0.16 and end_idx + 1 < len(syl):
                end_idx += 1
            end_t = off + syl[end_idx][1]
            if end_t > true_onset + 0.12:
                n_re = end_idx + 1
                out = list(syl)
                if n_re == 1:
                    out[0] = (true_onset - off, syl[0][1], syl[0][2])
                else:
                    splits = _find_energy_valleys(env, hop, true_onset, end_t, n_re - 1, min_gap=0.14)
                    if not splits:
                        step = (end_t - true_onset) / n_re
                        splits = [true_onset + step * (m + 1) for m in range(n_re - 1)]
                    bounds = [true_onset] + splits + [end_t]
                    for m in range(n_re):
                        out[m] = (bounds[m] - off, bounds[m + 1] - off, syl[m][2])
                syl = out

    # 规则 2：统计句首连续坍塌极短字数量（如「恶(9ms) 鬼(6ms) 霸(1.09s)」）
    lead_short = 0
    while lead_short < min(3, len(syl) - 1) and (syl[lead_short][1] - syl[lead_short][0]) < 0.05:
        lead_short += 1

    s_lead, e_lead, w_lead = syl[lead_short]
    dur_lead = e_lead - s_lead
    rest = [e - s for s, e, _ in syl[lead_short + 1:] if (e - s) >= 0.05]
    med = float(np.clip(np.median(rest), 0.22, 0.45)) if rest else 0.35
    min_stretch = max(0.65, med * 1.8) if lead_short > 0 else max(0.75, med * 2.0)
    if dur_lead < min_stretch:
        return syl

    abs_s = off + syl[0][0]
    abs_e = off + e_lead
    i0 = max(0, int(abs_s / hop))
    i1 = min(len(env), int(abs_e / hop))
    if i1 - i0 < int(0.4 / hop):
        return syl

    # 取该超长字后半段的峰值能量作为真正主唱参考
    tail_i0 = max(i0, i1 - max(4, int(min(dur_lead * 0.5, med * 1.3) / hop)))
    tail_peak = float(env[tail_i0:i1].max()) if i1 > tail_i0 else 0.0
    if tail_peak < 0.03:
        return syl

    # 从后半段强峰往左回溯，遇到真正低于静音/气声阈值的低谷即止（上限 0.045，避免把大嗓门句子的正常字间低谷误判为静音）
    peak_idx = tail_i0 + int(np.argmax(env[tail_i0:i1]))
    onset_th = min(0.045, max(0.018, tail_peak * 0.22))
    k = peak_idx
    low_run = 0
    while k > i0:
        if env[k] < onset_th:
            low_run += 1
            if low_run >= 2:
                break
        else:
            low_run = 0
        k -= 1

    new_abs_s = min(abs_e - 0.12, max(abs_s, (k + low_run) * hop))
    if new_abs_s - abs_s < 0.15:
        return syl

    out = list(syl)
    if lead_short == 0:
        out[0] = (new_abs_s - off, e_lead, w_lead)
        return out

    # 有贴边坍塌前导字：把 [0 .. end_idx] 一起重新落到 [new_abs_s, end_t] 内
    end_idx = lead_short
    end_t = abs_e
    if end_t - new_abs_s < (end_idx + 1) * 0.16 and end_idx + 1 < len(syl):
        end_idx += 1
        end_t = off + syl[end_idx][1]

    n_re = end_idx + 1
    splits = _find_energy_valleys(env, hop, new_abs_s, end_t, n_re - 1, min_gap=0.14)
    if not splits:
        step = (end_t - new_abs_s) / n_re
        splits = [new_abs_s + step * (m + 1) for m in range(n_re - 1)]
    bounds = [new_abs_s] + splits + [end_t]
    for m in range(n_re):
        out[m] = (bounds[m] - off, bounds[m + 1] - off, syl[m][2])
    return out


def _find_energy_valleys(env: np.ndarray, hop: float, abs_s: float, abs_e: float,
                         n_splits: int, min_gap: float = 0.22) -> list[float]:
    """在 [abs_s, abs_e] 内按人声 RMS 能量谷（相对两侧峰值的下陷深度）选出 n_splits 个切分点。"""
    if n_splits <= 0 or env is None or len(env) == 0:
        return []
    i0 = max(0, int((abs_s + min_gap) / hop))
    i1 = min(len(env) - 1, int((abs_e - min_gap) / hop))
    if i1 <= i0:
        return []

    win = max(2, int(0.18 / hop))
    # 计算每个局部极小值点的下陷深度（两侧局部峰值中较小者 - 当前谷值）
    cands: list[tuple[float, float]] = []
    for k in range(i0, i1 + 1):
        v = float(env[k])
        if k > 0 and k + 1 < len(env) and v <= env[k - 1] and v <= env[k + 1]:
            lp = float(env[max(0, k - win): k].max()) if k > 0 else v
            rp = float(env[k + 1: min(len(env), k + 1 + win)].max()) if k + 1 < len(env) else v
            side = min(lp, rp)
            depth = side - v
            # 谷底需比两侧较低峰至少低 25% 且绝对深度足够明显；
            # 按相对下陷比例（depth / side）排序，优先选真正跌近谷底的音节分界，避开长音内的轻微颤音
            if side >= 0.04 and depth >= max(0.02, side * 0.25):
                rel_depth = depth / (side + 1e-6)
                cands.append((rel_depth, k * hop))

    if len(cands) < n_splits:
        return []

    # 按深度从大到小贪心选出间距 >= min_gap 的 n_splits 个谷
    cands.sort(key=lambda x: -x[0])
    chosen: list[float] = []
    for _, t in cands:
        if all(abs(t - c) >= min_gap for c in chosen):
            chosen.append(t)
            if len(chosen) == n_splits:
                break
    if len(chosen) < n_splits:
        return []
    chosen.sort()
    return chosen


def _refine_stretched_syllables(syl: list[tuple[float, float, str]], off: float,
                                env: np.ndarray | None, hop: float = 0.02) -> list[tuple[float, float, str]]:
    """当非末字被 SOFA 异常拉长（吞并后续音节）且内部存在明显人声能量谷时，
    利用波形能量谷对受影响的尾部区间重新切分，恢复真实拍点。"""
    if len(syl) < 3 or env is None or len(env) == 0:
        return syl
    durs = [e - s for s, e, _ in syl if (e - s) >= 0.05]
    if not durs:
        return syl
    med = float(np.clip(np.median(durs), 0.22, 0.45))

    # 寻找首个异常拉长的非末字（>= max(0.85, 2.5*med)）
    bad_idx = -1
    for j in range(len(syl) - 1):
        if (syl[j][1] - syl[j][0]) >= max(0.85, med * 2.5):
            bad_idx = j
            break
    if bad_idx < 0:
        return syl

    # 若前一个字也明显偏长（>= 1.5*med），一并纳入重切分起点（例如「你」0.73s +「豪」1.31s）
    start_idx = bad_idx
    if start_idx > 0 and (syl[start_idx - 1][1] - syl[start_idx - 1][0]) >= max(0.55, med * 1.5):
        start_idx -= 1

    # 仅在区间确实包含足够多真实能量谷时才重切（避免误伤平滑真长拖音）
    n_words = len(syl) - start_idx
    abs_s = off + syl[start_idx][0]
    abs_e = off + syl[-1][1]
    splits = _find_energy_valleys(env, hop, abs_s, abs_e, n_words - 1, min_gap=0.22)
    if not splits:
        return syl

    # 校验重切结果：若原本 bad_idx 内部至少落入 1 个谷，说明确实是吞字而非平滑拖音
    bad_s = off + syl[bad_idx][0]
    bad_e = off + syl[bad_idx][1]
    if not any(bad_s + 0.12 < t < bad_e - 0.12 for t in splits):
        return syl

    bounds = [abs_s] + splits + [abs_e]
    out = list(syl[:start_idx])
    for k in range(n_words):
        _, _, w = syl[start_idx + k]
        out.append((bounds[k] - off, bounds[k + 1] - off, w))
    return out


def _pull_back_voiced_sp(syl: list[tuple[float, float, str]], off: float,
                         env: np.ndarray | None, hop: float = 0.02) -> list[tuple[float, float, str]]:
    """当 SOFA 因咬字含糊/重度调音把句首连续强歌声（>=0.30s）误标为 SP 静音跳过时，
    按人声轨包络把被挤到后面的前几个字拉回真实起音点。"""
    if len(syl) < 3 or env is None or len(env) == 0:
        return syl
    lead_sp = syl[0][0]
    if lead_sp < 0.30:
        return syl

    i0 = max(0, int(off / hop))
    i1 = min(len(env), int((off + lead_sp) / hop))
    if i1 - i0 < int(0.30 / hop):
        return syl

    sp_env = env[i0:i1]
    if float(sp_env.max()) < 0.08:
        return syl

    # 从 syl[0] 起点往左回溯，只要持续处于唱歌能量（>0.035）不跌入换气静音谷，就属于本句被吞的开头
    k = i1 - 1
    sil_run = 0
    while k >= i0:
        if env[k] < 0.035:
            sil_run += 1
            if sil_run >= 2:
                break
        else:
            sil_run = 0
        k -= 1

    voice_start = (k + sil_run + 1) * hop
    pulled = (off + lead_sp) - voice_start
    if pulled < 0.25:
        return syl

    # 往后合并被挤扁的若干字（至少前 4 个字，或直到遇到明显偏短字结束）一起按能量谷重新落位
    end_idx = min(len(syl) - 1, 3)
    while end_idx + 1 < len(syl) - 1 and (syl[end_idx + 1][1] - syl[end_idx + 1][0]) < 0.16:
        end_idx += 1
    end_t = off + syl[end_idx][1]
    n_re = end_idx + 1

    splits = _find_energy_valleys(env, hop, voice_start, end_t, n_re - 1, min_gap=0.18)
    if not splits:
        step = (end_t - voice_start) / n_re
        splits = [voice_start + step * (m + 1) for m in range(n_re - 1)]
    bounds = [voice_start] + splits + [end_t]
    out = list(syl)
    for m in range(n_re):
        out[m] = (bounds[m] - off, bounds[m + 1] - off, syl[m][2])
    return out


def _split_internal_silence(syl: list[tuple[float, float, str]], off: float,
                            env: np.ndarray | None, hop: float = 0.02) -> list[tuple[float, float, str]]:
    """当句中某个非首字（>=0.70s）内部横跨持续 >=0.22s 的彻底静音（如换气停顿），
    把静音前的尾音归还给前一个字，把当前字起点推到静音结束后真正出声处。"""
    if len(syl) < 2 or env is None or len(env) == 0:
        return syl

    out = [list(w) for w in syl]
    min_sil_frames = max(2, int(0.22 / hop))
    for j in range(1, len(out)):
        s_rel, e_rel, _ = out[j]
        if (e_rel - s_rel) < 0.70:
            continue
        abs_s = off + s_rel
        abs_e = off + e_rel
        i0 = max(0, int(abs_s / hop))
        i1 = min(len(env), int(abs_e / hop))
        if i1 - i0 < min_sil_frames + 8:
            continue

        # 找区间内最长的连续彻底静音段（RMS < 0.018）
        best_s, best_e = -1, -1
        cur_s = -1
        for k in range(i0, i1):
            if env[k] < 0.018:
                if cur_s < 0:
                    cur_s = k
            else:
                if cur_s >= 0:
                    if (k - cur_s) > (best_e - best_s):
                        best_s, best_e = cur_s, k
                    cur_s = -1
        if cur_s >= 0 and (i1 - cur_s) > (best_e - best_s):
            best_s, best_e = cur_s, i1

        if (best_e - best_s) < min_sil_frames:
            continue

        sil_s = best_s * hop
        sil_e = best_e * hop
        # 静音后必须仍有足够长且响亮的真正唱声（>=0.14s, 峰值 >=0.05）
        if (abs_e - sil_e) < 0.14:
            continue
        after_peak = float(env[best_e:i1].max()) if i1 > best_e else 0.0
        if after_peak < 0.05:
            continue

        # 静音前归还给前一个字，静音后作为当前字真正起点
        if sil_s > off + out[j - 1][0] + 0.08:
            out[j - 1][1] = sil_s - off
        out[j][0] = sil_e - off

    return [(s, e, w) for s, e, w in out]


def _spectral_flux_and_dip(wav_16k: np.ndarray, hop_s: float = 0.02) -> tuple[np.ndarray, np.ndarray, np.ndarray]:
    """计算每帧（20ms）的 RMS 包络、能量谷下陷度、对数频谱通量起音（Spectral Flux）。"""
    sr = 16000
    hop = int(sr * hop_s)
    env = au.rms_envelope(wav_16k, sr=sr, hop=hop_s)
    n_frames = len(env)
    if n_frames < 4:
        z = np.zeros(n_frames, dtype=np.float32)
        return env, z, z

    # 1) RMS 相对下陷度（dip）：比左右 0.14s 内较低峰值低的比例
    win = max(2, int(0.14 / hop_s))
    dip = np.zeros(n_frames, dtype=np.float32)
    for k in range(1, n_frames - 1):
        v = float(env[k])
        lp = float(env[max(0, k - win): k].max())
        rp = float(env[k + 1: min(n_frames, k + 1 + win)].max())
        side = min(lp, rp)
        if side >= 0.03 and v < side:
            dip[k] = (side - v) / (side + 1e-6)

    # 2) 对数频谱通量（Spectral Flux）：捕捉换字瞬间的辅音/共振峰突变
    n_fft = 1024
    hann = np.hanning(n_fft).astype(np.float32)
    padded = np.pad(wav_16k, (n_fft // 2, n_fft // 2))
    mags = []
    for k in range(n_frames):
        s = k * hop
        frame = padded[s: s + n_fft]
        if len(frame) < n_fft:
            frame = np.pad(frame, (0, n_fft - len(frame)))
        spec = np.abs(np.fft.rfft(frame * hann))
        mags.append(np.log1p(spec * 10.0))
    log_mag = np.stack(mags, axis=0)  # (T, F)
    # 只取人声主频段（150Hz ~ 5500Hz）
    freqs = np.fft.rfftfreq(n_fft, d=1.0 / sr)
    band = (freqs >= 150.0) & (freqs <= 5500.0)
    diff = np.maximum(0.0, np.diff(log_mag[:, band], axis=0, prepend=log_mag[:1, band]))
    flux = diff.mean(axis=1).astype(np.float32)
    p95 = float(np.percentile(flux, 95)) if len(flux) else 1.0
    if p95 > 1e-6:
        flux = np.clip(flux / p95, 0.0, 1.5)
    return env, dip, flux


def align_syls_waveform_first(syl: list[tuple[float, float, str]], off: float,
                              voc_16k: np.ndarray, seg_end: float,
                              hop_s: float = 0.02) -> list[tuple[float, float, str]]:
    """鬼畜 / 波形起音优先对齐模式：
    先按人声轨波形定死有声起止区间，再用「频谱通量起音 + RMS 能量谷 + 节拍平滑 DP」
    把 N 个音节直接贴到人声起音点上，SOFA 仅作软锚点参考。"""
    n_words = len(syl)
    if n_words == 0 or len(voc_16k) == 0:
        return syl

    sr = 16000
    s_idx = max(0, int(off * sr))
    e_idx = min(len(voc_16k), int(seg_end * sr))
    if e_idx - s_idx < int(0.25 * sr):
        return syl
    seg_wav = voc_16k[s_idx:e_idx]

    env, dip, flux = _spectral_flux_and_dip(seg_wav, hop_s=hop_s)
    T = len(env)
    if T < 6:
        return syl

    # 1) 定位本句真正有声区间 [f_start, f_end]
    p85 = float(np.percentile(env, 85))
    sil_th = min(0.045, max(0.020, p85 * 0.20))

    # 找第一个连续 >=2 帧高于静音阈值的起唱点
    f_start = 0
    for k in range(T - 1):
        if env[k] >= sil_th and env[k + 1] >= sil_th * 0.85:
            f_start = max(0, k)
            break

    # 找有效人声的自然收尾点：若唱完后出现持续 >=1.0s 的彻底低能量静音（如进间奏），
    # 直接截断在静音开始处，避免把后方间奏里的伴奏残留吸进末字拉长 4~5 秒
    tail_th = sil_th * 0.75
    f_end = T - 1
    voiced_seen = 0
    sil_run = 0
    sil_start = -1
    min_voiced_f = max(4, int(0.40 / hop_s))
    min_gap_f = max(6, int(1.00 / hop_s))
    for k in range(f_start, T):
        if env[k] >= sil_th:
            voiced_seen += 1
            sil_run = 0
            sil_start = -1
        elif voiced_seen >= min_voiced_f and env[k] < tail_th:
            if sil_run == 0:
                sil_start = k
            sil_run += 1
            if sil_run >= min_gap_f:
                f_end = min(T - 1, sil_start + 2)
                break
        elif voiced_seen >= min_voiced_f:
            sil_run = 0
            sil_start = -1
    if f_end == T - 1:
        for k in range(T - 1, f_start, -1):
            if env[k] >= tail_th:
                f_end = min(T - 1, k + 2)
                break

    if f_end - f_start < max(4, n_words * 3):
        f_start, f_end = 0, T - 1

    if n_words == 1:
        return [(f_start * hop_s, (f_end + 1) * hop_s, syl[0][2])]

    # 2) 构建每帧换字边界得分 = 频谱起音 + 能量谷 + 静音后起音加分 + 合理 SOFA 软锚点
    b_score = 0.55 * flux + 0.45 * dip

    # 句中彻底静音结束后的重新起音点（换气后的下半句开头）给强加分，
    # 而落在彻底静音内部的帧给负分，确保新字起点落在静音结束后而非静音开头
    in_sil = 0
    for k in range(f_start, f_end + 1):
        if env[k] < 0.022:
            b_score[k] -= 0.9
            in_sil += 1
        else:
            if in_sil >= int(0.16 / hop_s):
                b_score[k] += 1.6
            in_sil = 0

    # SOFA 软锚点（按字序号 m 精确对应，绝不跨字错位白嫖加分）：
    # 仅对时长合理的正常边界给加分，坍塌（<0.10s）或超长漂移（>0.80s）的边界完全交由波形起音与能量谷决定
    normal_sofa_durs = [e - s for s, e, _ in syl[:-1] if 0.12 <= (e - s) <= 0.75]
    anchor_bonus = np.zeros((n_words, T), dtype=np.float32)
    for j in range(1, n_words):
        d_prev = syl[j - 1][1] - syl[j - 1][0]
        d_cur = syl[j][1] - syl[j][0]
        if 0.12 <= d_prev <= 0.75 and 0.12 <= d_cur <= 0.95:
            fk = int(round(syl[j][0] / hop_s))
            for dk in range(-2, 3):
                kk = fk + dk
                if f_start < kk < f_end:
                    anchor_bonus[j, kk] += 0.85 * (1.0 - abs(dk) / 3.0)

    # 3) DP 在 [f_start, f_end] 内选最优的 n_words - 1 个切分点
    # 估算单字均长时扣除末字常见拖腔（避免虚高 avg_f 把前几个字拉长导致整句慢一拍）
    span_f = f_end - f_start
    if len(normal_sofa_durs) >= max(2, n_words // 3):
        avg_f = float(np.clip(np.median(normal_sofa_durs) / hop_s, 0.20 / hop_s, 0.45 / hop_s))
    else:
        tail_reserve = min(0.9 / hop_s, span_f * 0.22) if n_words >= 4 else 0.0
        avg_f = max(4.0, (span_f - tail_reserve) / float(max(1, n_words - 1)))
    min_f = max(4, int(round(min(0.18 / hop_s, avg_f * 0.42))))
    if min_f * n_words >= span_f:
        min_f = max(2, span_f // n_words)
    max_f = max(min_f + 4, int(round(max(0.85 / hop_s, avg_f * 2.2))))

    # 统计静音前缀和与有效有声帧前缀和：
    # 1) 严禁把任何字完整塞进彻底静音段（有声帧数过少直接重罚）
    # 2) 半句换气静音允许由静音前那个字吸收（后续 _split_internal_silence 会把静音切给空格），
    #    因此把 max_f 放宽到足以跨过句中换气停顿
    sil_prefix = np.zeros(T + 1, dtype=np.int32)
    sil_prefix[1:] = np.cumsum((env < 0.018).astype(np.int32))
    voiced_prefix = np.zeros(T + 1, dtype=np.int32)
    voiced_prefix[1:] = np.cumsum((env >= 0.032).astype(np.int32))
    sil_pen_frames = max(3, int(0.20 / hop_s))
    max_f = max(max_f, int(round(1.90 / hop_s)))

    NEG = -1e9
    # dp[m, k]: 第 m 个边界（m = 1..n_words-1）落在帧 k 的最大得分
    dp = np.full((n_words, T), NEG, dtype=np.float32)
    parent = np.full((n_words, T), -1, dtype=np.int32)

    # m = 1（第 1 个字结束 / 第 2 个字开始）
    lo1 = f_start + min_f
    hi1 = min(f_end - min_f * (n_words - 1), f_start + max_f)
    for k in range(lo1, hi1 + 1):
        d = k - f_start
        n_sil = sil_prefix[k] - sil_prefix[f_start]
        d_eff = max(1, d - n_sil)
        pen = 0.20 * ((d_eff - avg_f) / max(avg_f, 1.0)) ** 2
        if (voiced_prefix[k] - voiced_prefix[f_start]) < 2:
            pen += 6.0
        elif n_sil >= sil_pen_frames and env[k - 1] >= 0.032:
            pen += 2.5
        dp[1, k] = b_score[k] + anchor_bonus[1, k] - pen
        parent[1, k] = f_start

    for m in range(2, n_words):
        lo_k = f_start + min_f * m
        hi_k = f_end - min_f * (n_words - m)
        for k in range(lo_k, hi_k + 1):
            p_lo = max(f_start + min_f * (m - 1), k - max_f)
            p_hi = k - min_f
            best_v = NEG
            best_p = -1
            for p in range(p_lo, p_hi + 1):
                pv = dp[m - 1, p]
                if pv <= NEG / 2:
                    continue
                d = k - p
                n_sil = sil_prefix[k] - sil_prefix[p]
                d_eff = max(1, d - n_sil)
                pen = 0.20 * ((d_eff - avg_f) / max(avg_f, 1.0)) ** 2
                if (voiced_prefix[k] - voiced_prefix[p]) < 2:
                    pen += 6.0
                elif n_sil >= sil_pen_frames and env[k - 1] >= 0.032:
                    pen += 2.5
                val = pv + b_score[k] + anchor_bonus[m, k] - pen
                if val > best_v:
                    best_v = val
                    best_p = p
            dp[m, k] = best_v
            parent[m, k] = best_p

    # 选最后一个边界（末字允许拖腔，只要求 >= min_f 且不跨静音）
    best_last_v = NEG
    best_last_k = -1
    lo_last = f_start + min_f * (n_words - 1)
    hi_last = f_end - min_f
    for k in range(lo_last, hi_last + 1):
        v = dp[n_words - 1, k]
        if v <= NEG / 2:
            continue
        d_last = f_end - k
        # 末字允许长拖音（不惩罚偏长，仅轻度惩罚过短）
        pen = 0.15 * max(0.0, (avg_f - d_last) / max(avg_f, 1.0)) ** 2
        if v - pen > best_last_v:
            best_last_v = v - pen
            best_last_k = k

    if best_last_k < 0:
        step = (f_end - f_start) / float(n_words)
        bounds_f = [int(round(f_start + step * i)) for i in range(n_words)] + [f_end]
    else:
        cuts = [best_last_k]
        cur = best_last_k
        for m in range(n_words - 1, 1, -1):
            cur = int(parent[m, cur])
            cuts.append(cur)
        cuts.reverse()
        bounds_f = [f_start] + cuts + [f_end]

    out = []
    for i in range(n_words):
        out.append((bounds_f[i] * hop_s, bounds_f[i + 1] * hop_s, syl[i][2]))
    return out


def build_lrc(meta: list[dict], words_dir: Path, ti: str = "", ar: str = "",
              al: str = "", by: str = "SOFA 歌声对齐",
              vocal_wav: Path | None = None,
              waveform_first: bool = False) -> fmt.Lrc:
    """把逐音节时间贴回汉字，生成逐字 Lrc（含行尾标签、句间夹断）。"""
    env = None
    voc_16k = None
    if vocal_wav is not None and Path(vocal_wav).exists():
        try:
            voc_16k = au.decode(vocal_wav, 16000)
            env = au.rms_envelope(voc_16k, sr=16000, hop=0.02)
        except Exception:
            env = None
            voc_16k = None

    rows = []
    for m in meta:
        p = words_dir / f"{m['name']}.lab"
        if not p.exists():
            continue
        syl = [w for w in read_words(p) if w[2] not in ("SP", "AP")]
        if not syl:
            continue
        if waveform_first and voc_16k is not None:
            syl = align_syls_waveform_first(syl, m["offset"], voc_16k, m["seg_end"], hop_s=0.02)
            syl = _trim_leading_silence(syl, m["offset"], env, hop=0.02)
            syl = _split_internal_silence(syl, m["offset"], env, hop=0.02)
        else:
            syl = _fix_collapsed_syllables(syl)
            syl = _pull_back_voiced_sp(syl, m["offset"], env, hop=0.02)
            syl = _trim_leading_silence(syl, m["offset"], env, hop=0.02)
            syl = _refine_stretched_syllables(syl, m["offset"], env, hop=0.02)
            syl = _split_internal_silence(syl, m["offset"], env, hop=0.02)
        off = m["offset"]
        chars, k = [], 0
        tag_mask = _speaker_tag_mask(m["text"])
        tok_set = set(m["tok_idx"]) if "tok_idx" in m else None
        first_t = off + syl[0][0]
        for ci, ch in enumerate(m["text"]):
            if tag_mask[ci]:
                t_tag = first_t if k == 0 else (off + syl[min(k - 1, len(syl) - 1)][1])
                chars.append((ch, t_tag))
            elif (tok_set is not None and ci in tok_set) or (tok_set is None and HAN.match(ch)):
                if k < len(syl):
                    chars.append((ch, off + syl[k][0]))
                k += 1
            else:
                # 空格/标点放在前一个汉字的「结束时间」(syl[k-1][1])，不要放起点！
                # 否则前一个字时长变 0ms，整段拖音都被空格吞掉。
                if k > 0 and k - 1 < len(syl):
                    t_non = off + syl[k - 1][1]
                elif chars:
                    t_non = chars[-1][1]
                else:
                    t_non = first_t
                chars.append((ch, t_non))
        if not chars:
            continue
        end = off + syl[min(len(syl), k) - 1][1] if syl else chars[-1][1] + 0.1
        # 记录倒数第二个音节的结束时间，用于检测末字是否被 SOFA 吸到下一句（建在 nxt+0.9 切片尾）
        prev_syl_end = (off + syl[k - 2][1]) if (k >= 2 and k - 2 < len(syl)) else None
        rows.append((fmt.Line(chars[0][1], m["text"], chars, end), prev_syl_end))

    # 逐句独立对齐，句尾可能压到下一句
    out_rows = [r[0] for r in rows]
    for i, (ln, prev_syl_end) in enumerate(rows):
        cap = (out_rows[i + 1].chars[0][1] - 0.02) if i + 1 < len(out_rows) else None
        if cap is not None:
            # 若末字被 SOFA 吸进了下一句（起点 >= cap - 0.05 且与倒数第二字尾存在 >0.45s 异常断层），
            # 把末字拉回倒数第二字结束处，避免「倒数第二字拉超长、最后一字贴到下一句开头」
            last_han_idx = next((j for j in range(len(ln.chars) - 1, -1, -1) if HAN.match(ln.chars[j][0])), -1)
            if (last_han_idx >= 1 and prev_syl_end is not None
                    and ln.chars[last_han_idx][1] >= cap - 0.05
                    and cap - prev_syl_end > 0.45):
                # 估一个正常音节时长（取本句前序汉字间距的中位数，兜底 0.35s）
                gaps = [ln.chars[j + 1][1] - ln.chars[j][1]
                        for j in range(last_han_idx - 1)
                        if ln.chars[j + 1][1] - ln.chars[j][1] > 0.05]
                med = sorted(gaps)[len(gaps) // 2] if gaps else 0.35
                med = min(max(med, 0.20), 0.55)
                new_start = min(prev_syl_end, cap - 0.12)
                for j in range(last_han_idx, len(ln.chars)):
                    ln.chars[j] = (ln.chars[j][0], new_start)
                ln.end = min(cap, new_start + med)

            ln.chars = [(c, min(t, cap)) for c, t in ln.chars]
            ln.end = min(ln.end, cap)
        prev = -10.0
        fixed = []
        for c, t in ln.chars:
            t = max(t, prev)
            fixed.append((c, t))
            prev = t
        ln.chars = fixed
        ln.start = fixed[0][1]
        ln.end = max(ln.end, fixed[-1][1] + 0.1)

    metas = [x for x in (f"[ti:{ti}]" if ti else "", f"[ar:{ar}]" if ar else "",
                         f"[al:{al}]" if al else "", f"[by:{by}]") if x]
    return fmt.Lrc(metas, out_rows)
