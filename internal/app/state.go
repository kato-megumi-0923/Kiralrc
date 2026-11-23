package app

import (
	"bufio"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io/fs"
	"math"
	"math/bits"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
	pageSeparate
	pageCleanup
	pageSettings
	pageScrape
)

const (
	topBarHeight  = 40
	topEdgePad    = 6
	buttonsHeight = topBarHeight + topEdgePad // 46, matching Cloudy
	pageGap       = 12
)

type trackItem struct {
	Title    string
	Artist   string
	File     string
	FullPath string
	LrcPath  string
	Duration string
	Status   string // "enhanced", "line_only", "missing", "nolyrics_notfound", "inst_pure", "inst_ost", "inst_accomp"
	Lang     string
	Note     string
	Lines    int
	Hue      float32
}

type libStats struct {
	Total      int
	Vocal      int
	Enhanced   int
	Official   int // 逐字里来自平台（网易云 / QQ / AMLL / 酷狗）的
	Aligned    int // 逐字里 Kiralrc 自己对齐的
	Unknown    int // 逐字里没有来源标记的（早先别的工具写的）
	LineOnly   int
	Missing    int // 无歌词总数（本地缺词 + 全网无词）
	NotFound   int // 其中：多源均未搜到的「全网无词」数量
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
	Title          string
	Artist         string
	Sub            string
	AudioPath      string
	LrcPath        string
	Lang           string
	NeteaseID      string // 单曲手动指定的网易云歌曲 ID / 链接（避免批量任务串 ID）
	RefPath        string // 单曲手动参考歌词文件（--ref）
	AsrFallback    bool   // 是否显式开启 --asr-fallback 本地人声听写兜底
	WaveformFirst  bool   // 是否开启 --waveform-first 鬼畜 / 波形起音优先模式
	IgnoreRefTimes bool   // 是否开启 --ignore-ref-times 强制 ASR 重定位
	IgnoreRefEnds  bool   // 沿用参考句首，重新对齐逐字时间与句尾
	Status         string // "waiting", "running", "done", "skipped", "skipped_nolyrics", "failed"
	Stage          int    // 0..6 live stage for this song
	Note           string
	Hue            float32
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
	libFolders         []string
	downloading        bool // 正在批量下载歌词
	sepAudio           string
	sepRunning         bool
	sepStage           string
	sepPct             int
	sepStems           []sepStem
	sepPlaying         string // 正在试听哪条轨
	cleanupDir         string
	cleanupItems       []cleanupGroup
	cleanupConfirm     bool
	cleanupHardConfirm bool   // 「永久删除」的二次确认
	cleanupResult      string // 上一次删除的真实结果（成了几个、剩几个、为什么）
	cleanupScanned     bool
	sepPaused          bool
	downloadDone       int
	downloadAll        int
	downloadNote       string
	libScanning        bool
	scanToken          uint64 // 每次扫描自增，旧扫描据此自行退出（原子访问）
	libPage            int    // 当前页码（0 起）
	libPageSize        int    // 每页条数：50 / 100
	pageMenuOpen       bool   // 每页数下拉是否展开
	libScanDone        int
	libScanAll         int
	libScanTime        string
	libStats           libStats
	libFilter          int // 0:有效人声 1:待升级逐字 2:逐字就绪 3:无歌词 4:纯音乐/OST/伴奏 5:官方逐字
	libQuery           string
	tracks             []trackItem
	libSelected        map[string]bool // 勾选状态按路径存：切片重建也不会丢

	// 封面按需加载（见 cover.go）
	coverMu           sync.Mutex
	coverInFlight     map[string]bool
	coverSem          chan struct{}
	coverPending      bool
	libScroll         ui.ScrollState
	notFoundPaths     map[string]bool   // 持久化：多源均未搜到的「全网无词」音频路径（小写归一化）
	libBatchIdOpen    bool              // 无歌词 Tab：顶部「批量填网易云 ID」抽屉是否展开
	libBatchIdInputs  map[string]string // 批量填 ID 抽屉输入值（key=AudioPath）
	libExpandedPath   string            // 无歌词 Tab：当前展开行内抽屉的音频路径
	libExpandedMode   string            // "netease" | "manual"
	libSingleIdInput  string            // 单曲网易云 ID 输入值
	libManualText     string            // 单曲手动参考词多行文本
	libManualLang     int               // 单曲手动参考词语言：0:中文 1:日文 2:英文
	libManualFilePath string            // 用记事本编辑时的落盘路径

	// 2. 新建对齐
	alignAudio          string
	alignAudioPath      string
	alignLocalLrc       string
	alignRefLrc         string
	alignNeteaseID      string // 手动指定的网易云歌曲 ID 或链接
	alignBatchDir       string
	alignBatchTracks    []trackItem
	alignIgnoreRefTimes bool
	alignIgnoreRefEnds  bool
	alignWaveformFirst  bool // 鬼畜 / 波形起音优先模式（--waveform-first）
	alignSource         int  // 0:优先本地LRC(无则搜网易云) 1:手动指定参考文件 2:强制搜网易云
	alignSearch         string
	alignCandidate      int
	alignLang           int // 0:中文 1:日文 2:英文 3:其它
	alignPolyCheck      bool
	alignSustain        bool
	alignEnMode         int // 0:按词 1:按字母均分
	alignTitle          string
	alignArtist         string
	alignAlbum          string
	alignOut            string
	alignDecimals       int // 0:3位 1:2位
	alignLead           string
	alignEndTag         bool
	alignKeepTrans      bool
	alignAutoValid      bool
	alignScroll         ui.ScrollState

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
	player                *audioPlayer
	volume                float64
	muted                 bool
	preMuteVol            float64
	edFile                string
	edPath                string
	edAudioPath           string
	edCover               *ui.Bitmap
	edCoverAudio          string
	edMarqueeText         string
	edMarqueeStart        time.Time
	edVinylDeg            float32
	edVinylLast           time.Time
	dockDur               float64
	playTickerOn          bool
	edSub                 string
	edModified            bool
	edPreviewMode         int // 0:律动预览 1:LRC源码
	edTrackMode           int // 0:原曲 1:人声轨
	edSpeedMode           int // 0:0.5x 1:0.75x 2:1x
	edShowPinyin          bool
	edSnapOnset           bool
	edRipple              bool
	edPlaying             bool
	edPlayhead            float64
	edSelectedLine        int
	edSelectedChar        int
	edEditingLine         int // -1 表示未处于行内编辑状态
	edEditingText         string
	edSrcEditing          bool   // LRC 源码是否处于直接编辑模式
	edSrcText             string // LRC 源码编辑缓冲
	edSrcInitCaret        int    // 进入编辑时初始光标位置（按 rune 偏移定位到当前选中行）
	edSrcNeedCaret        bool   // 首帧应用初始光标位置
	edSrcScrollToSelected bool   // 选中行变更时自动滚动 LRC 源码到可视区域
	edTranslating         bool   // 是否正在执行单曲 AI 歌词翻译
	edMetas               []string
	edLines               []lyricLine
	edIssues              []string
	edLineScroll          ui.ScrollState
	edSrcScroll           ui.ScrollState
	waveMix               []float32
	waveVoc               []float32
	waveKey               string

	// 5. 工具箱
	toolSelectedFile int
	toolLog          string
	toolScroll       ui.ScrollState

	// 6. 标签刮削（见 scrape.go）
	scrapeDir         string
	scrapePaths       []string
	scrapeItems       []scrapeTrack
	scrapeCur         int
	scrapeFilter      int // 0 全部 1 无封面 2 低清(<600px) 3 缺 ID/年份
	scrapeQuery       string
	scrapeCands       []scrapeCandidate
	scrapeCandPath    string
	scrapeCandCache   map[string][]scrapeCandidate
	scrapeSearching   bool
	scrapeWarn        string    // 这次搜索的告警（比如网易云限速），显示在右栏
	scrapeCoolUntil   time.Time // 网易云限速后的冷却截止时间
	scrapeAuth        string    // 这次搜索用的身份（Cloudy 登录态 / 匿名）
	scrapeLoading     bool
	scrapeSel         map[string]scrapeCandidate // 路径 → 已确认的候选
	scrapeFields      [5]bool                    // 封面/年份/ID/专辑/标题歌手
	scrapeMode        int                        // 0 仅补缺失 1 覆盖选中
	scrapeQuality     int                        // 0 封面取原图 1 只请求 1200
	scrapeApplying    bool
	scrapeDone        int
	scrapeTotal       int
	scrapeMu          sync.Mutex
	scrapeThumbs      map[string]*ui.Bitmap
	scrapeThumbBusy   map[string]bool
	scrapeCovers      map[string]*ui.Bitmap // 当前曲目内嵌封面缩略图（不外挂缓存）
	scrapeWritten     map[string]bool       // 本次运行写过标签的路径，重读后仍标「已写入」
	scrapeFollowPath  string                // 写入前选中的曲目（写完看它还在不在列表里）
	scrapeFollowPos   int                   // 它在当时列表里的位置
	scrapeSizes       map[string][2]int     // 候选封面 URL → 实测像素
	scrapeSizeBusy    map[string]bool
	scrapePending     []scrapePlanItem // 限速暂停后没跑完的计划，冷却结束自动续跑
	scrapeGen         int              // 每次发起写入自增，用来作废旧定时器
	scrapeResumeTries int              // 连续续跑次数，防止一直被限速时死循环
	scrapeListScroll  ui.ScrollState
	scrapeCandScroll  ui.ScrollState

	// 7. 设置
	modelOK               [3]bool
	setAutoSkipNoVocal    bool
	setCloudyAuth         bool
	setVocalRatio         string
	setEnMode             int
	setFetchInterval      string
	setProxyOn            bool
	setProxyAddr          string
	setCacheSummary       string
	setGpuName            string
	setModels             []modelStatusItem
	setCleanDays          int // 0:7天前 1:30天前 2:全部
	setAppleToken         string
	setAppleStorefront    int // 0:jp 1:cn 2:us
	setAIBaseURL          string
	setAIModel            string
	setAIAPIKey           string
	setAIKeyEditing       bool
	setAIKeyDraft         string
	setAIURLDropOpen      bool
	setAIModelDropOpen    bool
	setConfirmDeleteModel string
	setCustomPython       string
	envInfo               pythonEnvInfo
	envChecked            bool
	envInstalling         bool
	envInstallLog         string
	setScroll             ui.ScrollState

	toast     string
	toastLast string
	toastSeq  int
}

type userPrefs struct {
	QueueConcurrency int      `json:"queue_concurrency"`
	QueueKeepGoing   *bool    `json:"queue_keep_going,omitempty"`
	LibFolders       []string `json:"lib_folders,omitempty"`
	Appearance       int      `json:"appearance"`
	AccentID         string   `json:"accent_id,omitempty"`
	RecentPaths      []string `json:"recent_paths,omitempty"`
	NotFoundPaths    []string `json:"not_found_paths,omitempty"`
	Volume           *float64 `json:"volume,omitempty"`
	AppleToken       string   `json:"apple_media_user_token,omitempty"`
	AppleStorefront  int      `json:"apple_storefront"`
	AIBaseURL        string   `json:"ai_base_url,omitempty"`
	AIModel          string   `json:"ai_model,omitempty"`
	AIAPIKey         string   `json:"ai_api_key,omitempty"`
	CustomPython     string   `json:"custom_python,omitempty"`
	LibPageSize      int      `json:"lib_page_size,omitempty"`
	CloudyAuth       bool     `json:"cloudy_auth,omitempty"` // 借用 Cloudy 的网易云登录态（默认关）
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
	switch up.LibPageSize {
	case 100:
		a.libPageSize = 100
	default:
		a.libPageSize = 50
	}
	covers := indexCovers()
	for _, rp := range up.RecentPaths {
		if info, err := os.Stat(rp); err == nil && !info.IsDir() {
			base := filepath.Base(rp)
			stem := strings.TrimSuffix(base, filepath.Ext(base))
			a.recents = append(a.recents, recentItem{
				Name:  stem,
				File:  base,
				Path:  rp,
				Hue:   hueForString(stem),
				Cover: covers.thumbnail(findSiblingAudio(rp)),
			})
		}
	}
	if a.notFoundPaths == nil {
		a.notFoundPaths = make(map[string]bool)
	}
	for _, np := range up.NotFoundPaths {
		if norm := normPathKey(np); norm != "" {
			a.notFoundPaths[norm] = true
		}
	}
	if up.Volume != nil && *up.Volume >= 0 && *up.Volume <= 1 {
		a.volume = *up.Volume
		a.muted = a.volume == 0
		if a.volume > 0 {
			a.preMuteVol = a.volume
		}
	}
	a.setAppleToken = strings.TrimSpace(up.AppleToken)
	if up.AppleStorefront >= 0 && up.AppleStorefront <= 2 {
		a.setAppleStorefront = up.AppleStorefront
	}
	a.setAIBaseURL = strings.TrimSpace(up.AIBaseURL)
	a.setAIModel = strings.TrimSpace(up.AIModel)
	a.setCustomPython = strings.TrimSpace(up.CustomPython)
	a.setCloudyAuth = up.CloudyAuth
	if key, migrate, err := unprotectSecret(up.AIAPIKey); err == nil {
		a.setAIAPIKey = key
		if migrate && key != "" {
			a.savePrefs()
		}
	}
}

func normPathKey(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	return strings.ToLower(filepath.Clean(p))
}

func (a *app) markTrackNotFound(audioPath string) {
	key := normPathKey(audioPath)
	if key == "" {
		return
	}
	if a.notFoundPaths == nil {
		a.notFoundPaths = make(map[string]bool)
	}
	if !a.notFoundPaths[key] {
		a.notFoundPaths[key] = true
		a.savePrefs()
	}
	for i := range a.tracks {
		if normPathKey(a.tracks[i].FullPath) == key && (a.tracks[i].Status == "missing" || a.tracks[i].Status == "nolyrics_notfound") {
			if a.tracks[i].Status == "missing" {
				a.libStats.NotFound++
			}
			a.tracks[i].Status = "nolyrics_notfound"
			a.tracks[i].Note = "多源均未搜到 · 批量跑库已自动跳过"
		}
	}
}

func (a *app) clearTrackNotFound(audioPath string) {
	key := normPathKey(audioPath)
	if key == "" || a.notFoundPaths == nil {
		return
	}
	if a.notFoundPaths[key] {
		delete(a.notFoundPaths, key)
		a.savePrefs()
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
	var nfPaths []string
	for k := range a.notFoundPaths {
		nfPaths = append(nfPaths, k)
	}
	sort.Strings(nfPaths)
	vol := a.volume
	encAIKey := ""
	if strings.TrimSpace(a.setAIAPIKey) != "" {
		if enc, err := protectSecret(a.setAIAPIKey); err == nil {
			encAIKey = enc
		}
	}
	up := userPrefs{
		QueueConcurrency: a.queueConcurrency,
		QueueKeepGoing:   &kg,
		LibFolders:       a.libFolders,
		Appearance:       a.appearance,
		AccentID:         a.accentID,
		RecentPaths:      rpaths,
		NotFoundPaths:    nfPaths,
		Volume:           &vol,
		AppleToken:       strings.TrimSpace(a.setAppleToken),
		AppleStorefront:  a.setAppleStorefront,
		AIBaseURL:        strings.TrimSpace(a.setAIBaseURL),
		AIModel:          strings.TrimSpace(a.setAIModel),
		AIAPIKey:         encAIKey,
		CustomPython:     strings.TrimSpace(a.setCustomPython),
		LibPageSize:      a.pageSize(),
		CloudyAuth:       a.setCloudyAuth,
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
		edEditingLine:      -1,
		scrapeFields:       [5]bool{true, true, true, false, false},
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
			{Lang: "通用", Name: "Demucs 人声分离（htdemucs）", Path: "models/hf/hub/models--adefossez--HTDemucs", Exists: false},
			{Lang: "通用", Name: "faster-whisper large-v3（ASR 句级定位）", Path: "models/faster-whisper-large-v3", Exists: false},
			{Lang: "中文", Name: "HubertFA v0.0.7 中文歌声对齐（默认）", Path: "models/hubertfa/model.onnx", Exists: false},
			{Lang: "中文", Name: "FireRedASR-AED-L（中文专精听写）", Path: "models/FireRedASR-AED-L", Exists: false},
			{Lang: "日文", Name: "MMS 日语卡拉 OK 歌声模型（NextFire）", Path: "models/hf/hub/models--NextFire--mms-300m-ForcedAligner-karaoke-ja-Latn", Exists: false},
			{Lang: "日文", Name: "fugashi + unidic-lite（日语词法读音）", Path: "Python 环境内置（MeCab / UniDic）", Exists: false},
			{Lang: "英文", Name: "MMS 英文/多语歌词模型（Echora-300M）", Path: "models/hf/hub/models--hcX02--echora-mms-300m-multilingual-lyrics-forced-aligner", Exists: false},
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
	a.libSelected = nil
	resetCoverKeys()
	a.libStats = libStats{}
	a.libScanning = false
	a.savePrefs()
}

var (
	reAccomp  = regexp.MustCompile(`(?i)(off\s*vocals?|instrumental|伴奏|karaoke|カラオケ)`)
	reOST     = regexp.MustCompile(`(?i)(original\s*soundtrack|\bost\b|白2纯音乐|纯音乐演奏|大提琴Free)`)
	rePure    = regexp.MustCompile(`(?i)(纯音乐|无词哼唱|烟雨濛濛\(人声曲\))`)
	reInstLrc = regexp.MustCompile(`(?i)(纯音乐\s*[，,]\s*请欣赏|純音樂|没有填词的纯音乐)`)
	reKana    = regexp.MustCompile(`[\x{3040}-\x{30ff}]`)
	reHan     = regexp.MustCompile(`[\x{4e00}-\x{9fff}]`)
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

func parseArtistTitle(fullPath, stem string) (artist, title string) {
	// 标签里写的才是真的，文件名只能猜（「标题 - 歌手」和「歌手 - 标题」都可能）
	tagTitle, tagArtist, _ := readAudioTags(fullPath)
	tagTitle, tagArtist = strings.TrimSpace(tagTitle), strings.TrimSpace(tagArtist)
	if tagTitle != "" && tagArtist != "" {
		return tagArtist, tagTitle
	}
	if idx := strings.Index(stem, " - "); idx > 0 {
		left := strings.TrimSpace(stem[:idx])
		right := strings.TrimSpace(stem[idx+3:])
		// Handle "02 - 好想好想 - 赵薇"
		if idx2 := strings.Index(right, " - "); idx2 > 0 {
			return strings.TrimSpace(right[idx2+3:]), strings.TrimSpace(right[:idx2])
		}
		return left, right
	}
	if tagTitle != "" {
		return "本地曲目", tagTitle
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

// ---------- 曲库扫描 ----------
//
// 分三段：walk 出清单（快）→ 分批读标签（慢，可中断）→ 由状态重算统计。
// 几万首也能先出前 60 条让用户操作，剩下的在后台一批批补。

// audioEntry 是 walk 出来的一条待读记录。
type audioEntry struct {
	full string // 绝对路径
	rel  string // 相对路径，排序键
}

var audioExts = map[string]bool{
	".mp3": true, ".flac": true, ".wav": true, ".m4a": true,
	".aac": true, ".ogg": true, ".opus": true,
}

const (
	scanFirstBatch = 60  // 冷启动第一批：先让首屏能点
	scanBatchSize  = 200 // 之后每批条数
)

// walkAudioFiles 只做遍历：找音频、去重、按相对路径排序。
// 不读标签、不碰 App 状态，几万首也就几百毫秒。
func walkAudioFiles(folders []string) []audioEntry {
	var files []audioEntry
	seen := map[string]bool{}
	for _, root := range folders {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if !audioExts[strings.ToLower(filepath.Ext(p))] {
				return nil
			}
			key := normPathKey(p)
			if seen[key] {
				return nil
			}
			seen[key] = true
			rel, err := filepath.Rel(root, p)
			if err != nil {
				rel = p
			}
			files = append(files, audioEntry{full: p, rel: rel})
			return nil
		})
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].rel == files[j].rel {
			return files[i].full < files[j].full
		}
		return files[i].rel < files[j].rel
	})
	return files
}

// readTrackBatch 读一批音频的标签与同名 .lrc，判定状态。
// 只读 notFoundSnap 这份只读快照，不写任何 App 状态（后台可直接调用）；
// 返回的第二项是「重新有词、该从持久化列表里摘掉」的路径。
func readTrackBatch(entries []audioEntry, notFoundSnap map[string]bool) (items []trackItem, clearedNotFound []string) {
	items = make([]trackItem, 0, len(entries))
	for _, f := range entries {
		ext := filepath.Ext(f.full)
		stem := strings.TrimSuffix(filepath.Base(f.full), ext)
		lrcPath := strings.TrimSuffix(f.full, ext) + ".lrc"
		lrcRaw := ""
		if b, err := os.ReadFile(lrcPath); err == nil {
			lrcRaw = string(b)
		}

		artist, title := parseArtistTitle(f.full, stem)
		relDir := filepath.ToSlash(filepath.Dir(f.rel))
		if relDir == "." {
			relDir = filepath.Base(filepath.Dir(f.full))
		}
		fileInfo := fmt.Sprintf("%s · %s", relDir, strings.TrimPrefix(strings.ToLower(ext), "."))
		hue := hueForString(stem)

		instKind := classifyAudioTrack(f.full, f.rel, lrcRaw)
		if instKind != "" {
			note := "纯音乐 · 已从待处理中排除"
			switch instKind {
			case "inst_accomp":
				note = "伴奏 / Off Vocal · 已从待处理中排除"
			case "inst_ost":
				note = "OST 原声配乐 · 已从待处理中排除"
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

		if lrcRaw == "" {
			lang := guessLang("", stem)
			status := "missing"
			note := "尚未搜索过全网歌词"
			if notFoundSnap[normPathKey(f.full)] {
				status = "nolyrics_notfound"
				note = "多源均未搜到 · 批量跑库已自动跳过"
			}
			items = append(items, trackItem{
				Title:    title,
				Artist:   artist,
				File:     fileInfo,
				FullPath: f.full,
				LrcPath:  lrcPath,
				Duration: "—",
				Status:   status,
				Lang:     lang,
				Note:     note,
				Hue:      hue,
			})
			continue
		}

		// 已经有词了：把之前的「全网无词」残留记下来，交给 UI 线程去删
		if key := normPathKey(f.full); notFoundSnap[key] {
			clearedNotFound = append(clearedNotFound, key)
		}

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
			items = append(items, trackItem{
				Title:    title,
				Artist:   artist,
				File:     fileInfo,
				FullPath: f.full,
				LrcPath:  lrcPath,
				Duration: "—",
				Status:   libWordStatus(norm),
				Lang:     lang,
				Note:     fmt.Sprintf("逐字已就绪 · %d 行", lineCount),
				Lines:    lineCount,
				Hue:      hue,
			})
		} else {
			engine := "HubertFA 逐字"
			if lang == "日文" {
				engine = "MMS 日语逐字"
			} else if lang == "英文" {
				engine = "MMS 英文逐字"
			} else if lang != "中文" {
				engine = "MMS 多语逐字"
			}
			items = append(items, trackItem{
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
	return items, clearedNotFound
}

// recomputeLibStats 完全由切片推出来，分段追加时每批重算一次即可。
func recomputeLibStats(items []trackItem) libStats {
	st := libStats{Total: len(items)}
	for i := range items {
		switch items[i].Status {
		case "inst_pure":
			st.InstTotal++
			st.InstPure++
		case "inst_ost":
			st.InstTotal++
			st.InstOST++
		case "inst_accomp":
			st.InstTotal++
			st.InstAccomp++
		case "missing":
			st.Vocal++
			st.Missing++
		case "nolyrics_notfound":
			st.Vocal++
			st.Missing++
			st.NotFound++
		case "line_only":
			st.Vocal++
			st.LineOnly++
		case "official":
			st.Vocal++
			st.Enhanced++
			st.Official++
		case "aligned":
			st.Vocal++
			st.Enhanced++
			st.Aligned++
		case "unknown":
			st.Vocal++
			st.Enhanced++
			st.Unknown++
		default:
			st.Vocal++
		}
	}
	return st
}

// scanLibrarySync 一次性扫完（单测与需要同步结果的地方用）。
func (a *app) scanLibrarySync() ([]trackItem, libStats) {
	items, cleared := readTrackBatch(walkAudioFiles(a.libFolders), a.notFoundPaths)
	a.applyClearedNotFound(cleared)
	a.tracks = items
	a.pruneSelectedTracks()
	return items, recomputeLibStats(items)
}

// nextScanToken 每次扫描拿一个新编号；旧扫描发现编号变了就自己退出。
func (a *app) nextScanToken() uint64 { return atomic.AddUint64(&a.scanToken, 1) }

func (a *app) scanTokenCurrent(token uint64) bool { return atomic.LoadUint64(&a.scanToken) == token }

// applyClearedNotFound 在 UI 线程把「又有词了」的路径从持久化列表里摘掉。
func (a *app) applyClearedNotFound(paths []string) {
	if len(paths) == 0 || a.notFoundPaths == nil {
		return
	}
	changed := false
	for _, key := range paths {
		if a.notFoundPaths[key] {
			delete(a.notFoundPaths, key)
			changed = true
		}
	}
	if changed {
		a.savePrefs()
	}
}

// scanLibraryAsync 分段扫描曲库。
//
//	冷启动（还没有列表）：首批 60 条一读完就上屏，之后每批追加；
//	刷新（已有列表）：中途只报进度，扫完整体替换，避免正在看的那页跳走。
//
// 封面不在这里解 —— 交给按页惰性加载，扫描只管「有哪些歌、什么状态」。
func (a *app) scanLibraryAsync() {
	if len(a.libFolders) == 0 {
		return
	}
	token := a.nextScanToken() // 每次调用都作废上一次
	a.libScanning = true
	a.libScanDone = 0
	a.libScanAll = 0

	folders := append([]string{}, a.libFolders...)
	nfSnap := make(map[string]bool, len(a.notFoundPaths))
	for k, v := range a.notFoundPaths {
		if v {
			nfSnap[k] = true
		}
	}
	refresh := len(a.tracks) > 0

	go func() {
		files := walkAudioFiles(folders)
		a.post(func() {
			if a.scanTokenCurrent(token) {
				a.libScanAll = len(files)
			}
		})

		items := make([]trackItem, 0, len(files))
		cleared := make([]string, 0, 8)
		for start := 0; start < len(files); {
			if !a.scanTokenCurrent(token) {
				return // 有更新的扫描接手了，别再往结果里塞东西
			}
			size := scanBatchSize
			if start == 0 {
				size = scanFirstBatch
			}
			end := min(start+size, len(files))
			batch, gone := readTrackBatch(files[start:end], nfSnap)
			cleared = append(cleared, gone...)
			if refresh {
				items = append(items, batch...)
			}
			start = end

			if refresh {
				done := len(items)
				a.post(func() {
					if a.scanTokenCurrent(token) {
						a.libScanDone = done
					}
				})
			} else {
				// batch 是刚分配的新切片，可以直接交给 UI 线程；
				// 在 UI 线程原地追加，避免每批整片复制（几万首就是 O(n²) 的内存抖动）
				a.post(func() {
					if !a.scanTokenCurrent(token) {
						return
					}
					a.tracks = append(a.tracks, batch...)
					a.libStats = recomputeLibStats(a.tracks)
					a.libScanDone = len(a.tracks)
					a.applyClearedNotFound(gone)
				})
			}
			if start < len(files) {
				time.Sleep(10 * time.Millisecond) // 让出调度，别把 UI 线程饿着
			}
		}

		st := recomputeLibStats(items)
		nowStr := time.Now().Format("15:04")
		a.post(func() {
			if !a.scanTokenCurrent(token) {
				return
			}
			if refresh {
				a.tracks = items
				a.libStats = st
				a.libScanDone = len(items)
			}
			a.applyClearedNotFound(cleared)
			a.pruneSelectedTracks()
			resetCoverKeys() // 文件可能被换过，封面键重新算
			a.libScanning = false
			a.libScanTime = "今天 " + nowStr
		})
	}()
}

func (a *app) appleStorefrontCode() string {
	switch a.setAppleStorefront {
	case 1:
		return "cn"
	case 2:
		return "us"
	default:
		return "jp"
	}
}

// pythonSubprocessEnv 所有 Python 子进程共用的一套环境变量：
// 编码、Apple Music Token、以及「借用 Cloudy 登录态」的开关。
// 任何新起的 python 调用都应该用它，别自己拼——漏一处就会有一条链路拿不到身份。
func (a *app) pythonSubprocessEnv(extra ...string) []string {
	env := append(os.Environ(), "PYTHONUTF8=1", "PYTHONIOENCODING=utf-8")
	env = append(env, extra...)
	if a.setCloudyAuth {
		// 借用 Cloudy 的登录态是用户显式打开的偏好，否则 Python 那边默认不读
		env = append(env, "KIRALRC_CLOUDY_AUTH=1")
	}
	if tok := strings.TrimSpace(a.setAppleToken); tok != "" {
		env = append(env,
			"KIRALRC_APPLE_MEDIA_USER_TOKEN="+tok,
			"KIRALRC_APPLE_STOREFRONT="+a.appleStorefrontCode(),
		)
	}
	return env
}

// pythonCmd builds `python -m kiralrc <args>` with the interpreter this app found.
func (a *app) pythonCmd(args ...string) *exec.Cmd {
	cmd := newHiddenCmd(getActivePythonPath(), append([]string{"-X", "utf8", "-m", "kiralrc"}, args...)...)
	// 工作目录定在程序根目录，`-m kiralrc` 才找得到包
	if root := appRootDir(); root != "" {
		cmd.Dir = root
	}
	cmd.Env = a.pythonSubprocessEnv()
	return cmd
}

// startDownloadLyrics 给曲库里的每个文件夹下载歌词：只下载，不跑模型。
func (a *app) startDownloadLyrics() {
	if a.downloading {
		a.toast = "正在下载中，请稍候"
		return
	}
	folders := append([]string{}, a.libFolders...)
	if len(folders) == 0 && a.alignBatchDir != "" {
		folders = append(folders, a.alignBatchDir)
	}
	if len(folders) == 0 {
		a.toast = "还没有添加音乐文件夹"
		return
	}
	a.downloading = true
	a.downloadDone, a.downloadAll, a.downloadNote = 0, 0, "准备中…"
	a.toast = "开始下载歌词"
	go func() {
		var last string
		for _, dir := range folders {
			cmd := a.pythonCmd("download", dir, "--jobs", "2")
			last = a.streamDownload(cmd, last)
		}
		a.post(func() {
			a.downloading = false
			a.downloadNote = last
			a.toast = "歌词下载结束：" + last
			a.scanLibraryAsync()
		})
	}()
}

// streamDownload 边跑边把「第几首、在做什么」显示出来，返回最后一行汇总。
func (a *app) streamDownload(cmd *exec.Cmd, last string) string {
	stdout, err := cmd.StdoutPipe()
	cmd.Stderr = cmd.Stdout
	if err == nil {
		err = cmd.Start()
	}
	if err != nil {
		return err.Error()
	}
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		last = line
		done, all, note := parseDownloadLine(line)
		a.post(func() {
			if all > 0 {
				a.downloadDone, a.downloadAll = done, all
			}
			a.downloadNote = note
		})
	}
	_ = cmd.Wait()
	return last
}

// parseDownloadLine 读「[3/19] upgraded 歌名 QQ音乐 逐字」这样的行。
func parseDownloadLine(line string) (done, all int, note string) {
	note = line
	if !strings.HasPrefix(line, "[") {
		return 0, 0, note
	}
	end := strings.Index(line, "]")
	if end < 0 {
		return 0, 0, note
	}
	head := line[1:end]
	if _, err := fmt.Sscanf(head, "%d/%d", &done, &all); err != nil {
		return 0, 0, note
	}
	return done, all, strings.TrimSpace(line[end+1:])
}

// downloadLyricsOf 给勾选的这些歌下载歌词：只下载，不跑模型。
func (a *app) downloadLyricsOf(tracks []trackItem) {
	if a.downloading || len(tracks) == 0 {
		return
	}
	list, err := os.CreateTemp("", "kiralrc-paths-*.txt")
	if err != nil {
		a.toolLog = err.Error()
		return
	}
	for _, tr := range tracks {
		fmt.Fprintln(list, tr.FullPath)
	}
	list.Close()
	a.downloading = true
	a.toolLog = fmt.Sprintf("正在为 %d 首歌下载歌词…", len(tracks))
	go func() {
		defer os.Remove(list.Name())
		cmd := a.pythonCmd("download", "--paths-file", list.Name(), "--jobs", "2")
		last := a.streamDownload(cmd, "")
		a.post(func() {
			a.downloading = false
			a.downloadNote = last
			a.toast = "歌词下载结束：" + last
			a.scanLibraryAsync()
		})
	}()
}

// runKiralrcCLI invokes `python -m kiralrc <args>` asynchronously and updates the UI.
func (a *app) runKiralrcCLI(args ...string) {
	env := a.pythonSubprocessEnv()
	py := a.pythonExe()
	go func() {
		cmdArgs := append([]string{"-X", "utf8", "-m", "kiralrc"}, args...)
		cmd := newHiddenCmd(py, cmdArgs...)
		cmd.Dir = appRootDir()
		cmd.Env = env
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

func canDeleteModelPath(p string) bool {
	p = strings.TrimSpace(p)
	if p == "" || strings.HasPrefix(p, "Python ") {
		return false
	}
	clean := filepath.Clean(p)
	low := strings.ToLower(clean)
	// 仅允许删除明确的本地模型文件/缓存目录，防止误删任意系统路径
	if strings.Contains(low, "models--") ||
		strings.Contains(low, string(filepath.Separator)+"models"+string(filepath.Separator)) ||
		strings.Contains(low, "checkpoints"+string(filepath.Separator)) {
		return true
	}
	return false
}

func (a *app) deleteModel(m modelStatusItem) {
	if !m.Exists || !canDeleteModelPath(m.Path) {
		return
	}
	target := filepath.Clean(m.Path)
	a.setConfirmDeleteModel = ""
	a.toast = "正在删除模型：" + m.Name
	go func() {
		err := os.RemoveAll(target)
		a.post(func() {
			if err != nil {
				a.toast = "删除模型失败：" + err.Error()
			} else {
				a.toast = "已删除模型：" + m.Name
			}
			a.refreshBackendInfo()
		})
	}()
}

func (a *app) checkModels() {
	paths := [3]string{
		"models/hubertfa/model.onnx",
		"third_party/SOFA/dictionary/opencpop-extension.txt",
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
	if a.edModified {
		a.toast = "请先保存歌词，再校验当前版本"
		return
	}
	a.checkEditorLrc(a.edPath, findSiblingAudio(a.edPath))
}

func (a *app) checkEditorLrc(path, audioPath string) {
	py := a.pythonExe()
	go func() {
		args := []string{"-X", "utf8", "-m", "kiralrc", "validate", path}
		if audioPath != "" {
			args = append(args, "--audio", audioPath)
		}
		cmd := newHiddenCmd(py, args...)
		cmd.Dir = appRootDir()
		cmd.Env = a.pythonSubprocessEnv()
		out, err := cmd.CombinedOutput()
		var issues []string
		for _, ln := range strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n") {
			ln = strings.TrimSpace(ln)
			if strings.HasPrefix(ln, "- ") {
				issues = append(issues, strings.TrimPrefix(ln, "- "))
			}
		}
		if err != nil && len(issues) == 0 {
			issues = []string{"校验未完成，请检查 Python 运行环境"}
		}
		a.post(func() {
			if a.edPath == path && !a.edModified {
				a.edIssues = issues
			}
		})
	}()
}

func (a *app) heartsCurrentLrc() {
	if a.edPath == "" {
		return
	}
	path := a.edPath
	py := a.pythonExe()
	go func() {
		cmd := newHiddenCmd(py, "-X", "utf8", "-m", "kiralrc", "hearts", path)
		cmd.Dir = appRootDir()
		cmd.Env = a.pythonSubprocessEnv()
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
	case "decrypt":
		go func() {
			dir, err := chooseFolder(a.win, "选择放着加密音频的文件夹", a.alignBatchDir)
			if err != nil || dir == "" {
				return
			}
			a.runKiralrcCLI("decrypt", dir)
		}()
	case "cleanup":
		go func() {
			dir, err := chooseFolder(a.win, "选择要清理歌词的目录", "")
			if err != nil || dir == "" {
				return
			}
			a.openCleanup(dir)
		}()
	case "scrape":
		go func() {
			dir, err := chooseFolder(a.win, "选择要刮削标签的文件夹", a.alignBatchDir)
			if err != nil || dir == "" {
				return
			}
			a.post(func() { a.openScrapeFolder(dir) })
		}()
	case "separate":
		go func() {
			p, err := chooseFile(a.win, "选择要分离人声的音频文件", "选择音频", []mygo.FileFilter{
				{Name: "音频文件", Extensions: []string{"flac", "mp3", "wav", "m4a", "aac", "ogg", "opus"}},
			})
			if err != nil || p == "" {
				return
			}
			a.startSeparate(p) // 直接进分离页看进度
		}()
	default:
		a.runKiralrcCLI("info")
	}
}

func (a *app) refreshBackendInfo() {
	a.checkModels()
	customPy := a.setCustomPython
	go func() {
		envInfo := detectPythonEnv(customPy)
		if envInfo.Path != "" {
			setActivePythonPath(envInfo.Path)
		}
		a.post(func() {
			a.envInfo = envInfo
			a.envChecked = true
		})
		py := envInfo.Path
		if py == "" {
			py = "python"
		}
		cmd := newHiddenCmd(py, "-X", "utf8", "-m", "kiralrc", "info", "--json")
		cmd.Dir = appRootDir()
		cmd.Env = a.pythonSubprocessEnv()
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
	artist, title := parseArtistTitle(p, stem)
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

func (a *app) setLibFilter(f int) {
	a.libFilter = f
	a.libPage = 0 // 换筛选就回第一页
	if f != 3 {
		a.libBatchIdOpen = false
		a.libExpandedPath = ""
		a.libExpandedMode = ""
	}
}

// libWordStatus tells where a word-timed lyric came from: the platform
// ("official"), Kiralrc's own alignment ("aligned"), or neither marked
// ("unknown", which is what older tools left behind).
func libWordStatus(body string) string {
	lower := strings.ToLower(body)
	if strings.Contains(lower, "[kiralrc:aligned") {
		return "aligned"
	}
	if lrcByOfficial(body) {
		return "official"
	}
	// 老文件没有 kiralrc 标记，就按 [by:] 里的引擎名认
	by := lrcByValue(body)
	for _, mark := range []string{"SOFA", "MMS", "WhisperX", "听写", "模板迁移", "波包络"} {
		if strings.Contains(by, mark) {
			return "aligned"
		}
	}
	return "unknown"
}

// lrcByValue is what the [by:] line says, if there is one.
func lrcByValue(body string) string {
	i := strings.Index(body, "[by:")
	if i < 0 {
		return ""
	}
	rest := body[i+len("[by:"):]
	if j := strings.Index(rest, "]"); j >= 0 {
		return strings.TrimSpace(rest[:j])
	}
	return strings.TrimSpace(rest)
}

// lrcByOfficial 看 [by:] 是不是平台的名字。
func lrcByOfficial(body string) bool {
	i := strings.Index(body, "[by:")
	if i < 0 {
		return false
	}
	rest := body[i+len("[by:"):]
	if j := strings.Index(rest, "]"); j >= 0 {
		rest = rest[:j]
	}
	for _, tag := range []string{"NeteaseCloud", "QQMusic", "AMLL", "Kugou"} {
		if strings.EqualFold(strings.TrimSpace(rest), tag) {
			return true
		}
	}
	return false
}

func isNoLyricsStatus(status string) bool {
	return status == "missing" || status == "nolyrics_notfound"
}

// matchesQuery 只看搜索词，与当前选中的 Tab 无关。
func (a *app) matchesQuery(tr *trackItem) bool {
	q := strings.TrimSpace(strings.ToLower(a.libQuery))
	if q == "" {
		return true
	}
	return strings.Contains(strings.ToLower(tr.Title), q) ||
		strings.Contains(strings.ToLower(tr.Artist), q) ||
		strings.Contains(strings.ToLower(tr.File), q)
}

// statusFilterMask 一个状态属于哪些 Tab，用位图表示（第 f 位 = 筛选值 f）。
// 列表判定与 Tab 计数都从这里来，规则只有一份。
func statusFilterMask(status string) uint {
	switch status {
	case "official":
		return 1<<0 | 1<<5 // 有效人声 + 官方逐字
	case "aligned", "unknown":
		return 1<<0 | 1<<2
	case "line_only":
		return 1<<0 | 1<<1
	case "missing", "nolyrics_notfound":
		return 1<<0 | 1<<3
	case "inst_pure", "inst_ost", "inst_accomp":
		return 1 << 4
	}
	return 1 << 0
}

// statusMatchesFilter 某个状态是否属于某个 Tab。
func statusMatchesFilter(status string, filter int) bool {
	if filter < 0 || filter >= 6 {
		return true
	}
	return statusFilterMask(status)&(1<<uint(filter)) != 0
}

func (a *app) trackMatchesFilter(tr *trackItem) bool {
	return a.matchesQuery(tr) && statusMatchesFilter(tr.Status, a.libFilter)
}

// filterCounts 一次遍历算出每个 Tab 的条数（含搜索词），下标就是筛选值。
func (a *app) filterCounts() [6]int {
	var counts [6]int
	for i := range a.tracks {
		tr := &a.tracks[i]
		if !a.matchesQuery(tr) {
			continue
		}
		for mask := statusFilterMask(tr.Status); mask != 0; mask &= mask - 1 {
			counts[bits.TrailingZeros(mask)]++
		}
	}
	return counts
}

// isTrackSelected / setTrackSelected / toggleTrackSelected 统一走路径键，
// 扫描时分批追加、整片替换都不影响已勾选的行。
func (a *app) isTrackSelected(path string) bool {
	return a.libSelected[normPathKey(path)]
}

func (a *app) setTrackSelected(path string, sel bool) {
	key := normPathKey(path)
	if key == "" {
		return
	}
	if !sel {
		delete(a.libSelected, key)
		return
	}
	if a.libSelected == nil {
		a.libSelected = map[string]bool{}
	}
	a.libSelected[key] = true
}

// pruneSelectedTracks 扫描收尾时丢掉已经不存在的勾选键（删了文件或换了文件夹）。
func (a *app) pruneSelectedTracks() {
	if len(a.libSelected) == 0 {
		return
	}
	alive := make(map[string]bool, len(a.tracks))
	for i := range a.tracks {
		alive[normPathKey(a.tracks[i].FullPath)] = true
	}
	for key := range a.libSelected {
		if !alive[key] {
			delete(a.libSelected, key)
		}
	}
}

func (a *app) toggleTrackSelected(path string) {
	a.setTrackSelected(path, !a.isTrackSelected(path))
}

func (a *app) selectedLibraryTracks() []trackItem {
	var batch []trackItem
	for i := range a.tracks {
		if a.isTrackSelected(a.tracks[i].FullPath) && a.trackMatchesFilter(&a.tracks[i]) {
			batch = append(batch, a.tracks[i])
		}
	}
	return batch
}

// pageSize 每页条数，只支持 50 / 100 两档。
func (a *app) pageSize() int {
	if a.libPageSize == 100 {
		return 100
	}
	return 50
}

// setPageSize 换每页条数时回到第一页，免得停在越界的页码上。
func (a *app) setPageSize(n int) {
	if n != 100 {
		n = 50
	}
	if a.libPageSize == n {
		return
	}
	a.libPageSize = n
	a.libPage = 0
	a.savePrefs()
}

// setPageSelected 只选当前这一页。
func (a *app) setPageSelected(sel bool) {
	size := a.pageSize()
	page := 0
	for i := range a.tracks {
		if !a.trackMatchesFilter(&a.tracks[i]) {
			continue
		}
		if page/size == a.libPage {
			a.setTrackSelected(a.tracks[i].FullPath, sel)
		}
		page++
	}
}

func (a *app) setVisibleTracksSelected(sel bool) {
	for i := range a.tracks {
		if a.trackMatchesFilter(&a.tracks[i]) {
			a.setTrackSelected(a.tracks[i].FullPath, sel)
		}
	}
}

var (
	reNeteaseIDParam = regexp.MustCompile(`[?&#]id=(\d+)`)
	reNeteaseIDSong  = regexp.MustCompile(`/song/(\d+)`)
	reNeteaseIDNum   = regexp.MustCompile(`\b(\d{4,})\b`)
)

// parseNeteaseID extracts a numeric NetEase song ID from raw digits or a music.163.com URL.
func parseNeteaseID(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	allDigits := len(s) >= 4
	for _, r := range s {
		if r < '0' || r > '9' {
			allDigits = false
			break
		}
	}
	if allDigits {
		return s
	}
	if m := reNeteaseIDParam.FindStringSubmatch(s); len(m) == 2 {
		return m[1]
	}
	if m := reNeteaseIDSong.FindStringSubmatch(s); len(m) == 2 {
		return m[1]
	}
	if m := reNeteaseIDNum.FindStringSubmatch(s); len(m) == 2 {
		return m[1]
	}
	return ""
}

func (a *app) batchIdTargetTracks() []trackItem {
	var all, sel []trackItem
	for i := range a.tracks {
		if isNoLyricsStatus(a.tracks[i].Status) {
			all = append(all, a.tracks[i])
			if a.isTrackSelected(a.tracks[i].FullPath) {
				sel = append(sel, a.tracks[i])
			}
		}
	}
	if len(sel) > 0 {
		return sel
	}
	return all
}

func (a *app) toggleInlineDrawer(tr *trackItem, mode string) {
	if strings.EqualFold(a.libExpandedPath, tr.FullPath) && a.libExpandedMode == mode {
		a.libExpandedPath = ""
		a.libExpandedMode = ""
		return
	}
	a.libBatchIdOpen = false
	a.libExpandedPath = tr.FullPath
	a.libExpandedMode = mode
	if mode == "netease" {
		if a.libBatchIdInputs != nil {
			a.libSingleIdInput = a.libBatchIdInputs[tr.FullPath]
		} else {
			a.libSingleIdInput = ""
		}
	} else if mode == "manual" {
		a.libManualText = ""
		a.libManualFilePath = ""
		switch tr.Lang {
		case "日文":
			a.libManualLang = 1
		case "英文":
			a.libManualLang = 2
		default:
			a.libManualLang = 0
		}
	}
}

func (a *app) upsertQueueItem(q queueItem) {
	for i := range a.queueItems {
		if strings.EqualFold(a.queueItems[i].AudioPath, q.AudioPath) {
			if a.queueItems[i].Status != "running" {
				a.queueItems[i] = q
			}
			return
		}
	}
	a.queueItems = append(a.queueItems, q)
}

func (a *app) enqueueLineOnlyTracks() {
	var batch []trackItem
	for _, tr := range a.tracks {
		if tr.Status == "line_only" {
			batch = append(batch, tr)
		}
	}
	if len(batch) == 0 {
		a.toast = "当前没有仅整行 LRC 歌曲"
		return
	}
	a.enqueueTracks(batch, false, false)
	a.navigate(pageQueue, 1)
}

func (a *app) enqueueNoLyricsAsr(batch []trackItem) {
	if len(batch) == 0 {
		a.toast = "请先勾选至少 1 首无歌词歌曲"
		return
	}
	for _, tr := range batch {
		a.upsertQueueItem(queueItem{
			Title:       tr.Title,
			Artist:      tr.Artist,
			Sub:         fmt.Sprintf("%s · %s · 本地 ASR 听写", tr.Artist, tr.Lang),
			AudioPath:   tr.FullPath,
			LrcPath:     tr.LrcPath,
			Lang:        langCode(tr.Lang),
			AsrFallback: true,
			Status:      "waiting",
			Note:        "本地 ASR 人声识别 + 逐字对齐",
			Hue:         tr.Hue,
		})
	}
	a.startQueueRunner()
	a.toast = fmt.Sprintf("已入队 %d 首（--asr-fallback 本地人声识别）", len(batch))
	a.navigate(pageQueue, 1)
}

func (a *app) enqueueNoLyricsRetrySearch(batch []trackItem) {
	if len(batch) == 0 {
		a.toast = "请先勾选要重试全网搜索的歌曲"
		return
	}
	for _, tr := range batch {
		a.upsertQueueItem(queueItem{
			Title:     tr.Title,
			Artist:    tr.Artist,
			Sub:       fmt.Sprintf("%s · %s · 重试全网搜索", tr.Artist, tr.Lang),
			AudioPath: tr.FullPath,
			LrcPath:   tr.LrcPath,
			Lang:      langCode(tr.Lang),
			Status:    "waiting",
			Note:      "重试 网易云 → QQ → 酷狗 → LRCLIB",
			Hue:       tr.Hue,
		})
	}
	a.startQueueRunner()
	a.toast = fmt.Sprintf("已入队 %d 首（重新尝试多源全网搜索）", len(batch))
	a.navigate(pageQueue, 1)
}

func (a *app) enqueueSingleNetease(tr *trackItem, rawID string) {
	pid := parseNeteaseID(rawID)
	if pid == "" {
		a.toast = "请输入有效的网易云歌曲 ID 或详情页链接"
		return
	}
	if a.libBatchIdInputs == nil {
		a.libBatchIdInputs = make(map[string]string)
	}
	a.libBatchIdInputs[tr.FullPath] = rawID
	a.upsertQueueItem(queueItem{
		Title:     tr.Title,
		Artist:    tr.Artist,
		Sub:       fmt.Sprintf("%s · %s · 网易云 ID %s", tr.Artist, tr.Lang, pid),
		AudioPath: tr.FullPath,
		LrcPath:   tr.LrcPath,
		Lang:      langCode(tr.Lang),
		NeteaseID: pid,
		Status:    "waiting",
		Note:      fmt.Sprintf("按网易云 ID %s 下载歌词并对齐", pid),
		Hue:       tr.Hue,
	})
	a.libExpandedPath = ""
	a.libExpandedMode = ""
	a.startQueueRunner()
	a.toast = fmt.Sprintf("已入队 1 首（--netease-id %s）：%s", pid, tr.Title)
	a.navigate(pageQueue, 1)
}

func (a *app) enqueueBatchNetease() {
	targets := a.batchIdTargetTracks()
	count := 0
	for _, tr := range targets {
		raw := ""
		if a.libBatchIdInputs != nil {
			raw = a.libBatchIdInputs[tr.FullPath]
		}
		pid := parseNeteaseID(raw)
		if pid == "" {
			continue
		}
		a.upsertQueueItem(queueItem{
			Title:     tr.Title,
			Artist:    tr.Artist,
			Sub:       fmt.Sprintf("%s · %s · 网易云 ID %s", tr.Artist, tr.Lang, pid),
			AudioPath: tr.FullPath,
			LrcPath:   tr.LrcPath,
			Lang:      langCode(tr.Lang),
			NeteaseID: pid,
			Status:    "waiting",
			Note:      fmt.Sprintf("按网易云 ID %s 下载歌词并对齐", pid),
			Hue:       tr.Hue,
		})
		count++
	}
	if count == 0 {
		a.toast = "尚未填写有效的网易云歌曲 ID，请至少填写 1 首"
		return
	}
	a.libBatchIdOpen = false
	a.startQueueRunner()
	a.toast = fmt.Sprintf("已按网易云 ID 批量入队 %d 首（未填写的行已跳过）", count)
	a.navigate(pageQueue, 1)
}

func manualRefPathForAudio(audioPath string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(strings.ToLower(audioPath)))
	dir := filepath.Join("work", "manual_refs")
	_ = os.MkdirAll(dir, 0o755)
	return filepath.Join(dir, fmt.Sprintf("manual_%08x.ref.txt", h.Sum32()))
}

func countNonEmptyLines(s string) int {
	n := 0
	for _, ln := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(ln) != "" {
			n++
		}
	}
	return n
}

func (a *app) pasteClipboardToManual() {
	txt := strings.TrimSpace(readClipboardText())
	if txt == "" {
		a.toast = "剪贴板为空，请先复制歌词文本"
		return
	}
	a.libManualText = txt
	a.toast = fmt.Sprintf("已从剪贴板粘贴 %d 行歌词", countNonEmptyLines(txt))
}

func (a *app) importManualLyricsFile() {
	go func() {
		p, err := chooseFile(a.win, "导入参考歌词 (.txt / .lrc)", "导入歌词", []mygo.FileFilter{
			{Name: "歌词或文本文件", Extensions: []string{"txt", "lrc"}},
		})
		if err != nil || p == "" {
			return
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			a.post(func() { a.toast = "读取文件失败：" + err.Error() })
			return
		}
		txt := strings.TrimSpace(strings.TrimPrefix(string(raw), "\xef\xbb\xbf"))
		a.post(func() {
			a.libManualText = txt
			a.libManualFilePath = p
			a.toast = fmt.Sprintf("已导入 %s（%d 行）", filepath.Base(p), countNonEmptyLines(txt))
		})
	}()
}

func (a *app) openManualLyricsInNotepad(tr *trackItem) {
	refPath := manualRefPathForAudio(tr.FullPath)
	if strings.TrimSpace(a.libManualText) != "" {
		_ = os.WriteFile(refPath, []byte(a.libManualText+"\n"), 0o644)
	} else if _, err := os.Stat(refPath); err != nil {
		_ = os.WriteFile(refPath, []byte(""), 0o644)
	}
	a.libManualFilePath = refPath
	absPath, err := filepath.Abs(refPath)
	if err != nil {
		absPath = refPath
	}
	_ = exec.Command("notepad.exe", absPath).Start()
	a.toast = "已用记事本打开参考歌词文件，保存后点击「读取记事本内容」或直接开始对齐"
}

func (a *app) reloadManualLyricsFromFile() {
	if a.libManualFilePath == "" {
		return
	}
	raw, err := os.ReadFile(a.libManualFilePath)
	if err != nil {
		return
	}
	txt := strings.TrimSpace(strings.TrimPrefix(string(raw), "\xef\xbb\xbf"))
	if txt != "" {
		a.libManualText = txt
		a.toast = fmt.Sprintf("已读取 %d 行歌词", countNonEmptyLines(txt))
	}
}

func (a *app) enqueueSingleManual(tr *trackItem) {
	// If user edited in Notepad without clicking reload, pick up the saved file automatically
	if strings.TrimSpace(a.libManualText) == "" && a.libManualFilePath != "" {
		if raw, err := os.ReadFile(a.libManualFilePath); err == nil {
			a.libManualText = strings.TrimSpace(strings.TrimPrefix(string(raw), "\xef\xbb\xbf"))
		}
	}
	nLines := countNonEmptyLines(a.libManualText)
	if nLines == 0 {
		a.toast = "请先粘贴、导入或在记事本中填写至少 1 行歌词文本"
		return
	}
	refPath := manualRefPathForAudio(tr.FullPath)
	if err := os.WriteFile(refPath, []byte(strings.TrimSpace(a.libManualText)+"\n"), 0o644); err != nil {
		a.toast = "保存参考词失败：" + err.Error()
		return
	}
	lang := "zh"
	langLbl := "中文"
	if a.libManualLang == 1 {
		lang = "ja"
		langLbl = "日文"
	} else if a.libManualLang == 2 {
		lang = "en"
		langLbl = "英文"
	}
	a.upsertQueueItem(queueItem{
		Title:     tr.Title,
		Artist:    tr.Artist,
		Sub:       fmt.Sprintf("%s · %s · 手动参考词 (%d行)", tr.Artist, langLbl, nLines),
		AudioPath: tr.FullPath,
		LrcPath:   tr.LrcPath,
		Lang:      lang,
		RefPath:   refPath,
		Status:    "waiting",
		Note:      fmt.Sprintf("手动参考词 (%d 行) · ASR 句定位 + 逐字对齐", nLines),
		Hue:       tr.Hue,
	})
	a.libExpandedPath = ""
	a.libExpandedMode = ""
	a.startQueueRunner()
	a.toast = fmt.Sprintf("已保存 %d 行参考词并入队：%s", nLines, tr.Title)
	a.navigate(pageQueue, 1)
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

func (a *app) enqueueTracks(batch []trackItem, ignoreRefTimes, ignoreRefEnds bool) {
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
					a.queueItems[i].IgnoreRefTimes = ignoreRefTimes
					a.queueItems[i].IgnoreRefEnds = ignoreRefEnds
					a.queueItems[i].Note = "等待重新对齐"
				}
				break
			}
		}
		if !found {
			a.queueItems = append(a.queueItems, queueItem{
				Title:          tr.Title,
				Artist:         tr.Artist,
				Sub:            fmt.Sprintf("%s · %s · %s", tr.Artist, tr.Lang, tr.File),
				AudioPath:      tr.FullPath,
				LrcPath:        tr.LrcPath,
				Lang:           langCode(tr.Lang),
				Status:         "waiting",
				Note:           tr.Note,
				Hue:            tr.Hue,
				IgnoreRefTimes: ignoreRefTimes,
				IgnoreRefEnds:  ignoreRefEnds,
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
	a.enqueueTracks(batch, false, false)
}

func (a *app) enqueueCurrentAlign() {
	if a.alignBatchDir != "" && len(a.alignBatchTracks) > 0 {
		a.enqueueTracks(a.alignBatchTracks, a.alignIgnoreRefTimes, a.alignIgnoreRefEnds)
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
	engine := "HubertFA 歌声对齐"
	if a.alignLang == 1 {
		lang = "ja"
		langLbl = "日文"
		engine = "NextFire MMS 日语歌声"
	} else if a.alignLang == 2 {
		lang = "en"
		langLbl = "英文"
		engine = "Echora-300M MMS 英文歌声"
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
				a.queueItems[i].NeteaseID = strings.TrimSpace(a.alignNeteaseID)
				a.queueItems[i].WaveformFirst = a.alignWaveformFirst
				a.queueItems[i].IgnoreRefTimes = a.alignIgnoreRefTimes
				a.queueItems[i].IgnoreRefEnds = a.alignIgnoreRefEnds
				a.queueItems[i].Note = "等待重新对齐"
			}
			break
		}
	}
	if !found {
		a.queueItems = append(a.queueItems, queueItem{
			Title:          title,
			Artist:         a.alignArtist,
			Sub:            sub,
			AudioPath:      a.alignAudioPath,
			LrcPath:        a.alignOut,
			Lang:           lang,
			NeteaseID:      strings.TrimSpace(a.alignNeteaseID),
			WaveformFirst:  a.alignWaveformFirst,
			IgnoreRefTimes: a.alignIgnoreRefTimes,
			IgnoreRefEnds:  a.alignIgnoreRefEnds,
			Status:         "waiting",
			Note:           "优先使用本地同目录 LRC",
			Hue:            hueForString(title),
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
	py := a.pythonExe()
	var worker alignWorker
	defer worker.close()
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

		args := []string{item.AudioPath, "--lang", item.Lang}
		if !a.setAutoSkipNoVocal {
			args = append(args, "--force-vocal")
		}
		if item.IgnoreRefTimes {
			args = append(args, "--ignore-ref-times")
		} else if item.IgnoreRefEnds {
			args = append(args, "--ignore-ref-ends")
		}
		if a.setEnMode == 1 || a.alignEnMode == 1 {
			args = append(args, "--en-granularity", "char")
		}
		if item.RefPath != "" {
			args = append(args, "--ref", item.RefPath)
		} else if a.alignSource == 1 && a.alignRefLrc != "" && strings.EqualFold(item.AudioPath, a.alignAudioPath) {
			args = append(args, "--ref", a.alignRefLrc)
		}
		if id := strings.TrimSpace(item.NeteaseID); id != "" {
			args = append(args, "--netease-id", id)
		}
		if item.AsrFallback {
			args = append(args, "--asr-fallback")
		}
		if item.WaveformFirst {
			args = append(args, "--waveform-first")
		}
		var err error
		if worker.cmd == nil {
			cmd := newHiddenCmd(py, "-u", "-X", "utf8", "-m", "kiralrc", "--align-worker")
			cmd.Dir = appRootDir()
			cmd.Env = a.pythonSubprocessEnv("PYTHONUNBUFFERED=1")
			err = worker.start(cmd)
		}
		var lastLine string
		var skippedNoVocal bool
		var skippedNoLyrics bool
		if err == nil {
			err = worker.run(args, func(raw string) {
				line := cleanLogLine(raw)
				if line == "" {
					return
				}
				lastLine = line
				if strings.HasPrefix(line, "[skip-nolyrics]") {
					skippedNoLyrics = true
				} else if strings.HasPrefix(line, "[skip]") {
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
			})
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
				case skippedNoLyrics:
					a.queueItems[i].Status = "skipped_nolyrics"
					a.queueItems[i].Note = lastLine
					a.markTrackNotFound(songPath)
				case err != nil:
					a.queueItems[i].Status = "failed"
					if lastLine != "" {
						a.queueItems[i].Note = lastLine
					} else {
						a.queueItems[i].Note = err.Error()
					}
					if strings.Contains(lastLine, "未搜到") || strings.Contains(lastLine, "没有参考文件") {
						a.markTrackNotFound(songPath)
					}
				default:
					a.queueItems[i].Status = "done"
					a.queueItems[i].Stage = 6
					a.queueItems[i].Note = "逐字 LRC 已生成"
					a.clearTrackNotFound(songPath)
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
					if ainfo, aerr := os.Stat(audioPath); aerr == nil && ainfo.ModTime().After(info.ModTime()) {
						continue
					}
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
			py := getActivePythonPath()
			cmd := newHiddenCmd(py, "-X", "utf8", "-m", "kiralrc", "cover", ap)
			cmd.Dir = appRootDir()
			cmd.Env = a.pythonSubprocessEnv()
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
	a.savePrefs()
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
	if a.edEditingLine != idx {
		a.edEditingLine = -1
	}
	a.edSelectedLine = idx
	a.edSelectedChar = 0
	a.edSrcScrollToSelected = true
	if len(a.edLines[idx].Chars) > 0 {
		a.edGlyphs = a.edLines[idx].Chars
		a.edPlayhead = a.edGlyphs[0].Start
	}
}

func (a *app) pruneMissingRecents() {
	kept := a.recents[:0]
	changed := false
	for _, r := range a.recents {
		if r.Path == "" {
			continue
		}
		if _, err := os.Stat(r.Path); err == nil {
			kept = append(kept, r)
		} else {
			changed = true
		}
	}
	a.recents = kept
	if changed {
		a.savePrefs()
	}
}

func (a *app) clearEditorState() {
	if a.player != nil {
		a.player.Pause()
	}
	a.edPlaying = false
	a.edFile = ""
	a.edPath = ""
	a.edSub = ""
	a.edLines = nil
	a.edGlyphs = nil
	a.edMetas = nil
	a.edIssues = nil
	a.edModified = false
	a.edEditingLine = -1
	a.edSrcEditing = false
	a.edSrcText = ""
	a.edCover = nil
	a.edCoverAudio = ""
}

func (a *app) refreshLibrary() {
	a.pruneMissingRecents()
	if a.edPath != "" {
		if _, err := os.Stat(a.edPath); err != nil {
			a.clearEditorState()
		}
	}
	if len(a.libFolders) > 0 {
		a.scanLibraryAsync()
		a.toast = "正在刷新曲库状态…"
	}
}

func (a *app) refreshEditor() {
	if a.edPath == "" {
		return
	}
	if _, err := os.Stat(a.edPath); err != nil {
		a.clearEditorState()
		a.pruneMissingRecents()
		if len(a.libFolders) > 0 {
			a.scanLibraryAsync()
		}
		a.toast = "本地 LRC 文件已删除，已刷新曲库状态"
		return
	}
	prevLine := a.edSelectedLine
	a.edCover = nil
	a.edCoverAudio = ""
	if err := a.loadLrcFile(a.edPath); err == nil {
		if prevLine >= 0 && prevLine < len(a.edLines) {
			a.selectEditorLine(prevLine)
		}
		a.toast = "已从磁盘重新加载 LRC"
	}
}

func (a *app) beginEditLyricLine(idx int) {
	if idx < 0 || idx >= len(a.edLines) {
		return
	}
	a.selectEditorLine(idx)
	a.edEditingLine = idx
	a.edEditingText = a.edLines[idx].Text
}

func (a *app) beginEditLrcSource() {
	a.edEditingLine = -1
	var sb strings.Builder
	for _, m := range a.edMetas {
		sb.WriteString(m)
		sb.WriteString("\n")
	}
	caretRune := 0
	for i, ln := range a.edLines {
		if i == a.edSelectedLine {
			caretRune = len([]rune(sb.String()))
		}
		sb.WriteString(formatLrcLine(ln))
		sb.WriteString("\n")
	}
	a.edSrcText = sb.String()
	a.edSrcInitCaret = caretRune
	a.edSrcNeedCaret = true
	a.edSrcEditing = true
}

func (a *app) cancelEditLrcSource() {
	a.edSrcEditing = false
	a.edSrcText = ""
}

func (a *app) saveEditLrcSource() {
	if !a.edSrcEditing || a.edPath == "" {
		return
	}
	norm := strings.ReplaceAll(a.edSrcText, "\r\n", "\n")
	if !strings.HasSuffix(norm, "\n") {
		norm += "\n"
	}
	if err := os.WriteFile(a.edPath, []byte(norm), 0o644); err != nil {
		a.toast = "保存 LRC 源码失败：" + err.Error()
		return
	}
	prevLine := a.edSelectedLine
	a.edSrcEditing = false
	a.edSrcText = ""
	if err := a.loadLrcFile(a.edPath); err == nil {
		if prevLine >= 0 && prevLine < len(a.edLines) {
			a.selectEditorLine(prevLine)
		}
		a.toast = "已保存 LRC 源码"
	}
}

func (a *app) commitEditLyricLine() {
	idx := a.edEditingLine
	if idx < 0 || idx >= len(a.edLines) {
		a.edEditingLine = -1
		return
	}
	newText := strings.TrimSpace(a.edEditingText)
	a.edEditingLine = -1
	if newText == "" {
		return
	}
	ln := &a.edLines[idx]
	if newText == ln.Text {
		return
	}
	ln.Text = newText
	newRunes := []rune(newText)
	oldChars := ln.Chars

	if len(oldChars) == len(newRunes) && len(newRunes) > 0 {
		// 改字不改字数（如改同音错别字/繁转简）：100% 保留每个字的逐字时间戳
		updated := make([]lyricChar, len(oldChars))
		copy(updated, oldChars)
		for i, r := range newRunes {
			updated[i].Ch = string(r)
		}
		ln.Chars = updated
	} else if len(newRunes) > 0 {
		// 字数变化：保留公共前缀与后缀的时间戳，仅对中间变动区段按比例重算
		oldRunes := make([]rune, 0, len(oldChars))
		flatChars := make([]lyricChar, 0, len(oldChars))
		for _, ch := range oldChars {
			rs := []rune(ch.Ch)
			if len(rs) <= 1 {
				if len(rs) == 1 {
					oldRunes = append(oldRunes, rs[0])
					flatChars = append(flatChars, ch)
				}
				continue
			}
			step := (ch.End - ch.Start) / float64(len(rs))
			for ri, r := range rs {
				oldRunes = append(oldRunes, r)
				flatChars = append(flatChars, lyricChar{
					Ch:    string(r),
					Start: ch.Start + float64(ri)*step,
					End:   ch.Start + float64(ri+1)*step,
				})
			}
		}
		pref := 0
		for pref < len(oldRunes) && pref < len(newRunes) && oldRunes[pref] == newRunes[pref] {
			pref++
		}
		suff := 0
		for suff < len(oldRunes)-pref && suff < len(newRunes)-pref &&
			oldRunes[len(oldRunes)-1-suff] == newRunes[len(newRunes)-1-suff] {
			suff++
		}
		tStart := ln.Start
		tEnd := max(ln.End, ln.Start+0.2)
		if pref > 0 {
			tStart = flatChars[pref-1].End
		}
		if suff > 0 {
			tEnd = flatChars[len(flatChars)-suff].Start
		}
		if tEnd < tStart+0.05 {
			tEnd = tStart + 0.05
		}
		midNew := len(newRunes) - pref - suff
		updated := make([]lyricChar, 0, len(newRunes))
		updated = append(updated, flatChars[:pref]...)
		if midNew > 0 {
			step := (tEnd - tStart) / float64(midNew)
			for mi := 0; mi < midNew; mi++ {
				cs := tStart + float64(mi)*step
				ce := cs + step
				if mi == midNew-1 {
					ce = tEnd
				}
				updated = append(updated, lyricChar{
					Ch:    string(newRunes[pref+mi]),
					Start: cs,
					End:   ce,
				})
			}
		}
		if suff > 0 {
			updated = append(updated, flatChars[len(flatChars)-suff:]...)
		}
		ln.Chars = updated
	}

	if a.edSelectedLine == idx && len(ln.Chars) > 0 {
		a.edGlyphs = ln.Chars
		if a.edSelectedChar >= len(a.edGlyphs) {
			a.edSelectedChar = len(a.edGlyphs) - 1
		}
	}
	a.edModified = true
	a.toast = fmt.Sprintf("已修改第 %d 行歌词（点击「保存 LRC」落盘）", idx+1)
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
		if sib := findSiblingAudio(path); enhLines > 0 && sib != "" {
			a.checkEditorLrc(path, sib)
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
			Title:           "Kiralrc",
			Width:           1360,
			Height:          860,
			MinWidth:        1040,
			MinHeight:       680,
			TitleBarStyle:   mygo.TitleBarHidden,
			TitleBarHeight:  buttonsHeight,
			BackgroundColor: "light-dark(#ffffff, #141416)",
			Content:         ui.View(a.view),
		})
		applyWindowIcon("Kiralrc")
		if dir := os.Getenv("KIRALRC_CLEANUP_DEMO"); dir != "" {
			a.cleanupDir = dir
			a.page = pageCleanup
			a.scanCleanup()
		}
		a.refreshBackendInfo()
		if len(a.libFolders) > 0 {
			a.scanLibraryAsync()
		}
	})
	err := mygo.App.Run()
	purgeSepCache()       // 分离的临时音轨跟着程序一起走
	cleanupScrapeThumbs() // 刮削画廊的缩略图也只活一次运行
	return err
}
