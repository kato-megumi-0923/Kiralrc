"""在 SOFA 专用 venv 里运行 SOFA 推理。

两点适配：
1. 把 third_party/SOFA 加进 sys.path；
2. torchaudio 2.11 读音频需要 torchcodec，强制走 SOFA 自带的 librosa 分支。
"""
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
SOFA_ROOT = ROOT / "third_party" / "SOFA"
sys.path.insert(0, str(SOFA_ROOT))

import modules.utils.load_wav as lw  # noqa: E402

lw.installed_torchaudio = False

from click.testing import CliRunner  # noqa: E402

import infer  # noqa: E402

if __name__ == "__main__":
    r = CliRunner().invoke(infer.main, sys.argv[1:], catch_exceptions=False)
    if r.output:
        print(r.output[-2000:])
    sys.exit(r.exit_code)
