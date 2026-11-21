"""faster-whisper 识别 + 把歌词行粗略分配到 ASR 分段（给 SOFA 提供每句起点）。"""
from __future__ import annotations

import difflib
import os

import numpy as np

from . import config

# ct2 的 CUDA 后端需要 cublas/cudnn，借 torch 自带的 dll 目录
_TORCH_LIB = None


def _prepare_dll() -> None:
    global _TORCH_LIB
    if _TORCH_LIB is not None:
        return
    import torch

    _TORCH_LIB = os.path.join(os.path.dirname(torch.__file__), "lib")
    if os.path.isdir(_TORCH_LIB):
        os.add_dll_directory(_TORCH_LIB)
        os.environ["PATH"] = _TORCH_LIB + os.pathsep + os.environ.get("PATH", "")


def transcribe(audio: np.ndarray, lang: str = "zh", device: str | None = None,
               prompt: str | None = None):
    """返回 (words, segments)。words: [{w,s,e,p}]"""
    _prepare_dll()
    from faster_whisper import WhisperModel

    if device is None:
        import torch
        device = "cuda" if torch.cuda.is_available() else "cpu"
    model_path = str(config.path("whisper_model"))
    model = WhisperModel(model_path, device=device, compute_type="float16" if device == "cuda" else "int8")

    segments, info = model.transcribe(
        audio, language=lang, word_timestamps=True, vad_filter=False, beam_size=5,
        temperature=[0.0, 0.2, 0.4, 0.6, 0.8, 1.0], condition_on_previous_text=False,
        hallucination_silence_threshold=2.0, no_speech_threshold=0.6,
        compression_ratio_threshold=2.4, initial_prompt=prompt,
    )

    words, segs = [], []
    for seg in segments:
        segs.append({"start": seg.start, "end": seg.end, "text": seg.text})
        for w in seg.words or []:
            tok = w.word.strip()
            if tok:
                words.append({"w": tok, "s": round(w.start, 3), "e": round(w.end, 3),
                              "p": round(w.probability, 3)})
    return words, segs, info.duration


def _coverage(line: str, seg_text: str) -> float:
    lc = [c for c in line if c.strip()]
    sc = [c for c in seg_text if c.strip()]
    if not lc or not sc:
        return 0.0
    sm = difflib.SequenceMatcher(None, sc, lc, autojunk=False)
    hit = sum(n for _, _, n in sm.get_matching_blocks() if n)
    return hit / len(lc)


def assign_lines(lines: list[str], segments: list[dict]) -> list[tuple[float, str]]:
    """把歌词行单调分配给 ASR 分段（行级 DP），返回 [(start, line)]。

    ASR 的**文字**可能错，但分段起止时间通常可信，所以只借它的时间骨架。
    """
    if not segments or not lines:
        return []
    n_seg, n_line = len(segments), len(lines)
    cov = [[_coverage(l, s["text"]) for s in segments] for l in lines]

    NEG = -1e9
    dp = [[NEG] * n_seg for _ in range(n_line)]
    back = [[-1] * n_seg for _ in range(n_line)]
    for j in range(n_seg):
        dp[0][j] = cov[0][j]
    for i in range(1, n_line):
        best, arg = NEG, -1
        for j in range(n_seg):
            if dp[i - 1][j] >= best:  # 同分时取更靠后的段，避免重复句挤在一起
                best, arg = dp[i - 1][j], j
            dp[i][j] = best + cov[i][j]
            back[i][j] = arg

    j = max(range(n_seg), key=lambda k: dp[n_line - 1][k])
    pick = [j]
    for i in range(n_line - 1, 0, -1):
        j = back[i][j]
        pick.append(j)
    pick.reverse()

    out, used = [], 0
    for line, seg_idx in zip(lines, pick):
        seg = segments[seg_idx]
        start = max(0.0, seg["start"])
        if out and start <= out[-1][0] + 0.2:
            start = out[-1][0] + 0.3
        used += 1
        out.append((round(start, 3), line))
    return out
