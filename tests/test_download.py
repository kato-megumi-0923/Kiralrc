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


def test_official_upgrade_preserves_local_translations(tmp_path, monkeypatch):
    """官方逐字覆盖已有双语歌词时，若新下载的缺译文，应无损继承本地已有中文译文。"""
    from kiralrc import pipeline as pl

    audio = tmp_path / "song.mp3"
    audio.write_bytes(b"")
    _lrc(tmp_path, "song.lrc",
         "[00:01.000]君のことが好き\n[00:01.000]我喜欢你\n")

    monkeypatch.setattr(download.tx, "netease_id_of", lambda p: 1001)
    monkeypatch.setattr(download.sources, "fetch", lambda *a, **k: sources.WordLyric(
        source="QQ音乐", tag="QQMusic",
        lines=[sources.WordLine(1.0, "君のことが好き", [("君", 1.0), ("の", 1.2), ("こ", 1.4), ("と", 1.6), ("が", 1.8), ("好", 2.0), ("き", 2.2)], 2.5)],
        timed=True))
    # 本地已把全部译文补齐，不应再触发远端补全
    remote_called = []
    monkeypatch.setattr(pl, "_fetch_missing_translations", lambda *a, **k: remote_called.append(1) or {})

    res = download.download_one(audio)
    assert res.status == "upgraded"
    assert not remote_called
    body = (tmp_path / "song.lrc").read_text(encoding="utf-8")
    assert "<00:01.000>君<00:01.200>の" in body
    assert "[00:01.000]我喜欢你" in body


def test_foreign_download_backfills_missing_translation_cross_source(tmp_path, monkeypatch):
    """外语歌下到无译文官方逐字时，跨源补全中文译文，且保留原逐字时间戳。"""
    from kiralrc import pipeline as pl

    audio = tmp_path / "jp.mp3"
    audio.write_bytes(b"")
    monkeypatch.setattr(download.tx, "netease_id_of", lambda p: 2002)
    monkeypatch.setattr(download.sources, "fetch", lambda *a, **k: sources.WordLyric(
        source="酷狗", tag="Kugou",
        lines=[sources.WordLine(1.0, "また会えるよね", [("ま", 1.0), ("た", 1.2), ("会", 1.4), ("え", 1.6), ("る", 1.8), ("よ", 2.0), ("ね", 2.2)], 2.5)],
        timed=True))
    monkeypatch.setattr(
        pl, "_fetch_missing_translations",
        lambda *a, **k: {pl._norm_lyric_key("また会えるよね"): [["我们还会再见的吧"]]},
    )

    res = download.download_one(audio)
    assert res.status == "downloaded"
    assert res.trans_filled == 1
    assert res.missing_trans is False
    body = (tmp_path / "jp.lrc").read_text(encoding="utf-8")
    assert "<00:01.000>ま<00:01.200>た" in body
    assert "[00:01.000]我们还会再见的吧" in body


def test_existing_official_foreign_lrc_backfills_without_touching_word_times(tmp_path, monkeypatch):
    """已有官方逐字但缺译文的外语歌，下载流程只补译文、原样保留全部 <mm:ss.xxx> 逐字时间戳。"""
    from kiralrc import pipeline as pl

    audio = tmp_path / "exist.mp3"
    audio.write_bytes(b"")
    _lrc(
        tmp_path, "exist.lrc",
        "[by:NeteaseCloud]\n"
        "[00:01.234]<00:01.234>君<00:01.567>の<00:01.890>声<00:02.345>\n"
        "[00:05.000]<00:05.000>届<00:05.300>い<00:05.600>た<00:06.000>\n"
        "[00:05.000]已经传达到了\n",
    )
    fetch_called = []
    monkeypatch.setattr(download.sources, "fetch", lambda *a, **k: fetch_called.append(1))
    monkeypatch.setattr(download.tx, "netease_id_of", lambda p: 3003)
    monkeypatch.setattr(
        pl, "_fetch_missing_translations",
        lambda *a, **k: {
            pl._norm_lyric_key("君の声"): [["你的声音"]],
            pl._norm_lyric_key("届いた"): [["远端第二句"]],
        },
    )

    res = download.download_one(audio)
    assert res.status == "upgraded"
    assert res.trans_filled == 1
    assert res.missing_trans is False
    assert not fetch_called
    body = (tmp_path / "exist.lrc").read_text(encoding="utf-8")
    # 原逐字时间戳一个字符都不能变
    assert "[00:01.234]<00:01.234>君<00:01.567>の<00:01.890>声<00:02.345>" in body
    assert "[00:01.234]你的声音" in body
    # 原已有译文不被远端覆盖
    assert "[00:05.000]已经传达到了" in body
    assert "远端第二句" not in body


def test_missing_trans_flagged_in_counts_and_report(tmp_path, monkeypatch):
    """外语官方逐字全网无译文时，保留原文件并在统计报告里标出缺译文。"""
    from kiralrc import pipeline as pl

    audio = tmp_path / "rare.mp3"
    audio.write_bytes(b"")
    _lrc(
        tmp_path, "rare.lrc",
        "[by:NeteaseCloud]\n"
        "[00:01.000]<00:01.000>ず<00:01.200>っ<00:01.400>と<00:01.600>見<00:01.800>て<00:02.000>\n",
    )
    monkeypatch.setattr(download.tx, "netease_id_of", lambda p: None)
    monkeypatch.setattr(pl, "_fetch_missing_translations", lambda *a, **k: {})

    counts = download.run(tmp_path, jobs=1)
    assert counts.kept == 1
    assert counts.missing_trans == 1
    assert "缺译文 1" in download.report(counts)


def test_vocalization_lines_and_high_coverage_do_not_refetch(tmp_path, monkeypatch):
    """仅有哼唱/拟声词缺译，或实质行译文覆盖率 ≥ 85% 时，不重复联网补译。"""
    from kiralrc import pipeline as pl

    # 1) 仅有 La la la / Yeah~ / Oh 缺译
    audio1 = tmp_path / "voc.mp3"
    audio1.write_bytes(b"")
    _lrc(
        tmp_path, "voc.lrc",
        "[by:NeteaseCloud]\n"
        "[00:01.000]<00:01.000>Hello <00:01.500>world<00:02.000>\n"
        "[00:01.000]你好世界\n"
        "[00:03.000]<00:03.000>La <00:03.200>la <00:03.400>la<00:03.800>\n"
        "[00:05.000]<00:05.000>Yeah~<00:05.500>\n"
        "[00:07.000]<00:07.000>Oh<00:07.300>\n",
    )
    called = []
    monkeypatch.setattr(pl, "_fetch_missing_translations", lambda *a, **k: called.append(1) or {})
    res1 = download.download_one(audio1)
    assert res1.status == "kept"
    assert res1.missing_trans is False
    assert not called

    # 2) 10 句实质外语歌词已有 9 句译文（90% ≥ 85%）→ 视为完整，不重复联网
    audio2 = tmp_path / "cov.mp3"
    audio2.write_bytes(b"")
    lines = ["[by:NeteaseCloud]"]
    for i in range(10):
        t0, t1 = f"00:{i+1:02d}.000", f"00:{i+1:02d}.500"
        lines.append(f"[{t0}]<{t0}>君<{t1}>の声{i}<00:{i+1:02d}.900>")
        if i < 9:
            lines.append(f"[{t0}]你的声音{i}")
    _lrc(tmp_path, "cov.lrc", "\n".join(lines) + "\n")
    res2 = download.download_one(audio2)
    assert res2.status == "kept"
    assert res2.missing_trans is False
    assert not called


def test_backfill_respects_netease_cooldown_and_trips_on_ratelimit(tmp_path, monkeypatch):
    """补译文遇网易云 RateLimited 触发熔断，冷却中直接跳过网易云只问 QQ。"""
    from kiralrc import netauth, pipeline as pl, text as tx
    from kiralrc.ratelimit import RateLimited

    audio = tmp_path / "Artist - Song.mp3"
    audio.write_bytes(b"")
    monkeypatch.setattr(tx, "netease_id_from_file", lambda p: None)

    tripped = []
    monkeypatch.setattr(sources, "_trip_netease", lambda *a, **k: tripped.append("sources"))
    monkeypatch.setattr(netauth, "record_rate_limit", lambda *a, **k: tripped.append("netauth"))
    monkeypatch.setattr(sources, "netease_available", lambda: True)
    monkeypatch.setattr(netauth, "cooling_for", lambda: 0.0)

    def raise_rl(*a, **k):
        raise RateLimited("HTTP 405")

    monkeypatch.setattr(tx, "netease_by_id", raise_rl)
    monkeypatch.setattr(tx, "netease_match", raise_rl)
    monkeypatch.setattr(tx, "qq_match", lambda *a, **k: {
        "lrc": "[00:01.00]Hello world\n",
        "tlyric": "[00:01.00]来自QQ的译文\n",
    })

    got = pl._fetch_missing_translations(audio, 200.0, netease_id=123, target_lines=["Hello world"])
    assert "sources" in tripped and "netauth" in tripped
    assert got == {pl._norm_lyric_key("Hello world"): [["来自QQ的译文"]]}

    # 冷却中再次调用时，网易云函数完全不被触发，直接走 QQ
    netease_calls = []
    monkeypatch.setattr(sources, "netease_available", lambda: False)
    monkeypatch.setattr(tx, "netease_by_id", lambda *a, **k: netease_calls.append("id"))
    monkeypatch.setattr(tx, "netease_match", lambda *a, **k: netease_calls.append("search"))
    got2 = pl._fetch_missing_translations(audio, 200.0, netease_id=123, target_lines=["Hello world"])
    assert not netease_calls
    assert got2 == {pl._norm_lyric_key("Hello world"): [["来自QQ的译文"]]}


def test_inplace_backfill_preserves_metas_and_heart_translations(tmp_path, monkeypatch):
    """原地写回时保留 [by:] / [kiralrc:aligned] / [kiralrc:id=...]，且不重复贴已有 ❤️ 译文行。"""
    from kiralrc import pipeline as pl

    audio = tmp_path / "heart.mp3"
    audio.write_bytes(b"")
    _lrc(
        tmp_path, "heart.lrc",
        "[ti:测试曲]\n"
        "[by:SOFA 歌声对齐]\n"
        "[kiralrc:aligned]\n"
        "[kiralrc:id=556677]\n"
        "[00:01.000]<00:01.000>君<00:01.300>の<00:01.600>声<00:02.000>\n"
        "[00:05.000]<00:05.000>届<00:05.300>い<00:05.600>た<00:06.000>\n"
        "[00:05.000]❤️已经传达到了❤️\n",
    )
    monkeypatch.setattr(download.tx, "netease_id_of", lambda p: 556677)
    monkeypatch.setattr(
        pl, "_fetch_missing_translations",
        lambda *a, **k: {
            pl._norm_lyric_key("君の声"): [["你的声音"]],
            pl._norm_lyric_key("届いた"): [["传达到了"]],
        },
    )

    added, still_missing = download._backfill_existing_lrc(audio)
    assert added == 1
    assert still_missing is False
    body = (tmp_path / "heart.lrc").read_text(encoding="utf-8")
    assert "[by:SOFA 歌声对齐]" in body
    assert "[kiralrc:aligned]" in body
    assert "[kiralrc:id=556677]" in body
    assert "[00:01.000]你的声音" in body
    # 已有 ❤️ 译文原样保留且仅出现一次，不会被远端译文重复贴成两条
    assert body.count("❤️已经传达到了❤️") == 1
    assert "传达到了\n" not in body.replace("❤️已经传达到了❤️", "")
