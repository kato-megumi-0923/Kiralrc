"""歌词文本：网易云抓取、参考文本读取、汉字→拼音。"""
from __future__ import annotations

import base64
import json
import os
import re
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import xml.etree.ElementTree as ET
from pathlib import Path

from . import ncm163

_DOWNLOADS: dict[str, int] | None = None

from . import config

HAN = re.compile(r"[\u4e00-\u9fff]")
KANA = re.compile(r"[\u3040-\u30ff]")
HANGUL = re.compile(r"[\uac00-\ud7a3]")
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


def parse_song_id(value: str | int | None) -> int | None:
    """从 ID、`id=123` 或网易云歌曲链接里取出歌曲 ID。"""
    if not value:
        return None
    if isinstance(value, int):
        return value if value > 0 else None
    text = str(value).strip()
    if text.isdigit():
        return int(text) if int(text) > 0 else None
    m = re.search(r"[?&#]id=(\d+)", text) or re.search(r"/song/(\d+)", text)
    if m:
        return int(m.group(1))
    m = re.search(r"\b(\d{4,})\b", text)
    return int(m.group(1)) if m else None


def _downloads_index() -> dict[str, int]:
    """Cloudy 的下载记录（可选）：同目录同名的「目录/文件名」→ 歌曲 ID。

    这份文件在别的机器上通常没有，读不到就当没有，不影响后面的步骤。
    """
    global _DOWNLOADS
    if _DOWNLOADS is not None:
        return _DOWNLOADS
    _DOWNLOADS = {}
    path = Path(os.environ.get("APPDATA", "")) / "Cloudy" / "downloads.json"
    try:
        entries = json.loads(path.read_text(encoding="utf-8"))
    except Exception:
        return _DOWNLOADS
    if not isinstance(entries, list):
        return _DOWNLOADS
    for entry in entries:
        try:
            audio = str(entry.get("path") or "")
            song_id = int((entry.get("track") or {}).get("id") or 0)
        except Exception:
            continue
        if not audio or song_id <= 0:
            continue
        key = _path_tail(audio)
        if key:
            _DOWNLOADS.setdefault(key, song_id)
    return _DOWNLOADS


def _path_tail(path: str) -> str:
    """「上一级目录/文件名」，用来跨盘符比对同一首歌。"""
    parts = [p for p in re.split(r"[\\/]+", str(path)) if p]
    return "/".join(parts[-2:]).lower()


def netease_id_from_downloads(path: str | Path) -> int | None:
    return _downloads_index().get(_path_tail(str(path)))


def netease_id_from_file(path: str | Path) -> int | None:
    """音频标签里的网易云 ID，先解 163 key，再看 Cloudy 写的明文标签。

    网易云客户端写的是加密的 163 key，用的人多，所以先试它。
    """
    try:
        from mutagen import File as MutagenFile
        from mutagen.id3 import ID3
    except Exception:
        return None
    tags = None
    try:
        audio = MutagenFile(str(path))
        tags = getattr(audio, "tags", None) if audio is not None else None
    except Exception:
        tags = None
    if not tags:
        # 音频容器认不出来时，标签仍可能读得出来
        try:
            tags = ID3(str(path))
        except Exception:
            return None
    if not tags:
        return None
    encrypted: list[str] = []
    plain = None
    try:
        for key in tags.keys():
            raw = tags[key]
            for text in (raw if isinstance(raw, list) else [raw]):
                text = str(text)
                if "163 key" in text:
                    encrypted.append(text)
                elif "NETEASE_ID" in str(key).upper():
                    plain = plain or text
    except Exception:
        return None
    for text in encrypted:
        sid = ncm163.music_id(text)
        if sid:
            return sid
    return parse_song_id(plain) if plain else None


def netease_id_of(path: str | Path) -> int | None:
    """一首歌的网易云 ID，不用联网：163 key → NETEASE_ID → 下载记录。"""
    return (netease_id_from_file(path) or netease_id_from_downloads(path))
def _fold(text: str) -> str:
    """歌名比大小写的形态：去括号副标题、去标点空白、统一大小写。"""
    if not text:
        return ""
    text = BRACKET_RE.sub("", text)
    text = re.sub(r"[\s\-_·.,，。!！?？'\"、:：~～/]", "", text)
    return text.lower()


def _titles_alike(a: str, b: str) -> float:
    """两个歌名的相似度（0~1），差一个字也算像。"""
    import difflib
    fa, fb = _fold(a), _fold(b)
    if not fa or not fb:
        return 0.0
    if fa == fb:
        return 1.0
    if len(fa) == len(fb) and sum(x != y for x, y in zip(fa, fb)) == 1:
        return 0.9
    return difflib.SequenceMatcher(None, fa, fb, autojunk=False).ratio()


def _artists_alike(a: str, b: str) -> float:
    """歌手相似度：字面重合的比例，简繁和译名差异也能给一点分。"""
    fa = set(_fold(a))
    fb = set(_fold(b))
    if not fa or not fb:
        return 0.0
    return len(fa & fb) / max(len(fa), len(fb))


def _queries(keyword: str, artist: str = "", name: str = "") -> list[str]:
    """搜索用词，从最具体到最宽松，去重后按序返回。"""
    out: list[str] = []
    for q in (
        f"{artist} {name}".strip() if artist and name else "",
        f"{artist} {keyword}".strip() if artist else "",
        keyword,
        BRACKET_RE.sub("", keyword).strip(),
        name,
    ):
        q = (q or "").strip()
        if q and q not in out:
            out.append(q)
    return out


def score_song(song: dict, name: str = "", artist: str = "",
               duration: float | None = None) -> float:
    """一条候选与要找的歌有多像：歌名 0.6、歌手 0.25、时长 0.15（缺歌手时按 0.8/0.2 归一化）。"""
    title = _titles_alike(song.get("name", ""), name) if name else 0.0
    gap = abs((song.get("duration") or 0) - duration) if duration else 0.0
    length = 1.0 if not duration else max(0.0, 1.0 - gap / 10.0)
    if artist:
        singer = _artists_alike(song.get("artist", ""), artist)
        return 0.6 * title + 0.25 * singer + 0.15 * length
    return 0.8 * title + 0.2 * length


def netease_candidates(keyword: str, duration: float | None = None,
                       name: str = "", artist: str = "", limit: int = 6,
                       search=None) -> list[dict]:
    """按多个用词搜索，合并去重后按相似度排序，返回前 limit 条。"""
    search = search or netease_search
    seen: dict[int, dict] = {}
    for q in _queries(keyword, artist, name):
        for song in search(q):
            if song["id"] not in seen:
                seen[song["id"]] = song
        if len(seen) >= limit * 2:
            break
    scored = []
    for song in seen.values():
        song = dict(song)
        song["score"] = round(score_song(song, name or keyword, artist, duration), 3)
        scored.append(song)
    scored.sort(key=lambda s: (-s["score"], abs((s.get("duration") or 0) - (duration or 0))))
    return scored[:limit]


def netease_match(keyword: str, duration: float | None = None, interval: float = 4.5,
                  name: str = "", artist: str = "",
                  search=None, fetch_lyric=None,
                  candidates: list[dict] | None = None) -> dict | None:
    """网易云音乐：多词搜索 + 相似度打分，够像且确实有完整歌词才采用。"""
    songs = (
        netease_candidates(keyword, duration, name=name or keyword, artist=artist, search=search)
        if candidates is None
        else candidates
    )
    if search is None and fetch_lyric is None and songs and songs[0]["score"] >= 0.55:
        time.sleep(min(interval, 1.5))
    lyric_fn = fetch_lyric or netease_lyric
    for cand in songs[:3]:
        if cand["score"] < 0.55:
            break
        lrc, tlrc = lyric_fn(cand["id"])
        if fetch_lyric is None:
            time.sleep(min(interval, 1.5))
        n = len([x for x in lrc.splitlines() if x.strip()])
        if n >= 8:
            return {**cand, "source": "网易云", "lrc": lrc, "tlyric": tlrc, "lines": n}
    return None


def netease_by_id(song_id: int) -> dict | None:
    """按 ID 直接取歌词，不经过搜索。"""
    lrc, tlyric = netease_lyric(song_id)
    if not lrc.strip():
        return None
    name, artist, dur = _song_meta(song_id)
    return {"id": song_id, "name": name, "artist": artist,
            "duration": dur, "lrc": lrc, "tlyric": tlyric, "source": "网易云"}


def _song_meta(song_id: int) -> tuple[str, str, int]:
    """一首歌的歌名、歌手、时长（秒），取不到就空着。"""
    data = _get_json(f"https://music.163.com/api/song/detail?ids=[{song_id}]") or {}
    songs = data.get("songs") or []
    if not songs:
        return "", "", 0
    s = songs[0]
    return (s.get("name") or "",
            ",".join(a.get("name", "") for a in s.get("artists") or []),
            round((s.get("duration") or 0) / 1000))


def _filter_candidates(cands: list[dict], keyword: str, duration: float | None = None,
                       name: str = "", artist: str = "", min_score: float = 0.55) -> list[dict]:
    """按歌名/歌手/时长相似度过滤并排序候选，拒绝不相关的误匹配（如长有声书/无关曲目）。"""
    target_name = name or keyword
    kept: list[dict] = []
    for raw in cands:
        cand = dict(raw)
        cand_dur = float(cand.get("duration") or 0)
        if duration and cand_dur > 0:
            if abs(cand_dur - duration) > max(45.0, duration * 0.35):
                continue
        sc = round(score_song(cand, target_name, artist, duration), 3)
        if sc < min_score:
            continue
        cand["score"] = sc
        kept.append(cand)
    kept.sort(key=lambda s: (-s["score"], abs((s.get("duration") or 0) - (duration or 0))))
    return kept


def qq_match(keyword: str, duration: float | None = None,
             name: str = "", artist: str = "") -> dict | None:
    """QQ 音乐：搜索并拉取原词 + 中文译文。"""
    try:
        payload = json.dumps({
            "req_1": {
                "method": "DoSearchForQQMusicDesktop",
                "module": "music.search.SearchCgiService",
                "param": {"query": keyword, "num_per_page": 5, "page_num": 1},
            }
        }).encode("utf-8")
        req = urllib.request.Request(
            "https://u.y.qq.com/cgi-bin/musicu.fcg",
            data=payload,
            headers={"User-Agent": UA, "Referer": "https://y.qq.com/", "Content-Type": "application/json"},
        )
        with urllib.request.urlopen(req, timeout=12) as r:
            data = json.loads(r.read().decode("utf-8", errors="replace"))
        songs = (((data.get("req_1") or {}).get("data") or {}).get("body") or {}).get("song", {}).get("list") or []
        if not songs:
            return None
        cands = []
        for s in songs:
            mid = s.get("mid") or ""
            if not mid:
                continue
            singer = ",".join(a.get("name", "") for a in (s.get("singer") or []))
            cands.append({
                "id": mid,
                "name": s.get("name") or "",
                "artist": singer,
                "duration": int(s.get("interval") or 0),
            })
        cands = _filter_candidates(cands, keyword, duration, name=name, artist=artist, min_score=0.50)
        if not cands:
            return None
        for cand in cands[:3]:
            lurl = f"https://c.y.qq.com/lyric/fcgi-bin/fcg_query_lyric_new.fcg?songmid={cand['id']}&format=json&nobase64=0"
            lreq = urllib.request.Request(lurl, headers={"User-Agent": UA, "Referer": "https://y.qq.com/"})
            with urllib.request.urlopen(lreq, timeout=12) as r:
                ld = json.loads(r.read().decode("utf-8", errors="replace"))
            lrc = base64.b64decode(ld.get("lyric") or "").decode("utf-8", errors="replace") if ld.get("lyric") else ""
            tlrc = base64.b64decode(ld.get("trans") or "").decode("utf-8", errors="replace") if ld.get("trans") else ""
            n = len([x for x in lrc.splitlines() if x.strip()])
            if n >= 8:
                return {**cand, "source": "QQ音乐", "lrc": lrc, "tlyric": tlrc, "lines": n}
    except Exception:
        return None
    return None


def kugou_match(keyword: str, duration: float | None = None,
                name: str = "", artist: str = "") -> dict | None:
    """酷狗音乐：搜歌取 hash 再拉取 LRC。"""
    try:
        kw = urllib.parse.quote(keyword)
        req = urllib.request.Request(
            f"http://mobilecdn.kugou.com/api/v3/search/song?format=json&keyword={kw}&page=1&pagesize=5",
            headers={"User-Agent": UA},
        )
        with urllib.request.urlopen(req, timeout=12) as r:
            info = ((json.loads(r.read().decode("utf-8", errors="replace")).get("data") or {}).get("info")) or []
        if not info:
            return None
        cands = [
            {
                "id": s.get("hash") or "",
                "name": s.get("songname") or "",
                "artist": s.get("singername") or "",
                "duration": int(s.get("duration") or 0),
            }
            for s in info
            if s.get("hash")
        ]
        cands = _filter_candidates(cands, keyword, duration, name=name, artist=artist, min_score=0.50)
        if not cands:
            return None
        for s in cands[:3]:
            h = s["id"]
            dur_s = s["duration"]
            sreq = urllib.request.Request(
                f"https://lyrics.kugou.com/search?ver=1&man=yes&client=pc&hash={h}&duration={dur_s * 1000}",
                headers={"User-Agent": UA},
            )
            with urllib.request.urlopen(sreq, timeout=12) as r:
                cands_lrc = json.loads(r.read().decode("utf-8", errors="replace")).get("candidates") or []
            if not cands_lrc:
                continue
            c0 = cands_lrc[0]
            dreq = urllib.request.Request(
                f"https://lyrics.kugou.com/download?ver=1&client=pc&id={c0['id']}&accesskey={c0['accesskey']}&fmt=lrc&charset=utf8",
                headers={"User-Agent": UA},
            )
            with urllib.request.urlopen(dreq, timeout=12) as r:
                dd = json.loads(r.read().decode("utf-8", errors="replace"))
            lrc = base64.b64decode(dd.get("content") or "").decode("utf-8", errors="replace") if dd.get("content") else ""
            n = len([x for x in lrc.splitlines() if x.strip()])
            if n >= 8:
                return {
                    **s,
                    "source": "酷狗音乐",
                    "lrc": lrc,
                    "tlyric": "",
                    "lines": n,
                }
    except Exception:
        return None
    return None


def lrclib_match(keyword: str, duration: float | None = None,
                 name: str = "", artist: str = "") -> dict | None:
    """LRCLIB：免鉴权国际歌词库，适合欧美 / 日文 / 独立音乐兜底。"""
    try:
        kw = urllib.parse.quote(keyword)
        req = urllib.request.Request(f"https://lrclib.net/api/search?q={kw}", headers={"User-Agent": UA})
        with urllib.request.urlopen(req, timeout=12) as r:
            arr = json.loads(r.read().decode("utf-8", errors="replace"))
        synced = [
            {
                "id": x.get("id") or 0,
                "name": x.get("trackName") or "",
                "artist": x.get("artistName") or "",
                "duration": round(float(x.get("duration") or 0)),
                "syncedLyrics": x.get("syncedLyrics") or "",
            }
            for x in (arr or [])
            if isinstance(x, dict) and (x.get("syncedLyrics") or "").strip()
        ]
        synced = _filter_candidates(synced, keyword, duration, name=name, artist=artist, min_score=0.50)
        if not synced:
            return None
        s0 = synced[0]
        lrc = s0["syncedLyrics"]
        n = len([x for x in lrc.splitlines() if x.strip()])
        if n >= 8:
            return {
                "source": "LRCLIB",
                "id": s0["id"],
                "name": s0["name"],
                "artist": s0["artist"],
                "duration": s0["duration"],
                "lrc": lrc,
                "tlyric": "",
                "lines": n,
            }
    except Exception:
        return None
    return None


def _parse_ttml_time(raw: str) -> float | None:
    """解析 TTML 时间戳（支持 HH:MM:SS.mmm / MM:SS.mmm / SS.mmm / 123s / 1234ms）。"""
    s = (raw or "").strip()
    if not s:
        return None
    if s.endswith("ms"):
        try:
            return float(s[:-2]) / 1000.0
        except ValueError:
            return None
    if s.endswith("s"):
        try:
            return float(s[:-1])
        except ValueError:
            return None
    parts = s.split(":")
    try:
        if len(parts) == 3:
            return int(parts[0]) * 3600 + int(parts[1]) * 60 + float(parts[2])
        if len(parts) == 2:
            return int(parts[0]) * 60 + float(parts[1])
        return float(parts[0])
    except ValueError:
        return None


def _xml_local_name(tag: str) -> str:
    return tag.rsplit("}", 1)[-1] if "}" in tag else tag


def _xml_attr_role(el: ET.Element) -> str:
    for k, v in el.attrib.items():
        if _xml_local_name(k) == "role":
            return (v or "").strip().lower()
    return ""


def ttml_to_lrc(xml_text: str) -> tuple[str, str]:
    """零依赖解析 TTML 歌词，返回整行 (lrc, tlyric)。

    忽略逐字 <span> 时间戳，仅提取每句 <p begin=...> 的纯文本与行首时间
    （逐字时间仍交由本地 SOFA / MMS 在人声轨上重新计算，避免不同母带静音偏移）；
    若节点内含 ttm:role="x-translation" 译文，则同步提取为 tlyric。
    """
    from . import format as fmt

    if not xml_text or not xml_text.strip():
        return "", ""
    try:
        root = ET.fromstring(xml_text.strip().lstrip("\ufeff"))
    except ET.ParseError:
        return "", ""

    lrc_lines: list[str] = []
    tr_lines: list[str] = []

    def _collect_text(node: ET.Element, main_buf: list[str], tr_buf: list[str], in_tr: bool = False) -> None:
        role = _xml_attr_role(node)
        if role in ("x-roman", "x-bg-roman"):
            return
        cur_tr = in_tr or role in ("x-translation", "translation")
        target = tr_buf if cur_tr else main_buf
        if node.text and not ("\n" in node.text and not node.text.strip()):
            target.append(node.text)
        for child in node:
            _collect_text(child, main_buf, tr_buf, cur_tr)
            if child.tail and not ("\n" in child.tail and not child.tail.strip()):
                target.append(child.tail)

    for el in root.iter():
        if _xml_local_name(el.tag) != "p":
            continue
        begin = _parse_ttml_time(el.attrib.get("begin", ""))
        if begin is None:
            continue
        main_buf: list[str] = []
        tr_buf: list[str] = []
        _collect_text(el, main_buf, tr_buf, False)
        main_txt = re.sub(r"\s+", " ", "".join(main_buf)).strip()
        tr_txt = re.sub(r"\s+", " ", "".join(tr_buf)).strip()
        if not main_txt:
            continue
        stamp = fmt.fmt_ts(begin)
        lrc_lines.append(f"{stamp}{main_txt}")
        if tr_txt:
            tr_lines.append(f"{stamp}{tr_txt}")

    return "\n".join(lrc_lines), "\n".join(tr_lines)


def amll_match(song_id: int | None, name: str = "", artist: str = "",
               duration: float = 0, timeout: float = 8.0) -> dict | None:
    """从开源 amll-ttml-db 按网易云 song_id 拉取 TTML 歌词（GitHub raw → jsDelivr 回退）。"""
    if not song_id:
        return None
    urls = (
        f"https://raw.githubusercontent.com/Steve-xmh/amll-ttml-db/main/ncm-lyrics/{song_id}.ttml",
        f"https://cdn.jsdelivr.net/gh/Steve-xmh/amll-ttml-db@main/ncm-lyrics/{song_id}.ttml",
    )
    for url in urls:
        try:
            req = urllib.request.Request(url, headers={"User-Agent": UA})
            with urllib.request.urlopen(req, timeout=timeout) as r:
                xml_text = r.read().decode("utf-8", errors="replace")
            lrc, tlyric = ttml_to_lrc(xml_text)
            n = len([x for x in lrc.splitlines() if x.strip()])
            if n >= 4:
                return {
                    "source": "AMLL",
                    "id": song_id,
                    "name": name,
                    "artist": artist,
                    "duration": round(duration) if duration else 0,
                    "lrc": lrc,
                    "tlyric": tlyric,
                    "lines": n,
                }
        except Exception:
            continue
    return None


_APPLE_BEARER_CACHE: str | None = None
_APPLE_WARNED: bool = False


def _apple_warn_once(msg: str) -> None:
    global _APPLE_WARNED
    if not _APPLE_WARNED:
        _APPLE_WARNED = True
        print(f"[warn] Apple Music: {msg}", file=sys.stderr)


def _get_apple_bearer(timeout: float = 10.0) -> str | None:
    """从 music.apple.com 抓取并缓存公开 Web Bearer Token。"""
    global _APPLE_BEARER_CACHE
    if _APPLE_BEARER_CACHE:
        return _APPLE_BEARER_CACHE
    try:
        req = urllib.request.Request("https://music.apple.com/", headers={"User-Agent": UA})
        with urllib.request.urlopen(req, timeout=timeout) as r:
            html = r.read().decode("utf-8", errors="replace")
        m_js = re.search(r'src="(/assets/index[^"]*\.js)"', html)
        if not m_js:
            return None
        js_url = "https://music.apple.com" + m_js.group(1)
        jreq = urllib.request.Request(js_url, headers={"User-Agent": UA})
        with urllib.request.urlopen(jreq, timeout=timeout) as r:
            js = r.read().decode("utf-8", errors="replace")
        m_tok = re.search(r"eyJhbGciOiJFUzI1NiI[A-Za-z0-9._-]+", js)
        if m_tok:
            _APPLE_BEARER_CACHE = m_tok.group(0)
            return _APPLE_BEARER_CACHE
    except Exception as e:
        _apple_warn_once(f"获取公开 Bearer Token 失败 ({type(e).__name__})，已跳过")
    return None


def apple_music_match(keyword: str, duration: float | None = None,
                      name: str = "", artist: str = "") -> dict | None:
    """Apple Music 官方歌词源（可选配置：未填 apple_media_user_token 时直接静默跳过）。"""
    user_token = str(config.opt("apple_media_user_token", "") or "").strip()
    if not user_token:
        return None
    storefront = str(config.opt("apple_storefront", "jp") or "jp").strip().lower() or "jp"
    bearer = _get_apple_bearer()
    if not bearer:
        return None
    headers = {
        "Authorization": f"Bearer {bearer}",
        "Media-User-Token": user_token,
        "Origin": "https://music.apple.com",
        "Referer": "https://music.apple.com/",
        "User-Agent": UA,
    }
    try:
        term = urllib.parse.quote(keyword)
        surl = f"https://amp-api.music.apple.com/v1/catalog/{storefront}/search?term={term}&types=songs&limit=5"
        sreq = urllib.request.Request(surl, headers=headers)
        with urllib.request.urlopen(sreq, timeout=10) as r:
            data = json.loads(r.read().decode("utf-8", errors="replace"))
        raw_songs = (((data.get("results") or {}).get("songs") or {}).get("data")) or []
        cands = []
        for s in raw_songs:
            attr = s.get("attributes") or {}
            sid = s.get("id") or ""
            if not sid:
                continue
            cands.append({
                "id": sid,
                "name": attr.get("name") or "",
                "artist": attr.get("artistName") or "",
                "duration": round(float(attr.get("durationInMillis") or 0) / 1000.0),
            })
        cands = _filter_candidates(cands, keyword, duration, name=name, artist=artist, min_score=0.50)
        if not cands:
            return None
        for cand in cands[:3]:
            for ep in ("lyrics", "syllable-lyrics"):
                lurl = f"https://amp-api.music.apple.com/v1/catalog/{storefront}/songs/{cand['id']}/{ep}"
                try:
                    lreq = urllib.request.Request(lurl, headers=headers)
                    with urllib.request.urlopen(lreq, timeout=10) as r:
                        ld = json.loads(r.read().decode("utf-8", errors="replace"))
                except urllib.error.HTTPError as he:
                    if he.code in (401, 403):
                        _apple_warn_once(f"认证失败 (HTTP {he.code})，请更新 apple_media_user_token")
                        return None
                    continue
                items = ld.get("data") or []
                ttml = ((items[0].get("attributes") or {}).get("ttml") or "") if items else ""
                if not ttml:
                    continue
                lrc, tlyric = ttml_to_lrc(ttml)
                n = len([x for x in lrc.splitlines() if x.strip()])
                if n >= 4:
                    return {**cand, "source": "Apple Music", "lrc": lrc, "tlyric": tlyric, "lines": n}
    except urllib.error.HTTPError as he:
        if he.code in (401, 403):
            _apple_warn_once(f"认证失败 (HTTP {he.code})，请更新 apple_media_user_token")
        else:
            _apple_warn_once(f"请求失败 (HTTP {he.code})")
        return None
    except Exception as e:
        _apple_warn_once(f"请求异常 ({type(e).__name__})，已跳过")
        return None
    return None


def _is_non_chinese_query(keyword: str, name: str = "", artist: str = "") -> bool:
    """判断检索目标是否偏非中文（含假名/韩文，或以拉丁字母为主）。"""
    sample = f"{name or keyword} {artist}".strip()
    if KANA.search(sample) or HANGUL.search(sample):
        return True
    han_cnt = len(HAN.findall(sample))
    latin_cnt = len(re.findall(r"[A-Za-z]", sample))
    return latin_cnt > 0 and (han_cnt == 0 or latin_cnt > han_cnt * 2)


def best_match(keyword: str, duration: float | None = None, interval: float = 4.5,
               song_id: int | None = None, name: str = "", artist: str = "") -> dict | None:
    """多源动态回退搜歌词：
    - 已知 song_id：先按 ID 查 网易云 → AMLL；
    - 非中文歌曲：网易云 → AMLL → Apple Music → QQ音乐 → LRCLIB → 酷狗音乐；
    - 中文歌曲：网易云 → AMLL → QQ音乐 → 酷狗音乐 → Apple Music → LRCLIB。
    """
    if song_id:
        hit = netease_by_id(song_id)
        if hit:
            return hit
        hit = amll_match(song_id, name=name or keyword, artist=artist, duration=duration or 0)
        if hit:
            return hit

    # 1) 只搜一次网易云候选，先取网易云歌词，未命中则复用高分候选 ID 查 AMLL
    cands = netease_candidates(keyword, duration, name=name or keyword, artist=artist)
    hit = netease_match(keyword, duration, interval=interval, name=name, artist=artist, candidates=cands)
    if hit:
        return hit

    if cands and cands[0]["score"] >= 0.55:
        amll_cand_id = cands[0]["id"]
        if amll_cand_id and amll_cand_id != song_id:
            hit = amll_match(
                amll_cand_id,
                name=cands[0].get("name") or name or keyword,
                artist=cands[0].get("artist") or artist,
                duration=cands[0].get("duration") or duration or 0,
            )
            if hit:
                return hit

    # 2) 按语种动态选择后续源回退顺序
    if _is_non_chinese_query(keyword, name=name, artist=artist):
        chain = (
            lambda: apple_music_match(keyword, duration, name=name, artist=artist),
            lambda: qq_match(keyword, duration, name=name, artist=artist),
            lambda: lrclib_match(keyword, duration, name=name, artist=artist),
            lambda: kugou_match(keyword, duration, name=name, artist=artist),
        )
    else:
        chain = (
            lambda: qq_match(keyword, duration, name=name, artist=artist),
            lambda: kugou_match(keyword, duration, name=name, artist=artist),
            lambda: apple_music_match(keyword, duration, name=name, artist=artist),
            lambda: lrclib_match(keyword, duration, name=name, artist=artist),
        )
    for fn in chain:
        hit = fn()
        if hit:
            return hit
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


DEFAULT_POLYPHONES: dict[str, list[str]] = {
    "还得": ["hai", "dei"],
    "走在": ["zou", "zai"],
    "看着": ["kan", "zhe"],
    "笑着": ["xiao", "zhe"],
    "听着": ["ting", "zhe"],
    "吹着": ["chui", "zhe"],
    "睡着": ["shui", "zhao"],
    "觉得": ["jue", "de"],
    "记得": ["ji", "de"],
    "舍不得": ["she", "bu", "de"],
    "音乐": ["yin", "yue"],
    "快乐": ["kuai", "le"],
    "长大": ["zhang", "da"],
    "长夜": ["chang", "ye"],
    "重逢": ["chong", "feng"],
    "重新": ["chong", "xin"],
    "行囊": ["xing", "nang"],
    "一行泪": ["yi", "hang", "lei"],
    "宿命": ["su", "ming"],
    "温柔地": ["wen", "rou", "de"],
    "静静地": ["jing", "jing", "de"],
    "悄悄地": ["qiao", "qiao", "de"],
    "慢慢地": ["man", "man", "de"],
}

COMMON_POLYPHONE_CHARS: dict[str, list[str]] = {
    "了": ["le", "liao"],
    "着": ["zhe", "zhao", "zhuo"],
    "得": ["de", "dei"],
    "还": ["hai", "huan"],
    "长": ["chang", "zhang"],
    "行": ["xing", "hang"],
    "重": ["chong", "zhong"],
    "乐": ["le", "yue"],
    "觉": ["jue", "jiao"],
    "地": ["de", "di"],
}


def han_chars(text: str) -> list[str]:
    return [c for c in text if HAN.match(c)]


def load_polyphones(override_path: str | Path | None = None) -> dict[str, list[str]]:
    """加载多音字词表（内置白名单 + polyphone.toml 人工覆盖，KI-5）。"""
    import tomllib
    from . import config

    table: dict[str, list[str]] = {k: list(v) for k, v in DEFAULT_POLYPHONES.items()}
    if override_path is None:
        raw = config.opt("polyphone_file", "polyphone.toml")
        p = Path(raw) if Path(raw).is_absolute() else config.ROOT / raw
    else:
        p = Path(override_path)

    if p.exists():
        try:
            with open(p, "rb") as f:
                data = tomllib.load(f)
            phrases = data.get("phrases", data)
            for k, v in phrases.items():
                if not isinstance(k, str):
                    continue
                syls = v.split() if isinstance(v, str) else list(v)
                h_chars = han_chars(k)
                if h_chars and len(h_chars) == len(syls):
                    table["".join(h_chars)] = [s.lower() for s in syls]
        except Exception:
            pass
    return table


def pinyin_syllables(text: str,
                     overrides: dict[str, list[str]] | None = None) -> list[str]:
    """整句转无声调拼音（拼音数 == 汉字数），非汉字自动丢弃，并应用多音字词表覆盖（KI-5）。"""
    from pypinyin import Style, lazy_pinyin

    syls = list(lazy_pinyin(text, style=Style.NORMAL, errors=lambda x: None))
    chars = han_chars(text)
    if len(syls) != len(chars) or not chars:
        return syls

    table = overrides if overrides is not None else load_polyphones()
    if not table:
        return syls

    # 只在同一连续汉字片段内匹配词组，避免跨标点误配
    pos_to_han: dict[int, int] = {}
    h_idx = 0
    for idx, ch in enumerate(text):
        if HAN.match(ch):
            pos_to_han[idx] = h_idx
            h_idx += 1

    locked = [False] * len(chars)
    for phrase in sorted(table.keys(), key=len, reverse=True):
        if not phrase or any(not HAN.match(c) for c in phrase):
            continue
        p_syls = table[phrase]
        if len(p_syls) != len(phrase):
            continue
        start = 0
        while True:
            pos = text.find(phrase, start)
            if pos == -1:
                break
            h_indices = [pos_to_han[pos + k] for k in range(len(phrase)) if (pos + k) in pos_to_han]
            if len(h_indices) == len(phrase) and not any(locked[i] for i in h_indices):
                for i, s in zip(h_indices, p_syls):
                    syls[i] = s
                    locked[i] = True
            start = pos + 1

    return syls


def detect_polyphones(text: str,
                      overrides: dict[str, list[str]] | None = None) -> list[dict]:
    """检测句中常见多音字及其当前采用的读音，供 UI 或 CLI 提示与覆写（KI-5）。"""
    chars = han_chars(text)
    syls = pinyin_syllables(text, overrides=overrides)
    out: list[dict] = []
    for idx, (ch, sy) in enumerate(zip(chars, syls)):
        if ch in COMMON_POLYPHONE_CHARS:
            out.append({
                "index": idx,
                "char": ch,
                "pinyin": sy,
                "candidates": COMMON_POLYPHONE_CHARS[ch],
            })
    return out


_KANA_RE = re.compile(r"[\u3040-\u30ff]")
_HANGUL_RE = re.compile(r"[\uac00-\ud7af]")
_LATIN_RE = re.compile(r"[A-Za-z]")


def _script_kind(text: str) -> str:
    """粗略判断一行歌词的主语种脚本：ja / ko / zh / latin / other。"""
    if _KANA_RE.search(text):
        return "ja"
    if _HANGUL_RE.search(text):
        return "ko"
    n_han = len(HAN.findall(text))
    n_lat = len(_LATIN_RE.findall(text))
    if n_han > 0 and n_han >= n_lat:
        return "zh"
    if n_lat > 0:
        return "latin"
    return "other"


def _is_translation_pair(prev_text: str, cur_text: str, dt: float) -> bool:
    """判断紧邻时间（dt < dup_tol）的后一行是否是前一行原词的译文，
    避免把同语种且间隔仅几十毫秒的两句相邻原词误吞成译文。"""
    if cur_text.startswith("❤️") or cur_text.endswith("❤️"):
        return True
    k1 = _script_kind(prev_text)
    k2 = _script_kind(cur_text)
    # 外文原词 + 中文译文（最常见双语 LRC）
    if k1 in ("ja", "ko", "latin") and k2 == "zh":
        return True
    # 日/韩原词 + 罗马音或英译
    if k1 in ("ja", "ko") and k2 == "latin":
        return True
    # 中文原词 + 英文译文仅在严格同时间戳时视为译文
    if k1 == "zh" and k2 == "latin" and dt <= 0.005:
        return True
    return False


def lines_with_translations(
    text: str, dup_tol: float = 0.06
) -> tuple[list[tuple[float, str]], list[list[str]]]:
    """从带时间戳的歌词里拆出原词和同时间戳的译文行。

    返回 ([(时间, 原词)], [[译文1, ...], ...])，两个列表等长。
    网易云 / 双语 LRC 中「原词 + 译文」共用同一个时间戳；对齐时只用原词，
    输出逐字 LRC 时再把译文按对齐后的行首时间戳贴回。
    """
    from . import format as fmt

    orig: list[tuple[float, str]] = []
    trans: list[list[str]] = []
    for ln in fmt.parse(text).lines:
        t = ln.text.strip()
        if not t or CREDIT_RE.match(t):
            continue
        if orig:
            dt = abs(ln.start - orig[-1][0])
            if dt < dup_tol and _is_translation_pair(orig[-1][1], t, dt):
                trans[-1].append(t)
                continue
        orig.append((ln.start, t))
        trans.append([])
    return orig, trans


def lines_with_times(text: str, dup_tol: float = 0.06) -> list[tuple[float, str]]:
    """从带时间戳的歌词里取 [(时间, 行)]，同一时间戳的第二行按「译文」丢弃。"""
    orig, _ = lines_with_translations(text, dup_tol=dup_tol)
    return orig


def merge_tlyric(orig_lrc: str, tlyric: str, tol: float = 0.25) -> str:
    """把网易云分开返回的 lrc（原文）和 tlyric（中文译文）按时间戳合并成双语 LRC。"""
    if not tlyric or not tlyric.strip():
        return orig_lrc
    from . import format as fmt

    orig = fmt.parse(orig_lrc)
    tr_lines = [ln for ln in fmt.parse(tlyric).lines if ln.text.strip() and not CREDIT_RE.match(ln.text.strip())]
    if not orig.lines or not tr_lines:
        return orig_lrc

    used: set[int] = set()
    out_lines: list[fmt.Line] = []
    for ln in orig.lines:
        out_lines.append(ln)
        if not ln.text.strip() or CREDIT_RE.match(ln.text.strip()):
            continue
        best_i, best_d = -1, tol + 1.0
        for i, tr in enumerate(tr_lines):
            if i in used:
                continue
            d = abs(tr.start - ln.start)
            if d <= tol and d < best_d:
                best_i, best_d = i, d
        if best_i >= 0:
            used.add(best_i)
            out_lines.append(fmt.Line(ln.start, tr_lines[best_i].text.strip()))
    return fmt.Lrc(metas=orig.metas, lines=out_lines).render()


def artist_title_from_filename(stem: str) -> tuple[str, str]:
    """从「歌手 - 歌名」类文件名里拆出 (artist, title)；拆不出时 artist 为空。"""
    s = TRACK_RE.sub("", stem).strip()
    parts = [p.strip() for p in DASH_RE.split(s) if p.strip()]
    if len(parts) >= 2:
        artist = BRACKET_RE.sub("", parts[0]).strip()
        title = " - ".join(parts[1:]).strip()
        return artist, title
    return "", query_from_filename(stem)


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
