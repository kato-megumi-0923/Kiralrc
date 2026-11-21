# Kiralrc

给歌曲补**逐字 LRC 歌词**的工具。

核心思路只有一条：**时间必须由专门的对齐器从音频里算出来，不能靠 ASR 的句子边界去猜。**

- 文本：优先用官方歌词（网易云），ASR 只用来给每句找个大致位置；
- 时间：按语种选用专门的强制对齐模型（中文走歌声专精 SOFA，日文走歌声专精 MMS，英文及其他语言走 WhisperX wav2vec2）；
- 所有对齐都在 **demucs 分离出的人声轨**上跑，因为伴奏会显著干扰对齐。

## 支持语言与对齐模型

| 语言 | 强制对齐模型 | 文本 / 音素处理 | 特色支持 |
|---|---|---|---|
| **中文（`zh`）** | **SOFA**（`v1.0.0_mandarin_singing.ckpt`，歌声专精） | `pypinyin` + `opencpop-extension` 词典 | 支持 `polyphone.toml` 自定义多音字读音覆盖 |
| **日文（`ja`）** | **MMS**（`NextFire/mms-300m-ForcedAligner-karaoke-ja-Latn`，歌声专精） | `fugashi`（MeCab + unidic）精确注音切分 | 准确处理「は→わ」「へ→え」等助词及长音、促音、拗音 |
| **英文（`en`）及其他** | **WhisperX**（`wav2vec2` CTC） | 词级 / 字符级对齐 | 支持 `en_granularity` 切换按词（`word`）或按字母（`char`），并按人声能量包络保真长音 |

> 前置定位与人声分离全语种统一采用 `demucs` + `faster-whisper-large-v3`。

## 为什么不用纯 Whisper / WhisperX

实测（本机）：

| 做法 | 结果 |
|---|---|
| faster-whisper 词级时间 + 按字数均摊 | 中文同音字错一堆（乡间→相见、暮归→木龟），且行首普遍**早 0.5~1.2 秒** |
| WhisperX（wav2vec2 语音 CTC） | 短音准；**长音被压扁**——实际 5.27s 的拖腔只给 2.47s |
| **SOFA（歌声专门）** | 长音正确、句内节奏自然；配合人声轨，与声学起音差 <0.1s |

还有一条经验：**不要拿网易云官方 LRC 当校验基准**。它对应的是另一版母带，
本地文件可能比它多 0.7s 前导静音——用它校验会得出「我很准」的错误结论。
判断准不准，要看**人声轨的能量包络**（见 `kiralrc/audio.py: rms_envelope`）。

## 输出格式

```
[ti:曲名]
[ar:歌手]
[by:SOFA 歌声对齐]
[00:06.232]<00:06.232>走<00:07.115>在<00:07.440>乡...
```

- 时间戳 **三位小数**（全库统一）
- **行尾多一个标签**，标该行最后一个字的收尾时间；少了它播放器会把末字起点当行尾，
  字还没唱完就被切掉
- UTF-8 无 BOM

## 安装

需要两个 Python 环境：主环境跑 ASR/demucs，另一个跑 SOFA（它锁 `numpy~=1.24`，新版 Python 装不了）。

```bash
# 1) 主环境
pip install faster-whisper demucs stable-ts whisperx pypinyin av numpy torch
pip install pypinyin

# 2) SOFA 环境（Python 3.10）
uv venv --python 3.10 .venv-sofa
uv pip install --python .venv-sofa/Scripts/python.exe torch torchaudio \
    "numpy~=1.24.1" click "einops==0.6.1" h5py "librosa<0.10.0" "lightning>=2.0.0" \
    "PyYAML~=6.0.1" tqdm textgrid chardet numba "pandas~=2.0.3" pypinyin "setuptools<70"
# 注：librosa 0.9 依赖 pkg_resources，所以 setuptools 必须 <70

# 3) 模型
python scripts/fetch_models.py
```

`python -m kiralrc info` 可以检查路径和模型是否齐备。

## 桌面端 GUI（Windows）

除命令行外，项目还提供基于 Go 编写的桌面端 GUI（`kiralrc-gui.exe`），包含**曲库**、**新建对齐**、**任务队列**、**编辑与校对**、**工具箱**五个面板，支持音频试听、批量任务队列、逐字时间轴校对以及 `polyphone.toml` 多音字词表编辑。

**从源码编译运行**（需要 Go 1.27+，且本地已配置好 Python 对齐环境与模型）：

```bash
go build -ldflags="-H windowsgui" -o kiralrc-gui.exe .
```

## 用法

```bash
# 音频 → 逐字 LRC（自动搜网易云歌词；也可以 --ref 指定歌词文件）
python -m kiralrc align "歌曲.mp3" --ti 曲名 --ar 歌手

# 日文
python -m kiralrc align "song.mp3" --lang ja

# 校验格式
python -m kiralrc validate "歌曲.lrc"

# 双语歌词给翻译行加 ❤️
python -m kiralrc hearts "歌曲.lrc"

# 两位小数 → 三位
python -m kiralrc convert "歌曲.lrc"
```

`align` 会把中间产物放在 `work/<歌名>_<hash>/`：`vocals.wav`（分离出的人声）、
`segments/`（按句切片的 wav + 拼音 lab）、`segments/htk/words/*.lab`（对齐结果）。
同一首歌重跑会复用已分离的人声。

## 已知问题

见 [`docs/known-issues.md`](docs/known-issues.md)：哪些坑已经修好（别再改回去）、哪些还需要设计。

## 目录

```
config.toml        路径与默认参数（模型、venv、词典、主唱检测/长音保真/粒度等）
polyphone.toml     多音字自定义读音词表
main.go            Windows 桌面端 GUI 入口
internal/app/      GUI 核心界面、任务调度与音频播放器
assets/            应用图标资源
design/            UI 设计稿
kiralrc/
  cli.py           命令行入口
  pipeline.py      端到端流程（定文本 → 定句子位置 → 分离 → 对齐 → 输出）
  sofa.py          SOFA 调用与结果拼装
  jp.py            WhisperX 多语言逐字对齐
  asr.py           faster-whisper + 歌词行↔ASR 分段的单调 DP
  audio.py         解码（PyAV）、demucs 分离、能量包络、主唱检测
  text.py          网易云抓歌词、拼音、多音字词表处理
  format.py        LRC 解析/格式化/校验/小数位转换
  hearts.py        双语加 ❤️
  legacy/          早期的一次性脚本（保留作参照，不再维护）
tools/run_sofa.py  在 SOFA venv 里跑推理（绕开 torchcodec）
third_party/SOFA/  SOFA 源码（MIT，已去 .git）
models/            CT2 whisper + SOFA ckpt
scripts/           模型下载、环境安装、批量抓歌词（fetch_lyrics_dir.py）
```

## 代码来源

| 部分 | 来源 | 说明 |
|---|---|---|
| `kiralrc/`、`tools/`、`tests/`、`scripts/`、`config.toml`、`polyphone.toml`、`main.go`、`internal/` | **本项目原创** | Python 对齐核心 + Go 桌面端 GUI |
| `third_party/SOFA/` | [qiuqiao/SOFA](https://github.com/qiuqiao/SOFA)，MIT © 2023 suco | 原样引入，**未改其源码**；只由 `tools/run_sofa.py` 调用 |
| `models/` | 下载，不入库 | CT2 whisper：`Systran/faster-whisper-large-v3`（经 ModelScope 镜像）<br>SOFA 权重：官方 Release `v1.0.0_mandarin_singing.ckpt` |
| 依赖库 | PyPI | faster-whisper / demucs / whisperx / PyAV / pypinyin / torch … |
| `kiralrc/legacy/` | 本项目早期版本 | 一次性脚本，保留作参照，不再维护 |

要公开的话：保留 `third_party/SOFA/LICENSE`，并在文档里注明模型来源即可（SOFA 为 MIT）。
本项目自身代码建议同样以 MIT 发布。

## 许可

本项目代码以 MIT 发布（见 `LICENSE`）。`third_party/SOFA` 为其原作者的 MIT 代码（见其 `LICENSE`），
模型权重版权归各自作者。

仓库里**不包含**任何歌曲音频或歌词文本；`examples/` 只有占位示例。
