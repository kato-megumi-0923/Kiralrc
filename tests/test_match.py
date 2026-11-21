"""取词：ID 解析、歌名/歌手相似度、候选排序。"""

import json

from kiralrc import text as tx


def test_parse_song_id():
    cases = {
        30967650: 30967650,
        "30967650": 30967650,
        "id=30967650": 30967650,
        "https://music.163.com/song?id=30967650": 30967650,
        "https://music.163.com/#/song?id=30967650&uct2=U2FsdGVkX18J0q6L": 30967650,
        "https://music.163.com/song/30967650": 30967650,
        "": None,
        None: None,
        "随便写点": None,
        "0": None,
    }
    for raw, want in cases.items():
        assert tx.parse_song_id(raw) == want, raw


def test_titles_alike():
    # 同一个歌名跨海峡的两种写法
    assert tx._titles_alike("恋は渾沌の隷也", "恋は混沌の隷也") > 0.85
    # 括号里的副标题不算数
    assert tx._titles_alike("ルージュ (口红)", "ルージュ") == 1.0
    # 不一样的歌
    assert tx._titles_alike("晴天", "稻香") < 0.4


def test_artists_alike():
    # 简繁、译名差异也要有分
    assert tx._artists_alike("後ろから這いより隊G", "后ろから这いより队G") > 0.6
    assert tx._artists_alike("中岛美雪", "中島みゆき") < 0.3


def test_candidates_rank_the_same_song_first():
    """搜索词只给出同名的别人版本时，歌名+歌手+时长把对的那条排前面。"""
    pool = [
        {"id": 1, "name": "我不能忍 (456)", "artist": "Rings1909", "duration": 95},
        {"id": 2, "name": "我不能忍", "artist": "大酒神", "duration": 107},
        {"id": 3, "name": "我不能忍 (Remix)", "artist": "Rings1909", "duration": 95},
    ]

    def fake_search(keyword):
        return pool if "我不能忍" in keyword else []

    got = tx.netease_candidates("我不能忍", duration=107, name="我不能忍",
                                artist="大酒神", search=fake_search)
    assert got[0]["id"] == 2
    assert got[0]["score"] > got[1]["score"]


def test_candidates_leave_a_different_song_out():
    pool = [{"id": 9, "name": "晴天", "artist": "周杰伦", "duration": 269}]

    def fake_search(keyword):
        return pool

    got = tx.netease_candidates("稻香", duration=223, name="稻香", artist="周杰伦",
                                search=fake_search)
    assert got and got[0]["score"] < 0.55, got


def test_queries_try_the_specific_one_first():
    qs = tx._queries("Answer", artist="冬馬かずさ", name="Answer")
    assert qs[0] == "冬馬かずさ Answer"
    assert "Answer" in qs


def test_netease_id_from_file_reads_nothing_from_a_stranger(tmp_path):
    p = tmp_path / "no-tags.mp3"
    p.write_bytes(b"\xff\xfb" + b"\x00" * 512)
    assert tx.netease_id_from_file(p) is None
