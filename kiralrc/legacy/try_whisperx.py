"""用 WhisperX(wav2vec2 CTC 强制对齐) 给已知歌词算逐字时间，并与现有结果对比。"""
import json
import os
import sys

import numpy as np
import torch

from whisperx import alignment as wx_align

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from lrcgen import decode_audio  # noqa: E402

AUDIO = r"C:\Users\yxljs\OneDrive\Music\cloudy music\不可描述\走在乡间的小路上-卓依婷.mp3"


def main():
    lines = []
    import re
    for raw in open("out/t3.lrc", encoding="utf-8-sig"):
        raw = raw.strip()
        m = re.match(r"^\[(\d{2}):(\d{2}\.\d{2,3})\](.*)$", raw)
        if not m:
            continue
        lines.append((int(m.group(1)) * 60 + float(m.group(2)), m.group(3).strip()))
    dur = json.load(open("out/t3.json", encoding="utf-8"))["duration"]

    audio = decode_audio(AUDIO, 16000)

    model, meta = wx_align.load_align_model(language_code="zh", device="cuda")
    print("[model]", type(model).__name__)

    segs = []
    for i, (t0, text) in enumerate(lines):
        t1 = lines[i + 1][0] if i + 1 < len(lines) else dur
        segs.append({
            "text": text,
            "start": max(0.0, t0 - 0.30),
            "end": min(dur, t1 + 0.30),
        })

    res = wx_align.align(segs, model, meta, audio, "cuda",
                         interpolate_method="nearest", return_char_alignments=True)

    with open("out/t3_wx.json", "w", encoding="utf-8") as f:
        json.dump(res, f, ensure_ascii=False, indent=1)

    for i, seg in enumerate(res["segments"][:6]):
        print(f"\n--- 行{i+1} {seg['start']:.2f}-{seg['end']:.2f} | {seg['text']}")
        for w in seg.get("words", []):
            if "chars" in w:
                print("   ", " ".join(f"{c['char']}{c['start']:.2f}" for c in w["chars"] if c.get("start") is not None))
            else:
                print(f"    {w['word']} {w.get('start')}-{w.get('end')}")


if __name__ == "__main__":
    main()
