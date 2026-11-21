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


def assign_lines_detail(
    lines: list[str],
    segments: list[dict],
    min_coverage: float | None = None,
) -> tuple[list[tuple[float, str]], list[dict]]:
    """全局对齐（DTW / 带跳行状态的单调 DP），返回 (已分配行, 被剔除的低覆盖率行)。

    KI-2：当音频是 TV size / 剪辑版（跳过了某段主歌）时，被剪掉的歌词行在周围分段的
    覆盖率低于 min_coverage，DP 会走「跳行」分支将其剔除，而不是硬塞进相邻分段。
    """
    if not segments or not lines:
        return [], []
    if min_coverage is None:
        min_coverage = float(config.opt("min_line_coverage", 0.22))

    n_seg, n_line = len(segments), len(lines)
    cov = []
    for l in lines:
        row = []
        for j, s in enumerate(segments):
            c = _coverage(l, s["text"])
            if j + 1 < n_seg:
                c = max(c, _coverage(l, s["text"] + segments[j + 1]["text"]) * 0.95)
            row.append(c)
        cov.append(row)

    max_cov = max((max(r) for r in cov), default=0.0)
    # 若整首歌覆盖率都极低（例如跨语种或无文本分段），则不做剔除兜底分配
    allow_skip = max_cov >= min_coverage and min_coverage > 0.0
    skip_reward = min_coverage - 1e-4 if allow_skip else -1e9
    crowd_penalty = 0.12

    NEG = -1e9
    dp = [[NEG] * n_seg for _ in range(n_line)]
    # parent[i][j] = (prev_j, matched: bool)
    parent: list[list[tuple[int, bool]]] = [[(-1, True)] * n_seg for _ in range(n_line)]

    for j in range(n_seg):
        if cov[0][j] >= skip_reward:
            dp[0][j] = cov[0][j]
            parent[0][j] = (j, True)
        else:
            dp[0][j] = skip_reward
            parent[0][j] = (j, False)

    for i in range(1, n_line):
        best_prev, arg_prev = NEG, 0
        for j in range(n_seg):
            # 1) 跳行（该行在剪辑版音频中被剪掉）
            best_here = dp[i - 1][j] + skip_reward
            p_here = (j, False)

            # 2) 同段复用（两句歌词落在同一个 ASR 分段内，加轻微拥挤惩罚）
            same_seg = dp[i - 1][j] + cov[i][j] - crowd_penalty
            if same_seg >= best_here:
                best_here = same_seg
                p_here = (j, True)

            # 3) 从更早的分段推进到当前分段 j（同分时取更靠后的段，避免重复句挤在一起）
            if best_prev > NEG / 2:
                adv = best_prev + cov[i][j]
                if adv >= best_here:
                    best_here = adv
                    p_here = (arg_prev, True)

            if dp[i - 1][j] >= best_prev:
                best_prev, arg_prev = dp[i - 1][j], j

            dp[i][j] = best_here
            parent[i][j] = p_here

    j = max(range(n_seg), key=lambda k: dp[n_line - 1][k])
    choices: list[tuple[int, bool]] = []
    for i in range(n_line - 1, -1, -1):
        prev_j, matched = parent[i][j]
        choices.append((j, matched))
        j = prev_j
    choices.reverse()

    out: list[tuple[float, str]] = []
    skipped: list[dict] = []
    for idx, (line, (seg_idx, matched)) in enumerate(zip(lines, choices)):
        c = cov[idx][seg_idx]
        if not matched or (allow_skip and c < min_coverage):
            skipped.append({"index": idx + 1, "text": line, "coverage": round(c, 3)})
            continue
        seg = segments[seg_idx]
        start = max(0.0, float(seg["start"]))
        if out and start <= out[-1][0] + 0.2:
            start = out[-1][0] + 0.3
        out.append((round(start, 3), line))

    return out, skipped


def assign_lines(lines: list[str], segments: list[dict],
                 min_coverage: float | None = None) -> list[tuple[float, str]]:
    """把歌词行单调分配给 ASR 分段（行级 DP），自动剔除剪辑版缺失行，返回 [(start, line)]。"""
    out, _ = assign_lines_detail(lines, segments, min_coverage=min_coverage)
    return out
