"""音频解码与人声分离。

注意：`av` 的 packed 格式（layout=stereo + format="flt"）用 to_ndarray() 拿到的是
(1, n*ch) 交错数组，直接 concat 会让时长翻倍；所以立体声一律用 "fltp"。
"""
from __future__ import annotations

import os
import wave
from pathlib import Path

import numpy as np


def decode(path: str | Path, sr: int = 16000) -> np.ndarray:
    """解成单声道 float32，长度 = 秒数 * sr。"""
    import av

    with av.open(str(path), mode="r") as container:
        resampler = av.AudioResampler(format="flt", layout="mono", rate=sr)
        chunks = []
        for frame in container.decode(audio=0):
            for out in resampler.resample(frame):
                chunks.append(out.to_ndarray())
        for out in resampler.resample(None):
            chunks.append(out.to_ndarray())
    if not chunks:
        raise RuntimeError(f"无法解码音频: {path}")
    audio = np.concatenate(chunks, axis=1)[0].astype(np.float32)
    return np.clip(audio, -1.0, 1.0)


def decode_stereo(path: str | Path, sr: int = 44100) -> np.ndarray:
    """解成立体声 float32，shape=(2, n)。"""
    import av

    with av.open(str(path), mode="r") as container:
        resampler = av.AudioResampler(format="fltp", layout="stereo", rate=sr)
        chunks = []
        for frame in container.decode(audio=0):
            for out in resampler.resample(frame):
                chunks.append(out.to_ndarray())
        for out in resampler.resample(None):
            chunks.append(out.to_ndarray())
    a = np.concatenate(chunks, axis=1)
    if a.shape[0] == 1:
        a = np.vstack([a, a])
    return np.clip(a, -1, 1).astype(np.float32)


def write_wav(path: str | Path, data: np.ndarray, sr: int = 44100) -> None:
    """data: (n,) 单声道 或 (2, n) 立体声。"""
    arr = data.T if data.ndim == 2 else data
    pcm = (np.clip(arr, -1, 1) * 32767).astype(np.int16)
    with wave.open(str(path), "wb") as w:
        w.setnchannels(1 if pcm.ndim == 1 else pcm.shape[1])
        w.setsampwidth(2)
        w.setframerate(sr)
        w.writeframes(pcm.tobytes())


def separate(path: str | Path, out_prefix: str | Path, model_name: str = "htdemucs",
             sr: int = 44100, device: str | None = None) -> dict[str, Path]:
    """demucs 分离，返回 {stem: wav 路径}。权重首次运行会下载（需能访问 fbaipublicfiles）。"""
    import torch
    from demucs.apply import apply_model
    from demucs.pretrained import get_model

    out_prefix = Path(out_prefix)
    out_prefix.parent.mkdir(parents=True, exist_ok=True)

    wav = decode_stereo(path, sr)
    model = get_model(model_name)
    model.eval()
    device = device or ("cuda" if torch.cuda.is_available() else "cpu")

    ref = wav.mean(0)
    x = torch.from_numpy((wav - ref.mean()) / (ref.std() + 1e-8))
    with torch.no_grad():
        src = apply_model(model, x[None], device=device, shifts=1, split=True,
                          overlap=0.25, progress=False)[0]
    src = src * ref.std() + ref.mean()

    result = {}
    for i, name in enumerate(model.sources):
        p = out_prefix.with_name(f"{out_prefix.name}_{name}.wav")
        write_wav(p, src[i].numpy(), sr)
        result[name] = p
    return result


def duration(path: str | Path) -> float:
    return len(decode(path, 8000)) / 8000


def rms_envelope(audio: np.ndarray, sr: int = 16000, hop: float = 0.02) -> np.ndarray:
    """按 hop 秒切帧的 RMS 包络，用于起音检测/校验。"""
    n = int(hop * sr)
    m = len(audio) // n
    if m == 0:
        return np.zeros(0)
    return np.sqrt((audio[: m * n].reshape(m, n) ** 2).mean(axis=1))


def vocal_activity(audio_or_path: np.ndarray | str | Path, sr: int = 16000,
                   min_rms: float | None = None,
                   min_mid_ratio: float | None = None) -> dict:
    """检测人声轨是否存在有效主唱（KI-1：筛除纯音乐/伴奏/无词低能量轨）。

    判定准则：
    1. 人声轨 P90 RMS >= min_rms（默认 0.008）
    2. 1~4kHz 歌声共振峰频段能量占总能量比例 >= min_mid_ratio（默认 0.08）
    """
    from . import config

    if min_rms is None:
        min_rms = float(config.opt("min_vocal_rms", 0.008))
    if min_mid_ratio is None:
        min_mid_ratio = float(config.opt("min_vocal_ratio", 0.08))

    if isinstance(audio_or_path, (str, Path)):
        audio = decode(audio_or_path, sr=sr)
    else:
        audio = np.asarray(audio_or_path, dtype=np.float32)

    env = rms_envelope(audio, sr=sr, hop=0.02)
    if len(env) == 0:
        return {"has_vocal": False, "rms_p90": 0.0, "mid_ratio": 0.0,
                "reason": "音频为空或过短"}

    rms_p90 = float(np.percentile(env, 90))

    win = min(len(audio), max(256, int(sr * 0.05)))
    n_frames = len(audio) // win
    if n_frames == 0:
        mid_ratio = 0.0
    else:
        frames = audio[: n_frames * win].reshape(n_frames, win)
        hann = np.hanning(win).astype(np.float32)
        power = np.abs(np.fft.rfft(frames * hann, axis=1)) ** 2
        freqs = np.fft.rfftfreq(win, d=1.0 / sr)
        valid = freqs >= 40.0
        mid = (freqs >= 1000.0) & (freqs <= 4000.0)
        total_e = float(power[:, valid].sum())
        mid_e = float(power[:, mid].sum())
        mid_ratio = mid_e / total_e if total_e > 1e-12 else 0.0

    reasons = []
    if rms_p90 < min_rms:
        reasons.append(f"人声能量过低 RMS={rms_p90:.4f}<{min_rms}")
    if mid_ratio < min_mid_ratio:
        reasons.append(f"1-4kHz 占比 {mid_ratio:.3f}<{min_mid_ratio}")

    return {
        "has_vocal": len(reasons) == 0,
        "rms_p90": round(rms_p90, 4),
        "mid_ratio": round(mid_ratio, 4),
        "reason": "；".join(reasons) if reasons else "主唱正常",
    }


def find_valley_start(env: np.ndarray, hop: float, t0: float,
                      nxt: float, prev_end: float | None = None,
                      lead: float = 0.35) -> float:
    """按人声轨 RMS 波形定位本句左边界：若 t0 附近存在「上句尾音 → 换气静音谷 → 本句起唱」，
    直接从静音谷底开切，避免把上一句尾音切进本句导致首字抢拍/贴边坍塌。"""
    seg_start = max(0.0, t0 - lead)
    if prev_end is not None and prev_end < t0 + 0.35:
        seg_start = max(seg_start, prev_end - 0.05)
    if len(env) == 0 or (nxt - t0) < 1.5:
        return seg_start

    hi_t = min(nxt - 0.4, t0 + 0.85)
    f0 = max(0, int(seg_start / hop))
    f1 = min(len(env) - 1, int(hi_t / hop))
    if f1 - f0 < 8:
        return seg_start

    win = env[f0: f1 + 1]
    # 用 3 帧轻微平滑避免单帧毛刺干扰谷底定位
    if len(win) >= 5:
        smooth = np.convolve(win, np.ones(3, dtype=np.float32) / 3.0, mode="same")
    else:
        smooth = win

    min_rel = int(np.argmin(smooth))
    if min_rel < 2 or min_rel >= len(smooth) - 2:
        return seg_start

    head_peak = float(smooth[:min_rel].max())
    valley = float(smooth[min_rel])
    after_peak = float(smooth[min_rel + 1:].max())

    # 前段有明显上句尾音、中间跌入换气低谷、后段重新起唱
    if head_peak >= 0.05 and valley <= max(0.032, head_peak * 0.45) and after_peak >= max(0.04, valley * 1.6):
        return (f0 + min_rel) * hop
    return seg_start


def protect_reference_end(end: float, reference_end: float | None, start: float,
                          next_start: float, total: float) -> float:
    """Keep an explicit lyric tail in the slice, with room for the last phoneme."""
    cap = min(next_start, total)
    if reference_end is None or not start < reference_end <= cap:
        return end
    return max(end, min(reference_end + 0.08, cap))


def find_valley_end(env: np.ndarray, hop: float, t0: float, nxt: float,
                    eff_total: float, min_line_dur: float = 0.8) -> float:
    """按人声轨 RMS 波形定位本句右边界：在交界处寻找换气静音谷，
    避免把下一句开头人声切进当前切片导致末字被吸走。"""
    cap = min(eff_total, nxt + 0.15)
    lo_t = max(t0 + min_line_dur, nxt - 0.9)
    if lo_t >= cap - 0.1 or len(env) == 0:
        return cap
    i0 = max(0, int(lo_t / hop))
    i1 = min(len(env), int(cap / hop))
    if i1 - i0 < 6:
        return cap

    s0 = max(0, int(t0 / hop))
    s1 = min(len(env), max(s0 + 5, i0))
    ref_e = float(np.percentile(env[s0:s1], 80)) if s1 > s0 else float(np.percentile(env[i0:i1], 80))
    thresh = min(0.032, max(0.012, ref_e * 0.15))

    win = 6
    seg = env[i0:i1]
    smooth = np.convolve(seg, np.ones(win, dtype=np.float32) / win, mode="valid")
    if len(smooth) == 0:
        return cap
    min_val = float(smooth.min())
    if min_val <= thresh:
        quiet = np.where(smooth <= max(thresh, min_val * 1.5))[0]
        idx = int(quiet[-1]) + win // 2
        return min(cap, (i0 + idx) * hop)
    return cap
