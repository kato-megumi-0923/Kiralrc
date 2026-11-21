"""按交接说明的验收标准检查 LRC。"""
import argparse
import io
import re
import sys

TS = re.compile(r"^\[(\d{2}):(\d{2}\.\d{2,3})\](.*)$")


def check(path, max_chars=30, min_lines=1, max_gap=15.0, official=None):
    raw = open(path, "rb").read()
    problems = []
    if raw.startswith(b"\xef\xbb\xbf"):
        problems.append("有 BOM")
    if b"\r\n" in raw:
        problems.append("使用 CRLF")
    try:
        text = raw.decode("utf-8")
    except Exception:
        return ["不是合法 UTF-8"]

    rows = []
    for ln in text.splitlines():
        m = TS.match(ln.strip())
        if m:
            rows.append((int(m.group(1)) * 60 + float(m.group(2)), m.group(3)))
    if not rows:
        return ["没有解析到时间戳行"]

    times = [r[0] for r in rows]
    if times[0] < 0.5:
        problems.append(f"首行时间 {times[0]:.2f}s < 0.50")
    for i in range(1, len(times)):
        if times[i] <= times[i - 1]:
            problems.append(f"第 {i+1} 行时间未严格递增 ({times[i-1]:.2f} -> {times[i]:.2f})")
    gaps = [(times[i] - times[i - 1], i) for i in range(1, len(times))]
    big = [(g, i) for g, i in gaps if g > max_gap]
    for g, i in big:
        problems.append(f"第 {i+1} 行前间隔 {g:.1f}s > {max_gap}s（可能是间奏）")
    long_lines = [(len(t), t) for _, t in rows if len(t) > max_chars]
    for n, t in long_lines:
        problems.append(f"过长 {n} 字: {t}")
    if len(rows) < min_lines:
        problems.append(f"行数 {len(rows)} < {min_lines}")

    print(f"=== {path}")
    print(f"  行数 {len(rows)}  首行 {times[0]:.2f}s  末行 {times[-1]:.2f}s  "
          f"最大间隔 {max(g for g, _ in gaps) if gaps else 0:.1f}s")
    if official:
        ref = {}
        for ln in open(official, encoding="utf-8-sig"):
            m = TS.match(ln.strip())
            if not m:
                continue
            tt = int(m.group(1)) * 60 + float(m.group(2))
            txt = m.group(3).strip()
            if txt and not re.match(r"^(作词|作曲|编曲)", txt):
                ref.setdefault(txt, []).append(tt)
        diffs = []
        for t, txt in rows:
            if txt in ref and ref[txt]:
                diffs.append(t - ref[txt].pop(0))
        if diffs:
            print(f"  与官方对照 {len(diffs)} 行: 平均偏差 {sum(diffs)/len(diffs):+.2f}s  "
                  f"最大 {max(diffs, key=abs):+.2f}s")
    if problems:
        print("  问题:")
        for p in problems:
            print("   -", p)
    else:
        print("  ✅ 全部通过")
    return problems


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("files", nargs="+")
    ap.add_argument("--max-chars", type=int, default=30)
    ap.add_argument("--min-lines", type=int, default=1)
    ap.add_argument("--official", default=None)
    a = ap.parse_args()
    allp = []
    for f in a.files:
        allp += check(f, a.max_chars, a.min_lines, official=a.official)
    sys.exit(0)
