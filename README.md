<div align="center">

# Kiralrc

**面向本地音乐曲库的逐字（Enhanced LRC）歌词管理、下载与歌声对齐工具**

[![Version](https://img.shields.io/badge/version-v0.2.0--beta-e53935?style=flat-square)](https://github.com/kato-megumi-0923/Kiralrc/releases)
[![Platform](https://img.shields.io/badge/platform-Windows%2010%2F11%20x64-0078d4?style=flat-square)](#快速开始)
[![License](https://img.shields.io/badge/license-MIT-2e8b57?style=flat-square)](LICENSE)

优先直下平台官方逐字歌词，冷门缺词再上人声轨歌声模型逐字精算，<br/>
一站式打通 **NCM 解密 → 曲库管理 → 官方取词 → 歌声对齐 → 波形校对 → AI 翻译**。

</div>

---

<p align="center">
  <img src="docs/screenshots/library.png" alt="曲库总览" width="49%" />
  <img src="docs/screenshots/editor.png" alt="编辑与校对" width="49%" />
</p>

## 为什么做 Kiralrc？

整理本地无损曲库时，歌词往往卡在四个痛点上：
1. **网易云 NCM 解密后丢 ID**：只转了音频格式，后续搜歌词还得靠文件名盲猜；
2. **盲目跑模型费时费显卡**：明明平台本身就有现成的官方逐字词，却被一锅端扔进模型重算；
3. **冷门曲 / Live / 翻唱没逐字**：传统 ASR（如 Whisper）在歌声上行首偏早、长拖腔被压扁；
4. **冷门外语歌没译文**：好不容易对上了日文/英文时间轴，听歌时却缺一份自然流畅的中文翻译。

Kiralrc 用一条完整的工作流解决这四个问题：

```text
  ① NCM 解密留 ID  ──▶  ② 优先直下官方逐字  ──▶  ③ 人声轨歌声模型兜底  ──▶  ④ 波形校对 & AI 翻译
  还原标签/封面/ID       网易云/QQ/AMLL/酷狗      HubertFA / NextFire / Echora     拖拽微调 · 一键补译文
```

---

## 核心特性

### 1. 本地曲库总览 & 智能分类
- 原生读取 FLAC / MP3 内嵌标签与专辑封面，自动识别歌手与歌名，不再受限于文件命名格式。
- 自动统计歌词覆盖率，精准区分 **官方逐字 / 对齐逐字 / 仅整行 / 无歌词**，并自动排除纯音乐、OST 原声与伴奏轨。
- **曲库分段加载**：按页读取（每页 50 / 100 条可切），上千首也不卡在首屏；**封面按需解码 + 固定容量 LRU**（400 张，约 26MB，最久未用的先淘汰），翻页不重复读盘，也不会把位图囤满内存。

### 2. 优先直下平台官方逐字歌词（不烧显卡）
- 优先解析音频内嵌的网易云 `163 key` 与 `NETEASE_ID` 标签，绕过模糊搜索直连原曲。
- 按 **网易云（YRC）→ QQ 音乐（QRC）→ AMLL（TTML）→ 酷狗（KRC）** 顺序抓取官方逐字与双语译文；已有对齐版时用官方逐字直接覆盖（不留备份）。

### 3. 人声轨分语种歌声强制对齐
对平台仅有整行或无逐字的曲目，先用 `demucs (htdemucs)` 分离纯净人声轨（内置 1–4kHz 主唱能量检测，自动跳过无人声轨），再按语种调用专门的歌声对齐模型：

| 语种 | 默认对齐模型 | 特色处理 |
| :--- | :--- | :--- |
| **中文 (`zh`)** | **HubertFA v0.0.7**（ONNX，可选回退 SOFA） | `pypinyin` + `polyphone.toml` 多音字自定义词表，支持「鬼畜/波形起音优先」模式 |
| **日文 (`ja`)** | **NextFire MMS**（日语卡拉 OK 歌声模型） | `fugashi`（MeCab / UniDic）精确语境读音，自动处理助词读音与长拖腔 |
| **英文 (`en`) 及多语** | **Echora-300M MMS**（多语歌词专精模型） | 24s 长滑窗上下文 + 人声能量包络长音保真，支持按词 / 按字母粒度切换 |

> 全网无词的极端冷门曲，也支持在曲库内 **批量填写网易云 ID**、**手动粘贴/导入参考词**，或启用 **本地 ASR 人声听写兜底**（`faster-whisper-large-v3` / 中文 `FireRedASR-AED-L`）。

### 4. 可视化编辑校对 & 一键 AI 歌词翻译
- **沉浸式校对台**：黑胶封面律动预览、原曲/人声轨双波形对照、逐字时间块直接拖拽微调、`±50ms` 整体偏移、LRC 源码直编，以及人声波形异常行一键定位。
- **AI 整首翻译**：面向缺译文的外语歌，支持 OpenAI 兼容接口（内置 DeepSeek、OpenAI、Gemini、硅基流动、智谱 GLM、通义千问、OpenRouter、本地 Ollama 预设），整首结合上下文翻译并严格对齐行数；API Key 在 Windows 上经 **DPAPI 加密存储**。

### 5. 实用工具箱
- **NCM 加密音频解密**：还原无损 MP3 / FLAC，完整写回歌名、歌手、专辑、封面与 `NETEASE_ID`（QQ 的 `.qmc` / `.mflac` / `.mgg` 暂不支持，会明确提示）。
- **独立人声分离页**：实时进度条、分离出人声与伴奏双轨、自动标出首次起声点、支持页面内试听并导出为 **FLAC / WAV / MP3**。
- **格式批处理**：逐字 LRC 规范校验、双语译文行一键加 ❤️、两位/三位小数时间戳批量转换。
- **标签刮削**：并排摆出网易云与 iTunes 的候选封面，挑准版本后批量补高清封面、专辑年份与 `NETEASE_ID`。支持**按专辑**（同一张专辑只取一次曲目表、封面只下一次，版本/时长对得上才写）、**按 ID**（文件里已有 ID 的直接补全）与**按网易云**（歌名+歌手+专辑严格匹配）三条批量路径，字段与「仅补缺失 / 覆盖选中」都可控；遇到限速会暂停并从当前曲目/当前专辑续跑。
- **清理歌词文件**：选定目录后按格式勾选（`.lrc` / `.vtt` / `.ttml` / `.srt` / `.ass` / `.ssa` / `.txt`），**默认只移入回收站**——回收站报错或系统对话框被取消时，没处理的文件原样保留，界面会如实报「成了几个 / 剩几个 / 为什么」；**永久删除是独立入口**，需要再确认一次（不可恢复）。删完自动刷新曲库状态。

---

## 快速开始

### 硬件与环境说明
- **操作系统**：Windows 10 / 11 (x64)
- **仅使用「官方取词 / NCM 解密 / 编辑校对 / AI 翻译」**：普通电脑即可流畅运行。
- **使用「人声分离与模型逐字对齐」**：推荐 **NVIDIA 显卡（CUDA，≥6GB 显存）**，并预留约 **6~8GB** 磁盘空间存放模型权重；无 NVIDIA 显卡时可自动回退至 CPU 运行（速度较慢）。

### 方式一：下载便携版（推荐）
1. 前往 [Releases](https://github.com/kato-megumi-0923/Kiralrc/releases) 下载 `kiralrc-gui-v0.2.0-windows-amd64.zip` 并解压。
2. 双击运行 `kiralrc-gui.exe`。
3. 首次启动会自动检测本机 Python 环境；若缺少依赖或模型，前往 **「设置 → 模型与运行环境」** 点击：
   - **一键安装/补全运行环境**（自动创建独立 `.venv` 并适配 CUDA / CPU 版 PyTorch）
   - **一键下载基础模型**（自动拉取 `faster-whisper-large-v3` 与 `HubertFA` 中文模型；日/英 MMS 模型将在首次对齐对应语种时自动下载至 `models/` 目录）

### 方式二：从源码运行
需要 **Go 1.27+** 与 **Python 3.10 ~ 3.13**：

```bash
# 1. 安装 CUDA 版 PyTorch（NVIDIA 显卡请带 cu124 源；无 N 卡可去掉 --index-url）
pip install torch torchaudio --index-url https://download.pytorch.org/whl/cu124

# 2. 安装 Python 依赖
pip install transformers faster-whisper demucs pypinyin fugashi unidic-lite pykakasi av numpy pyyaml numba mutagen scipy soundfile onnxruntime

# 3. 下载基础模型（如需额外下载可选 SOFA 权重，追加 --with-sofa）
python scripts/fetch_models.py

# 4. 编译并启动桌面端 GUI
go build -ldflags="-H windowsgui" -o kiralrc-gui.exe .
./kiralrc-gui.exe
```

---

## 输出格式与播放器兼容性

Kiralrc 统一输出标准的 **Enhanced LRC（增强型逐字 LRC）**：

```lrc
[ti:示例曲目]
[ar:示例歌手]
[by:HubertFA 歌声对齐]
[kiralrc:aligned]
[00:06.232]<00:06.232>走<00:07.115>在<00:07.440>乡<00:07.820>间<00:08.510>
```

- 统一 **UTF-8 无 BOM**、**三位小数时间戳**（`mm:ss.SSS`），且每行末尾自带**闭合结束标签**（防止播放器在末字起唱瞬间提前切字）。
- **完美支持**：椒盐音乐（Salt Player）、Foobar2000 逐字插件、MusicFree、Oto Music、Cloudy 等支持 Enhanced LRC 的本地播放器。
- **不兼容提示**：网易云音乐官方 App 的本地歌词解析器仅支持传统整行 LRC，不支持 `<mm:ss.xxx>` 行内标签，直接导入会把尖括号时间戳原样显示在屏幕上。

---

## 进阶说明与命令行用法

<details>
<summary><b>点击展开：为什么不用纯 Whisper / WhisperX 做逐字对齐？</b></summary>

本机实测对比：

| 方案 | 实测表现 |
| :--- | :--- |
| faster-whisper 词级时间 + 均摊 | 同音字误识多，且歌声行首普遍 **偏早 0.5 ~ 1.2 秒** |
| WhisperX（wav2vec2 语音 CTC） | 短音尚可，但 **长拖腔被严重压扁**（实测 5.27s 拖腔被压成 2.47s） |
| **HubertFA / SOFA（中文歌声专精）** | 专为歌声训练，长音准确、句内节奏自然，与人声起音差 < 0.1s |
| **NextFire / Echora MMS（日/英/多语歌声）** | 针对歌声与歌词微调，配合 24s 长滑窗与人声能量包络保真长音 |

> 另：切勿拿网易云官方整行 LRC 当对齐校验基准——它常对应另一版母带（前导静音差 0.5~1s）。Kiralrc 统一以**本地音频分离出的人声轨能量包络**为准。

</details>

<details>
<summary><b>点击展开：命令行（CLI）常用指令</b></summary>

```bash
# 批量下载官方逐字歌词（网易云 → QQ → AMLL → 酷狗，不跑模型）
python -m kiralrc download "D:\Music" --jobs 2

# 解密网易云 NCM 音频（保留原标签、封面与 NETEASE_ID）
python -m kiralrc decrypt "D:\Music"

# 单曲音频 → 逐字 LRC（中文默认 HubertFA；日文 --lang ja，英文 --lang en）
python -m kiralrc align "歌曲.flac" --ti 曲名 --ar 歌手

# 独立人声分离（产物保存在 work/sep/，不污染曲库）
python -m kiralrc separate "歌曲.flac"

# 格式校验 / 双语译文加 ❤️ / 两位转三位小数 / 环境自检
python -m kiralrc validate "歌曲.lrc"
python -m kiralrc hearts "歌曲.lrc"
python -m kiralrc convert "歌曲.lrc"
python -m kiralrc info
```

切换中文对齐器为 SOFA：在 `config.toml` 的 `[defaults]` 中将 `zh_aligner` 设为 `"sofa"`，并运行 `python scripts/fetch_models.py --with-sofa`。

</details>

<details>
<summary><b>点击展开：项目结构与开源致谢</b></summary>

```text
main.go            Windows 桌面端 GUI 入口
internal/app/      Go 桌面端界面、任务队列、音频播放器、DPAPI 安全存储与 AI 翻译
kiralrc/           Python 核心包（官方取词、NCM 解密、HubertFA/SOFA/MMS 对齐、人声分离与校验）
config.toml        路径与默认参数配置
polyphone.toml     中文多音字自定义读音词表
scripts/           模型下载（fetch_models.py）与便携打包（build_release.ps1）
models/            本地模型与 HuggingFace / Torch 统一缓存目录（不提交 Git）
```

**致谢与代码来源**：
- [MyGo](https://github.com/nya-owo/mygo)（MIT）：Go 原生桌面 UI 框架，窗口、绘制、控件与音频输出都由它支撑（`internal/app/` 全部界面代码）。
- [mewkiz/flac](https://github.com/mewkiz/flac)、[hajimehoshi/go-mp3](https://github.com/hajimehoshi/go-mp3)（均为 MIT）：曲库扫描时直读 FLAC / MP3 标签。
- [HubertFA v0.0.7](https://github.com/wolfgitpr/HubertFA)（Apache-2.0）：中文 ONNX 歌声强制对齐核心与推理代码（`kiralrc/_hubertfa/`）。
- [qiuqiao/SOFA](https://github.com/qiuqiao/SOFA)（MIT）：中文拼音词典与可选歌声对齐模型（`third_party/SOFA/`）。
- [NextFire MMS](https://huggingface.co/NextFire/mms-300m-ForcedAligner-karaoke-ja-Latn) & [Echora-300M MMS](https://huggingface.co/hcX02/echora-mms-300m-multilingual-lyrics-forced-aligner)：日/英/多语歌声歌词强制对齐模型。
- [Demucs](https://github.com/facebookresearch/demucs)、[faster-whisper](https://github.com/SYSTRAN/faster-whisper) 与 [amll-ttml-db](https://github.com/amll-dev/amll-ttml-db)。

</details>

<details>
<summary><b>点击展开：版本更新记录</b></summary>

### 未发布：标签刮削与批量补全（2026-11-24）
- **标签刮削（新）**：双栏工作台——左栏按「全部 / 无封面 / 低清 / 缺 ID·年份」筛，右栏并排看网易云与 iTunes 候选，手动挑版本（默认不预选），底栏控制写入字段（默认只补高清封面、专辑年份、`NETEASE_ID`）与策略（仅补缺失 / 覆盖选中）。
- **三条批量路径**：按专辑（专辑 ID 分组，一张专辑查一次、封面下一次，歌名/版本/歌手/时长对得上才写，多条命中留手选）、按 ID（已有 `NETEASE_ID` 直接补全）、按网易云（严格匹配后写入）。
- **写入安全**：只改勾选项、原标签对象上增量写（`163 key` / `REPLAYGAIN` / 封底图都留着）；封面必须能解码且不比本地小才写；限速统一冷却，暂停后从当前曲目/当前专辑自动续跑（可取消，连续三次即停）。
- **曲库分段加载 & 封面 LRU**：曲库按页读取（50 / 100 条可切），封面按需解码并做 400 张固定容量 LRU，翻页与长列表不再越用越吃内存。
- **清理歌词文件更保守**：不再在回收站失败后改走直接删除；没处理的文件原样保留并如实报结果，永久删除独立成第二个入口（二次确认）。

### v0.2.0-beta（2026-11-23）
- **优先直下官方逐字**：新增 `download` 流程与曲库直下入口，支持按标签 ID（`163 key` / `NETEASE_ID`）及精准匹配从网易云 / QQ / AMLL / 酷狗获取官方逐字歌词。
- **中文默认切换 HubertFA v0.0.7**：ONNX 推理开箱即用，保留 SOFA 作为可选回退。
- **NCM 加密音频解密**：内置解密工具，完整保留封面、元信息与网易云歌曲 ID。
- **独立人声分离页**：支持分离进度展示、人声/伴奏双轨试听、首次起声定位及 FLAC / WAV / MP3 导出。
- **AI 歌词翻译 & DPAPI 加密**：编辑页新增整首 AI 翻译，内置 8 家主流服务商与本地 Ollama 预设，API Key 经 Windows DPAPI 加密存储。
- **清理歌词文件**：新增独立清理页，按扩展名分组统计并勾选，二次确认后批量**移入回收站**；回收站报错或取消时文件原样保留（永久删除是单独的二次确认入口），删完自动重扫曲库。
- **曲库与体验增强**：原生读取 FLAC / MP3 标签与内嵌封面、常驻 Python Worker 复用、便携运行环境一键初始化、人声波形校验点击跳转。

</details>

---

## 许可

本项目原创代码以 [MIT License](LICENSE) 开源。`kiralrc/_hubertfa/` 遵循上游 Apache-2.0 许可，`third_party/SOFA/` 遵循上游 MIT 许可，各模型权重版权归原作者所有。仓库内不包含任何受版权保护的音频或歌词文件。