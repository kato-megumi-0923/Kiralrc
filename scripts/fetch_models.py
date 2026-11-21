"""下载对齐需要的模型（CT2 whisper 从 ModelScope，SOFA ckpt 从 GitHub Release 走镜像）。"""
from __future__ import annotations

import os
import sys
import urllib.request
import zipfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
MODELS = ROOT / "models"

CT2_FILES = ["config.json", "model.bin", "preprocessor_config.json",
             "tokenizer.json", "vocabulary.json"]
CT2_BASE = "https://www.modelscope.cn/models/Systran/faster-whisper-large-v3/resolve/master/"
SOFA_ZIP = ("https://gh-proxy.com/https://github.com/qiuqiao/SOFA/releases/download/"
            "v1.0.1/pretrained_mandarin_singing.zip")


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


if __name__ == "__main__":
    proxy = None
    for i, a in enumerate(sys.argv):
        if a == "--proxy" and i + 1 < len(sys.argv):
            proxy = sys.argv[i + 1]
    print("下载 faster-whisper large-v3 (CT2, ~3.1GB)")
    fetch_ct2(proxy)
    print("下载 SOFA 中文歌声模型 (~390MB)")
    fetch_sofa(proxy)
