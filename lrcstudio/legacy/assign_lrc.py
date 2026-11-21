"""把参考歌词行对齐到 ASR 分段：行级 DP（单调）+ 段内字符定位。

为什么不是单纯字符级 difflib：ASR 文字可能整段错乱，全局匹配会把某一行
锚到后一段重复副歌上；用「行 × 分段」的单调 DP 可以先锁定每行属于哪一段。

用法:
  python assign_lrc.py --words out/t2.json --ref ref/t2_use.txt --out out/t2.lrc
"""
import argparse
import difflib
import json
import re

KEEP = re.compile(r"[\u3040-\u30ff\u3400-\u4dbf\u4e00-\u9fffA-Za-z0-9]")
META_RE = re.compile(r"^\s*(作词|作曲|编曲|制作|出品|监制|混音|母带|演唱|OP|SP)\s*[:：]")


def load_ref(path):
    lines = []
    for ln in open(path, encoding="utf-8-sig").read().splitlines():
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


def seg_chars(seg, words):
    out = []
    for w in words:
        if w["s"] >= seg["start"] - 0.05 and w["e"] <= seg["end"] + 0.05:
            cs = KEEP.findall(w["w"])
            if not cs:
                continue
            step = max(0.02, w["e"] - w["s"]) / len(cs)
            for i, c in enumerate(cs):
                out.append((c, w["s"] + i * step, w["s"] + (i + 1) * step))
    return out


def coverage(line, seg_text):
    lc = KEEP.findall(line)
    if not lc:
        return 0.0
    sc = KEEP.findall(seg_text)
    if not sc:
        return 0.0
    sm = difflib.SequenceMatcher(None, sc, lc, autojunk=False)
    hit = sum(n for _, _, n in sm.get_matching_blocks() if n)
    return hit / len(lc)


def locate(seg_cs, ptr, line):
    """段内从 ptr 起定位该行，返回 (start, end, new_ptr) 或 None"""
    lc = KEEP.findall(line)
    if not lc or ptr >= len(seg_cs):
        return None
    head = [c[0] for c in seg_cs[ptr:]]
    sm = difflib.SequenceMatcher(None, head, lc, autojunk=False)
    idx = []
    for a, b, n in sm.get_matching_blocks():
        if n:
            idx.extend(range(a, a + n))
    if not idx:
        return None
    return seg_cs[ptr + idx[0]][1], seg_cs[ptr + idx[-1]][2], ptr + idx[-1] + 1


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
    ap.add_argument("--debug", action="store_true")
    args = ap.parse_args()

    data = json.load(open(args.words, encoding="utf-8"))
    words, segs = data["words"], data["segments"]
    lines = load_ref(args.ref)
    S, L = len(segs), len(lines)

    cov = [[coverage(l, s["text"]) for s in segs] for l in lines]
    NEG = -1e9
    dp = [[NEG] * S for _ in range(L)]
    back = [[-1] * S for _ in range(L)]
    for j in range(S):
        dp[0][j] = cov[0][j]
    for i in range(1, L):
        best, arg = NEG, -1
        for j in range(S):
            # 同分时取更靠后的段，避免重复句全挤在一段里
            if dp[i - 1][j] >= best:
                best, arg = dp[i - 1][j], j
            dp[i][j] = best + cov[i][j]
            back[i][j] = arg

    j = max(range(S), key=lambda k: dp[L - 1][k])
    assign = [j]
    for i in range(L - 1, 0, -1):
        j = back[i][j]
        assign.append(j)
    assign.reverse()

    seg_cs = [seg_chars(s, words) for s in segs]
    ptr = [0] * S
    rows = []
    cur = 0
    used = set()
    for line, j in zip(lines, assign):
        # 同一句连续重复时 DP 可能都塞进同一段，段内取完就往后顺延
        got = None
        for k in [j] + list(range(max(j, cur) + 1, min(j + 5, S))):
            if k < cur:
                continue
            first_in_seg = k not in used
            pool = seg_cs[k][ptr[k]:]
            bound = len(pool)                    # 跨到下一段时的拼接点
            if k + 1 < S:                        # 允许跨一个段边界匹配（行首落在两段之间）
                pool = pool + seg_cs[k + 1]
            got = locate(pool, 0, line)
            if not got and ptr[k] > 0:
                # 指针已过界（上一行跨段溢出），从段首重试
                pool = seg_cs[k] + (seg_cs[k + 1] if k + 1 < S else [])
                bound = len(seg_cs[k])
                got = locate(pool, 0, line)
            if got:
                start, end, end_idx = got
                # 首字符压根不在这一段里（拟声词等），且它是本段开头 -> 用段起点
                if first_in_seg and start > segs[k]["start"] + 0.35:
                    head = "".join(c[0] for c in pool[:8])
                    if KEEP.findall(line)[0] not in head:
                        start = segs[k]["start"]
                if end_idx <= bound:
                    ptr[k] += end_idx
                else:
                    ptr[k] = len(seg_cs[k])
                    if k + 1 < S:
                        ptr[k + 1] = max(0, end_idx - bound)
                        used.add(k + 1)
                cur = k
                used.add(k)
                got = (start, end, end_idx)
                break
        rows.append((line, got[0] if got else None))

    kept, prev = [], -10.0
    for line, s in rows:
        if s is None:
            kept.append((None, line))
            continue
        start = max(0.0, s - args.lead)
        if start <= prev + 0.2:
            kept.append((None, line))
            continue
        kept.append((start, line))
        prev = start

    # 兜底：仍未定位的行，在「上一行 ~ 下一行」的时间窗里整段再找一次
    flat = []
    for s in segs:
        flat.extend(seg_chars(s, words))
    flat.sort(key=lambda x: x[1])
    for i, (s, line) in enumerate(kept):
        if s is not None:
            continue
        lo = (kept[i - 1][0] + 0.4) if i > 0 and kept[i - 1][0] is not None else 0.0
        hi = float("inf")
        for k in range(i + 1, len(kept)):
            if kept[k][0] is not None:
                hi = kept[k][0] - 0.2
                break
        window = [c for c in flat if lo - 0.05 <= c[1] <= hi]
        got = locate(window, 0, line) if window else None
        if got:
            kept[i] = (max(0.0, got[0] - args.lead), line)
    for i, (s, line) in enumerate(kept):
        if s is not None:
            kept[i] = (max(s, 0.5), line)
            break

    header = [f"[ti:{args.ti}]"] if args.ti else []
    if args.ar:
        header.append(f"[ar:{args.ar}]")
    if args.al:
        header.append(f"[al:{args.al}]")
    header.append(f"[by:{args.by}]")

    body = [fmt_ts(s) + line for s, line in kept if s is not None]
    with open(args.out, "w", encoding="utf-8", newline="\n") as f:
        f.write("\n".join(header + body) + "\n")

    ok = len(body)
    print(f"[ok] {args.out}  {ok}/{L} 行")
    for s, line in kept:
        print(f"{fmt_ts(s)}{line}" if s is not None else f"   --:--.-- {line}  [丢弃]")
    if args.debug:
        times = [s for s, _ in kept if s is not None]
        gaps = [times[i + 1] - times[i] for i in range(len(times) - 1)]
        if gaps:
            print(f"[gap] max={max(gaps):.1f}s min={min(gaps):.1f}s 时长={data.get('duration'):.1f}s")


if __name__ == "__main__":
    main()
