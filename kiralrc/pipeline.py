"""端到端流水线：音频（+可选参考歌词） → 逐字 LRC。

    1. 定歌词文本：给了参考文件就用，否则去网易云按文件名搜；
    2. 定每句起始时间：参考歌词本身带时间戳就直接用，否则用 ASR + 行级 DP 估；
    3. demucs 分离人声（缓存）；
    4. 中文走 SOFA 歌声对齐，其它语言走 WhisperX；
    5. 输出三位小数、带行尾标签的逐字 LRC。
"""
from __future__ import annotations

import hashlib
import json
import re
import shutil
from pathlib import Path

from . import audio as au
from . import config, format as fmt, text as tx

TS = re.compile(r"^\[(\d{2}):(\d{2}\.\d{2,3})\]", re.M)


def _cache_dir(audio_path: Path) -> Path:
    key = hashlib.md5(str(audio_path.resolve()).encode()).hexdigest()[:10]
    d = config.work_dir() / f"{audio_path.stem[:40]}_{key}"
    d.mkdir(parents=True, exist_ok=True)
    return d


def _vocals(audio_path: Path, cache: Path) -> Path:
    """分离人声（已存在则复用）。"""
    for stem in ("vocals",):
        p = cache / f"{stem}.wav"
        if p.exists() and p.stat().st_size > 1024:
            return p
    res = au.separate(audio_path, cache / "sep", sr=config.opt("sep_sample_rate", 44100))
    src = res.get("vocals")
    if not src:
        raise RuntimeError("demucs 没有产出 vocals 轨")
    dst = cache / "vocals.wav"
    shutil.move(str(src), dst)
    for k, v in res.items():
        if k != "vocals" and Path(v).exists():
            Path(v).unlink()
    return dst


def resolve_reference(audio_path: Path, ref: Path | None,
                      query: str | None = None) -> tuple[Path | None, list[str], str]:
    """返回 (参考文件, 歌词行, 说明)。"""
    if ref and Path(ref).exists():
        body = Path(ref).read_text(encoding="utf-8-sig")
        stamped = tx.lines_with_times(body)
        lines = [t for _, t in stamped] if len(stamped) >= 6 else tx.ref_lines(ref)
        return Path(ref), lines, f"参考文件 {Path(ref).name}"

    kw = query or tx.query_from_filename(audio_path.stem)
    dur = round(au.duration(audio_path))
    hit = tx.best_match(kw, dur)
    if not hit:
        return None, [], f"网易云没搜到「{kw}」，且没有参考文件"

    p = config.work_dir() / (audio_path.stem[:40] + ".ref.lrc")
    p.write_text(hit["lrc"], encoding="utf-8")
    stamped = tx.lines_with_times(hit["lrc"])
    lines = [t for _, t in stamped] if stamped else tx.ref_lines(p)
    return p, lines, f"网易云 {hit['artist']} - {hit['name']} ({hit['duration']}s)"


def line_times(ref_path: Path | None, lines: list[str], audio_path: Path,
               cache: Path, lang: str) -> list[tuple[float, str]]:
    """每句的起始时间：参考 LRC 自带时间戳优先，否则 ASR 估。"""
    if ref_path:
        stamped = tx.lines_with_times(Path(ref_path).read_text(encoding="utf-8-sig"))
        if len(stamped) >= max(6, len(lines) - 2):
            return stamped

    from . import asr

    audio = au.decode(audio_path, 16000)
    words, segs, dur = asr.transcribe(audio, lang=lang)
    (cache / "asr.json").write_text(
        json.dumps({"words": words, "segments": segs, "duration": dur}, ensure_ascii=False),
        encoding="utf-8")
    return asr.assign_lines(lines, segs)


def _metas(ti: str, ar: str, al: str, by: str) -> list[str]:
    """统一的头部：ti / ar / al / by，空字段跳过。"""
    return [x for x in (f"[ti:{ti}]" if ti else "", f"[ar:{ar}]" if ar else "",
                        f"[al:{al}]" if al else "", f"[by:{by}]") if x]


def make_lrc(audio_path: str | Path, ref: str | Path | None = None, lang: str = "zh",
             query: str | None = None, ti: str = "", ar: str = "",
             al: str = "") -> tuple[fmt.Lrc, str]:
    audio_path = Path(audio_path)
    cache = _cache_dir(audio_path)
    total = au.duration(audio_path)

    ref_path, lines, note = resolve_reference(audio_path, Path(ref) if ref else None, query)
    if not lines:
        raise RuntimeError(note)

    rough = line_times(ref_path, lines, audio_path, cache, lang)
    if not rough:
        raise RuntimeError("无法确定每句的起始时间")
    rough = [(t, l) for t, l in rough if not tx.CREDIT_RE.match(l)]

    vocals = _vocals(audio_path, cache)

    if lang == "zh":
        from . import sofa

        seg_dir = cache / "segments"
        meta, missing = sofa.prep_segments(vocals, rough, seg_dir, total)
        if missing:
            note += f" | 拼音异常 {len(missing)} 处"
        sofa.run(seg_dir)
        lrc = sofa.build_lrc(meta, seg_dir / "htk" / "words")
        by = "SOFA 歌声对齐"
    else:
        from . import jp

        lrc = jp.align_lines(str(vocals), rough, lang=lang, total=total)
        by = f"WhisperX {lang} 逐字"

    lrc.metas = _metas(ti, ar, al, by)
    return lrc, note
