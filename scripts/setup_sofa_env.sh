#!/usr/bin/env bash
# 建 SOFA 专用环境（Python 3.10）。SOFA 锁 numpy~=1.24，主环境装不了。
set -e
cd "$(dirname "$0")/.."
UV_PYTHON_INSTALL_MIRROR="https://gh-proxy.com/https://github.com/astral-sh/python-build-standalone/releases/download" \
  uv venv --python 3.10 .venv-sofa
uv pip install --python .venv-sofa/Scripts/python.exe \
  torch torchaudio "numpy~=1.24.1" click "einops==0.6.1" h5py "librosa<0.10.0" \
  "lightning>=2.0.0" "PyYAML~=6.0.1" tqdm textgrid chardet numba "pandas~=2.0.3" \
  pypinyin "setuptools<70" "matplotlib~=3.7.3" tensorboardX
# setuptools<70 是给 librosa 0.9 用的（它 import pkg_resources）
echo "[ok] .venv-sofa 就绪"
