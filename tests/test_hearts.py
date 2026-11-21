"""双语加 ❤️ 的测试。"""
from karalrc import hearts

BILINGUAL = "\r\n".join([
    "[ti:t]",
    "[00:01.000]<00:01.000>あ<00:01.400>い<00:02.000>",
    "[00:01.000]<00:01.000>爱<00:02.000>",
    "",
])


def test_only_translation_lines(tmp_path):
    p = tmp_path / "a.lrc"
    p.write_bytes(BILINGUAL.encode("utf-8"))
    assert hearts.wrap(p) == 1
    out = p.read_bytes().decode("utf-8").split("\r\n")
    assert out[1].startswith("[00:01.000]<00:01.000>あ")      # 逐字行不动
    assert "❤️爱❤️" in out[2] and out[2].endswith("<00:02.000>")  # 译文加心且保留行尾
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
