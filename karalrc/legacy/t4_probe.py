"""t4 参数实验：同一段音频换不同解码参数, 看能否抠出人声。"""
import os
import sys
import time

import av
import numpy as np
import torch

_TORCH_LIB = os.path.join(os.path.dirname(torch.__file__), "lib")
os.add_dll_directory(_TORCH_LIB)
os.environ["PATH"] = _TORCH_LIB + os.pathsep + os.environ.get("PATH", "")

from faster_whisper import WhisperModel

MODEL = os.path.join(os.path.dirname(os.path.abspath(__file__)), "models", "faster-whisper-large-v3")
AUDIO = r"C:\Users\yxljs\OneDrive\Music\情深深雨濛濛\CD2\13 - 烟雨濛濛(人声曲) - 群星.wav"
PROMPT = "我最怕最怕烟雨蒙蒙 看不清看不清你的身影 我曾经曾经对天呼唤 天在哭我在哭你在何处 往事一幕幕伤心一幕幕"


def decode(path, sr=16000):
    with av.open(path, mode="r") as c:
        rs = av.AudioResampler(format="flt", layout="mono", rate=sr)
        ch = []
        for fr in c.decode(audio=0):
            for o in rs.resample(fr):
                ch.append(o.to_ndarray())
        for o in rs.resample(None):
            ch.append(o.to_ndarray())
    a = np.concatenate(ch, axis=1)[0].astype(np.float32)
    return np.clip(a, -1, 1)


def run(model, audio, tag, **kw):
    t0 = time.time()
    segs, info = model.transcribe(audio, language="zh", word_timestamps=True, **kw)
    txt = []
    for s in segs:
        txt.append(f"{s.start:6.1f}-{s.end:6.1f} {s.text.strip()}")
    print(f"\n===== {tag} ({time.time()-t0:.1f}s, {len(txt)} segs) =====")
    print("\n".join(txt[:40]))


if __name__ == "__main__":
    print("decode...", flush=True)
    a = decode(AUDIO)
    print(f"{len(a)/16000:.1f}s, rms={float(np.sqrt((a**2).mean())):.4f}", flush=True)
    m = WhisperModel(MODEL, device="cuda", compute_type="float16")
    common = dict(beam_size=5, condition_on_previous_text=True, temperature=0.0,
                  hallucination_silence_threshold=None)
    run(m, a, "A: prompt + 宽阈值", vad_filter=False, no_speech_threshold=0.95,
        compression_ratio_threshold=3.0, initial_prompt=PROMPT, log_prob_threshold=-2.0, **common)
    run(m, a, "B: 无prompt + 宽阈值", vad_filter=False, no_speech_threshold=0.95,
        compression_ratio_threshold=3.0, log_prob_threshold=-2.0, **common)
    run(m, a, "C: vad + prompt", vad_filter=True, no_speech_threshold=0.6,
        compression_ratio_threshold=2.4, initial_prompt=PROMPT, **common)
