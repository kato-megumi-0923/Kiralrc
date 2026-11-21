"""修整逐字 LRC 的句尾：末字至少显示 0.10s；只在重叠过大（>0.45s）时才夹断。"""
import re
import sys

LINE = re.compile(r"^\[(\d{2}):(\d{2}\.\d{2,3})\](.*)$")
TAG = re.compile(r"<(\d{2}):(\d{2}\.\d{2,3})>")
META = re.compile(r"^\[(ti|ar|al|by|tool|offset|length):", re.I)
MIN_TAIL = 0.10
MAX_OVERLAP = 0.45


def fmt(sec):
    sec = max(0.0, sec)
    return "%02d:%06.3f" % (int(sec // 60), sec % 60)


def main():
    path = sys.argv[1]
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

    changed = 0
    for i, (t0, body) in enumerate(lines):
        tags = [(m.start(), m.end(), int(m.group(1)) * 60 + float(m.group(2)))
                for m in TAG.finditer(body)]
        if len(tags) < 2:
            continue
        starts = [t[2] for t in tags[:-1]]
        end = tags[-1][2]
        new_end = max(end, starts[-1] + MIN_TAIL)

        if i + 1 < len(lines):
            nxt = lines[i + 1][0]
            if new_end > nxt + MAX_OVERLAP:
                # 重叠过大：把越界的字起点一起拉回下一句之前
                cap = nxt - 0.02
                new_starts, prev = [], t0
                for s in starts:
                    s = max(prev, min(s, cap))
                    new_starts.append(s)
                    prev = s
                if new_starts != starts:
                    out, prev = [], t0
                    for (s0, e0, _), s in zip(tags[:-1], new_starts):
                        out.append((s0, e0, s))
                    for s0, e0, nv in reversed(out):
                        body = body[:s0] + f"<{fmt(nv)}>" + body[e0:]
                    starts = new_starts
                    tags = [(m.start(), m.end(), int(m.group(1)) * 60 + float(m.group(2)))
                            for m in TAG.finditer(body)]
                    changed += 1
                new_end = max(new_starts[-1] + MIN_TAIL, min(new_end, cap))

        if abs(new_end - end) > 0.001:
            s, e, _ = tags[-1]
            body = body[:s] + f"<{fmt(new_end)}>" + body[e:]
            changed += 1
        lines[i][1] = body

    with open(path, "w", encoding="utf-8", newline="\n") as f:
        f.write("\n".join(metas + [f"[{fmt(t)}]{b}" for t, b in lines]) + "\n")
    print(f"[ok] {path} 调整 {changed} 处句尾")


if __name__ == "__main__":
    main()
