"""批量下载歌词：扫描 → 认 ID → 取词 → 按规则写盘。

规则（跟 Cloudy 一致，官方优先）：
- 已有官方逐字：跳过（同源不重下），除非 force；
- 已有对齐逐字、下载到官方逐字：直接覆盖，不留备份；
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
    trans_filled: int = 0
    missing_trans: bool = False


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
    trans_filled: int = 0    # 补全了中文译文的曲目数
    missing_trans: int = 0   # 外语但仍缺译文的曲目数
    by_source: dict[str, int] = field(default_factory=dict)


_TRANS_COMPLETE_RATIO = 0.85

# 常见无实质语义的哼唱/拟声/感叹行，本来就没有中文译文，避免触发反复联网补译
_VOCALIZATION_TOKEN_RE = re.compile(
    r"^(?:la+|na+|da+|oh+|ah+|uh+|eh+|mm+|hm+|ha+|he+|hey+|yeah+|yea+|yo+|"
    r"woah+|whoa+|ooh+|woo+|yay+|wow+|shh+|"
    r"[ラらルるアあオおウうフふハはヘへー〜～]+|イェ[イイー]*|イエイ+)$",
    re.I,
)
_STRIP_PUNCT_RE = re.compile(r"[\s,，.。、!！?？:：;；'’\"”“`~～\-—_·•…()（）\[\]【】「」『』♪♫♡♥❤️]+")


def _is_skippable_for_trans(text: str) -> bool:
    """判断一行歌词是否为纯拟声/哼唱/极短感叹词，不计入缺译文判定。"""
    s = _STRIP_PUNCT_RE.sub(" ", text).strip()
    if not s:
        return True
    tokens = s.split()
    if tokens and all(_VOCALIZATION_TOKEN_RE.match(tok) for tok in tokens):
        return True
    # 仅含 1-2 个拉丁字母（如 "Oh"、"Ah"、"Go"）也跳过
    if re.fullmatch(r"[A-Za-z]{1,2}", s):
        return True
    return False


def _needs_zh_trans(lines: list[str]) -> bool:
    """判断歌词文本是否为外语（日/韩/英等），需要中文译文。"""
    if not lines:
        return False
    kana = sum(1 for ln in lines for ch in ln if "\u3040" <= ch <= "\u30ff")
    hangul = sum(1 for ln in lines for ch in ln if "\uac00" <= ch <= "\ud7af")
    han = sum(1 for ln in lines for ch in ln if "\u4e00" <= ch <= "\u9fff")
    latin = sum(1 for ln in lines for ch in ln if ("a" <= ch.lower() <= "z"))
    if kana >= 3 or hangul >= 3:
        return True
    if latin >= 10 and latin > han * 2:
        return True
    return False


def _missing_word_lines(lines: list[sources.WordLine]) -> list[str]:
    return [
        ln.text for ln in lines
        if ln.text.strip() and not ln.translation.strip() and not _is_skippable_for_trans(ln.text)
    ]


def _trans_sufficient(total_substantive: int, missing_substantive: int) -> bool:
    """译文覆盖率已达标（≥ 85%）时视为完整，避免少量哼唱/未译句反复触发联网请求。"""
    if total_substantive <= 0:
        return True
    covered = total_substantive - missing_substantive
    return covered > 0 and (covered / total_substantive) >= _TRANS_COMPLETE_RATIO


def _apply_trans_map_to_word_lines(
    lines: list[sources.WordLine],
    trans_map: dict[str, list[list[str]]],
) -> int:
    """用 trans_map 补齐 WordLine 中空的 translation 槽位，不覆盖已有译文、不动逐字时间戳。"""
    if not trans_map or not lines:
        return 0
    from . import pipeline as pl

    seen: dict[str, int] = {}
    added = 0
    for ln in lines:
        key = pl._norm_lyric_key(ln.text)
        if not key:
            continue
        occ = seen.get(key, 0)
        seen[key] = occ + 1
        if ln.translation.strip():
            continue
        q = trans_map.get(key) or []
        cand = q[occ] if occ < len(q) else []
        if not any(t.strip() for t in cand):
            cand = next((x for x in q if any(t.strip() for t in x)), [])
        if cand:
            picked = next((t.strip() for t in cand if t.strip()), "")
            if picked:
                ln.translation = picked
                added += 1
    return added


def _backfill_word_lyric_trans(
    audio: Path,
    lyric: sources.WordLyric,
    *,
    song_id: int | None,
    title: str,
    artist: str,
    duration: float | None,
    local_lrc_path: Path | None,
) -> tuple[int, int, bool]:
    """给刚下载的 WordLyric 保底与补全中文译文：
    1. 若本地已有译文，先无损继承到本地缺失槽位（防止官方逐字覆盖时把原有中文盖掉）；
    2. 若仍是外语且实质歌词行译文覆盖率不足 85%，再跨源（网易云 ID → 网易云 → QQ）拉取中文译文补齐。
    返回 (继承本地行数, 跨源补全行数, 是否仍缺译文)。
    """
    from . import pipeline as pl

    from_local = 0
    from_remote = 0
    if local_lrc_path and local_lrc_path.exists() and _missing_word_lines(lyric.lines):
        try:
            local_map = pl._extract_trans_map(local_lrc_path)
            if pl._has_any_trans(local_map):
                from_local = _apply_trans_map_to_word_lines(lyric.lines, local_map)
        except Exception:
            pass

    all_texts = [ln.text for ln in lyric.lines]
    is_foreign = _needs_zh_trans(all_texts)
    substantive = [ln.text for ln in lyric.lines if ln.text.strip() and not _is_skippable_for_trans(ln.text)]
    missing = _missing_word_lines(lyric.lines)
    if is_foreign and missing and not _trans_sufficient(len(substantive), len(missing)):
        query = f"{artist} {title}".strip() if (artist and title) else (title or None)
        try:
            fetched = pl._fetch_missing_translations(
                audio, duration or 0.0, query=query,
                netease_id=song_id, target_lines=missing,
            )
            if pl._has_any_trans(fetched):
                from_remote = _apply_trans_map_to_word_lines(lyric.lines, fetched)
        except Exception:
            pass

    missing_after = _missing_word_lines(lyric.lines)
    still_missing = is_foreign and bool(missing_after) and not _trans_sufficient(len(substantive), len(missing_after))
    return from_local, from_remote, still_missing


def _backfill_existing_lrc(
    audio: Path,
    *,
    song_id: int | None = None,
    title: str = "",
    artist: str = "",
    duration: float | None = None,
) -> tuple[int, bool]:
    """对本地已有的逐字 LRC，在不改动任何 <mm:ss.xxx> 逐字时间戳、元数据与已有译文行（含 ❤️）的前提下补全缺失的中文译文。

    返回 (新增译文行数, 是否仍缺译文)。中文歌词、或译文覆盖率 ≥ 85% 时直接返回 (0, False) 且不发起网络请求。
    """
    lrc_path = audio.with_suffix(".lrc")
    if not lrc_path.exists():
        return 0, False
    try:
        body = lrc_path.read_text(encoding="utf-8-sig", errors="ignore")
    except Exception:
        return 0, False
    if not body.strip():
        return 0, False

    orig_pairs, _ = tx.lines_with_translations(body)
    orig_texts = [t for _, t in orig_pairs]
    if not _needs_zh_trans(orig_texts):
        return 0, False

    from . import pipeline as pl

    local_map = pl._build_trans_map_from_text(body)
    substantive = [t for t in orig_texts if not _is_skippable_for_trans(t)]
    raw_missing = pl._missing_trans_lines(orig_texts, local_map)
    missing = [t for t in raw_missing if not _is_skippable_for_trans(t)]
    if not missing or _trans_sufficient(len(substantive), len(missing)):
        return 0, False

    wait_pause()
    if song_id is None:
        song_id = tx.netease_id_of(audio)
    if not title:
        title, artist = song_name_artist(audio)
    if duration is None:
        try:
            from . import audio as au
            duration = au.duration(audio)
        except Exception:
            duration = 0.0

    query = f"{artist} {title}".strip() if (artist and title) else (title or None)
    try:
        fetched = pl._fetch_missing_translations(
            audio, duration or 0.0, query=query,
            netease_id=song_id, target_lines=missing,
        )
    except Exception:
        return 0, True

    if not pl._has_any_trans(fetched):
        return 0, True

    # 原地按块保留 parsed.metas、所有原逐字行以及已有译文行（含 ❤️ 与原标签），
    # 仅在尚无译文行的原词后插入新拉取到的译文，彻底避免重复贴行或改动元数据。
    parsed = fmt.parse(body)
    blocks: list[tuple[fmt.Line, list[fmt.Line], bool]] = []
    last_lyric_idx = -1
    for ln in parsed.lines:
        t = ln.text.strip()
        is_lyric = bool(t) and not tx.CREDIT_RE.match(t)
        if is_lyric and last_lyric_idx >= 0:
            prev_orig = blocks[last_lyric_idx][0]
            dt = abs(ln.start - prev_orig.start)
            if dt < 0.06 and tx._is_translation_pair(prev_orig.text.strip(), t, dt):
                blocks[last_lyric_idx][1].append(ln)
                continue
        blocks.append((ln, [], is_lyric))
        if is_lyric:
            last_lyric_idx = len(blocks) - 1

    out_lines: list[fmt.Line] = []
    occ_seen: dict[str, int] = {}
    added = 0
    missing_after = 0
    for orig_ln, existing_trs, is_lyric in blocks:
        out_lines.append(orig_ln)
        if not is_lyric:
            continue
        t = orig_ln.text.strip()
        key = pl._norm_lyric_key(t)
        occ = occ_seen.get(key, 0) if key else 0
        if key:
            occ_seen[key] = occ + 1
        if existing_trs:
            out_lines.extend(existing_trs)
            continue
        q = fetched.get(key) or [] if key else []
        cand = q[occ] if occ < len(q) else []
        if not any(x.strip() for x in cand):
            cand = next((x for x in q if any(s.strip() for s in x)), [])
        inserted = False
        for tr in cand:
            if tr.strip():
                out_lines.append(fmt.Line(orig_ln.start, tr.strip()))
                added += 1
                inserted = True
        if not inserted and not _is_skippable_for_trans(t):
            missing_after += 1

    still_missing = bool(missing_after) and not _trans_sufficient(len(substantive), missing_after)
    if added > 0:
        rebuilt = fmt.Lrc(metas=list(parsed.metas), lines=out_lines)
        _write(audio, rebuilt)
    return added, still_missing


def _write(path: Path, lrc: fmt.Lrc) -> None:
    """写盘：LF + 三位小数，直接覆盖（按约定不留备份）。"""
    target = path.with_suffix(".lrc")
    tmp = target.with_suffix(".lrc.tmp")
    fmt.write(tmp, lrc)
    os.replace(tmp, target)


def download_one(audio: Path, force: bool = False,
                 sources_order: tuple[str, ...] | None = None) -> Result:
    """给一首歌下载歌词，按规则决定要不要写。"""
    audio = Path(audio)
    kind = lrc_kind(audio)
    if kind == "official" and not force:
        added, still_missing = _backfill_existing_lrc(audio)
        if added > 0:
            tail = "（仍有部分缺译）" if still_missing else ""
            return Result(
                audio, "upgraded",
                f"补全中文译文 {added} 行（保留原逐字）{tail}",
                trans_filled=added, missing_trans=still_missing,
            )
        if still_missing:
            return Result(audio, "kept", "已有官方逐字（缺译文）", missing_trans=True)
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
        lyric = sources.fetch(song_id, name=name, artist=artist, duration=duration, sources=sources_order)
    except RateLimited:
        # 被限速时整条队列一起等，然后给这首一次机会
        hold(60)
        try:
            wait_pause()
            lyric = sources.fetch(song_id, name=name, artist=artist, duration=duration, sources=sources_order)
        except RateLimited:
            return Result(audio, "failed", "平台限速，稍后重试")
        except Exception as e:
            return Result(audio, "failed", f"{type(e).__name__}: {e}")

    if not lyric:
        if kind in ("aligned", "word"):
            added, still_missing = _backfill_existing_lrc(
                audio, song_id=song_id, title=title, artist=artist, duration=duration,
            )
            if added > 0:
                return Result(
                    audio, "upgraded",
                    f"补全中文译文 {added} 行（保留原逐字）",
                    trans_filled=added, missing_trans=still_missing,
                )
            if still_missing:
                return Result(audio, "kept", "没有源给出歌词（缺译文）", missing_trans=True)
        return Result(audio, "missing" if kind == "missing" else "kept",
                      "没有源给出歌词")

    if not lyric.timed:
        # 只有逐行：缺的时候才写，别拿逐行盖掉已有的逐行或逐字（显式 force 指定来源时允许写入）
        if kind != "missing" and not force:
            if kind in ("aligned", "word"):
                added, still_missing = _backfill_existing_lrc(
                    audio, song_id=song_id, title=title, artist=artist, duration=duration,
                )
                if added > 0:
                    return Result(
                        audio, "upgraded",
                        f"补全中文译文 {added} 行（保留原逐字）",
                        lyric.source, trans_filled=added, missing_trans=still_missing,
                    )
            return Result(audio, "kept", f"只拿到逐行（{lyric.source}）", lyric.source)
        _, from_remote, still_missing = _backfill_word_lyric_trans(
            audio, lyric, song_id=song_id, title=title, artist=artist,
            duration=duration, local_lrc_path=None,
        )
        lrc = sources.to_lrc(lyric, ti=title, ar=artist, song_id=song_id or 0)
        _write(audio, lrc)
        note = f"逐行（{lyric.source}）"
        if from_remote:
            note += f" + 补全译文 {from_remote} 行"
        elif still_missing:
            note += "（缺译文）"
        return Result(audio, "line", note, lyric.source,
                      trans_filled=from_remote, missing_trans=still_missing)

    local_lrc = audio.with_suffix(".lrc") if kind != "missing" else None
    from_local, from_remote, still_missing = _backfill_word_lyric_trans(
        audio, lyric, song_id=song_id, title=title, artist=artist,
        duration=duration, local_lrc_path=local_lrc,
    )
    lrc = sources.to_lrc(lyric, ti=title, ar=artist, song_id=song_id or 0)
    extra = ""
    if from_local:
        extra += f" + 保留本地译文 {from_local} 行"
    if from_remote:
        extra += f" + 补全译文 {from_remote} 行"
    if still_missing:
        extra += "（缺译文）"
    if kind == "aligned":
        _write(audio, lrc)
        return Result(audio, "replaced", f"官方逐字替换对齐结果（{lyric.source}）{extra}",
                      lyric.source, trans_filled=from_remote, missing_trans=still_missing)
    _write(audio, lrc)
    return Result(audio, "upgraded" if kind != "missing" else "downloaded",
                  f"{lyric.source} 逐字{extra}", lyric.source,
                  trans_filled=from_remote, missing_trans=still_missing)


def run(folder: str | Path, force: bool = False, jobs: int = 2,
        on_song=None, on_done=None, stop: threading.Event | None = None,
        paths: list[str] | None = None,
        sources_order: tuple[str, ...] | None = None) -> Counts:
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
            return download_one(path, force=force, sources_order=sources_order)
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
            if result.trans_filled > 0:
                counts.trans_filled += 1
            if result.missing_trans:
                counts.missing_trans += 1
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
    if counts.trans_filled:
        parts.append(f"补译文 {counts.trans_filled}")
    if counts.missing_trans:
        parts.append(f"缺译文 {counts.missing_trans}")
    if counts.failed:
        parts.append(f"失败 {counts.failed}")
    if counts.by_source:
        src = "、".join(f"{k} {v}" for k, v in sorted(counts.by_source.items(), key=lambda x: -x[1]))
        parts.append(f"来源：{src}")
    return " | ".join(parts)
