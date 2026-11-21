"""用人声轨能量检测每句真实起音，用来评估三种时间源。"""
import json
import re
import sys

import numpy as np

sys.path.insert(0, __file__.rsplit("\\", 1)[0])
from lrcgen import decode_audio  # noqa: E402

TS = re.compile(r"^\[(\d{2}):(\d{2}\.\d{2,3})\](.*)$")


def onset(env, t0, t1, hop=0.02, rel=0.18):
    """在 [t0, t1] 内找起音：首个超过窗口峰值 rel 倍的帧"""
    a, b = int(t0 / hop), int(t1 / hop)
    seg = env[max(0, a): min(len(env), b)]
    if len(seg) < 3:
        return None
    peak = seg.max()
    if peak < 0.008:
        return None
    thr = max(0.006, peak * rel)
    # 要求连续 2 帧超阈值，避免毛刺
    for i in range(len(seg) - 1):
        if seg[i] >= thr and seg[i + 1] >= thr:
            return (max(0, a) + i) * hop
    return None


def main():
    vocal_wav, lrc_path = sys.argv[1:3]
    a = decode_audio(vocal_wav, 16000)
    hop = 0.02
    n = len(a) // int(hop * 16000)
    env = np.sqrt((a[: n * int(hop * 16000)].reshape(n, int(hop * 16000)) ** 2).mean(axis=1))

    lines = []
    for raw in open(lrc_path, encoding="utf-8-sig"):
        m = TS.match(raw.strip())
        if m:
            lines.append((int(m.group(1)) * 60 + float(m.group(2)), m.group(3).strip()))

    print(f"{'#':<3}{'检测起音':>9}{'LRC':>9}{'差':>7}  文本")
    out = []
    for i, (t0, text) in enumerate(lines):
        t1 = lines[i + 1][0] if i + 1 < len(lines) else t0 + 6
        o = onset(env, max(0, t0 - 1.6), min(t1 + 0.4, t0 + 3.0))
        out.append({"i": i + 1, "text": text, "lrc": t0, "onset": o})
        print(f"{i+1:<3}{(o if o else 0):9.2f}{t0:9.2f}{(f'{o-t0:+.2f}' if o else '   -'):>7}  {text[:14]}")

    diffs = [x["onset"] - x["lrc"] for x in out if x["onset"]]
    if diffs:
        print(f"\n平均 {np.mean(diffs):+.2f}s 中位 {np.median(diffs):+.2f}s 标准差 {np.std(diffs):.2f}s")
    json.dump(out, open("out/onsets.json", "w", encoding="utf-8"), ensure_ascii=False, indent=1)


if __name__ == "__main__":
    main()
