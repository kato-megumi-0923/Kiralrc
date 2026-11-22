# Kiralrc

给歌曲补**逐字 LRC 歌词**的工具。

核心思路只有一条：**时间必须由专门的对齐器从音频里算出来，不能靠 ASR 的句子边界去猜。**

- 文本：优先使用参考歌词（支持多平台顺序拉取），ASR 只用来给每句找个大致位置；
- 时间：按语种选用专门的歌声/歌词强制对齐模型（中文走 SOFA，日文走 NextFire MMS，英文及其他语言走 Echora-300M MMS）；
- 所有对齐都在 **demucs 分离出的人声轨**上跑，因为伴奏会显著干扰对齐。

## 支持语言与对齐模型

| 语言 | 强制对齐模型 | 文本 / 音素处理 | 特色支持 |
|---|---|---|---|
| **中文（`zh`）** | **SOFA**（`v1.0.0_mandarin_singing.ckpt`，歌声专精） | `pypinyin` + `opencpop-extension` 词典 | 支持 `polyphone.toml` 自定义多音字读音覆盖 |
| **日文（`ja`）** | **NextFire MMS**（`NextFire/mms-300m-ForcedAligner-karaoke-ja-Latn`，歌声专精） | `fugashi`（MeCab + unidic）精确注音切分 | 准确处理「は→わ」「へ→え」等助词及长音、促音、拗音 |
| **英文（`en`）及其他** | **Echora-300M MMS**（`hcX02/echora-mms-300m-multilingual-lyrics-forced-aligner`，多语歌词专精） | 24s 长滑窗 + 词级/音节级对齐 | 支持 `en_granularity` 切换按词（`word`）或按字母（`char`），并按人声能量包络保真长音 |

> 前置定位与人声分离全语种统一采用 `demucs` + `faster-whisper-large-v3`。

## 为什么不用纯 Whisper / WhisperX（而选用 SOFA / MMS）

实测（本机）：

| 做法 | 结果 |
|---|---|
| faster-whisper 词级时间 + 按字数均摊 | 中文同音字错一堆（乡间→相见、暮归→木龟），且行首普遍**早 0.5~1.2 秒** |
| WhisperX（wav2vec2 语音 CTC） | 短音准；**长音被压扁**——实际 5.27s 的拖腔只给 2.47s，快歌/歌声鲁棒性差 |
| **SOFA（中文歌声专门）** | 长音正确、句内节奏自然；配合人声轨，与声学起音差 <0.1s |
| **MMS 歌声/歌词微调（日/英/多语）** | 基于 MMS-300M 针对**歌声/歌词微调**（NextFire 日语 / Echora-300M 多语），适应歌唱发声与连音；配合 24s 长滑窗上下文、通配符边界对齐与人声能量包络长音保真，全面替代 WhisperX |

还有一条经验：**不要拿网易云官方 LRC 当校验基准**。它对应的是另一版母带，
本地文件可能比它多 0.7s 前导静音——用它校验会得出「我很准」的错误结论。
判断准不准，要看**人声轨的能量包络**（见 `kiralrc/audio.py: rms_envelope`）。

## 获取歌词优先级

运行对齐时，按以下顺序获取参考歌词文本（`resolve_reference`）：

1. **显式指定的参考文件（`--ref`）**：若指定路径存在，优先使用。
2. **同目录同名 `.lrc` 文件**：若音频同目录下存在同名 `.lrc` 且有效歌词不少于 4 行（过滤「纯音乐，请欣赏」等短占位），直接复用。
3. **多平台顺序自动搜索（`best_match`）**：根据 `--query` 或文件名提取关键词，结合音频时长按 **网易云 → QQ音乐 → 酷狗音乐 → LRCLIB** 的顺序逐级兜底匹配，找到即停并合并原文与中文译文。
4. **外语译文自动补全**：若歌词识别为日文（`ja`）或英文（`en`）且本地参考文件不含中文译文，会自动向网易云拉取对应译文补全。

## 逐字歌词生成链路

端到端处理流程（`make_lrc`）分为 6 步：

1. **确定文本与语种**：按上述优先级取得歌词行与译文映射，并自动识别语种（`zh` / `ja` / `en` 等）。
2. **确定每句位置**：
   - 若参考 LRC 自带精确时间戳（非粗糙/统一小数尾数且未强制重定位），直接沿用句首时间；
   - 否则用 `faster-whisper-large-v3` 转录音频，通过单调动态规划将歌词行对齐到 ASR 分段，同时过滤制作人员信息行与剪辑版音频中缺失的段落（低于 `min_line_coverage`）。
3. **人声分离**：使用 `demucs`（`htdemucs`）分离人声轨 `vocals.wav`（同一首歌会复用缓存）。
4. **主唱检测**：分析人声轨 1~4kHz 能量占比与 P90 RMS（`min_vocal_ratio` / `min_vocal_rms`），拦截纯音乐或无人声轨道。
5. **分语种逐字强制对齐**（在分离后的人声轨上按句切片运行）：
   - **中文（`zh`）**：`pypinyin` + `polyphone.toml` 词表转拼音 → 在主环境用纯 PyTorch（CUDA/CPU）直接运行 **SOFA** 歌声模型对齐。
   - **日文（`ja`）**：`fugashi`（MeCab + unidic）切分注音，结合人声能量包络避开上句拖音 → 运行 **NextFire MMS** 日语歌声模型对齐。
   - **英文（`en`）及其他**：24s 长滑窗全上下文计算发射概率 → 运行 **Echora-300M MMS** 多语歌词专精模型对齐，按 `en_granularity`（`word` / `char`）切分粒度，并按人声能量包络做长音时长保真。
6. **回填译文与输出**：挂载对应中文译文行，写入 `[ti:]` / `[ar:]` / `[al:]` / `[by:]` 元信息并按统一格式输出。

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

只需**单个 Python 环境**即可运行全部功能（SOFA 推理已剥离 `lightning` / `librosa<0.10` / `matplotlib` / `pandas` / `textgrid` 等训练期旧依赖，直接复用主环境 PyTorch + torchaudio 并在 GPU 上推理）：

```bash
# 1) Python 依赖（支持 Python 3.10~3.13 + NumPy 2.x）
pip install torch torchaudio transformers faster-whisper demucs pypinyin fugashi unidic-lite pykakasi av numpy pyyaml numba mutagen

# 2) 模型
python scripts/fetch_models.py
```

`python -m kiralrc info` 可以检查路径和模型是否齐备。

## 桌面端 GUI（Windows）

除命令行外，项目还提供基于 Go 编写的桌面端 GUI（`kiralrc-gui.exe`），包含**曲库**、**新建对齐**、**任务队列**、**编辑与校对**、**工具箱**五个面板，支持音频试听、批量任务队列、逐字时间轴校对以及 `polyphone.toml` 多音字词表编辑。

- **直接运行**：从 [Releases](https://github.com/kato-megumi-0923/Kiralrc/releases) 下载 `kiralrc-gui-v0.2.0-windows-amd64.zip` 解压后运行 `kiralrc-gui.exe`（需本地已配置好 Python 对齐环境与模型）。
- **从源码编译运行**（需要 Go 1.27+）：

```bash
go build -ldflags="-H windowsgui" -o kiralrc-gui.exe .
```

### 主要功能

- **曲库管理**：扫描本地音乐目录并统计歌词覆盖状态（无歌词 / 整行 / 逐字），支持一键批量升级整行歌词、全网重试搜词、批量填写网易云歌曲 ID、直接 ASR 转录，以及单曲手工粘贴/导入/调用记事本编辑参考歌词。
- **新建对齐**：支持单曲或整文件夹导入，灵活配置语种、参考 LRC、网易云 ID、强制 ASR 重定位及英文逐字粒度等选项。
- **任务队列**：实时显示 6 步对齐流水线进度、状态与提示信息，对齐完成后可一键打开校对。
- **编辑与校对**：内置音频播放与逐字同步高亮预览（支持专辑封面背景）、时间轴微调（`±50ms` 等）、格式校验、双语加 ❤️ 及保存/放弃修改。
- **工具箱与设置**：提供模型与环境状态检测、`polyphone.toml` 多音字词表编辑、中间产物缓存清理及常用工具快捷执行。

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

## 版本更新内容

以下按版本与演进阶段汇总自仓库创建以来的全部主要更新（历史坑点与设计背景另见 [`docs/known-issues.md`](docs/known-issues.md)）：

### v0.2.0（2026-11-23）
- **AI 歌词翻译与安全存储**：GUI 新增 OpenAI 兼容 AI 翻译功能（内置 DeepSeek / OpenAI / Gemini / 硅基流动 / 智谱 GLM / 通义千问 / OpenRouter / 本地 Ollama 预设），API Key 在 Windows 上通过 DPAPI 加密存储。
- **双语译文识别与补全增强**：按语种文字体系精准识别双语译文对，支持逐行检测缺失译文、合并多源译文与自动补全回填。
- **逐字参考迁移与多源搜索增强**：支持逐字参考 LRC 时间迁移与日语碰撞检测；新增可选 Apple Music 官方歌词源与 AMLL 复用网易云候选搜索；HuggingFace / Torch 模型缓存统一收拢至项目 `models/` 目录；Windows 播放器增强音频格式兼容。

### 阶段四：全面切换 MMS 歌声/歌词模型 & 内嵌 SOFA（2026-11-22）
- **内嵌 SOFA 推理**：将 SOFA 推理直接整合进 `kiralrc/sofa.py`，移除独立的 `.venv-sofa` 环境与 `tools/run_sofa.py` 老依赖。
- **全面移除 WhisperX**：
  - 日文（`ja`）采用 **NextFire MMS** 日语歌声模型，结合 `fugashi`（MeCab / UniDic）词法读音与人声能量包络换气低谷检测，避免切片吞入上句拖音。
  - 英文（`en`）及其他多语种采用 **Echora-300M MMS** 多语歌词专精模型，配合 24s 长滑窗上下文、通配符边界对齐与人声能量包络长音保真。
- **无词跳过与兜底增强**：新增 `NoLyricsError` 与 `skipped_nolyrics` 状态（GUI 队列显示「全网无词 · 已跳过」），拦截空对齐结果防止写入空 LRC，并优化 `--asr-fallback` 听写兜底分行。

### 阶段三：多源歌词拉取与相似度匹配（2026-11-22）
- **多平台顺序兜底**：接入 **网易云 → QQ音乐 → 酷狗音乐 → LRCLIB** 多源搜索回退。
- **精准匹配与校验**：支持按歌名/歌手/时长综合打分（缺歌手时权重归一化）、自动跳过空歌词候选、按单曲网易云 ID 精准取词，并在 GUI 任务队列中按单曲隔离 `NeteaseID`。

### 阶段二：Windows 桌面端 GUI 与 KI-1~KI-7 修复（2026-11-22）
- **Go 桌面端 GUI（`kiralrc-gui.exe`）**：新增曲库管理、新建对齐、任务队列、编辑与校对、工具箱五大面板，支持音频试听、批量入队、时间轴微调与快捷剪贴板操作。
- **已知问题修复（KI-1 ~ KI-7）**：新增人声主唱检测、剪辑版缺失行自动剔除、非中文长音能量包络保真、`polyphone.toml` 多音字自定义词表、英文 `word`/`char` 粒度切换；SOFA 跳过 `【...】` 演唱者标注；修复 GUI 单字逐字行判定与队列状态更新错位。

### 阶段一：项目工程化与核心 Bug 修复（2026-11-21）
- **架构整理与开源**：完成项目包化、统一 CLI 入口、确定 **Kiralrc** 名称，补充 MIT 许可与占位示例。
- **核心修复**：修复多时间戳行解析、双语译文混入、文件名搜索词提取与程序构造 `Line` 丢失逐字标签的问题，并建立 `docs/known-issues.md`。

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
  sofa.py          内嵌 SOFA 推理与结果拼装
  jp.py            日文（NextFire MMS）与英文/多语（Echora-300M MMS）逐字对齐
  asr.py           faster-whisper + 歌词行↔ASR 分段的单调 DP
  audio.py         解码（PyAV）、demucs 分离、能量包络、主唱检测
  text.py          多源抓歌词、拼音、多音字词表处理
  format.py        LRC 解析/格式化/校验/小数位转换
  hearts.py        双语加 ❤️
  legacy/          早期的一次性脚本（保留作参照，不再维护）
third_party/SOFA/  SOFA 词典与参考源码（MIT，已去 .git）
models/            CT2 whisper + SOFA ckpt
scripts/           模型下载、环境安装、批量抓歌词（fetch_lyrics_dir.py）
```

## 代码来源

| 部分 | 来源 | 说明 |
|---|---|---|
| `kiralrc/`、`tests/`、`scripts/`、`config.toml`、`polyphone.toml`、`main.go`、`internal/` | **本项目原创** | Python 对齐核心 + Go 桌面端 GUI |
| `third_party/SOFA/` | [qiuqiao/SOFA](https://github.com/qiuqiao/SOFA)，MIT © 2023 suco | 保留词典与原版 LICENSE，推理已内嵌到 `kiralrc/sofa.py` |
| `models/` | 本地下载（不提交到 Git） | CT2 whisper：`Systran/faster-whisper-large-v3`（经 ModelScope 镜像）<br>SOFA 权重：官方 Release `v1.0.0_mandarin_singing.ckpt` |
| 依赖库 | PyPI | faster-whisper / demucs / transformers / PyAV / pypinyin / torch … |
| `kiralrc/legacy/` | 本项目早期版本 | 一次性脚本，保留作参照，不再维护 |

## 许可

本项目代码以 MIT 发布（见 `LICENSE`）。`third_party/SOFA` 为其原作者的 MIT 代码（见其 `LICENSE`），
模型权重版权归各自作者。

仓库里**不包含**任何歌曲音频或歌词文本；`examples/` 只有占位示例。
