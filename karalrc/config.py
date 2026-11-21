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
    env = os.environ.get(f"KARALRC_{key.upper()}")
    raw = env or cfg()["paths"].get(key)
    if not raw:
        raise KeyError(f"config.toml 缺少 paths.{key}")
    p = Path(raw)
    return p if p.is_absolute() else ROOT / p


def opt(key: str, default=None):
    env = os.environ.get(f"KARALRC_{key.upper()}")
    if env is not None:
        return type(default)(env) if default is not None else env
    return cfg()["defaults"].get(key, default)


def work_dir() -> Path:
    d = path("work_dir")
    d.mkdir(parents=True, exist_ok=True)
    return d
