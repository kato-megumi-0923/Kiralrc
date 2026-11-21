"""在已有整行 LRC 基础上，补出逐字（enhanced LRC）。

输入：整行 LRC（行序与行首时间）+ ASR 词级时间 json
输出：[mm:ss.xx]<mm:ss.xx>字<mm:ss.xx>字...<mm:ss.xx>
      行尾最后一个标签是**该行最后一个字的收尾时间**（库内主流写法，16676 行有、3382 行无），
      否则播放器会把最后一个字的起点当成行尾，字还没唱完就被切掉。

逐字时间来源：该行时间窗内的 ASR 词，先把每个词的时长摊到它的字符上，
再与行文本做字符级 difflib 对齐；对不上的字用邻居插值。
"""
import argparse
import difflib
import json
import re

KEEP = re.compile(r"[\u3040-\u30ff\u3400-\u4dbf\u4e00-\u9fffA-Za-z0-9]")
TS = re.compile(r"^\[(\d{2}):(\d{2}\.\d{2,3})\](.*)$")
META = re.compile(r"^\[(ti|ar|al|by|tool|offset|length):", re.I)


def fmt_line(sec):
    sec = max(0.0, sec)
    return "[%02d:%06.3f]" % (int(sec // 60), sec % 60)


def fmt_tag(sec):
    sec = max(0.0, sec)
    return "<%02d:%06.3f>" % (int(sec // 60), sec % 60)


def load_lrc(path):
    metas, lines = [], []
    for raw in open(path, encoding="utf-8-sig"):
        ln = raw.strip()
        if not ln:
            continue
        if META.match(ln):
            metas.append(ln)
            continue
        m = TS.match(ln)
        if m:
            lines.append((int(m.group(1)) * 60 + float(m.group(2)), m.group(3).strip()))
    return metas, lines


def char_times(words, t0, t1):
    """该行时间窗内的字符时间轴 [(char, start, end)]"""
    seq = []
    for w in words:
        if w["e"] < t0 - 0.15 or w["s"] > t1 + 0.05:
            continue
        cs = KEEP.findall(w["w"])
        if not cs:
            continue
        s, e = max(w["s"], t0 - 0.15), min(w["e"], t1)
        if e <= s:
            continue
        step = (e - s) / len(cs)
        for i, c in enumerate(cs):
            seq.append((c, s + i * step, s + (i + 1) * step))
    return seq


def align_line(text, seq, t0, t1):
    """返回 (逐字时间列表, 行尾时间)"""
    chars = list(text)
    keep = [i for i, c in enumerate(chars) if KEEP.match(c)]
    tgt = [chars[i] for i in keep]
    pairs = [None] * len(text)

    if tgt and seq:
        src = [s[0] for s in seq]
        sm = difflib.SequenceMatcher(None, src, tgt, autojunk=False)
        for tag, i1, i2, j1, j2 in sm.get_opcodes():
            if tag == "equal":
                for k in range(i2 - i1):
                    pairs[keep[j1 + k]] = (seq[i1 + k][1], seq[i1 + k][2])
            elif tag == "replace" and (i2 - i1) > 0:
                s, e = seq[i1][1], seq[i2 - 1][2]
                n = j2 - j1
                step = max(0.02, (e - s) / max(1, n))
                for k in range(n):
                    pairs[keep[j1 + k]] = (s + k * step, s + (k + 1) * step)

    if not any(pairs):
        # 整行没有锚点（ASR 没吐字）：按字数在窗口内均分
        n = max(1, len(text))
        span = max(0.6, min(t1 - t0, 0.42 * n))
        return [(t0 + span * i / n, t0 + span * (i + 1) / n) for i in range(n)], t0 + span

    # 未对上的实词：在前后锚点之间均分
    i = 0
    while i < len(pairs):
        if pairs[i] is not None:
            i += 1
            continue
        j = i
        while j < len(pairs) and pairs[j] is None:
            j += 1
        prev = next((pairs[k] for k in range(i - 1, -1, -1) if pairs[k]), None)
        nxt = next((pairs[k] for k in range(j, len(pairs)) if pairs[k]), None)
        s = prev[1] if prev else (nxt[0] if nxt else t0)
        e = nxt[0] if nxt else (s + 0.2 * (j - i))
        n = max(1, j - i)
        step = max(0.02, (e - s) / n)
        for k in range(n):
            pairs[i + k] = (s + k * step, s + (k + 1) * step)
        i = j

    # 标点/空格：沿用前一个字的起止（不额外占时间）
    last = None
    for k in range(len(pairs)):
        if not KEEP.match(chars[k]) and pairs[k] is None:
            pairs[k] = last if last else (t0, t0)
        else:
            last = pairs[k]

    # 单调化，并把首字钉在行首
    fixed, prev_start = [], t0
    for s, e in pairs:
        s = max(s, prev_start)
        e = max(e, s + 0.02)
        fixed.append([s, e])
        prev_start = s
    fixed[0][0] = t0
    fixed[0][1] = max(fixed[0][1], t0 + 0.05)

    line_end = fixed[-1][1]
    return fixed, line_end


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--lrc", required=True)
    ap.add_argument("--words", required=True)
    ap.add_argument("--out", required=True)
    ap.add_argument("--by", default="Whisper-ASR 逐字")
    args = ap.parse_args()

    data = json.load(open(args.words, encoding="utf-8"))
    words, dur = data["words"], data["duration"]
    metas, lines = load_lrc(args.lrc)

    body = []
    for i, (t0, text) in enumerate(lines):
        if not text:
            continue
        t1 = lines[i + 1][0] if i + 1 < len(lines) else dur
        pairs, line_end = align_line(text, char_times(words, t0, t1), t0, t1)
        # 行尾不能咬到下一行
        cap = (t1 - 0.02) if i + 1 < len(lines) else dur
        if line_end > cap:
            line_end = max(pairs[-1][0] + 0.05, cap)
        seg = fmt_line(t0) + "".join(fmt_tag(s) + c for c, (s, _) in zip(text, pairs))
        seg += fmt_tag(line_end)
        body.append(seg)

    head = [h for h in metas if not re.match(r"^\[by:", h, re.I)]
    head.append(f"[by:{args.by}]")

    with open(args.out, "w", encoding="utf-8", newline="\n") as f:
        f.write("\n".join(head + body) + "\n")

    print(f"[ok] {args.out}  {len(body)} 行")
    for b in body[:3]:
        print(b)
    print("  ...")
    for b in body[-2:]:
        print(b)


if __name__ == "__main__":
    main()
