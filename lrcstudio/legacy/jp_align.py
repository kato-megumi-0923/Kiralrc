"""日文逐字对齐：WhisperX(ja wav2vec2) 在分离人声上逐句对齐，输出增强 LRC。"""
import json
import os
import re
import sys

import numpy as np

from whisperx import alignment as wx

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from lrcgen import decode_audio  # noqa: E402

TS = re.compile(r"^\[(\d{2}):(\d{2}\.\d{2,3})\](.*)$")


def fmt(sec, tag=False):
    sec = max(0.0, sec)
    s = "%02d:%06.3f" % (int(sec // 60), sec % 60)
    return f"<{s}>" if tag else f"[{s}]"


def main():
    vocal_wav, lrc_path, out_path = sys.argv[1:4]
    audio = decode_audio(vocal_wav, 16000)
    dur = len(audio) / 16000

    lines = []
    for raw in open(lrc_path, encoding="utf-8-sig"):
        m = TS.match(raw.strip())
        if m:
            lines.append((int(m.group(1)) * 60 + float(m.group(2)), m.group(3).strip()))

    model, meta = wx.load_align_model(language_code="ja", device="cuda")

    segs = []
    for i, (t0, text) in enumerate(lines):
        nxt = lines[i + 1][0] if i + 1 < len(lines) else dur
        n = len(re.sub(r"\s", "", text))
        e = min(dur, max(nxt, t0 + max(2.2, n * 0.45) + 0.8))
        segs.append({"text": text, "start": max(0.0, t0 - 0.35), "end": e})

    res = wx.align(segs, model, meta, audio, "cuda", interpolate_method="nearest",
                   return_char_alignments=True)

    rows = []
    for s in res["segments"]:
        chars = []
        for w in s.get("words", []):
            if "chars" in w and w["chars"]:
                for c in w["chars"]:
                    if c.get("start") is None or not c["char"].strip():
                        continue
                    chars.append((c["char"], float(c["start"]), float(c["end"])))
            elif w.get("start") is not None:
                chars.append((w["word"], float(w["start"]), float(w["end"])))
        rows.append({"text": s["text"], "chars": chars})

    head = []
    if len(sys.argv) > 4:
        head.append(f"[ti:{sys.argv[4]}]")
    body = []
    for r, (t0, text) in zip(rows, lines):
        if not r["chars"]:
            print("[skip]", text)
            continue
        cs = r["chars"]
        # 把字符对齐结果贴回原文（原文含标点/空格，标点沿用前一个字）
        placed, idx, last = [], 0, cs[0]
        for ch in text:
            if re.search(r"[\w\u3040-\u30ff\u4e00-\u9fff]", ch):
                last = cs[min(idx, len(cs) - 1)]
                placed.append(last)
                idx += 1
            else:
                placed.append(last)
        body.append(fmt(placed[0][1]) +
                    "".join(fmt(p[1], True) + ch for ch, p in zip(text, placed)) +
                    fmt(placed[-1][2], True))

    with open(out_path, "w", encoding="utf-8", newline="\n") as f:
        f.write("\n".join(head + body) + "\n")
    print(f"[ok] {out_path} {len(body)} 行")
    for b in body[:3]:
        print(b)


if __name__ == "__main__":
    main()
