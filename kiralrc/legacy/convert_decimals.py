"""把 LRC 的两位小数时间戳补成三位（值为精确等价，仅在末尾补 0）。"""
import re
import sys

TS2 = re.compile(r"([\[<]\d{2}:\d{2}\.\d{2})([\]>])")


def main():
    for path in sys.argv[1:]:
        raw = open(path, "rb").read()
        text = raw.decode("utf-8")
        new, n = TS2.subn(r"\g<1>0\g<2>", text)
        if n:
            with open(path, "wb") as f:
                f.write(new.encode("utf-8"))
        print(f"[ok] {path} 转换 {n} 个时间戳")


if __name__ == "__main__":
    main()
