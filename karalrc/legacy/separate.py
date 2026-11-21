"""demucs 人声分离（只为把埋在伴奏里的人声抠出来）。"""
import argparse
import os
import wave

import av
import numpy as np
import torch


def decode_stereo(path, sr=44100):
    with av.open(path, mode="r") as c:
        # 必须用 planar: packed 的 to_ndarray() 是 (1, n*ch) 交错, 直接 concat 会把时长翻倍
        rs = av.AudioResampler(format="fltp", layout="stereo", rate=sr)
        ch = []
        for fr in c.decode(audio=0):
            for o in rs.resample(fr):
                ch.append(o.to_ndarray())
        for o in rs.resample(None):
            ch.append(o.to_ndarray())
    a = np.concatenate(ch, axis=1)          # (2, n)
    if a.shape[0] == 1:
        a = np.vstack([a, a])
    return np.clip(a, -1, 1).astype(np.float32)


def write_wav(path, data, sr=44100):
    pcm = (np.clip(data.T, -1, 1) * 32767).astype(np.int16)
    with wave.open(path, "wb") as w:
        w.setnchannels(pcm.shape[1])
        w.setsampwidth(2)
        w.setframerate(sr)
        w.writeframes(pcm.tobytes())


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--audio", required=True)
    ap.add_argument("--out", required=True)
    ap.add_argument("--model", default="htdemucs")
    args = ap.parse_args()

    from demucs.apply import apply_model
    from demucs.pretrained import get_model

    print("[decode]", flush=True)
    wav = decode_stereo(args.audio)
    print(f"[decode] {wav.shape[1]/44100:.1f}s stereo", flush=True)

    model = get_model(args.model)
    model.eval()
    device = "cuda" if torch.cuda.is_available() else "cpu"
    print(f"[demucs] {args.model} on {device}", flush=True)

    ref = wav.mean(0)
    x = torch.from_numpy((wav - ref.mean()) / (ref.std() + 1e-8))
    with torch.no_grad():
        src = apply_model(model, x[None], device=device, shifts=1, split=True,
                          overlap=0.25, progress=True)[0]
    src = src * ref.std() + ref.mean()
    names = model.sources
    print("[stems]", names, flush=True)
    for i, n in enumerate(names):
        write_wav(os.path.join(args.out.replace(".wav", f"_{n}.wav")), src[i].numpy())
        print(f"[write] {n} rms={float(src[i].pow(2).mean().sqrt()):.4f}", flush=True)


if __name__ == "__main__":
    main()
