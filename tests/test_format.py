"""格式层单元测试（不需要模型/音频）。"""
import pytest

from kiralrc import format as fmt

SAMPLE = """[ti:测试]
[ar:某人]
[00:01.230]<00:01.230>你<00:01.500>好<00:02.000>
[00:02.000]<00:02.000>翻译行<00:03.000>
"""


def test_parse_enhanced_and_translation():
    lrc = fmt.parse(SAMPLE)
    assert lrc.metas == ["[ti:测试]", "[ar:某人]"]
    assert len(lrc.lines) == 2

    a, b = lrc.lines
    assert a.enhanced and not b.enhanced
    assert a.chars[0] == ("你", 1.230)
    assert a.end == pytest.approx(2.000)
    # 翻译行只有首尾两个标签，文字不能被拆成逐字
    assert b.chars == []
    assert b.tag_count == 2
    assert b.end == pytest.approx(3.000)


def test_render_roundtrip():
    lrc = fmt.parse(SAMPLE)
    assert lrc.render().strip() == SAMPLE.strip()


def test_translation_line_keeps_end_tag():
    """曾经踩过：译文行丢行尾标签，播放器会把末字起点当行尾。"""
    ln = fmt.parse("[00:02.000]<00:02.000>你好世界<00:03.000>\n").lines[0]
    assert "<00:03.000>" in ln.render()


def test_convert_decimals(tmp_path):
    p = tmp_path / "a.lrc"
    p.write_bytes("[00:01.23]<00:01.23>你<00:01.50>\n".encode("utf-8"))
    n = fmt.convert_decimals(p)
    assert n == 3   # 行首 + 首标签 + 行尾
    assert p.read_text(encoding="utf-8") == "[00:01.230]<00:01.230>你<00:01.500>\n"
    # 已经是三位的不会被重复处理
    assert fmt.convert_decimals(p) == 0


def test_validate_catches_problems(tmp_path):
    p = tmp_path / "bad.lrc"
    p.write_bytes("[00:01.000]<00:01.000>你<00:00.500>好<00:01.000>\n".encode("utf-8"))
    probs = fmt.validate_enhanced(p)
    assert any("倒序" in x for x in probs)
    assert any("倒序" in x for x in probs)


def test_validate_passes_on_good(tmp_path):
    p = tmp_path / "ok.lrc"
    p.write_bytes(SAMPLE.encode("utf-8"))
    assert fmt.validate_enhanced(p) == []


# --- 回归：解析层曾经踩过的坑 ---

def test_multi_timestamp_line_expands():
    """[00:01][00:15]副歌 —— 一行挂多个时间戳要展开成多行。"""
    lrc = fmt.parse("[00:01.000][00:15.000]重复副歌\n")
    assert [(l.start, l.text) for l in lrc.lines] == [(1.0, "重复副歌"), (15.0, "重复副歌")]


def test_multi_timestamp_with_enhanced_body():
    lrc = fmt.parse("[00:01.000][00:09.000]<00:01.000>你<00:01.500>好<00:02.000>\n")
    assert len(lrc.lines) == 2
    assert lrc.lines[0].enhanced and lrc.lines[1].enhanced
    assert lrc.lines[1].chars[0][1] == 1.0        # 逐字时间是原样（不做偏移）


def test_programmatic_line_renders_enhanced():
    """SOFA/WhisperX 里直接构造的 Line 没有 tag_count，也必须按逐字渲染。"""
    ln = fmt.Line(1.0, "你好", [("你", 1.0), ("好", 1.5)], 2.0)
    assert ln.enhanced
    assert ln.render() == "[00:01.000]<00:01.000>你<00:01.500>好<00:02.000>"


def test_single_char_line_is_enhanced():
    """单字行只有「字标签 + 行尾」两个标签，不能被当成译文行。"""
    ln = fmt.parse("[00:26.747]<00:26.747>你<00:27.780>\n").lines[0]
    assert ln.enhanced
    assert ln.chars[0][0] == "你"
    assert ln.end == pytest.approx(27.780)
    # 真正的译文行（两个字以上）仍判为非逐字
    tr = fmt.parse("[00:01.000]<00:01.000>中文翻译<00:02.000>\n").lines[0]
    assert not tr.enhanced
