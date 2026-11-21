"""LRC 时间戳的解析、格式化、转换与校验。

约定（本项目统一）：
- 时间为 `mm:ss.SSS`（三位小数）
- 逐字（enhanced）行写法：`[mm:ss.SSS]<mm:ss.SSS>字<mm:ss.SSS>字...<mm:ss.SSS>`
  末尾那个标签没有字跟着，表示**该行最后一个字的收尾时间**；
  少了它播放器会把末字起点当行尾，字还没唱完就被切掉。
"""
from __future__ import annotations

import re
from dataclasses import dataclass, field

LINE_RE = re.compile(r"^\[(\d{2}):(\d{2}\.\d{2,3})\](.*)$")
TAG_RE = re.compile(r"<(\d{2}):(\d{2}\.\d{2,3})>")
META_RE = re.compile(r"^\[(ti|ar|al|by|tool|offset|length|re|ve):", re.I)
TS2_RE = re.compile(r"([\[<]\d{2}:\d{2}\.\d{2})([\]>])")


def fmt_ts(sec: float, tag: bool = False) -> str:
    sec = max(0.0, sec)
    body = "%02d:%06.3f" % (int(sec // 60), sec % 60)
    return f"<{body}>" if tag else f"[{body}]"


def to_sec(mm: str, ss: str) -> float:
    return int(mm) * 60 + float(ss)


@dataclass
class Line:
    start: float
    text: str
    chars: list[tuple[str, float]] = field(default_factory=list)  # (字, 起始秒)
    end: float | None = None
    tag_count: int = 0

    @property
    def enhanced(self) -> bool:
        """逐字行：除首尾两个时间标签外，行内还有逐字标签。

        不能用「标签数 >= 字数」判断——含 "My " 这类多字符 token 的行，
        一个标签会盖住多个字符。
        """
        return self.tag_count > 2

    def render(self) -> str:
        if not self.enhanced:
            # 有的文件写成 [行首]<首>文本<尾>，首标签保留原样
            head = fmt_ts(self.start, tag=True) if self.tag_count >= 1 else ""
            body = f"{fmt_ts(self.start)}{head}{self.text}"
            if self.end is not None:
                body += fmt_ts(self.end, tag=True)
            return body
        body = fmt_ts(self.start) + "".join(fmt_ts(t, tag=True) + c for c, t in self.chars)
        if self.end is not None:
            body += fmt_ts(self.end, tag=True)
        return body


@dataclass
class Lrc:
    metas: list[str] = field(default_factory=list)
    lines: list[Line] = field(default_factory=list)

    def render(self) -> str:
        return "\n".join(self.metas + [ln.render() for ln in self.lines]) + "\n"


def parse(path_or_text: str | bytes) -> Lrc:
    """解析 LRC：文件路径或文本内容都行。"""
    if isinstance(path_or_text, bytes):
        text = path_or_text.decode("utf-8-sig")
    elif "\n" in path_or_text:
        text = path_or_text
    else:
        raw = open(path_or_text, "rb").read()
        text = raw.decode("utf-8-sig")

    lrc = Lrc()
    for raw_line in text.replace("\r\n", "\n").split("\n"):
        s = raw_line.strip()
        if not s:
            continue
        if META_RE.match(s):
            lrc.metas.append(s)
            continue
        m = LINE_RE.match(s)
        if not m:
            continue
        start = to_sec(m.group(1), m.group(2))
        body = m.group(3)
        tags = list(TAG_RE.finditer(body))
        if not tags:
            lrc.lines.append(Line(start, body.strip()))
            continue
        plain = TAG_RE.sub("", body)
        enhanced = len(tags) > 2
        chars = []
        if enhanced:
            for idx, ch in enumerate(plain):
                t = (to_sec(tags[idx].group(1), tags[idx].group(2))
                     if idx < len(tags) else (chars[-1][1] if chars else start))
                chars.append((ch, t))
        # 行尾标签：逐字行是「字数 + 1 个标签」，纯翻译行则是「首 + 尾」两个标签
        end = None
        if len(tags) >= 2 and (enhanced or len(tags) == 2):
            end = to_sec(tags[-1].group(1), tags[-1].group(2))
        lrc.lines.append(Line(start, plain, chars, end, len(tags)))
    return lrc


def write(path: str | Path, lrc: Lrc, newline: str = "\n") -> None:
    data = newline.join(lrc.metas + [ln.render() for ln in lrc.lines]) + newline
    with open(path, "w", encoding="utf-8", newline="") as f:
        f.write(data)


def convert_decimals(path: str | Path) -> int:
    """两位 → 三位小数（末尾补 0，数值等价）。返回改动的时间戳个数。"""
    raw = open(path, "rb").read()
    text = raw.decode("utf-8")
    new, n = TS2_RE.subn(r"\g<1>0\g<2>", text)
    if n:
        with open(path, "wb") as f:
            f.write(new.encode("utf-8"))
    return n


def validate_enhanced(path: str | Path, max_jump: float = 3.0,
                      max_chars: int = 30, overlap: float = 0.15) -> list[str]:
    """按逐字格式检查，返回问题列表（空 = 通过）。"""
    raw = open(path, "rb").read()
    probs: list[str] = []
    if raw.startswith(b"\xef\xbb\xbf"):
        probs.append("有 BOM")
    if b"\r\n" in raw:
        probs.append("CRLF 换行（我生成的应为 LF，LDDC 原件保留 CRLF 属正常）")

    lrc = parse(raw)
    if not lrc.lines:
        return ["没有解析到时间戳行"]

    enhanced = [ln for ln in lrc.lines if ln.enhanced]
    if not enhanced:
        return ["整篇没有逐字标签（可能只是整行 LRC）"]

    ends = 0
    for i, ln in enumerate(lrc.lines):
        if len(ln.text) > max_chars:
            probs.append(f"过长 {len(ln.text)} 字: {ln.text[:20]}")
        if not ln.enhanced:
            continue          # 双语文件里的译文行，不做逐字检查
        if abs(ln.chars[0][1] - ln.start) > 0.011:
            probs.append(f"行首 {ln.start:.3f} 与首标签 {ln.chars[0][1]:.3f} 不一致: {ln.text[:16]}")
        if ln.end is not None:
            ends += 1
            if ln.end <= ln.chars[-1][1]:
                probs.append(f"行尾标签 {ln.end:.3f} 未晚于末字 {ln.chars[-1][1]:.3f}: {ln.text[:16]}")
        prev = None
        for _, t in ln.chars:
            if prev is not None:
                if t < prev - 0.001:
                    probs.append(f"标签倒序 {prev:.3f}->{t:.3f}: {ln.text[:16]}")
                elif t - prev > max_jump:
                    probs.append(f"行内跳变 {t - prev:.1f}s（可能是长音）: {ln.text[:16]}")
            prev = t
        if i and lrc.lines[i - 1].start >= ln.start:
            probs.append(f"第 {i+1} 行时间未递增")
        if i and lrc.lines[i - 1].end and lrc.lines[i - 1].end > ln.start + overlap:
            probs.append(f"第 {i} 行末字 {lrc.lines[i-1].end:.3f} 越过下一行行首 {ln.start:.3f}")
    if ends == 0:
        probs.append("所有行都缺少行尾结束标签")
    return probs
