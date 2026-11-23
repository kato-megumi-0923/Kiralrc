"""手动多候选标签与高清封面刮削。

曲库本来就比较规整，这里只做「补 / 换」两件事：把网易云和 iTunes 的候选
并排摆出来，由人看图挑版本，确认之后才落盘。

几条硬规矩：
- 只在原标签对象上改，绝不清空重建（163 key、REPLAYGAIN、封底图都得留着）；
- 候选没有 song_id（比如只挑了 iTunes 的高清封面）时，绝不覆盖已有 NETEASE_ID；
- 高清图只在真要写入时才下载；
- 不改文件名，同目录同名 .lrc 才配得上。
"""

from __future__ import annotations

import datetime as _dt
import json
import re
import time
import urllib.parse
import urllib.request
import zlib
from pathlib import Path

from . import netauth
from . import text as tx

# 能稳定写标签的容器。WAV / APE 之类只读出来给人看，标 writable: false。
WRITABLE = {".flac": "flac", ".mp3": "mp3", ".m4a": "m4a", ".mp4": "m4a"}
AUDIO_EXTS = {".flac", ".mp3", ".m4a", ".mp4", ".wav", ".aif", ".aiff",
              ".ape", ".wv", ".ogg", ".opus", ".dsf"}

THUMB = 240           # 画廊缩略图边长
LOW_COVER = 600       # 低于这个尺寸算「低清」，fill 模式下可以换掉
UA = tx.UA
JSON_HEADERS = {"User-Agent": UA}


# ---------- 图片尺寸（不引 Pillow，直接读字节头） ----------

def image_size(data: bytes) -> tuple[int, int]:
    """返回 (宽, 高)；认不出就 (0, 0)。只处理 PNG 与 JPEG。"""
    if not data:
        return 0, 0
    if data[:8] == b"\x89PNG\r\n\x1a\n":
        # 8 字节签名 + 4 字节长度 + 4 字节 "IHDR" + 宽高各 4 字节（大端）
        if len(data) >= 24 and data[12:16] == b"IHDR":
            return int.from_bytes(data[16:20], "big"), int.from_bytes(data[20:24], "big")
        return 0, 0
    if data[:2] != b"\xff\xd8":
        return 0, 0
    i, n = 2, len(data)
    while i + 4 <= n:
        if data[i] != 0xFF:
            i += 1
            continue
        marker = data[i + 1]
        if marker == 0xFF:          # 填充字节
            i += 1
            continue
        # 不带长度字段的标记：SOI / EOI / RSTn / TEM
        if marker in (0xD8, 0xD9, 0x01) or 0xD0 <= marker <= 0xD7:
            i += 2
            continue
        seg = int.from_bytes(data[i + 2:i + 4], "big")
        if seg < 2:
            return 0, 0
        # SOF0..SOF15 里有宽高；C4(DHT) / C8(JPG) / CC(DAC) 不是 SOF
        if 0xC0 <= marker <= 0xCF and marker not in (0xC4, 0xC8, 0xCC):
            if i + 9 <= n:
                h = int.from_bytes(data[i + 5:i + 7], "big")
                w = int.from_bytes(data[i + 7:i + 9], "big")
                return w, h
            return 0, 0
        i += 2 + seg
    return 0, 0


def _cover_mime(data: bytes) -> str:
    if data[:8] == b"\x89PNG\r\n\x1a\n":
        return "image/png"
    return "image/jpeg"


# 三种容器都只吃 JPEG / PNG；别的格式（WebP、GIF）宁可不要，也别塞进去冒风险
SUPPORTED_COVER_MIME = frozenset({"image/png", "image/jpeg"})


def _png_intact(data: bytes) -> bool:
    """按块走一遍 PNG：CRC 对得上、IDAT 能解压、末尾有 IEND。"""
    i, n = 8, len(data)
    idat: list[bytes] = []
    saw_ihdr = saw_idat = False
    while i + 8 <= n:
        length = int.from_bytes(data[i:i + 4], "big")
        ctype = data[i + 4:i + 8]
        end = i + 8 + length
        if length < 0 or end + 4 > n:
            return False
        body = data[i + 8:end]
        crc = int.from_bytes(data[end:end + 4], "big")
        if zlib.crc32(ctype + body) & 0xFFFFFFFF != crc:
            return False
        if not saw_ihdr:
            if ctype != b"IHDR":
                return False
            saw_ihdr = True
        elif ctype == b"IDAT":
            saw_idat = True
            idat.append(body)
        elif ctype == b"IEND":
            if not (saw_ihdr and saw_idat):
                return False
            try:
                zlib.decompress(b"".join(idat))
            except Exception:
                return False
            return True
        i = end + 4
    return False


def _jpeg_intact(data: bytes) -> bool:
    """按标记走一遍 JPEG：结构连得上、有 SOF、最后落在 EOI。"""
    n = len(data)
    if data[:2] != b"\xff\xd8":
        return False
    i, saw_sof = 2, False
    while i + 1 < n:
        if data[i] != 0xFF:
            return False
        marker = data[i + 1]
        if marker == 0xFF:            # 填充字节
            i += 1
            continue
        if marker in (0xD8, 0x01) or 0xD0 <= marker <= 0xD7:
            i += 2
            continue
        if marker == 0xD9:
            return saw_sof
        if i + 4 > n:
            return False
        seg = int.from_bytes(data[i + 2:i + 4], "big")
        if seg < 2:
            return False
        if 0xC0 <= marker <= 0xCF and marker not in (0xC4, 0xC8, 0xCC):
            saw_sof = True
        if marker == 0xDA:
            # 后面是熵编码数据：跳到下一个真正的标记（跳过 FF00 与 RSTn）
            j = i + 2 + seg
            while j + 1 < n:
                if data[j] != 0xFF:
                    j += 1
                    continue
                nxt = data[j + 1]
                if nxt == 0x00 or 0xD0 <= nxt <= 0xD7:
                    j += 2
                    continue
                if nxt == 0xFF:
                    j += 1
                    continue
                break
            i = j
            continue
        i += 2 + seg
    return False


def cover_problem(data: bytes) -> str:
    """检查下载回来的封面能不能用；能用返回空串，否则返回给人看的原因。

    CDN 出错时经常回一张 HTML 错误页（200 + text/html），字节数也不小，
    光看长度会当成图写进去——那样封面就成了张 0×0 的破图。
    """
    if not data:
        return "封面下载失败（没拿到内容）"
    if data[:8] == b"\x89PNG\r\n\x1a\n":
        return "" if _png_intact(data) else "封面是残缺的 PNG（解不开）"
    if data[:2] == b"\xff\xd8":
        return "" if _jpeg_intact(data) else "封面是残缺的 JPEG（解不开）"
    head = data[:256].lstrip().lower()
    if head.startswith((b"<!doctype", b"<html", b"<?xml", b"<")):
        return "下载回来的不是图片（像是 HTML 错误页）"
    if head.startswith(b"{"):
        return "下载回来的不是图片（像是接口报错 JSON）"
    return "下载回来的不是 PNG/JPEG，这个容器放不了"


# ---------- 读本地现状 ----------

def _first(value) -> str:
    """mutagen 的文本值可能是帧、字符串或字符串列表，统一取第一个。"""
    if value is None:
        return ""
    if isinstance(value, (list, tuple)):
        return str(value[0]).strip() if value else ""
    return str(value).strip()


def _year(value) -> str:
    m = re.search(r"(?:19|20)\d{2}", _first(value))
    return m.group(0) if m else ""


def _flac_meta(audio) -> dict:
    tags = getattr(audio, "tags", None)
    out = {"title": "", "artist": "", "album": "", "year": "", "cover": b""}
    if tags:
        out["title"] = _first(tags.get("title"))
        out["artist"] = _first(tags.get("artist"))
        out["album"] = _first(tags.get("album"))
        out["year"] = _year(tags.get("date")) or _year(tags.get("year"))
    pics = list(getattr(audio, "pictures", []) or [])
    front = [p for p in pics if getattr(p, "type", 0) == 3]
    pick = front[0] if front else (pics[0] if pics else None)
    if pick is not None:
        out["cover"] = bytes(pick.data)
    return out


def _id3_tags(path: Path):
    from mutagen.id3 import ID3
    try:
        return ID3(str(path))
    except Exception:
        return None


def _id3_meta(tags) -> dict:
    out = {"title": "", "artist": "", "album": "", "year": "", "cover": b""}
    if not tags:
        return out
    out["title"] = _first(tags.get("TIT2"))
    out["artist"] = _first(tags.get("TPE1"))
    out["album"] = _first(tags.get("TALB"))
    out["year"] = _year(tags.get("TDRC")) or _year(tags.get("TYER"))
    apics = [f for f in tags.getall("APIC") if getattr(f, "data", None)]
    front = [f for f in apics if getattr(f, "type", 0) == 3]
    pick = front[0] if front else (apics[0] if apics else None)
    if pick is not None:
        out["cover"] = bytes(pick.data)
    return out


def _m4a_meta(audio) -> dict:
    tags = getattr(audio, "tags", None)
    out = {"title": "", "artist": "", "album": "", "year": "", "cover": b""}
    if not tags:
        return out
    out["title"] = _first(tags.get("\xa9nam"))
    out["artist"] = _first(tags.get("\xa9ART"))
    out["album"] = _first(tags.get("\xa9alb"))
    out["year"] = _year(tags.get("\xa9day"))
    covr = tags.get("covr")
    if covr:
        out["cover"] = bytes(covr[0])
    return out


def inspect_track(path: str | Path) -> dict:
    """读一首歌的现有标签、年份、网易云 ID 与正面封面分辨率。"""
    p = Path(path)
    ext = p.suffix.lower()
    info = {
        "path": str(p),
        "name": p.name,
        "format": WRITABLE.get(ext, ext.lstrip(".")),
        "writable": ext in WRITABLE,
        "title": "", "artist": "", "album": "", "year": "",
        "netease_id": 0, "cover_w": 0, "cover_h": 0,
    }
    try:
        info["netease_id"] = int(tx.netease_id_of(p) or 0)
    except Exception:
        info["netease_id"] = 0

    audio = None
    try:
        from mutagen import File as MutagenFile
        audio = MutagenFile(str(p))
    except Exception:
        audio = None

    meta = {"title": "", "artist": "", "album": "", "year": "", "cover": b""}
    try:
        if ext == ".flac":
            meta = _flac_meta(audio) if audio is not None else meta
        elif ext in (".mp3",):
            tags = getattr(audio, "tags", None) if audio is not None else None
            meta = _id3_meta(tags if tags is not None else _id3_tags(p))
        elif ext in (".m4a", ".mp4"):
            meta = _m4a_meta(audio) if audio is not None else meta
        elif audio is not None:
            # WAV / AIFF 之类：容器可能带 ID3，读得出来就显示，反正不写
            meta = _id3_meta(getattr(audio, "tags", None))
    except Exception:
        meta = {"title": "", "artist": "", "album": "", "year": "", "cover": b""}

    info.update({k: meta[k] for k in ("title", "artist", "album", "year")})
    w, h = image_size(meta["cover"])
    info["cover_w"], info["cover_h"] = w, h
    return info


def list_audio(path: str | Path) -> list[Path]:
    """目录递归找音频；给的是文件就只返回它。"""
    p = Path(path)
    if p.is_file():
        return [p] if p.suffix.lower() in AUDIO_EXTS else []
    found = [q for q in sorted(p.rglob("*")) if q.is_file() and q.suffix.lower() in AUDIO_EXTS]
    return found


# ---------- 候选搜索 ----------

def _pic_url(url: str, size: int = 0) -> str:
    """size 为 0 时不带 param，直接要原图。

    网易云的 ?param=WxH 只缩不放（源图 390 请求 1200 还是 390），
    所以「高清」只有一条路：不带参数取原图。
    """
    if not url:
        return ""
    if size <= 0 or "?" in url:
        return url
    return f"{url}?param={size}y{size}"


def _netease_candidate(song: dict) -> dict:
    album = song.get("album") or song.get("al") or {}
    artists = song.get("artists") or song.get("ar") or []
    names = []
    for a in artists:
        name = a.get("name") if isinstance(a, dict) else str(a)
        if name:
            names.append(str(name))
    publish = album.get("publishTime") or 0
    year = ""
    if publish:
        try:
            year = _dt.datetime.fromtimestamp(int(publish) / 1000).strftime("%Y")
        except Exception:
            year = ""
    pic = album.get("picUrl") or album.get("blurPicUrl") or ""
    return {
        "source": "网易云",
        "title": str(song.get("name") or song.get("title") or ""),
        "artist": "/".join(names),
        "album": str(album.get("name") or ""),
        "year": year,
        "duration": int(round((song.get("duration") or 0) / 1000)),
        "song_id": int(song.get("id") or 0),
        "thumb_url": _pic_url(pic, THUMB),
        "cover_url": _pic_url(pic),          # 原图，尺寸由前端实测
        "cover_hint": "",
    }


def netease_details(ids: list[int], netease_json=None, state: dict | None = None) -> list[dict]:
    """一次请求补齐若干首歌的 album.picUrl / publishTime。

    state 给了的话，被限速时记一笔 rate_limited，好让上层如实告诉用户。
    """
    ids = [i for i in ids if i]
    if not ids:
        return []
    get = netease_json or tx._get_json
    url = ("https://music.163.com/api/song/detail?ids=[%s]"
           % ",".join(str(i) for i in ids))
    try:
        data = get(url) or {}
    except tx.RateLimited:
        if state is not None:
            state["rate_limited"] = True
        return []
    except Exception:
        return []
    songs = data.get("songs") or []
    return [s for s in songs if isinstance(s, dict)]


def _netease_candidates(query, artist, title, netease_id, limit,
                        netease_search=None, netease_json=None,
                        state: dict | None = None) -> list[dict]:
    search = netease_search or tx.netease_search
    out: list[dict] = []
    seen: set[int] = set()

    if netease_id:
        for song in netease_details([int(netease_id)], netease_json, state):
            cand = _netease_candidate(song)
            if cand["song_id"] and cand["song_id"] not in seen:
                seen.add(cand["song_id"])
                out.append(cand)      # 给了 ID 就先按 ID 取，排第一

    keyword = query or " ".join(x for x in (title, artist) if x)
    if keyword:
        try:
            hits = search(keyword, limit=6) or []
        except tx.RateLimited:
            hits = []
            if state is not None:
                state["rate_limited"] = True
        except Exception:
            hits = []
        ids = [int(h["id"]) for h in hits if h.get("id") and int(h["id"]) not in seen]
        detailed = netease_details(ids, netease_json, state)
        if not detailed:
            # 补详情失败（限速 / 接口抽风）时，至少把搜索结果的歌名歌手摆出来
            detailed = [{"id": int(h["id"]), "name": h.get("name", ""),
                         "artists": [{"name": n} for n in str(h.get("artist", "")).split(",") if n],
                         "duration": int(h.get("duration") or 0) * 1000}
                        for h in hits if h.get("id")]
        for song in detailed:
            cand = _netease_candidate(song)
            if cand["song_id"] and cand["song_id"] not in seen:
                seen.add(cand["song_id"])
                out.append(cand)
    return out[:max(limit, 1)]


def _itunes_country(text: str) -> str:
    if tx.KANA.search(text):
        return "jp"
    if tx.HAN.search(text):
        return "hk"
    return "us"


# Apple 的图按路径里的边长给，最大一般 3000（再大也返回 3000）。
# 6000 只是「要尽可能大」，服务端会夹到自己的原图尺寸。
APPLE_MAX = 6000


def _artwork(url: str, size: int) -> str:
    if not url:
        return ""
    # iTunes 给的形如 .../100x100bb.jpg，换成想要的边长
    return re.sub(r"/\d+x\d+bb\.", f"/{size}x{size}bb.", url)


def resize_cover_url(url: str, size: int) -> str:
    """把原图地址改成「请求指定边长」：网易云加 ?param，Apple 改路径里的 WxH。

    size<=0 表示要原图，原样返回。
    """
    if not url or size <= 0:
        return url
    if "bb.jpg" in url or "mzstatic.com" in url:
        return re.sub(r"/\d+x\d+bb\.", f"/{size}x{size}bb.", url)
    if "?" in url:
        return url
    return f"{url}?param={size}y{size}"


def _http_json(url: str, timeout: int = 15):
    """iTunes 用的轻量 GET：失败就当没结果，不打断另一个来源。"""
    try:
        req = urllib.request.Request(url, headers=JSON_HEADERS)
        with urllib.request.urlopen(req, timeout=timeout) as r:
            return json.loads(r.read().decode("utf-8"))
    except Exception:
        return {}


def _itunes_rows(term: str, country: str, itunes_json) -> list[dict]:
    q = urllib.parse.quote(term)
    url = (f"https://itunes.apple.com/search?term={q}&media=music&entity=song"
           f"&country={country}&limit=6")
    get = itunes_json or _http_json
    try:
        data = get(url) or {}
    except Exception:
        return []
    rows = data.get("results") or []
    return [r for r in rows if isinstance(r, dict)]


def _itunes_candidates(query, artist, title, itunes_json) -> list[dict]:
    term = " ".join(x for x in (title or query, artist) if x) or query
    if not term:
        return []
    country = _itunes_country(" ".join(x for x in (query, artist, title) if x))
    rows = _itunes_rows(term, country, itunes_json)
    if not rows and country == "hk":
        rows = _itunes_rows(term, "tw", itunes_json)   # 港区没有就试台区
    out = []
    for r in rows:
        art = r.get("artworkUrl100") or ""
        out.append({
            "source": "iTunes",
            "title": str(r.get("trackName") or ""),
            "artist": str(r.get("artistName") or ""),
            "album": str(r.get("collectionName") or ""),
            "year": str(r.get("releaseDate") or "")[:4],
            "duration": int(r.get("trackTimeMillis") or 0) // 1000,
            "song_id": 0,                     # iTunes 没有网易云 ID
            "thumb_url": _artwork(art, THUMB),
            "cover_url": _artwork(art, APPLE_MAX),   # 尺寸由前端实测
            "cover_hint": "",
        })
    return out


def _auth_name() -> str:
    try:
        from . import netauth
        return netauth.describe()
    except Exception:
        return ""


def search_with_note(query: str, artist: str = "", title: str = "",
                     netease_id: int | None = None, limit: int = 8,
                     netease_search=None, netease_json=None,
                     itunes_json=None) -> dict:
    """给 CLI 用：除了候选，还告诉前端「网易云那边是不是被限速了」。

    限速时静默只剩 iTunes 的结果，用户会以为「明明搜得到为什么这里没有」，
    所以必须把原因带出去；冷却期间干脆不发请求。
    """
    wait = netauth.cooling_for()
    if wait > 0:
        return {"items": [], "auth": _auth_name(),
                "warning": f"网易云限速冷却中，约 {int(wait) + 1} 秒后再搜"}
    state: dict = {}
    cands = search_candidates(query, artist=artist, title=title,
                              netease_id=netease_id, limit=limit,
                              netease_search=netease_search, netease_json=netease_json,
                              itunes_json=itunes_json, state=state)
    note = ""
    if state.get("rate_limited"):
        has_netease = any(c.get("source") == "网易云" for c in cands)
        note = ("网易云限速了，这次只有 iTunes 候选；过一会儿再点「重新搜索」"
                if not has_netease else
                "网易云限速，候选可能不全；过一会儿可以再搜一次")
    return {"items": cands, "warning": note, "auth": _auth_name()}


def _interleave(first: list[dict], second: list[dict]) -> list[dict]:
    """两个来源交替排，前 N 条里两边都有，画廊才不会一边倒。"""
    out: list[dict] = []
    for i in range(max(len(first), len(second))):
        if i < len(first):
            out.append(first[i])
        if i < len(second):
            out.append(second[i])
    return out


def search_candidates(query: str, artist: str = "", title: str = "",
                      netease_id: int | None = None, limit: int = 8,
                      netease_search=None, netease_json=None,
                      itunes_json=None, state: dict | None = None) -> list[dict]:
    """网易云 + iTunes 一起搜，返回可直接喂给画廊的候选。"""
    netease = _netease_candidates(query, artist, title, netease_id, limit,
                                  netease_search, netease_json, state)
    itunes = _itunes_candidates(query, artist, title, itunes_json)
    merged = _interleave(netease, itunes)
    if limit and limit > 0:
        return merged[:limit]
    return merged


# ---------- 写入 ----------

RESOLVE_GAP = 0.8            # 「按网易云补全」每首之间歇一下，免得被限速
DURATION_TOLERANCE = 3       # 秒。同一录音不会差太多；长曲子再放宽 2%（编码留白）

# 版本关键词不许当副标题丢掉：不然「晴天 (Live)」会被当成原版写进去。
# ASCII 词加边界，免得 Deliver 里的 live、Karaoke 之外的东西被误伤。
_VERSION_WORDS = (
    ("live", (r"(?<![a-z])live(?![a-z])", "ライブ", "现场")),
    ("remix", (r"(?<![a-z])remix(?![a-z])", r"(?<![a-z])rmx(?![a-z])",
               r"(?<![a-z])mix(?![a-z])", "混音")),
    ("acoustic", (r"(?<![a-z])acoustic(?![a-z])", "アコースティック", "不插电")),
    ("instrumental", (r"(?<![a-z])instrumental(?![a-z])", r"(?<![a-z])inst\.?(?![a-z])",
                      r"off\s*vocal", "伴奏", "纯音乐")),
    ("karaoke", (r"(?<![a-z])karaoke(?![a-z])", "カラオケ")),
    ("cover", (r"(?<![a-z])cover(?![a-z])", "翻唱")),
    ("demo", (r"(?<![a-z])demo(?![a-z])",)),
    ("remaster", (r"(?<![a-z])remaster(?:ed)?(?![a-z])",)),
    ("edit", (r"(?<![a-z])edit(?![a-z])",)),
    ("extended", (r"(?<![a-z])extended(?![a-z])",)),
    ("spedup", (r"sped\s*up", "加速")),
    ("slowed", (r"slowed", "减速")),
)
_VERSION_RES = tuple((name, re.compile("|".join(parts), re.I))
                     for name, parts in _VERSION_WORDS)
# 多歌手的写法：歌名里 / , 、 & feat. 都算分隔
_ARTIST_SPLIT = re.compile(r"[/,，、&＆;；]|(?<![a-z])feat\.?(?![a-z])|(?<![a-z])ft\.?(?![a-z])",
                           re.I)


def version_tags(text: str) -> frozenset[str]:
    """歌名里带的版本标记：live / remix / acoustic / instrumental / …"""
    raw = str(text or "")
    return frozenset(name for name, rx in _VERSION_RES if rx.search(raw))


def artist_set(text: str) -> frozenset[str]:
    """把「A/B」「A feat. B」拆成集合，顺序无所谓，但人数要一样。"""
    return frozenset(p for p in (tx._fold(x) for x in _ARTIST_SPLIT.split(str(text or ""))) if p)


def strict_match(local: dict, cand: dict) -> bool:
    """歌名、版本、歌手、专辑都对上才算同一首（有时长再对一次时长）。

    比之前严的地方：
    - 版本关键词保留，(Live) / (Remix) / 伴奏 这类不会被当成原版；
    - 歌手按「完整艺人」比，不再允许一方包含另一方；
    - 本地有专辑而候选没有专辑，也不算对上——信息不全就别猜。
    """
    fold = tx._fold
    t1, t2 = fold(local.get("title", "")), fold(cand.get("title", ""))
    if not t1 or t1 != t2:
        return False
    if version_tags(local.get("title", "")) != version_tags(cand.get("title", "")):
        return False

    a1, a2 = artist_set(local.get("artist", "")), artist_set(cand.get("artist", ""))
    if a1 and a1 != a2:
        return False

    alb1, alb2 = fold(local.get("album", "")), fold(cand.get("album", ""))
    if alb1 and alb1 != alb2:
        return False

    d1 = int(local.get("duration") or 0)
    d2 = int(cand.get("duration") or 0)
    if d1 > 0 and d2 > 0:
        tolerance = max(DURATION_TOLERANCE, int(d1 * 0.02))
        if abs(d1 - d2) > tolerance:
            return False
    return True


def find_unique_match(local: dict, cands: list[dict]) -> tuple[dict, str]:
    """在候选里找唯一一条对得上的。

    返回 (候选, 原因)：原因 "" 表示命中，"none" 没对上，"ambiguous" 有多条
    都对得上（不同录音版本）——这种留给用户手选，绝不替他挑第一条。
    """
    hits = [c for c in cands if strict_match(local, c)]
    if not hits:
        return {}, "none"
    if len({int(c.get("song_id") or 0) for c in hits}) > 1:
        return {}, "ambiguous"
    return hits[0], ""


def resolve_by_search(local: dict, path: Path,
                      cache: dict | None = None) -> tuple[dict, str]:
    """按「歌名 歌手」搜网易云，返回 (唯一命中, 原因)。

    cache 是同一批里共享的**候选列表**，不是匹配结果：同名同歌手的曲目
    查询完全一样，但专辑 / 版本 / 时长可能不同，所以每首都要拿自己那份
    现状重新比一遍，不能把上一首选好的候选直接借过来。
    """
    key = (tx._fold(local.get("title", "")), tx._fold(local.get("artist", "")))
    if cache is not None and key[0] and key in cache:
        return find_unique_match(local, cache[key])

    query = " ".join(x for x in (local.get("title"), local.get("artist")) if x)
    if not query:
        query = path.stem
    state: dict = {}
    try:
        cands = _netease_candidates(query, "", "", None, 6, state=state)
    except tx.RateLimited:
        return {}, "rate_limited"
    except Exception:
        return {}, "error"
    if state.get("rate_limited"):
        # 限速时候选是不全的：这时候「没找到」不能当结论，得如实说被限速了
        return {}, "rate_limited"
    if cache is not None and key[0]:
        cache[key] = cands
    return find_unique_match(local, cands)


def _download(url: str, timeout: int = 30) -> bytes:
    if not url:
        return b""
    try:
        req = urllib.request.Request(url, headers=JSON_HEADERS)
        with urllib.request.urlopen(req, timeout=timeout) as r:
            data = r.read()
    except Exception:
        return b""
    return data if len(data) > 512 else b""


def _local_meta(path: Path, ext: str) -> dict:
    """写入前再看一眼本地现状（封面尺寸决定 fill 模式要不要换图）。"""
    try:
        from mutagen import File as MutagenFile
        audio = MutagenFile(str(path))
    except Exception:
        audio = None
    if ext == ".flac":
        meta = _flac_meta(audio) if audio is not None else _flac_meta(None)
    elif ext == ".mp3":
        tags = getattr(audio, "tags", None) if audio is not None else None
        meta = _id3_meta(tags if tags is not None else _id3_tags(path))
    elif ext == ".m4a":
        meta = _m4a_meta(audio) if audio is not None else _m4a_meta(None)
    else:
        meta = {"title": "", "artist": "", "album": "", "year": "", "cover": b""}
    # 时长用来核对「是不是同一段录音」，读不出来就 0（不参与比较）
    try:
        meta["duration"] = int(round(getattr(getattr(audio, "info", None), "length", 0) or 0))
    except Exception:
        meta["duration"] = 0
    return meta


def _want(local: dict, cand: dict, fields: dict, mode: str) -> dict:
    """算出这次到底要写哪几项。返回 {字段: 值}，cover 用 cover_url 表示。"""
    overwrite = mode == "overwrite"
    out: dict = {}

    title = str(cand.get("title") or "").strip()
    artist = str(cand.get("artist") or "").strip()
    album = str(cand.get("album") or "").strip()
    year = _year(cand.get("year"))
    song_id = int(cand.get("song_id") or 0)

    if fields.get("title_artist"):
        if title and (overwrite or not local["title"]):
            out["title"] = title
        if artist and (overwrite or not local["artist"]):
            out["artist"] = artist
    if fields.get("album") and album and (overwrite or not local["album"]):
        out["album"] = album
    if fields.get("year") and year and (overwrite or not local["year"]):
        out["year"] = year
    # 候选没带 song_id（比如选了 iTunes）时，宁可不写，也不能把已有的 ID 抹掉
    if fields.get("netease_id") and song_id > 0:
        if overwrite or not local.get("netease_id"):
            out["netease_id"] = song_id

    if fields.get("cover"):
        cover_url = str(cand.get("cover_url") or "")
        w, h = image_size(local.get("cover") or b"")
        low = w <= 0 or w < LOW_COVER or h < LOW_COVER
        if cover_url and (overwrite or low):
            out["cover_url"] = cover_url
    return out


def _write_flac(path: Path, want: dict, cover: bytes) -> None:
    from mutagen.flac import FLAC, Picture
    audio = FLAC(str(path))
    for key in ("title", "artist", "album"):
        if key in want:
            audio[key] = want[key]
    if "year" in want:
        audio["date"] = want["year"]
    if "netease_id" in want:
        audio["NETEASE_ID"] = str(want["netease_id"])
    if cover:
        # 只换正面图（type 3）：封底、艺人照这类原样留着
        kept = [p for p in audio.pictures if p.type != 3]
        audio.clear_pictures()
        for p in kept:
            audio.add_picture(p)
        pic = Picture()
        pic.type = 3
        pic.mime = _cover_mime(cover)
        pic.desc = "Cover"
        pic.data = cover
        audio.add_picture(pic)
    audio.save()


def _write_mp3(path: Path, want: dict, cover: bytes) -> None:
    from mutagen.id3 import (APIC, ID3, ID3NoHeaderError, TALB, TDRC,
                             TIT2, TPE1, TXXX)
    try:
        tag = ID3(str(path))
    except ID3NoHeaderError:
        tag = ID3()
    if "title" in want:
        tag.setall("TIT2", [TIT2(encoding=3, text=want["title"])])
    if "artist" in want:
        tag.setall("TPE1", [TPE1(encoding=3, text=want["artist"])])
    if "album" in want:
        tag.setall("TALB", [TALB(encoding=3, text=want["album"])])
    if "year" in want:
        tag.setall("TDRC", [TDRC(encoding=3, text=want["year"])])
    if "netease_id" in want:
        tag.delall("TXXX:NETEASE_ID")
        tag.add(TXXX(encoding=3, desc="NETEASE_ID", text=str(want["netease_id"])))
    if cover:
        # 只删正面封面，别把封底一起清了
        for key in list(tag.keys()):
            if not key.startswith("APIC:"):
                continue
            frame = tag[key]
            if getattr(frame, "type", 3) == 3:
                del tag[key]
        tag.add(APIC(encoding=3, mime=_cover_mime(cover), type=3,
                     desc="Cover", data=cover))
    tag.save(str(path))


def _write_m4a(path: Path, want: dict, cover: bytes) -> None:
    from mutagen.mp4 import MP4, MP4Cover, MP4FreeForm
    audio = MP4(str(path))
    if audio.tags is None:
        audio.add_tags()
    tags = audio.tags
    if "title" in want:
        tags["\xa9nam"] = [want["title"]]
    if "artist" in want:
        tags["\xa9ART"] = [want["artist"]]
    if "album" in want:
        tags["\xa9alb"] = [want["album"]]
    if "year" in want:
        tags["\xa9day"] = [want["year"]]
    if "netease_id" in want:
        # 默认 dataformat 就是 UTF-8，不必再传常量（各版本常量名还不一样）
        tags["----:com.apple.iTunes:NETEASE_ID"] = [
            MP4FreeForm(str(want["netease_id"]).encode("utf-8"))
        ]
    if cover:
        fmt = (MP4Cover.FORMAT_PNG if _cover_mime(cover) == "image/png"
               else MP4Cover.FORMAT_JPEG)
        tags["covr"] = [MP4Cover(cover, imageformat=fmt)]
    audio.save()


WRITERS = {".flac": _write_flac, ".mp3": _write_mp3, ".m4a": _write_m4a,
           ".mp4": _write_m4a}


_RESOLVE_NOTES = {
    "rate_limited": "网易云限速，先暂停",
    "ambiguous": "网易云有多条都对得上（可能是不同录音版本），请手动挑一条",
    "error": "网易云搜索失败，稍后再试",
}


def _resolve_note(reason: str) -> str:
    return _RESOLVE_NOTES.get(reason, "网易云没搜到歌名/歌手/专辑全对得上的")


def apply_one(item: dict, by_id: dict | None = None,
              search_cache: dict | None = None) -> dict:
    """写一首。返回 {path, ok, changed, note, readonly}。

    计划里没带 candidate、只带了 netease_id 时，用 by_id 里预取到的那条
    （apply_plan 会一次性把所有 ID 补齐，省得一首一个请求）。
    """
    path = Path(str(item.get("path") or ""))
    cand = item.get("candidate") or {}
    fields = item.get("fields") or {}
    mode = "overwrite" if str(item.get("mode") or "") == "overwrite" else "fill"
    res = {"path": str(path), "ok": False, "changed": [], "note": "",
           "readonly": False, "skipped": False, "cover_px": "", "cover_kept": False,
           "needs_pick": False}

    if not path.is_file():
        res["note"] = "文件不存在"
        return res
    ext = path.suffix.lower()
    writer = WRITERS.get(ext)
    if writer is None:
        res["note"] = "只读跳过：这个格式不支持稳定写标签"
        res["readonly"] = True
        return res

    local = _local_meta(path, ext)
    if not local.get("netease_id"):
        try:
            local["netease_id"] = int(tx.netease_id_of(path) or 0)
        except Exception:
            local["netease_id"] = 0

    if not cand:
        if str(item.get("resolve") or "") == "netease":
            # 「按网易云补全」：拿本地歌名/歌手去搜，只认唯一一条全对得上的
            cand, reason = resolve_by_search(local, path, search_cache)
            if not cand:
                res["note"] = _resolve_note(reason)
                res["skipped"] = True
                res["needs_pick"] = reason == "ambiguous"
                return res
        else:
            # 计划里没带候选（Go 侧「按 ID 补全」发来的就是这样）：按 ID 取那首歌
            want_id = int(item.get("netease_id") or 0)
            cand = (by_id or {}).get(want_id) or {}
            if not cand:
                res["note"] = "按 NETEASE_ID 没取到这首歌（可能被限速）"
                res["skipped"] = True
                return res

    want = _want(local, cand, fields, mode)
    if not want:
        res["note"] = "没有需要改动的字段"
        res["skipped"] = True
        return res

    cover = b""
    cover_px = ""
    kept_note = ""
    if want.get("cover_url"):
        cover_url = resize_cover_url(str(want.pop("cover_url")),
                                    int(item.get("cover_size") or 0))
        data = _download(cover_url)
        problem = cover_problem(data)
        if problem:
            # CDN 出错常回一张 HTML（200 + text/html，字节数还不小）：
            # 不校验就会把错误页当封面写进音频，还报「更新成功」。
            res["note"] = problem
        else:
            new_w, new_h = image_size(data)
            old_w, old_h = image_size(local.get("cover") or b"")
            if old_w and old_h and min(new_w, new_h) < min(old_w, old_h):
                # 网易云的 ?param=1200 只是请求尺寸，服务端不会放大：源图本来就 390px 时
                # 硬写进去就是把大图换成小图，宁可保留原图并把实情说出来。
                kept_note = f"源图 {new_w}×{new_h}，比本地 {old_w}×{old_h} 小，保留原图"
                res["cover_kept"] = True
            elif new_w <= 0 or new_h <= 0:
                res["note"] = "封面尺寸读不出来，没写"
            else:
                cover = data
                cover_px = f"{new_w}×{new_h}"
                want["cover"] = True

    if not want and not cover:
        if kept_note and not res["note"]:
            res["note"] = kept_note
        if not res["note"]:
            res["note"] = "没有需要改动的字段"
            res["skipped"] = True
        return res

    try:
        writer(path, want, cover)
    except Exception as exc:
        res["note"] = f"{type(exc).__name__}: {exc}"
        return res

    res["changed"] = [k for k in ("cover", "year", "netease_id", "album",
                                  "title", "artist") if k in want]
    if cover_px:
        res["cover_px"] = cover_px
    if kept_note and not res["note"]:
        res["note"] = kept_note
    res["ok"] = bool(res["changed"])
    if not res["ok"] and not res["note"]:
        res["note"] = "没有需要改动的字段"
    return res


def _skip_result(item: dict, note: str) -> dict:
    return {"path": str(item.get("path") or ""), "ok": False, "changed": [],
            "note": note, "readonly": False, "skipped": True,
            "cover_px": "", "cover_kept": False, "needs_pick": False}


def apply_plan(plan, on_item=None) -> dict:
    """按计划逐首写。on_item(i, total, result) 用来往外报进度。

    计划里只给 netease_id 的条目（「按 ID 批量补全」走这条路）：先把所有 ID
    一次请求补齐（同批重复的 ID 只问一次），再逐首写——一首一个请求会被限速。

    限速在这里统一兜住：第一次吃到 405/418/429 就停手，剩下的记成「暂停」并带上
    retry_after，交给调用方等冷却结束再从 resume_from 续跑；没跑到的条目也要
    报一条结果，进度条才不会停在半路。
    """
    items = plan.get("items") if isinstance(plan, dict) else plan
    items = [it for it in (items or []) if isinstance(it, dict)]
    total = len(items)

    def emit(i: int, res: dict) -> None:
        if on_item is not None:
            on_item(i, total, res)

    def pause(reason: str, start: int, ok: int, skipped: int, failed: int, picks: int) -> dict:
        wait = int(round(netauth.cooling_for())) + 1
        for j in range(start, total + 1):
            emit(j, _skip_result(items[j - 1], reason))
        return {"summary": True, "total": total, "ok": ok,
                "skipped": skipped + max(0, total - start + 1), "failed": failed,
                "needs_pick_count": picks, "paused": True,
                "retry_after": wait, "resume_from": start}

    if netauth.cooling_for() > 0:
        # 还在冷却里：一次请求都不发，直接说还要等多久
        return pause("网易云限速冷却中，先不请求", 1, 0, 0, 0, 0)

    by_id: dict[int, dict] = {}
    ids = sorted({int(it.get("netease_id") or 0) for it in items} - {0})
    for start in range(0, len(ids), 100):        # URL 别太长，分批问
        state: dict = {}
        for song in netease_details(ids[start:start + 100], state=state):
            cand = _netease_candidate(song)
            if cand["song_id"]:
                by_id[cand["song_id"]] = cand
        if state.get("rate_limited"):
            return pause("网易云限速，批量详情没取完，先暂停", 1, 0, 0, 0, 0)

    ok = failed = skipped = picks = 0
    last_search = 0.0
    search_cache: dict[tuple[str, str], tuple[dict, str]] = {}
    for i, item in enumerate(items, 1):
        if netauth.cooling_for() > 0:
            return pause("网易云限速，已暂停", i, ok, skipped, failed, picks)
        if str(item.get("resolve") or "") == "netease":
            gap = time.monotonic() - last_search
            if gap < RESOLVE_GAP:
                time.sleep(RESOLVE_GAP - gap)   # 一首一次搜索，别把网易云问急了
            last_search = time.monotonic()

        res = apply_one(item, by_id, search_cache)

        if netauth.cooling_for() > 0 and not res["ok"]:
            # 这一首没办成、而且是限速造成的：它算「待处理」，从本首续跑。
            # 这里不记它的结果，否则续跑时会重复统计，而 resume_from 若写成
            # i+1，这一首就永远没人再试了（只有一首时更是根本不会续跑）。
            return pause("网易云限速，已暂停", i, ok, skipped, failed, picks)

        if res["ok"]:
            ok += 1
        elif res.get("skipped") or res.get("readonly"):
            skipped += 1
        else:
            failed += 1
        if res.get("needs_pick"):
            picks += 1
        emit(i, res)

    return {"summary": True, "total": total, "ok": ok,
            "skipped": skipped, "failed": failed, "needs_pick_count": picks}


def run_inspect(target: str | Path | None, paths: list[str] | None = None) -> list[dict]:
    files: list[Path] = []
    for raw in paths or []:
        files += list_audio(Path(raw))
    if target:
        files += list_audio(Path(target))
    seen, out = set(), []
    for f in files:
        key = str(f).lower()
        if key in seen:
            continue
        seen.add(key)
        out.append(inspect_track(f))
    return out


def load_plan(path: str | Path) -> dict:
    data = json.loads(Path(path).read_text(encoding="utf-8"))
    if isinstance(data, list):
        return {"items": data}
    return data if isinstance(data, dict) else {"items": []}
