"""用 ASR 的字符级时间轴去对齐「真实歌词文本」，输出 LRC。

做法：先把 ASR 词级时间摊到字符上，再与参考文本做字符级 difflib 对齐；
每一行歌词只采信「真正匹配到 ASR 字符」的时间锚点，
整行都没匹配上的（ASR 没唱、或该段被跳过）直接丢弃，不做插值硬凑。

用法:
  python make_lrc.py --words out/t3.json --ref ref/t3.txt --out out/t3.lrc
"""
import argparse
import difflib
import json
import re

KEEP = re.compile(r"[\u3040-\u30ff\u3400-\u4dbf\u4e00-\u9fffA-Za-z0-9]")
META_RE = re.compile(r"^\s*(作词|作曲|编曲|制作|出品|监制|混音|母带|演唱|OP|SP)\s*[:：]")


def norm_chars(s):
    return KEEP.findall(s)


def char_timeline(words):
    out = []
    for w in words:
        cs = norm_chars(w["w"])
        if not cs:
            continue
        dur = max(0.02, w["e"] - w["s"])
        step = dur / len(cs)
        for i, c in enumerate(cs):
            out.append((c, w["s"] + i * step, w["s"] + (i + 1) * step))
    return out


def load_ref(path):
    raw = open(path, encoding="utf-8-sig").read()
    lines = []
    for ln in raw.splitlines():
        t = ln.strip()
        if not t or t == "---":
            continue
        if t.startswith("[") and "]" in t:
            t = t.split("]", 1)[1].strip()
            if not t:
                continue
        if META_RE.match(t):
            continue
        lines.append(t)
    return lines


def align_lines(lines, timeline):
    """返回 [(line_text, start, end, matched_chars, total_chars)]，未匹配的行被丢弃。"""
    ref_chars, offsets, pos = [], [], 0
    for t in lines:
        cs = norm_chars(t)
        offsets.append((pos, pos + len(cs)))
        ref_chars.extend(cs)
        pos += len(cs)

    asr = [x[0] for x in timeline]
    sm = difflib.SequenceMatcher(None, asr, ref_chars, autojunk=False)
    tmap = [None] * len(ref_chars)          # ref 下标 -> (start, end)
    for tag, i1, i2, j1, j2 in sm.get_opcodes():
        if tag == "equal":
            for k in range(i2 - i1):
                tmap[j1 + k] = (timeline[i1 + k][1], timeline[i1 + k][2])
        elif tag == "replace" and (i2 - i1) > 0:
            s, e = timeline[i1][1], timeline[i2 - 1][2]
            n = j2 - j1
            step = max(0.02, (e - s) / max(1, n))
            for k in range(n):
                tmap[j1 + k] = (s + k * step, s + (k + 1) * step)

    out = []
    for t, (a, b) in zip(lines, offsets):
        anchors = [tmap[i] for i in range(a, b) if tmap[i]]
        total = max(1, b - a)
        if not anchors:
            out.append([t, None, None, 0, total])
            continue
        out.append([t, anchors[0][0], anchors[-1][1], len(anchors), total])
    return out, sm.ratio()


def fmt_ts(sec):
    sec = max(0.0, sec)
    return "[%02d:%06.3f]" % (int(sec // 60), sec % 60)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--words", required=True)
    ap.add_argument("--ref", required=True)
    ap.add_argument("--out", required=True)
    ap.add_argument("--lead", type=float, default=0.15)
    ap.add_argument("--ti", default="")
    ap.add_argument("--ar", default="")
    ap.add_argument("--al", default="")
    ap.add_argument("--by", default="Whisper-ASR+歌词文本对齐")
    ap.add_argument("--min-cover", type=float, default=0.30, help="行内匹配字符占比下限")
    ap.add_argument("--debug", action="store_true")
    args = ap.parse_args()

    data = json.load(open(args.words, encoding="utf-8"))
    timeline = char_timeline(data["words"])
    lines = load_ref(args.ref)
    rows, ratio = align_lines(lines, timeline)

    kept, dropped, prev_start = [], [], -10.0
    for t, s, e, m, total in rows:
        cover = m / total
        if s is None or cover < args.min_cover:
            dropped.append((t, m, total))
            continue
        start = max(0.0, s - args.lead)
        if start <= prev_start + 0.2:     # 明显乱序，视为误配
            dropped.append((t, m, total))
            continue
        kept.append((start, t, cover))
        prev_start = start

    if kept:
        kept[0] = (max(kept[0][0], 0.5), kept[0][1], kept[0][2])

    header = []
    if args.ti:
        header.append(f"[ti:{args.ti}]")
    if args.ar:
        header.append(f"[ar:{args.ar}]")
    if args.al:
        header.append(f"[al:{args.al}]")
    header.append(f"[by:{args.by}]")

    body = [fmt_ts(s) + t for s, t, _ in kept]
    with open(args.out, "w", encoding="utf-8", newline="\n") as f:
        f.write("\n".join(header + body) + "\n")

    print(f"[ok] {args.out}  保留 {len(kept)} 行 / 丢弃 {len(dropped)} 行  match={ratio:.3f}")
    for (s, t, c) in kept:
        print(fmt_ts(s) + t)
    if dropped:
        print("--- 丢弃（未唱到或匹配过低）---")
        for t, m, total in dropped:
            print(f"    [{m}/{total}] {t}")
    if args.debug:
        gaps = [kept[i + 1][0] - kept[i][0] for i in range(len(kept) - 1)]
        if gaps:
            print(f"[gap] max={max(gaps):.1f}s  min={min(gaps):.1f}s  时长={data.get('duration'):.1f}s")


if __name__ == "__main__":
    main()
