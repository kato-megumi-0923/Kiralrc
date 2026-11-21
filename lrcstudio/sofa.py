"""SOFA（Singing-Oriented Forced Aligner）歌声对齐。

流程：整句切片 → 拼音 .lab → 在独立 venv 里跑 SOFA → 读回逐音节时间。

为什么按句切片而不是整首：SOFA 在 opencpop 的**乐句**上训练，整首 3 分钟会塌到开头。
为什么按字数限长：否则遇到长间奏，模型会把一句摊到几十秒里。
"""
from __future__ import annotations

import json
import re
import subprocess
from pathlib import Path

from . import audio as au
from . import config, format as fmt, text as tx

HAN = re.compile(r"[\u4e00-\u9fff]")
TS = re.compile(r"^\[(\d{2}):(\d{2}\.\d{2,3})\](.*)$")


def prep_segments(vocal_wav: Path, lines: list[tuple[float, str]], out_dir: Path,
                  total: float) -> list[dict]:
    """按行切片写 wav + 拼音 lab，返回 meta。"""
    out_dir.mkdir(parents=True, exist_ok=True)
    keys = {ln.split("\t")[0].strip()
            for ln in open(config.path("sofa_dict"), encoding="utf-8") if ln.strip()}
    wav = au.decode(vocal_wav, 44100)

    meta, missing = [], []
    for i, (t0, line) in enumerate(lines):
        nxt = lines[i + 1][0] if i + 1 < len(lines) else total
        chars = tx.han_chars(line)
        if not chars:
            continue
        syls = tx.pinyin_syllables(line)
        bad = [s for s in syls if s not in keys]
        if bad:
            missing.append((line[:12], bad))
        if len(syls) != len(chars):
            missing.append((line[:12], f"拼音数 {len(syls)} != 汉字数 {len(chars)}"))

        start = max(0.0, t0 - 0.35)
        end = min(total, nxt + 0.9, t0 + max(3.0, len(chars) * 0.85) + 1.0)
        end = max(end, start + 1.2)
        seg = wav[int(start * 44100): int(end * 44100)]

        name = f"l{i + 1:02d}"
        au.write_wav(out_dir / f"{name}.wav", seg, 44100)
        (out_dir / f"{name}.lab").write_text(" ".join(syls), encoding="utf-8")
        meta.append({"name": name, "offset": start, "start": t0, "seg_end": end,
                     "text": line, "chars": chars, "syls": syls})

    (out_dir / "meta.json").write_text(
        json.dumps(meta, ensure_ascii=False, indent=1), encoding="utf-8")
    return meta, missing


def run(segments_dir: Path, out_formats: str = "htk") -> None:
    """在 sofa venv 里调 SOFA 推理。"""
    python = config.path("sofa_python")
    driver = config.ROOT / "tools" / "run_sofa.py"
    ckpt = config.path("sofa_ckpt")
    dic = config.path("sofa_dict")
    if not Path(python).exists():
        raise RuntimeError(f"找不到 SOFA 运行环境: {python}（见 README 的 setup）")
    if not Path(ckpt).exists():
        raise RuntimeError(f"找不到 SOFA 模型: {ckpt}（运行 scripts/fetch_models.py）")
    cmd = [str(python), str(driver), "--ckpt", str(ckpt), "--folder", str(segments_dir),
           "--out_formats", out_formats, "--dictionary", str(dic)]
    p = subprocess.run(cmd, capture_output=True, text=True, cwd=str(config.ROOT))
    if p.returncode != 0:
        raise RuntimeError(f"SOFA 失败:\n{(p.stderr or p.stdout)[-2500:]}")


def read_words(path: Path) -> list[tuple[float, float, str]]:
    """htk/words/*.lab → [(start, end, token)]，时间相对切片起点。"""
    out = []
    for ln in open(path, encoding="utf-8"):
        p = ln.split()
        if len(p) >= 3:
            out.append((int(p[0]) / 1e7, int(p[1]) / 1e7, p[2]))
    return out


def build_lrc(meta: list[dict], words_dir: Path, ti: str = "", ar: str = "",
              al: str = "", by: str = "SOFA 歌声对齐") -> fmt.Lrc:
    """把逐音节时间贴回汉字，生成逐字 Lrc（含行尾标签、句间夹断）。"""
    rows = []
    for m in meta:
        p = words_dir / f"{m['name']}.lab"
        if not p.exists():
            continue
        syl = [w for w in read_words(p) if w[2] not in ("SP", "AP")]
        if not syl:
            continue
        off = m["offset"]
        chars, k = [], 0
        for ch in m["text"]:
            if HAN.match(ch):
                if k < len(syl):
                    chars.append((ch, off + syl[k][0]))
                k += 1
            else:
                chars.append((ch, chars[-1][1] if chars else off + syl[0][0]))
        if not chars:
            continue
        end = off + syl[min(len(syl), k) - 1][1] if syl else chars[-1][1] + 0.1
        rows.append(fmt.Line(chars[0][1], m["text"], chars, end))

    # 逐句独立对齐，句尾可能压到下一句
    for i, ln in enumerate(rows):
        cap = (rows[i + 1].chars[0][1] - 0.02) if i + 1 < len(rows) else None
        if cap is not None:
            ln.chars = [(c, min(t, cap)) for c, t in ln.chars]
            ln.end = min(ln.end, cap)
        prev = -10.0
        fixed = []
        for c, t in ln.chars:
            t = max(t, prev)
            fixed.append((c, t))
            prev = t
        ln.chars = fixed
        ln.start = fixed[0][1]
        ln.end = max(ln.end, fixed[-1][1] + 0.1)

    metas = [x for x in (f"[ti:{ti}]" if ti else "", f"[ar:{ar}]" if ar else "",
                         f"[al:{al}]" if al else "", f"[by:{by}]") if x]
    return fmt.Lrc(metas, rows)
