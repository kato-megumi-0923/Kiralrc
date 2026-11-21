"""非中文语言（以日文为主）的逐字对齐：WhisperX wav2vec2 CTC，逐句在**人声轨**上跑。

SOFA 只有中文歌声模型；日文/英文用 WhisperX。
注意 wav2vec2 是语音模型，长音会被压扁，所以务必喂 demucs 分离后的人声，
并在对齐后用人声能量包络做「时长保真」后处理（KI-3）。
"""
from __future__ import annotations

import re
import sys
import numpy as np

from . import config, format as fmt

WORD_CHAR = re.compile(r"[\w\u3040-\u30ff\u4e00-\u9fff]")
ASCII_ALNUM = re.compile(r"^[A-Za-z0-9]+$")
SPEAKER_TAG_RE = re.compile(r"【[^】]*】")
KANA_CHAR_RE = re.compile(r"^[\u3041-\u3096\u30a1-\u30faー]$")
SMALL_KANA = set("ゃゅょぁぃぅぇぉャュョァィゥェォ")
MMS_JA_MODEL_ID = "NextFire/mms-300m-ForcedAligner-karaoke-ja-Latn"

_warned: set[str] = set()


def _warn(msg: str) -> None:
    """同类警告只打一次，批量跑时不刷屏。"""
    if msg in _warned:
        return
    _warned.add(msg)
    print(f"[warn] {msg}", file=sys.stderr, flush=True)


def _kata_to_hira(s: str) -> str:
    return "".join(
        chr(ord(c) - 0x60) if "\u30a1" <= c <= "\u30f6" else c
        for c in s
    )


def _split_ja_units(orig: str, hira: str, kks) -> list[tuple[str, str]]:
    """把 pykakasi 的单个词块拆成更细的 (原文子串, 罗马音) 单元，尽量做到单字对齐。"""
    if not orig:
        return []
    # 纯假名块：按单字拆（若 fugashi 给出了读音差异如助词「は→わ」「へ→え」，用读音转罗马音）
    if all(KANA_CHAR_RE.match(c) for c in orig):
        src_for_rom = hira if (hira and len(hira) == len(orig) and all(KANA_CHAR_RE.match(c) for c in hira)) else orig
        groups: list[tuple[str, str]] = []
        for ch_o, ch_r in zip(orig, src_for_rom):
            if ch_o in SMALL_KANA and groups:
                go, gr = groups[-1]
                groups[-1] = (go + ch_o, gr + ch_r)
            else:
                groups.append((ch_o, ch_r))
        out: list[tuple[str, str]] = []
        prev_v = ""
        for g_o, g_r in groups:
            if g_r == "ー":
                out.append((g_o, prev_v or "a"))
                continue
            rom = "".join(x["hepburn"] for x in kks.convert(g_r)).lower()
            out.append((g_o, rom))
            for ch in reversed(rom):
                if ch in "aeiou":
                    prev_v = ch
                    break
        return out

    # 混合汉字+送假名（如「沈ん」-> 沈+ん、「剥が」-> 剥+が）：剥离首尾与 hira 匹配的假名
    orig_h = _kata_to_hira(orig)
    prefix: list[tuple[str, str]] = []
    suffix: list[tuple[str, str]] = []
    l_o, r_o = 0, len(orig)
    l_h, r_h = 0, len(hira)
    while l_o < r_o and l_h < r_h and KANA_CHAR_RE.match(orig[l_o]) and orig_h[l_o] == hira[l_h]:
        ch = orig[l_o]
        rom = "".join(x["hepburn"] for x in kks.convert(ch)).lower()
        prefix.append((ch, rom))
        l_o += 1
        l_h += 1
    while r_o > l_o and r_h > l_h and KANA_CHAR_RE.match(orig[r_o - 1]) and orig_h[r_o - 1] == hira[r_h - 1]:
        ch = orig[r_o - 1]
        rom = "".join(x["hepburn"] for x in kks.convert(ch)).lower()
        suffix.append((ch, rom))
        r_o -= 1
        r_h -= 1
    suffix.reverse()

    mid_o = orig[l_o:r_o]
    mid_h = hira[l_h:r_h]
    mid: list[tuple[str, str]] = []
    if mid_o:
        rom = "".join(x["hepburn"] for x in kks.convert(mid_h or mid_o)).lower()
        mid.append((mid_o, rom))
    return prefix + mid + suffix


def _align_ja_mms_karaoke(
    audio: np.ndarray,
    lines: list[tuple[float, str]],
    dur: float,
    device: str = "cuda",
) -> list[fmt.Line]:
    """使用 NextFire/mms-300m-ForcedAligner-karaoke-ja-Latn（日语卡拉OK歌声专精模型）做逐字对齐。"""
    import pykakasi
    import torch
    import torchaudio
    from transformers import AutoModelForCTC, AutoTokenizer

    dev = device if (device == "cuda" and torch.cuda.is_available()) else "cpu"
    try:
        tok = AutoTokenizer.from_pretrained(MMS_JA_MODEL_ID, local_files_only=True)
        model = AutoModelForCTC.from_pretrained(MMS_JA_MODEL_ID, local_files_only=True)
    except Exception:
        tok = AutoTokenizer.from_pretrained(MMS_JA_MODEL_ID)
        model = AutoModelForCTC.from_pretrained(MMS_JA_MODEL_ID)
    model = model.to(dev).eval()
    vocab = tok.get_vocab()
    pad_id = tok.pad_token_id if tok.pad_token_id is not None else 29
    kks = pykakasi.kakasi()
    tagger = None
    try:
        import fugashi
        tagger = fugashi.Tagger()
    except Exception:
        tagger = None

    def _tokenize_ja(seg_txt: str) -> list[tuple[str, str]]:
        """返回 [(orig_token, hira_pron), ...]；优先用 fugashi(MeCab+unidic) 精确处理「君=きみ」「は=わ」等语境读音。"""
        if tagger is None:
            return [(it["orig"], it["hira"]) for it in kks.convert(seg_txt)]
        out_toks: list[tuple[str, str]] = []
        cursor = 0
        for w in tagger(seg_txt):
            surf = w.surface
            idx = seg_txt.find(surf, cursor)
            if idx > cursor:
                gap = seg_txt[cursor:idx]
                out_toks.append((gap, gap))
                cursor = idx
            pron = getattr(w.feature, "pron", None) or getattr(w.feature, "kana", None) or ""
            if pron and pron != "*":
                hira = _kata_to_hira(pron)
            else:
                hira = "".join(x["hira"] for x in kks.convert(surf))
            out_toks.append((surf, hira))
            cursor = idx + len(surf) if idx >= 0 else cursor + len(surf)
        if cursor < len(seg_txt):
            tail = seg_txt[cursor:]
            out_toks.append((tail, tail))
        return out_toks

    # 预先计算人声包络，用于避免切片向前吞进上一句未唱完的拖音（如慢歌句尾延长超过参考时间戳）
    from . import audio as au
    env_20ms = au.rms_envelope(audio, sr=16000, hop=0.02)

    rows: list[fmt.Line] = []
    for i, (t0, text) in enumerate(lines):
        nxt = lines[i + 1][0] if i + 1 < len(lines) else dur
        tag_mask = _speaker_tag_mask(text)

        # 按原文字符索引建立 romaji 映射
        rom_chars: list[str] = []
        char_rom_span: dict[int, tuple[int, int]] = {}

        pos = 0
        while pos < len(text):
            if tag_mask[pos]:
                pos += 1
                continue
            seg_end = pos
            while seg_end < len(text) and not tag_mask[seg_end]:
                seg_end += 1
            seg_txt = text[pos:seg_end]
            cur_i = pos
            for orig, hira in _tokenize_ja(seg_txt):
                units = _split_ja_units(orig, hira, kks)
                u_i = cur_i
                for u_orig, u_rom in units:
                    valid_r = [c for c in u_rom.lower() if c in vocab and c != "|"]
                    u_len = len(u_orig)
                    if valid_r:
                        r0 = len(rom_chars)
                        rom_chars.extend(valid_r)
                        r1 = len(rom_chars)
                        # 将本单元的 romaji 区间按字数分配给单元内每个字符
                        for k in range(u_len):
                            c0 = r0 + int((r1 - r0) * k / u_len)
                            c1 = r0 + int((r1 - r0) * (k + 1) / u_len)
                            if c1 <= c0:
                                c1 = min(r1, c0 + 1)
                            char_rom_span[u_i + k] = (c0, c1)
                    u_i += u_len
                cur_i += len(orig)
            pos = seg_end

        if not rom_chars:
            continue

        # 切片范围：绝不往前侵入上一句已对齐的唱腔尾音；若参考起点恰好落在上句拖音中，自动延后到换气低谷
        seg_start = max(0.0, t0 - 0.30)
        if rows and rows[-1].end is not None and rows[-1].end < t0 + 0.35:
            seg_start = max(seg_start, rows[-1].end - 0.05)
        if rows and len(env_20ms) > 0:
            # 检查 [seg_start, t0+0.60] 内是否存在「先高能量(上句尾音) -> 明显换气低谷 -> 再起音」
            f0 = max(0, int(seg_start / 0.02))
            f1 = min(len(env_20ms) - 1, int((t0 + 0.60) / 0.02))
            if f1 > f0 + 6:
                win = env_20ms[f0:f1 + 1]
                # 窗口前段有明显上句尾音能量
                head_peak = float(np.max(win[:min(8, len(win))]))
                min_rel = int(np.argmin(win))
                valley = float(win[min_rel])
                if head_peak > 0.08 and min_rel >= 2 and min_rel < len(win) - 2 and valley < head_peak * 0.48:
                    after_peak = float(np.max(win[min_rel + 1:]))
                    if after_peak > valley * 1.6:
                        seg_start = (f0 + min_rel) * 0.02
        max_line_dur = max(5.0, len(rom_chars) * 0.45) + 2.0
        if i + 1 < len(lines) and nxt > t0 + 0.6:
            seg_end_t = min(dur, nxt + 0.45, t0 + max_line_dur)
        else:
            seg_end_t = min(dur, t0 + max_line_dur)
        s_idx = int(seg_start * 16000)
        e_idx = min(len(audio), int(seg_end_t * 16000))
        seg_wav = audio[s_idx:e_idx]
        if len(seg_wav) < 400:
            continue

        inp = torch.tensor(seg_wav, dtype=torch.float32, device=dev).unsqueeze(0)
        inp = (inp - inp.mean()) / (inp.std() + 1e-7)
        with torch.no_grad():
            logits = model(inp).logits
            em = torch.log_softmax(logits, dim=-1).cpu()

        # 如果音频帧数少于目标 token 数，跳过以免 forced_align 报错
        if em.shape[1] <= len(rom_chars) + 3:
            continue
        # 首尾通配符 * 惩罚设为 -3.2：仅吸收真正的静音/换气/非匹配杂音，绝不吞掉真实唱出的首字（如「君は」）
        star_id = em.shape[-1]
        star_col = torch.full((em.shape[0], em.shape[1], 1), -3.2, dtype=em.dtype)
        em_star = torch.cat([em, star_col], dim=-1)
        targets = torch.tensor([[star_id] + [vocab[c] for c in rom_chars] + [star_id]], dtype=torch.int32)
        aligned, scores = torchaudio.functional.forced_align(em_star, targets, blank=pad_id)
        spans = torchaudio.functional.merge_tokens(aligned[0], scores[0], blank=pad_id)
        if len(spans) == len(rom_chars) + 2:
            spans = spans[1:-1]
        elif len(spans) != len(rom_chars):
            continue

        frame_dur = (len(seg_wav) / 16000.0) / float(em.shape[1])
        first_sung_idx = min(char_rom_span.keys())
        first_t = round(seg_start + spans[char_rom_span[first_sung_idx][0]].start * frame_dur, 3)

        placed: list[tuple[str, float]] = []
        last_end = first_t
        for ci, ch in enumerate(text):
            if tag_mask[ci]:
                placed.append((ch, first_t if ci < first_sung_idx else last_end))
            elif ci in char_rom_span:
                r0, r1 = char_rom_span[ci]
                cs = round(seg_start + spans[r0].start * frame_dur, 3)
                ce = round(seg_start + spans[r1 - 1].end * frame_dur, 3)
                placed.append((ch, cs))
                last_end = max(ce, cs + 0.03)
            else:
                # 空格/标点放在前一演唱字的结束时间，不吞拖音
                placed.append((ch, last_end))

        end_t = max(last_end, placed[-1][1] + 0.08)
        rows.append(fmt.Line(placed[0][1], text, placed, round(end_t, 3)))

    return rows


def _speaker_tag_mask(text: str) -> list[bool]:
    """标记不参与演唱的歌手/分部标签（如【狗妈】【合】【亚里亚】）字符位置。"""
    mask = [False] * len(text)
    for m in SPEAKER_TAG_RE.finditer(text):
        for i in range(m.start(), m.end()):
            mask[i] = True
    return mask


def place_segment_chars(text: str, words: list[dict],
                        en_granularity: str | None = None) -> fmt.Line | None:
    """把 WhisperX 的 words/chars 贴回原句字符（支持 KI-7 英文/数字按词或按字母均分）。

    - en_granularity="word"：英文/数字词内各字母共用该词起点（整词同步亮起）
    - en_granularity="char"：英文/数字词内按字母数均分 [start, end] 时长
    """
    if en_granularity is None:
        en_granularity = str(config.opt("en_granularity", "word"))

    slots: list[tuple[str, float, float]] = []
    for w in words:
        w_text = str(w.get("word", ""))
        w_chars = w.get("chars") or []
        w_start = w.get("start")
        w_end = w.get("end")

        # 判别是否为纯英文/数字 token
        alnum_only = bool(w_text.strip()) and ASCII_ALNUM.match(w_text.strip()) is not None

        if w_chars and not (alnum_only and en_granularity in ("word", "char") and w_start is not None and w_end is not None):
            valid_c = [c for c in w_chars if c.get("start") is not None and str(c.get("char", "")).strip()]
            if valid_c:
                for c in valid_c:
                    s = float(c["start"])
                    e = float(c.get("end", s + 0.1))
                    slots.append((str(c["char"]), s, max(e, s + 0.02)))
                continue

        if w_start is not None:
            s0 = float(w_start)
            e0 = float(w_end) if w_end is not None else s0 + 0.2
            e0 = max(e0, s0 + 0.05)
            tokens = [ch for ch in w_text if WORD_CHAR.match(ch)]
            if not tokens:
                continue
            n = len(tokens)
            for k, ch in enumerate(tokens):
                if en_granularity == "char" and n > 1:
                    cs = s0 + (e0 - s0) * (k / n)
                    ce = s0 + (e0 - s0) * ((k + 1) / n)
                    slots.append((ch, round(cs, 3), round(ce, 3)))
                else:
                    slots.append((ch, round(s0, 3), round(e0, 3)))

    if not slots:
        return None

    placed: list[tuple[str, float]] = []
    idx = 0
    last = slots[0]
    tag_mask = _speaker_tag_mask(text)
    for ci, ch in enumerate(text):
        if tag_mask[ci]:
            # 歌手标签（如【狗妈】）不演唱：行首标签贴首字起点，行中标签贴前一字起点，不消耗音节槽
            t_tag = slots[0][1] if idx == 0 else last[1]
            placed.append((ch, t_tag))
            continue
        if WORD_CHAR.match(ch):
            last = slots[min(idx, len(slots) - 1)]
            placed.append((ch, last[1]))
            idx += 1
        else:
            # 空格/标点放在前一音节结束时间（若已有音节），避免吞掉拖音
            t_non = last[2] if idx > 0 else slots[0][1]
            placed.append((ch, t_non))

    end_time = max(last[2], placed[-1][1] + 0.1)
    return fmt.Line(placed[0][1], text, placed, round(end_time, 3))


def restore_sustain(rows: list[fmt.Line], env: np.ndarray, hop: float = 0.02,
                    total: float | None = None,
                    max_gap_sec: float = 0.16) -> list[fmt.Line]:
    """用人声轨 RMS 能量包络恢复被 wav2vec2 压扁的长拖腔（KI-3）。

    若行尾之后人声能量仍持续高于该句主唱阈值（允许短于 max_gap_sec 的颤音低谷），
    则将行尾结束标签（以及被压在句尾的长音节）延展至真实声学收尾处。
    """
    if not rows or len(env) == 0:
        return rows

    dur = total or (len(env) * hop)
    max_gap_frames = max(1, int(round(max_gap_sec / hop)))

    for i, ln in enumerate(rows):
        if not ln.chars:
            continue
        cap = (rows[i + 1].start - 0.04) if i + 1 < len(rows) else dur
        cur_end = ln.end if ln.end is not None else (ln.chars[-1][1] + 0.1)
        if cap <= cur_end + 0.05:
            continue

        s_frame = max(0, int(ln.start / hop))
        e_frame = min(len(env), max(s_frame + 1, int(cur_end / hop) + 1))
        seg_env = env[s_frame:e_frame]
        if len(seg_env) == 0:
            continue

        peak = float(np.percentile(seg_env, 85))
        if peak < 1e-4:
            continue
        thr = max(0.012, peak * 0.28)

        cap_frame = min(len(env), int(cap / hop))
        last_active = e_frame - 1
        gap = 0
        for f in range(e_frame, cap_frame):
            if env[f] >= thr:
                last_active = f
                gap = 0
            else:
                gap += 1
                if gap > max_gap_frames:
                    break

        new_end = min(cap, (last_active + 1) * hop)
        if new_end > cur_end + 0.12:
            old_span = max(cur_end - ln.start, 0.05)
            new_span = max(new_end - ln.start, 0.1)
            # 检测 wav2vec2 CTC 把整句歌声压扁在句首（如 5 字挤在 0.6s 内，随后把 2.2s 全堆给末字）
            sung_idxs = [
                ci for ci, (ch, _) in enumerate(ln.chars)
                if WORD_CHAR.match(ch) and not _speaker_tag_mask(ln.text)[ci]
            ]
            n_sung = len(sung_idxs)
            if n_sung >= 2 and old_span < new_span * 0.55 and (old_span / n_sung) < 0.22:
                # 混合 CTC 相对比例与均匀分布，避免把全部拖音都压到最后一个字上
                new_chars = list(ln.chars)
                sung_times: list[float] = []
                for k, ci in enumerate(sung_idxs):
                    ctc_frac = (ln.chars[ci][1] - ln.start) / old_span
                    uni_frac = k / float(n_sung)
                    # 0.35 * CTC + 0.65 * 均匀（保留一点原始节奏差异，同时彻底消除 0.02s 压扁）
                    frac = max(0.0, min(0.95, 0.35 * ctc_frac + 0.65 * uni_frac))
                    t_new = round(ln.start + frac * new_span, 3)
                    if sung_times and t_new < sung_times[-1] + 0.05:
                        t_new = round(sung_times[-1] + 0.05, 3)
                    sung_times.append(t_new)
                    new_chars[ci] = (ln.chars[ci][0], t_new)
                # 同步更新非演唱字符（标签贴首字、空格/标点贴前一字）
                tag_mask = _speaker_tag_mask(ln.text)
                first_t = sung_times[0]
                last_t = first_t
                for ci, (ch, _) in enumerate(new_chars):
                    if tag_mask[ci]:
                        new_chars[ci] = (ch, first_t if ci < sung_idxs[0] else last_t)
                    elif ci in sung_idxs:
                        last_t = new_chars[ci][1]
                    else:
                        new_chars[ci] = (ch, last_t)
                ln.chars = new_chars
            ln.end = round(new_end, 3)

    return rows


def _clamp_rows(rows: list[fmt.Line]) -> list[fmt.Line]:
    for i, ln in enumerate(rows):
        if i + 1 < len(rows):
            cap = rows[i + 1].start - 0.02
            ln.chars = [(c, min(t, cap)) for c, t in ln.chars]
            if ln.end is not None:
                ln.end = min(ln.end, cap)
        prev = -10.0
        fixed = []
        for c, t in ln.chars:
            t = max(t, prev)
            fixed.append((c, t))
            prev = t
        ln.chars = fixed
        ln.start = fixed[0][1]
        ln.end = max(ln.end or 0.0, fixed[-1][1] + 0.1)
    return rows


def align_lines(vocal_wav: str, lines: list[tuple[float, str]], lang: str = "ja",
                total: float | None = None, device: str = "cuda",
                en_granularity: str | None = None,
                restore_long_vowel: bool | None = None) -> fmt.Lrc:
    from . import audio as au

    audio = au.decode(vocal_wav, 16000)
    dur = total or len(audio) / 16000

    # 日语优先走卡拉OK歌声专精模型 NextFire/mms-300m-ForcedAligner-karaoke-ja-Latn
    if lang == "ja":
        try:
            rows = _align_ja_mms_karaoke(audio, lines, dur, device=device)
            if rows:
                rows = _clamp_rows(rows)
                if restore_long_vowel is None:
                    restore_long_vowel = bool(config.opt("restore_long_vowel", True))
                if restore_long_vowel:
                    env = au.rms_envelope(audio, sr=16000, hop=0.02)
                    rows = restore_sustain(rows, env, hop=0.02, total=dur)
                    rows = _clamp_rows(rows)
                return fmt.Lrc(["[by:MMS 日语歌声逐字]"], rows)
        except Exception as e:                      # 模型没下全 / 依赖缺失时退回 WhisperX
            _warn(f"MMS 日语模型不可用，回退 WhisperX：{type(e).__name__}: {e}")

    from whisperx import alignment as wx

    model, meta = wx.load_align_model(language_code=lang, device=device)

    segs = []
    for i, (t0, text) in enumerate(lines):
        nxt = lines[i + 1][0] if i + 1 < len(lines) else dur
        clean_text = SPEAKER_TAG_RE.sub("", text).strip() or text
        n = len(re.sub(r"\s", "", clean_text))
        if i + 1 < len(lines) and nxt > t0 + 0.6:
            end = min(dur, nxt + 0.45)
        else:
            end = min(dur, max(nxt, t0 + max(2.2, n * 0.45) + 0.8))
        segs.append({"text": clean_text, "start": max(0.0, t0 - 0.25), "end": end})

    res = wx.align(segs, model, meta, audio, device,
                   interpolate_method="nearest", return_char_alignments=True)

    rows: list[fmt.Line] = []
    for seg, (_, text) in zip(res["segments"], lines):
        ln = place_segment_chars(text, seg.get("words", []),
                                 en_granularity=en_granularity)
        if ln is not None:
            rows.append(ln)

    rows = _clamp_rows(rows)

    if restore_long_vowel is None:
        restore_long_vowel = bool(config.opt("restore_long_vowel", True))
    if restore_long_vowel and rows:
        env = au.rms_envelope(audio, sr=16000, hop=0.02)
        rows = restore_sustain(rows, env, hop=0.02, total=dur)
        rows = _clamp_rows(rows)

    return fmt.Lrc([f"[by:WhisperX {lang} 逐字]"], rows)
