"""校验逐字（enhanced LRC）。"""
import argparse
import re
import sys

LINE = re.compile(r"^\[(\d{2}):(\d{2}\.\d{2,3})\](.*)$")
TAG = re.compile(r"<(\d{2}):(\d{2}\.\d{2,3})>")
META = re.compile(r"^\[(ti|ar|al|by|tool|offset|length):", re.I)


def secs(a, b):
    return int(a) * 60 + float(b)


def check(path, max_jump=2.0, max_chars=30):
    raw = open(path, "rb").read()
    probs = []
    if raw.startswith(b"\xef\xbb\xbf"):
        probs.append("有 BOM")
    if b"\r\n" in raw:
        probs.append("CRLF")
    text = raw.decode("utf-8")

    lines, ends = [], 0
    for ln in text.splitlines():
        if not ln.strip() or META.match(ln.strip()):
            continue
        m = LINE.match(ln.strip())
        if not m:
            probs.append(f"无法解析: {ln[:30]}")
            continue
        body = m.group(3)
        tags = list(TAG.finditer(body))
        plain = TAG.sub("", body)
        if not tags:
            probs.append(f"改行无逐字标签: {plain[:20]}")
            continue
        t0 = secs(m.group(1), m.group(2))
        first = secs(tags[0].group(1), tags[0].group(2))
        if abs(first - t0) > 0.011:
            probs.append(f"行首[{t0:.2f}]与首个标签[{first:.2f}]不一致: {plain[:16]}")
        has_end = len(tags) == len(plain) + 1
        if not has_end and len(tags) != len(plain):
            probs.append(f"标签数 {len(tags)} 与字符数 {len(plain)} 不匹配: {plain[:16]}")
        if len(plain) > max_chars:
            probs.append(f"过长 {len(plain)} 字: {plain[:20]}")
        prev = None
        for t in tags[:-1] if has_end else tags:
            v = secs(t.group(1), t.group(2))
            if prev is not None:
                if v < prev - 0.001:
                    probs.append(f"标签倒序 {prev:.2f}->{v:.2f}: {plain[:16]}")
                elif v - prev > max_jump:
                    probs.append(f"行内跳变 {v - prev:.1f}s: {plain[:16]}")
            prev = v
        end = secs(tags[-1].group(1), tags[-1].group(2)) if has_end else prev
        if has_end and end <= prev:
            probs.append(f"行尾标签 {end:.2f} 未晚于末字起点 {prev:.2f}: {plain[:16]}")
        lines.append((t0, plain, end))
        ends += 1 if has_end else 0

    for i in range(1, len(lines)):
        if lines[i][0] <= lines[i - 1][0]:
            probs.append(f"第 {i+1} 行时间未递增")
        if lines[i - 1][2] > lines[i][0] + 0.15:
            probs.append(f"第 {i} 行末字 {lines[i-1][2]:.2f} 越过下一行行首 {lines[i][0]:.2f}")

    print(f"=== {path}")
    print(f"  逐字行 {len(lines)}  首行 {lines[0][0]:.2f}s  末字 {lines[-1][2]:.2f}s  "
          f"带行尾标签: {'是' if ends == len(lines) else f'否({ends}/{len(lines)})'}")
    if probs:
        print(f"  问题 {len(probs)}:")
        for p in probs[:12]:
            print("   -", p)
        if len(probs) > 12:
            print(f"   ... 另 {len(probs)-12} 条")
    else:
        print("  ✅ 逐字格式与时间均正常")
    return probs


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("files", nargs="+")
    ap.add_argument("--max-jump", type=float, default=2.0)
    a = ap.parse_args()
    bad = 0
    for f in a.files:
        bad += len(check(f, a.max_jump))
    sys.exit(0)
