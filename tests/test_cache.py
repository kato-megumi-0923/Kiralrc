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
