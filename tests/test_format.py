"""格式层单元测试（不需要模型/音频）。"""
import pytest

from karalrc import format as fmt

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
