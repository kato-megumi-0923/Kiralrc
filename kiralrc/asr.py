"""faster-whisper 识别 + 把歌词行粗略分配到 ASR 分段（给 SOFA 提供每句起点）。"""
from __future__ import annotations

import ctypes
import difflib
import os
import sys

import numpy as np

from . import config

_LCMAP_SIMPLIFIED_CHINESE = 0x02000000
_LC_MAP_FN = None


def to_simplified(text: str) -> str:
    """将中文文本统一转为简体中文（优先 Windows 原生 LCMapStringEx，零外部依赖）。"""
    if not text or sys.platform != "win32":
        return text
    global _LC_MAP_FN
    if _LC_MAP_FN is None:
        try:
            from ctypes import wintypes
            fn = ctypes.windll.kernel32.LCMapStringEx
            fn.argtypes = [
                wintypes.LPCWSTR, wintypes.DWORD, wintypes.LPCWSTR, ctypes.c_int,
                wintypes.LPWSTR, ctypes.c_int, ctypes.c_void_p, ctypes.c_void_p, ctypes.c_size_t,
            ]
            fn.restype = ctypes.c_int
            _LC_MAP_FN = fn
        except Exception:
            _LC_MAP_FN = False
    if not _LC_MAP_FN:
        return text
    n = _LC_MAP_FN("zh-CN", _LCMAP_SIMPLIFIED_CHINESE, text, -1, None, 0, None, None, 0)
    if n <= 0:
        return text
    buf = ctypes.create_unicode_buffer(n)
    if _LC_MAP_FN("zh-CN", _LCMAP_SIMPLIFIED_CHINESE, text, -1, buf, n, None, None, 0) <= 0:
        return text
    return buf.value

# ct2 的 CUDA 后端需要 cublas/cudnn，借 torch 自带的 dll 目录
_TORCH_LIB = None


def _prepare_dll() -> None:
    global _TORCH_LIB
    if _TORCH_LIB is not None:
        return
    import torch

    _TORCH_LIB = os.path.join(os.path.dirname(torch.__file__), "lib")
    if os.path.isdir(_TORCH_LIB):
        os.add_dll_directory(_TORCH_LIB)
        os.environ["PATH"] = _TORCH_LIB + os.pathsep + os.environ.get("PATH", "")


def transcribe(audio: np.ndarray, lang: str = "zh", device: str | None = None,
               prompt: str | None = None):
    """返回 (words, segments)。words: [{w,s,e,p}]"""
    _prepare_dll()
    from faster_whisper import WhisperModel

    if device is None:
        import torch
        device = "cuda" if torch.cuda.is_available() else "cpu"
    model_path = str(config.path("whisper_model"))
    model = WhisperModel(model_path, device=device, compute_type="float16" if device == "cuda" else "int8")

    if lang == "zh" and not prompt:
        prompt = "以下是普通话简体中文歌词。"

    segments, info = model.transcribe(
        audio, language=lang, word_timestamps=True, vad_filter=False, beam_size=5,
        temperature=[0.0, 0.2, 0.4, 0.6, 0.8, 1.0], condition_on_previous_text=False,
        hallucination_silence_threshold=2.0, no_speech_threshold=0.6,
        compression_ratio_threshold=2.4, initial_prompt=prompt,
    )

    words, segs = [], []
    for seg in segments:
        txt = to_simplified(seg.text) if lang == "zh" else seg.text
        segs.append({"start": seg.start, "end": seg.end, "text": txt})
        for w in seg.words or []:
            tok = w.word.strip()
            if tok:
                if lang == "zh":
                    tok = to_simplified(tok)
                words.append({"w": tok, "s": round(w.start, 3), "e": round(w.end, 3),
                              "p": round(w.probability, 3)})
    return words, segs, info.duration


_FIRERED_CACHE: dict[str, dict] = {}


def firered_available() -> bool:
    try:
        p = config.path("firered_model")
        return (p / "model.pth.tar").exists() and (p / "cmvn.ark").exists()
    except Exception:
        return False


def _load_firered_bundle(device: str | None = None) -> dict:
    import torch
    from fireredasr.data.asr_feat import ASRFeatExtractor
    from fireredasr.models.fireredasr_aed import FireRedAsrAed
    from fireredasr.tokenizer.aed_tokenizer import ChineseCharEnglishSpmTokenizer

    dev = device or ("cuda" if torch.cuda.is_available() else "cpu")
    model_dir = config.path("firered_model")
    key = f"{model_dir.resolve()}::{dev}"
    if key in _FIRERED_CACHE:
        return _FIRERED_CACHE[key]

    feat_extractor = ASRFeatExtractor(str(model_dir / "cmvn.ark"))
    pkg = torch.load(str(model_dir / "model.pth.tar"), map_location="cpu", weights_only=False)
    model = FireRedAsrAed.from_args(pkg["args"])
    model.load_state_dict(pkg["model_state_dict"], strict=True)
    model = model.to(dev).eval()
    tokenizer = ChineseCharEnglishSpmTokenizer(
        str(model_dir / "dict.txt"),
        str(model_dir / "train_bpe1000.model"),
    )
    bundle = {
        "device": dev,
        "feat": feat_extractor,
        "model": model,
        "tok": tokenizer,
    }
    _FIRERED_CACHE[key] = bundle
    return bundle


def _split_vocal_phrases(
    audio: np.ndarray,
    sr: int = 16000,
    hop: float = 0.02,
    min_silence_sec: float = 0.45,
    min_phrase_sec: float = 0.40,
    max_phrase_sec: float = 11.0,
    pad_sec: float = 0.12,
) -> list[dict]:
    """在 Demucs 分离后的纯人声轨上按 RMS 能量包络切分乐句（避免语音 VAD 误杀拖长音歌声）。"""
    from . import audio as au

    dur = len(audio) / float(sr)
    env = au.rms_envelope(audio, sr=sr, hop=hop)
    if len(env) == 0:
        return []
    p90 = float(np.percentile(env, 90))
    thr = max(0.010, p90 * 0.10)
    active = env > thr

    # 桥接短于 min_silence_sec 的换气静音
    min_sil_frames = max(1, int(round(min_silence_sec / hop)))
    n = len(active)
    i = 0
    while i < n:
        if not active[i]:
            j = i
            while j < n and not active[j]:
                j += 1
            if i > 0 and j < n and (j - i) < min_sil_frames:
                active[i:j] = True
            i = j
        else:
            i += 1

    # 提取连续人声区间
    raw_spans: list[tuple[int, int]] = []
    i = 0
    while i < n:
        if active[i]:
            j = i
            while j < n and active[j]:
                j += 1
            if (j - i) * hop >= min_phrase_sec:
                raw_spans.append((i, j))
            i = j
        else:
            i += 1

    # 对超过 max_phrase_sec 的长乐段，在中间能量最低谷处切开
    max_frames = int(round(max_phrase_sec / hop))
    margin_frames = max(1, int(round(2.5 / hop)))

    def _split_long(f0: int, f1: int) -> list[tuple[int, int]]:
        if f1 - f0 <= max_frames:
            return [(f0, f1)]
        lo = f0 + margin_frames
        hi = f1 - margin_frames
        if hi <= lo:
            mid = (f0 + f1) // 2
        else:
            mid = lo + int(np.argmin(env[lo:hi]))
        return _split_long(f0, mid) + _split_long(mid, f1)

    phrases: list[dict] = []
    for f0, f1 in raw_spans:
        for a0, a1 in _split_long(f0, f1):
            s = max(0.0, a0 * hop - pad_sec)
            e = min(dur, a1 * hop + pad_sec)
            if e - s >= min_phrase_sec:
                phrases.append({"start": round(s, 3), "end": round(e, 3)})
    return phrases


def transcribe_zh_firered(audio: np.ndarray, device: str | None = None, batch_size: int = 8):
    """使用人声能量包络按乐句切分 + FireRedASR-AED-L 中文专精模型听写纯人声。

    返回与 transcribe() 一致的 (words, segs, duration)。
    """
    import torch

    dur = len(audio) / 16000.0
    bundle = _load_firered_bundle(device=device)
    dev = bundle["device"]

    speech_ts = _split_vocal_phrases(audio, sr=16000)
    if not speech_ts:
        return [], [], dur

    feat_ext = bundle["feat"]
    model = bundle["model"]
    tok = bundle["tok"]

    segs: list[dict] = []
    words: list[dict] = []

    with torch.no_grad():
        for b0 in range(0, len(speech_ts), batch_size):
            batch_ts = speech_ts[b0:b0 + batch_size]
            feats_list = []
            valid_ts = []
            for st in batch_ts:
                s_idx = max(0, int(st["start"] * 16000))
                e_idx = min(len(audio), int(st["end"] * 16000))
                if e_idx - s_idx < 1600:
                    continue
                seg_i16 = np.clip(audio[s_idx:e_idx] * 32768.0, -32768, 32767).astype(np.int16)
                fb = feat_ext.fbank((16000, seg_i16))
                if fb.shape[0] < 4:
                    continue
                if feat_ext.cmvn is not None:
                    fb = feat_ext.cmvn(fb)
                feats_list.append(torch.from_numpy(fb).float())
                valid_ts.append(st)
            if not feats_list:
                continue

            lengths = torch.tensor([f.size(0) for f in feats_list], dtype=torch.long)
            feats_pad = feat_ext.pad_feat(feats_list, 0.0)
            if dev == "cuda":
                feats_pad, lengths = feats_pad.cuda(), lengths.cuda()

            hyps = model.transcribe(
                feats_pad,
                lengths,
                beam_size=3,
                nbest=1,
                decode_max_len=0,
                softmax_smoothing=1.25,
                length_penalty=0.6,
                eos_penalty=1.0,
            )
            for st, hyp_list in zip(valid_ts, hyps):
                if not hyp_list:
                    continue
                hyp_ids = [int(x) for x in hyp_list[0]["yseq"].cpu()]
                text = to_simplified(tok.detokenize(hyp_ids).strip())
                if not text:
                    continue
                s_sec = round(float(st["start"]), 3)
                e_sec = round(float(st["end"]), 3)
                segs.append({"start": s_sec, "end": e_sec, "text": text})
                words.append({"w": text, "s": s_sec, "e": e_sec, "p": 1.0})

    return words, segs, dur


def _coverage(line: str, seg_text: str) -> float:
    lc = [c for c in line if c.strip()]
    sc = [c for c in seg_text if c.strip()]
    if not lc or not sc:
        return 0.0
    sm = difflib.SequenceMatcher(None, sc, lc, autojunk=False)
    hit = sum(n for _, _, n in sm.get_matching_blocks() if n)
    return hit / len(lc)


def assign_lines_detail(
    lines: list[str],
    segments: list[dict],
    min_coverage: float | None = None,
) -> tuple[list[tuple[float, str]], list[dict]]:
    """全局对齐（DTW / 带跳行状态的单调 DP），返回 (已分配行, 被剔除的低覆盖率行)。

    KI-2：当音频是 TV size / 剪辑版（跳过了某段主歌）时，被剪掉的歌词行在周围分段的
    覆盖率低于 min_coverage，DP 会走「跳行」分支将其剔除，而不是硬塞进相邻分段。
    """
    if not segments or not lines:
        return [], []
    if min_coverage is None:
        min_coverage = float(config.opt("min_line_coverage", 0.22))

    n_seg, n_line = len(segments), len(lines)
    cov = []
    for l in lines:
        row = []
        for j, s in enumerate(segments):
            c = _coverage(l, s["text"])
            if j + 1 < n_seg:
                c = max(c, _coverage(l, s["text"] + segments[j + 1]["text"]) * 0.95)
            row.append(c)
        cov.append(row)

    max_cov = max((max(r) for r in cov), default=0.0)
    # 若整首歌覆盖率都极低（例如跨语种或无文本分段），则不做剔除兜底分配
    allow_skip = max_cov >= min_coverage and min_coverage > 0.0
    skip_reward = min_coverage - 1e-4 if allow_skip else -1e9
    crowd_penalty = 0.12

    NEG = -1e9
    dp = [[NEG] * n_seg for _ in range(n_line)]
    # parent[i][j] = (prev_j, matched: bool)
    parent: list[list[tuple[int, bool]]] = [[(-1, True)] * n_seg for _ in range(n_line)]

    for j in range(n_seg):
        if cov[0][j] >= skip_reward:
            dp[0][j] = cov[0][j]
            parent[0][j] = (j, True)
        else:
            dp[0][j] = skip_reward
            parent[0][j] = (j, False)

    for i in range(1, n_line):
        best_prev, arg_prev = NEG, 0
        for j in range(n_seg):
            # 1) 跳行（该行在剪辑版音频中被剪掉）
            best_here = dp[i - 1][j] + skip_reward
            p_here = (j, False)

            # 2) 同段复用（两句歌词落在同一个 ASR 分段内，加轻微拥挤惩罚）
            same_seg = dp[i - 1][j] + cov[i][j] - crowd_penalty
            if same_seg >= best_here:
                best_here = same_seg
                p_here = (j, True)

            # 3) 从更早的分段推进到当前分段 j（同分时取更靠后的段，避免重复句挤在一起）
            if best_prev > NEG / 2:
                adv = best_prev + cov[i][j]
                if adv >= best_here:
                    best_here = adv
                    p_here = (arg_prev, True)

            if dp[i - 1][j] >= best_prev:
                best_prev, arg_prev = dp[i - 1][j], j

            dp[i][j] = best_here
            parent[i][j] = p_here

    j = max(range(n_seg), key=lambda k: dp[n_line - 1][k])
    choices: list[tuple[int, bool]] = []
    for i in range(n_line - 1, -1, -1):
        prev_j, matched = parent[i][j]
        choices.append((j, matched))
        j = prev_j
    choices.reverse()

    out: list[tuple[float, str]] = []
    skipped: list[dict] = []
    for idx, (line, (seg_idx, matched)) in enumerate(zip(lines, choices)):
        c = cov[idx][seg_idx]
        if not matched or (allow_skip and c < min_coverage):
            skipped.append({"index": idx + 1, "text": line, "coverage": round(c, 3)})
            continue
        seg = segments[seg_idx]
        start = max(0.0, float(seg["start"]))
        if out and start <= out[-1][0] + 0.2:
            start = out[-1][0] + 0.3
        out.append((round(start, 3), line))

    return out, skipped


def assign_lines(lines: list[str], segments: list[dict],
                 min_coverage: float | None = None) -> list[tuple[float, str]]:
    """把歌词行单调分配给 ASR 分段（行级 DP），自动剔除剪辑版缺失行，返回 [(start, line)]。"""
    out, _ = assign_lines_detail(lines, segments, min_coverage=min_coverage)
    return out
