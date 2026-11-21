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
