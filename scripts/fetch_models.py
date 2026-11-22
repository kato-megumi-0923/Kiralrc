"""下载句级定位和普通话歌声对齐的基础模型。"""
from __future__ import annotations

import hashlib
import os
import shutil
import sys
import urllib.request
import zipfile
from pathlib import Path
from tempfile import TemporaryDirectory

ROOT = Path(__file__).resolve().parent.parent
MODELS = ROOT / "models"

CT2_FILES = ["config.json", "model.bin", "preprocessor_config.json",
             "tokenizer.json", "vocabulary.json"]
CT2_BASE = "https://www.modelscope.cn/models/Systran/faster-whisper-large-v3/resolve/master/"
SOFA_ZIP = ("https://gh-proxy.com/https://github.com/qiuqiao/SOFA/releases/download/"
            "v1.0.1/pretrained_mandarin_singing.zip")
HUBERTFA_ZIP = ("https://github.com/wolfgitpr/HubertFA/releases/download/"
                "v0.0.7/1218_hfa_model_new_dict.zip")
HUBERTFA_SHA256 = "48bd6dbcc293e47cc6cbcc556baf575e3d91d61d0ac88ddeccd6098f8f346fa2"


def _download(url: str, dst: Path, proxy: str | None = None) -> None:
    dst.parent.mkdir(parents=True, exist_ok=True)
    if proxy:
        os.environ["https_proxy"] = proxy
        os.environ["http_proxy"] = proxy
    else:
        os.environ["no_proxy"] = "*"
    print(f"  -> {dst.name}")
    urllib.request.urlretrieve(url, dst)


def fetch_ct2(proxy: str | None) -> None:
    d = MODELS / "faster-whisper-large-v3"
    d.mkdir(parents=True, exist_ok=True)
    for f in CT2_FILES:
        dst = d / f
        if dst.exists() and dst.stat().st_size > 1000:
            print(f"  [skip] {f}")
            continue
        _download(CT2_BASE + f, dst, proxy)
    print(f"[ok] {d}")


def fetch_sofa(proxy: str | None) -> None:
    d = MODELS / "sofa"
    ckpt = d / "v1.0.0_mandarin_singing.ckpt"
    if ckpt.exists() and ckpt.stat().st_size > 1e8:
        print(f"  [skip] {ckpt.name}")
        return
    z = d / "_model.zip"
    _download(SOFA_ZIP, z, proxy)
    with zipfile.ZipFile(z) as f:
        for n in f.namelist():
            if n.endswith((".ckpt", ".yaml", ".md")):
                with f.open(n) as src, open(d / Path(n).name, "wb") as out:
                    out.write(src.read())
    z.unlink(missing_ok=True)
    print(f"[ok] {d}")


def fetch_hubertfa(proxy: str | None) -> None:
    d = MODELS / "hubertfa"
    files = ("config.json", "vocab.json", "ds-zh-pinyin-lite.txt", "model.onnx", "VERSION")
    if all((d / name).is_file() for name in files) and (d / "model.onnx").stat().st_size == 415220874:
        print(f"  [skip] {d}")
        return
    MODELS.mkdir(parents=True, exist_ok=True)
    with TemporaryDirectory(dir=MODELS, prefix="hubertfa-") as tmp:
        staging = Path(tmp)
        archive = staging / "hubertfa.zip"
        try:
            _download(HUBERTFA_ZIP, archive, proxy)
        except OSError:
            _download("https://gh-proxy.com/" + HUBERTFA_ZIP, archive, proxy)
        digest = hashlib.sha256()
        with archive.open("rb") as f:
            for chunk in iter(lambda: f.read(1024 * 1024), b""):
                digest.update(chunk)
        if digest.hexdigest() != HUBERTFA_SHA256:
            raise ValueError("HubertFA 模型压缩包 SHA-256 校验失败")
        with zipfile.ZipFile(archive) as z:
            for name in files:
                with z.open(f"1218_hfa_model_new_dict/{name}") as src, (staging / name).open("wb") as dst:
                    shutil.copyfileobj(src, dst)
        d.mkdir(parents=True, exist_ok=True)
        for name in files:
            (staging / name).replace(d / name)
    print(f"[ok] {d}")


if __name__ == "__main__":
    proxy = None
    for i, a in enumerate(sys.argv):
        if a == "--proxy" and i + 1 < len(sys.argv):
            proxy = sys.argv[i + 1]
    print("下载 faster-whisper large-v3 (CT2, ~3.1GB)")
    fetch_ct2(proxy)
    print("下载 HubertFA 中文歌声模型 (~245MB)")
    fetch_hubertfa(proxy)
    if "--with-sofa" in sys.argv:
        print("下载可选的 SOFA 中文歌声模型 (~390MB)")
        fetch_sofa(proxy)
