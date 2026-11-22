"""网易云 163 key：AES-128-ECB 解密与标签读取。"""

from mutagen.id3 import COMM, ID3, TXXX

from kiralrc import ncm163
from kiralrc import text as tx

# 一首真实网易云文件里的 163 key（《空も飛べるはず》，musicId 28661547）
BLOB = (
    "L64FU3W4YxX3ZFTmbZ+8/QsWzPVPohTa0w4VmHq5Waq7lAmWu17jyNV02ymzB3erpKiIto4VgpTumRSaHbtbIvR4ie40"
    "+tCXQgi9AZZ9cthvA6AqVL6cBBbHybbujAAweYVbKT+/Jdn1biR3RsEwt84Vs5ceriTcLl6NZuirBul6jStZ1nkFUPFY"
    "fMwvU6xI5xcUekbgkNFGV7KgtA1zYrfDpD3Z1TBPNnceSnMaBDrUoSjHsICkSAbshLmtbeBuc11+kY6tqELLc/j2DeELdxex"
    "awbjK+jlhvyX5PV9chHXh03VswhMU5c+hnUVsjXDq/BtM1CmjSgb8/7gDNXbH6f4TICoQq4YlL1jEkkPZqo+l0q/SDT2D+X"
    "RZ2AplM6rsnnPnJ/hrVZsRer2bpHOTVgJL0iy1L3SMrxVmhTSRQd5R8Xiy4/IB9r/d2B2pDmy/USaBn2S6kDOdzKkqbO9g2"
    "80JGNue5SBOBINcLdRFpTH9NfkcA5iR/ptOIZDvaefbZ3qaT7mOhrul58uWc61GfBZqEdiF77PsqVcyN8k2nr1VZqmLWyskT"
    "d0ahRkrJ5q"
)


def test_aes_decrypts_a_real_key():
    import base64

    plain = ncm163.decrypt_ecb(base64.b64decode(BLOB))
    assert plain.startswith(b"music:{")
    assert b'"musicId":28661547' in plain


def test_music_id_from_both_spellings():
    assert ncm163.music_id(BLOB) == 28661547
    assert ncm163.music_id(ncm163.PREFIX + BLOB) == 28661547
    assert ncm163.music_id("") is None
    assert ncm163.music_id("不是密钥") is None
    # 16 字节的占位值（同一段出现在很多老文件里），解不出东西
    assert ncm163.music_id("6YURAByiRHQ6CI5tvtxzQg==") is None


def _mp3_with(path, frame):
    # 一个完整的 128kbps/44.1kHz 帧，重复几次，好让 mutagen 认得出是 MP3
    path.write_bytes((b"\xff\xfb\x90\x00" + b"\x00" * 413) * 6)
    tag = ID3()
    tag.add(frame)
    tag.save(path)
    return path


def test_id_read_from_a_netease_file(tmp_path):
    p = _mp3_with(tmp_path / "a.mp3",
                  COMM(encoding=1, lang="XXX", desc="", text=ncm163.PREFIX + BLOB))
    assert tx.netease_id_from_file(p) == 28661547


def test_cloudys_own_tag_is_read(tmp_path):
    p = _mp3_with(tmp_path / "b.mp3",
                  TXXX(encoding=1, desc="NETEASE_ID", text="30967650"))
    assert tx.netease_id_from_file(p) == 30967650


def test_the_163_key_comes_first(tmp_path):
    """两个都有时，用网易云的 163 key：它来自官方客户端，覆盖的人多。"""
    p = _mp3_with(tmp_path / "d.mp3",
                  COMM(encoding=1, lang="XXX", desc="", text=ncm163.PREFIX + BLOB))
    tag = ID3(str(p))
    tag.add(TXXX(encoding=1, desc="NETEASE_ID", text="30967650"))
    tag.save(p)
    assert tx.netease_id_from_file(p) == 28661547


def test_id_from_downloads_json(tmp_path, monkeypatch):
    """下载记录按「上一级目录/文件名」比对，跨盘符也能对上。"""
    monkeypatch.setattr(tx, "_DOWNLOADS", {
        "cloudy music/song.mp3": 12345,
    })
    p = tmp_path / "cloudy music" / "song.mp3"
    p.parent.mkdir()
    p.write_bytes(b"")
    assert tx.netease_id_from_downloads(p) == 12345
    assert tx.netease_id_from_downloads(tmp_path / "other.mp3") is None


def test_a_file_without_tags_has_no_id(tmp_path):
    p = tmp_path / "c.mp3"
    p.write_bytes(b"\xff\xfb" + b"\x00" * 256)
    assert tx.netease_id_from_file(p) is None
