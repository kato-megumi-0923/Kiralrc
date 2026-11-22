"""Flag suspect lyric timing against an isolated vocal track."""
from pathlib import Path

import numpy as np

from . import audio as au, format as fmt


def check(lrc: fmt.Lrc, vocals: Path) -> list[str]:
    hop = 0.02
    env = au.rms_envelope(au.decode(vocals, 16000), hop=hop)
    rows = [line for line in lrc.lines if line.enhanced and line.chars]
    issues = []
    for i, line in enumerate(rows):
        if line.end is None or not 0 <= line.start < line.end <= len(env) * hop:
            continue
        start = round(line.start / hop)
        end = round(line.end / hop)
        level = float(np.percentile(env[start:end], 80)) if end > start else 0.0
        if level < 0.012:
            continue
        threshold = max(0.008, level * 0.18)
        active = env >= threshold
        reasons = []
        if end - start > 16 and not active[start:start + 12].any() and active[start + 12:end].any():
            reasons.append("句首约 240 ms 内未检测到明显人声")

        next_start = round(rows[i + 1].start / hop) if i + 1 < len(rows) else len(env)
        if next_start >= end + 14 and active[end - 3:end + 14].sum() >= 15:
            reasons.append("句尾后仍有连续人声")

        sung = [time for char, time in line.chars if char.isalnum()]
        if len(sung) >= 4:
            tail = sung[-4:]
            cramped = sum(0 <= right - left <= 0.065 for left, right in zip(tail, tail[1:]))
            last = round(tail[-1] / hop)
            if cramped >= 2 and end - last >= 18 and active[last:end].sum() >= 12:
                reasons.append("尾字挤压，末字时间内仍有人声")

        if reasons:
            issues.append(f"[人声] 第 {i + 1} 行 {fmt.fmt_ts(line.start)} {line.text}：" + "、".join(reasons))
    return issues
