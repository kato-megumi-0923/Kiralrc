"""歌词文本：网易云抓取、参考文本读取、汉字→拼音。"""
from __future__ import annotations

import json
import re
import time
import urllib.parse
import urllib.request
from pathlib import Path

HAN = re.compile(r"[\u4e00-\u9fff]")
KANA = re.compile(r"[\u3040-\u30ff]")
UA = ("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
      "(KHTML, like Gecko) Chrome/120 Safari/537.36")
HEADERS = {"User-Agent": UA, "Referer": "https://music.163.com/"}
META_RE = re.compile(r"^\[(ti|ar|al|by|tool|offset|length):", re.I)
TRACK_RE = re.compile(r"^\s*\d{1,3}\s*[-._]\s*")
BRACKET_RE = re.compile(r"[\(\[（【][^\)\]）】]*[\)\]）】]")
DASH_RE = re.compile(r"\s*[-–—]{1,}\s*")
CREDIT_RE = re.compile(r"^\s*(作词|作曲|编曲|制作|出品|监制|混音|母带|演唱|OP|SP)\s*[:：]")


def _get_json(url: str, tries: int = 3, timeout: int = 20):
    for _ in range(tries):
        try:
            req = urllib.request.Request(url, headers=HEADERS)
            with urllib.request.urlopen(req, timeout=timeout) as r:
                return json.loads(r.read().decode("utf-8"))
        except Exception:
            time.sleep(3)
    return None


def netease_search(keyword: str, limit: int = 5) -> list[dict]:
    url = ("https://music.163.com/api/search/get/web?s=%s&type=1&limit=%d"
           % (urllib.parse.quote(keyword), limit))
    data = _get_json(url)
    if not data or data.get("code") != 200:
        return []
    songs = (data.get("result") or {}).get("songs") or []
    return [{"id": s["id"], "name": s["name"],
             "artist": ",".join(a["name"] for a in s.get("artists", [])),
             "duration": round((s.get("duration") or 0) / 1000)}
            for s in songs]


def netease_lyric(song_id: int) -> tuple[str, str]:
    """返回 (原文 LRC, 翻译 LRC)。匿名请求注意限速，否则会 405。"""
    url = f"https://music.163.com/api/song/lyric?id={song_id}&lv=1&kv=1&tv=-1"
    data = _get_json(url) or {}
    return ((data.get("lrc") or {}).get("lyric") or "",
            (data.get("tlyric") or {}).get("lyric") or "")


def best_match(keyword: str, duration: float | None = None, interval: float = 4.5) -> dict | None:
    """按曲名搜索并挑时长最接近的条目。"""
    songs = netease_search(keyword)
    if not songs:
        return None
    if duration:
        songs.sort(key=lambda s: abs(s["duration"] - duration))
    else:
        songs.sort(key=lambda s: s["duration"])
    time.sleep(interval)
    for cand in songs[:3]:
        lrc, tlrc = netease_lyric(cand["id"])
        time.sleep(interval)
        n = len([x for x in lrc.splitlines() if x.strip()])
        if n >= 8:
            return {**cand, "lrc": lrc, "tlyric": tlrc, "lines": n}
    return None


def ref_lines(path: str | Path) -> list[str]:
    """从 LRC 或纯文本里取歌词行（去掉时间戳与元信息）。"""
    text = open(path, encoding="utf-8-sig").read()
    out = []
    for raw in text.splitlines():
        t = raw.strip()
        if not t or t == "---":
            continue
        if t.startswith("[") and "]" in t:
            t = t.split("]", 1)[1].strip()
            if not t:
                continue
        if META_RE.match(t) or CREDIT_RE.match(t):
            continue
        out.append(t)
    return out


def han_chars(text: str) -> list[str]:
    return [c for c in text if HAN.match(c)]


def pinyin_syllables(text: str) -> list[str]:
    """整句转无声调拼音（拼音数 == 汉字数），非汉字自动丢弃。"""
    from pypinyin import Style, lazy_pinyin
    return lazy_pinyin(text, style=Style.NORMAL, errors=lambda x: None)


def lines_with_times(text: str, dup_tol: float = 0.06) -> list[tuple[float, str]]:
    """从带时间戳的歌词里取 [(时间, 行)]，同一时间戳的第二行按「译文」丢弃。

    网易云的双语歌词就是「原词 + 译文」共用同一个时间戳；不处理的话译文会被
    当成独立歌词行，行数翻倍、对齐全乱。
    """
    from . import format as fmt

    out: list[tuple[float, str]] = []
    for ln in fmt.parse(text).lines:
        t = ln.text.strip()
        if not t or CREDIT_RE.match(t):
            continue
        if out and abs(ln.start - out[-1][0]) < dup_tol:
            continue
        out.append((ln.start, t))
    return out


def query_from_filename(stem: str) -> str:
    """文件名 → 网易云搜索词。

    "02 - 好想好想 - 赵薇"        -> "好想好想 赵薇"
    "艾秩曼+-+白狐（TV+Version）" -> "艾秩曼 白狐"
    "[110316]ソラノシズク"        -> "ソラノシズク"
    """
    s = BRACKET_RE.sub(" ", stem)
    s = TRACK_RE.sub("", s)
    s = s.replace("_", " ").replace("+", " ").replace("\u3000", " ")
    s = DASH_RE.sub(" ", s)
    s = re.sub(r"\s{2,}", " ", s)
    return s.strip(" -._")
