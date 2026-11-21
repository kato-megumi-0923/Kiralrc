"""把整首按「行」切段，生成 SOFA 需要的 wav + 拼音 lab。"""
import json
import os
import re
import sys
import wave

import numpy as np
import pypinyin
from pypinyin import Style

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

DICT = r"D:\WindowsDev\SOFA\dictionary\opencpop-extension.txt"
TS = re.compile(r"^\[(\d{2}):(\d{2}\.\d{2,3})\](.*)$")
HAN = re.compile(r"[\u4e00-\u9fff]")
PAD = 0.40


def load_lines(path):
    out = []
    for raw in open(path, encoding="utf-8-sig"):
        m = TS.match(raw.strip())
        if m:
            out.append((int(m.group(1)) * 60 + float(m.group(2)), m.group(3).strip()))
    return out


def main():
    audio_path, lrc_path, words_json, out_dir = sys.argv[1:5]
    ends_path = sys.argv[5] if len(sys.argv) > 5 else None
    from lrcgen import decode_audio

    keys = {ln.split("\t")[0].strip() for ln in open(DICT, encoding="utf-8") if ln.strip()}
    dur = json.load(open(words_json, encoding="utf-8"))["duration"]
    lines = load_lines(lrc_path)
    # 每句的「预期结束」：取已有逐字 LRC 的行尾标签，避免把长间奏也算进本句
    ends = []
    if ends_path:
        import re as _re
        for raw in open(ends_path, encoding="utf-8-sig"):
            tags = _re.findall(r"<(\d{2}):(\d{2}\.\d{2,3})>", raw)
            if tags:
                ends.append(int(tags[-1][0]) * 60 + float(tags[-1][1]))
    audio = decode_audio(audio_path, 44100)

    os.makedirs(out_dir, exist_ok=True)
    meta = []
    for i, (t0, text) in enumerate(lines):
        nxt = lines[i + 1][0] if i + 1 < len(lines) else dur
        chars = [c for c in text if HAN.match(c)]
        s = max(0.0, t0 - 0.35)
        # 按字数给时长上限（约 0.8s/字），既避开长间奏，也不至于把整句挤在开头
        e = min(len(audio) / 44100, nxt + 0.9, t0 + max(3.0, len(chars) * 0.85) + 1.0)
        e = max(e, s + 1.2)
        seg = audio[int(s * 44100): int(e * 44100)]

        syls = [pypinyin.lazy_pinyin(c, style=Style.NORMAL)[0] for c in chars]
        bad = [s2 for s2 in syls if s2 not in keys]

        name = f"l{i + 1:02d}"
        with wave.open(os.path.join(out_dir, name + ".wav"), "wb") as w:
            w.setnchannels(1)
            w.setsampwidth(2)
            w.setframerate(44100)
            w.writeframes((np.clip(seg, -1, 1) * 32767).astype(np.int16).tobytes())
        with open(os.path.join(out_dir, name + ".lab"), "w", encoding="utf-8") as f:
            f.write(" ".join(syls))

        meta.append({"name": name, "offset": s, "start": t0, "end": nxt,
                     "seg_end": e, "text": text, "chars": chars, "syls": syls, "bad": bad})

    json.dump(meta, open(os.path.join(out_dir, "meta.json"), "w", encoding="utf-8"),
              ensure_ascii=False, indent=1)
    print(f"[ok] {len(meta)} 段 -> {out_dir}")
    bad = [(m['name'], m['bad']) for m in meta if m['bad']]
    if bad:
        print("[警告] 有音节不在词典：", bad)


if __name__ == "__main__":
    main()
