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
