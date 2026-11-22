"""项目配置：读取 config.toml，路径一律相对项目根目录。"""
from __future__ import annotations

import os
import tomllib
from functools import lru_cache
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


@lru_cache(maxsize=1)
def cfg() -> dict:
    path = ROOT / "config.toml"
    data = {}
    if path.exists():
        with open(path, "rb") as f:
            data = tomllib.load(f)
    paths = data.get("paths", {})
    defaults = data.get("defaults", {})
    return {"paths": paths, "defaults": defaults}


def path(key: str) -> Path:
    """取 config.toml [paths] 里的路径（相对项目根），允许同名环境变量覆盖。"""
    env = os.environ.get(f"KIRALRC_{key.upper()}")
    raw = env or cfg()["paths"].get(key)
    if not raw:
        raise KeyError(f"config.toml 缺少 paths.{key}")
    p = Path(raw)
    return p if p.is_absolute() else ROOT / p


def opt(key: str, default=None):
    env = os.environ.get(f"KIRALRC_{key.upper()}")
    if env is not None:
        return type(default)(env) if default is not None else env
    return cfg()["defaults"].get(key, default)


def work_dir() -> Path:
    d = path("work_dir")
    d.mkdir(parents=True, exist_ok=True)
    return d


def models_dir() -> Path:
    """所有本地模型与缓存的统一根目录（<ROOT>/models）。"""
    env = os.environ.get("KIRALRC_MODELS_DIR")
    return Path(env) if env else (ROOT / "models")


def hf_home_dir() -> Path:
    env = os.environ.get("HF_HOME")
    return Path(env) if env else (models_dir() / "hf")


def hf_hub_dir() -> Path:
    env = os.environ.get("HF_HUB_CACHE") or os.environ.get("HUGGINGFACE_HUB_CACHE")
    return Path(env) if env else (hf_home_dir() / "hub")


def torch_home_dir() -> Path:
    env = os.environ.get("TORCH_HOME")
    return Path(env) if env else (models_dir() / "torch")


def ensure_model_cache_env() -> None:
    """将 HuggingFace / Torch 默认模型缓存统一收拢到项目 models/ 目录（外部已显式设置环境变量时不覆盖）。"""
    hf_home = hf_home_dir()
    hf_hub = hf_hub_dir()
    torch_home = torch_home_dir()
    os.environ.setdefault("HF_HOME", str(hf_home))
    os.environ.setdefault("HF_HUB_CACHE", str(hf_hub))
    os.environ.setdefault("HUGGINGFACE_HUB_CACHE", str(hf_hub))
    os.environ.setdefault("TORCH_HOME", str(torch_home))


ensure_model_cache_env()
