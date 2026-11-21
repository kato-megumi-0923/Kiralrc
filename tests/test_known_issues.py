"""针对 docs/known-issues.md 中 7 项待办（KI-1 ~ KI-7）的单元测试。"""
from __future__ import annotations

import os
import time
import numpy as np
import pytest

from kiralrc import asr, audio, format as fmt, jp, pipeline, text as tx


def test_ki1_vocal_activity_distinguishes_singing_and_instrumental():
    """KI-1：人声轨 1~4kHz 共振峰能量占比与 RMS 筛除无主唱/低频伴奏。"""
    sr = 16000
    t = np.linspace(0, 2.0, sr * 2, endpoint=False, dtype=np.float32)

    # 1) 静音 / 极微弱底噪 -> 无主唱
    silent = 0.0005 * np.sin(2 * np.pi * 1500 * t)
    act_sil = audio.vocal_activity(silent, sr=sr)
    assert not act_sil["has_vocal"]
    assert "能量过低" in act_sil["reason"]

    # 2) 仅有低频大提琴/贝斯（200 Hz，1~4kHz 占比接近 0） -> 无主唱
    bass_only = 0.15 * np.sin(2 * np.pi * 200 * t)
    act_bass = audio.vocal_activity(bass_only, sr=sr)
    assert not act_bass["has_vocal"]
    assert act_bass["mid_ratio"] < 0.08

    # 3) 含 1~4kHz 歌声共振峰能量 -> 主唱正常
    vocal = 0.08 * np.sin(2 * np.pi * 440 * t) + 0.07 * np.sin(2 * np.pi * 2200 * t)
    act_voc = audio.vocal_activity(vocal, sr=sr)
    assert act_voc["has_vocal"]
    assert act_voc["mid_ratio"] >= 0.08


def test_ki2_assign_lines_drops_cut_verse_in_tv_size():
    """KI-2：剪辑版音频跳过了第二段主歌时，DP 应剔除低覆盖率的被剪行，而非硬塞进相邻分段。"""
    lines = [
        "走在乡间的小路上",
        "暮归的老牛是我同伴",
        "蓝天配朵夕阳在胸膛",      # 假设在 TV size 里被剪掉
        "缤纷的云彩是晚霞的衣裳",  # 假设在 TV size 里被剪掉
        "笑意写在脸上",
        "哼一曲乡居小唱",
    ]
    segments = [
        {"start": 6.0, "end": 10.0, "text": "走在乡间的小路上"},
        {"start": 11.0, "end": 15.0, "text": "暮归的老牛是我同伴"},
        {"start": 16.0, "end": 20.0, "text": "笑意写在脸上"},
        {"start": 21.0, "end": 25.0, "text": "哼一曲乡居小唱"},
    ]
    matched, skipped = asr.assign_lines_detail(lines, segments, min_coverage=0.25)
    assert [m[1] for m in matched] == [
        "走在乡间的小路上",
        "暮归的老牛是我同伴",
        "笑意写在脸上",
        "哼一曲乡居小唱",
    ]
    assert [m[0] for m in matched] == [6.0, 11.0, 16.0, 21.0]
    assert [s["index"] for s in skipped] == [3, 4]


def test_ki3_restore_sustain_extends_compressed_long_vowel():
    """KI-3：wav2vec2 把 5.2s 的拖腔压成 2.4s 时，按人声 RMS 包络恢复真实收尾时间。"""
    hop = 0.02
    # 构造 7 秒包络：1.0s ~ 5.26s 持续有人声（模拟长拖腔），之后静音
    env = np.full(int(7.0 / hop), 0.002, dtype=np.float32)
    env[int(1.0 / hop): int(5.26 / hop)] = 0.12

    rows = [
        fmt.Line(1.0, "ずっと", [("ず", 1.0), ("っ", 1.4), ("と", 1.8)], end=2.47),
        fmt.Line(6.0, "見ていたい", [("見", 6.0), ("て", 6.3)], end=6.8),
    ]
    fixed = jp.restore_sustain(rows, env, hop=hop, total=7.0)
    assert fixed[0].end == pytest.approx(5.26, abs=0.05)


def test_ki4_batch_align_and_scan_library(tmp_path, monkeypatch):
    """KI-4：批量扫描与补齐，已存在逐字跳过、无主唱跳过、单首异常不中断。"""
    # 1) 已有逐字 LRC
    a1 = tmp_path / "01_done.mp3"
    a1.write_bytes(b"fake")
    (tmp_path / "01_done.lrc").write_bytes(
        "[00:01.000]<00:01.000>好<00:01.500>的<00:02.000>\n".encode("utf-8")
    )
    # 2) 无主唱曲目
    a2 = tmp_path / "02_inst.mp3"
    a2.write_bytes(b"fake")
    # 3) 正常待生成曲目
    a3 = tmp_path / "03_todo.mp3"
    a3.write_bytes(b"fake")

    items = pipeline.scan_library(tmp_path)
    assert {it["name"]: it["status"] for it in items} == {
        "01_done": "enhanced",
        "02_inst": "missing",
        "03_todo": "missing",
    }

    # 验证纯音乐 / OST / 伴奏自动识别与排除
    a_off = tmp_path / "04_POWDER SNOW (off Vocals).wav"
    a_off.write_bytes(b"fake")
    a_ost = tmp_path / "05_情深深雨濛濛(纯音乐演奏).wav"
    a_ost.write_bytes(b"fake")
    a_pure = tmp_path / "06_bgm.mp3"
    a_pure.write_bytes(b"fake")
    (tmp_path / "06_bgm.lrc").write_bytes("[00:00.00]纯音乐，请欣赏\n".encode("utf-8"))

    items2 = pipeline.scan_library(tmp_path)
    by_name = {it["name"]: it for it in items2}
    assert by_name["04_POWDER SNOW (off Vocals)"]["status"] == "instrumental"
    assert by_name["04_POWDER SNOW (off Vocals)"]["inst_kind"] == "伴奏 / Off Vocal"
    assert by_name["05_情深深雨濛濛(纯音乐演奏)"]["inst_kind"] == "OST 原声"
    assert by_name["06_bgm"]["inst_kind"] == "纯音乐"

    def fake_make_lrc(audio_path, **kw):
        if "02_inst" in str(audio_path):
            raise pipeline.NoVocalError("无主唱 (1-4kHz 占比 0.04<0.08)")
        ln = fmt.Line(1.0, "测试", [("测", 1.0), ("试", 1.5)], 2.0)
        return fmt.Lrc(["[ti:测试]"], [ln]), "模拟生成"

    monkeypatch.setattr(pipeline, "make_lrc", fake_make_lrc)
    res = pipeline.batch_align(tmp_path)
    assert res["total"] == 6
    assert res["skipped_existing"] == 1
    assert res["skipped_novocal"] == 4
    assert res["done"] == 1
    assert res["failed"] == 0
    assert (tmp_path / "03_todo.lrc").exists()


def test_ki5_polyphone_whitelist_and_custom_override(tmp_path):
    """KI-5：多音字词表覆盖（内置 + 自定义 TOML），且保证音节数 == 汉字数。"""
    custom = tmp_path / "poly.toml"
    custom.write_text('[phrases]\n"还得走" = "hai dei zou"\n"朝霞" = "zhao xia"\n', encoding="utf-8")
    ov = tx.load_polyphones(custom)

    syls = tx.pinyin_syllables("我还得走在朝霞里！", overrides=ov)
    chars = tx.han_chars("我还得走在朝霞里！")
    assert len(syls) == len(chars)
    assert syls[1:3] == ["hai", "dei"]
    assert syls[5:7] == ["zhao", "xia"]

    detected = tx.detect_polyphones("我还觉得快乐", overrides=ov)
    assert any(d["char"] == "得" and d["pinyin"] == "de" for d in detected)
    assert any(d["char"] == "乐" and d["pinyin"] == "le" for d in detected)


def test_ki6_clean_cache_by_days(tmp_path):
    """KI-6：按天数清理 work/ 缓存。"""
    old_dir = tmp_path / "songA_1111"
    old_dir.mkdir()
    f_old = old_dir / "vocals.wav"
    f_old.write_bytes(b"x" * 2048)
    old_time = time.time() - 10 * 86400
    os.utime(f_old, (old_time, old_time))
    os.utime(old_dir, (old_time, old_time))

    new_dir = tmp_path / "songB_2222"
    new_dir.mkdir()
    (new_dir / "vocals.wav").write_bytes(b"y" * 1024)

    st = pipeline.cache_stats(tmp_path)
    assert st["count"] == 2
    assert st["bytes"] == 3072

    # 仅清理 7 天前的缓存
    res = pipeline.clean_cache(days=7, work=tmp_path)
    assert res["removed"] == 1
    assert res["freed_bytes"] == 2048
    assert not old_dir.exists() and new_dir.exists()


def test_ki7_english_granularity_word_vs_char():
    """KI-7：英文/数字混排支持按词（整词同起点）或按字母均分词时长。"""
    words = [
        {"word": "I", "start": 1.0, "end": 1.2},
        {"word": "promise", "start": 1.3, "end": 2.0},
        {"word": "you", "start": 2.1, "end": 2.4},
    ]
    ln_word = jp.place_segment_chars("I promise you", words, en_granularity="word")
    assert ln_word is not None
    # 按词模式："promise" 的 7 个字母都是 1.300，且不会把后面的 "you" 挤偏
    p_times_word = [t for _, t in ln_word.chars[2:9]]
    assert all(t == pytest.approx(1.3) for t in p_times_word)
    assert ln_word.chars[-1] == ("u", 2.1)

    ln_char = jp.place_segment_chars("I promise you", words, en_granularity="char")
    assert ln_char is not None
    # 按字母均分模式："promise" 的 7 个字母从 1.3 到 1.9 递增（步长 0.1s）
    p_times_char = [t for c, t in ln_char.chars[2:9]]
    assert p_times_char == [pytest.approx(1.3 + 0.1 * k, abs=1e-3) for k in range(7)]
    assert ln_char.chars[-3][1] == pytest.approx(2.1, abs=1e-3)
