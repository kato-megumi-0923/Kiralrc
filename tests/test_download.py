"""下载：QRC/TTML/KRC 解析、状态判定、写盘规则。"""

import base64
import zlib

from kiralrc import download, sources


def test_qrc_words_are_read():
    raw = ('<?xml version="1.0" encoding="utf-8"?>\n<QrcInfos><LyricInfo>'
           '<Lyric_1 LyricType="1" LyricContent="[ti:测]\n'
           '[100,1540]初(100,154)め(254,154)て(408,154)\n'
           '[2000,600]BiliBili (2000,300)AV(2300,300)\n"/></LyricInfo></QrcInfos>')
    lines = sources.parse_qrc(raw)
    assert [l.text for l in lines] == ["初めて", "BiliBili AV"]
    assert lines[0].start == 0.1 and lines[0].end == 1.64
    assert lines[0].chars[1] == ("め", 0.254)


def test_ttml_words_and_translation():
    raw = """<tt xmlns="http://www.w3.org/ns/ttml"><body><div>
      <p begin="00:00:01.000" end="00:00:03.000">
        <span begin="00:00:01.000" end="00:00:01.500">故</span>
        <span begin="00:00:01.500" end="00:00:02.000">事</span>
        <span role="x-translation" begin="00:00:01.000" end="00:00:03.000">故事</span>
      </p></div></body></tt>"""
    lines = sources.parse_ttml(raw)
    assert len(lines) == 1
    line = lines[0]
    assert line.text == "故事"
    assert line.chars == [("故", 1.0), ("事", 1.5)]
    assert line.translation == "故事"


def _make_krc(text: str) -> str:
    """按酷狗的规矩造一份 KRC：krc1 + 异或 + zlib + base64。"""
    raw = zlib.compress(text.encode("utf-8"))
    data = bytes(b ^ sources.KRC_KEY[i % len(sources.KRC_KEY)] for i, b in enumerate(raw))
    return base64.b64encode(b"krc1" + data).decode("ascii")


def test_krc_decode_and_parse():
    body = ("[ti:测试]\n"
            "[1000,1500]<0,300,0>初<300,300,0>め<600,300,0>て\n"
            "[3000,900]<0,450,0>の<450,450,0>恋\n")
    text = sources.decode_krc(_make_krc(body))
    lines = sources.parse_krc(text)
    assert [l.text for l in lines] == ["初めて", "の恋"]
    assert lines[0].start == 1.0 and lines[0].end == 2.5
    assert lines[0].chars[2] == ("て", 1.6)


def _lrc(tmp_path, name, body):
    p = tmp_path / name
    p.write_text(body, encoding="utf-8")
    return p


def test_lrc_kind_tells_official_from_aligned(tmp_path):
    audio = tmp_path / "a.mp3"
    audio.write_bytes(b"")
    assert download.lrc_kind(audio) == "missing"

    _lrc(tmp_path, "a.lrc", "[00:01.00]只有一行\n[00:02.00]第二行\n")
    assert download.lrc_kind(audio) == "line"

    _lrc(tmp_path, "a.lrc", "[by:NeteaseCloud]\n[00:01.000]<00:01.000>字<00:01.500>幕<00:02.000>\n")
    assert download.lrc_kind(audio) == "official"

    _lrc(tmp_path, "a.lrc", "[by:SOFA 歌声对齐]\n[00:01.000]<00:01.000>字<00:01.500>幕<00:02.000>\n")
    assert download.lrc_kind(audio) == "aligned"

    _lrc(tmp_path, "a.lrc", "[00:01.000]<00:01.000>字<00:01.500>幕<00:02.000>\n")
    assert download.lrc_kind(audio) == "word"


def test_official_replaces_aligned_without_leaving_backup(tmp_path, monkeypatch):
    audio = tmp_path / "b.mp3"
    audio.write_bytes(b"")
    old = "[by:SOFA 歌声对齐]\n[00:01.000]<00:01.000>旧<00:01.500>的<00:02.000>\n"
    _lrc(tmp_path, "b.lrc", old)

    monkeypatch.setattr(download.tx, "netease_id_of", lambda p: 123)
    monkeypatch.setattr(download.sources, "fetch", lambda *a, **k: sources.WordLyric(
        source="网易云", tag="NeteaseCloud",
        lines=[sources.WordLine(1.0, "新的", [("新", 1.0), ("的", 1.4)], 1.8)], timed=True))

    result = download.download_one(audio)
    assert result.status == "replaced"
    body = (tmp_path / "b.lrc").read_text(encoding="utf-8")
    assert "[by:NeteaseCloud]" in body
    assert "<00:01.000>新<00:01.400>的" in body
    # 按约定不留备份：目录里只有 .lrc，没有 .bak
    assert not (tmp_path / "b.lrc.bak").exists()
    assert sorted(p.name for p in tmp_path.iterdir()) == ["b.lrc", "b.mp3"]


def test_an_official_file_is_left_alone(tmp_path, monkeypatch):
    audio = tmp_path / "c.mp3"
    audio.write_bytes(b"")
    _lrc(tmp_path, "c.lrc", "[by:QQMusic]\n[00:01.000]<00:01.000>字<00:02.000>\n")
    called = []
    monkeypatch.setattr(download.sources, "fetch", lambda *a, **k: called.append(1))
    assert download.download_one(audio).status == "kept"
    assert not called


def test_line_lyrics_never_overwrite_what_is_there(tmp_path, monkeypatch):
    audio = tmp_path / "d.mp3"
    audio.write_bytes(b"")
    _lrc(tmp_path, "d.lrc", "[00:01.00]已有的一行\n")
    monkeypatch.setattr(download.tx, "netease_id_of", lambda p: 123)
    monkeypatch.setattr(download.sources, "fetch", lambda *a, **k: sources.WordLyric(
        source="网易云", tag="NeteaseCloud",
        lines=[sources.WordLine(1.0, "平台的一行", [], 2.0)], timed=False))
    assert download.download_one(audio).status == "kept"
    assert "已有的一行" in (tmp_path / "d.lrc").read_text(encoding="utf-8")


def test_instrumentals_are_skipped(tmp_path):
    for name in ("x - 伴奏.flac", "y - Off Vocal.mp3", "z - instrumental.wav"):
        (tmp_path / name).write_bytes(b"")
    counts = download.run(tmp_path)
    assert counts.all == 0


def test_our_own_files_are_marked_and_recognised(tmp_path):
    """自己对齐出来的 LRC 带 [kiralrc:aligned]，一眼就知道不是平台给的。"""
    from kiralrc import format as fmt
    from kiralrc import pipeline

    metas = pipeline._metas("歌", "人", "", "SOFA 歌声对齐")
    assert "[kiralrc:aligned]" in metas

    body = "[00:01.000]<00:01.000>字<00:01.500>幕<00:02.000>"
    text = "\n".join(metas + [body]) + "\n"
    parsed = fmt.parse(text)          # 解析器不能把它当歌词行丢掉
    assert "[kiralrc:aligned]" in parsed.metas

    audio = tmp_path / "e.mp3"
    audio.write_bytes(b"")
    (tmp_path / "e.lrc").write_text(text, encoding="utf-8")
    assert download.lrc_kind(audio) == "aligned"


def test_a_file_without_any_mark_is_word_not_aligned(tmp_path):
    audio = tmp_path / "f.mp3"
    audio.write_bytes(b"")
    (tmp_path / "f.lrc").write_text(
        "[00:01.000]<00:01.000>字<00:01.500>幕<00:02.000>\n", encoding="utf-8")
    assert download.lrc_kind(audio) == "word"


def test_an_empty_path_list_does_nothing(tmp_path, monkeypatch):
    """空清单是一首都不做，不能退回扫当前目录。"""
    monkeypatch.chdir(tmp_path)
    (tmp_path / "近处的歌.mp3").write_bytes(b"")
    monkeypatch.setattr(download.sources, "fetch", lambda *a, **k: None)
    counts = download.run("", paths=[])
    assert counts.all == 0 and counts.done == 0


def test_platform_name_alone_does_not_make_it_official(tmp_path, monkeypatch):
    """带 [by:NeteaseCloud] 但只有整行时间戳的，还是逐行，能继续升级。"""
    audio = tmp_path / "g.mp3"
    audio.write_bytes(b"")
    (tmp_path / "g.lrc").write_text(
        "[by:NeteaseCloud]\n[00:01.00]第一句\n[00:05.00]第二句\n", encoding="utf-8")
    assert download.lrc_kind(audio) == "line"

    monkeypatch.setattr(download.tx, "netease_id_of", lambda p: 123)
    monkeypatch.setattr(download.sources, "fetch", lambda *a, **k: sources.WordLyric(
        source="网易云", tag="NeteaseCloud",
        lines=[sources.WordLine(1.0, "第一句", [("第", 1.0), ("一", 1.2), ("句", 1.4)], 1.8)],
        timed=True))
    assert download.download_one(audio).status == "upgraded"


def test_name_and_artist_come_from_tags_then_filename(tmp_path):
    from pathlib import Path
    # 没有标签时按「标题 - 歌手」拆（本库的文件名就是这个样子）
    p = tmp_path / "千百度 - 许嵩.flac"
    p.write_bytes(b"")
    assert download.song_name_artist(p) == ("千百度", "许嵩")

    p2 = tmp_path / "02 - 好想好想 - 赵薇.mp3"
    p2.write_bytes(b"")
    assert download.song_name_artist(p2) == ("好想好想", "赵薇")

    p3 = tmp_path / "只有歌名.mp3"
    p3.write_bytes(b"")
    assert download.song_name_artist(p3) == ("只有歌名", "")


def test_the_id_is_written_into_the_lrc_and_read_back(tmp_path, monkeypatch):
    """下载时把网易云 ID 记在 LRC 头里，以后没有标签也能认出来。"""
    audio = tmp_path / "h.mp3"
    audio.write_bytes(b"")
    monkeypatch.setattr(download.tx, "netease_id_of", lambda p: 167732)
    monkeypatch.setattr(download.sources, "fetch", lambda *a, **k: sources.WordLyric(
        source="网易云", tag="NeteaseCloud",
        lines=[sources.WordLine(1.0, "第一句", [("第", 1.0), ("一", 1.2)], 1.6)],
        timed=True, song_id=167732))
    download.download_one(audio)
    body = (tmp_path / "h.lrc").read_text(encoding="utf-8")
    assert "[kiralrc:id=167732]" in body
    # 再把标签与下载记录都断掉，仍然能从 LRC 头认出来
    monkeypatch.setattr(download.tx, "netease_id_from_file", lambda p: None)
    monkeypatch.setattr(download.tx, "netease_id_from_downloads", lambda p: None)
    assert download.tx.netease_id_from_lrc(audio) == 167732
