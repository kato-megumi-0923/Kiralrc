"""下载源：yrc / lrc 解析与写成 LRC。"""

from kiralrc import format as fmt
from kiralrc import sources

YRC = """[0,1000](0,500,0)初(500,500,0)め(1000,400,0)て(1400,600,0)
[2000,1200](2000,300,0)の(2300,300,0)恋(2600,600,0)
"""

LRC = """[ti:测试]
[00:01.50]第一句
[00:05.00]第二句
[00:09.00]第三句
"""

TLRC = """[00:01.52]第一句的译文
[00:05.30]第二句的译文
"""


def test_parse_yrc_keeps_word_times():
    lines = sources.parse_yrc(YRC)
    assert [l.text for l in lines] == ["初めて", "の恋"]
    assert lines[0].start == 0 and lines[0].end == 1.0
    assert lines[0].chars == [("初", 0.0), ("め", 0.5), ("て", 1.0)]
    assert lines[1].chars[0][1] == 2.0


def test_parse_lrc_skips_meta():
    got = sources.parse_lrc(LRC)
    assert got == [(1.5, "第一句"), (5.0, "第二句"), (9.0, "第三句")]


def test_translation_goes_to_the_nearest_line():
    lines = sources.parse_yrc(YRC)
    sources.attach_translation(lines, "[00:00.10]第一句的译文\n[00:02.30]第二句的译文")
    assert lines[0].translation == "第一句的译文"
    assert lines[1].translation == "第二句的译文"


def test_to_lrc_writes_by_and_translation():
    lyric = sources.WordLyric(
        source="网易云", tag=sources.TAGS["netease"],
        lines=[
            sources.WordLine(1.0, "初めて", [("初", 1.0), ("め", 1.2), ("て", 1.4)], 1.6, "第一次"),
            sources.WordLine(3.0, "の恋", [("の", 3.0), ("恋", 3.1)], 3.4),
        ],
        timed=True,
    )
    text = sources.to_lrc(lyric, ti="歌", ar="人").render()
    assert "[by:NeteaseCloud]" in text
    assert "[00:01.000]<00:01.000>初<00:01.200>め<00:01.400>て<00:01.600>" in text
    # 译文单独成行，跟原行同时间戳
    assert "\n[00:01.000]第一次\n" in text
    assert text.count("[b") == 0 or "[by:" in text


def test_timed_lines_round_trip_through_the_format():
    lyric = sources.WordLyric(
        source="网易云", tag=sources.TAGS["netease"],
        lines=[sources.WordLine(1.0, "初めて", [("初", 1.0), ("め", 1.2), ("て", 1.4)], 1.6)],
        timed=True,
    )
    text = sources.to_lrc(lyric).render()
    import tempfile, os
    p = os.path.join(tempfile.mkdtemp(), "x.lrc")
    with open(p, "w", encoding="utf-8", newline="") as f:
        f.write(text)
    back = fmt.parse(p)
    assert len(back.lines) == 1 and back.lines[0].enhanced


def test_a_placeholder_is_not_lyrics():
    assert sources.PLACEHOLDER.search("纯音乐，请欣赏")
    assert sources.PLACEHOLDER.search("此歌曲为没有填词的纯音乐，请您欣赏")
    assert not sources.PLACEHOLDER.search("好想好想和你在一起")


def test_a_line_only_source_does_not_stop_the_search(monkeypatch):
    """网易云只给逐行时，QQ 的逐字还有机会。"""
    calls = []
    monkeypatch.setattr(sources, "from_netease", lambda sid, timeout=20: calls.append("netease") or
                        sources.WordLyric(source="网易云", tag="NeteaseCloud",
                                          lines=[sources.WordLine(1.0, "整行", [], 2.0)], timed=False))
    monkeypatch.setattr(sources, "from_qq", lambda name, artist="", duration=None: calls.append("qq") or
                        sources.WordLyric(source="QQ音乐", tag="QQMusic",
                                          lines=[sources.WordLine(1.0, "逐字", [("逐", 1.0), ("字", 1.2)], 1.4)],
                                          timed=True))
    hit = sources.fetch(1, name="歌", pause=0)
    assert hit.source == "QQ音乐" and hit.timed
    assert calls == ["netease", "qq"]


def test_a_line_only_hit_is_kept_when_nothing_better(monkeypatch):
    monkeypatch.setattr(sources, "from_netease", lambda sid, timeout=20:
                        sources.WordLyric(source="网易云", tag="NeteaseCloud",
                                          lines=[sources.WordLine(1.0, "整行", [], 2.0)], timed=False))
    monkeypatch.setattr(sources, "from_qq", lambda *a, **k: None)
    monkeypatch.setattr(sources, "from_amll", lambda *a, **k: None)
    monkeypatch.setattr(sources, "from_kugou", lambda *a, **k: None)
    hit = sources.fetch(1, name="歌", pause=0)
    assert hit and not hit.timed


def test_a_line_only_source_does_not_stop_the_search(monkeypatch):
    """网易云只给逐行时，QQ 的逐字还有机会。"""
    calls = []
    monkeypatch.setattr(sources, "from_netease", lambda sid, timeout=20: calls.append("netease") or
                        sources.WordLyric(source="网易云", tag="NeteaseCloud",
                                          lines=[sources.WordLine(1.0, "整行", [], 2.0)], timed=False))
    monkeypatch.setattr(sources, "from_qq", lambda name, artist="", duration=None: calls.append("qq") or
                        sources.WordLyric(source="QQ音乐", tag="QQMusic",
                                          lines=[sources.WordLine(1.0, "逐字", [("逐", 1.0), ("字", 1.2)], 1.4)],
                                          timed=True))
    hit = sources.fetch(1, name="歌", pause=0)
    assert hit.source == "QQ音乐" and hit.timed
    assert calls == ["netease", "qq"]


def test_a_line_only_hit_is_kept_when_nothing_better(monkeypatch):
    monkeypatch.setattr(sources, "from_netease", lambda sid, timeout=20:
                        sources.WordLyric(source="网易云", tag="NeteaseCloud",
                                          lines=[sources.WordLine(1.0, "整行", [], 2.0)], timed=False))
    monkeypatch.setattr(sources, "from_qq", lambda *a, **k: None)
    monkeypatch.setattr(sources, "from_amll", lambda *a, **k: None)
    monkeypatch.setattr(sources, "from_kugou", lambda *a, **k: None)
    hit = sources.fetch(1, name="歌", pause=0)
    assert hit and not hit.timed
