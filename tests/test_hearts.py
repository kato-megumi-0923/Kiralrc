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


def test_wrap_plain_translation_after_enhanced_line(tmp_path):
    path = tmp_path / "ai.lrc"
    original = (
        "\ufeff[00:01.00]<00:01.00>あ<00:01.40>い<00:02.00>\r\n"
        "  [00:01.050] 爱意  \n"
        "[00:01.050]第二条整行歌词\r\n"
        "[00:02.00]<00:02.00>こ<00:02.40>い<00:03.00>\n"
        "[00:02.060]不同时的歌词"
    )
    path.write_bytes(original.encode("utf-8"))

    assert hearts.wrap(path) == 1
    assert path.read_bytes() == original.replace(" 爱意  ", " ❤️爱意❤️  ").encode("utf-8")
    assert hearts.wrap(path) == 0


def test_skip_credits(tmp_path):
    p = tmp_path / "a.lrc"
    p.write_bytes("[00:00.000]<00:00.000>QQ音乐享有本翻译作品的著作权<00:01.000>\r\n".encode())
    assert hearts.wrap(p) == 0


def test_bilingual_reference_dedup(tmp_path):
    """双语歌词里译文和原词共用时间戳，不能被当成独立歌词行。"""
    from kiralrc import text as tx

    # 原词要有假名才算日文；纯汉字的「日文第一句」会被当成中文，本来就分辨不出
    body = "\n".join([
        "[00:01.00]君の名前を呼ぶ",
        "[00:01.00]呼唤你的名字",
        "[00:05.00]風が吹いてる",
        "[00:05.01]风吹过来",
        "[00:09.00]また会おうね",
    ])
    assert tx.lines_with_times(body) == [(1.0, "君の名前を呼ぶ"), (5.0, "風が吹いてる"),
                                        (9.0, "また会おうね")]


def test_two_chinese_lines_a_breath_apart_are_not_merged():
    """同语种、只差几十毫秒的两句是两句，不能当译文吞掉。"""
    from kiralrc import text as tx

    body = "\n".join([
        "[00:05.00]第一句中文",
        "[00:05.02]第二句中文",
    ])
    assert tx.lines_with_times(body) == [(5.0, "第一句中文"), (5.02, "第二句中文")]


def test_query_from_filename():
    from kiralrc import text as tx

    assert tx.query_from_filename("02 - 好想好想 - 赵薇") == "好想好想 赵薇"
    assert tx.query_from_filename("艾秩曼+-+白狐（TV+Version）") == "艾秩曼 白狐"
    assert tx.query_from_filename("[110316]ソラノシズク") == "ソラノシズク"
    assert tx.query_from_filename("1986年のマリリン") == "1986年のマリリン"
