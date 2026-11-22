"""批量下载歌词：扫描 → 认 ID → 取词 → 按规则写盘。

规则（跟 Cloudy 一致，官方优先）：
- 已有官方逐字：跳过（同源不重下），除非 force；
- 已有对齐逐字、下载到官方逐字：覆盖（旧文件留 .lrc.bak）；
- 已有逐行、下载到逐字：升级；
- 只有逐行可下：缺失时才写，不拿逐行盖逐行；
- 一个源都没给：不写文件。
"""

from __future__ import annotations

import os
import re
import threading
import time
from concurrent.futures import ThreadPoolExecutor
from dataclasses import dataclass, field
from pathlib import Path

from . import format as fmt
from . import sources
from . import text as tx
from .ratelimit import RateLimited
from .ratelimit import RateLimited

BY_RE = re.compile(r"^\[by:([^\]]*)\]", re.M)
ALIGN_RE = re.compile(r"^\[kiralrc:aligned\]", re.M | re.I)
OFFICIAL = set(sources.TAGS.values())
# 纯音乐、伴奏、卡拉 OK 版：没有主唱，下歌词没意义
INSTRUMENTAL = re.compile(
    r"伴奏|纯音乐|off\s*vocal|instrumental|インスト|カラオケ|karaoke|"
    r"\bbgm\b|オリジナルカラオケ|オルゴール|music\s*box", re.I)
ALIGNED_MARKS = ("SOFA", "MMS", "WhisperX", "听写", "模板迁移", "波包络")

# 一个源碰了限速，整条队列一起等，别各自硬撞
_PAUSE = {"until": 0.0}
_LOCK = threading.Lock()


def wait_pause() -> None:
    while True:
        left = _PAUSE["until"] - time.time()
        if left <= 0:
            return
        time.sleep(min(left, 5))


def hold(seconds: float) -> None:
    with _LOCK:
        _PAUSE["until"] = max(_PAUSE["until"], time.time() + seconds)


def lrc_kind(path: Path) -> str:
    """这首歌现在的歌词状态：官方逐字 / 对齐逐字 / 逐行 / 无。"""
    lrc = path.with_suffix(".lrc")
    if not lrc.exists():
        return "missing"
    try:
        body = lrc.read_text(encoding="utf-8-sig", errors="ignore")
    except Exception:
        return "missing"
    if not body.strip():
        return "missing"
    word = "<" in body
    if ALIGN_RE.search(body):
        return "aligned"
    m = BY_RE.search(body)
    by = (m.group(1) if m else "").strip()
    if by in OFFICIAL:
        # 平台名只说明来自哪儿；没有字级标签就还是逐行，别当成已完成的逐字
        return "official" if word else "line"
    if any(mark in by for mark in ALIGNED_MARKS):
        return "aligned"
    return "word" if word else "line"


def song_name_artist(audio: Path) -> tuple[str, str]:
    """这首歌的标题与歌手：标签优先，其次按「标题 - 歌手」拆文件名。"""
    title = artist = ""
    # 同目录的 .lrc 里常有 [ti:] / [ar:]（平台写下的），比文件名可靠
    lrc = audio.with_suffix(".lrc")
    if lrc.exists():
        try:
            body = lrc.read_text(encoding="utf-8-sig", errors="ignore")
            for tag, slot in (("ti", "title"), ("ar", "artist")):
                m = re.search(r"^\[" + tag + r":([^\]]*)\]", body, re.M | re.I)
                if m and m.group(1).strip():
                    if slot == "title":
                        title = m.group(1).strip()
                    else:
                        artist = m.group(1).strip()
        except Exception:
            pass
    try:
        from mutagen import File as MutagenFile
        f = MutagenFile(str(audio))
        tags = getattr(f, "tags", None)
        if tags:
            for key in ("title", "TIT2"):
                v = tags.get(key) if hasattr(tags, "get") else None
                if v:
                    title = str(v[0] if isinstance(v, list) else v).strip()
                    break
            for key in ("artist", "TPE1"):
                v = tags.get(key) if hasattr(tags, "get") else None
                if v:
                    artist = str(v[0] if isinstance(v, list) else v).strip()
                    break
    except Exception:
        pass
    if not title:
        parts = [x.strip() for x in audio.stem.split(" - ") if x.strip()]
        if len(parts) >= 3 and parts[0].isdigit():
            title, artist = parts[1], " - ".join(parts[2:])
        elif len(parts) >= 2:
            title, artist = parts[0], " - ".join(parts[1:])
        else:
            title = audio.stem
    return title.strip(), artist.strip()


@dataclass
class Result:
    audio: Path
    status: str = "failed"   # downloaded / upgraded / replaced / kept / line / missing / failed
    note: str = ""
    source: str = ""


@dataclass
class Counts:
    all: int = 0
    done: int = 0
    downloaded: int = 0      # 新写或升级成逐字
    replaced: int = 0        # 官方逐字盖掉了对齐逐字
    kept: int = 0
    line: int = 0            # 只拿到逐行
    missing: int = 0
    failed: int = 0
    by_source: dict[str, int] = field(default_factory=dict)


def _write(path: Path, lrc: fmt.Lrc) -> None:
    """写盘：先备份一次原文件，再用 LF 与三位小数写出。"""
    from . import pipeline

    target = path.with_suffix(".lrc")
    if target.exists():
        pipeline.backup_lrc_once(target)
    tmp = target.with_suffix(".lrc.tmp")
    fmt.write(tmp, lrc)
    os.replace(tmp, target)


def download_one(audio: Path, force: bool = False) -> Result:
    """给一首歌下载歌词，按规则决定要不要写。"""
    audio = Path(audio)
    kind = lrc_kind(audio)
    if kind == "official" and not force:
        return Result(audio, "kept", "已有官方逐字")

    wait_pause()
    song_id = tx.netease_id_of(audio)
    # 匹配要的是「歌名 + 歌手」，不是拼在一起的搜索串
    title, artist = song_name_artist(audio)
    name = title or tx.query_from_filename(audio.stem)
    duration = None
    try:
        from . import audio as au
        duration = au.duration(audio)
    except Exception:
        duration = None

    try:
        lyric = sources.fetch(song_id, name=name, artist=artist, duration=duration)
    except RateLimited:
        # 被限速时整条队列一起等，然后给这首一次机会
        hold(60)
        try:
            wait_pause()
            lyric = sources.fetch(song_id, name=name, artist=artist, duration=duration)
        except RateLimited:
            return Result(audio, "failed", "平台限速，稍后重试")
        except Exception as e:
            return Result(audio, "failed", f"{type(e).__name__}: {e}")

    if not lyric:
        return Result(audio, "missing" if kind == "missing" else "kept",
                      "没有源给出歌词")

    if not lyric.timed:
        # 只有逐行：缺的时候才写，别拿逐行盖掉已有的逐行或逐字
        if kind != "missing":
            return Result(audio, "kept", f"只拿到逐行（{lyric.source}）", lyric.source)
        lrc = sources.to_lrc(lyric, ti=title, ar=artist)
        _write(audio, lrc)
        return Result(audio, "line", f"逐行（{lyric.source}）", lyric.source)

    lrc = sources.to_lrc(lyric, ti=title, ar=artist)
    if kind == "aligned":
        _write(audio, lrc)
        return Result(audio, "replaced", f"官方逐字替换对齐结果（{lyric.source}）", lyric.source)
    _write(audio, lrc)
    return Result(audio, "upgraded" if kind != "missing" else "downloaded",
                  f"{lyric.source} 逐字", lyric.source)


def run(folder: str | Path, force: bool = False, jobs: int = 2,
        on_song=None, on_done=None, stop: threading.Event | None = None,
        paths: list[str] | None = None) -> Counts:
    """批量下载歌词：一个目录（含子目录），或者给一份文件清单。"""
    root = Path(folder) if folder else Path(".")
    if paths is not None:
        # 给了清单就只做清单里的；空清单是一首都不做，不能退回扫当前目录
        files = [Path(p) for p in paths if Path(p).suffix.lower() in
                 (".mp3", ".flac", ".wav", ".m4a", ".aac", ".dsf", ".dff")]
    elif root.is_file():
        files = [root]
    else:
        files = sorted(p for p in root.rglob("*")
                       if p.is_file() and p.suffix.lower() in (".mp3", ".flac", ".wav", ".m4a", ".aac", ".dsf", ".dff"))
    files = [p for p in files if not INSTRUMENTAL.search(p.stem)]
    counts = Counts(all=len(files))
    stop = stop or threading.Event()

    def work(path: Path) -> Result:
        if stop.is_set():
            return Result(path, "kept", "已停止")
        try:
            return download_one(path, force=force)
        except Exception as e:      # 一首歌的意外不该把整批带下去
            return Result(path, "failed", f"{type(e).__name__}: {e}")

    with ThreadPoolExecutor(max_workers=max(1, jobs)) as pool:
        for result in pool.map(work, files):
            counts.done += 1
            if result.status == "downloaded":
                counts.downloaded += 1
            elif result.status == "upgraded":
                counts.downloaded += 1
            elif result.status == "replaced":
                counts.replaced += 1
            elif result.status == "line":
                counts.line += 1
            elif result.status == "missing":
                counts.missing += 1
            elif result.status == "failed":
                counts.failed += 1
            else:
                counts.kept += 1
            if result.source:
                counts.by_source[result.source] = counts.by_source.get(result.source, 0) + 1
            if on_song:
                on_song(counts.done, counts.all, result)
            if on_done:
                on_done(result)
    return counts


def report(counts: Counts) -> str:
    """给命令行看的一行总结。"""
    parts = [f"共 {counts.all} 首",
             f"写入逐字 {counts.downloaded}",
             f"官方替换对齐 {counts.replaced}",
             f"仅逐行 {counts.line}",
             f"跳过 {counts.kept}",
             f"没找到 {counts.missing}"]
    if counts.failed:
        parts.append(f"失败 {counts.failed}")
    if counts.by_source:
        src = "、".join(f"{k} {v}" for k, v in sorted(counts.by_source.items(), key=lambda x: -x[1]))
        parts.append(f"来源：{src}")
    return " | ".join(parts)
