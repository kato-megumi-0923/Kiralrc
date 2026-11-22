"""端到端流水线：音频（+可选参考歌词） → 逐字 LRC。

    1. 定歌词文本：给了参考文件就用，否则去网易云按文件名搜；
    2. 定每句起始时间：参考歌词本身带时间戳就直接用，否则用 ASR + 行级 DP 估；
    3. demucs 分离人声（缓存）；
    4. 主唱检测（KI-1）：筛除纯音乐/无主唱曲目；
    5. 中文走 SOFA 歌声对齐，日文走 NextFire MMS，英文及其它多语种走 Echora-300M MMS（带长音保真 KI-3 与英文粒度 KI-7）；
    6. 输出三位小数、带行尾标签的逐字 LRC。
"""
from __future__ import annotations

import concurrent.futures
import hashlib
import json
import re
import shutil
import time
from pathlib import Path
from typing import Callable

from . import audio as au
from . import config, format as fmt, text as tx

TS = re.compile(r"^\[(\d{2}):(\d{2}\.\d{2,3})\]", re.M)
AUDIO_EXTS = {".mp3", ".flac", ".wav", ".m4a", ".aac", ".ogg", ".opus"}

INST_ACCOMP_RE = re.compile(r"(off\s*vocals?|instrumental|伴奏|karaoke|カラオケ)", re.I)
INST_OST_RE = re.compile(r"(original\s*soundtrack|\bost\b|白2纯音乐|纯音乐演奏|大提琴Free)", re.I)
INST_PURE_RE = re.compile(r"(纯音乐|无词哼唱|烟雨濛濛\(人声曲\))", re.I)
INST_LRC_RE = re.compile(r"(纯音乐\s*[，,]\s*请欣赏|純音樂|没有填词的纯音乐)", re.I)


def classify_instrumental(audio_path: Path, lrc_raw: str = "",
                          root: Path | None = None) -> str | None:
    """识别纯音乐 / OST 原声 / 伴奏曲目，返回分类标签（人声歌曲返回 None）。"""
    stem = audio_path.stem
    rel_str = str(audio_path.relative_to(root)) if (root and audio_path.is_relative_to(root)) else str(audio_path)

    if INST_ACCOMP_RE.search(stem):
        return "伴奏 / Off Vocal"
    if INST_OST_RE.search(stem):
        return "OST 原声"
    if INST_PURE_RE.search(stem):
        return "纯音乐"

    in_ost_dir = bool(INST_OST_RE.search(rel_str))
    in_pure_dir = any(part in ("纯音乐", "Instrumental", "BGM") for part in Path(rel_str).parts)
    is_inst_lrc = bool(lrc_raw and INST_LRC_RE.search(lrc_raw))

    if in_ost_dir and (is_inst_lrc or not lrc_raw or "白2纯音乐" in rel_str):
        return "OST 原声"
    if in_pure_dir or is_inst_lrc:
        return "OST 原声" if in_ost_dir else "纯音乐"
    return None


class NoVocalError(RuntimeError):
    """人声轨未检出有效主唱（纯音乐 / 伴奏 / 无词低能量哼唱，KI-1）。"""


class NoLyricsError(RuntimeError):
    """多源搜索与本地均未找到歌词且未开启 --asr-fallback。"""


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
                      query: str | None = None,
                      netease_id: int | None = None) -> tuple[Path | None, list[str], str]:
    """返回 (参考文件, 歌词行, 说明)。

    歌词的来源按可靠程度排：显式参考文件 → 同目录 lrc → 指定的网易云 ID →
    音频标签里的 ID → 多源搜索。ID 比搜索可靠，冷门歌往往只有 ID 找得到。
    """
    if ref and Path(ref).exists():
        body = Path(ref).read_text(encoding="utf-8-sig")
        stamped = tx.lines_with_times(body)
        lines = [t for _, t in stamped] if len(stamped) >= 6 else tx.ref_lines(ref)
        return Path(ref), lines, f"参考文件 {Path(ref).name}"

    # 同目录已有 lrc（多半是整行时间）→ 直接当参考，省一次搜索
    # 若当前 .lrc 已经是逐字 LRC 且存在最初原版 .lrc.bak，则优先回读 .lrc.bak，
    # 避免多次重跑时因切片缓冲导致行首时间戳滚雪球漂移。
    sibling = audio_path.with_suffix(".lrc")
    sibling_bak = sibling.with_name(sibling.name + ".bak")
    if sibling.exists():
        body = sibling.read_text(encoding="utf-8-sig")
        if sibling_bak.exists() and re.search(r"<\d{2}:\d{2}\.\d{2,3}>", body):
            bak_body = sibling_bak.read_text(encoding="utf-8-sig")
            bak_stamped = tx.lines_with_times(bak_body)
            bak_lines = [t for _, t in bak_stamped] if len(bak_stamped) >= 6 else tx.ref_lines(sibling_bak)
            if len(bak_lines) >= 4:
                return sibling_bak, bak_lines, f"原始备份 {sibling_bak.name}"
        stamped = tx.lines_with_times(body)
        lines = [t for _, t in stamped] if len(stamped) >= 6 else tx.ref_lines(sibling)
        if len(lines) >= 4:          # 少于 4 行多半是「纯音乐，请欣赏」这类占位
            return sibling, lines, f"同目录 {sibling.name}"

    kw = query or tx.query_from_filename(audio_path.stem)
    dur = round(au.duration(audio_path))
    artist_hint, title_hint = tx.artist_title_from_filename(audio_path.stem)
    if query:
        title_hint, artist_hint = query, ""
    used_id = netease_id or tx.netease_id_from_file(audio_path)
    hit = tx.best_match(kw, dur, song_id=used_id,
                        name=title_hint or kw, artist=artist_hint)
    if not hit:
        where = f"（ID {used_id} 也没取到）" if used_id else ""
        return None, [], f"多源均未搜到「{kw}」{where}，且没有参考文件"

    p = config.work_dir() / (audio_path.stem[:40] + ".ref.lrc")
    merged = tx.merge_tlyric(hit["lrc"], hit.get("tlyric") or "")
    p.write_text(merged, encoding="utf-8")
    stamped = tx.lines_with_times(merged)
    lines = [t for _, t in stamped] if stamped else tx.ref_lines(p)
    src_lbl = hit.get("source") or "网易云"
    return p, lines, f"{src_lbl} {hit['artist']} - {hit['name']} ({hit['duration']}s)"


def _is_coarse_timestamps(stamped: list[tuple[float, str]]) -> bool:
    """判断参考 LRC 是否为粗糙/统一小数时间戳（>=70% 共享同一百分秒尾数，如全 .000 或重复偏移后的 .650/.300）。"""
    if len(stamped) < 4:
        return False
    buckets: dict[int, int] = {}
    for t, _ in stamped:
        frac = int(round((t - int(t)) * 100)) % 100
        buckets[frac] = buckets.get(frac, 0) + 1
    top = max(buckets.values())
    return (top / len(stamped)) >= 0.70


def line_times(ref_path: Path | None, lines: list[str], audio_path: Path,
               cache: Path, lang: str,
               ignore_ref_times: bool = False) -> list[tuple[float, str]]:
    """每句的起始时间：参考 LRC 自带精确时间戳优先，粗糙整秒或强制忽略时走 ASR 重定位。"""
    rough, _ = line_times_detail(ref_path, lines, audio_path, cache, lang,
                                 ignore_ref_times=ignore_ref_times)
    return rough


def line_times_detail(ref_path: Path | None, lines: list[str], audio_path: Path,
                      cache: Path, lang: str,
                      ignore_ref_times: bool = False) -> tuple[list[tuple[float, str]], list[dict]]:
    """返回 (每句起始时间, 被剔除的剪辑版缺失行)。"""
    if ref_path and not ignore_ref_times:
        stamped = tx.lines_with_times(Path(ref_path).read_text(encoding="utf-8-sig"))
        if len(stamped) >= max(6, len(lines) - 2) and not _is_coarse_timestamps(stamped):
            return stamped, []

    from . import asr

    audio = au.decode(audio_path, 16000)
    words, segs, dur = asr.transcribe(audio, lang=lang)
    (cache / "asr.json").write_text(
        json.dumps({"words": words, "segments": segs, "duration": dur}, ensure_ascii=False),
        encoding="utf-8")
    return asr.assign_lines_detail(lines, segs)


def meta_from_ref(ref_path: Path | None) -> dict[str, str]:
    """读参考 LRC 的 [ti:]/[ar:]/[al:]，用于补全调用方没给的头部信息。"""
    out: dict[str, str] = {}
    if not ref_path or not Path(ref_path).exists():
        return out
    for raw in Path(ref_path).read_text(encoding="utf-8-sig").splitlines():
        m = re.match(r"^\[(ti|ar|al):(.*)\]$", raw.strip(), re.I)
        if m and m.group(2).strip():
            out[m.group(1).lower()] = m.group(2).strip()
    return out


def _is_enhanced_ref(ref_path: Path | None) -> bool:
    """判断显式传入的参考文件本身是否为逐字 LRC（>=50% 歌词行含 <mm:ss.xxx>）。"""
    if not ref_path or not Path(ref_path).exists():
        return False
    body = Path(ref_path).read_text(encoding="utf-8-sig")
    lrc = fmt.parse(body)
    valid = [ln for ln in lrc.lines if ln.text.strip() and not tx.CREDIT_RE.match(ln.text.strip())]
    if len(valid) < 4:
        return False
    enh = sum(1 for ln in valid if ln.chars and len(ln.chars) >= 2)
    return (enh / len(valid)) >= 0.50


def _transfer_enhanced_lrc(ref_path: Path, vocals: Path, total: float) -> tuple[fmt.Lrc, str]:
    """将原唱逐字 LRC 的逐字节奏迁移到目标音频（Live 版 / 翻唱版）：
    1) 用目标人声轨 RMS 包络与原唱模板活动掩码做全局互相关，秒级锁定主偏移 global_shift；
    2) 每句在 global_shift 附近做小范围局部相关性微调（吸收 Live 抢拍/拖拍与间奏微差）；
    3) 自动清理原唱 YRC 首字吃前奏静音（如 5s）或末字吃长间奏静音（如 18s）的瑕疵。"""
    import numpy as np

    hop = 0.02
    voc_16k = au.decode(vocals, 16000)
    env = au.rms_envelope(voc_16k, sr=16000, hop=hop)
    n_env = len(env)
    p80 = float(np.percentile(env, 80)) if n_env else 0.1
    act_th = max(0.02, p80 * 0.20)
    voc_act = (env >= act_th).astype(np.float32)

    src = fmt.parse(Path(ref_path).read_text(encoding="utf-8-sig"))
    src_lines = [
        ln for ln in src.lines
        if ln.text.strip() and not tx.CREDIT_RE.match(ln.text.strip())
    ]

    # 先清理每句的合理有效时间跨度（去掉 YRC 常见首字吃 5s 前奏 / 末字吃 18s 间奏）
    clean_spans: list[tuple[float, float]] = []
    for ln in src_lines:
        cs = ln.chars or [(ln.text, ln.start)]
        t_s = cs[0][1]
        if len(cs) >= 2 and (cs[1][1] - cs[0][1]) > 1.8:
            t_s = cs[1][1] - 0.35
        t_e = ln.end if ln.end is not None else cs[-1][1] + 0.5
        if (t_e - cs[-1][1]) > 4.0:
            t_e = cs[-1][1] + 1.5
        clean_spans.append((max(0.0, t_s), max(t_s + 0.2, t_e)))

    # 构建模板有声掩码，与目标人声活动做 [-30s, +30s] 互相关找全局最优偏移
    max_t = max(total, max((e for _, e in clean_spans), default=0.0)) + 32.0
    m_len = int(max_t / hop) + 1
    tmpl = np.zeros(m_len, dtype=np.float32)
    for ts, te in clean_spans:
        i0 = max(0, int(ts / hop))
        i1 = min(m_len, int(te / hop))
        tmpl[i0:i1] = 1.0

    # 粗搜 0.1s 步长 [-30s, +30s]，再精搜 0.02s 步长 ±0.5s
    def _corr_at(shift_f: int) -> float:
        if shift_f >= 0:
            ln_c = min(n_env - shift_f, m_len)
            if ln_c <= 0:
                return -1.0
            return float(np.dot(voc_act[shift_f: shift_f + ln_c], tmpl[:ln_c]))
        off_t = -shift_f
        ln_c = min(n_env, m_len - off_t)
        if ln_c <= 0:
            return -1.0
        return float(np.dot(voc_act[:ln_c], tmpl[off_t: off_t + ln_c]))

    max_shift_f = int(30.0 / hop)
    best_f = 0
    best_c = -1.0
    for sf in range(-max_shift_f, max_shift_f + 1, 5):
        c = _corr_at(sf)
        if c > best_c:
            best_c = c
            best_f = sf
    for sf in range(best_f - 25, best_f + 26):
        c = _corr_at(sf)
        if c > best_c:
            best_c = c
            best_f = sf
    global_shift = best_f * hop

    # 逐句在 global_shift 附近做 ±0.35s 局部相关性微调，并清理首/尾静音瑕疵
    out_lines: list[fmt.Line] = []
    for idx, ln in enumerate(src_lines):
        ts, te = clean_spans[idx]
        win_len = max(10, int((te - ts) / hop))
        seg_tmpl = np.ones(win_len, dtype=np.float32)
        base_f = int(round((ts + global_shift) / hop))
        best_lf = 0
        best_lc = -1.0
        for df in range(-17, 18):  # ±0.34s
            p0 = base_f + df
            if p0 < 0 or p0 + win_len > n_env:
                continue
            # 偏好紧贴真实起音：开头 0.15s 有声、开头前 0.10s 尽量安静
            sc = float(np.dot(voc_act[p0: p0 + win_len], seg_tmpl))
            if p0 >= 4:
                sc -= 0.35 * float(voc_act[p0 - 4: p0].sum())
            sc -= 0.015 * abs(df)  # 轻微偏向 global_shift，防止无依据漂移
            if sc > best_lc:
                best_lc = sc
                best_lf = df
        shift = global_shift + best_lf * hop

        new_chars: list[tuple[str, float]] = []
        if ln.chars:
            for j, (ch, ct) in enumerate(ln.chars):
                nt = max(0.0, ct + shift)
                # 清理 YRC 首字吃超长前奏（如第 1 句 11.37 -> 16.59）
                if j == 0 and len(ln.chars) >= 2 and (ln.chars[1][1] - ct) > 1.8:
                    nt = max(0.0, (ln.chars[1][1] - 0.35) + shift)
                new_chars.append((ch, round(nt, 3)))
            new_start = new_chars[0][1]
        else:
            new_start = round(max(0.0, ln.start + shift), 3)

        raw_end = (ln.end + shift) if ln.end is not None else (new_chars[-1][1] + 0.5 if new_chars else new_start + 1.0)
        # 清理 YRC 末字吃超长间奏（如 18s）
        last_char_t = new_chars[-1][1] if new_chars else new_start
        if (raw_end - last_char_t) > 3.5:
            # 沿目标人声包络找到真实衰减结束点（不超过 3.0s，避开 Live 观众欢呼/伴奏残留）
            k0 = max(0, int(last_char_t / hop))
            k_lim = min(n_env, int(min(total, raw_end, last_char_t + 3.0) / hop))
            sil_run = 0
            cut_k = min(k_lim, k0 + int(1.2 / hop))
            for k in range(k0, k_lim):
                if env[k] < act_th:
                    sil_run += 1
                    if sil_run >= 8:  # 0.16s 静音
                        cut_k = max(k0 + 5, k - 6)
                        break
                else:
                    sil_run = 0
                    cut_k = k + 2
            raw_end = cut_k * hop

        new_end = round(min(total, max(last_char_t + 0.08, raw_end)), 3)
        out_lines.append(fmt.Line(new_start, ln.text, new_chars, new_end))

    # 句间防重叠夹断（同时确保末字起点也不越过下一行行首）
    for i in range(len(out_lines) - 1):
        cap = round(out_lines[i + 1].start - 0.02, 3)
        if out_lines[i].chars:
            for j in range(len(out_lines[i].chars)):
                ch, ct = out_lines[i].chars[j]
                if ct > cap - 0.04:
                    out_lines[i].chars[j] = (ch, round(max(out_lines[i].start, cap - 0.04), 3))
        if out_lines[i].end is not None and out_lines[i].end > cap:
            out_lines[i].end = max(round(out_lines[i].start + 0.04, 3), cap)

    return fmt.Lrc([], out_lines), f"原唱逐字模板迁移 (全局偏移 {global_shift:+.2f}s，{len(out_lines)} 行)"


def _jp_wall_collision_ratio(lrc: fmt.Lrc) -> tuple[int, int]:
    """检测非中文逐字 LRC 是否因参考时间戳整体偏早/失配（如 CD 版 LRC 配 Live 版音频）
    导致大量句子尾部多字被挤扁在切片右边界（<=60ms）。"""
    hit = 0
    total = 0
    for ln in lrc.lines:
        cs = [(ch, t) for ch, t in (ln.chars or []) if ch.strip()]
        if len(cs) < 4:
            continue
        total += 1
        end_t = ln.end if ln.end is not None else cs[-1][1] + 0.1
        durs = [
            (cs[i + 1][1] if i + 1 < len(cs) else end_t) - cs[i][1]
            for i in range(len(cs))
        ]
        # 末尾 4 字中至少 2 字被挤成 <= 65ms（不含末字被 restore_sustain 拉长的情况）
        tail_durs = durs[-4:-1] if len(durs) >= 4 else durs[-3:]
        if sum(1 for d in tail_durs if d <= 0.065) >= 2:
            hit += 1
    return hit, total


def _metas(ti: str, ar: str, al: str, by: str) -> list[str]:
    """统一的头部：ti / ar / al / by，空字段跳过。"""
    return [x for x in (f"[ti:{ti}]" if ti else "", f"[ar:{ar}]" if ar else "",
                        f"[al:{al}]" if al else "", f"[by:{by}]") if x]


def _detect_lyrics_lang(lines: list[str], fallback: str = "zh") -> str:
    """根据实际歌词文本自动纠正语言（如中文歌名「(英文翻唱)」实际唱英文时自动切到 en 走 Echora-300M MMS）。"""
    if not lines:
        return fallback
    kana = sum(1 for ln in lines for ch in ln if "\u3040" <= ch <= "\u30ff")
    han = sum(1 for ln in lines for ch in ln if "\u4e00" <= ch <= "\u9fff")
    latin = sum(1 for ln in lines for ch in ln if ("a" <= ch.lower() <= "z"))
    if kana >= 8:
        return "ja"
    if fallback == "zh" and latin >= 20 and latin > han * 2:
        return "en"
    return fallback


def _norm_lyric_key(s: str) -> str:
    """归一化歌词键：去掉【合】/【歌手】标签与空白，提升译文匹配率。"""
    s = re.sub(r"【[^】]*】", "", s)
    return re.sub(r"\s+", "", s).strip().lower()


def _build_trans_map_from_text(body: str) -> dict[str, list[list[str]]]:
    orig, trans = tx.lines_with_translations(body)
    buckets: dict[str, list[list[str]]] = {}
    for (_, txt), trs in zip(orig, trans):
        key = _norm_lyric_key(txt)
        if key:
            buckets.setdefault(key, []).append(trs)
    return buckets


def _has_any_trans(trans_map: dict[str, list[list[str]]]) -> bool:
    return any(bool(trs) for q in trans_map.values() for trs in q)


def _extract_trans_map(ref_path: Path | None) -> dict[str, list[list[str]]]:
    """从参考 LRC 中提取「原词 -> 译文列表」映射（按出现顺序队列化，支持副歌重复行）。"""
    if not ref_path or not Path(ref_path).exists():
        return {}
    body = Path(ref_path).read_text(encoding="utf-8-sig")
    return _build_trans_map_from_text(body)


def _fetch_missing_translations(audio_path: Path, total: float, query: str | None = None) -> dict[str, list[list[str]]]:
    """外语歌曲本地 LRC 缺少中文译文时，按顺序从网易云 → QQ音乐拉取中文译文补全。"""
    try:
        kw = query or tx.query_from_filename(audio_path.stem)
        dur = round(total)
        for fn in (
            lambda: tx.netease_match(kw, dur, interval=1.0),
            lambda: tx.qq_match(kw, dur),
        ):
            hit = fn()
            if hit and (hit.get("tlyric") or "").strip():
                merged = tx.merge_tlyric(hit["lrc"], hit["tlyric"])
                tmap = _build_trans_map_from_text(merged)
                if _has_any_trans(tmap):
                    return tmap
        return {}
    except Exception:
        return {}


def _attach_translations(lrc: fmt.Lrc, trans_map: dict[str, list[list[str]]]) -> int:
    """把原双语 LRC 的译文行按对齐后的行首时间戳贴回逐字 LRC，返回贴回的译文行数。"""
    if not trans_map:
        return 0
    out: list[fmt.Line] = []
    added = 0
    for ln in lrc.lines:
        out.append(ln)
        key = _norm_lyric_key(ln.text)
        q = trans_map.get(key)
        if q:
            trs = q.pop(0)
            for tr in trs:
                if tr.strip():
                    out.append(fmt.Line(ln.start, tr.strip()))
                    added += 1
    lrc.lines = out
    return added


def ensure_cover(audio_path: str | Path, cache: Path | None = None,
                 query: str | None = None) -> Path | None:
    """提取或补全专辑封面到 work/<cache>/cover.jpg：
    1. 已有缓存 cover.jpg 直接复用
    2. 尝试读取音频内嵌封面（FLAC/MP3/M4A）或同目录封面图
    3. 都没有时自动从网易云拉取专辑封面补上
    """
    audio_path = Path(audio_path)
    if cache is None:
        cache = _cache_dir(audio_path)
    dst = cache / "cover.jpg"
    if dst.exists() and dst.stat().st_size > 1024:
        try:
            if not audio_path.exists() or audio_path.stat().st_mtime <= dst.stat().st_mtime:
                return dst
        except OSError:
            return dst

    # 1) 同目录同名/通用封面图
    for cand in (
        audio_path.with_suffix(".jpg"),
        audio_path.with_suffix(".png"),
        audio_path.parent / "cover.jpg",
        audio_path.parent / "cover.png",
        audio_path.parent / "folder.jpg",
    ):
        if cand.exists() and cand.stat().st_size > 1024:
            shutil.copyfile(cand, dst)
            return dst

    # 2) 音频内嵌封面（mutagen）
    try:
        import mutagen
        mf = mutagen.File(str(audio_path))
        img_bytes = None
        if mf is not None:
            pics = getattr(mf, "pictures", None)
            if pics:
                img_bytes = pics[0].data
            elif getattr(mf, "tags", None):
                for k, v in mf.tags.items():
                    if k.startswith("APIC") and hasattr(v, "data"):
                        img_bytes = v.data
                        break
                    if k == "covr" and v:
                        img_bytes = bytes(v[0])
                        break
        if img_bytes and len(img_bytes) > 512:
            dst.write_bytes(img_bytes)
            return dst
    except Exception:
        pass

    # 3) 音频无内嵌封面 -> 自动从网易云补全
    try:
        import urllib.request
        kw = query or tx.query_from_filename(audio_path.stem)
        dur = round(au.duration(audio_path)) if audio_path.exists() else None
        songs = tx.netease_search(kw, limit=5)
        if songs:
            if dur:
                songs.sort(key=lambda s: abs(s["duration"] - dur))
            sid = songs[0]["id"]
            detail = tx._get_json(f"https://music.163.com/api/song/detail/?id={sid}&ids=[{sid}]")
            pic = ((detail or {}).get("songs") or [{}])[0].get("album", {}).get("picUrl", "")
            if pic:
                url = pic + ("?param=500y500" if "?" not in pic else "")
                req = urllib.request.Request(url, headers=tx.HEADERS)
                with urllib.request.urlopen(req, timeout=15) as r:
                    data = r.read()
                if len(data) > 1024:
                    dst.write_bytes(data)
                    return dst
    except Exception:
        pass
    return None


def make_lrc(audio_path: str | Path, ref: str | Path | None = None, lang: str = "zh",
             query: str | None = None, ti: str = "", ar: str = "",
             al: str = "", force_vocal: bool = False,
             netease_id: int | None = None,
             asr_fallback: bool = False,
             ignore_ref_times: bool = False,
             en_granularity: str | None = None,
             restore_long_vowel: bool | None = None,
             waveform_first: bool = False,
             on_progress: Callable[[int, int, str, str], None] | None = None) -> tuple[fmt.Lrc, str]:
    def emit(step: int, stage: str, detail: str = "") -> None:
        if on_progress is not None:
            on_progress(step, 6, stage, detail)

    audio_path = Path(audio_path)
    cache = _cache_dir(audio_path)
    total = au.duration(audio_path)

    emit(1, "确定歌词文本", audio_path.name)
    explicit_ref = Path(ref) if ref else None
    ref_path, lines, note = resolve_reference(audio_path, explicit_ref, query,
                                               netease_id=netease_id)
    used_asr_fallback = False
    used_ref_times = False
    vocals: Path | None = None
    trans_map: dict[str, list[str]] = {}

    # 若用户显式传入的是「原唱逐字 LRC」（含 <mm:ss.xxx>），且未强制忽略参考时间戳，
    # 直接走「原唱逐字模板节奏迁移」（适用 Live 版 / 翻唱版，秒级精准对齐全语种）
    if explicit_ref and not ignore_ref_times and _is_enhanced_ref(explicit_ref):
        trans_map = _extract_trans_map(explicit_ref)
        emit(3, "demucs 人声分离", "htdemucs")
        vocals = _vocals(audio_path, cache)
        emit(4, "主唱检测", vocals.name)
        if not force_vocal:
            act = au.vocal_activity(vocals)
            if not act["has_vocal"]:
                raise NoVocalError(f"无主唱 ({act['reason']})")
        emit(5, "原唱逐字模板迁移", explicit_ref.name)
        lrc, tr_note = _transfer_enhanced_lrc(explicit_ref, vocals, total)
        note = f"{note} | {tr_note}"
        by = "原唱逐字模板迁移"
        emit(6, "输出并校验", f"{len(lrc.lines)} 行")
        tr_cnt = _attach_translations(lrc, trans_map)
        if tr_cnt:
            note += f" | 保留译文 {tr_cnt} 行"
        ensure_cover(audio_path, cache, query)
        ref_meta = meta_from_ref(ref_path)
        lrc.metas = _metas(ti or ref_meta.get("ti", "") or audio_path.stem,
                           ar or ref_meta.get("ar", ""),
                           al or ref_meta.get("al", ""), by)
        return lrc, note

    if not lines:
        if not asr_fallback:
            raise NoLyricsError(note)
        # 显式开启 --asr-fallback：先分离干净人声并做主唱检测，再听写
        used_asr_fallback = True
        emit(3, "demucs 人声分离", "htdemucs")
        vocals = _vocals(audio_path, cache)

        emit(4, "主唱检测", vocals.name)
        if not force_vocal:
            act = au.vocal_activity(vocals)
            if not act["has_vocal"]:
                raise NoVocalError(f"无主唱 ({act['reason']})")

        from . import asr

        voc_16k = au.decode(vocals, 16000)
        asr_engine_name = "ASR"
        if lang == "zh" and asr.firered_available():
            emit(2, "本地 ASR 人声听写", "FireRedASR-AED-L (中文专精)")
            words, segs, dur = asr.transcribe_zh_firered(voc_16k)
            asr_engine_name = "FireRedASR"
            rough = [
                (round(float(s["start"]), 3), str(s.get("text") or "").strip())
                for s in segs
                if str(s.get("text") or "").strip() and not tx.CREDIT_RE.match(str(s.get("text") or "").strip())
            ]
        else:
            emit(2, "本地 ASR 人声听写", f"faster-whisper ({lang})")
            words, segs, dur = asr.transcribe(voc_16k, lang=lang)
            rough = asr.segments_to_fallback_lines(words, segs)
        (cache / "asr.json").write_text(
            json.dumps({"words": words, "segments": segs, "duration": dur}, ensure_ascii=False),
            encoding="utf-8",
        )
        if not rough:
            raise RuntimeError("本地 ASR 未在人声轨中识别到有效歌词")
        lines = [t for _, t in rough]
        note = f"本地 {asr_engine_name} 人声听写 ({len(rough)} 句)"
        detected = _detect_lyrics_lang(lines, lang)
        if detected != lang:
            note += f" | 歌词语言自动识别为 {detected}"
            lang = detected
    else:
        trans_map = _extract_trans_map(ref_path)
        detected = _detect_lyrics_lang(lines, lang)
        if detected != lang:
            note += f" | 歌词语言自动识别为 {detected}"
            lang = detected
        # 外语歌曲（日文/英文）如果本地参考 LRC 没有中文译文，自动去网易云拉取 tlyric 补全
        if lang in ("ja", "en") and not _has_any_trans(trans_map):
            fetched = _fetch_missing_translations(audio_path, total, query)
            if _has_any_trans(fetched):
                trans_map = fetched
                note += " | 自动补全网易云中文译文"

        emit(2, "确定每句位置", f"{len(lines)} 行" + (" · 强制 ASR 重定位" if ignore_ref_times else ""))
        if ref_path and not ignore_ref_times:
            _stamped = tx.lines_with_times(Path(ref_path).read_text(encoding="utf-8-sig"))
            used_ref_times = len(_stamped) >= max(6, len(lines) - 2) and not _is_coarse_timestamps(_stamped)
        rough, skipped = line_times_detail(ref_path, lines, audio_path, cache, lang,
                                           ignore_ref_times=ignore_ref_times)
        if not rough:
            raise RuntimeError("无法确定每句的起始时间")
        rough = [(t, l) for t, l in rough if not tx.CREDIT_RE.match(l)]
        if skipped:
            note += f" | 剔除剪辑缺失 {len(skipped)} 行"

        emit(3, "demucs 人声分离", "htdemucs")
        vocals = _vocals(audio_path, cache)

        emit(4, "主唱检测", vocals.name)
        if not force_vocal:
            act = au.vocal_activity(vocals)
            if not act["has_vocal"]:
                raise NoVocalError(f"无主唱 ({act['reason']})")

    if lang == "zh":
        from . import sofa

        emit(5, "SOFA 逐句对齐", f"{len(rough)} 句")
        seg_dir = cache / "segments"
        meta, missing = sofa.prep_segments(vocals, rough, seg_dir, total)
        if missing:
            note += f" | 拼音异常 {len(missing)} 处"
        sofa.run(seg_dir)
        if used_ref_times:
            clipped, seg_cnt = sofa.clipped_start_ratio(seg_dir / "htk" / "words")
            if seg_cnt >= 6 and (clipped / seg_cnt) >= 0.40:
                emit(5, "参考时间戳偏慢，自动校准偏移", f"{clipped}/{seg_cnt} 句开头被裁切")
                from . import asr
                import numpy as np

                voc_16k = au.decode(vocals, 16000)
                words_r, segs_r, dur_r = asr.transcribe(voc_16k, lang=lang)
                (cache / "asr.json").write_text(
                    json.dumps({"words": words_r, "segments": segs_r, "duration": dur_r}, ensure_ascii=False),
                    encoding="utf-8",
                )
                orig_texts = [l for _, l in rough]
                matched_r, skipped_r = asr.assign_lines_detail(orig_texts, segs_r)
                skipped_idx = {s["index"] - 1 for s in skipped_r}
                # 建立原始行号 -> ASR 匹配时间映射，计算全局中位偏移（保留全部歌词行，绝不删词）
                asr_by_idx: dict[int, float] = {}
                mi = 0
                for idx in range(len(rough)):
                    if idx not in skipped_idx and mi < len(matched_r):
                        asr_by_idx[idx] = matched_r[mi][0]
                        mi += 1
                deltas = [asr_by_idx[i] - rough[i][0] for i in asr_by_idx]
                if deltas:
                    shift = float(np.median(deltas))
                    if abs(shift) >= 0.25:
                        rough_fixed: list[tuple[float, str]] = []
                        for idx, (ref_t, txt) in enumerate(rough):
                            # 保持参考 LRC 原有的句间相对节奏，统一按全局中位偏移平移，
                            # 避免 Whisper 分段起点包含前一句尾音/换气静音导致首字抢占上一句尾音。
                            t_new = max(0.0, ref_t + shift)
                            if rough_fixed and t_new < rough_fixed[-1][0]:
                                t_new = rough_fixed[-1][0] + 0.05
                            rough_fixed.append((t_new, txt))
                        rough = rough_fixed
                        meta, missing = sofa.prep_segments(vocals, rough, seg_dir, total)
                        sofa.run(seg_dir)
                        note += f" | 参考时间戳整体偏移 {shift:+.2f}s，已自动校准({len(rough)}行保留)"
        lrc = sofa.build_lrc(meta, seg_dir / "htk" / "words", vocal_wav=vocals,
                             waveform_first=waveform_first)
        by = "SOFA 波形优先对齐" if waveform_first else "SOFA 歌声对齐"
    else:
        from . import jp

        stage_lbl = "MMS 日语歌声" if lang == "ja" else ("MMS 英文歌声" if lang == "en" else f"MMS 多语歌声 ({lang})")
        emit(5, f"{stage_lbl} 逐句对齐", f"{len(rough)} 句")
        lrc = jp.align_lines(str(vocals), rough, lang=lang, total=total,
                             en_granularity=en_granularity,
                             restore_long_vowel=restore_long_vowel)
        if used_ref_times:
            col_hit, col_tot = _jp_wall_collision_ratio(lrc)
            if col_tot >= 6 and (col_hit / col_tot) >= 0.30:
                emit(5, "参考时间戳失配，自动 ASR 重定位", f"{col_hit}/{col_tot} 句尾部被截断挤扁")
                from . import asr

                voc_16k = au.decode(vocals, 16000)
                words_r, segs_r, dur_r = asr.transcribe(voc_16k, lang=lang)
                (cache / "asr.json").write_text(
                    json.dumps({"words": words_r, "segments": segs_r, "duration": dur_r}, ensure_ascii=False),
                    encoding="utf-8",
                )
                orig_texts = [l for _, l in rough]
                rough2, _ = asr.assign_lines_detail(orig_texts, segs_r, min_coverage=0.0)
                if rough2 and len(rough2) == len(rough):
                    import numpy as np
                    # 若 Whisper 把两句合并成同一分段（导致 t_i - t_{i-1} < 1.2s 而参考间隔 >= 2.0s），
                    # 用前一句的局部偏移 + 参考原始句间间隔还原，避免把前一句压扁成 0.8s
                    deltas = [
                        rough2[i][0] - rough[i][0]
                        for i in range(len(rough))
                        if i == 0 or (rough2[i][0] - rough2[i - 1][0]) >= 1.2
                    ]
                    med_shift = float(np.median(deltas)) if deltas else 0.0
                    repaired: list[tuple[float, str]] = []
                    for i, (t_asr, txt) in enumerate(rough2):
                        t_ref = rough[i][0]
                        if i > 0 and (t_asr - repaired[i - 1][0]) < 1.2 and (t_ref - rough[i - 1][0]) >= 2.0:
                            local_shift = repaired[i - 1][0] - rough[i - 1][0]
                            t_asr = max(repaired[i - 1][0] + 1.0, t_ref + local_shift)
                        elif i > 0 and t_asr < repaired[i - 1][0] + 0.05:
                            t_asr = max(repaired[i - 1][0] + 0.5, t_ref + med_shift)
                        repaired.append((t_asr, txt))
                    rough = repaired
                    lrc = jp.align_lines(str(vocals), rough, lang=lang, total=total,
                                         en_granularity=en_granularity,
                                         restore_long_vowel=restore_long_vowel)
                    note += f" | 参考时间戳失配({col_hit}/{col_tot}句撞墙)，已自动 ASR 重定位"
        by = "MMS 日语歌声逐字" if lang == "ja" else ("MMS 英文歌声逐字" if lang == "en" else f"MMS 多语歌声逐字 ({lang})")

    if used_asr_fallback:
        by = f"{asr_engine_name} 听写 + {by}"

    if not lrc.lines:
        raise RuntimeError(f"逐字对齐未产出有效歌词行（lang={lang}），已中止以免写入空 LRC")

    emit(6, "输出并校验", f"{len(lrc.lines)} 行")
    tr_cnt = _attach_translations(lrc, trans_map)
    if tr_cnt:
        note += f" | 保留译文 {tr_cnt} 行"
    # 顺手提取/补全专辑封面到缓存，供校对页背景展示
    ensure_cover(audio_path, cache, query)
    ref_meta = meta_from_ref(ref_path)
    lrc.metas = _metas(ti or ref_meta.get("ti", "") or audio_path.stem,
                       ar or ref_meta.get("ar", ""),
                       al or ref_meta.get("al", ""), by)
    return lrc, note


def scan_library(folder: str | Path) -> list[dict]:
    """扫描音频目录，统计每首音频的歌词状态（自动识别并排除纯音乐 / OST / 伴奏）。"""
    root = Path(folder)
    if not root.exists():
        return []
    base = root.parent if root.is_file() else root
    files = [root] if root.is_file() else sorted(
        p for p in root.rglob("*") if p.is_file() and p.suffix.lower() in AUDIO_EXTS
    )
    items: list[dict] = []
    for audio in files:
        lrc_path = audio.with_suffix(".lrc")
        raw = lrc_path.read_text(encoding="utf-8-sig", errors="ignore") if lrc_path.exists() else ""
        inst_kind = classify_instrumental(audio, raw, root=base)

        status = "missing"
        lines_total = 0
        lines_enh = 0
        problems: list[str] = []

        if inst_kind is not None:
            status = "instrumental"
        elif lrc_path.exists():
            try:
                parsed = fmt.parse(str(lrc_path))
                lines_total = len(parsed.lines)
                lines_enh = sum(1 for ln in parsed.lines if ln.enhanced)
                if lines_enh > 0:
                    all_probs = fmt.validate_enhanced(str(lrc_path))
                    problems = [
                        x for x in all_probs
                        if "CRLF" not in x and "可能是长音" not in x
                    ]
                    status = "enhanced"
                elif lines_total > 0:
                    status = "line_only"
            except Exception as e:
                status = "needs_fix"
                problems = [str(e)]
        items.append({
            "audio": str(audio),
            "name": audio.stem,
            "lrc": str(lrc_path),
            "status": status,
            "inst_kind": inst_kind or "",
            "lines": lines_total,
            "enhanced_lines": lines_enh,
            "problems": problems,
        })
    return items


def backup_lrc_once(path: str | Path) -> Path | None:
    """首次覆盖已有 .lrc 前自动备份为 .lrc.bak（若已存在则不覆盖，永远保留最初原版）。"""
    p = Path(path)
    bak = p.with_name(p.name + ".bak")
    if p.exists() and not bak.exists():
        shutil.copy2(p, bak)
        return bak
    return None


def batch_align(folder: str | Path, lang: str = "zh", jobs: int = 1,
                skip_existing: bool = True, force_vocal: bool = False,
                en_granularity: str | None = None,
                on_song: Callable[[int, int, Path, str, str], None] | None = None) -> dict:
    """批量补齐目录下的逐字 LRC，单首失败或无主唱不中断（KI-4）。"""
    entries = scan_library(folder)
    total = len(entries)
    summary = {
        "total": total,
        "done": 0,
        "skipped_existing": 0,
        "skipped_novocal": 0,
        "skipped_nolyrics": 0,
        "failed": 0,
        "results": [],
    }
    if total == 0:
        return summary

    def process_one(idx: int, entry: dict) -> dict:
        audio = Path(entry["audio"])
        out = Path(entry["lrc"])
        if entry["status"] == "instrumental":
            msg = f"已排除 ({entry.get('inst_kind') or '纯音乐/OST/伴奏'})"
            if on_song:
                on_song(idx, total, audio, "skipped_novocal", msg)
            return {"audio": str(audio), "status": "skipped_novocal", "note": msg}
        if skip_existing and entry["status"] == "enhanced":
            msg = f"已是逐字 ({entry['enhanced_lines']} 行)"
            if on_song:
                on_song(idx, total, audio, "skipped_existing", msg)
            return {"audio": str(audio), "status": "skipped_existing", "note": msg}

        ref = out if (out.exists() and entry["status"] == "line_only") else None
        try:
            lrc, note = make_lrc(
                audio, ref=ref, lang=lang, ti=audio.stem,
                force_vocal=force_vocal, en_granularity=en_granularity,
            )
            backup_lrc_once(out)
            fmt.write(out, lrc)
            if on_song:
                on_song(idx, total, audio, "done", note)
            return {"audio": str(audio), "status": "done", "lines": len(lrc.lines), "note": note}
        except NoVocalError as e:
            if on_song:
                on_song(idx, total, audio, "skipped_novocal", str(e))
            return {"audio": str(audio), "status": "skipped_novocal", "note": str(e)}
        except NoLyricsError as e:
            if on_song:
                on_song(idx, total, audio, "skipped_nolyrics", str(e))
            return {"audio": str(audio), "status": "skipped_nolyrics", "note": str(e)}
        except Exception as e:
            if on_song:
                on_song(idx, total, audio, "failed", str(e))
            return {"audio": str(audio), "status": "failed", "note": str(e)}

    if jobs <= 1:
        results = [process_one(i + 1, ent) for i, ent in enumerate(entries)]
    else:
        with concurrent.futures.ThreadPoolExecutor(max_workers=jobs) as pool:
            futs = [pool.submit(process_one, i + 1, ent) for i, ent in enumerate(entries)]
            results = [f.result() for f in futs]

    for r in results:
        st = r["status"]
        if st in summary:
            summary[st] += 1
        summary["results"].append(r)
    return summary


def cache_stats(work: Path | None = None) -> dict:
    """统计 work/ 缓存占用（KI-6）。"""
    d = work or config.work_dir()
    if not d.exists():
        return {"count": 0, "bytes": 0, "items": []}
    items = []
    total_bytes = 0
    for child in sorted(d.iterdir()):
        if child.name.startswith("."):
            continue
        if child.is_dir():
            sz = sum(f.stat().st_size for f in child.rglob("*") if f.is_file())
            mt = max((f.stat().st_mtime for f in child.rglob("*") if f.is_file()),
                     default=child.stat().st_mtime)
        else:
            sz = child.stat().st_size
            mt = child.stat().st_mtime
        total_bytes += sz
        items.append({"path": str(child), "name": child.name, "bytes": sz, "mtime": mt})
    return {"count": len(items), "bytes": total_bytes, "items": items}


def clean_cache(days: float = 0.0, dry_run: bool = False,
                work: Path | None = None) -> dict:
    """清理 work/ 下超过 days 天的中间产物缓存（days<=0 清理全部，KI-6）。"""
    st = cache_stats(work)
    now = time.time()
    cutoff = now - max(0.0, days) * 86400.0
    removed = 0
    freed = 0
    paths: list[str] = []
    for it in st["items"]:
        if days > 0 and it["mtime"] > cutoff:
            continue
        p = Path(it["path"])
        if not dry_run:
            if p.is_dir():
                shutil.rmtree(p, ignore_errors=True)
            elif p.exists():
                p.unlink()
        removed += 1
        freed += it["bytes"]
        paths.append(it["name"])
    return {"removed": removed, "freed_bytes": freed, "paths": paths, "dry_run": dry_run}
