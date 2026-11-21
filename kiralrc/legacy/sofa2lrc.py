"""把 SOFA 的逐音节对齐结果拼成逐字增强 LRC。

输入：sofa/lines_*/meta.json（含 offset/行文本/字与音节）+ sofa/lines_*/htk/words/*.lab
输出：[mm:ss.xx]<mm:ss.xx>字...<mm:ss.xx>
"""
import argparse
import json
import os
import re

HAN = re.compile(r"[\u4e00-\u9fff]")


def fmt(sec, tag=False):
    sec = max(0.0, sec)
    s = "%02d:%06.3f" % (int(sec // 60), sec % 60)
    return f"<{s}>" if tag else f"[{s}]"


def read_words(path):
    """返回 [(start, end, token)]，时间为相对秒"""
    out = []
    for ln in open(path, encoding="utf-8"):
        p = ln.split()
        if len(p) >= 3:
            out.append((int(p[0]) / 1e7, int(p[1]) / 1e7, p[2]))
    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--meta", required=True)
    ap.add_argument("--words-dir", required=True)
    ap.add_argument("--out", required=True)
    ap.add_argument("--ti", default="")
    ap.add_argument("--ar", default="")
    ap.add_argument("--al", default="")
    ap.add_argument("--by", default="SOFA 歌声对齐")
    args = ap.parse_args()

    meta = json.load(open(args.meta, encoding="utf-8"))
    head = []
    if args.ti:
        head.append(f"[ti:{args.ti}]")
    if args.ar:
        head.append(f"[ar:{args.ar}]")
    if args.al:
        head.append(f"[al:{args.al}]")
    head.append(f"[by:{args.by}]")

    body, prev_start, warn = [], -10.0, []
    rows = []
    for m in meta:
        path = os.path.join(args.words_dir, m["name"] + ".lab")
        if not os.path.exists(path):
            warn.append(f"{m['name']} 无输出")
            continue
        syl = [w for w in read_words(path) if w[2] not in ("SP", "AP")]
        if not syl:
            warn.append(f"{m['name']} 全是静音")
            continue
        if len(syl) != len(m["chars"]):
            warn.append(f"{m['name']} 音节数 {len(syl)} != 字数 {len(m['chars'])}")

        off = m["offset"]
        times, k = [], 0
        for c in m["text"]:
            if HAN.match(c):
                if k < len(syl):
                    times.append([off + syl[k][0], off + syl[k][1]])
                k += 1
            else:
                times.append(list(times[-1]) if times else [off + syl[0][0], off + syl[0][1]])
        if not times:
            continue
        rows.append({"name": m["name"], "text": m["text"], "times": times})

    # 逐句独立对齐，句尾可能压到下一句：按下一句行首统一夹住
    for i, r in enumerate(rows):
        if i + 1 < len(rows):
            cap = rows[i + 1]["times"][0][0] - 0.02
            for t in r["times"]:
                t[0] = min(t[0], cap)
                t[1] = min(t[1], cap)
        prev = -10.0
        for t in r["times"]:
            t[0] = max(t[0], prev)
            t[1] = max(t[1], t[0] + 0.02)
            prev = t[0]

    for r in rows:
        start, line_end = r["times"][0][0], r["times"][-1][1]
        if start <= prev_start + 0.2:
            warn.append(f"{r['name']} 行首 {start:.2f} 未递增（上句 {prev_start:.2f}）")
        seg = fmt(start) + "".join(fmt(t[0], True) + c for c, t in zip(r["text"], r["times"]))
        seg += fmt(line_end, True)
        body.append(seg)
        prev_start = start

    with open(args.out, "w", encoding="utf-8", newline="\n") as f:
        f.write("\n".join(head + body) + "\n")

    print(f"[ok] {args.out} {len(body)} 行")
    for b in body[:3]:
        print(b)
    print(" ...")
    for b in body[-2:]:
        print(b)
    for w in warn:
        print("[警告]", w)


if __name__ == "__main__":
    main()
