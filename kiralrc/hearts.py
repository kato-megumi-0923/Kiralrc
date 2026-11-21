"""双语歌词：给「中文翻译行」前后加 ❤️（保持其余字节不变）。"""
from __future__ import annotations

import re
from pathlib import Path

from . import format as fmt

CJK = re.compile(r"[\u4e00-\u9fff]")
SKIP = ("著作权", "作词", "作曲", "词：", "曲：")


def is_translation_line(ln: fmt.Line) -> bool:
    """翻译行：整行只有首尾两个标签（没有逐字），含中文，且不是版权/创作信息。"""
    if ln.enhanced:
        return False
    if ln.tag_count != 2:      # 只处理「[行首]<首>文本<尾>」这种
        return False
    if not CJK.search(ln.text):
        return False
    if any(k in ln.text for k in SKIP):
        return False
    return not ln.text.startswith("❤️")


def wrap(path: str | Path) -> int:
    """就地加 ❤️，返回改动的行数。"""
    raw = Path(path).read_bytes()
    text = raw.decode("utf-8")
    nl = "\r\n" if "\r\n" in text else "\n"
    out, changed = [], 0
    for line in text.split(nl):
        parsed = fmt.parse(line + "\n")
        if len(parsed.lines) == 1 and is_translation_line(parsed.lines[0]):
            ln = parsed.lines[0]
            # 原文件是 [行首]<首>文本<尾> 的写法，保留那个首标签
            body = fmt.fmt_ts(ln.start)
            if ln.tag_count >= 1:
                body += fmt.fmt_ts(ln.start, tag=True)
            body += "❤️" + ln.text + "❤️"
            if ln.end is not None:
                body += fmt.fmt_ts(ln.end, tag=True)
            line = body
            changed += 1
        out.append(line)
    if changed:
        Path(path).write_bytes(nl.join(out).encode("utf-8"))
    return changed
