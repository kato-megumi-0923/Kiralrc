"""歌词 ASR → LRC 生成器。

用法:
  python lrcgen.py --audio "xxx.mp3" --lang zh --out "xxx.lrc" [--json out.json] [--shift -0.2]

流程: faster-whisper 词级时间戳 → 按标点/停顿/字数切行 → 写 LRC。
"""
import argparse
import json
import os
import sys
import time

import av
import numpy as np

# ct2 的 CUDA 后端需要 cublas/cudnn, 借用 torch 自带的 dll 目录
import torch

_TORCH_LIB = os.path.join(os.path.dirname(torch.__file__), "lib")
if os.path.isdir(_TORCH_LIB):
    os.add_dll_directory(_TORCH_LIB)
    os.environ["PATH"] = _TORCH_LIB + os.pathsep + os.environ.get("PATH", "")

from faster_whisper import WhisperModel

SAMPLE_RATE = 16000


def decode_audio(path, sr=SAMPLE_RATE):
    """用 PyAV 解成 16k 单声道 float32, 绕开 faster-whisper 对 av>=14 的 API 不兼容。"""
    with av.open(path, mode="r") as container:
        resampler = av.AudioResampler(format="flt", layout="mono", rate=sr)
        chunks = []
        for frame in container.decode(audio=0):
            for out in resampler.resample(frame):
                chunks.append(out.to_ndarray())
        for out in resampler.resample(None):
            chunks.append(out.to_ndarray())
    if not chunks:
        raise RuntimeError(f"无法解码音频: {path}")
    audio = np.concatenate(chunks, axis=1)[0].astype(np.float32)
    return np.clip(audio, -1.0, 1.0)

MODEL_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), "models", "faster-whisper-large-v3")

HARD_PUNCT = set("。！？!?…")
SOFT_PUNCT = set("，、；,;：:")
CJK_RANGE = [(0x3040, 0x30FF), (0x4E00, 0x9FFF), (0x3400, 0x4DBF), (0xF900, 0xFAFF)]


def is_cjk(ch: str) -> bool:
    o = ord(ch)
    return any(a <= o <= b for a, b in CJK_RANGE)


def norm_token(t: str) -> str:
    return t.strip()


def transcribe(model, audio, lang, vad=False, prompt=None, progress=True):
    t0 = time.time()
    segments, info = model.transcribe(
        audio,
        language=lang,
        word_timestamps=True,
        vad_filter=vad,
        beam_size=5,
        temperature=[0.0, 0.2, 0.4, 0.6, 0.8, 1.0],
        condition_on_previous_text=False,
        hallucination_silence_threshold=2.0,
        no_speech_threshold=0.6,
        compression_ratio_threshold=2.4,
        initial_prompt=prompt,
    )
    words = []
    segs = []
    for seg in segments:
        segs.append({"start": seg.start, "end": seg.end, "text": seg.text,
                     "no_speech": seg.no_speech_prob, "cr": seg.compression_ratio})
        for w in seg.words or []:
            tok = norm_token(w.word)
            if not tok:
                continue
            words.append({"w": tok, "s": round(w.start, 3), "e": round(w.end, 3),
                          "p": round(w.probability, 3)})
    return words, segs, info, time.time() - t0


def build_text(tokens):
    """CJK 之间不加空格, 拉丁文之间加空格。"""
    out = []
    for i, t in enumerate(tokens):
        if i and (not is_cjk(t[0]) and not is_cjk(tokens[i - 1][-1])):
            out.append(" ")
        out.append(t)
    return "".join(out)


def group_lines(words, max_chars=26, min_chars=5, hard_pause=0.80, soft_pause=0.35):
    lines, cur, cur_len = [], [], 0
    for i, w in enumerate(words):
        tok = w["w"]
        if cur and not is_cjk(tok[0]) and not is_cjk(cur[-1]["w"][-1]):
            cur_len += 1
        cur.append(w)
        cur_len += len(tok)
        nxt = words[i + 1] if i + 1 < len(words) else None
        gap = (nxt["s"] - w["e"]) if nxt else 99.0
        ends_hard = tok[-1] in HARD_PUNCT
        ends_soft = tok[-1] in SOFT_PUNCT
        if ends_hard or gap > hard_pause or cur_len >= max_chars or (ends_soft and gap > soft_pause and cur_len >= 8):
            lines.append(cur)
            cur, cur_len = [], 0
    if cur:
        lines.append(cur)

    # 合并过短的行
    merged = []
    for ln in lines:
        if merged and len(build_text([w["w"] for w in ln])) < min_chars and len(merged[-1]) < 30:
            gap = ln[0]["s"] - merged[-1][-1]["e"]
            if gap < 1.2:
                merged[-1].extend(ln)
                continue
        merged.append(ln)
    return merged


def fmt_ts(sec: float) -> str:
    sec = max(0.0, sec)
    return "[%02d:%06.3f]" % (int(sec // 60), sec % 60)


def write_lrc(path, lines, shift, header=None):
    out = []
    if header:
        out.extend(header)
    for i, ln in enumerate(lines):
        start = ln[0]["s"] + shift
        if i == 0:
            start = max(start, 0.5)
        text = build_text([w["w"] for w in ln]).strip()
        out.append(fmt_ts(start) + text)
    data = "\n".join(out) + "\n"
    with open(path, "w", encoding="utf-8", newline="\n") as f:
        f.write(data)
    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--audio", required=True)
    ap.add_argument("--lang", default="zh")
    ap.add_argument("--out")
    ap.add_argument("--json")
    ap.add_argument("--shift", type=float, default=0.0)
    ap.add_argument("--model", default=MODEL_DIR)
    ap.add_argument("--vad", action="store_true")
    ap.add_argument("--prompt", default=None)
    ap.add_argument("--prompt-file", default=None)
    ap.add_argument("--ti", default="")
    ap.add_argument("--ar", default="")
    ap.add_argument("--al", default="")
    ap.add_argument("--max-chars", type=int, default=26)
    args = ap.parse_args()

    prompt = args.prompt
    if args.prompt_file:
        with open(args.prompt_file, encoding="utf-8") as f:
            prompt = f.read().strip()[:800]

    print(f"[load] {args.model}", flush=True)
    model = WhisperModel(args.model, device="cuda", compute_type="float16")
    print(f"[asr] {os.path.basename(args.audio)} lang={args.lang}", flush=True)
    audio = decode_audio(args.audio)
    print(f"[asr] decoded {len(audio)/SAMPLE_RATE:.1f}s", flush=True)
    words, segs, info, dur = transcribe(model, audio, args.lang, args.vad, prompt)
    print(f"[asr] {len(words)} words, audio {info.duration:.1f}s, cost {dur:.1f}s", flush=True)

    lines = group_lines(words, max_chars=args.max_chars)
    header = []
    if args.ti:
        header.append(f"[ti:{args.ti}]")
    if args.ar:
        header.append(f"[ar:{args.ar}]")
    if args.al:
        header.append(f"[al:{args.al}]")
    header.append("[by:Whisper-large-v3]")

    rendered = write_lrc(args.out or os.path.splitext(args.audio)[0] + ".lrc", lines, args.shift, header)
    for r in rendered:
        print(r)

    if args.json:
        with open(args.json, "w", encoding="utf-8") as f:
            json.dump({"words": words, "segments": segs, "lines": rendered,
                       "duration": info.duration}, f, ensure_ascii=False, indent=1)
        print(f"[json] {args.json}")


if __name__ == "__main__":
    main()
