"""为 SOFA 准备输入：拼音 lab + 41k 单声道 wav，并输出「行→字→音节」映射。

SOFA 的 g2p 吃的是空格分隔的无声调拼音（音节），行与行之间用 SP 标记乐句边界。
"""
import json
import re
import sys

import wave

import numpy as np
import pypinyin
from pypinyin import Style

DICT = r"D:\WindowsDev\SOFA\dictionary\opencpop-extension.txt"
TS = re.compile(r"^\[(\d{2}):(\d{2}\.\d{2,3})\](.*)$")
HAN = re.compile(r"[\u4e00-\u9fff]")


def load_lines(path):
    out = []
    for raw in open(path, encoding="utf-8-sig"):
        m = TS.match(raw.strip())
        if m:
            out.append((int(m.group(1)) * 60 + float(m.group(2)), m.group(3).strip()))
    return out


def main():
    lrc, lab_out, wav_out, map_out = sys.argv[1:5]

    keys = set()
    for ln in open(DICT, encoding="utf-8"):
        if ln.strip():
            keys.add(ln.split("\t")[0].strip())

    lines = load_lines(lrc)
    words, mapping, missing = [], [], []
    for t0, text in lines:
        chars = [c for c in text if HAN.match(c)]
        syls = []
        for c in chars:
            s = pypinyin.lazy_pinyin(c, style=Style.NORMAL)[0]
            if s not in keys:
                missing.append((c, s))
            syls.append(s)
        mapping.append({"start": t0, "text": text, "chars": chars, "syls": syls})
        words.extend(syls)
        words.append("SP")

    if words and words[-1] == "SP":
        words.pop()

    with open(lab_out, "w", encoding="utf-8") as f:
        f.write(" ".join(words))

    json.dump(mapping, open(map_out, "w", encoding="utf-8"), ensure_ascii=False, indent=1)

    print(f"[lab] {lab_out}  {len(words)} 个 token（含 SP）")
    print(f"[map] {len(mapping)} 行，共 {sum(len(m['chars']) for m in mapping)} 字")
    if missing:
        print("[警告] 不在词典里的音节：")
        for c, s in missing:
            print(f"   {c} -> {s}")
    else:
        print("[ok] 所有音节都在词典中")


if __name__ == "__main__":
    main()
