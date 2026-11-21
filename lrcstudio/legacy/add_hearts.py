"""给双语 LRC 的「中文翻译行」前后加 ❤️（保持其余字节与换行风格不变）。"""
import re
import shutil
import sys

LINE = re.compile(r"^\[(\d{2}):(\d{2}\.\d{2,3})\](<[^>]+>)(.*)(<[^>]+>)$")
CJK = re.compile(r"[\u4e00-\u9fff]")
SKIP = ("著作权", "作词", "作曲", "词：", "曲：")


def main():
    path = sys.argv[1]
    dry = "--dry" in sys.argv
    shutil.copy2(path, sys.argv[2]) if len(sys.argv) > 2 and not dry else None

    raw = open(path, "rb").read()
    text = raw.decode("utf-8")
    nl = "\r\n" if "\r\n" in text else "\n"
    lines = text.split(nl)

    out, changed = [], 0
    for ln in lines:
        m = LINE.match(ln)
        if m:
            body = m.group(4)
            if ("<" not in body and ">" not in body
                    and CJK.search(body)
                    and not any(k in body for k in SKIP)
                    and not body.startswith("❤️")):
                ln = f"[{m.group(1)}:{m.group(2)}]{m.group(3)}❤️{body}❤️{m.group(5)}"
                changed += 1
        out.append(ln)

    print(f"[{'dry-run' if dry else 'ok'}] {changed} 行加上 ❤️")
    for ln in out:
        if "❤️" in ln:
            print("   ", ln[:40] + ("..." if len(ln) > 40 else ""), "|", ln[-30:])

    if not dry:
        with open(path, "wb") as f:
            f.write(nl.join(out).encode("utf-8"))


if __name__ == "__main__":
    main()
