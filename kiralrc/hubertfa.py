"""HubertFA v0.0.7 ONNX inference for the shared Mandarin lyric slices."""
from __future__ import annotations

import json
from functools import lru_cache
from importlib.util import find_spec
from pathlib import Path

from . import audio, config

MODEL_FILES = ("model.onnx", "config.json", "vocab.json", "VERSION", "ds-zh-pinyin-lite.txt")


def model_dir() -> Path:
    try:
        return config.path("hubertfa_model")
    except KeyError:
        return config.models_dir() / "hubertfa"


def available() -> bool:
    root = model_dir()
    return find_spec("onnxruntime") is not None and all((root / name).is_file() for name in MODEL_FILES)


@lru_cache(maxsize=1)
def _session(path: Path):
    import onnxruntime as ort

    options = ort.SessionOptions()
    options.intra_op_num_threads = 4
    options.inter_op_num_threads = 1
    # The portable runtime installs the CPU build. Use CUDA when the user has
    # installed ONNX Runtime GPU and its CUDA libraries can be loaded.
    providers = [p for p in ("CUDAExecutionProvider", "CPUExecutionProvider") if p in ort.get_available_providers()]
    return ort.InferenceSession(str(path), sess_options=options, providers=providers)


def run(segments_dir: Path, meta: list[dict]) -> None:
    """Write the same HTK word timing format consumed by the Mandarin pipeline."""
    from ._hubertfa.decoder import AlignmentDecoder, NonLexicalDecoder
    from ._hubertfa.g2p import DictionaryG2P

    root = model_dir()
    if (root / "VERSION").read_text(encoding="utf-8").strip() != "5":
        raise ValueError("HubertFA 需要 v0.0.7 的 VERSION 5 ONNX 模型")
    vocab = json.loads((root / "vocab.json").read_text(encoding="utf-8"))
    mel = json.loads((root / "config.json").read_text(encoding="utf-8"))["mel_spec_config"]
    rate, hop = mel["sample_rate"], mel["hop_size"]
    g2p = DictionaryG2P("zh" if vocab["language_prefix"] else None, root / "ds-zh-pinyin-lite.txt")
    decoder = AlignmentDecoder(vocab, rate, hop)
    breaths = NonLexicalDecoder(vocab, ["None", *vocab["non_lexical_phonemes"]], rate, hop)
    session = _session(root / "model.onnx")
    outputs = ["ph_frame_logits", "ph_edge_logits", "cvnt_logits"]
    words_dir = segments_dir / "htk" / "words"
    words_dir.mkdir(parents=True, exist_ok=True)
    for segment in meta:
        name = segment["name"]
        phones, syllables, indices = g2p(" ".join(segment["syls"]))
        if syllables != segment["syls"]:
            raise ValueError(f"HubertFA 词典缺少音节：{segment['text']}")
        wav = audio.decode(segments_dir / f"{name}.wav", rate)
        duration = len(wav) / rate
        frame, edge, non_lexical = session.run(outputs, {"waveform": wav[None, :]})
        words, _ = decoder.decode(frame, edge, duration, phones, syllables, indices)
        for group in breaths.decode(non_lexical, duration, ["AP"]):
            for breath in group:
                words.add_AP(breath)
        words.fill_small_gaps(duration)
        sung = [word for word in words if word.text not in ("SP", "AP", "EP")]
        if [word.text for word in sung] != syllables:
            raise ValueError(f"HubertFA 未完整对齐：{segment['text']}")
        (words_dir / f"{name}.lab").write_text("".join(
            f"{round(word.start * 1e7)} {round(word.end * 1e7)} {word.text}\n" for word in sung
        ), encoding="utf-8")
