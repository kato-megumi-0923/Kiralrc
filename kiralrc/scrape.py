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
import urllib.parse
import urllib.request
from pathlib import Path

from . import text as tx

# 能稳定写标签的容器。WAV / APE 之类只读出来给人看，标 writable: false。
WRITABLE = {".flac": "flac", ".mp3": "mp3", ".m4a": "m4a", ".mp4": "m4a"}
AUDIO_EXTS = {".flac", ".mp3", ".m4a", ".mp4", ".wav", ".aif", ".aiff",
              ".ape", ".wv", ".ogg", ".opus", ".dsf"}

HIRES = 1200          # 高清封面请求边长
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

def _pic_url(url: str, size: int) -> str:
    if not url:
        return ""
    if "?" in url:
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
        "cover_url": _pic_url(pic, HIRES),
        "cover_hint": f"≤{HIRES}×{HIRES}",
    }


def netease_details(ids: list[int], netease_json=None) -> list[dict]:
    """一次请求补齐若干首歌的 album.picUrl / publishTime。"""
    ids = [i for i in ids if i]
    if not ids:
        return []
    get = netease_json or tx._get_json
    url = ("https://music.163.com/api/song/detail?ids=[%s]"
           % ",".join(str(i) for i in ids))
    try:
        data = get(url) or {}
    except tx.RateLimited:
        return []
    except Exception:
        return []
    songs = data.get("songs") or []
    return [s for s in songs if isinstance(s, dict)]


def _netease_candidates(query, artist, title, netease_id, limit,
                        netease_search=None, netease_json=None) -> list[dict]:
    search = netease_search or tx.netease_search
    out: list[dict] = []
    seen: set[int] = set()

    if netease_id:
        for song in netease_details([int(netease_id)], netease_json):
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
        except Exception:
            hits = []
        ids = [int(h["id"]) for h in hits if h.get("id") and int(h["id"]) not in seen]
        detailed = netease_details(ids, netease_json)
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


def _artwork(url: str, size: int) -> str:
    if not url:
        return ""
    # iTunes 给的形如 .../100x100bb.jpg，换成想要的边长
    return re.sub(r"/\d+x\d+bb\.", f"/{size}x{size}bb.", url)


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
            "cover_url": _artwork(art, HIRES),
            "cover_hint": f"≤{HIRES}×{HIRES}",
        })
    return out


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
                      itunes_json=None) -> list[dict]:
    """网易云 + iTunes 一起搜，返回可直接喂给画廊的候选。"""
    netease = _netease_candidates(query, artist, title, netease_id, limit,
                                  netease_search, netease_json)
    itunes = _itunes_candidates(query, artist, title, itunes_json)
    merged = _interleave(netease, itunes)
    if limit and limit > 0:
        return merged[:limit]
    return merged


# ---------- 写入 ----------

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
        return _flac_meta(audio) if audio is not None else _flac_meta(None)
    if ext == ".mp3":
        tags = getattr(audio, "tags", None) if audio is not None else None
        return _id3_meta(tags if tags is not None else _id3_tags(path))
    if ext == ".m4a":
        return _m4a_meta(audio) if audio is not None else _m4a_meta(None)
    return {"title": "", "artist": "", "album": "", "year": "", "cover": b""}


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


def apply_one(item: dict, by_id: dict | None = None) -> dict:
    """写一首。返回 {path, ok, changed, note, readonly}。

    计划里没带 candidate、只带了 netease_id 时，用 by_id 里预取到的那条
    （apply_plan 会一次性把所有 ID 补齐，省得一首一个请求）。
    """
    path = Path(str(item.get("path") or ""))
    cand = item.get("candidate") or {}
    fields = item.get("fields") or {}
    mode = "overwrite" if str(item.get("mode") or "") == "overwrite" else "fill"
    res = {"path": str(path), "ok": False, "changed": [], "note": "",
           "readonly": False, "skipped": False, "cover_px": "", "cover_kept": False}

    if not path.is_file():
        res["note"] = "文件不存在"
        return res
    ext = path.suffix.lower()
    writer = WRITERS.get(ext)
    if writer is None:
        res["note"] = "只读跳过：这个格式不支持稳定写标签"
        res["readonly"] = True
        return res

    if not cand:
        # 计划里没带候选（Go 侧「按 ID 补全」发来的就是这样）：按 ID 取那首歌
        want_id = int(item.get("netease_id") or 0)
        cand = (by_id or {}).get(want_id) or {}
        if not cand:
            res["note"] = "按 NETEASE_ID 没取到这首歌（可能被限速）"
            res["skipped"] = True
            return res

    local = _local_meta(path, ext)
    if not local.get("netease_id"):
        try:
            local["netease_id"] = int(tx.netease_id_of(path) or 0)
        except Exception:
            local["netease_id"] = 0

    want = _want(local, cand, fields, mode)
    if not want:
        res["note"] = "没有需要改动的字段"
        res["skipped"] = True
        return res

    cover = b""
    cover_px = ""
    kept_note = ""
    if want.get("cover_url"):
        data = _download(str(want.pop("cover_url")))
        new_w, new_h = image_size(data)
        old_w, old_h = image_size(local.get("cover") or b"")
        if not data:
            res["note"] = "高清封面下载失败"
        elif old_w and old_h and min(new_w, new_h) < min(old_w, old_h):
            # 网易云的 ?param=1200 只是请求尺寸，服务端不会放大：源图本来就 390px 时
            # 硬写进去就是把大图换成小图，宁可保留原图并把实情说出来。
            kept_note = f"源图 {new_w}×{new_h}，比本地 {old_w}×{old_h} 小，保留原图"
            res["cover_kept"] = True
        else:
            cover = data
            cover_px = f"{new_w}×{new_h}"
            want["cover"] = True

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


def apply_plan(plan, on_item=None) -> dict:
    """按计划逐首写。on_item(i, total, result) 用来往外报进度。

    计划里只给 netease_id 的条目（「按 ID 批量补全」走这条路）：先把所有 ID
    一次请求补齐，再逐首写——一首一个请求会被网易云限速。
    """
    items = plan.get("items") if isinstance(plan, dict) else plan
    items = [it for it in (items or []) if isinstance(it, dict)]
    total = len(items)

    by_id: dict[int, dict] = {}
    ids = sorted({int(it.get("netease_id") or 0) for it in items} - {0})
    for start in range(0, len(ids), 100):        # URL 别太长，分批问
        for song in netease_details(ids[start:start + 100]):
            cand = _netease_candidate(song)
            if cand["song_id"]:
                by_id[cand["song_id"]] = cand

    ok = failed = skipped = 0
    for i, item in enumerate(items, 1):
        res = apply_one(item, by_id)
        if res["ok"]:
            ok += 1
        elif res.get("skipped") or res.get("readonly"):
            skipped += 1
        else:
            failed += 1
        if on_item is not None:
            on_item(i, total, res)
    return {"summary": True, "total": total, "ok": ok,
            "skipped": skipped, "failed": failed}


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
