"""分离缓存：落在 work/sep，按时间清理。"""

import os
import time
import wave
from pathlib import Path

from kiralrc import audio as au
from kiralrc import cache


def _touch(path: Path, age_hours: float) -> None:
    path.mkdir(parents=True, exist_ok=True)
    old = time.time() - age_hours * 3600
    os.utime(path, (old, old))


def test_sep_dir_is_outside_the_library(tmp_path):
    audio = tmp_path / "某歌 - 某人.flac"
    audio.write_bytes(b"")
    d = cache.sep_dir(audio)
    assert "work" in d.parts and "sep" in d.parts
    assert d.name.startswith("某歌 - 某人_")
    assert tmp_path not in d.parents


def test_purge_only_drops_the_old_ones(monkeypatch, tmp_path):
    monkeypatch.setattr(cache, "sep_root", lambda: tmp_path)
    _touch(tmp_path / "old_1", 4)
    _touch(tmp_path / "fresh_2", 0.5)
    assert cache.purge() == 1
    assert not (tmp_path / "old_1").exists()
    assert (tmp_path / "fresh_2").exists()


def test_purge_all_empties_the_cache(monkeypatch, tmp_path):
    monkeypatch.setattr(cache, "sep_root", lambda: tmp_path)
    _touch(tmp_path / "a_1", 0)
    _touch(tmp_path / "b_2", 0)
    assert cache.purge_all() == 2
    assert not (tmp_path / "a_1").exists()


def _write_tone(path, seconds, start=0.0, amp=0.3, sr=44100):
    import numpy as np

    n = int(sr * seconds)
    pcm = np.zeros(n, dtype=np.float32)
    begin = int(sr * start)
    pcm[begin:] = amp * np.sin(2 * np.pi * 440 * np.arange(n - begin) / sr)
    au.write_wav(path, pcm, sr)


def test_first_sound_ignores_the_noise_floor(tmp_path):
    """demucs 的轨开头有一层底噪，不能因此报 0 秒。"""
    import numpy as np

    p = tmp_path / "noisy.wav"
    sr = 44100
    n = sr * 6
    pcm = (np.random.default_rng(0).normal(0, 0.02, n)).astype(np.float32)
    pcm[int(sr * 3.5):] += 0.5 * np.sin(2 * np.pi * 440 * np.arange(n - int(sr * 3.5)) / sr)
    au.write_wav(p, pcm, sr)
    got = au.first_sound(p)
    assert got is not None and 3.3 < got < 3.8, got


def test_first_sound_is_none_when_silent(tmp_path):
    import numpy as np

    p = tmp_path / "silent.wav"
    au.write_wav(p, np.zeros(44100, dtype=np.float32), 44100)
    assert au.first_sound(p) is None


def _write_tone(path, seconds, start=0.0, amp=0.3, sr=44100):
    import numpy as np

    n = int(sr * seconds)
    pcm = np.zeros(n, dtype=np.float32)
    begin = int(sr * start)
    pcm[begin:] = amp * np.sin(2 * np.pi * 440 * np.arange(n - begin) / sr)
    au.write_wav(path, pcm, sr)


def test_first_sound_ignores_the_noise_floor(tmp_path):
    """demucs 的轨开头有一层底噪，不能因此报 0 秒。"""
    import numpy as np

    p = tmp_path / "noisy.wav"
    sr = 44100
    n = sr * 6
    pcm = (np.random.default_rng(0).normal(0, 0.02, n)).astype(np.float32)
    pcm[int(sr * 3.5):] += 0.5 * np.sin(2 * np.pi * 440 * np.arange(n - int(sr * 3.5)) / sr)
    au.write_wav(p, pcm, sr)
    got = au.first_sound(p)
    assert got is not None and 3.3 < got < 3.8, got


def test_first_sound_is_none_when_silent(tmp_path):
    import numpy as np

    p = tmp_path / "silent.wav"
    au.write_wav(p, np.zeros(44100, dtype=np.float32), 44100)
    assert au.first_sound(p) is None


def test_transcode_writes_flac_and_mp3(tmp_path):
    src = tmp_path / "tone.wav"
    with wave.open(str(src), "wb") as w:
        w.setnchannels(1)
        w.setsampwidth(2)
        w.setframerate(44100)
        w.writeframes(b"\x00\x10" * 44100)

    for ext in ("flac", "mp3"):
        dst = tmp_path / f"tone.{ext}"
        au.transcode(src, dst)
        assert dst.exists() and dst.stat().st_size > 1000
