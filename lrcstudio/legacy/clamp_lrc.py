"""夹断：让每行的所有标签都不越过下一行的行首时间。"""
import re
import sys

LINE = re.compile(r"^\[(\d{2}):(\d{2}\.\d{2,3})\](.*)$")
TAG = re.compile(r"<(\d{2}):(\d{2}\.\d{2,3})>")
META = re.compile(r"^\[(ti|ar|al|by|tool|offset|length):", re.I)


def fmt(sec):
    sec = max(0.0, sec)
    return "%02d:%06.3f" % (int(sec // 60), sec % 60)


def main():
    path = sys.argv[1]
    gap = float(sys.argv[2]) if len(sys.argv) > 2 else 0.02

    metas, lines = [], []
    for raw in open(path, encoding="utf-8-sig"):
        ln = raw.rstrip("\n")
        if not ln.strip():
            continue
        if META.match(ln.strip()):
            metas.append(ln.strip())
            continue
        m = LINE.match(ln.strip())
        if m:
            lines.append([int(m.group(1)) * 60 + float(m.group(2)), m.group(3)])

    fixed = 0
    for i in range(len(lines) - 1):
        cap = lines[i + 1][0] - gap
        t0, body = lines[i]
        out, prev = [], t0
        for m in TAG.finditer(body):
            v = int(m.group(1)) * 60 + float(m.group(2))
            nv = max(prev, min(v, cap))
            if abs(nv - v) > 0.001:
                fixed += 1
            out.append((m.start(), m.end(), nv))
            prev = nv
        # 从后往前替换，避免位移
        for s, e, nv in reversed(out):
            body = body[:s] + f"<{fmt(nv)}>" + body[e:]
        lines[i][1] = body

    res = [f"[{fmt(t)}]{b}" for t, b in lines]
    with open(path, "w", encoding="utf-8", newline="\n") as f:
        f.write("\n".join(metas + res) + "\n")
    print(f"[ok] {path} 夹断 {fixed} 个标签")


if __name__ == "__main__":
    main()
