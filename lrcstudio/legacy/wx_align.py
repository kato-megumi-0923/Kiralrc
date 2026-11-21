"""整首歌词一次性 WhisperX 强制对齐，再按行切分。

不做逐行窗口限制（逐行会把对不上的字挤成 20ms），
整首连续对齐后按字符累计数切回每一行。
"""
import json
import os
import re
import sys

import numpy as np

from whisperx import alignment as wx_align

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from lrcgen import decode_audio  # noqa: E402

TS = re.compile(r"^\[(\d{2}):(\d{2}\.\d{2,3})\](.*)$")


def load_lines(path, dur):
    out = []
    for raw in open(path, encoding="utf-8-sig"):
        m = TS.match(raw.strip())
        if m:
            out.append((int(m.group(1)) * 60 + float(m.group(2)), m.group(3).strip()))
    last_end = out[-1][0] + 6.0
    return out, min(dur, max(last_end, dur))


def main():
    audio_path, lrc_path, lang, words_json, out_json = sys.argv[1:6]
    dur = json.load(open(words_json, encoding="utf-8"))["duration"]
    lines, vocal_end = load_lines(lrc_path, dur)
    audio = decode_audio(audio_path, 16000)

    text = " ".join(t for _, t in lines)
    seg = [{"text": text, "start": max(0.0, lines[0][0] - 0.6), "end": vocal_end}]

    model, meta = wx_align.load_align_model(language_code=lang, device="cuda")
    res = wx_align.align(seg, model, meta, audio, "cuda", interpolate_method="nearest",
                         return_char_alignments=True)

    chars = []
    for s in res["segments"]:
        for w in s.get("words", []):
            for c in w.get("chars", []) or []:
                if c.get("start") is None:
                    continue
                if c["char"].strip() == "":
                    continue
                chars.append((c["char"], float(c["start"]), float(c["end"])))

    # 按行字符累计数切回
    out, idx = [], 0
    for i, (t0, tx) in enumerate(lines):
        n = len(re.sub(r"\s", "", tx))
        chunk = chars[idx: idx + n]
        idx += n
        if not chunk:
            out.append({"text": tx, "start": t0, "end": None, "chars": []})
            continue
        out.append({
            "text": tx,
            "start": chunk[0][1],
            "end": chunk[-1][2],
            "chars": [{"c": c, "s": s, "e": e} for c, s, e in chunk],
        })

    with open(out_json, "w", encoding="utf-8") as f:
        json.dump({"lines": out, "total_chars": len(chars)}, f, ensure_ascii=False, indent=1)

    print(f"[ok] chars={len(chars)} 期望={idx} -> {out_json}")
    for ln in out[:8]:
        cs = " ".join(f"{c['c']}{c['s']:.2f}" for c in ln["chars"])
        print(f"{ln['start']:7.2f}-{(ln['end'] or 0):7.2f} {ln['text']}\n    {cs}")


if __name__ == "__main__":
    main()
