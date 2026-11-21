"""命令行入口：python -m kiralrc <命令>"""
from __future__ import annotations

import argparse
import sys
from pathlib import Path

from . import config, format as fmt


def cmd_align(a) -> int:
    from . import pipeline

    src = Path(a.audio)
    if not src.exists():
        print(f"找不到音频: {src}")
        return 2
    lrc, note = pipeline.make_lrc(
        src, ref=a.ref, lang=a.lang, query=a.query,
        ti=a.ti or src.stem, ar=a.ar or "", al=a.al or "")
    out = Path(a.out) if a.out else src.with_suffix(".lrc")
    if a.dry:
        print(lrc.render())
        print(f"\n[来源] {note}\n[dry-run] 未写入 {out}")
        return 0
    fmt.write(out, lrc)
    probs = fmt.validate_enhanced(out)
    print(f"[ok] {out}  {len(lrc.lines)} 行  | {note}")
    for p in probs:
        print("  -", p)
    return 0


def cmd_validate(a) -> int:
    bad = 0
    for f in a.files:
        probs = fmt.validate_enhanced(f, max_jump=a.max_jump)
        lrc = fmt.parse(f)
        print(f"=== {f}\n  逐字行 {sum(1 for l in lrc.lines if l.chars)} / {len(lrc.lines)}")
        if probs:
            bad += 1
            for p in probs:
                print("  -", p)
        else:
            print("  ✅ 通过")
    return 1 if bad else 0


def cmd_hearts(a) -> int:
    from . import hearts

    for f in a.files:
        n = hearts.wrap(f)
        print(f"[ok] {f}  加了 {n} 行 ❤️")
    return 0


def cmd_convert(a) -> int:
    for f in a.files:
        n = fmt.convert_decimals(f)
        print(f"[ok] {f}  转换 {n} 个时间戳")
    return 0


def cmd_separate(a) -> int:
    from . import audio as au

    out = Path(a.out) if a.out else Path(a.audio).with_suffix("")
    res = au.separate(a.audio, out)
    for k, v in res.items():
        print(f"[ok] {k}: {v}")
    return 0


def cmd_fetch(a) -> int:
    from . import text as tx

    hit = tx.best_match(a.query, a.duration)
    if not hit:
        print("没搜到")
        return 1
    print(f"{hit['artist']} - {hit['name']}  {hit['duration']}s  id={hit['id']}  行数={hit['lines']}")
    if a.out:
        Path(a.out).write_text(hit["lrc"], encoding="utf-8")
        print(f"[ok] 写入 {a.out}")
    else:
        print(hit["lrc"][:1200])
    return 0


def cmd_info(a) -> int:
    print(f"项目根目录 : {config.ROOT}")
    for key in ("sofa_ckpt", "sofa_root", "sofa_python", "sofa_dict",
                "whisper_model", "work_dir"):
        try:
            p = config.path(key)
            print(f"{key:<13}: {'存在' if p.exists() else '缺失'}  {p}")
        except KeyError as e:
            print(f"{key:<13}: 未配置 ({e})")
    print(f"默认输出小数位: {config.opt('decimals', 3)}")
    return 0


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(prog="kiralrc", description="歌词逐字对齐工具")
    sub = ap.add_subparsers(dest="cmd", required=True)

    p = sub.add_parser("align", help="音频 → 逐字 LRC")
    p.add_argument("audio")
    p.add_argument("--ref", help="参考歌词（LRC 或纯文本）")
    p.add_argument("--lang", default="zh")
    p.add_argument("--query", help="网易云搜索关键词，默认用文件名")
    p.add_argument("--ti", default="")
    p.add_argument("--ar", default="")
    p.add_argument("--al", default="")
    p.add_argument("--out")
    p.add_argument("--dry", action="store_true")
    p.set_defaults(func=cmd_align)

    p = sub.add_parser("validate", help="校验逐字 LRC")
    p.add_argument("files", nargs="+")
    p.add_argument("--max-jump", type=float, default=3.0)
    p.set_defaults(func=cmd_validate)

    p = sub.add_parser("hearts", help="双语歌词给翻译行加 ❤️")
    p.add_argument("files", nargs="+")
    p.set_defaults(func=cmd_hearts)

    p = sub.add_parser("convert", help="两位小数 → 三位小数")
    p.add_argument("files", nargs="+")
    p.set_defaults(func=cmd_convert)

    p = sub.add_parser("separate", help="demucs 人声分离")
    p.add_argument("audio")
    p.add_argument("--out")
    p.set_defaults(func=cmd_separate)

    p = sub.add_parser("fetch", help="从网易云抓歌词")
    p.add_argument("query")
    p.add_argument("--duration", type=float)
    p.add_argument("--out")
    p.set_defaults(func=cmd_fetch)

    p = sub.add_parser("info", help="检查环境与路径")
    p.set_defaults(func=cmd_info)

    a = ap.parse_args(argv)
    return a.func(a)


if __name__ == "__main__":
    sys.exit(main())
