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


def test_netease_match_fetches_lyrics_and_skips_empty_candidate():
    pool = [
        {"id": 10, "name": "晴天", "artist": "周杰伦", "duration": 269},
        {"id": 11, "name": "晴天", "artist": "周杰伦", "duration": 270},
    ]
    sample_lrc = "\n".join(f"[00:0{i}.000]第{i}句歌词" for i in range(1, 10))
    got = tx.netease_match(
        "周杰伦 晴天",
        duration=269,
        name="晴天",
        artist="周杰伦",
        search=lambda _: pool,
        fetch_lyric=lambda sid: ("", "") if sid == 10 else (sample_lrc, ""),
    )
    assert got is not None
    assert got["id"] == 11
    assert got["source"] == "网易云"
    assert got["lrc"] == sample_lrc
    assert got["lines"] == 9


def test_artist_title_from_filename_and_score_without_artist():
    assert tx.artist_title_from_filename("A路人 - 如何让白鼠爱上学习(英文翻唱)") == ("A路人", "如何让白鼠爱上学习(英文翻唱)")
    assert tx.artist_title_from_filename("02 - 好想好想") == ("", "好想好想")
    # 缺歌手时权重归一化，精准歌名+相近时长应拿到高分
    s = tx.score_song({"name": "晴天", "artist": "周杰伦", "duration": 269}, name="晴天", artist="", duration=270)
    assert s > 0.95


def test_filter_candidates_rejects_unrelated_or_duration_mismatch():
    pool = [
        {"id": "1", "name": "我的阿勒泰 05 汉族孩子们", "artist": "喜马拉雅", "duration": 620},
        {"id": "2", "name": "秦城的汉子2021", "artist": "新蛤社", "duration": 620},
        {"id": "3", "name": "秦城的汉子2021", "artist": "新蛤社", "duration": 277},
    ]
    got = tx._filter_candidates(pool, "秦城的汉子2021 新蛤社", duration=277,
                                name="秦城的汉子2021", artist="新蛤社")
    assert [c["id"] for c in got] == ["3"]


def test_cli_accepts_asr_fallback_flag():
    import subprocess, sys
    r = subprocess.run([sys.executable, "-m", "kiralrc", "align", "--help"], capture_output=True, text=True)
    assert r.returncode == 0
    assert "--asr-fallback" in r.stdout
    assert "--netease-id" in r.stdout


def test_align_worker_continues_after_failed_request(monkeypatch, capsys):
    import io
    import sys
    from kiralrc import cli

    def align(args):
        if args.audio == "bad.flac":
            raise RuntimeError("alignment failed")
        assert args.lang == "ja"
        print(f"[ok] {args.audio}")
        return 0

    monkeypatch.setattr(cli, "cmd_align", align)
    monkeypatch.setattr(sys, "stdin", io.StringIO(
        '["bad.flac"]\n["good.flac", "--lang", "ja"]\n'
    ))
    assert cli.main(["--align-worker"]) == 0
    captured = capsys.readouterr()
    assert captured.out.split() == [
        "[kiralrc-worker-result]", "1", "[ok]", "good.flac", "[kiralrc-worker-result]", "0",
    ]
    assert "RuntimeError: alignment failed" in captured.err


def test_multilingual_char_to_latin():
    from kiralrc import jp
    assert jp._char_to_latin("사") == ("sa", True)
    assert jp._char_to_latin("랑") == ("rang", True)
    assert jp._char_to_latin("해") == ("hae", True)
    assert jp._char_to_latin("é") == ("e", False)
    assert jp._char_to_latin("ü") == ("u", False)
    assert jp._char_to_latin("ß") == ("ss", False)


def test_segments_to_fallback_lines_filters_hallucinations_and_splits_on_gap():
    from kiralrc import asr

    segs = [
        {"start": 0.0, "end": 2.0, "text": "ご視聴ありがとうございました"},
        {"start": 2.5, "end": 4.0, "text": "啊啊啊啊啊啊啊"},
        {"start": 5.0, "end": 9.0, "text": "hello world how are you"},
        {"start": 10.0, "end": 12.0, "text": "Subtitles by Amara.org"},
    ]
    words = [
        {"w": "hello", "s": 5.0, "e": 5.4},
        {"w": "world", "s": 5.45, "e": 5.9},
        {"w": "how", "s": 6.6, "e": 6.9},   # gap = 0.70s > 0.45s -> split
        {"w": "are", "s": 6.95, "e": 7.2},
        {"w": "you", "s": 7.25, "e": 7.6},
    ]
    got = asr.segments_to_fallback_lines(words, segs)
    assert got == [(5.0, "hello world"), (6.6, "how are you")]


def test_ttml_to_lrc_parses_word_spans_and_translations():
    xml = """<?xml version="1.0" encoding="UTF-8"?>
    <tt xmlns="http://www.w3.org/ns/ttml" xmlns:ttm="http://www.w3.org/ns/ttml#metadata">
      <body>
        <div>
          <p begin="00:07.734" end="00:08.525">
            <span begin="00:07.734" end="00:07.900">Hi </span>
            <span begin="00:07.900" end="00:08.100">you </span>
            <span begin="00:08.100" end="00:08.425">guys!</span>
            <span ttm:role="x-translation">大家好</span>
          </p>
          <p begin="1:02.50" end="1:05.00">
            <span begin="1:02.50" end="1:03.20">君は</span>
            <span begin="1:03.20" end="1:05.00">笑う</span>
            <span ttm:role="x-roman">kimi wa warau</span>
            <span ttm:role="x-translation">你在微笑</span>
          </p>
        </div>
      </body>
    </tt>"""
    lrc, tlyric = tx.ttml_to_lrc(xml)
    assert lrc == "[00:07.734]Hi you guys!\n[01:02.500]君は笑う"
    assert tlyric == "[00:07.734]大家好\n[01:02.500]你在微笑"


def test_apple_music_match_skips_when_token_empty(monkeypatch):
    monkeypatch.setenv("KIRALRC_APPLE_MEDIA_USER_TOKEN", "")
    called = []

    def boom(*args, **kwargs):
        called.append(True)
        raise AssertionError("should not perform network request when token is empty")

    monkeypatch.setattr(tx.urllib.request, "urlopen", boom)
    assert tx.apple_music_match("晴天", duration=269, name="晴天", artist="周杰伦") is None
    assert not called


def test_multi_sources_reject_low_similarity_matches(monkeypatch):
    class _Resp:
        def __init__(self, payload: bytes):
            self._payload = payload

        def read(self):
            return self._payload

        def __enter__(self):
            return self

        def __exit__(self, *a):
            return False

    sample_lrc = "\n".join(f"[00:0{i}.000]line {i}" for i in range(1, 10))

    # 1) QQ 音乐返回时长碰巧接近但歌名完全无关的曲目 -> 必须拒绝
    qq_payload = json.dumps({
        "req_1": {"data": {"body": {"song": {"list": [
            {"mid": "bad1", "name": "完全无关的歌", "singer": [{"name": "路人甲"}], "interval": 240}
        ]}}}}
    }).encode("utf-8")
    monkeypatch.setattr(tx.urllib.request, "urlopen", lambda req, timeout=12: _Resp(qq_payload))
    assert tx.qq_match("晴天", duration=240, name="晴天", artist="周杰伦") is None

    # 2) 酷狗返回时长接近但歌名无关 -> 必须拒绝
    kg_payload = json.dumps({
        "data": {"info": [{"hash": "abc", "songname": "毫不相干", "singername": "某人", "duration": 240}]}
    }).encode("utf-8")
    monkeypatch.setattr(tx.urllib.request, "urlopen", lambda req, timeout=12: _Resp(kg_payload))
    assert tx.kugou_match("晴天", duration=240, name="晴天", artist="周杰伦") is None

    # 3) LRCLIB 返回时长接近但歌名无关 -> 必须拒绝
    lr_payload = json.dumps([
        {"id": 99, "trackName": "Totally Different Song", "artistName": "Nobody", "duration": 240, "syncedLyrics": sample_lrc}
    ]).encode("utf-8")
    monkeypatch.setattr(tx.urllib.request, "urlopen", lambda req, timeout=12: _Resp(lr_payload))
    assert tx.lrclib_match("晴天", duration=240, name="晴天", artist="周杰伦") is None


def test_best_match_dynamic_order_and_amll_reuses_netease_id(monkeypatch):
    # A) 中文歌曲顺序：网易云 → AMLL → QQ音乐 → 酷狗音乐 → Apple Music → LRCLIB
    zh_calls: list[str] = []
    cand_calls = 0

    def fake_zh_cands(*a, **kw):
        nonlocal cand_calls
        cand_calls += 1
        return [{"id": 123456, "name": "晴天", "artist": "周杰伦", "duration": 269, "score": 0.95}]

    monkeypatch.setattr(tx, "netease_candidates", fake_zh_cands)
    monkeypatch.setattr(tx, "netease_lyric", lambda sid: zh_calls.append(f"netease:{sid}") or ("", ""))
    monkeypatch.setattr(tx.time, "sleep", lambda *a, **kw: None)
    monkeypatch.setattr(tx, "amll_match", lambda sid, **kw: zh_calls.append(f"amll:{sid}") or None)
    monkeypatch.setattr(tx, "qq_match", lambda *a, **kw: zh_calls.append("qq") or None)
    monkeypatch.setattr(tx, "kugou_match", lambda *a, **kw: zh_calls.append("kugou") or None)
    monkeypatch.setattr(tx, "apple_music_match", lambda *a, **kw: zh_calls.append("apple") or None)
    monkeypatch.setattr(tx, "lrclib_match", lambda *a, **kw: zh_calls.append("lrclib") or None)
    tx.best_match("晴天", duration=269, name="晴天", artist="周杰伦")
    assert cand_calls == 1, "netease_candidates 只能搜索一次并复用于 netease_match 与 AMLL"
    assert zh_calls == ["netease:123456", "amll:123456", "qq", "kugou", "apple", "lrclib"]

    # B) 日/英文歌曲顺序：网易云 → AMLL → Apple Music → QQ音乐 → LRCLIB → 酷狗音乐
    ja_calls: list[str] = []
    cand_calls = 0

    def fake_ja_cands(*a, **kw):
        nonlocal cand_calls
        cand_calls += 1
        return [{"id": 654321, "name": "晴る", "artist": "ヨルシカ", "duration": 270, "score": 0.92}]

    monkeypatch.setattr(tx, "netease_candidates", fake_ja_cands)
    monkeypatch.setattr(tx, "netease_lyric", lambda sid: ja_calls.append(f"netease:{sid}") or ("", ""))
    monkeypatch.setattr(tx, "amll_match", lambda sid, **kw: ja_calls.append(f"amll:{sid}") or None)
    monkeypatch.setattr(tx, "apple_music_match", lambda *a, **kw: ja_calls.append("apple") or None)
    monkeypatch.setattr(tx, "qq_match", lambda *a, **kw: ja_calls.append("qq") or None)
    monkeypatch.setattr(tx, "lrclib_match", lambda *a, **kw: ja_calls.append("lrclib") or None)
    monkeypatch.setattr(tx, "kugou_match", lambda *a, **kw: ja_calls.append("kugou") or None)
    tx.best_match("ヨルシカ 晴る", duration=270, name="晴る", artist="ヨルシカ")
    assert cand_calls == 1, "netease_candidates 只能搜索一次并复用于 netease_match 与 AMLL"
    assert ja_calls == ["netease:654321", "amll:654321", "apple", "qq", "lrclib", "kugou"]


def test_partial_translation_detection_and_merge():
    from kiralrc import pipeline as pl

    local_lrc = "[00:01.00]Line one\n[00:01.00]第一句\n[00:02.00]Line two\n"
    local_map = pl._build_trans_map_from_text(local_lrc)
    lines = ["Line one", "Line two"]
    assert pl._has_missing_trans(lines, local_map) is True
    assert pl._missing_trans_lines(lines, local_map) == ["Line two"]

    fetched_lrc = (
        "[00:01.00]Line one\n[00:01.00]远端第一句\n"
        "[00:02.00]Line two\n[00:02.00]远端第二句\n"
    )
    fetched_map = pl._build_trans_map_from_text(fetched_lrc)
    merged = pl._merge_trans_maps(local_map, fetched_map)
    assert merged[pl._norm_lyric_key("Line one")] == [["第一句"]]
    assert merged[pl._norm_lyric_key("Line two")] == [["远端第二句"]]
    assert pl._has_missing_trans(lines, merged) is False


def test_fetch_missing_translations_fallback_on_mismatch_timeout_and_explicit_id(monkeypatch, tmp_path):
    from kiralrc import pipeline as pl

    dummy = tmp_path / "Artist - Song.mp3"
    dummy.write_bytes(b"")
    monkeypatch.setattr(tx, "netease_id_from_file", lambda p: 99999)

    # 1) 显式 netease_id 优先于音频标签 ID
    seen_ids = []

    def fake_by_id(sid):
        seen_ids.append(sid)
        return {"lrc": "[00:01.00]Hello world\n", "tlyric": "[00:01.00]你好世界\n"}

    monkeypatch.setattr(tx, "netease_by_id", fake_by_id)
    got = pl._fetch_missing_translations(
        dummy, 200.0, netease_id=12345, target_lines=["Hello world"],
    )
    assert seen_ids == [12345]
    assert got == {pl._norm_lyric_key("Hello world"): [["你好世界"]]}

    # 2) 网易云返回完全不匹配的译文时，继续回退到 QQ 音乐
    monkeypatch.setattr(tx, "netease_id_from_file", lambda p: None)
    monkeypatch.setattr(tx, "netease_match", lambda *a, **kw: {
        "lrc": "[00:01.00]Unrelated lyric\n",
        "tlyric": "[00:01.00]无关译文\n",
    })
    qq_called = []

    def fake_qq(*a, **kw):
        qq_called.append(True)
        return {
            "lrc": "[00:01.00]Hello world\n",
            "tlyric": "[00:01.00]来自QQ的译文\n",
        }

    monkeypatch.setattr(tx, "qq_match", fake_qq)
    got2 = pl._fetch_missing_translations(dummy, 200.0, target_lines=["Hello world"])
    assert qq_called == [True]
    assert got2 == {pl._norm_lyric_key("Hello world"): [["来自QQ的译文"]]}

    # 3) 网易云抛出超时异常时，不中断，继续回退到 QQ 音乐
    def boom(*a, **kw):
        raise TimeoutError("netease timeout")

    monkeypatch.setattr(tx, "netease_match", boom)
    qq_called.clear()
    got3 = pl._fetch_missing_translations(dummy, 200.0, target_lines=["Hello world"])
    assert qq_called == [True]
    assert got3 == {pl._norm_lyric_key("Hello world"): [["来自QQ的译文"]]}


def test_attach_translations_skips_dropped_duplicate_occurrence():
    from kiralrc import format as fmt
    from kiralrc import pipeline as pl

    ref_body = (
        "[00:01.00]Repeat line\n[00:01.00]第一次译文\n"
        "[00:05.00]Middle\n[00:05.00]中间\n"
        "[00:10.00]Repeat line\n[00:10.00]第二次译文\n"
    )
    orig_lines = ["Repeat line", "Middle", "Repeat line"]
    tmap = pl._build_trans_map_from_text(ref_body)
    aligned_lrc = fmt.Lrc(lines=[
        fmt.Line(5.0, "Middle"),
        fmt.Line(10.0, "Repeat line"),
    ])
    added = pl._attach_translations(
        aligned_lrc, tmap, skipped=[{"index": 1, "text": "Repeat line"}], orig_lines=orig_lines,
    )
    assert added == 2
    texts = [ln.text for ln in aligned_lrc.lines]
    assert texts == ["Middle", "中间", "Repeat line", "第二次译文"]


def test_lines_with_translations_keeps_close_same_language_lines():
    # 相隔 40ms 的两句英文原词不应把第二句吞成译文
    lrc_en = "[00:10.000]First English line\n[00:10.040]Second English line\n"
    orig_en, trans_en = tx.lines_with_translations(lrc_en)
    assert len(orig_en) == 2
    assert [t for _, t in orig_en] == ["First English line", "Second English line"]
    assert trans_en == [[], []]

    # 真正的双语（日文 + 中文，相隔 40ms）仍正常拆分
    lrc_bi = "[00:10.000]君のことが好き\n[00:10.040]我喜欢你\n"
    orig_bi, trans_bi = tx.lines_with_translations(lrc_bi)
    assert len(orig_bi) == 1
    assert orig_bi[0][1] == "君のことが好き"
    assert trans_bi == [["我喜欢你"]]
