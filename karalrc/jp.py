"""非中文语言（以日文为主）的逐字对齐：WhisperX wav2vec2 CTC，逐句在**人声轨**上跑。

SOFA 只有中文歌声模型；日文/英文用 WhisperX。
注意 wav2vec2 是语音模型，长音会被压扁，所以务必喂 demucs 分离后的人声。
"""
from __future__ import annotations

import re

from . import format as fmt

WORD_CHAR = re.compile(r"[\w\u3040-\u30ff\u4e00-\u9fff]")


def align_lines(vocal_wav: str, lines: list[tuple[float, str]], lang: str = "ja",
                total: float | None = None, device: str = "cuda") -> fmt.Lrc:
    from whisperx import alignment as wx

    from . import audio as au

    audio = au.decode(vocal_wav, 16000)
    dur = total or len(audio) / 16000
    model, meta = wx.load_align_model(language_code=lang, device=device)

    segs = []
    for i, (t0, text) in enumerate(lines):
        nxt = lines[i + 1][0] if i + 1 < len(lines) else dur
        n = len(re.sub(r"\s", "", text))
        end = min(dur, max(nxt, t0 + max(2.2, n * 0.45) + 0.8))
        segs.append({"text": text, "start": max(0.0, t0 - 0.35), "end": end})

    res = wx.align(segs, model, meta, audio, device,
                   interpolate_method="nearest", return_char_alignments=True)

    rows = []
    for seg, (t0, text) in zip(res["segments"], lines):
        chars = []
        for w in seg.get("words", []):
            if w.get("chars"):
                for c in w["chars"]:
                    if c.get("start") is not None and c["char"].strip():
                        chars.append((c["char"], float(c["start"]), float(c["end"])))
            elif w.get("start") is not None:
                chars.append((w["word"], float(w["start"]), float(w["end"])))
        if not chars:
            continue
        placed, idx, last = [], 0, chars[0]
        for ch in text:
            if WORD_CHAR.match(ch):
                last = chars[min(idx, len(chars) - 1)]
                placed.append((ch, last[1]))
                idx += 1
            else:
                placed.append((ch, last[1]))
        rows.append(fmt.Line(placed[0][1], text, placed, placed[-1][1] + 0.1))

    for i, ln in enumerate(rows):
        if i + 1 < len(rows):
            cap = rows[i + 1].start - 0.02
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

    return fmt.Lrc([f"[by:WhisperX {lang} 逐字]"], rows)
