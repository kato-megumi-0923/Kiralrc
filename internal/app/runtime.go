package app

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
)

type pythonEnvInfo struct {
	Path      string   // 解析到的 python.exe 路径
	Source    string   // "自定义" / "内置 .venv" / "本机环境" / "未检测到"
	Version   string   // 例如 "3.12.7"
	Ready     bool     // 所有核心依赖均已就绪
	Missing   []string // 缺失的核心依赖模块名
	HasGPU    bool     // 是否检测到 NVIDIA GPU
	ErrorText string   // 探测错误摘要
}

var (
	activePyMu   sync.RWMutex
	activePyPath string
)

func setActivePythonPath(p string) {
	p = strings.TrimSpace(p)
	if p == "" {
		return
	}
	activePyMu.Lock()
	activePyPath = p
	activePyMu.Unlock()
}

func getActivePythonPath() string {
	activePyMu.RLock()
	p := activePyPath
	activePyMu.RUnlock()
	if p != "" {
		return p
	}
	return resolvePythonPath("")
}

// appRootDir 定位项目/程序根目录：优先检查 exe 所在目录是否包含 kiralrc/ 或 config.toml，否则回退当前工作目录。
func appRootDir() string {
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		if hasKiralrcFiles(dir) {
			return dir
		}
	}
	if wd, err := os.Getwd(); err == nil && hasKiralrcFiles(wd) {
		return wd
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Dir(exe)
	}
	wd, _ := os.Getwd()
	return wd
}

func hasKiralrcFiles(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, "kiralrc", "__init__.py")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(dir, "config.toml")); err == nil {
		return true
	}
	return false
}

// candidatePythons 返回按优先级排序的 Python 候选列表（路径 + 来源标签）。
func candidatePythons(customPath string) [][2]string {
	var out [][2]string
	seen := make(map[string]bool)
	add := func(p, src string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		key := strings.ToLower(filepath.Clean(p))
		if seen[key] {
			return
		}
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			seen[key] = true
			out = append(out, [2]string{filepath.Clean(p), src})
		}
	}

	// 1) 用户手动指定或环境变量覆盖
	if customPath != "" {
		add(customPath, "自定义")
	}
	if envPy := strings.TrimSpace(os.Getenv("KIRALRC_PYTHON")); envPy != "" {
		add(envPy, "环境变量")
	}

	// 2) 程序根目录自带的隔离虚拟环境 .venv
	root := appRootDir()
	add(filepath.Join(root, ".venv", "Scripts", "python.exe"), "内置 .venv")

	// 3) PATH 中已安装的 python / python3
	for _, name := range []string{"python", "python3"} {
		if lp, err := exec.LookPath(name); err == nil {
			// 排除 WindowsApps 的 Store 占位壳
			if !strings.Contains(strings.ToLower(lp), `\windowsapps\`) {
				add(lp, "本机环境")
			}
		}
	}

	// 4) 常见本机 Python 安装目录（含 LocalAppData 下常见版本与内置发行版）
	if localApp := os.Getenv("LOCALAPPDATA"); localApp != "" {
		for _, pat := range []string{
			filepath.Join(localApp, "Programs", "Python", "Python3*", "python.exe"),
			filepath.Join(localApp, "Programs", "WorkBuddy", "resources", "vendor", "python", "python.exe"),
		} {
			if matches, err := filepath.Glob(pat); err == nil {
				for _, m := range matches {
					add(m, "本机环境")
				}
			}
		}
	}
	return out
}

type pyProbeResult struct {
	Version string   `json:"version"`
	Missing []string `json:"missing"`
}

func probePython(pyPath string) (pyProbeResult, error) {
	code := `import importlib.util, json, sys
req = ["torch", "torchaudio", "numpy", "av", "faster_whisper", "demucs", "transformers", "pypinyin", "fugashi", "unidic_lite", "yaml", "pykakasi", "onnxruntime"]
miss = [m for m in req if importlib.util.find_spec(m) is None]
print(json.dumps({"version": f"{sys.version_info.major}.{sys.version_info.minor}.{sys.version_info.micro}", "missing": miss}))`
	cmd := newHiddenCmd(pyPath, "-X", "utf8", "-c", code)
	cmd.Dir = appRootDir()
	cmd.Env = append(os.Environ(), "PYTHONUTF8=1", "PYTHONIOENCODING=utf-8")
	out, err := cmd.Output()
	if err != nil {
		return pyProbeResult{}, err
	}
	var res pyProbeResult
	if err := json.Unmarshal(bytes.TrimSpace(out), &res); err != nil {
		return pyProbeResult{}, err
	}
	return res, nil
}

func hasNvidiaGPU() bool {
	cmd := newHiddenCmd("nvidia-smi", "--query-gpu=name", "--format=csv,noheader")
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}

// detectPythonEnv 分层探测可用的 Python 环境：优先复用已装齐依赖的环境，否则返回最佳候选及缺失模块列表。
func detectPythonEnv(customPath string) pythonEnvInfo {
	cands := candidatePythons(customPath)
	gpu := hasNvidiaGPU()
	var fallback *pythonEnvInfo

	for _, c := range cands {
		res, err := probePython(c[0])
		if err != nil {
			continue
		}
		info := pythonEnvInfo{
			Path:    c[0],
			Source:  c[1],
			Version: res.Version,
			Ready:   len(res.Missing) == 0,
			Missing: res.Missing,
			HasGPU:  gpu,
		}
		// 若用户明确指定了自定义路径，直接以其为准
		if c[1] == "自定义" {
			return info
		}
		// 一旦命中完整就绪的环境（无论是内置 .venv 还是本机环境），立即复用
		if info.Ready {
			return info
		}
		if fallback == nil {
			cp := info
			fallback = &cp
		}
	}

	if fallback != nil {
		return *fallback
	}
	return pythonEnvInfo{
		Source:    "未检测到",
		Ready:     false,
		HasGPU:    gpu,
		ErrorText: "未在本机检测到可用的 Python 环境",
	}
}

// resolvePythonPath 返回当前最适合执行 kiralrc 的 Python 可执行文件路径。
func resolvePythonPath(customPath string) string {
	info := detectPythonEnv(customPath)
	if info.Path != "" {
		return info.Path
	}
	return "python"
}

func (a *app) pythonExe() string {
	if p := strings.TrimSpace(a.envInfo.Path); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	p := resolvePythonPath(a.setCustomPython)
	setActivePythonPath(p)
	return p
}

// ensureUV 定位或自动下载单文件 uv.exe（约 30 MB）到 <root>/tools/uv.exe。
func ensureUV(root string, logFn func(string)) (string, error) {
	for _, cand := range []string{
		filepath.Join(root, "tools", "uv.exe"),
		filepath.Join(root, "uv.exe"),
	} {
		if st, err := os.Stat(cand); err == nil && !st.IsDir() {
			return cand, nil
		}
	}
	if lp, err := exec.LookPath("uv"); err == nil {
		return lp, nil
	}

	toolsDir := filepath.Join(root, "tools")
	if err := os.MkdirAll(toolsDir, 0o755); err != nil {
		return "", err
	}
	uvDst := filepath.Join(toolsDir, "uv.exe")

	urls := []string{
		"https://gh-proxy.com/https://github.com/astral-sh/uv/releases/latest/download/uv-x86_64-pc-windows-msvc.zip",
		"https://github.com/astral-sh/uv/releases/latest/download/uv-x86_64-pc-windows-msvc.zip",
	}
	client := &http.Client{Timeout: 120 * time.Second}
	var lastErr error
	for _, u := range urls {
		logFn("正在下载轻量环境管理器 uv.exe ...")
		resp, err := client.Get(u)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			continue
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			lastErr = err
			continue
		}
		for _, f := range zr.File {
			if strings.EqualFold(filepath.Base(f.Name), "uv.exe") {
				rc, err := f.Open()
				if err != nil {
					lastErr = err
					break
				}
				tmp := uvDst + ".tmp"
				out, err := os.Create(tmp)
				if err != nil {
					rc.Close()
					return "", err
				}
				_, copyErr := io.Copy(out, rc)
				out.Close()
				rc.Close()
				if copyErr != nil {
					_ = os.Remove(tmp)
					lastErr = copyErr
					break
				}
				if err := os.Rename(tmp, uvDst); err != nil {
					return "", err
				}
				logFn("uv.exe 已就绪：" + uvDst)
				return uvDst, nil
			}
		}
	}
	return "", fmt.Errorf("自动下载 uv.exe 失败：%v", lastErr)
}

func runStreamedCmd(cmd *exec.Cmd, logFn func(string)) error {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return err
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 512*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line != "" {
			logFn(line)
		}
	}
	return cmd.Wait()
}

// bootstrapEnv 一键创建内置 .venv 并安装 PyTorch（自动适配 CUDA/CPU）及全部核心依赖。
func (a *app) bootstrapEnv() {
	if a.envInstalling {
		return
	}
	a.envInstalling = true
	a.envInstallLog = "开始初始化 Kiralrc Python 运行环境...\n"
	a.toast = "正在初始化运行环境，请稍候…"
	envExtra := a.pythonSubprocessEnv()

	appendLog := func(line string) {
		a.post(func() {
			if len(a.envInstallLog) > 16000 {
				a.envInstallLog = a.envInstallLog[len(a.envInstallLog)-12000:]
			}
			a.envInstallLog += line + "\n"
		})
	}

	go func() {
		defer a.post(func() {
			a.envInstalling = false
		})

		root := appRootDir()
		uvExe, err := ensureUV(root, appendLog)
		if err != nil {
			appendLog("[错误] " + err.Error())
			a.post(func() { a.toast = "环境初始化失败：" + err.Error() })
			return
		}

		venvDir := filepath.Join(root, ".venv")
		venvPy := filepath.Join(venvDir, "Scripts", "python.exe")
		if _, err := os.Stat(venvPy); err != nil {
			appendLog("步骤 1/3：创建独立虚拟环境 (.venv, Python 3.11)...")
			cmd := newHiddenCmd(uvExe, "venv", venvDir, "--python", "3.11")
			cmd.Dir = root
			cmd.Env = append(envExtra,
				"UV_PYTHON_INSTALL_MIRROR=https://gh-proxy.com/https://github.com/astral-sh/python-build-standalone/releases/download",
			)
			if err := runStreamedCmd(cmd, appendLog); err != nil {
				appendLog("[错误] 创建虚拟环境失败：" + err.Error())
				a.post(func() { a.toast = "创建虚拟环境失败，请查看日志" })
				return
			}
		} else {
			appendLog("步骤 1/3：复用已有内置虚拟环境 " + venvPy)
		}

		hasGPU := hasNvidiaGPU()
		if hasGPU {
			appendLog("步骤 2/3：检测到 NVIDIA GPU，安装 PyTorch (CUDA 12.4)...")
			cmd := newHiddenCmd(uvExe, "pip", "install", "--python", venvPy,
				"torch", "torchaudio", "--index-url", "https://download.pytorch.org/whl/cu124")
			cmd.Dir = root
			cmd.Env = envExtra
			if err := runStreamedCmd(cmd, appendLog); err != nil {
				appendLog("[错误] 安装 PyTorch CUDA 失败：" + err.Error())
				a.post(func() { a.toast = "安装 PyTorch 失败，请查看日志" })
				return
			}
		} else {
			appendLog("步骤 2/3：未检测到 NVIDIA GPU，安装 PyTorch (CPU)...")
			cmd := newHiddenCmd(uvExe, "pip", "install", "--python", venvPy, "torch", "torchaudio")
			cmd.Dir = root
			cmd.Env = envExtra
			if err := runStreamedCmd(cmd, appendLog); err != nil {
				appendLog("[错误] 安装 PyTorch 失败：" + err.Error())
				a.post(func() { a.toast = "安装 PyTorch 失败，请查看日志" })
				return
			}
		}

		appendLog("步骤 3/3：安装核心依赖（demucs / faster-whisper / transformers / fugashi 等）...")
		pkgs := []string{
			"numpy>=1.24", "av>=12", "faster-whisper>=1.1", "demucs>=4.0",
			"transformers>=4.40", "pypinyin>=0.49", "fugashi>=1.3", "unidic-lite>=1.0.8",
			"scipy>=1.10", "soundfile>=0.12",
			"pyyaml", "pykakasi", "numba", "mutagen",
			"onnxruntime>=1.19",
		}
		args := append([]string{"pip", "install", "--python", venvPy}, pkgs...)
		cmd := newHiddenCmd(uvExe, args...)
		cmd.Dir = root
		cmd.Env = envExtra
		if err := runStreamedCmd(cmd, appendLog); err != nil {
			appendLog("[错误] 安装依赖失败：" + err.Error())
			a.post(func() { a.toast = "安装依赖失败，请查看日志" })
			return
		}

		appendLog("✓ Python 运行环境初始化完成！")
		a.post(func() {
			a.setCustomPython = ""
			a.savePrefs()
			a.toast = "运行环境已就绪，正在刷新模型状态…"
			a.refreshBackendInfo()
		})
	}()
}

// downloadBaseModels 调用 scripts/fetch_models.py 一键下载基础模型。
func (a *app) downloadBaseModels() {
	if a.envInstalling {
		return
	}
	py := a.pythonExe()
	root := appRootDir()
	script := filepath.Join(root, "scripts", "fetch_models.py")
	if _, err := os.Stat(script); err != nil {
		a.toast = "未找到 scripts/fetch_models.py"
		return
	}
	a.envInstalling = true
	a.envInstallLog = "开始下载基础模型（faster-whisper-large-v3 + HubertFA 中文模型）...\n"
	a.toast = "正在下载基础模型，请保持网络畅通…"
	envExtra := a.pythonSubprocessEnv("PYTHONUNBUFFERED=1")
	proxyAddr := ""
	if a.setProxyOn {
		proxyAddr = strings.TrimSpace(a.setProxyAddr)
	}

	appendLog := func(line string) {
		a.post(func() {
			if len(a.envInstallLog) > 16000 {
				a.envInstallLog = a.envInstallLog[len(a.envInstallLog)-12000:]
			}
			a.envInstallLog += line + "\n"
		})
	}

	go func() {
		defer a.post(func() {
			a.envInstalling = false
		})
		args := []string{"-X", "utf8", script}
		if proxyAddr != "" {
			args = append(args, "--proxy", proxyAddr)
		}
		cmd := newHiddenCmd(py, args...)
		cmd.Dir = root
		cmd.Env = envExtra
		if err := runStreamedCmd(cmd, appendLog); err != nil {
			appendLog("[错误] 下载模型失败：" + err.Error())
			a.post(func() { a.toast = "下载模型失败，请查看日志" })
			return
		}
		appendLog("✓ 基础模型下载完成！")
		a.post(func() {
			a.toast = "基础模型下载完成，正在刷新状态…"
			a.refreshBackendInfo()
		})
	}()
}

func (a *app) pickCustomPython() {
	if a.envInstalling {
		return
	}
	go func() {
		picked, err := chooseFile(a.win, "选择 Python 可执行文件 (python.exe)", "选择", []mygo.FileFilter{
			{Name: "Python 解释器", Extensions: []string{"exe"}},
		})
		if err != nil || strings.TrimSpace(picked) == "" {
			return
		}
		a.post(func() {
			a.setCustomPython = strings.TrimSpace(picked)
			a.savePrefs()
			a.toast = "已切换自定义 Python，正在检测…"
			a.refreshBackendInfo()
		})
	}()
}
