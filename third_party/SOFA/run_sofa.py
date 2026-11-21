"""SOFA 推理驱动：强制走 librosa 读取（当前 torchaudio 需要 torchcodec）。"""
import sys

import modules.utils.load_wav as lw

lw.installed_torchaudio = False

from click.testing import CliRunner  # noqa: E402

import infer  # noqa: E402

if __name__ == "__main__":
    args = sys.argv[1:]
    r = CliRunner().invoke(infer.main, args, catch_exceptions=False)
    print(r.output[-3000:] if r.output else "")
    sys.exit(r.exit_code)
