"""按 ASR 分段直接输出 LRC（一行一段），适合没有参考文本的日文歌。"""
import argparse
import json
import re

KEEP_PUNCT = "?!.,、。！？…"


def fmt_ts(sec):
    sec = max(0.0, sec)
    return "[%02d:%06.3f]" % (int(sec // 60), sec % 60)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--words", required=True)
    ap.add_argument("--out", required=True)
    ap.add_argument("--lead", type=float, default=0.15)
    ap.add_argument("--ti", default="")
    ap.add_argument("--ar", default="")
    ap.add_argument("--al", default="")
    ap.add_argument("--by", default="Whisper-ASR")
    args = ap.parse_args()

    d = json.load(open(args.words, encoding="utf-8"))
    header = []
    if args.ti:
        header.append(f"[ti:{args.ti}]")
    if args.ar:
        header.append(f"[ar:{args.ar}]")
    if args.al:
        header.append(f"[al:{args.al}]")
    header.append(f"[by:{args.by}]")

    body, prev = [], -10.0
    for s in d["segments"]:
        text = re.sub(r"\s+", " ", s["text"].strip())
        text = re.sub(r"\s+([?!.,、。！？…])", r"\1", text)
        if not text:
            continue
        st = max(0.0, s["start"] - args.lead)
        if body:
            st = max(st, prev + 0.3)
        else:
            st = max(st, 0.5)
        if st <= prev:
            continue
        body.append(fmt_ts(st) + text)
        prev = st

    with open(args.out, "w", encoding="utf-8", newline="\n") as f:
        f.write("\n".join(header + body) + "\n")
    print(f"[ok] {args.out} {len(body)} 行")
    print("\n".join(body))


if __name__ == "__main__":
    main()
