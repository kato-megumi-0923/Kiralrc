"""中间产物的落点：分离出来的音轨放在 work/sep 下，不往曲库写。

缓存跟 app 的存活期绑在一起：关掉就清；开着最多留三小时，
设置里也留了一个手动清除的入口（Go 侧调用 purge）。
"""

from __future__ import annotations

import hashlib
import re
import shutil
import time
from pathlib import Path

HOUR = 3600.0
KEEP_SECONDS = 3 * HOUR


def work_root() -> Path:
    return Path(__file__).resolve().parent.parent / "work"


def sep_root() -> Path:
    return work_root() / "sep"


def safe_stem(audio: str | Path) -> str:
    stem = Path(audio).stem
    cleaned = re.sub(r'[<>:"/\\|?*\x00-\x1f]', "_", stem).strip().rstrip(". ")
    return cleaned[:60] or "track"


def sep_dir(audio: str | Path) -> Path:
    """一首歌一个目录：路径哈希区分同名文件，改过的音频自然落到新目录。"""
    p = Path(audio).resolve()
    digest = hashlib.md5(str(p).encode("utf-8")).hexdigest()[:8]
    return sep_root() / f"{safe_stem(audio)}_{digest}"


def purge(keep_seconds: float = KEEP_SECONDS) -> int:
    """删掉超过 keep_seconds 没动过的分离结果，返回删掉的目录数。"""
    root = sep_root()
    if not root.is_dir():
        return 0
    now = time.time()
    removed = 0
    for entry in root.iterdir():
        try:
            if not entry.is_dir():
                continue
            if now - entry.stat().st_mtime < keep_seconds:
                continue
            shutil.rmtree(entry, ignore_errors=True)
            removed += 1
        except OSError:
            continue
    return removed


def purge_all() -> int:
    """全部清掉，返回删掉的目录数。"""
    root = sep_root()
    if not root.is_dir():
        return 0
    removed = 0
    for entry in root.iterdir():
        if entry.is_dir():
            shutil.rmtree(entry, ignore_errors=True)
            removed += 1
    try:
        root.rmdir()
    except OSError:
        pass
    return removed
