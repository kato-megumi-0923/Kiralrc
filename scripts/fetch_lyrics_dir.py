"""按目录批量抓纯歌词（只要文字、不要时间轴），存成同名 .txt。

用法：
    python scripts/fetch_lyrics_dir.py "H:\\无歌词" [--interval 2.0] [--force]

特性：
- 已存在 .txt 的跳过（可中断续跑）
- 按文件名清洗出搜索词，用音频时长挑最接近的条目
- 双语歌词只保留原词（同一时间戳的第二行是译文）
- 失败不中断，最后汇总 & 写 抓取结果.csv
"""
from __future__ import annotations

import argparse
import csv
import re
import sys
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from kiralrc import text as tx  # noqa: E402

AUD = {".mp3", ".flac", ".wav", ".m4a", ".ogg", ".ape", ".aac", ".wma", ".opus"}
TAG = re.compile(r"<\d{2}:\d{2}\.\d{2,3}>")
STAMP = re.compile(r"\[\d{2}:\d{2}\.\d{2,3}\]")
META = re.compile(r"^\[(ti|ar|al|by|tool|offset|length|re|ve):", re.I)
CREDIT = re.compile(r"^\s*(作词|作曲|编曲|制作人?|出品|监制|混音|母带|录音|吉他|贝斯|鼓|键盘|"
                    r"和声|弦乐|OP|SP|发行|策划|统筹|封面|翻译)\s*[:：/]")


def duration(path: Path) -> float:
    try:
        import av
        with av.open(str(path)) as c:
            return float(c.duration) / av.time_base if c.duration else 0.0
    except Exception:
        return 0.0


def to_plain(lrc_text: str) -> list[str]:
    """LRC → 纯歌词行：去时间戳/标签/元信息/译文/制作人员。"""
    out, last_t = [], None
    for raw in lrc_text.splitlines():
        s = raw.strip()
        if not s or META.match(s):
            continue
        m = re.match(r"^\[(\d{2}):(\d{2}\.\d{2,3})\](.*)$", s)
        if not m:
            continue
        t = int(m.group(1)) * 60 + float(m.group(2))
        body = TAG.sub("", m.group(3)).strip()
        if not body or CREDIT.match(body):
            continue
        if last_t is not None and abs(t - last_t) < 0.06:   # 同时间戳 = 译文
            continue
        out.append(body)
        last_t = t
    return out


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("folder")
    ap.add_argument("--interval", type=float, default=2.0)
    ap.add_argument("--force", action="store_true")
    a = ap.parse_args()

    root = Path(a.folder)
    files = sorted(p for p in root.rglob("*")
                   if p.suffix.lower() in AUD and p.is_file())
    print(f"共 {len(files)} 首，开始抓词（间隔 {a.interval}s）\n")

    rows, ok, skip, fail = [], 0, 0, 0
    for i, p in enumerate(files, 1):
        txt = p.with_suffix(".txt")
        if txt.exists() and txt.stat().st_size > 8 and not a.force:
            skip += 1
            print(f"[{i:2}/{len(files)}] 跳过（已存在） {p.name[:42]}")
            continue

        kw = tx.query_from_filename(p.stem)
        dur = duration(p)
        hit = tx.best_match(kw, dur, interval=a.interval)
        if not hit:
            fail += 1
            rows.append([str(p.relative_to(root)), kw, "未找到", 0])
            print(f"[{i:2}/{len(files)}] ✗ 未找到  {kw}")
            continue

        lines = to_plain(hit["lrc"])
        if len(lines) < 4:
            fail += 1
            rows.append([str(p.relative_to(root)), kw, "歌词太少", len(lines)])
            print(f"[{i:2}/{len(files)}] ✗ 只有 {len(lines)} 行  {kw}")
            continue

        txt.write_text("\n".join(lines) + "\n", encoding="utf-8", newline="\n")
        ok += 1
        rows.append([str(p.relative_to(root)), kw,
                     f"{hit['artist']} - {hit['name']}", len(lines)])
        print(f"[{i:2}/{len(files)}] ✓ {len(lines):3}行  {hit['name'][:34]}")
        time.sleep(a.interval)

    with open(root / "抓取结果.csv", "w", encoding="utf-8-sig", newline="") as f:
        w = csv.writer(f)
        w.writerow(["相对路径", "搜索词", "匹配到的歌曲", "行数"])
        w.writerows(rows)

    print(f"\n完成：成功 {ok} / 跳过 {skip} / 失败 {fail}，清单见 {root / '抓取结果.csv'}")
    return 0 if fail == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
