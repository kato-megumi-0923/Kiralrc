package app

import (
	"bufio"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type pageKind int

const (
	pageLibrary pageKind = iota
	pageAlign
	pageQueue
	pageEditor
	pageTools
	pageSettings
)

const (
	topBarHeight  = 40
	topEdgePad    = 6
	buttonsHeight = topBarHeight + topEdgePad // 46, matching Cloudy
	pageGap       = 12
)

type trackItem struct {
	Selected bool
	Title    string
	Artist   string
	File     string
	FullPath string
	LrcPath  string
	Duration string
	Status   string // "enhanced", "line_only", "missing", "inst_pure", "inst_ost", "inst_accomp"
	Lang     string
	Note     string
	Lines    int
	Hue      float32
}

type libStats struct {
	Total      int
	Vocal      int
	Enhanced   int
	LineOnly   int
	Missing    int
	InstTotal  int
	InstPure   int
	InstOST    int
	InstAccomp int
}

type lyricChar struct {
	Ch     string
	Pinyin string
	Start  float64
	End    float64
}

type lyricLine struct {
	Index       int
	Start       float64
	End         float64
	Text        string
	Translation string
	Status      string // "ok", "warn", "tr", "cut"
	Chars       []lyricChar
}

type queueItem struct {
	Title     string
	Artist    string
	Sub       string
	AudioPath string
	LrcPath   string
	Lang      string
	Status    string // "waiting", "running", "done", "skipped", "failed"
	Stage     int    // 0..6 live stage for this song
	Note      string
	Hue       float32
}

type modelStatusItem struct {
	Lang   string `json:"lang"`
	Name   string `json:"name"`
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
}

type recentItem struct {
	Name  string
	File  string
	Path  string
	Hue   float32
	Cover *ui.Bitmap
}

var chooseFolder = func(parent *mygo.Window, title, start string) (string, error) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent:            parent,
		Title:             title,
		DefaultPath:       start,
		ButtonLabel:       "选择文件夹",
		Directory:         true,
		CreateDirectories: true,
	})
	if err != nil || len(paths) == 0 {
		return "", err
	}
	return paths[0], nil
}

var chooseFile = func(parent *mygo.Window, title, button string, filters []mygo.FileFilter) (string, error) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent:      parent,
		Title:       title,
		ButtonLabel: button,
		Filters:     filters,
	})
	if err != nil || len(paths) == 0 {
		return "", err
	}
	return paths[0], nil
}

type app struct {
	win         *mygo.Window
	pal         palette
	page        pageKind
	pageDir     int
	accentID    string
	accentHex   string
	appearance  int // 0: 浅色, 1: 深色
	libPill     ui.Rect
	spinAngle   float32
	spinLast    time.Time
	sideWidthPx float32
	dockPos     float64
	edGlyphs    []lyricChar

	searchQuery string
	searchBox   ui.Handle
	recents     []recentItem

	// 1. 曲库（默认未添加文件夹，用户点击添加后再扫描）
	libFolders  []string
	libScanning bool
	libScanDone int
	libScanAll  int
	libScanTime string
	libStats    libStats
	libFilter   int // 0:有效人声 1:待升级逐字 2:逐字就绪 3:纯音乐/OST/伴奏
	libQuery    string
	tracks      []trackItem
	libScroll   ui.ScrollState

	// 2. 新建对齐
	alignAudio       string
	alignAudioPath   string
	alignLocalLrc    string
	alignRefLrc      string
	alignBatchDir       string
	alignBatchTracks    []trackItem
	alignIgnoreRefTimes bool
	alignSource         int // 0:优先本地LRC(无则搜网易云) 1:手动指定参考文件 2:强制搜网易云
	alignSearch    string
	alignCandidate int
	alignLang      int // 0:中文 1:日文 2:英文 3:其它
	alignPolyCheck bool
	alignSustain   bool
	alignEnMode    int // 0:按词 1:按字母均分
	alignTitle     string
	alignArtist    string
	alignAlbum     string
	alignOut       string
	alignDecimals  int // 0:3位 1:2位
	alignLead      string
	alignEndTag    bool
	alignKeepTrans bool
	alignAutoValid bool
	alignScroll    ui.ScrollState

	// 3. 任务队列
	queueJobs        int
	queueConcurrency int // 0: 1并发, 1: 2并发, 2: 3并发
	queueWorkers     int
	queueKeepGoing   bool
	queuePaused      bool
	queueRunning     bool
	queueActiveTitle string
	queueActiveSub   string
	queueStage       int // 1..6
	queuePct         float32
	queueLogs        []string
	queueItems       []queueItem
	queueScroll      ui.ScrollState

	// 4. 编辑与校对
	player         *audioPlayer
	volume         float64
	muted          bool
	preMuteVol     float64
	edFile         string
	edPath         string
	edAudioPath    string
	edCover        *ui.Bitmap
	edCoverAudio   string
	edMarqueeText  string
	edMarqueeStart time.Time
	edVinylDeg     float32
	edVinylLast    time.Time
	dockDur        float64
	playTickerOn   bool
	edSub          string
	edModified     bool
	edPreviewMode  int // 0:律动预览 1:LRC源码
	edTrackMode    int // 0:原曲 1:人声轨
	edSpeedMode    int // 0:0.5x 1:0.75x 2:1x
	edShowPinyin   bool
	edSnapOnset    bool
	edRipple       bool
	edPlaying      bool
	edPlayhead     float64
	edSelectedLine int
	edSelectedChar int
	edMetas        []string
	edLines        []lyricLine
	edIssues       []string
	edLineScroll   ui.ScrollState
	edSrcScroll    ui.ScrollState
	waveMix        []float32
	waveVoc        []float32
	waveKey        string

	// 5. 工具箱
	toolSelectedFile int
	toolLog          string
	toolScroll       ui.ScrollState

	// 6. 设置
	modelOK            [3]bool
	setAutoSkipNoVocal bool
	setVocalRatio      string
	setEnMode          int
	setFetchInterval   string
	setProxyOn         bool
	setProxyAddr       string
	setCacheSummary    string
	setGpuName         string
	setModels          []modelStatusItem
	setCleanDays       int // 0:7天前 1:30天前 2:全部
	setScroll          ui.ScrollState

	toast string
}

type userPrefs struct {
	QueueConcurrency int      `json:"queue_concurrency"`
	QueueKeepGoing   *bool    `json:"queue_keep_going,omitempty"`
	LibFolders       []string `json:"lib_folders,omitempty"`
	Appearance       int      `json:"appearance"`
	AccentID         string   `json:"accent_id,omitempty"`
	RecentPaths      []string `json:"recent_paths,omitempty"`
}

func prefsPath() string {
	if os.Getenv("KIRALRC_NO_PREFS") == "1" {
		return ""
	}
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		return ""
	}
	return filepath.Join(dir, "Kiralrc", "prefs.json")
}

func (a *app) loadPrefs() {
	p := prefsPath()
	if p == "" {
		return
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return
	}
	var up userPrefs
	if json.Unmarshal(raw, &up) != nil {
		return
	}
	if up.QueueConcurrency >= 0 && up.QueueConcurrency <= 2 {
		a.queueConcurrency = up.QueueConcurrency
	}
	if up.QueueKeepGoing != nil {
		a.queueKeepGoing = *up.QueueKeepGoing
	}
	if up.Appearance == 0 || up.Appearance == 1 {
		a.appearance = up.Appearance
	}
	if up.AccentID != "" {
		a.setAccent(up.AccentID)
	}
	for _, f := range up.LibFolders {
		if info, err := os.Stat(f); err == nil && info.IsDir() {
			a.libFolders = append(a.libFolders, f)
		}
	}
	a.libFolders = foldLibraryFolders(a.libFolders)
	for _, rp := range up.RecentPaths {
		if info, err := os.Stat(rp); err == nil && !info.IsDir() {
			base := filepath.Base(rp)
			stem := strings.TrimSuffix(base, filepath.Ext(base))
			a.recents = append(a.recents, recentItem{
				Name: stem,
				File: base,
				Path: rp,
				Hue:  hueForString(stem),
			})
		}
	}
}

func (a *app) savePrefs() {
	p := prefsPath()
	if p == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	kg := a.queueKeepGoing
	var rpaths []string
	for _, r := range a.recents {
		if r.Path != "" {
			rpaths = append(rpaths, r.Path)
		}
	}
	up := userPrefs{
		QueueConcurrency: a.queueConcurrency,
		QueueKeepGoing:   &kg,
		LibFolders:       a.libFolders,
		Appearance:       a.appearance,
		AccentID:         a.accentID,
		RecentPaths:      rpaths,
	}
	if data, err := json.MarshalIndent(up, "", "  "); err == nil {
		_ = os.WriteFile(p, data, 0o644)
	}
}

func newApp() *app {
	a := &app{
		player:             &audioPlayer{},
		volume:             0.50,
		preMuteVol:         0.50,
		dockDur:            180,
		page:               pageLibrary,
		accentID:           "red",
		accentHex:          "",
		libFilter:          0,
		alignSource:        0,
		alignLang:          0,
		alignPolyCheck:     true,
		alignSustain:       true,
		alignEnMode:        0,
		alignDecimals:      0,
		alignLead:          "0.000 s",
		alignEndTag:        true,
		alignAutoValid:     true,
		queueKeepGoing:     true,
		edTrackMode:        0,
		edSpeedMode:        2,
		edShowPinyin:       true,
		edSnapOnset:        true,
		sideWidthPx:        232,
		setAutoSkipNoVocal: true,
		setVocalRatio:      "0.08",
		setEnMode:          0,
		setFetchInterval:   "4.5 s",
		setProxyOn:         true,
		setProxyAddr:       "127.0.0.1:7890",
		setCacheSummary:    "0.0 MB · 0 项",
		setGpuName:         "正在检测显卡…",
		setModels: []modelStatusItem{
			{Lang: "通用", Name: "Demucs 人声分离（htdemucs）", Path: "~/.cache/torch/hub/checkpoints/955717e8-8726e21a.th", Exists: false},
			{Lang: "通用", Name: "faster-whisper large-v3（ASR 句级定位）", Path: "models/faster-whisper-large-v3", Exists: false},
			{Lang: "中文", Name: "SOFA 中文歌声对齐模型", Path: "models/sofa/v1.0.0_mandarin_singing.ckpt", Exists: false},
			{Lang: "中文", Name: "SOFA 独立运行环境（Python 3.10）", Path: ".venv-sofa/Scripts/python.exe", Exists: false},
			{Lang: "日文", Name: "MMS 日语卡拉 OK 歌声模型（NextFire）", Path: "~/.cache/huggingface/hub/models--NextFire--mms-300m-ForcedAligner-karaoke-ja-Latn", Exists: false},
			{Lang: "日文", Name: "fugashi + unidic-lite（日语词法读音）", Path: "Python 环境内置（MeCab / UniDic）", Exists: false},
			{Lang: "英文", Name: "MMS 英文/多语歌词模型（Echora-300M）", Path: "~/.cache/huggingface/hub/models--hcX02--echora-mms-300m-multilingual-lyrics-forced-aligner", Exists: false},
		},
	}
	a.loadPrefs()
	return a
}

func (a *app) setAccent(id string) {
	for _, ac := range accentColors {
		if ac.id == id {
			a.accentID = ac.id
			a.accentHex = ac.hex
			return
		}
	}
}

func (a *app) navigate(to pageKind, dir int) {
	if a.page == to {
		return
	}
	a.pageDir = dir
	a.page = to
}

func (a *app) post(fn func()) {
	if a.win == nil {
		fn()
		return
	}
	a.win.Update(func() {
		fn()
		a.win.Invalidate()
	})
}

// pickLibraryFolder opens the native Windows folder dialog and scans the chosen folder.
func (a *app) pickLibraryFolder() {
	start := ""
	if len(a.libFolders) > 0 {
		start = a.libFolders[len(a.libFolders)-1]
	} else if music, err := mygo.App.Path(mygo.PathMusic); err == nil {
		start = music
	}
	go func() {
		dir, err := chooseFolder(a.win, "选择本地音乐文件夹", start)
		if err != nil || dir == "" {
			return
		}
		a.post(func() {
			a.addLibraryFolder(dir)
		})
	}()
}

func isSubOrSameDir(parent, child string) bool {
	p := strings.ToLower(filepath.Clean(parent))
	c := strings.ToLower(filepath.Clean(child))
	if p == c {
		return true
	}
	if !strings.HasSuffix(p, string(filepath.Separator)) {
		p += string(filepath.Separator)
	}
	return strings.HasPrefix(c, p)
}

func foldLibraryFolders(folders []string) []string {
	var out []string
	for _, raw := range folders {
		dir := filepath.Clean(raw)
		if dir == "" || dir == "." {
			continue
		}
		covered := false
		for _, existing := range out {
			if isSubOrSameDir(existing, dir) {
				covered = true
				break
			}
		}
		if covered {
			continue
		}
		// Remove any existing children that are now covered by this parent folder
		kept := out[:0]
		for _, existing := range out {
			if !isSubOrSameDir(dir, existing) {
				kept = append(kept, existing)
			}
		}
		out = append(kept, dir)
	}
	return out
}

func (a *app) addLibraryFolder(dir string) {
	dir = filepath.Clean(dir)
	a.libFolders = foldLibraryFolders(append(a.libFolders, dir))
	a.savePrefs()
	a.scanLibraryAsync()
}

func (a *app) clearLibraryFolders() {
	a.libFolders = nil
	a.tracks = nil
	a.libStats = libStats{}
	a.libScanning = false
	a.savePrefs()
}

var (
	reAccomp = regexp.MustCompile(`(?i)(off\s*vocals?|instrumental|伴奏|karaoke|カラオケ)`)
	reOST    = regexp.MustCompile(`(?i)(original\s*soundtrack|\bost\b|白2纯音乐|纯音乐演奏|大提琴Free)`)
	rePure   = regexp.MustCompile(`(?i)(纯音乐|无词哼唱|烟雨濛濛\(人声曲\))`)
	reInstLrc = regexp.MustCompile(`(?i)(纯音乐\s*[，,]\s*请欣赏|純音樂|没有填词的纯音乐)`)
	reKana   = regexp.MustCompile(`[\x{3040}-\x{30ff}]`)
	reHan    = regexp.MustCompile(`[\x{4e00}-\x{9fff}]`)
)

func classifyAudioTrack(audioPath, relPath, lrcRaw string) string {
	stem := strings.TrimSuffix(filepath.Base(audioPath), filepath.Ext(audioPath))
	if reAccomp.MatchString(stem) {
		return "inst_accomp"
	}
	if reOST.MatchString(stem) {
		return "inst_ost"
	}
	if rePure.MatchString(stem) {
		return "inst_pure"
	}
	inOstDir := reOST.MatchString(relPath)
	inPureDir := false
	for _, part := range strings.Split(filepath.ToSlash(relPath), "/") {
		if part == "纯音乐" || strings.EqualFold(part, "Instrumental") || strings.EqualFold(part, "BGM") {
			inPureDir = true
			break
		}
	}
	isInstLrc := lrcRaw != "" && reInstLrc.MatchString(lrcRaw)
	if inOstDir && (isInstLrc || lrcRaw == "" || strings.Contains(relPath, "白2纯音乐")) {
		return "inst_ost"
	}
	if inPureDir || isInstLrc {
		if inOstDir {
			return "inst_ost"
		}
		return "inst_pure"
	}
	return ""
}

func parseArtistTitle(stem string) (artist, title string) {
	if idx := strings.Index(stem, " - "); idx > 0 {
		left := strings.TrimSpace(stem[:idx])
		right := strings.TrimSpace(stem[idx+3:])
		// Handle "02 - 好想好想 - 赵薇"
		if idx2 := strings.Index(right, " - "); idx2 > 0 {
			return strings.TrimSpace(right[idx2+3:]), strings.TrimSpace(right[:idx2])
		}
		return left, right
	}
	return "本地曲目", stem
}

func hueForString(s string) float32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return float32(h.Sum32() % 360)
}

func guessLang(text, stem string) string {
	lowStem := strings.ToLower(stem)
	if strings.Contains(stem, "英文") || strings.Contains(lowStem, "english") {
		return "英文"
	}
	if strings.Contains(stem, "日文") || strings.Contains(stem, "日语") || reKana.MatchString(text) || reKana.MatchString(stem) {
		return "日文"
	}
	if text != "" {
		latin, han := 0, 0
		for _, r := range text {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				latin++
			} else if r >= 0x4e00 && r <= 0x9fff {
				han++
			}
		}
		if latin >= 20 && latin > han*2 {
			return "英文"
		}
	}
	if reHan.MatchString(text) || reHan.MatchString(stem) {
		return "中文"
	}
	return "英文"
}

// scanLibrarySync scans a.libFolders synchronously (used in tests and by scanLibraryAsync).
func (a *app) scanLibrarySync() ([]trackItem, libStats) {
	audioExts := map[string]bool{
		".mp3": true, ".flac": true, ".wav": true, ".m4a": true,
		".aac": true, ".ogg": true, ".opus": true,
	}
	var audioFiles []struct{ full, rel string }
	seenFiles := map[string]bool{}
	for _, root := range a.libFolders {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if audioExts[strings.ToLower(filepath.Ext(p))] {
				key := strings.ToLower(filepath.Clean(p))
				if seenFiles[key] {
					return nil
				}
				seenFiles[key] = true
				rel, err := filepath.Rel(root, p)
				if err != nil {
					rel = p
				}
				audioFiles = append(audioFiles, struct{ full, rel string }{p, rel})
			}
			return nil
		})
	}
	sort.Slice(audioFiles, func(i, j int) bool { return audioFiles[i].rel < audioFiles[j].rel })

	var st libStats
	st.Total = len(audioFiles)
	items := make([]trackItem, 0, len(audioFiles))

	for i, f := range audioFiles {
		if i%25 == 0 && a.win != nil {
			done, all := i, len(audioFiles)
			a.post(func() {
				a.libScanDone = done
				a.libScanAll = all
			})
		}
		ext := filepath.Ext(f.full)
		stem := strings.TrimSuffix(filepath.Base(f.full), ext)
		lrcPath := strings.TrimSuffix(f.full, ext) + ".lrc"
		lrcRaw := ""
		if b, err := os.ReadFile(lrcPath); err == nil {
			lrcRaw = string(b)
		}

		artist, title := parseArtistTitle(stem)
		relDir := filepath.ToSlash(filepath.Dir(f.rel))
		if relDir == "." {
			relDir = filepath.Base(filepath.Dir(f.full))
		}
		fileInfo := fmt.Sprintf("%s · %s", relDir, strings.TrimPrefix(strings.ToLower(ext), "."))
		hue := hueForString(stem)

		instKind := classifyAudioTrack(f.full, f.rel, lrcRaw)
		if instKind != "" {
			st.InstTotal++
			note := "纯音乐 · 已从待处理中排除"
			switch instKind {
			case "inst_accomp":
				st.InstAccomp++
				note = "伴奏 / Off Vocal · 已从待处理中排除"
			case "inst_ost":
				st.InstOST++
				note = "OST 原声配乐 · 已从待处理中排除"
			default:
				st.InstPure++
			}
			items = append(items, trackItem{
				Title:    title,
				Artist:   artist,
				File:     fileInfo,
				FullPath: f.full,
				LrcPath:  lrcPath,
				Duration: "—",
				Status:   instKind,
				Lang:     "—",
				Note:     note,
				Hue:      hue,
			})
			continue
		}

		st.Vocal++
		if lrcRaw == "" {
			st.Missing++
			lang := guessLang("", stem)
			items = append(items, trackItem{
				Selected: true,
				Title:    title,
				Artist:   artist,
				File:     fileInfo,
				FullPath: f.full,
				LrcPath:  lrcPath,
				Duration: "—",
				Status:   "missing",
				Lang:     lang,
				Note:     "缺失 LRC · 将自动搜索或走 ASR 对齐",
				Hue:      hue,
			})
			continue
		}

		// Count lines and check whether any line has >2 word tags (<mm:ss.SSS>)
		norm := strings.ReplaceAll(lrcRaw, "\r\n", "\n")
		lineCount := 0
		enhCount := 0
		for _, ln := range strings.Split(norm, "\n") {
			ln = strings.TrimSpace(ln)
			if m := lrcLineRe.FindStringSubmatch(ln); m != nil {
				lineCount++
				body := m[3]
				nTags := len(lrcTagRe.FindAllStringIndex(body, -1))
				nRunes := len([]rune(strings.TrimSpace(lrcTagRe.ReplaceAllString(body, ""))))
				if nTags > 2 || (nRunes > 0 && nTags == nRunes+1) {
					enhCount++
				}
			}
		}
		lang := guessLang(norm, stem)
		if enhCount > 0 {
			st.Enhanced++
			items = append(items, trackItem{
				Selected: false,
				Title:    title,
				Artist:   artist,
				File:     fileInfo,
				FullPath: f.full,
				LrcPath:  lrcPath,
				Duration: "—",
				Status:   "enhanced",
				Lang:     lang,
				Note:     fmt.Sprintf("逐字已就绪 · %d 行", lineCount),
				Lines:    lineCount,
				Hue:      hue,
			})
		} else {
			st.LineOnly++
			engine := "SOFA 逐字"
			if lang != "中文" {
				engine = "WhisperX 逐字"
			}
			items = append(items, trackItem{
				Selected: st.LineOnly <= 12,
				Title:    title,
				Artist:   artist,
				File:     fileInfo,
				FullPath: f.full,
				LrcPath:  lrcPath,
				Duration: "—",
				Status:   "line_only",
				Lang:     lang,
				Note:     fmt.Sprintf("仅整行 %d 行 · 可升级为 %s", lineCount, engine),
				Lines:    lineCount,
				Hue:      hue,
			})
		}
	}
	return items, st
}

func (a *app) scanLibraryAsync() {
	if a.libScanning || len(a.libFolders) == 0 {
		return
	}
	a.libScanning = true
	a.libScanDone = 0
	a.libScanAll = 0
	go func() {
		items, st := a.scanLibrarySync()
		nowStr := time.Now().Format("15:04")
		a.post(func() {
			a.tracks = items
			a.libStats = st
			a.libScanning = false
			a.libScanTime = "今天 " + nowStr
		})
	}()
}

// runKiralrcCLI invokes `python -m kiralrc <args>` asynchronously and updates the UI.
func (a *app) runKiralrcCLI(args ...string) {
	go func() {
		py := "python"
		wb := `C:\Users\yxljs\AppData\Local\Programs\WorkBuddy\resources\vendor\python\python.exe`
		if _, err := os.Stat(wb); err == nil {
			py = wb
		}
		cmdArgs := append([]string{"-X", "utf8", "-m", "kiralrc"}, args...)
		cmd := newHiddenCmd(py, cmdArgs...)
		cmd.Env = append(os.Environ(), "PYTHONUTF8=1", "PYTHONIOENCODING=utf-8")
		out, err := cmd.CombinedOutput()
		msg := strings.TrimSpace(string(out))
		if err != nil && msg == "" {
			msg = err.Error()
		}
		a.post(func() {
			a.toolLog = msg
			if len(args) > 0 && args[0] == "clean" {
				a.refreshBackendInfo()
			}
		})
	}()
}

func (a *app) checkModels() {
	paths := [3]string{
		"models/sofa/v1.0.0_mandarin_singing.ckpt",
		".venv-sofa/Scripts/python.exe",
		"models/faster-whisper-large-v3",
	}
	roots := []string{"."}
	if exe, err := os.Executable(); err == nil {
		roots = append(roots, filepath.Dir(exe))
	}
	var ok [3]bool
	for i, rel := range paths {
		for _, r := range roots {
			if _, err := os.Stat(filepath.Join(r, rel)); err == nil {
				ok[i] = true
				break
			}
		}
	}
	a.modelOK = ok
}

func (a *app) openPolyphoneToml() {
	target := "polyphone.toml"
	if _, err := os.Stat(target); err != nil {
		if exe, err2 := os.Executable(); err2 == nil {
			cand := filepath.Join(filepath.Dir(exe), "polyphone.toml")
			if _, err3 := os.Stat(cand); err3 == nil {
				target = cand
			}
		}
	}
	go func() {
		_ = newHiddenCmd("cmd", "/c", "start", "", target).Start()
	}()
}

func (a *app) pickAlignRefLrc() {
	go func() {
		p, err := chooseFile(a.win, "选择本地参考 LRC 文件", "选择参考 LRC", []mygo.FileFilter{
			{Name: "LRC 歌词文件", Extensions: []string{"lrc", "txt"}},
		})
		if err != nil || p == "" {
			return
		}
		a.post(func() {
			a.alignRefLrc = p
		})
	}()
}

func (a *app) validateCurrentLrc() {
	if a.edPath == "" {
		return
	}
	path := a.edPath
	go func() {
		py := "python"
		wb := `C:\Users\yxljs\AppData\Local\Programs\WorkBuddy\resources\vendor\python\python.exe`
		if _, err := os.Stat(wb); err == nil {
			py = wb
		}
		cmd := newHiddenCmd(py, "-X", "utf8", "-m", "kiralrc", "validate", path)
		cmd.Env = append(os.Environ(), "PYTHONUTF8=1", "PYTHONIOENCODING=utf-8")
		out, _ := cmd.CombinedOutput()
		var issues []string
		for _, ln := range strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n") {
			ln = strings.TrimSpace(ln)
			if strings.HasPrefix(ln, "- ") {
				issues = append(issues, strings.TrimPrefix(ln, "- "))
			}
		}
		if len(issues) == 0 {
			issues = []string{"✓ 校验通过：BOM / 时间顺序 / 行尾标签全部符合规范"}
		}
		a.post(func() {
			a.edIssues = issues
		})
	}()
}

func (a *app) heartsCurrentLrc() {
	if a.edPath == "" {
		return
	}
	path := a.edPath
	go func() {
		py := "python"
		wb := `C:\Users\yxljs\AppData\Local\Programs\WorkBuddy\resources\vendor\python\python.exe`
		if _, err := os.Stat(wb); err == nil {
			py = wb
		}
		cmd := newHiddenCmd(py, "-X", "utf8", "-m", "kiralrc", "hearts", path)
		cmd.Env = append(os.Environ(), "PYTHONUTF8=1", "PYTHONIOENCODING=utf-8")
		out, _ := cmd.CombinedOutput()
		msg := strings.TrimSpace(string(out))
		a.post(func() {
			_ = a.loadLrcFile(path)
			if msg != "" {
				a.edIssues = append([]string{msg}, a.edIssues...)
			}
		})
	}()
}

func (a *app) runToolInteractive(toolCmd string) {
	switch toolCmd {
	case "validate", "hearts", "convert":
		go func() {
			p, err := chooseFile(a.win, "选择要处理的 LRC 文件", "选择 LRC", []mygo.FileFilter{
				{Name: "LRC 歌词文件", Extensions: []string{"lrc"}},
			})
			if err != nil || p == "" {
				return
			}
			a.runKiralrcCLI(toolCmd, p)
		}()
	case "separate":
		go func() {
			p, err := chooseFile(a.win, "选择要分离人声的音频文件", "选择音频", []mygo.FileFilter{
				{Name: "音频文件", Extensions: []string{"flac", "mp3", "wav", "m4a", "aac", "ogg", "opus"}},
			})
			if err != nil || p == "" {
				return
			}
			a.runKiralrcCLI("separate", p)
		}()
	default:
		a.runKiralrcCLI("info")
	}
}

func (a *app) refreshBackendInfo() {
	a.checkModels()
	go func() {
		py := "python"
		wb := `C:\Users\yxljs\AppData\Local\Programs\WorkBuddy\resources\vendor\python\python.exe`
		if _, err := os.Stat(wb); err == nil {
			py = wb
		}
		cmd := newHiddenCmd(py, "-X", "utf8", "-m", "kiralrc", "info", "--json")
		cmd.Env = append(os.Environ(), "PYTHONUTF8=1", "PYTHONIOENCODING=utf-8")
		out, err := cmd.Output()
		if err != nil {
			return
		}
		var info struct {
			GPU    string            `json:"gpu"`
			Models []modelStatusItem `json:"models"`
			Cache  struct {
				Count int   `json:"count"`
				Bytes int64 `json:"bytes"`
			} `json:"cache"`
		}
		if json.Unmarshal(out, &info) == nil {
			mb := float64(info.Cache.Bytes) / (1024 * 1024)
			a.post(func() {
				a.setCacheSummary = fmt.Sprintf("%.1f MB · %d 项", mb, info.Cache.Count)
				if info.GPU != "" {
					a.setGpuName = info.GPU
				}
				if len(info.Models) > 0 {
					a.setModels = info.Models
				}
			})
		}
	}()
}

var (
	lrcLineRe = regexp.MustCompile(`^\[(\d{1,2}):(\d{2}(?:[.:]\d{1,3})?)\](.*)$`)
	lrcTagRe  = regexp.MustCompile(`<(\d{1,2}):(\d{2}(?:[.:]\d{1,3})?)>`)
)

func parseSec(mm, ss string) float64 {
	m, _ := strconv.Atoi(mm)
	ss = strings.ReplaceAll(ss, ":", ".")
	s, _ := strconv.ParseFloat(ss, 64)
	return float64(m)*60 + s
}

func fmtSec(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	m := int(sec) / 60
	s := sec - float64(m*60)
	return fmt.Sprintf("%02d:%06.3f", m, s)
}

func (a *app) pickAlignAudio() {
	go func() {
		p, err := chooseFile(a.win, "选择音频文件", "选择音频", []mygo.FileFilter{
			{Name: "音频文件", Extensions: []string{"flac", "mp3", "wav", "m4a", "aac", "ogg", "opus"}},
		})
		if err != nil || p == "" {
			return
		}
		a.post(func() {
			a.setAlignAudioPath(p)
		})
	}()
}

func (a *app) pickAlignFolder() {
	start := ""
	if len(a.libFolders) > 0 {
		start = a.libFolders[len(a.libFolders)-1]
	}
	go func() {
		dir, err := chooseFolder(a.win, "选择要批量对齐的音乐文件夹", start)
		if err != nil || dir == "" {
			return
		}
		tmp := &app{libFolders: []string{dir}}
		items, _ := tmp.scanLibrarySync()
		var targets []trackItem
		for _, tr := range items {
			if tr.Status == "line_only" || tr.Status == "missing" {
				targets = append(targets, tr)
			}
		}
		a.post(func() {
			a.alignBatchDir = dir
			a.alignBatchTracks = targets
			a.alignAudio = ""
			a.alignAudioPath = ""
			a.alignLocalLrc = ""
			a.alignOut = dir + "（同目录输出同名 .lrc）"
		})
	}()
}

func (a *app) setAlignAudioPath(p string) {
	a.alignBatchDir = ""
	a.alignBatchTracks = nil
	a.alignAudioPath = p
	a.alignAudio = filepath.Base(p)
	ext := filepath.Ext(p)
	stem := strings.TrimSuffix(filepath.Base(p), ext)
	artist, title := parseArtistTitle(stem)
	a.alignArtist = artist
	a.alignTitle = title
	a.alignSearch = strings.TrimSpace(title + " " + artist)
	lrcPath := strings.TrimSuffix(p, ext) + ".lrc"
	a.alignOut = lrcPath
	if info, err := os.Stat(lrcPath); err == nil && !info.IsDir() {
		a.alignLocalLrc = lrcPath
	} else {
		a.alignLocalLrc = ""
	}
	lang := guessLang("", stem)
	switch lang {
	case "日文":
		a.alignLang = 1
	case "英文":
		a.alignLang = 2
	default:
		a.alignLang = 0
	}
}

func (a *app) pickLrcFile() {
	go func() {
		p, err := chooseFile(a.win, "打开 LRC 歌词文件", "打开 LRC", []mygo.FileFilter{
			{Name: "LRC 歌词文件", Extensions: []string{"lrc"}},
		})
		if err != nil || p == "" {
			return
		}
		a.post(func() {
			_ = a.loadLrcFile(p)
		})
	}()
}

func (a *app) trackMatchesFilter(tr *trackItem) bool {
	isInst := tr.Status == "inst_pure" || tr.Status == "inst_ost" || tr.Status == "inst_accomp"
	switch a.libFilter {
	case 0:
		return !isInst
	case 1:
		return tr.Status == "line_only" || tr.Status == "missing"
	case 2:
		return tr.Status == "enhanced"
	case 3:
		return isInst
	}
	return true
}

func (a *app) selectedLibraryTracks() []trackItem {
	var batch []trackItem
	for _, tr := range a.tracks {
		if tr.Selected {
			batch = append(batch, tr)
		}
	}
	return batch
}

func (a *app) setVisibleTracksSelected(sel bool) {
	for i := range a.tracks {
		if a.trackMatchesFilter(&a.tracks[i]) {
			a.tracks[i].Selected = sel
		}
	}
}

func langCode(label string) string {
	switch label {
	case "日文":
		return "ja"
	case "英文":
		return "en"
	default:
		return "zh"
	}
}

func (a *app) enqueueTracks(batch []trackItem) {
	if len(batch) == 0 {
		return
	}
	for _, tr := range batch {
		found := false
		for i := range a.queueItems {
			if strings.EqualFold(a.queueItems[i].AudioPath, tr.FullPath) {
				found = true
				// If already finished/failed/skipped, reset in place instead of duplicating!
				if a.queueItems[i].Status != "running" && a.queueItems[i].Status != "waiting" {
					a.queueItems[i].Status = "waiting"
					a.queueItems[i].Note = "等待重新对齐"
				}
				break
			}
		}
		if !found {
			a.queueItems = append(a.queueItems, queueItem{
				Title:     tr.Title,
				Artist:    tr.Artist,
				Sub:       fmt.Sprintf("%s · %s · %s", tr.Artist, tr.Lang, tr.File),
				AudioPath: tr.FullPath,
				LrcPath:   tr.LrcPath,
				Lang:      langCode(tr.Lang),
				Status:    "waiting",
				Note:      tr.Note,
				Hue:       tr.Hue,
			})
		}
	}
	a.startQueueRunner()
}

func (a *app) enqueueFromLibrary() {
	batch := a.selectedLibraryTracks()
	if len(batch) == 0 {
		for _, tr := range a.tracks {
			if tr.Status == "line_only" || tr.Status == "missing" {
				batch = append(batch, tr)
			}
		}
	}
	a.enqueueTracks(batch)
}

func (a *app) enqueueCurrentAlign() {
	if a.alignBatchDir != "" && len(a.alignBatchTracks) > 0 {
		a.enqueueTracks(a.alignBatchTracks)
		return
	}
	if a.alignAudio == "" {
		return
	}
	title := a.alignTitle
	if title == "" {
		title = a.alignAudio
	}
	lang := "zh"
	langLbl := "中文"
	engine := "SOFA 歌声对齐"
	if a.alignLang == 1 {
		lang = "ja"
		langLbl = "日文"
		engine = "WhisperX 强制对齐"
	} else if a.alignLang == 2 {
		lang = "en"
		langLbl = "英文"
		engine = "WhisperX 强制对齐"
	}
	sub := fmt.Sprintf("%s · %s · %s", a.alignArtist, langLbl, engine)
	found := false
	for i := range a.queueItems {
		if a.alignAudioPath != "" && strings.EqualFold(a.queueItems[i].AudioPath, a.alignAudioPath) {
			found = true
			if a.queueItems[i].Status != "running" && a.queueItems[i].Status != "waiting" {
				a.queueItems[i].Status = "waiting"
				a.queueItems[i].Lang = lang
				a.queueItems[i].Sub = sub
				a.queueItems[i].Note = "等待重新对齐"
			}
			break
		}
	}
	if !found {
		a.queueItems = append(a.queueItems, queueItem{
			Title:     title,
			Artist:    a.alignArtist,
			Sub:       sub,
			AudioPath: a.alignAudioPath,
			LrcPath:   a.alignOut,
			Lang:      lang,
			Status:    "waiting",
			Note:      "优先使用本地同目录 LRC",
			Hue:       hueForString(title),
		})
	}
	a.startQueueRunner()
}

var (
	stageRe = regexp.MustCompile(`^\[(\d)/6\]`)
	ansiRe  = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
)

func cleanLogLine(raw string) string {
	raw = ansiRe.ReplaceAllString(raw, "")
	if strings.Contains(raw, "\r") {
		parts := strings.Split(raw, "\r")
		for i := len(parts) - 1; i >= 0; i-- {
			if s := strings.TrimSpace(parts[i]); s != "" {
				return s
			}
		}
		return ""
	}
	return strings.TrimSpace(raw)
}

func (a *app) startQueueRunner() {
	if a.win == nil {
		return
	}
	maxWorkers := a.queueConcurrency + 1
	if maxWorkers < 1 {
		maxWorkers = 1
	}
	if maxWorkers > 3 {
		maxWorkers = 3
	}
	// Check if there is at least one waiting item before spawning workers
	hasWaiting := false
	for _, it := range a.queueItems {
		if it.Status == "waiting" {
			hasWaiting = true
			break
		}
	}
	if !hasWaiting {
		return
	}
	a.queueRunning = true
	for a.queueWorkers < maxWorkers {
		a.queueWorkers++
		go a.runQueueWorker()
	}
}

func (a *app) runQueueWorker() {
	py := "python"
	wb := `C:\Users\yxljs\AppData\Local\Programs\WorkBuddy\resources\vendor\python\python.exe`
	if _, err := os.Stat(wb); err == nil {
		py = wb
	}
	for {
		// Pick next waiting item on UI thread
		var idx = -1
		var item queueItem
		var finishedAll bool
		doneCh := make(chan struct{})
		a.post(func() {
			for i, it := range a.queueItems {
				if it.Status == "waiting" {
					idx = i
					item = it
					a.queueItems[i].Status = "running"
					a.queueItems[i].Stage = 1
					a.queueItems[i].Note = "启动对齐进程…"
					a.queueActiveTitle = it.Title
					a.queueActiveSub = it.Sub
					a.queueStage = 1
					a.queuePct = 0.08
					a.queueLogs = append(a.queueLogs,
						fmt.Sprintf("%s  [%s] 开始对齐 (%s)", time.Now().Format("15:04:05"), it.Title, filepath.Base(it.AudioPath)),
					)
					if len(a.queueLogs) > 14 {
						a.queueLogs = a.queueLogs[len(a.queueLogs)-14:]
					}
					break
				}
			}
			if idx < 0 {
				a.queueWorkers--
				if a.queueWorkers <= 0 {
					a.queueWorkers = 0
					a.queueRunning = false
					a.queueActiveTitle = ""
					a.queueActiveSub = ""
					finishedAll = true
				}
			}
			close(doneCh)
		})
		<-doneCh
		if idx < 0 {
			if finishedAll && len(a.libFolders) > 0 {
				a.scanLibraryAsync()
			}
			return
		}

		args := []string{"-u", "-X", "utf8", "-m", "kiralrc", "align", item.AudioPath, "--lang", item.Lang}
		if !a.setAutoSkipNoVocal {
			args = append(args, "--force-vocal")
		}
		if a.alignIgnoreRefTimes {
			args = append(args, "--ignore-ref-times")
		}
		if a.setEnMode == 1 || a.alignEnMode == 1 {
			args = append(args, "--en-granularity", "char")
		}
		if a.alignSource == 1 && a.alignRefLrc != "" {
			args = append(args, "--ref", a.alignRefLrc)
		}
		cmd := newHiddenCmd(py, args...)
		cmd.Env = append(os.Environ(), "PYTHONUTF8=1", "PYTHONIOENCODING=utf-8", "PYTHONUNBUFFERED=1")
		stdout, err := cmd.StdoutPipe()
		cmd.Stderr = cmd.Stdout
		var lastLine string
		var skippedNoVocal bool
		if err == nil {
			err = cmd.Start()
		}
		if err == nil {
			sc := bufio.NewScanner(stdout)
			for sc.Scan() {
				line := cleanLogLine(sc.Text())
				if line == "" {
					continue
				}
				lastLine = line
				if strings.HasPrefix(line, "[skip]") {
					skippedNoVocal = true
				}
				step := 0
				if m := stageRe.FindStringSubmatch(line); m != nil {
					step, _ = strconv.Atoi(m[1])
				}
				songTitle := item.Title
				songPath := item.AudioPath
				a.post(func() {
					for i := range a.queueItems {
						if strings.EqualFold(a.queueItems[i].AudioPath, songPath) && a.queueItems[i].Status == "running" {
							if step >= 1 && step <= 6 {
								a.queueItems[i].Stage = step
								a.queueItems[i].Note = line
							} else if strings.HasPrefix(line, "[") {
								a.queueItems[i].Note = line
							}
							break
						}
					}
					if step >= 1 && step <= 6 {
						a.queueStage = step
						a.queuePct = float32(step) / 6.0
					}
					a.queueLogs = append(a.queueLogs, fmt.Sprintf("%s  [%s] %s", time.Now().Format("15:04:05"), songTitle, line))
					if len(a.queueLogs) > 14 {
						a.queueLogs = a.queueLogs[len(a.queueLogs)-14:]
					}
				})
			}
			err = cmd.Wait()
		}

		songPath := item.AudioPath
		a.post(func() {
			for i := range a.queueItems {
				if !strings.EqualFold(a.queueItems[i].AudioPath, songPath) {
					continue
				}
				switch {
				case skippedNoVocal:
					a.queueItems[i].Status = "skipped"
					a.queueItems[i].Note = lastLine
				case err != nil:
					a.queueItems[i].Status = "failed"
					if lastLine != "" {
						a.queueItems[i].Note = lastLine
					} else {
						a.queueItems[i].Note = err.Error()
					}
				default:
					a.queueItems[i].Status = "done"
					a.queueItems[i].Stage = 6
					a.queueItems[i].Note = "逐字 LRC 已生成"
				}
				break
			}
			// If another song is still running, point active header to it
			for _, it := range a.queueItems {
				if it.Status == "running" {
					a.queueActiveTitle = it.Title
					a.queueActiveSub = it.Sub
					if it.Stage >= 1 && it.Stage <= 6 {
						a.queueStage = it.Stage
						a.queuePct = float32(it.Stage) / 6.0
					}
					break
				}
			}
		})
		if err != nil && !a.queueKeepGoing {
			a.post(func() {
				a.queueWorkers--
				if a.queueWorkers <= 0 {
					a.queueWorkers = 0
					a.queueRunning = false
					a.queueActiveTitle = ""
				}
			})
			return
		}
	}
}

func findSiblingAudio(lrcPath string) string {
	base := strings.TrimSuffix(lrcPath, filepath.Ext(lrcPath))
	for _, ext := range []string{".flac", ".wav", ".mp3", ".m4a", ".aac", ".ogg", ".opus"} {
		cand := base + ext
		if info, err := os.Stat(cand); err == nil && !info.IsDir() {
			return cand
		}
	}
	return ""
}

func (a *app) loadAudioAsync(audioPath string) {
	if audioPath == "" || a.player == nil || a.win == nil {
		return
	}
	if audioPath != a.edAudioPath {
		a.edPlaying = false
		a.dockPos = 0
	}
	a.edAudioPath = audioPath
	a.edTrackMode = 0
	go func() {
		dur, err := a.player.Load(audioPath)
		if err == nil && dur > 0 {
			a.post(func() {
				a.dockDur = dur
			})
		}
	}()
}

func (a *app) openTrackInEditor(tr *trackItem) {
	if tr == nil {
		return
	}
	if tr.FullPath != "" {
		a.edAudioPath = tr.FullPath
		a.loadAudioAsync(tr.FullPath)
	}
	if tr.LrcPath != "" {
		_ = a.loadLrcFile(tr.LrcPath)
	}
	if a.edFile == "" {
		a.edFile = tr.Title
		a.edSub = tr.Artist + " · " + tr.File
	}
	a.navigate(pageEditor, 1)
}

func (a *app) currentPlaybackPath() string {
	if a.edTrackMode == 1 && a.edAudioPath != "" {
		if v := findVocalsCache(a.edAudioPath); v != "" {
			return v
		}
	}
	return a.edAudioPath
}

func (a *app) togglePlayback() {
	if a.player == nil {
		return
	}
	// Resolve audio path if not yet loaded (e.g. clicking Play from the bottom Glass Dock)
	if a.edAudioPath == "" {
		if a.edPath != "" {
			a.edAudioPath = findSiblingAudio(a.edPath)
		}
		if a.edAudioPath == "" {
			for _, it := range a.queueItems {
				if it.AudioPath != "" {
					a.edAudioPath = it.AudioPath
					break
				}
			}
		}
	}
	path := a.currentPlaybackPath()
	if path == "" {
		return
	}
	a.edPlaying = !a.edPlaying
	playing := a.edPlaying
	go func() {
		if playing {
			dur, _ := a.player.Load(path)
			if dur > 0 {
				a.post(func() { a.dockDur = dur })
			}
			_ = a.player.Play()
			a.startPlayTicker()
		} else {
			_ = a.player.Pause()
		}
	}()
}

func (a *app) seekPlayback(sec float64) {
	if sec < 0 {
		sec = 0
	}
	a.dockPos = sec
	a.edPlayhead = sec
	if a.player == nil || a.edAudioPath == "" {
		return
	}
	go func() {
		_ = a.player.Seek(sec)
	}()
}

func findCoverCache(audioPath string) string {
	if audioPath == "" {
		return ""
	}
	stem := strings.TrimSuffix(filepath.Base(audioPath), filepath.Ext(audioPath))
	runes := []rune(stem)
	if len(runes) > 40 {
		stem = string(runes[:40])
	}
	workDirs := []string{"work"}
	if exe, err := os.Executable(); err == nil {
		workDirs = append(workDirs, filepath.Join(filepath.Dir(exe), "work"))
	}
	prefix := stem + "_"
	for _, wd := range workDirs {
		entries, err := os.ReadDir(wd)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() && strings.HasPrefix(e.Name(), prefix) {
				cand := filepath.Join(wd, e.Name(), "cover.jpg")
				if info, err := os.Stat(cand); err == nil && info.Size() > 512 {
					return cand
				}
			}
		}
	}
	return ""
}

func (a *app) loadEditorCoverAsync(audioPath string) {
	if audioPath == "" {
		a.edCover = nil
		a.edCoverAudio = ""
		return
	}
	if strings.EqualFold(a.edCoverAudio, audioPath) && a.edCover != nil {
		return
	}
	a.edCover = nil
	a.edCoverAudio = audioPath
	go func(ap string) {
		coverPath := findCoverCache(ap)
		if coverPath == "" {
			py := "python"
			wb := `C:\Users\yxljs\AppData\Local\Programs\WorkBuddy\resources\vendor\python\python.exe`
			if _, err := os.Stat(wb); err == nil {
				py = wb
			}
			cmd := newHiddenCmd(py, "-X", "utf8", "-m", "kiralrc", "cover", ap)
			cmd.Env = append(os.Environ(), "PYTHONUTF8=1", "PYTHONIOENCODING=utf-8")
			if out, err := cmd.Output(); err == nil {
				p := strings.TrimSpace(string(out))
				if p != "" {
					coverPath = p
				}
			}
		}
		if coverPath == "" {
			return
		}
		raw, err := os.ReadFile(coverPath)
		if err != nil || len(raw) < 512 {
			return
		}
		bmp, err := ui.DecodeBitmap(raw)
		if err != nil || bmp == nil {
			return
		}
		a.post(func() {
			if strings.EqualFold(a.edCoverAudio, ap) {
				a.edCover = bmp
				for i := range a.recents {
					if strings.EqualFold(a.recents[i].Path, a.edPath) {
						a.recents[i].Cover = bmp
					}
				}
			}
		})
	}(audioPath)
}

func findVocalsCache(audioPath string) string {
	if audioPath == "" {
		return ""
	}
	stem := strings.TrimSuffix(filepath.Base(audioPath), filepath.Ext(audioPath))
	runes := []rune(stem)
	if len(runes) > 40 {
		stem = string(runes[:40])
	}
	workDirs := []string{"work"}
	if exe, err := os.Executable(); err == nil {
		workDirs = append(workDirs, filepath.Join(filepath.Dir(exe), "work"))
	}
	prefix := stem + "_"
	for _, wd := range workDirs {
		entries, err := os.ReadDir(wd)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			if strings.HasPrefix(e.Name(), prefix) || strings.EqualFold(e.Name(), stem) {
				cand := filepath.Join(wd, e.Name(), "vocals.wav")
				if info, err := os.Stat(cand); err == nil && !info.IsDir() && info.Size() > 1024 {
					return cand
				}
			}
		}
	}
	return ""
}

func (a *app) switchTrackMode(mode int) {
	if a.edAudioPath == "" || a.player == nil {
		a.edTrackMode = 0
		return
	}
	targetPath := a.edAudioPath
	if mode == 1 {
		vocals := findVocalsCache(a.edAudioPath)
		if vocals == "" {
			a.edTrackMode = 0
			a.toast = "未找到该曲的人声轨缓存（请先分离或对齐该曲）"
			return
		}
		targetPath = vocals
	}
	a.edTrackMode = mode
	wasPlaying := a.edPlaying
	curPos, _ := a.player.Position()
	if curPos <= 0 {
		curPos = a.edPlayhead
	}
	go func() {
		dur, err := a.player.Load(targetPath)
		if err != nil {
			return
		}
		if dur > 0 {
			a.post(func() { a.dockDur = dur })
		}
		if curPos > 0 {
			_ = a.player.Seek(curPos)
		}
		if wasPlaying {
			_ = a.player.Play()
		}
	}()
}

func (a *app) setPlaybackSpeed(mode int) {
	a.edSpeedMode = mode
	if a.player == nil {
		return
	}
	spd := 1.0
	switch mode {
	case 0:
		spd = 0.5
	case 1:
		spd = 0.75
	}
	go func() {
		_ = a.player.SetSpeed(spd)
	}()
}

func (a *app) setVolume(v float64) {
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	a.volume = v
	a.muted = v == 0
	if v > 0 {
		a.preMuteVol = v
	}
	if a.player != nil {
		a.player.SetVolume(v)
	}
}

func (a *app) toggleMute() {
	if a.muted || a.volume == 0 {
		restore := a.preMuteVol
		if restore <= 0.05 {
			restore = 0.85
		}
		a.setVolume(restore)
	} else {
		a.preMuteVol = a.volume
		a.setVolume(0)
	}
}

func (a *app) startPlayTicker() {
	if a.playTickerOn || a.win == nil {
		return
	}
	a.playTickerOn = true
	go func() {
		ticker := time.NewTicker(30 * time.Millisecond)
		defer ticker.Stop()
		lastLine, lastChar, lastSec := -1, -1, -1
		for range ticker.C {
			if !a.edPlaying {
				a.playTickerOn = false
				return
			}
			pos, dur := a.player.Position()
			curSec := int(pos)
			// Only trigger a UI layout pass when active line, active character, or displayed second changes;
			// the 60fps playhead line itself is painted directly inside Draw via pt.After(16ms).
			a.win.Update(func() {
				a.dockPos = pos
				a.edPlayhead = pos
				if dur > 0 {
					a.dockDur = dur
				}
				changed := curSec != lastSec
				lastSec = curSec
				if len(a.edLines) > 0 {
					bestLine := a.edSelectedLine
					// Show incoming line ~80ms early for natural karaoke reading, while keeping exact char timing
					for i, ln := range a.edLines {
						if pos+0.08 >= ln.Start {
							bestLine = i
						}
					}
					if bestLine != a.edSelectedLine {
						a.edSelectedLine = bestLine
						if len(a.edLines[bestLine].Chars) > 0 {
							a.edGlyphs = a.edLines[bestLine].Chars
						}
						targetY := max(0, float32(bestLine)*33-180)
						a.edLineScroll.ScrollTo(0, targetY, ui.Snappy)
						changed = true
					}
					bestChar := 0
					for ci, ch := range a.edGlyphs {
						if pos >= ch.Start {
							bestChar = ci
						}
					}
					if bestChar != a.edSelectedChar {
						a.edSelectedChar = bestChar
						changed = true
					}
					if bestLine != lastLine || bestChar != lastChar {
						lastLine, lastChar = bestLine, bestChar
						changed = true
					}
				}
				if changed {
					a.win.Invalidate()
				}
			})
		}
	}()
}

func (a *app) ensureLineWaveforms(tMin, tMax float64) {
	if a.edAudioPath == "" || a.win == nil {
		return
	}
	key := fmt.Sprintf("%s|%.2f|%.2f", a.edAudioPath, tMin, tMax)
	if a.waveKey == key {
		return
	}
	a.waveKey = key
	audioPath := a.edAudioPath
	vocPath := findVocalsCache(audioPath)
	go func() {
		mix := extractWaveformPeaks(audioPath, tMin, tMax, 140)
		var voc []float32
		if vocPath != "" {
			voc = extractWaveformPeaks(vocPath, tMin, tMax, 140)
		}
		a.post(func() {
			if a.waveKey == key {
				a.waveMix = mix
				a.waveVoc = voc
			}
		})
	}()
}

func (a *app) selectEditorLine(idx int) {
	if idx < 0 || idx >= len(a.edLines) {
		return
	}
	a.edSelectedLine = idx
	a.edSelectedChar = 0
	if len(a.edLines[idx].Chars) > 0 {
		a.edGlyphs = a.edLines[idx].Chars
		a.edPlayhead = a.edGlyphs[0].Start
	}
}

func (a *app) shiftAllLyrics(delta float64) {
	if len(a.edLines) == 0 {
		return
	}
	for i := range a.edLines {
		a.edLines[i].Start = max(0, a.edLines[i].Start+delta)
		a.edLines[i].End = max(a.edLines[i].Start+0.05, a.edLines[i].End+delta)
		for j := range a.edLines[i].Chars {
			a.edLines[i].Chars[j].Start = max(0, a.edLines[i].Chars[j].Start+delta)
			a.edLines[i].Chars[j].End = max(a.edLines[i].Chars[j].Start+0.02, a.edLines[i].Chars[j].End+delta)
		}
	}
	if a.edSelectedLine >= 0 && a.edSelectedLine < len(a.edLines) && len(a.edLines[a.edSelectedLine].Chars) > 0 {
		a.edGlyphs = a.edLines[a.edSelectedLine].Chars
	}
	a.edModified = true
}

func isAsciiNonSpace(s string) bool {
	if strings.TrimSpace(s) == "" {
		return false
	}
	for _, r := range s {
		if r >= 128 {
			return false
		}
	}
	return true
}

// mergeSameTsAsciiWords collapses word-level English LRC tags (where every letter of a word
// shares the same <mm:ss.SSS> start tag and 0ms duration until the last letter) into whole-word
// tokens so the editor timeline, preview, and inspector operate on full words ("being", "frankly")
// instead of single trailing letters ("g", "y"). CJK/Japanese speaker tags (e.g. 【合】) and
// char-level English (distinct timestamps per letter) are left untouched.
func mergeSameTsAsciiWords(chars []lyricChar) []lyricChar {
	if len(chars) < 2 {
		return chars
	}
	out := make([]lyricChar, 0, len(chars))
	for _, cur := range chars {
		if len(out) > 0 {
			prev := &out[len(out)-1]
			if math.Abs(cur.Start-prev.Start) < 0.001 &&
				prev.End-prev.Start < 0.001 &&
				isAsciiNonSpace(prev.Ch) && isAsciiNonSpace(cur.Ch) {
				prev.Ch += cur.Ch
				prev.End = cur.End
				continue
			}
		}
		out = append(out, cur)
	}
	return out
}

func formatLrcLine(ln lyricLine) string {
	var sb strings.Builder
	if len(ln.Chars) > 0 {
		sb.WriteString("[")
		sb.WriteString(fmtSec(ln.Chars[0].Start))
		sb.WriteString("]")
		for _, ch := range ln.Chars {
			sb.WriteString("<")
			sb.WriteString(fmtSec(ch.Start))
			sb.WriteString(">")
			sb.WriteString(ch.Ch)
		}
		sb.WriteString("<")
		sb.WriteString(fmtSec(ln.Chars[len(ln.Chars)-1].End))
		sb.WriteString(">")
	} else {
		sb.WriteString("[")
		sb.WriteString(fmtSec(ln.Start))
		sb.WriteString("]")
		sb.WriteString(ln.Text)
	}
	if ln.Translation != "" {
		st := ln.Start
		if len(ln.Chars) > 0 {
			st = ln.Chars[0].Start
		}
		sb.WriteString("\n[")
		sb.WriteString(fmtSec(st))
		sb.WriteString("]")
		sb.WriteString(ln.Translation)
	}
	return sb.String()
}

func (a *app) saveLrcFile() {
	if a.edPath == "" || len(a.edLines) == 0 {
		return
	}
	var sb strings.Builder
	for _, m := range a.edMetas {
		sb.WriteString(m)
		sb.WriteString("\n")
	}
	for _, ln := range a.edLines {
		st := ln.Start
		if len(ln.Chars) > 0 {
			st = ln.Chars[0].Start
			sb.WriteString("[")
			sb.WriteString(fmtSec(st))
			sb.WriteString("]")
			for _, ch := range ln.Chars {
				sb.WriteString("<")
				sb.WriteString(fmtSec(ch.Start))
				sb.WriteString(">")
				sb.WriteString(ch.Ch)
			}
			sb.WriteString("<")
			sb.WriteString(fmtSec(ln.Chars[len(ln.Chars)-1].End))
			sb.WriteString(">\n")
		} else {
			sb.WriteString("[")
			sb.WriteString(fmtSec(st))
			sb.WriteString("]")
			sb.WriteString(ln.Text)
			sb.WriteString("\n")
		}
		if ln.Translation != "" {
			sb.WriteString("[")
			sb.WriteString(fmtSec(st))
			sb.WriteString("]")
			sb.WriteString(ln.Translation)
			sb.WriteString("\n")
		}
	}
	if err := os.WriteFile(a.edPath, []byte(sb.String()), 0o644); err == nil {
		a.edModified = false
		a.toast = "已保存 " + a.edFile
	}
}

func (a *app) loadLrcFile(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text := strings.TrimPrefix(string(raw), "\xef\xbb\xbf")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	var metas []string
	var lines []lyricLine
	idx := 1
	enhLines := 0
	missingEndTag := 0
	for _, row := range strings.Split(text, "\n") {
		row = strings.TrimSpace(row)
		if strings.HasPrefix(row, "[") && !lrcLineRe.MatchString(row) && strings.Contains(row, ":") && strings.HasSuffix(row, "]") {
			metas = append(metas, row)
			continue
		}
		m := lrcLineRe.FindStringSubmatch(row)
		if m == nil {
			continue
		}
		start := parseSec(m[1], m[2])
		body := m[3]
		plain := strings.TrimSpace(lrcTagRe.ReplaceAllString(body, ""))
		if plain == "" {
			continue
		}
		tagLocs := lrcTagRe.FindAllStringSubmatchIndex(body, -1)
		runes := []rune(plain)
		isEnh := len(tagLocs) > 2 || (len(runes) > 0 && len(tagLocs) == len(runes)+1)
		// Same-timestamp non-enhanced line following an existing line = bilingual translation!
		if !isEnh && len(lines) > 0 && math.Abs(start-lines[len(lines)-1].Start) < 0.06 {
			if lines[len(lines)-1].Translation == "" {
				lines[len(lines)-1].Translation = plain
			} else {
				lines[len(lines)-1].Translation += " / " + plain
			}
			continue
		}
		var chars []lyricChar
		end := start + 2.4
		if isEnh {
			enhLines++
			// Parse each <tag>segment accurately so tagless spaces or multi-char tokens never shift subsequent timestamps!
			for ti, loc := range tagLocs {
				ts := parseSec(body[loc[2]:loc[3]], body[loc[4]:loc[5]])
				segEnd := len(body)
				nextTs := ts + 0.35
				if ti+1 < len(tagLocs) {
					segEnd = tagLocs[ti+1][0]
					nextTs = parseSec(body[tagLocs[ti+1][2]:tagLocs[ti+1][3]], body[tagLocs[ti+1][4]:tagLocs[ti+1][5]])
				}
				segText := body[loc[1]:segEnd]
				segRunes := []rune(segText)
				if len(segRunes) == 0 {
					continue
				}
				step := (nextTs - ts) / float64(len(segRunes))
				if step < 0 {
					step = 0
				}
				for ri, r := range segRunes {
					cs := ts + float64(ri)*step
					ce := cs + step
					if ri == len(segRunes)-1 {
						ce = max(cs, nextTs)
					}
					chars = append(chars, lyricChar{Ch: string(r), Start: cs, End: ce})
				}
			}
			chars = mergeSameTsAsciiWords(chars)
			lastLoc := tagLocs[len(tagLocs)-1]
			end = parseSec(body[lastLoc[2]:lastLoc[3]], body[lastLoc[4]:lastLoc[5]])
			if len(body[lastLoc[1]:]) > 0 {
				missingEndTag++
			}
		} else if len(runes) > 0 {
			step := 2.4 / float64(len(runes))
			for i, r := range runes {
				t := start + float64(i)*step
				chars = append(chars, lyricChar{Ch: string(r), Start: t, End: t + step})
			}
		}
		lines = append(lines, lyricLine{
			Index:  idx,
			Start:  start,
			End:    end,
			Text:   plain,
			Status: "ok",
			Chars:  chars,
		})
		idx++
	}
	if len(lines) > 0 {
		// For line-only LRC files (no <mm:ss.SSS> word tags), clamp each line's end
		// to the next line's start so fast rap / dense lines don't overshoot!
		for i := range lines {
			if len(lines[i].Chars) == 0 {
				continue
			}
			// Check if this line was line-only (synthesized evenly)
			if enhLines == 0 {
				end := lines[i].Start + 2.4
				if i+1 < len(lines) && lines[i+1].Start > lines[i].Start+0.1 {
					end = lines[i+1].Start
				}
				lines[i].End = end
				n := len(lines[i].Chars)
				step := (end - lines[i].Start) / float64(n)
				for j := range lines[i].Chars {
					lines[i].Chars[j].Start = lines[i].Start + float64(j)*step
					lines[i].Chars[j].End = lines[i].Chars[j].Start + step
				}
			}
		}
		base := filepath.Base(path)
		stem := strings.TrimSuffix(base, filepath.Ext(base))
		a.edFile = base
		a.edPath = path
		if sib := findSiblingAudio(path); sib != "" {
			a.loadAudioAsync(sib)
			a.loadEditorCoverAsync(sib)
		} else {
			a.edCover = nil
			a.edCoverAudio = ""
		}
		if enhLines > 0 {
			a.edSub = fmt.Sprintf("逐字 LRC · %d 行（%d 行含逐字标签）", len(lines), enhLines)
		} else {
			a.edSub = fmt.Sprintf("整行 LRC · %d 行（尚未升级逐字标签）", len(lines))
		}
		a.edMetas = metas
	a.edLines = lines
		a.edModified = false
		a.selectEditorLine(0)

		a.edIssues = nil
		if enhLines == 0 {
			a.edIssues = append(a.edIssues, "当前文件仅含整行时间戳，可在「新建对齐」中生成逐字卡拉 OK 标签")
		} else if missingEndTag > 0 {
			a.edIssues = append(a.edIssues, fmt.Sprintf("发现 %d 行缺少行尾结束标签，建议在工具箱中一键补齐", missingEndTag))
		}

		// Record in recents (最近校对)
		var prevCover *ui.Bitmap
		for _, r := range a.recents {
			if strings.EqualFold(r.Path, path) {
				prevCover = r.Cover
				break
			}
		}
		if a.edCover != nil {
			prevCover = a.edCover
		}
		var nextRecents []recentItem
		nextRecents = append(nextRecents, recentItem{
			Name:  stem,
			File:  base,
			Path:  path,
			Hue:   hueForString(stem),
			Cover: prevCover,
		})
		for _, r := range a.recents {
			if !strings.EqualFold(r.Path, path) && len(nextRecents) < 6 {
				nextRecents = append(nextRecents, r)
			}
		}
		a.recents = nextRecents
		a.savePrefs()
	}
	return nil
}

func Run() error {
	a := newApp()
	mygo.App.WhenReady(func() {
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:           "Kiralrc · 逐字歌词对齐",
			Width:           1360,
			Height:          860,
			MinWidth:        1040,
			MinHeight:       680,
			TitleBarStyle:   mygo.TitleBarHidden,
			TitleBarHeight:  buttonsHeight,
			BackgroundColor: "light-dark(#ffffff, #141416)",
			Content:         ui.View(a.view),
		})
		applyWindowIcon("Kiralrc · 逐字歌词对齐")
		a.refreshBackendInfo()
		if len(a.libFolders) > 0 {
			a.scanLibraryAsync()
		}
	})
	return mygo.App.Run()
}
