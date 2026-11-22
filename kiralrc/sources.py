"""逐字歌词的下载源：网易云 yrc、QQ QRC、AMLL TTML、酷狗 KRC。

平台的逐字数据比对齐便宜得多，也准，所以下载优先于跑模型；
拿不到逐字就退回逐行，交给 Kiralrc 自己对齐。
"""

from __future__ import annotations

import json
import re
import time
import urllib.parse
import urllib.request
from dataclasses import dataclass, field

UA = ("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
      "(KHTML, like Gecko) Chrome/131.0 Safari/537.36")
HEADERS = {"User-Agent": UA, "Referer": "https://music.163.com/"}

YRC_LINE = re.compile(r"^\[(\d+),(\d+)\]")
YRC_WORD = re.compile(r"\((\d+),(\d+),\d+\)([^(]*)")
LRC_LINE = re.compile(r"^\[(\d{1,3}):(\d{1,2}(?:[.:]\d{1,3})?)\]")
META_LINE = re.compile(r"^\[(ti|ar|al|by|offset|length|re|ve|tool):", re.I)

# 歌词来自哪，写进 [by:] 里，扫描时用它区分「官方逐字」与「对齐逐字」
TAGS = {"netease": "NeteaseCloud", "qq": "QQMusic", "amll": "AMLL", "kugou": "Kugou"}


@dataclass
class WordLine:
    start: float
    text: str
    chars: list[tuple[str, float]] = field(default_factory=list)  # (字, 起始秒)
    end: float = 0.0
    translation: str = ""

    @property
    def timed(self) -> bool:
        return len(self.chars) >= 2


@dataclass
class WordLyric:
    source: str            # 人看的名字：网易云 / QQ音乐 / AMLL / 酷狗
    tag: str               # [by:] 的值
    lines: list[WordLine] = field(default_factory=list)
    timed: bool = False    # 有字级时间

    def __len__(self) -> int:
        return len(self.lines)


class RateLimited(RuntimeError):
    """平台要求慢一点（HTTP 405 一类）。"""


def _get(url: str, timeout: int = 20) -> bytes:
    req = urllib.request.Request(url, headers=HEADERS)
    with urllib.request.urlopen(req, timeout=timeout) as r:
        return r.read()


def _json(url: str, timeout: int = 20):
    body = _get(url, timeout)
    return json.loads(body.decode("utf-8", "replace"))


def parse_yrc(text: str) -> list[WordLine]:
    """网易云 yrc：`[行起点,行时长](字起点,字时长,0)字…`"""
    out: list[WordLine] = []
    for raw in text.splitlines():
        m = YRC_LINE.match(raw)
        if not m:
            continue
        start, dur = int(m.group(1)) / 1000, int(m.group(2)) / 1000
        body = raw[m.end():]
        chars: list[tuple[str, float]] = []
        for w in YRC_WORD.finditer(body):
            at = int(w.group(1)) / 1000
            piece = w.group(3)
            if not piece.strip():
                if chars:
                    chars[-1] = (chars[-1][0] + piece, chars[-1][1])
                continue
            chars.append((piece, at))
        if not chars:
            continue
        text_line = "".join(c for c, _ in chars).strip()
        if not text_line:
            continue
        out.append(WordLine(start=start, text=text_line, chars=chars, end=start + dur))
    out.sort(key=lambda l: l.start)
    return out


def parse_lrc(text: str) -> list[tuple[float, str]]:
    """普通 LRC：[(起点秒, 行)]。"""
    out = []
    for raw in text.splitlines():
        if META_LINE.match(raw):
            continue
        m = LRC_LINE.match(raw)
        if not m:
            continue
        body = raw[m.end():]
        sec = int(m.group(1)) * 60 + float(m.group(2).replace(":", "."))
        body = body.strip()
        if body:
            out.append((sec, body))
    out.sort(key=lambda x: x[0])
    return out


def _spread(text: str, start: float, end: float) -> list[tuple[str, float]]:
    """逐行歌词没有字级时间：平均摊开，好让播放器有个落点。"""
    chars = [c for c in text if not c.isspace()]
    if not chars or end <= start:
        return []
    step = (end - start) / len(chars)
    return [(c, start + i * step) for i, c in enumerate(chars)]


def attach_translation(lines: list[WordLine], tlyric: str) -> None:
    """把译文挂到时间最接近的行上（网易云的逐字与逐行差着几百毫秒）。"""
    for sec, text in parse_lrc(tlyric):
        best, gap = None, 1.5
        for line in lines:
            d = abs(line.start - sec)
            if d < gap:
                best, gap = line, d
        if best is not None and not best.translation:
            best.translation = text


def netease_lyric(song_id: int, timeout: int = 20) -> dict:
    """网易云歌词：yrc 逐字、lrc 逐行、tlyric 译文。"""
    url = ("https://music.163.com/api/song/lyric?"
           f"id={song_id}&lv=-1&kv=-1&tv=-1&yv=-1&rv=-1")
    data = _json(url, timeout)
    if data.get("code") not in (200, None):
        raise RateLimited(f"网易云返回 code={data.get('code')}")
    return {
        "yrc": ((data.get("yrc") or {}).get("lyric") or "").strip(),
        "lrc": ((data.get("lrc") or {}).get("lyric") or "").strip(),
        "tlyric": ((data.get("tlyric") or {}).get("lyric") or "").strip(),
    }


def from_netease(song_id: int, timeout: int = 20) -> WordLyric | None:
    """按 ID 取网易云歌词：有逐字用逐字，否则逐行；译文一并挂上。"""
    raw = netease_lyric(song_id, timeout)
    lines = parse_yrc(raw["yrc"])
    timed = bool(lines)
    if not timed:
        timed_lines = parse_lrc(raw["lrc"])
        if not timed_lines:
            return None
        lines = []
        for i, (sec, text) in enumerate(timed_lines):
            end = timed_lines[i + 1][0] if i + 1 < len(timed_lines) else sec + 6
            lines.append(WordLine(start=sec, text=text, end=min(end, sec + 6)))
    if len(lines) <= 2 and PLACEHOLDER.search("".join(l.text for l in lines)):
        return None
    if raw["tlyric"]:
        attach_translation(lines, raw["tlyric"])
    return WordLyric(source="网易云", tag=TAGS["netease"], lines=lines, timed=timed)


def fetch(song_id: int | None, name: str = "", artist: str = "",
          duration: float | None = None, sources: tuple[str, ...] | None = None,
          pause: float = 0.6) -> WordLyric | None:
    """按顺序问各个源，第一个给出歌词的算数。

    顺序默认跟 Cloudy 一致：网易云 → QQ → AMLL → 酷狗；
    网易云只认 ID（它搜索不可靠，ID 我们已经能从标签里拿到）。
    """
    order = sources or ("netease", "qq", "amll", "kugou")
    for which in order:
        try:
            hit = None
            if which == "netease" and song_id:
                hit = from_netease(song_id)
            elif which == "qq" and name:
                hit = from_qq(name, artist, duration)
            elif which == "amll" and song_id:
                hit = from_amll(song_id)
            elif which == "kugou" and name:
                hit = from_kugou(name, artist, duration)
            if hit and len(hit):
                return hit
        except RateLimited:
            raise
        except Exception:
            pass
        finally:
            if pause:
                time.sleep(pause)
    return None


PLACEHOLDER = re.compile(r"纯音乐|請欣賞|请欣赏|暂无歌词|没有歌词|此歌曲为没有填词的纯音乐", re.I)


def to_lrc(lyric: WordLyric, ti: str = "", ar: str = "", al: str = ""):
    """下载到的歌词 → Kiralrc 的 Lrc 结构，译文按同时间戳单独成行。"""
    from . import format as fmt

    metas = [x for x in (
        f"[ti:{ti}]" if ti else "",
        f"[ar:{ar}]" if ar else "",
        f"[al:{al}]" if al else "",
        f"[by:{lyric.tag}]",
    ) if x]
    out = fmt.Lrc(metas=metas)
    for ln in lyric.lines:
        chars = list(ln.chars) if ln.timed else []
        end = ln.end
        if chars:
            end = max(end, chars[-1][1])
        out.lines.append(fmt.Line(start=ln.start, text=ln.text, chars=chars, end=end or None))
        if ln.translation.strip():
            out.lines.append(fmt.Line(start=ln.start, text=ln.translation.strip()))
    return out


def from_qq(name: str, artist: str = "", duration: float | None = None) -> WordLyric | None:
    """QQ 音乐 QRC（待实现）。"""
    return None


def from_amll(song_id: int) -> WordLyric | None:
    """AMLL TTML（待实现）。"""
    return None


def from_kugou(name: str, artist: str = "", duration: float | None = None) -> WordLyric | None:
    """酷狗 KRC（待实现）。"""
    return None
