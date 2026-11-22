"""解平台的加密格式：网易云 NCM，以及 QQ 音乐的 QMC。

NCM 能完整解：音频、歌名/歌手/专辑、封面，并把网易云 ID 写进标签，
这样后面的歌词流程不用再搜。QQ 的 QMC 还没做——它要在文件里挖密钥，
手上没有真实样本可验证，宁可先不写。
"""

from __future__ import annotations

import base64
import json
import struct
from dataclasses import dataclass
from pathlib import Path

from . import ncm163

NCM_MAGIC = b"CTENFDAM"
NCM_KEY = b"hzHRAmso5kInbaxW"     # 音频流密钥那一段
NCM_STREAM_PREFIX = b"neteasecloudmusic"
NCM_META_PREFIX = "163 key(Don't modify):"

# QQ 的加密格式，暂时只认得出来、解不开
QQ_FORMATS = (".qmc0", ".qmc2", ".qmc3", ".qmcflac", ".qmcogg",
              ".mflac", ".mflac0", ".mflac2", ".mflac4",
              ".mgg", ".mgg0", ".mgg1", ".mgg2", ".mgg4", ".mggl",
              ".bkc", ".bkcmp3", ".bkcflac", ".bkcogg")


class NotEncrypted(ValueError):
    """不是认识的加密文件。"""


class Damaged(ValueError):
    """认出来了，但字段读不下去。"""


@dataclass
class Decrypted:
    path: Path
    format: str            # mp3 / flac
    title: str = ""
    artist: str = ""
    album: str = ""
    song_id: int = 0
    cover: bytes = b""


def looks_encrypted(path: str | Path) -> bool:
    p = Path(path)
    if p.suffix.lower() in QQ_FORMATS:
        return True
    try:
        with open(p, "rb") as f:
            return f.read(8) == NCM_MAGIC
    except OSError:
        return False


def _read_u32(buf: bytes, at: int) -> int:
    if at + 4 > len(buf):
        raise Damaged("文件截断")
    return struct.unpack("<I", buf[at:at + 4])[0]


def _keybox(key: bytes) -> list[int]:
    """NCM 的音频密钥流：KSA 是标准的，PRGA 被改过。"""
    box = list(range(256))
    j = 0
    for i in range(256):
        j = (j + box[i] + key[i % len(key)]) % 256
        box[i], box[j] = box[j], box[i]
    return box


def _decrypt_audio(data: bytes, key: bytes) -> bytes:
    box = _keybox(key)
    out = bytearray(data)
    for p in range(len(out)):
        i = (p + 1) & 0xFF
        a = box[i]
        b = box[(a + i) & 0xFF]
        out[p] ^= box[(a + b) & 0xFF]
    return bytes(out)


def _audio_format(head: bytes) -> str:
    if head[:4] == b"fLaC":
        return "flac"
    if head[:3] == b"ID3":
        return "mp3"
    if len(head) >= 2 and head[0] == 0xFF and (head[1] & 0xE0) == 0xE0:
        return "mp3"
    return ""


def _parse_meta(blob: bytes) -> dict:
    """元信息块：异或 0x63 → 去前缀 → base64 → AES → music: JSON。"""
    raw = bytes(b ^ 0x63 for b in blob)
    text = raw.decode("utf-8", "ignore")
    if not text.startswith(NCM_META_PREFIX):
        return {}
    b64 = text[len(NCM_META_PREFIX):].strip()
    try:
        cipher = base64.b64decode(b64)
        plain = ncm163.decrypt_ecb(cipher, key=ncm163._KEY)
    except Exception:
        return {}
    if not plain.startswith(b"music:"):
        return {}
    try:
        info = json.loads(plain[6:].decode("utf-8"))
    except Exception:
        return {}
    return info if isinstance(info, dict) else {}


def decrypt_ncm(path: str | Path, out_dir: str | Path | None = None,
                write_tags: bool = True) -> Decrypted:
    """把一个 NCM 解成普通音频，元信息与封面一并写回标签。"""
    src = Path(path)
    data = src.read_bytes()
    if data[:8] != NCM_MAGIC:
        raise NotEncrypted(f"{src.name} 不是 NCM")

    at = 10
    key_len = _read_u32(data, at)
    at += 4
    key_blob = bytes(b ^ 0x64 for b in data[at:at + key_len])
    at += key_len
    try:
        stream_key = ncm163.decrypt_ecb(key_blob, key=NCM_KEY)
    except Exception as e:
        raise Damaged(f"密钥区解不开：{e}")
    if not stream_key.startswith(NCM_STREAM_PREFIX):
        raise Damaged("密钥区格式不对")
    stream_key = stream_key[len(NCM_STREAM_PREFIX):].strip(b"\x00")

    meta_len = _read_u32(data, at) if at + 4 <= len(data) else 0
    at += 4
    meta = _parse_meta(data[at:at + meta_len]) if meta_len else {}
    at += meta_len

    # 元信息之后：CRC(4) + 1 字节，然后才是封面帧（实测如此，文档写的 5 字节不对）
    at += 5
    cover = b""
    if at + 4 <= len(data):
        frame_len = _read_u32(data, at)
        frame_start = at
        at += 4
        if frame_len:
            image_len = _read_u32(data, at)
            cover = data[at + 4:at + 4 + image_len]
            # frameLen 数的是它后面那段（imageLen + 图片 + 补白）
            at = frame_start + 4 + frame_len
    audio_start = min(at, len(data))

    audio = _decrypt_audio(data[audio_start:], stream_key)
    fmt = _audio_format(audio[:8])
    if not fmt:
        # 封面帧长度有的实现不含那 4 字节，两头都试一下
        for shift in (4, -4):
            guess = data[max(0, audio_start + shift):]
            if not guess:
                continue
            trial = _decrypt_audio(guess, stream_key)
            fmt = _audio_format(trial[:8])
            if fmt:
                audio = trial
                break
    if not fmt:
        raise Damaged("解密后认不出音频格式")

    out_dir = Path(out_dir) if out_dir else src.parent
    out_dir.mkdir(parents=True, exist_ok=True)
    name = _output_name(src, meta)
    target = out_dir / f"{name}.{fmt}"
    n = 1
    while target.exists() and target.resolve() != src.resolve():
        n += 1
        target = out_dir / f"{name} ({n}).{fmt}"
    target.write_bytes(audio)

    info = Decrypted(path=target, format=fmt,
                     title=str(meta.get("musicName") or name),
                     artist=_artists(meta),
                     album=str(meta.get("album") or ""),
                     song_id=int(meta.get("musicId") or 0),
                     cover=cover)
    if write_tags:
        _write_tags(info)
    return info


def _output_name(src: Path, meta: dict) -> str:
    title = str(meta.get("musicName") or "").strip()
    artist = _artists(meta).strip()
    if title and artist:
        return f"{title} - {artist}"
    return title or src.stem


def _artists(meta: dict) -> str:
    out = []
    for item in meta.get("artist") or []:
        if isinstance(item, (list, tuple)) and item:
            out.append(str(item[0]))
        elif isinstance(item, dict):
            out.append(str(item.get("name") or ""))
        elif isinstance(item, str):
            out.append(item)
    return ", ".join(x for x in out if x and not x.isdigit())


def _write_tags(info: Decrypted) -> None:
    """歌名、歌手、专辑、封面，以及网易云 ID（后面下歌词要用）。"""
    try:
        if info.format == "mp3":
            from mutagen.id3 import APIC, ID3, TALB, TIT2, TPE1, TXXX
            tag = ID3()
            if info.title:
                tag.add(TIT2(encoding=3, text=info.title))
            if info.artist:
                tag.add(TPE1(encoding=3, text=info.artist))
            if info.album:
                tag.add(TALB(encoding=3, text=info.album))
            if info.song_id:
                tag.add(TXXX(encoding=3, desc="NETEASE_ID", text=str(info.song_id)))
            if info.cover:
                tag.add(APIC(encoding=3, mime=_cover_mime(info.cover), type=3,
                             desc="Cover", data=info.cover))
            tag.save(info.path)
        elif info.format == "flac":
            from mutagen.flac import FLAC, Picture
            audio = FLAC(info.path)
            if info.title:
                audio["title"] = info.title
            if info.artist:
                audio["artist"] = info.artist
            if info.album:
                audio["album"] = info.album
            if info.song_id:
                audio["NETEASE_ID"] = str(info.song_id)
            if info.cover:
                pic = Picture()
                pic.type = 3
                pic.mime = _cover_mime(info.cover)
                pic.desc = "Cover"
                pic.data = info.cover
                audio.add_picture(pic)
            audio.save()
    except Exception:
        # 标签写不进去不该让音频白解一遍
        pass


def _cover_mime(data: bytes) -> str:
    if data[:3] == b"\xff\xd8\xff":
        return "image/jpeg"
    if data[:8] == b"\x89PNG\r\n\x1a\n":
        return "image/png"
    return "image/jpeg"


def decrypt(path: str | Path, out_dir: str | Path | None = None,
            write_tags: bool = True) -> Decrypted:
    """按扩展名/文件头选解密方式。"""
    p = Path(path)
    if p.suffix.lower() in QQ_FORMATS:
        raise NotEncrypted(f"{p.name}：QQ 的加密格式还没支持")
    return decrypt_ncm(p, out_dir=out_dir, write_tags=write_tags)
