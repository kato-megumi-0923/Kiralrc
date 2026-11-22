"""NCM 解密：自己造一个 NCM，再解回来。"""

import base64
import json
import struct

from mutagen.id3 import ID3

from kiralrc import decrypt, ncm163

STREAM_KEY = b"0123456789abcdef"
AUDIO = b"ID3\x04\x00\x00\x00\x00\x00\x00" + b"\x00" * 240


def build_ncm(meta: dict, cover: bytes = b"", audio: bytes = AUDIO) -> bytes:
    """按 NCM 的规矩造一个文件，用来验证解密。"""
    key_plain = b"neteasecloudmusic" + STREAM_KEY
    key_blob = bytes(b ^ 0x64 for b in ncm163.encrypt_ecb(key_plain, key=decrypt.NCM_KEY))

    meta_plain = (decrypt.NCM_META_PREFIX.encode()
                  + base64.b64encode(ncm163.encrypt_ecb(b"music:" + json.dumps(meta).encode())))
    meta_blob = bytes(b ^ 0x63 for b in meta_plain)

    enc_audio = decrypt._decrypt_audio(audio, STREAM_KEY)   # 异或，正反同一个函数

    frame = b""
    if cover:
        # frameLen 数的是它后面那段：imageLen + 图片 + 补白
        frame = struct.pack("<I", len(cover)) + cover

    return (decrypt.NCM_MAGIC + b"\x01\x70"
            + struct.pack("<I", len(key_blob)) + key_blob
            + struct.pack("<I", len(meta_blob)) + meta_blob
            + b"\x00" * 4 + b"\x00"   # CRC(4) + 1 字节
            + struct.pack("<I", len(frame)) + frame
            + enc_audio)


META = {
    "musicId": 167732,
    "musicName": "千百度",
    "artist": [["许嵩", 1234]],
    "album": "寻雾启示",
    "format": "mp3",
}


def test_audio_and_metadata_come_back(tmp_path):
    src = tmp_path / "千百度.ncm"
    src.write_bytes(build_ncm(META))

    info = decrypt.decrypt_ncm(src, write_tags=False)   # 不写标签才好逐字节比
    assert info.format == "mp3"
    assert info.title == "千百度" and info.artist == "许嵩"
    assert info.album == "寻雾启示" and info.song_id == 167732
    # 默认沿用 .ncm 的名字，同名 .lrc 才配得上；要改名用 rename=True
    assert info.path.name == "千百度.mp3"
    assert info.path.read_bytes() == AUDIO


def test_the_id_lands_in_the_tags(tmp_path):
    src = tmp_path / "a.ncm"
    src.write_bytes(build_ncm(META))
    info = decrypt.decrypt_ncm(src)

    tags = ID3(str(info.path))
    assert str(tags["TIT2"]) == "千百度"
    assert str(tags["TPE1"]) == "许嵩"
    assert str(tags["TXXX:NETEASE_ID"]) == "167732"


def test_cover_is_carried_over(tmp_path):
    cover = b"\xff\xd8\xff\xe0" + b"\x00" * 60          # 一段"JPEG"
    src = tmp_path / "b.ncm"
    src.write_bytes(build_ncm(META, cover=cover))
    info = decrypt.decrypt_ncm(src)
    assert info.cover == cover
    pics = ID3(str(info.path)).getall("APIC")
    assert pics and pics[0].data == cover


def test_a_file_that_is_not_ncm_is_refused(tmp_path):
    p = tmp_path / "x.mp3"
    p.write_bytes(b"\xff\xfb" + b"\x00" * 64)
    try:
        decrypt.decrypt_ncm(p)
    except decrypt.NotEncrypted:
        return
    raise AssertionError("应当拒绝非 NCM")


def test_qq_formats_are_named_as_unsupported(tmp_path):
    p = tmp_path / "x.mflac"
    p.write_bytes(b"fLaC" + b"\x00" * 32)
    assert decrypt.looks_encrypted(p)
    try:
        decrypt.decrypt(p)
    except decrypt.NotEncrypted as e:
        assert "QQ" in str(e)
        return
    raise AssertionError("QQ 格式应当报「没支持」，而不是硬解")


def test_rename_gives_title_and_artist(tmp_path):
    src = tmp_path / "01.ncm"
    src.write_bytes(build_ncm(META))
    info = decrypt.decrypt_ncm(src, write_tags=False, rename=True)
    assert info.path.name == "千百度 - 许嵩.mp3"


# ---- 元信息格式不规范时的边界 ----

def _minimal_flac(pictures=()) -> bytes:
    """一个能做标签读写的极简 FLAC（只有 STREAMINFO）。"""
    import struct
    info = struct.pack(">HH", 4096, 4096) + b"\x00" * 6
    packed = (44100 << 44) | (1 << 41) | (15 << 36) | 44100
    info += packed.to_bytes(8, "big") + b"\x00" * 16
    return b"fLaC" + bytes([0x80, 0, 0, len(info)]) + info


def test_artist_as_dict_or_string(tmp_path):
    from mutagen.id3 import ID3

    for i, artist in enumerate(({"name": "许嵩", "id": 5771}, "许嵩", ["许嵩"])):
        meta = dict(META, artist=artist)
        src = tmp_path / f"a{i}.ncm"
        src.write_bytes(build_ncm(meta))
        info = decrypt.decrypt_ncm(src)
        tags = ID3(str(info.path))
        assert str(tags["TPE1"]) == "许嵩", artist
        assert str(tags["TXXX:NETEASE_ARTIST"]) == "许嵩"
        if isinstance(artist, dict):
            assert str(tags["TXXX:NETEASE_ARTIST_IDS"]) == "5771"


def test_a_bad_duration_only_skips_the_length(tmp_path):
    from mutagen.id3 import ID3

    src = tmp_path / "b.ncm"
    src.write_bytes(build_ncm(dict(META, duration="unknown")))
    info = decrypt.decrypt_ncm(src)
    tags = ID3(str(info.path))
    assert str(tags["TIT2"]) == "千百度"                  # 别的标签照写
    assert str(tags["TXXX:NETEASE_ID"]) == "167732"
    assert "TLEN" not in tags                            # 只少了时长


def test_a_back_cover_does_not_block_the_front_one(tmp_path):
    from mutagen.flac import FLAC, Picture
    import io

    # NCM 里的音频自带一张封底（type 4）
    audio = _minimal_flac()
    tmp = tmp_path / "with-back.flac"
    tmp.write_bytes(audio)
    f = FLAC(str(tmp))
    back = Picture()
    back.type = 4
    back.mime = "image/png"
    back.data = b"\x89PNG\r\n\x1a\n" + b"\x00" * 40
    f.add_picture(back)
    f.save()
    audio_with_back = io.open(tmp, "rb").read()

    cover = b"\x89PNG\r\n\x1a\n" + b"\xff" * 80
    src = tmp_path / "c.ncm"
    src.write_bytes(build_ncm(META, cover=cover, audio=audio_with_back))
    info = decrypt.decrypt_ncm(src)

    pics = FLAC(str(info.path)).pictures
    assert any(p.type == 3 for p in pics), [(p.type, len(p.data)) for p in pics]
    assert any(p.type == 4 for p in pics)          # 原来的封底还在


def test_rename_replaces_characters_windows_refuses(tmp_path):
    src = tmp_path / "01.ncm"
    src.write_bytes(build_ncm(dict(META, musicName="A/B:C*?")))
    info = decrypt.decrypt_ncm(src, write_tags=False, rename=True)
    assert info.path.parent == tmp_path
    assert "/" not in info.path.name and ":" not in info.path.name
    assert info.path.name.startswith("A_B_C__")
