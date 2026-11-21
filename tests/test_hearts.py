"""双语加 ❤️ 的测试。"""
from kiralrc import hearts

BILINGUAL = "\r\n".join([
    "[ti:t]",
    "[00:01.000]<00:01.000>あ<00:01.400>い<00:02.000>",
    "[00:01.000]<00:01.000>爱意<00:02.000>",
    "",
])


def test_only_translation_lines(tmp_path):
    p = tmp_path / "a.lrc"
    p.write_bytes(BILINGUAL.encode("utf-8"))
    assert hearts.wrap(p) == 1
    out = p.read_bytes().decode("utf-8").split("\r\n")
    assert out[1].startswith("[00:01.000]<00:01.000>あ")      # 逐字行不动
    assert "❤️爱意❤️" in out[2] and out[2].endswith("<00:02.000>")  # 译文加心且保留行尾
    assert p.read_bytes().count(b"\r\n") == 3                  # 换行风格不变


def test_idempotent(tmp_path):
    p = tmp_path / "a.lrc"
    p.write_bytes(BILINGUAL.encode("utf-8"))
    hearts.wrap(p)
    assert hearts.wrap(p) == 0        # 已经是 ❤️ 包着的，不再加


def test_skip_credits(tmp_path):
    p = tmp_path / "a.lrc"
    p.write_bytes("[00:00.000]<00:00.000>QQ音乐享有本翻译作品的著作权<00:01.000>\r\n".encode())
    assert hearts.wrap(p) == 0


def test_bilingual_reference_dedup(tmp_path):
    """双语歌词里译文和原词共用时间戳，不能被当成独立歌词行。"""
    from kiralrc import text as tx

    body = "\n".join([
        "[00:01.00]日文第一句",
        "[00:01.00]中文翻译一",
        "[00:05.00]日文第二句",
        "[00:05.01]中文翻译二",
        "[00:09.00]日文第三句",
    ])
    assert tx.lines_with_times(body) == [(1.0, "日文第一句"), (5.0, "日文第二句"),
                                        (9.0, "日文第三句")]


def test_query_from_filename():
    from kiralrc import text as tx

    assert tx.query_from_filename("02 - 好想好想 - 赵薇") == "好想好想 赵薇"
    assert tx.query_from_filename("艾秩曼+-+白狐（TV+Version）") == "艾秩曼 白狐"
    assert tx.query_from_filename("[110316]ソラノシズク") == "ソラノシズク"
    assert tx.query_from_filename("1986年のマリリン") == "1986年のマリリン"
