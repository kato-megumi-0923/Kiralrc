"""双语歌词：给「中文翻译行」前后加 ❤️（保持其余字节不变）。"""
from __future__ import annotations

import re
from pathlib import Path

from . import format as fmt

CJK = re.compile(r"[\u4e00-\u9fff]")
SKIP = ("著作权", "作词", "作曲", "词：", "曲：")


def is_translation_line(ln: fmt.Line, prev: fmt.Line | None = None) -> bool:
    """识别带首尾标签的译文，以及紧跟同时间戳逐字行的整行译文。"""
    if ln.enhanced:
        return False
    plain_translation = (
        ln.tag_count == 0 and prev is not None and prev.enhanced
        and abs(round(ln.start * 1000) - round(prev.start * 1000)) < 60
    )
    if ln.tag_count != 2 and not plain_translation:
        return False
    if not CJK.search(ln.text):
        return False
    if any(k in ln.text for k in SKIP):
        return False
    return not ln.text.lstrip().startswith("❤️")


def wrap(path: str | Path) -> int:
    """就地加 ❤️，返回改动的行数。"""
    raw = Path(path).read_bytes()
    text = raw.decode("utf-8")
    out, changed = [], 0
    prev = None
    for line in text.splitlines(keepends=True):
        stripped = line.lstrip("\ufeff \t")
        parsed = fmt.parse(stripped + "\n")
        ln = parsed.lines[0] if len(parsed.lines) == 1 else None
        stamp = fmt.STAMP_RE.match(stripped)
        if ln is not None and stamp is not None and is_translation_line(ln, prev):
            start, end = len(line) - len(stripped) + stamp.end(), len(line)
            if ln.tag_count == 2:
                first, last = fmt.TAG_RE.finditer(line, start)
                start, end = first.end(), last.start()
            body = line[start:end]
            left = start + len(body) - len(body.lstrip())
            right = start + len(body.rstrip())
            line = line[:left] + "❤️" + line[left:right] + "❤️" + line[right:]
            changed += 1
        out.append(line)
        prev = ln
    if changed:
        Path(path).write_bytes("".join(out).encode("utf-8"))
    return changed
