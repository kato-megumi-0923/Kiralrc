"""逐字歌词的下载源：网易云 yrc、QQ QRC、AMLL TTML、酷狗 KRC。

平台的逐字数据比对齐便宜得多，也准，所以下载优先于跑模型；
拿不到逐字就退回逐行，交给 Kiralrc 自己对齐。
"""

from __future__ import annotations

import json
import re
import time
import urllib.error
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


from .ratelimit import RateLimited  # noqa: E402  （与 text 共用同一个异常）

# 平台要求慢一点（HTTP 405 一类）


def _get(url: str, timeout: int = 20) -> bytes:
    req = urllib.request.Request(url, headers=HEADERS)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            return r.read()
    except urllib.error.HTTPError as e:
        # 405/429 是"问得太勤"，跟"没有这首歌"是两回事
        if e.code in (405, 429, 418):
            raise RateLimited(f"HTTP {e.code}") from e
        raise


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


def from_netease(song_id: int, timeout: int = 20, name: str = "",
                 artist: str = "") -> WordLyric | None:
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
    lines = trim_preamble(lines, name, artist)
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
    # 逐行只是兜底：别的平台也许有逐字，别被排在前面的一档挡住
    fallback: WordLyric | None = None
    for which in order:
        if which == "netease" and not netease_available():
            continue          # 刚被限速过，这一轮先不碰它
        try:
            hit = None
            if which == "netease":
                hit = (from_netease(song_id, name=name, artist=artist) if song_id
                       else netease_by_search(name, artist, duration))
            elif which == "qq" and name:
                hit = from_qq(name, artist, duration)
            elif which == "amll" and song_id:
                hit = from_amll(song_id)
            elif which == "kugou" and name:
                hit = from_kugou(name, artist, duration)
            if hit and len(hit):
                if hit.timed:
                    return hit
                fallback = fallback or hit
        except RateLimited:
            if which == "netease":
                _trip_netease()   # 熔断：换下一个源，别让整批都卡在它身上
                continue
            raise
        except Exception:
            pass
        finally:
            if pause:
                time.sleep(pause)
    return fallback


# 制作人员行：词：X / 作曲：X / 编曲：X …，不是唱出来的
CREDIT = re.compile(
    r"^\s*(作词|作曲|编曲|制作人?|出品|监制|混音|母带|和声|吉他|贝斯|鼓|键盘|弦乐|"
    r"词|曲|演唱|原唱|翻唱|lyrics?|music|composer|arranger|producer|mixed|mastered)\s*[:：]",
    re.I)


def _is_preamble(text: str, name: str, artist: str) -> bool:
    """开头的歌曲信息行与制作人员行：QQ / 酷狗的逐字歌词常带这几行。"""
    head = text.strip()
    if not head:
        return True
    if CREDIT.match(head):
        return True
    flat = re.sub(r"[\s\-–—_·.,，。!！?？'\"、:：~～/]", "", head).lower()
    for candidate in (name, f"{name} {artist}".strip(), f"{artist} {name}".strip(),
                      f"{name} - {artist}", f"{artist} - {name}"):
        c = (candidate or "").strip()
        if not c:
            continue
        if flat == re.sub(r"[\s\-–—_·.,，。!！?？'\"、:：~～/]", "", c).lower():
            return True
    return False


def trim_preamble(lines: list[WordLine], name: str = "", artist: str = "") -> list[WordLine]:
    """去掉开头的歌曲信息与制作人员行，只留真正唱出来的。"""
    out = list(lines)
    while out and _is_preamble(out[0].text, name, artist):
        out.pop(0)
    return out


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


# 网易云一旦说"操作频繁"，后面几十分钟都不必再问它，免得每首都等一轮
_NETEASE_DOWN_UNTIL = {"at": 0.0}
NETEASE_COOLDOWN = 600.0


def netease_available() -> bool:
    return time.time() >= _NETEASE_DOWN_UNTIL["at"]


def _trip_netease(seconds: float = NETEASE_COOLDOWN) -> None:
    _NETEASE_DOWN_UNTIL["at"] = max(_NETEASE_DOWN_UNTIL["at"], time.time() + seconds)


def _post(url: str, payload: dict, timeout: int = 20) -> dict:
    req = urllib.request.Request(
        url, data=json.dumps(payload).encode("utf-8"),
        headers={**HEADERS, "Content-Type": "application/json",
                 "Referer": "https://y.qq.com/"})
    with urllib.request.urlopen(req, timeout=timeout) as r:
        return json.loads(r.read().decode("utf-8", "replace"))


def _pick(cands: list[dict], name: str, artist: str = "",
          duration: float | None = None, tol: float = 3.0) -> dict | None:
    """同名（允许一字之差）、时长在容差内，优先歌手对得上的那一条。"""
    from . import text as tx

    want = tx._fold(name)
    survivors = []
    for c in cands:
        title = tx._fold(str(c.get("name") or ""))
        if title != want and not tx.one_rune_apart(want, title):
            continue
        if duration and c.get("duration"):
            if abs(float(c["duration"]) - float(duration)) > tol:
                continue
        survivors.append(c)
    for c in survivors:
        if artist and tx._artists_alike(str(c.get("artist") or ""), artist) >= 0.6:
            return c
    return survivors[0] if survivors else None


QQ_SEARCH = ("https://c.y.qq.com/soso/fcgi-bin/client_search_cp"
             "?w={keyword}&format=json&n=20&p=1&t=0")
QQ_LYRIC = "https://u.y.qq.com/cgi-bin/musicu.fcg"
QQ_KEY = b"!@#)(*$%123ZXC!@!@#)(NHL"

QRC_CONTENT = re.compile(r'<Lyric_1 LyricType="1" LyricContent="(.*?)"/>', re.S)
QRC_LINE = re.compile(r"^\[(\d+),(\d+)\](.*)$")
QRC_WORD = re.compile(r"\((\d+),(\d+)\)")


def parse_qrc(text: str) -> list[WordLine]:
    """QQ 的 QRC：`[行起点,时长]字(起点,时长)…`，时间都是毫秒。"""
    import html

    m = QRC_CONTENT.search(text)
    if not m:
        return []
    out: list[WordLine] = []
    for raw in html.unescape(m.group(1)).split("\n"):
        g = QRC_LINE.match(raw.rstrip("\r"))
        if not g:
            continue
        body = g.group(3)
        chars: list[tuple[str, float]] = []
        prev = 0
        for w in QRC_WORD.finditer(body):
            piece = body[prev:w.start()]
            at = int(w.group(1)) / 1000
            if piece:
                chars.append((piece, at))
            prev = w.end()
        if not chars:
            continue
        line_text = "".join(c for c, _ in chars).strip()
        if not line_text:
            continue
        out.append(WordLine(start=int(g.group(1)) / 1000, text=line_text, chars=chars,
                            end=(int(g.group(1)) + int(g.group(2))) / 1000))
    out.sort(key=lambda l: l.start)
    return out


def netease_by_search(name: str, artist: str = "", duration: float | None = None,
                      min_score: float = 0.55) -> WordLyric | None:
    """没有 ID 的歌：按歌名+歌手搜一条够像的，再按它的 ID 取词。"""
    from . import text as tx

    keyword = f"{artist} {name}".strip() if artist else name
    cands = tx.netease_candidates(keyword, duration, name=name, artist=artist)
    if not cands or cands[0]["score"] < min_score:
        return None
    return from_netease(int(cands[0]["id"]), name=name, artist=artist)


def from_qq(name: str, artist: str = "", duration: float | None = None) -> WordLyric | None:
    """QQ 音乐：搜歌取 mid，再要 QRC（逐字，密文）与译文（明文 base64）。"""
    from . import qqcipher

    keyword = f"{artist} {name}".strip() if artist else name
    data = _json(QQ_SEARCH.format(keyword=urllib.parse.quote(keyword)))
    songs = (((data.get("data") or {}).get("song") or {}).get("list")) or []
    cands = [{
        "id": s.get("songmid") or "",
        "name": s.get("songname") or "",
        "artist": ",".join(a.get("name", "") for a in (s.get("singer") or [])),
        "duration": int(s.get("interval") or 0),
    } for s in songs if s.get("songmid")]
    hit = _pick(cands, name, artist, duration)
    if not hit:
        return None

    payload = {
        "comm": {"uin": "0", "format": "json", "ct": 11, "cv": "1003006"},
        "req_1": {"module": "music.musichallSong.PlayLyricInfo",
                  "method": "GetPlayLyricInfo",
                  "param": {"songMID": hit["id"], "songID": 0, "qrc": 1,
                            "lrcType": 1, "trans": 1}},
    }
    res = _post(QQ_LYRIC, payload)
    body = ((res.get("req_1") or {}).get("data")) or {}
    encrypted = str(body.get("lyric") or "")
    if not encrypted or encrypted == "0":
        return None
    try:
        import zlib
        plain = zlib.decompress(qqcipher.triple_decrypt(bytes.fromhex(encrypted), QQ_KEY))
        qrc_text = plain.decode("utf-8-sig")
    except Exception:
        return None
    lines = trim_preamble(parse_qrc(qrc_text), name, artist)
    if not lines:
        return None
    trans = body.get("trans") or ""
    if trans:
        import base64
        try:
            attach_translation(lines, base64.b64decode(trans).decode("utf-8-sig"))
        except Exception:
            pass
    return WordLyric(source="QQ音乐", tag=TAGS["qq"], lines=lines, timed=True)


AMLL_BASES = (
    "https://raw.githubusercontent.com/amll-dev/amll-ttml-db/main/ncm-lyrics/",
    "https://gh-proxy.com/https://raw.githubusercontent.com/amll-dev/amll-ttml-db/main/ncm-lyrics/",
    "https://ghfast.top/https://raw.githubusercontent.com/amll-dev/amll-ttml-db/main/ncm-lyrics/",
)


def _ttml_time(value: str | None) -> float | None:
    if not value:
        return None
    try:
        parts = value.strip().split(":")
        sec = 0.0
        for p in parts:
            sec = sec * 60 + float(p)
        return sec
    except Exception:
        return None


def parse_ttml(text: str) -> list[WordLine]:
    """AMLL 的 TTML：<p> 一行，带 begin/end 的 <span> 是一个词。"""
    import xml.etree.ElementTree as ET

    try:
        root = ET.fromstring(text)
    except Exception:
        return []
    out: list[WordLine] = []
    for p in root.iter():
        if p.tag.split("}")[-1] != "p":
            continue
        start = _ttml_time(p.get("begin")) or 0.0
        end = _ttml_time(p.get("end")) or 0.0
        chars: list[tuple[str, float]] = []
        translation = ""
        for span in p.iter():
            if span is p or span.tag.split("}")[-1] != "span":
                continue
            role = span.get("role") or ""
            words = "".join(span.itertext())
            if role == "x-translation":
                translation = (translation + words).strip()
                continue
            if role in ("x-roman", "x-bg"):
                continue
            at = _ttml_time(span.get("begin"))
            if at is None:
                continue
            piece = words
            if piece.strip():
                chars.append((piece, at))
        if not chars:
            continue
        line_text = "".join(c for c, _ in chars).strip()
        if not line_text:
            continue
        out.append(WordLine(start=start, text=line_text, chars=chars, end=end or start,
                            translation=translation))
    out.sort(key=lambda l: l.start)
    return out


def from_amll(song_id: int) -> WordLyric | None:
    """AMLL TTML 库按网易云 ID 存，一次一个镜像地试。"""
    for base in AMLL_BASES:
        try:
            raw = _get(f"{base}{song_id}.ttml", timeout=15)
        except Exception:
            continue
        if not raw:
            continue
        lines = parse_ttml(raw.decode("utf-8", "replace"))
        if lines:
            return WordLyric(source="AMLL", tag=TAGS["amll"], lines=lines, timed=True)
    return None


KRC_KEY = bytes([64, 71, 97, 119, 94, 50, 116, 71, 81, 54, 49, 45, 206, 210, 110, 105])
KRC_LINE = re.compile(r"^\[(\d+),(\d+)\](.*)$")
KRC_WORD = re.compile(r"<(\d+),(\d+),\d+>([^<]*)")
KRC_LANG = re.compile(r"^\[language:([^\]]*)\]")


def decode_krc(content: str) -> str:
    """酷狗的 KRC：base64 → "krc1" → 按固定表异或 → zlib。"""
    import base64
    import zlib

    raw = base64.b64decode(content.strip())
    if len(raw) < 5 or raw[:4] != b"krc1":
        raise ValueError("not a KRC")
    data = bytearray(raw[4:])
    for i in range(len(data)):
        data[i] ^= KRC_KEY[i % len(KRC_KEY)]
    return zlib.decompress(bytes(data)).decode("utf-8-sig")


def _krc_translations(b64: str) -> list[str]:
    import base64

    try:
        payload = json.loads(base64.b64decode(b64).decode("utf-8"))
    except Exception:
        return []
    out = []
    for item in payload.get("content") or []:
        if item.get("type") == 1:
            out.append(str(item.get("lyricContent") or "").strip())
    return out


def parse_krc(text: str) -> list[WordLine]:
    out: list[WordLine] = []
    trans: list[str] = []
    for raw in text.splitlines():
        raw = raw.strip()
        m = KRC_LANG.match(raw)
        if m:
            trans = _krc_translations(m.group(1))
            continue
        g = KRC_LINE.match(raw)
        if not g:
            continue
        start = int(g.group(1)) / 1000
        length = int(g.group(2)) / 1000
        chars: list[tuple[str, float]] = []
        for w in KRC_WORD.finditer(g.group(3)):
            off, dur, piece = int(w.group(1)) / 1000, int(w.group(2)) / 1000, w.group(3)
            if piece:
                chars.append((piece, start + off))
        if not chars:
            continue
        line_text = "".join(c for c, _ in chars).strip()
        if not line_text:
            continue
        out.append(WordLine(start=start, text=line_text, chars=chars, end=start + length))
    for i, line in enumerate(out):
        if i < len(trans) and trans[i]:
            line.translation = trans[i]
    return out


def from_kugou(name: str, artist: str = "", duration: float | None = None) -> WordLyric | None:
    """酷狗：搜歌取 hash，再要 KRC 逐字（歌词链路要它，译文不用）。"""
    keyword = f"{artist} - {name}" if artist else name
    q = urllib.parse.urlencode({
        "ver": "1", "man": "yes", "client": "pc", "keyword": keyword,
        "duration": int((duration or 0) * 1000), "hash": "",
    })
    data = _json(f"http://lyrics.kugou.com/search?{q}", timeout=15)
    cands = [{
        "id": c.get("id") or "",
        "accesskey": c.get("accesskey") or "",
        "name": c.get("song") or "",
        "artist": c.get("singer") or "",
        "duration": (int(c.get("duration") or 0)) / 1000,
    } for c in (data.get("candidates") or [])]
    hit = _pick(cands, name, artist, duration)
    if not hit or not hit["accesskey"]:
        return None
    q = urllib.parse.urlencode({
        "ver": "1", "client": "pc", "id": hit["id"],
        "accesskey": hit["accesskey"], "fmt": "krc", "charset": "utf8",
    })
    data = _json(f"http://lyrics.kugou.com/download?{q}", timeout=15)
    content = data.get("content") or ""
    if not content:
        return None
    try:
        lines = trim_preamble(parse_krc(decode_krc(content)), name, artist)
    except Exception:
        return None
    if not lines:
        return None
    return WordLyric(source="酷狗", tag=TAGS["kugou"], lines=lines, timed=True)
