package app

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestMain(m *testing.M) {
	// Ensure go test never reads or overwrites the user's real %APPDATA%\Kiralrc\prefs.json.
	_ = os.Setenv("KIRALRC_NO_PREFS", "1")
	os.Exit(m.Run())
}

// Checks that the sidebar navigates across all six views and that the settings'
// color swatches update the app's accent and appearance.
func TestNavigationAndAccentSwitching(t *testing.T) {
	t.Setenv("KIRALRC_NO_PREFS", "1")
	a := newApp()
	tt := ui.NewTester(a.view, 1360, 860)
	tt.Frame()

	for _, nav := range []struct {
		label string
		want  pageKind
	}{
		{"新建对齐", pageAlign},
		{"任务队列", pageQueue},
		{"编辑与校对", pageEditor},
		{"工具箱", pageTools},
		{"曲库", pageLibrary},
	} {
		if err := tt.Click(nav.label); err != nil {
			t.Fatalf("click %s: %v", nav.label, err)
		}
		tt.Frame()
		if a.page != nav.want {
			t.Fatalf("after clicking %s: got page %v, want %v", nav.label, a.page, nav.want)
		}
	}

	if err := tt.Click("设置"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.settingsOpen || a.page != pageLibrary {
		t.Fatal("设置应以弹窗打开并保留当前页面")
	}
	initial := tt.Image().At(85, 430)
	tt.Advance(motionPane.Duration)
	if tt.Image().At(85, 430) == initial {
		t.Fatal("设置卡片打开时应有可见过渡")
	}
	if err := tt.Click("主题色：紫罗兰"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.accentID != "violet" || a.accentHex != "#7C5CD6" {
		t.Fatalf("got accent %s (%s), want violet", a.accentID, a.accentHex)
	}
	if err := tt.Click("缓存管理"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if _, ok := tt.Find("立即清理"); !ok {
		t.Fatal("缓存分类未显示原有清理操作")
	}
	if err := tt.Click("搜索设置"); err != nil {
		t.Fatal(err)
	}
	tt.Type("Python")
	tt.Frame()
	if a.settingsSection != settingsRuntime {
		t.Fatal("搜索 Python 应切换到模型与运行环境")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.settingsOpen || a.page != pageLibrary {
		t.Fatal("Escape 应关闭设置并返回原页面")
	}
	if _, ok := tt.Find("关闭设置"); !ok {
		t.Fatal("退出动画期间应保留设置卡片")
	}
	tt.Advance(motionPane.Duration + 20*time.Millisecond)
	if _, ok := tt.Find("关闭设置"); ok {
		t.Fatal("退出动画结束后应移除设置卡片")
	}
	if err := tt.Click("设置"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(motionPane.Duration + 20*time.Millisecond)
	if err := tt.Click("关闭设置"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.settingsOpen {
		t.Fatal("关闭按钮应关闭设置")
	}
	tt.Advance(motionPane.Duration + 20*time.Millisecond)

	// On startup, Library, Queue, Recents, and Editor should ALL start in a clean empty state.
	if len(a.libFolders) != 0 || len(a.tracks) != 0 || len(a.recents) != 0 || len(a.queueItems) != 0 || a.queueActiveTitle != "" || a.edFile != "" {
		t.Fatalf("expected completely clean empty state on startup")
	}

	// Simulate user adding a music folder and scanning it dynamically.
	tmpMusic := t.TempDir()
	lrc1 := filepath.Join(tmpMusic, "伍佰 - 突然的自我.lrc")
	_ = os.WriteFile(filepath.Join(tmpMusic, "伍佰 - 突然的自我.flac"), []byte("RIFF"), 0o644)
	_ = os.WriteFile(lrc1, []byte("[00:01.000]听见你说\n[00:04.000]朝阳起又落\n"), 0o644)
	_ = os.WriteFile(filepath.Join(tmpMusic, "ヨルシカ - 晴る.flac"), []byte("RIFF"), 0o644)
	_ = os.WriteFile(filepath.Join(tmpMusic, "ヨルシカ - 晴る.lrc"), []byte("[00:01.000]<00:01.000>晴<00:01.500>る<00:02.000>\n"), 0o644)
	_ = os.WriteFile(filepath.Join(tmpMusic, "泽野弘之 - Battle (OST).flac"), []byte("RIFF"), 0o644)

	a.libFolders = []string{tmpMusic}
	a.tracks, a.libStats = a.scanLibrarySync()
	if a.libStats.Total != 3 || a.libStats.Vocal != 2 || a.libStats.LineOnly != 1 || a.libStats.Enhanced != 1 || a.libStats.InstOST != 1 {
		t.Fatalf("unexpected scanned library stats: %+v", a.libStats)
	}

	// Verify interactive selection on Library track rows, Align candidates, and Editor character blocks.
	if err := tt.Click("曲库"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tIdx := -1
	for i, tr := range a.tracks {
		if tr.Title == "突然的自我" {
			tIdx = i
			break
		}
	}
	if tIdx < 0 {
		t.Fatal("track 突然的自我 not found in scanned tracks")
	}
	wasSel := a.isTrackSelected(a.tracks[tIdx].FullPath)
	if err := tt.Click("突然的自我"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.isTrackSelected(a.tracks[tIdx].FullPath) == wasSel {
		t.Fatalf("clicking track row should toggle selection")
	}

	if err := a.loadLrcFile(lrc1); err != nil {
		t.Fatal(err)
	}
	if len(a.recents) != 1 {
		t.Fatalf("expected 1 recent item after loading LRC file, got %d", len(a.recents))
	}
	if err := tt.Click("编辑与校对"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("字块：你"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.edSelectedChar != 2 {
		t.Fatalf("expected edSelectedChar=2 after clicking 字块：你, got %d", a.edSelectedChar)
	}

	// Verify mouse dragging on the sidebar resizer and editor waveform scrubber.
	tt.Press(230, 300)
	tt.Move(270, 300)
	tt.Frame()
	tt.Release(270, 300)
	tt.Frame()
	if a.sideWidthPx <= 232 {
		t.Fatalf("expected sidebar width to increase after dragging resizer, got %v", a.sideWidthPx)
	}

	beforePh := a.edPlayhead
	tt.Press(500, 710)
	tt.Move(760, 710)
	tt.Frame()
	tt.Release(760, 710)
	tt.Frame()
	if a.edPlayhead == beforePh {
		t.Fatalf("expected edPlayhead to change after dragging waveform scrubber, still %v", a.edPlayhead)
	}
}

// Checks that loading an LRC file parses its enhanced character timestamps into
// the editor's lines.
func TestLoadLrcFileIntoEditor(t *testing.T) {
	a := newApp()
	p := filepath.Join(t.TempDir(), "test.lrc")
	content := "[ti:测试]\n[00:01.000]<00:01.000>晚<00:01.400>风<00:02.000>\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.loadLrcFile(p); err != nil {
		t.Fatal(err)
	}
	if len(a.edLines) != 1 || len(a.edLines[0].Chars) != 2 {
		t.Fatalf("unexpected parsed lines: %+v", a.edLines)
	}
	if a.edLines[0].Chars[1].Ch != "风" || a.edLines[0].End != 2.0 {
		t.Fatalf("unexpected char timing: %+v", a.edLines[0])
	}
}

func TestNoLyricsWorkflowAndNeteaseIDParsing(t *testing.T) {
	cases := map[string]string{
		"30967650": "30967650",
		"https://music.163.com/#/song?id=30967650":    "30967650",
		"https://music.163.com/song/30967650?foo=bar": "30967650",
		"随便写的": "",
		"":     "",
	}
	for raw, want := range cases {
		if got := parseNeteaseID(raw); got != want {
			t.Fatalf("parseNeteaseID(%q) = %q, want %q", raw, got, want)
		}
	}

	a := newApp()
	tmp := t.TempDir()
	a1 := filepath.Join(tmp, "芹澤優 - ずっと見ていたい.flac")
	a2 := filepath.Join(tmp, "伍佰 - 突然的自我.flac")
	_ = os.WriteFile(a1, []byte("RIFF"), 0o644)
	_ = os.WriteFile(a2, []byte("RIFF"), 0o644)
	_ = os.WriteFile(filepath.Join(tmp, "伍佰 - 突然的自我.lrc"), []byte("[00:01.000]听见你说\n"), 0o644)

	a.libFolders = []string{tmp}
	a.markTrackNotFound(a1)
	a.tracks, a.libStats = a.scanLibrarySync()
	if a.libStats.Missing != 1 || a.libStats.NotFound != 1 || a.libStats.LineOnly != 1 {
		t.Fatalf("unexpected stats: %+v", a.libStats)
	}
	a.setLibFilter(3) // 无歌词 Tab
	tt := ui.NewTester(a.view, 1360, 860)
	tt.Frame()

	// 列表固定按相对路径排（不再让「全网无词」抢到前面），状态本身照旧区分
	a3 := filepath.Join(tmp, "新歌 - 本地缺词.flac")
	_ = os.WriteFile(a3, []byte("RIFF"), 0o644)
	a.tracks, a.libStats = a.scanLibrarySync()
	if len(a.tracks) < 3 {
		t.Fatalf("expected 3 tracks, got %+v", a.tracks)
	}
	byName := map[string]string{}
	for _, tr := range a.tracks {
		byName[tr.Title] = tr.Status
	}
	if byName["ずっと見ていたい"] != "nolyrics_notfound" || byName["本地缺词"] != "missing" {
		t.Fatalf("unexpected statuses: %+v", byName)
	}
	for i := 1; i < len(a.tracks); i++ {
		if a.tracks[i-1].FullPath > a.tracks[i].FullPath && filepath.Dir(a.tracks[i-1].FullPath) == filepath.Dir(a.tracks[i].FullPath) {
			t.Fatalf("列表应按路径排，第 %d 条乱了: %s / %s", i, a.tracks[i-1].FullPath, a.tracks[i].FullPath)
		}
	}
	_ = os.WriteFile(filepath.Join(tmp, "芹澤優 - ずっと見ていたい.lrc"), []byte("[00:01.000]ずっと見ていたい\n"), 0o644)
	a.tracks, a.libStats = a.scanLibrarySync()
	if a.notFoundPaths[normPathKey(a1)] || a.libStats.NotFound != 0 {
		t.Fatalf("expected notFoundPaths auto-cleared once .lrc exists, got %+v", a.notFoundPaths)
	}
}

func TestInlineLyricLineEditAndRefreshDeletedLrc(t *testing.T) {
	a := newApp()
	p := filepath.Join(t.TempDir(), "asr.lrc")
	content := "[00:10.000]<00:10.000>給<00:10.300>我<00:10.600>苦<00:10.900>彼<00:11.200>時<00:11.600>\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.loadLrcFile(p); err != nil {
		t.Fatal(err)
	}
	// Same-length typo fix: timestamps must be preserved 100%
	a.beginEditLyricLine(0)
	a.edEditingText = "给我苦笔试"
	a.commitEditLyricLine()
	if a.edLines[0].Text != "给我苦笔试" || len(a.edLines[0].Chars) != 5 {
		t.Fatalf("unexpected edited line: %+v", a.edLines[0])
	}
	if a.edLines[0].Chars[3].Ch != "笔" || a.edLines[0].Chars[3].Start != 10.9 {
		t.Fatalf("expected preserved timestamp 10.9, got %+v", a.edLines[0].Chars[3])
	}
	// Length-changed edit: prefix/suffix preserved, middle remapped
	a.beginEditLyricLine(0)
	a.edEditingText = "给我一次苦笔试"
	a.commitEditLyricLine()
	if len(a.edLines[0].Chars) != 7 || a.edLines[0].Chars[0].Start != 10.0 || a.edLines[0].Chars[6].End != 11.6 {
		t.Fatalf("unexpected remapped chars: %+v", a.edLines[0].Chars)
	}
	// Deleting local LRC and clicking refresh clears editor & prunes recents
	_ = os.Remove(p)
	a.refreshEditor()
	if a.edFile != "" || len(a.recents) != 0 {
		t.Fatalf("expected editor cleared and recents pruned after deleting LRC, got edFile=%q recents=%d", a.edFile, len(a.recents))
	}
}

func TestPythonSubprocessEnvPassesAppleMusicToken(t *testing.T) {
	a := newApp()
	// Empty token: must not inject KIRALRC_APPLE_MEDIA_USER_TOKEN
	envEmpty := a.pythonSubprocessEnv("PYTHONUNBUFFERED=1")
	for _, kv := range envEmpty {
		if kv == "KIRALRC_APPLE_MEDIA_USER_TOKEN=" || len(kv) > 31 && kv[:31] == "KIRALRC_APPLE_MEDIA_USER_TOKEN=" {
			t.Fatalf("unexpected apple token env when empty: %q", kv)
		}
	}
	// Configured token + US storefront
	a.setAppleToken = "  test-token-xyz  "
	a.setAppleStorefront = 2
	envSet := a.pythonSubprocessEnv("PYTHONUNBUFFERED=1")
	var gotTok, gotSf string
	for _, kv := range envSet {
		switch {
		case len(kv) > 31 && kv[:31] == "KIRALRC_APPLE_MEDIA_USER_TOKEN=":
			gotTok = kv[31:]
		case len(kv) > 25 && kv[:25] == "KIRALRC_APPLE_STOREFRONT=":
			gotSf = kv[25:]
		}
	}
	if gotTok != "test-token-xyz" || gotSf != "us" {
		t.Fatalf("expected token=test-token-xyz sf=us, got token=%q sf=%q", gotTok, gotSf)
	}

	// Cloudy 登录态也从这一个出口走：所有 python 子进程才不会有漏掉的链路
	a.setCloudyAuth = true
	found := false
	for _, kv := range a.pythonSubprocessEnv() {
		if kv == "KIRALRC_CLOUDY_AUTH=1" {
			found = true
		}
	}
	if !found {
		t.Fatal("pythonSubprocessEnv 没把 KIRALRC_CLOUDY_AUTH 带上")
	}
}

func TestParseAITranslations(t *testing.T) {
	// 1) 纯 JSON 数组
	got, err := parseAITranslations(`["你在微笑", "晴空万里"]`, 2)
	if err != nil || len(got) != 2 || got[0] != "你在微笑" || got[1] != "晴空万里" {
		t.Fatalf("plain json array failed: got=%v err=%v", got, err)
	}

	// 2) 带 Markdown 代码块
	md := "```json\n[\"第一句\", \"第二句\"]\n```"
	got, err = parseAITranslations(md, 2)
	if err != nil || got[0] != "第一句" || got[1] != "第二句" {
		t.Fatalf("markdown fenced json failed: got=%v err=%v", got, err)
	}

	// 3) 对象包裹格式（translations / lines）
	got, err = parseAITranslations(`{"translations": ["A", "B"]}`, 2)
	if err != nil || got[0] != "A" || got[1] != "B" {
		t.Fatalf("wrapped translations failed: got=%v err=%v", got, err)
	}
	got, err = parseAITranslations(`{"lines": ["C", "D"]}`, 2)
	if err != nil || got[0] != "C" || got[1] != "D" {
		t.Fatalf("wrapped lines failed: got=%v err=%v", got, err)
	}

	// 4) 行数不匹配必须报错拦截
	if _, err := parseAITranslations(`["只有一行"]`, 2); err == nil {
		t.Fatalf("expected error on line count mismatch, got nil")
	}
}

func TestTranslateCurrentLyricsMockServer(t *testing.T) {
	if got := aiChatCompletionsURL("https://api.deepseek.com/v1/"); got != "https://api.deepseek.com/v1/chat/completions" {
		t.Fatalf("unexpected url: %s", got)
	}
	if got := aiChatCompletionsURL("https://api.deepseek.com/v1/chat/completions"); got != "https://api.deepseek.com/v1/chat/completions" {
		t.Fatalf("unexpected url: %s", got)
	}

	var gotAuth, gotPath string
	var gotReq aiChatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Errorf("decode request failed: %v", err)
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": "```json\n[\"你露出微笑\", \"今天天气放晴了\"]\n```"}},
			},
		})
	}))
	defer srv.Close()

	a := newApp()
	a.edFile = "晴る.lrc"
	a.edMetas = []string{"[ti:晴る]", "[ar:ヨルシカ]"}
	a.edLines = []lyricLine{
		{Index: 1, Start: 1.0, End: 3.0, Text: "君は笑う"},
		{Index: 2, Start: 3.5, End: 6.0, Text: "空が晴れる"},
	}

	if !a.editorNeedsTranslation() {
		t.Fatalf("expected Japanese lyrics to need translation")
	}

	// 默认官方内置模式无需 API Key 即视为已配置
	if !a.aiTranslateConfigured() {
		t.Fatalf("expected official AI translate mode to be configured by default")
	}
	if u, m, k := a.aiTranslateConfig(); u != officialAITranslateBaseURL || m != officialAITranslateModel || k != "" {
		t.Fatalf("unexpected official config: %q %q %q", u, m, k)
	}

	// 切换自定义模式且未配置时拦截并提示
	a.setAIMode = 1
	if err := a.translateCurrentLyricsSync(srv.Client()); err == nil {
		t.Fatalf("expected error when custom AI config is empty")
	}
	if !strings.Contains(a.toast, "设置") {
		t.Fatalf("expected settings toast, got %q", a.toast)
	}

	a.setAIBaseURL = srv.URL + "/v1"
	a.setAIModel = "deepseek-chat"
	a.setAIAPIKey = "sk-test-123"
	a.edModified = false

	if err := a.translateCurrentLyricsSync(srv.Client()); err != nil {
		t.Fatalf("translateCurrentLyricsSync failed: %v", err)
	}
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("expected path /v1/chat/completions, got %q", gotPath)
	}
	if gotAuth != "Bearer sk-test-123" {
		t.Fatalf("expected Bearer auth header, got %q", gotAuth)
	}
	if gotReq.Model != "deepseek-chat" || gotReq.Temperature != 0.3 || len(gotReq.Messages) != 2 {
		t.Fatalf("unexpected request payload: %+v", gotReq)
	}
	userMsg := gotReq.Messages[1].Content
	if !strings.Contains(userMsg, "晴る") || !strings.Contains(userMsg, "ヨルシカ") ||
		!strings.Contains(userMsg, "1. 君は笑う") || !strings.Contains(userMsg, "2. 空が晴れる") {
		t.Fatalf("user prompt missing song meta or full lyrics: %q", userMsg)
	}
	if a.edLines[0].Translation != "你露出微笑" || a.edLines[1].Translation != "今天天气放晴了" {
		t.Fatalf("translations not filled back: %+v", a.edLines)
	}
	if !a.edModified {
		t.Fatalf("expected edModified=true after translation")
	}

	// 中文歌应隐藏翻译按钮
	a.edLines = []lyricLine{{Index: 1, Text: "故事的小黄花"}, {Index: 2, Text: "从出生那年就飘着"}}
	if a.editorNeedsTranslation() {
		t.Fatalf("expected Chinese lyrics to hide translation button")
	}
}

func TestProtectUnprotectSecret(t *testing.T) {
	plain := "sk-live-super-secret-key-987654321"
	enc, err := protectSecret(plain)
	if err != nil {
		t.Fatalf("protectSecret failed: %v", err)
	}
	if !strings.HasPrefix(enc, "dpapi:") {
		t.Fatalf("expected dpapi: prefix, got %q", enc)
	}
	if strings.Contains(enc, plain) || strings.Contains(enc, "super-secret") {
		t.Fatalf("encrypted output leaked plaintext: %q", enc)
	}
	got, migrate, err := unprotectSecret(enc)
	if err != nil {
		t.Fatalf("unprotectSecret failed: %v", err)
	}
	if migrate || got != plain {
		t.Fatalf("expected (%q, false), got (%q, %v)", plain, got, migrate)
	}
	if _, _, err := unprotectSecret("dpapi:not-valid-cipher!!!"); err == nil {
		t.Fatalf("expected error on tampered ciphertext")
	}
	if got := maskAPIKey("sk-81a94999999999ac49"); got != "sk-81a94****ac49" {
		t.Fatalf("unexpected maskAPIKey: %q", got)
	}
}

// With KIRALRC_FRAMES set to a directory, saves PNG frames of all six views.
func TestRenderFrames(t *testing.T) {
	dir := os.Getenv("KIRALRC_FRAMES")
	if dir == "" {
		t.Skip("set KIRALRC_FRAMES to save PNG frames")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	scenes := []struct {
		name   string
		page   pageKind
		accent string
		dark   bool
	}{
		{"native-library", pageLibrary, "red", false},
		{"native-align", pageAlign, "clay", false},
		{"native-queue", pageQueue, "red", false},
		{"native-editor", pageEditor, "red", false},
		{"native-editor-dark", pageEditor, "violet", true},
		{"native-tools", pageTools, "teal", false},
		{"native-settings", pageLibrary, "clay", false},
	}
	for _, sc := range scenes {
		a := newApp()
		a.page = sc.page
		if sc.name == "native-settings" {
			a.openSettings(settingsAppearance)
		}
		a.setAccent(sc.accent)
		if sc.dark {
			a.appearance = 1
		}
		tt := ui.NewTester(a.view, 1360, 860)
		tt.SetDark(sc.dark)
		tt.Frame()
		tt.Advance(400 * time.Millisecond)
		tt.Frame()
		f, err := os.Create(filepath.Join(dir, sc.name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, tt.Image()); err != nil {
			f.Close()
			t.Fatal(err)
		}
		f.Close()
	}
}

func TestCandidatePythonsPriority(t *testing.T) {
	tmp := t.TempDir()
	customPy := filepath.Join(tmp, "custom_python.exe")
	envPy := filepath.Join(tmp, "env_python.exe")
	if err := os.WriteFile(customPy, []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(envPy, []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KIRALRC_PYTHON", envPy)

	cands := candidatePythons(customPy)
	if len(cands) < 2 {
		t.Fatalf("expected at least 2 candidates, got %v", cands)
	}
	if cands[0][0] != customPy || cands[0][1] != "自定义" {
		t.Fatalf("expected custom python first, got %+v", cands[0])
	}
	if cands[1][0] != envPy || cands[1][1] != "环境变量" {
		t.Fatalf("expected env python second, got %+v", cands[1])
	}
}

// 标签里写着歌名与歌手时，就别再靠文件名猜「标题 - 歌手」的顺序了。
func TestParseArtistTitlePrefersTags(t *testing.T) {
	dir := t.TempDir()
	flac := filepath.Join(dir, "千百度 - 许嵩.flac")
	writeTestFLAC(t, flac, "千百度", "许嵩")
	artist, title := parseArtistTitle(flac, "千百度 - 许嵩")
	if title != "千百度" || artist != "许嵩" {
		t.Fatalf("got title=%q artist=%q, want 千百度 / 许嵩", title, artist)
	}

	mp3 := filepath.Join(dir, "反向 - 命名.mp3")
	writeTestMP3(t, mp3, "反向的标题", "反向的歌手")
	artist, title = parseArtistTitle(mp3, "反向 - 命名")
	if title != "反向的标题" || artist != "反向的歌手" {
		t.Fatalf("mp3 tag: got title=%q artist=%q", title, artist)
	}
}

func writeTestFLAC(t *testing.T, path, title, artist string) {
	t.Helper()
	vendor := []byte("test")
	body := binary.LittleEndian.AppendUint32(nil, uint32(len(vendor)))
	body = append(body, vendor...)
	entries := []string{"TITLE=" + title, "ARTIST=" + artist, "ALBUM=专辑"}
	body = binary.LittleEndian.AppendUint32(body, uint32(len(entries)))
	for _, e := range entries {
		body = binary.LittleEndian.AppendUint32(body, uint32(len(e)))
		body = append(body, e...)
	}
	out := []byte("fLaC")
	out = append(out, 0x00, 0x00, 0x00, 34) // STREAMINFO，34 字节
	out = append(out, make([]byte, 34)...)
	out = append(out, 0x84, byte(len(body)>>16), byte(len(body)>>8), byte(len(body))) // 最后一块
	out = append(out, body...)
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeTestMP3(t *testing.T, path, title, artist string) {
	t.Helper()
	frame := func(id, value string) []byte {
		data := append([]byte{3}, []byte(value)...)
		head := []byte(id)
		n := len(data)
		head = append(head, byte(n>>21)&0x7F, byte(n>>14)&0x7F, byte(n>>7)&0x7F, byte(n)&0x7F)
		head = append(head, 0, 0)
		return append(head, data...)
	}
	body := append(frame("TIT2", title), frame("TPE1", artist)...)
	n := len(body)
	head := []byte{'I', 'D', '3', 4, 0, 0,
		byte(n>>21) & 0x7F, byte(n>>14) & 0x7F, byte(n>>7) & 0x7F, byte(n) & 0x7F}
	if err := os.WriteFile(path, append(head, body...), 0o644); err != nil {
		t.Fatal(err)
	}
}

// 每个 Tab 上写的数字必须等于点下去列表里的行数（含搜索词），
// 两边共用 statusMatchesFilter，这里把它钉住。
func TestTabCountsMatchTheFilteredList(t *testing.T) {
	t.Setenv("KIRALRC_NO_PREFS", "1")
	a := newApp()
	a.tracks = []trackItem{
		{Title: "官方", Status: "official"},
		{Title: "对齐", Status: "aligned"},
		{Title: "来源未知", Status: "unknown"},
		{Title: "仅整行", Status: "line_only"},
		{Title: "缺词", Status: "missing"},
		{Title: "全网无词", Status: "nolyrics_notfound"},
		{Title: "纯音乐", Status: "inst_pure"},
		{Title: "OST", Status: "inst_ost"},
		{Title: "伴奏", Status: "inst_accomp"},
	}

	for _, query := range []string{"", "词", "官方"} {
		a.libQuery = query
		counts := a.filterCounts()
		for f := 0; f <= 5; f++ {
			a.libFilter = f
			listed := 0
			for i := range a.tracks {
				if a.trackMatchesFilter(&a.tracks[i]) {
					listed++
				}
			}
			if listed != counts[f] {
				t.Errorf("query=%q filter=%d：Tab 写 %d，列表是 %d", query, f, counts[f], listed)
			}
		}
	}

	// 顺带确认各 Tab 覆盖的是应有状态，官方逐字不能被「对齐」Tab 吞掉
	a.libQuery = ""
	c := a.filterCounts()
	if c[0] != 6 || c[5] != 1 || c[2] != 2 || c[1] != 1 || c[3] != 2 || c[4] != 3 {
		t.Errorf("分类条数不对: %v", c)
	}
}

// 分批读出来的结果必须和一次读完完全一致：不重复、不丢、顺序不变。
func TestBatchedScanMatchesOneShotScan(t *testing.T) {
	a := newApp()
	tmp := t.TempDir()
	for i := 0; i < 260; i++ {
		stem := fmt.Sprintf("%03d - 歌%d", i, i)
		audio := filepath.Join(tmp, stem+".flac")
		if err := os.WriteFile(audio, []byte("RIFF"), 0o644); err != nil {
			t.Fatal(err)
		}
		if i%3 == 0 {
			_ = os.WriteFile(filepath.Join(tmp, stem+".lrc"),
				[]byte("[00:01.000]第一句\n"), 0o644)
		}
	}
	a.libFolders = []string{tmp}
	entries := walkAudioFiles(a.libFolders)
	if len(entries) != 260 {
		t.Fatalf("walk 出 %d 条，期望 260", len(entries))
	}
	for i := 1; i < len(entries); i++ {
		if entries[i-1].rel > entries[i].rel {
			t.Fatalf("walk 结果没按相对路径排: %q > %q", entries[i-1].rel, entries[i].rel)
		}
	}

	var batched []trackItem
	for start := 0; start < len(entries); {
		size := scanBatchSize
		if start == 0 {
			size = scanFirstBatch
		}
		end := min(start+size, len(entries))
		items, _ := readTrackBatch(entries[start:end], nil)
		batched = append(batched, items...)
		start = end
	}
	oneShot, _ := readTrackBatch(entries, nil)

	if len(batched) != len(oneShot) {
		t.Fatalf("分批 %d 条，一次读完 %d 条", len(batched), len(oneShot))
	}
	for i := range batched {
		if batched[i].FullPath != oneShot[i].FullPath || batched[i].Status != oneShot[i].Status {
			t.Fatalf("第 %d 条不一致: %+v vs %+v", i, batched[i], oneShot[i])
		}
	}
	if s := recomputeLibStats(oneShot); s.Total != 260 || s.LineOnly != 87 || s.Missing != 173 {
		t.Fatalf("统计不对: %+v", s)
	}
}

// 统计必须能从切片重算出来，且与逐条累加的结果一致。
func TestRecomputeLibStatsCoversEveryStatus(t *testing.T) {
	items := []trackItem{
		{Status: "official"}, {Status: "aligned"}, {Status: "unknown"},
		{Status: "line_only"}, {Status: "missing"}, {Status: "nolyrics_notfound"},
		{Status: "inst_pure"}, {Status: "inst_ost"}, {Status: "inst_accomp"},
	}
	st := recomputeLibStats(items)
	if st.Total != 9 || st.Vocal != 6 || st.Enhanced != 3 || st.Official != 1 ||
		st.Aligned != 1 || st.Unknown != 1 || st.LineOnly != 1 || st.Missing != 2 ||
		st.NotFound != 1 || st.InstTotal != 3 || st.InstPure != 1 || st.InstOST != 1 || st.InstAccomp != 1 {
		t.Fatalf("unexpected stats: %+v", st)
	}
}

// 连续两次扫描：旧的那次必须作废，不能把两批结果混在一起。
func TestASecondScanCancelsTheFirst(t *testing.T) {
	a := newApp()
	tmp := t.TempDir()
	for i := 0; i < 240; i++ {
		stem := fmt.Sprintf("%03d - 歌%d", i, i)
		_ = os.WriteFile(filepath.Join(tmp, stem+".flac"), []byte("RIFF"), 0o644)
	}
	a.libFolders = []string{tmp}

	a.scanLibraryAsync()
	a.scanLibraryAsync() // 立刻再开一次，第一次的 token 就此作废

	deadline := time.Now().Add(15 * time.Second)
	for a.libScanning && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if a.libScanning {
		t.Fatal("扫描超时未结束")
	}
	if len(a.tracks) != 240 {
		t.Fatalf("最终 %d 条，期望 240（旧扫描不该往里追加）", len(a.tracks))
	}
	seen := map[string]bool{}
	for _, tr := range a.tracks {
		if seen[tr.FullPath] {
			t.Fatalf("重复条目: %s", tr.FullPath)
		}
		seen[tr.FullPath] = true
	}
	if a.libStats.Total != 240 || a.libStats.Missing != 240 {
		t.Fatalf("统计不对: %+v", a.libStats)
	}
}

// 勾选状态按路径存：扫描重建切片不该丢，清空曲库才跟着清。
func TestSelectionSurvivesRescan(t *testing.T) {
	a := newApp()
	tmp := t.TempDir()
	audio := filepath.Join(tmp, "某歌 - 某人.flac")
	if err := os.WriteFile(audio, []byte("RIFF"), 0o644); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(tmp, "另一首 - 某人.flac")
	if err := os.WriteFile(other, []byte("RIFF"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.libFolders = []string{tmp}
	a.tracks, a.libStats = a.scanLibrarySync()

	a.setTrackSelected(audio, true)
	if !a.isTrackSelected(audio) || a.isTrackSelected(other) {
		t.Fatalf("勾选没记上")
	}

	// 重扫一次：切片整片重建，勾选必须还在
	a.tracks, a.libStats = a.scanLibrarySync()
	if !a.isTrackSelected(audio) {
		t.Fatalf("重扫后勾选丢了")
	}
	if got := a.selectedLibraryTracks(); len(got) != 1 || got[0].FullPath != audio {
		t.Fatalf("selectedLibraryTracks 不对: %+v", got)
	}

	// 大小写 / 反斜杠差异不该算成另一首
	if !a.isTrackSelected(strings.ToUpper(audio)) {
		t.Fatalf("同一路径换个大小写就认不出来了")
	}

	a.toggleTrackSelected(audio)
	if a.isTrackSelected(audio) {
		t.Fatalf("再点一下应该取消")
	}

	a.setTrackSelected(other, true)
	a.clearLibraryFolders()
	if a.isTrackSelected(other) || len(a.libSelected) != 0 {
		t.Fatalf("清空曲库后勾选该一起清掉")
	}
}

// 页码越界要自动夹回有效范围（筛选变小、重建切片都会发生）。
func TestPageClampsAfterTrackListShrinks(t *testing.T) {
	a := newApp()
	a.libPageSize = 50
	a.libFolders = []string{`C://lib`} // 有文件夹才渲染列表（不真去扫）
	for i := 0; i < 150; i++ {
		a.tracks = append(a.tracks, trackItem{
			Title:    fmt.Sprintf("歌 %03d", i),
			FullPath: fmt.Sprintf("C://lib//%03d - 歌.flac", i),
			File:     "lib · flac",
			Status:   "missing",
			Lang:     "中文",
		})
	}
	tt := ui.NewTester(a.view, 1360, 860)

	a.libPage = 2 // 150 首 = 3 页，第 3 页合法
	tt.Frame()
	if a.libPage != 2 {
		t.Fatalf("第 3 页合法，不该被改，got %d", a.libPage)
	}

	a.tracks = a.tracks[:60] // 剩 2 页
	tt.Frame()
	if a.libPage != 1 {
		t.Fatalf("列表变短后应夹到最后一页 1，got %d", a.libPage)
	}

	a.tracks = nil
	a.libPage = 7
	tt.Frame()
	if a.libPage != 0 {
		t.Fatalf("空列表应该回到第 1 页，got %d", a.libPage)
	}
}

// 封面 LRU：超过容量淘汰最久未用的，且容量严格不超过上限。
func TestCoverLRUEvictsLeastRecentlyUsed(t *testing.T) {
	bmp := func() *ui.Bitmap {
		return ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 4, 4)))
	}

	s := newCoverStore(3)
	for _, k := range []string{"a", "b", "c"} {
		s.put(k, bmp())
	}
	if s.size() != 3 {
		t.Fatalf("应有 3 条，got %d", s.size())
	}
	// 用一下 a，让它变成「最近用过」，接着写入 d 应该淘汰 b
	if _, ok := s.get("a"); !ok {
		t.Fatal("a 应在缓存里")
	}
	s.put("d", bmp())
	if _, ok := s.get("b"); ok {
		t.Fatal("b 是最久没用的，应该被淘汰")
	}
	if _, ok := s.get("a"); !ok {
		t.Fatal("a 刚用过，不该被淘汰")
	}

	big := newCoverStore(400)
	for i := 0; i < 450; i++ {
		big.put(fmt.Sprintf("k%d", i), bmp())
	}
	if big.size() != 400 {
		t.Fatalf("容量应封顶 400，got %d", big.size())
	}
	if _, ok := big.get("k0"); ok {
		t.Fatal("最早的 k0 应该被淘汰")
	}
	if _, ok := big.get("k449"); !ok {
		t.Fatal("最新的 k449 应该还在")
	}
}

// 无封面的音频要记负结果，别在每次重绘时反复去开文件。
func TestMissingCoverIsRememberedAndNotRetried(t *testing.T) {
	a := newApp()
	audio := filepath.Join(t.TempDir(), "没有封面 - 某人.flac")
	if err := os.WriteFile(audio, []byte("not a real flac"), 0o644); err != nil {
		t.Fatal(err)
	}
	key := coverCacheKey(audio)

	a.requestCovers([]string{audio})
	deadline := time.Now().Add(5 * time.Second)
	for !coverMem.isMiss(key) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !coverMem.isMiss(key) {
		t.Fatal("解析不出封面时应该记下负结果")
	}
	if bmp, known := a.lookupCover(audio); bmp != nil || !known {
		t.Fatalf("lookupCover 应为 (nil, true)，got (%v, %v)", bmp, known)
	}

	a.coverMu.Lock()
	left := len(a.coverInFlight)
	a.coverMu.Unlock()
	if left != 0 {
		t.Fatalf("解码完应清掉 inFlight，还剩 %d", left)
	}

	// 再来一次：已经知道没封面，不该再提交
	a.requestCovers([]string{audio})
	a.coverMu.Lock()
	again := len(a.coverInFlight)
	a.coverMu.Unlock()
	if again != 0 {
		t.Fatalf("已知无封面却重复提交了 %d 个", again)
	}
}

// 扫描收尾要丢掉「文件已经不在了」的勾选，别越攒越多。
func TestPruneDropsSelectionsForGoneFiles(t *testing.T) {
	a := newApp()
	alive := `C:/lib/还在.flac`
	gone := `C:/lib/已删除.flac`
	a.tracks = []trackItem{{FullPath: alive, Title: "还在"}}
	a.setTrackSelected(alive, true)
	a.setTrackSelected(gone, true)

	a.pruneSelectedTracks()
	if a.isTrackSelected(gone) {
		t.Fatal("文件不在了，勾选该清掉")
	}
	if !a.isTrackSelected(alive) {
		t.Fatal("还在的曲目不该被清")
	}
}

// 造一个 ID3v2 标签：帧头按版本用不同的长度编码（v2.3 普通大端 / v2.4 syncsafe）。
func buildID3(version byte, frames [][2][]byte) []byte {
	syncsafe := func(n int) []byte {
		return []byte{byte(n>>21) & 0x7f, byte(n>>14) & 0x7f, byte(n>>7) & 0x7f, byte(n) & 0x7f}
	}
	var body []byte
	for _, fr := range frames {
		id, data := fr[0], fr[1]
		body = append(body, id...)
		if version >= 4 {
			body = append(body, syncsafe(len(data))...)
		} else {
			n := len(data)
			body = append(body, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
		}
		body = append(body, 0, 0) // 帧标志
		body = append(body, data...)
	}
	out := []byte{'I', 'D', '3', version, 0, 0}
	out = append(out, syncsafe(len(body))...)
	return append(out, body...)
}

// v2.3 的帧长是普通 32 位，不是 syncsafe。早前一律按 syncsafe 读，
// 前面有个 >127 字节的帧就会串位，后面的 APIC 再也找不到。
func TestEmbeddedCoverParsesID3v23AndV24(t *testing.T) {
	cover := []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("x", 64))
	apic := func() []byte {
		b := []byte{0} // 编码：Latin-1
		b = append(b, []byte("image/png")...)
		b = append(b, 0) // MIME 结尾
		b = append(b, 3) // 图片类型：正面
		b = append(b, 0) // 空描述
		return append(b, cover...)
	}
	filler := [2][]byte{[]byte("USLT"), append([]byte{0, 0, 0}, bytes.Repeat([]byte("歌词"), 120)...)}

	for _, version := range []byte{3, 4} {
		tag := buildID3(version, [][2][]byte{filler, {[]byte("APIC"), apic()}})
		path := filepath.Join(t.TempDir(), fmt.Sprintf("v2.%d.mp3", version))
		if err := os.WriteFile(path, tag, 0o644); err != nil {
			t.Fatal(err)
		}
		got := readEmbeddedCover(path)
		if !bytes.Equal(got, cover) {
			t.Fatalf("ID3v2.%d 取封面失败：拿到 %d 字节", version, len(got))
		}
	}
}

// ---------- 标签刮削 ----------

func TestScrapeDefaultsAndQuickFilters(t *testing.T) {
	a := newApp()
	// 默认只补最保守的三项：封面、年份、NETEASE_ID
	if a.scrapeFields != [5]bool{true, true, true, false, false} {
		t.Fatalf("默认勾选不对：%v", a.scrapeFields)
	}
	if a.scrapeMode != 0 {
		t.Fatalf("默认策略应是「仅补缺失」，拿到 %d", a.scrapeMode)
	}

	a.scrapeItems = []scrapeTrack{
		{Path: "a.flac", Writable: true}, // 什么都不全
		{Path: "b.flac", Writable: true, CoverW: 1200, CoverH: 1200, NeteaseID: 33, Year: "2020"}, // 都齐了
		{Path: "c.flac", Writable: true, CoverW: 300, CoverH: 300, NeteaseID: 44},                 // 低清 + 缺年份
	}

	a.scrapeFilter = 0
	if got := len(a.scrapeVisible()); got != 3 {
		t.Fatalf("「全部」应有 3 首，拿到 %d", got)
	}

	a.scrapeFilter = 1 // 无封面：只有一张都没有的
	got := a.scrapeVisible()
	if len(got) != 1 || a.scrapeItems[got[0]].Path != "a.flac" {
		t.Fatalf("「无封面」筛错了：%v", got)
	}

	a.scrapeFilter = 2 // 低清：有图但 <600px
	got = a.scrapeVisible()
	if len(got) != 1 || a.scrapeItems[got[0]].Path != "c.flac" {
		t.Fatalf("「低清」筛错了：%v", got)
	}

	a.scrapeFilter = 3 // 缺 ID / 年份
	got = a.scrapeVisible()
	if len(got) != 2 || a.scrapeItems[got[0]].Path != "a.flac" || a.scrapeItems[got[1]].Path != "c.flac" {
		t.Fatalf("「缺 ID / 年份」筛错了：%v", got)
	}

	if counts := a.scrapeFilterCounts(); counts != [4]int{3, 1, 1, 2} {
		t.Fatalf("筛选条数不对：%v", counts)
	}

	if tag := scrapeLocalTag(a.scrapeItems[2]); tag != "300px" {
		t.Fatalf("本地现状小标签不对：%q", tag)
	}
	if tag := scrapeLocalTag(a.scrapeItems[1]); tag != "" {
		t.Fatalf("都齐了的歌不该有小标签：%q", tag)
	}
	if tag := scrapeLocalTag(a.scrapeItems[0]); tag != "无封面" {
		t.Fatalf("无封面应该标出来：%q", tag)
	}
}

func TestScrapeCandidateSelectionCounts(t *testing.T) {
	a := newApp()
	path := filepath.Join(t.TempDir(), "a.flac")
	a.scrapeItems = []scrapeTrack{{Path: path, Writable: true}}

	netease := scrapeCandidate{Source: "网易云", Title: "晴天", SongID: 11, CoverURL: "n"}
	itunes := scrapeCandidate{Source: "iTunes", Title: "晴天", CoverURL: "i"}

	a.toggleScrapeCandidate(path, netease)
	if a.scrapeSelectedCount() != 1 || !a.isScrapeCandidateSelected(path, netease) {
		t.Fatal("点一次应该选中")
	}
	a.toggleScrapeCandidate(path, netease) // 再点取消
	if a.scrapeSelectedCount() != 0 {
		t.Fatal("再点一次应该取消选中")
	}

	a.toggleScrapeCandidate(path, netease)
	a.toggleScrapeCandidate(path, itunes) // 同一首只留一个候选
	if a.scrapeSelectedCount() != 1 {
		t.Fatalf("同一首应该只留一个候选，拿到 %d", a.scrapeSelectedCount())
	}
	if !a.isScrapeCandidateSelected(path, itunes) || a.isScrapeCandidateSelected(path, netease) {
		t.Fatal("选中应该切到后点的那张")
	}

	plan := a.scrapePlan()
	if len(plan) != 1 || plan[0].Candidate.Source != "iTunes" || plan[0].Mode != "fill" {
		t.Fatalf("计划不对：%+v", plan)
	}
	if !plan[0].Fields["cover"] || plan[0].Fields["album"] {
		t.Fatalf("字段勾选没传进计划：%+v", plan[0].Fields)
	}

	a.scrapeMode = 1
	a.scrapeFields[3] = true
	plan = a.scrapePlan()
	if plan[0].Mode != "overwrite" || !plan[0].Fields["album"] {
		t.Fatalf("覆盖模式 / 专辑字段没生效：%+v", plan[0])
	}
}

func TestScrapePageRenders(t *testing.T) {
	a := newApp()
	path := filepath.Join(t.TempDir(), "晴天.flac")
	a.scrapeDir = `D:/music`
	a.scrapeItems = []scrapeTrack{{
		Path: path, Name: "晴天.flac", Writable: true, Format: "flac",
		Title: "晴天", Artist: "周杰伦", Album: "叶惠美", Year: "2003",
		CoverW: 300, CoverH: 300, NeteaseID: 186016,
	}}
	a.scrapeCur = 0
	a.scrapeCandPath = path
	a.scrapeQuery = "晴天 周杰伦"
	a.scrapeCands = []scrapeCandidate{
		{Source: "网易云", Title: "晴天", Artist: "周杰伦", Album: "叶惠美",
			Year: "2003", Duration: 269, SongID: 186016,
			ThumbURL: "demo://a", CoverURL: "demo://a-big"},
		{Source: "iTunes", Title: "晴天", Artist: "Jay Chou", Album: "叶惠美",
			Year: "2003", Duration: 269,
			ThumbURL: "demo://b", CoverURL: "demo://b-big"},
	}
	a.page = pageScrape

	tt := ui.NewTester(a.view, 1360, 860)
	tt.Frame()
	a.toggleScrapeCandidate(path, a.scrapeCands[1])
	tt.Frame()
	tt.Frame()
}

// 后端（python -m kiralrc scrape）的输出形状是跨语言的契约，这里按真实输出核对一遍。
func TestScrapeJSONContract(t *testing.T) {
	var items []scrapeTrack
	inspect := `[{"path":"E://m//a.flac","name":"a.flac","format":"flac","writable":true,` +
		`"title":"晴天","artist":"周杰伦","album":"叶惠美","year":"2003",` +
		`"netease_id":186016,"cover_w":1200,"cover_h":1200}]`
	if err := json.Unmarshal([]byte(inspect), &items); err != nil {
		t.Fatalf("inspect 输出解不动：%v", err)
	}
	if items[0].NeteaseID != 186016 || items[0].CoverW != 1200 || items[0].Year != "2003" {
		t.Fatalf("inspect 字段映射不对：%+v", items[0])
	}

	var cands []scrapeCandidate
	search := `[{"source":"网易云","title":"晴天","artist":"周杰伦","album":"叶惠美",` +
		`"year":"2003","duration":269,"song_id":186016,` +
		`"thumb_url":"https://p1.music.126.net/a.jpg?param=240y240",` +
		`"cover_url":"https://p1.music.126.net/a.jpg?param=1200y1200","cover_hint":"1200×1200"},` +
		`{"source":"iTunes","title":"晴天","artist":"Jay Chou","album":"叶惠美","year":"2003",` +
		`"duration":269,"song_id":0,"thumb_url":"https://x/240x240bb.jpg",` +
		`"cover_url":"https://x/1200x1200bb.jpg","cover_hint":"1200×1200"}]`
	if err := json.Unmarshal([]byte(search), &cands); err != nil {
		t.Fatalf("search 输出解不动：%v", err)
	}
	if len(cands) != 2 || cands[0].SongID != 186016 || cands[1].SongID != 0 {
		t.Fatalf("search 字段映射不对：%+v", cands)
	}

	// apply 的进度行：逐首 written 是布尔，末尾汇总 ok 是首数
	var progress struct {
		I       int      `json:"i"`
		Total   int      `json:"total"`
		Path    string   `json:"path"`
		Written bool     `json:"written"`
		Changed []string `json:"changed"`
		Note    string   `json:"note"`
	}
	line := `{"i":1,"total":1,"path":"E://m//a.flac","written":true,` +
		`"changed":["cover","year"],"note":"","readonly":false,"skipped":false}`
	if err := json.Unmarshal([]byte(line), &progress); err != nil {
		t.Fatalf("apply 进度行解不动：%v", err)
	}
	if !progress.Written || len(progress.Changed) != 2 {
		t.Fatalf("apply 进度字段映射不对：%+v", progress)
	}
	var summary struct {
		Summary bool `json:"summary"`
		OK      int  `json:"ok"`
		Skipped int  `json:"skipped"`
		Failed  int  `json:"failed"`
	}
	if err := json.Unmarshal([]byte(`{"summary":true,"total":3,"ok":2,"skipped":1,"failed":0}`), &summary); err != nil {
		t.Fatalf("apply 汇总行解不动：%v", err)
	}
	if !summary.Summary || summary.OK != 2 || summary.Skipped != 1 {
		t.Fatalf("apply 汇总字段映射不对：%+v", summary)
	}
}

// 刮削页只看文件里内嵌的封面：work/ 下对齐时缓存的 cover.jpg 不算，
// 否则会出现「左边写无封面、右边却摆着一张图」。
func TestScrapeEmbeddedCoverIgnoresCachedArt(t *testing.T) {
	dir := t.TempDir()
	withCover := filepath.Join(dir, "with.flac")
	buildFLACFixture(t, withCover, true)
	bare := filepath.Join(dir, "bare.flac")
	buildFLACFixture(t, bare, false)

	a := newApp()
	if a.scrapeEmbeddedCover(withCover) == nil {
		t.Fatal("有内嵌封面却没给出缩略图")
	}
	if got := a.scrapeEmbeddedCover(bare); got != nil {
		t.Fatal("文件里没有内嵌封面，不该给出图")
	}
	// 同一路径第二次走缓存，结果要一致（错解也要记住，别每帧重开文件）
	if got := a.scrapeEmbeddedCover(bare); got != nil {
		t.Fatal("缓存之后的负结果也不该变成有图")
	}
}

// buildFLACFixture 造一个极简 FLAC：STREAMINFO +（可选）一张正面 PICTURE。
func buildFLACFixture(t *testing.T, path string, withPicture bool) {
	t.Helper()
	var b bytes.Buffer
	b.WriteString("fLaC")
	b.Write([]byte{0x00, 0, 0, 34}) // STREAMINFO，不是最后一块
	b.Write(make([]byte, 34))
	if !withPicture {
		b.Write([]byte{0x81, 0, 0, 0}) // PADDING 收尾
		if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	img := image.NewRGBA(image.Rect(0, 0, 1200, 1200))
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		t.Fatal(err)
	}
	cover := pngBuf.Bytes()

	var body bytes.Buffer
	be := func(v uint32) { body.Write(binary.BigEndian.AppendUint32(nil, v)) }
	mime := []byte("image/png")
	be(3) // 图片类型：正面
	be(uint32(len(mime)))
	body.Write(mime)
	be(0) // 空描述
	be(1200)
	be(1200)
	be(24) // 色深
	be(0)  // 索引色数
	be(uint32(len(cover)))
	body.Write(cover)

	data := body.Bytes()
	b.Write([]byte{0x86, byte(len(data) >> 16), byte(len(data) >> 8), byte(len(data))}) // PICTURE 且收尾
	b.Write(data)
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// 封面缓存只认「路径哈希对得上」的那一份：work/ 里别的曲子留下的同名目录不能借来用。
func TestCoverIndexOnlyUsesTheTracksOwnCache(t *testing.T) {
	stem := "汪苏泷 - 有点甜"
	audio := filepath.Join(t.TempDir(), stem+".flac")
	if err := os.WriteFile(audio, []byte("fLaC"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(audio, old, old); err != nil {
		t.Fatal(err)
	}

	name := []rune(stem)
	dirName := func(sum [16]byte) string {
		return fmt.Sprintf("%s_%x", string(name[:min(len(name), 40)]), sum[:5])
	}
	ownHash := md5.Sum([]byte(mustAbsResolved(t, audio)))

	writeCover := func(root, dir string) {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
		img := image.NewRGBA(image.Rect(0, 0, 400, 400))
		f, err := os.Create(filepath.Join(root, dir, "cover.jpg"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
	}

	// 自己对得上的缓存：要用
	root := t.TempDir()
	writeCover(root, dirName(ownHash))
	if indexCoversIn(root).thumbImage(audio) == nil {
		t.Fatal("哈希对得上的缓存应该被用上")
	}

	// 只有别的路径留下的同名目录：不许借
	other := t.TempDir()
	elsewhere := md5.Sum([]byte(`D:\别处\` + stem))
	writeCover(other, fmt.Sprintf("%s_%x", string(name), elsewhere[:5]))
	if indexCoversIn(other).thumbImage(audio) != nil {
		t.Fatal("不能把别的路径的同名文件封面贴过来")
	}
}

func mustAbsResolved(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

// 「按 ID 批量补全」只挑：有 ID、可写、没写过、也没手工挑过候选的曲目。
func TestScrapeByIDTargets(t *testing.T) {
	a := newApp()
	a.scrapeItems = []scrapeTrack{
		{Path: "a.flac", Writable: true, NeteaseID: 11, CoverW: 1200, CoverH: 1200},
		{Path: "b.flac", Writable: true, NeteaseID: 0, CoverW: 1200, CoverH: 1200},
		{Path: "c.flac", Writable: false, NeteaseID: 13, CoverW: 1200, CoverH: 1200},
		{Path: "d.flac", Writable: true, NeteaseID: 14, WriteState: "done", CoverW: 1200, CoverH: 1200},
		{Path: "e.flac", Writable: true, NeteaseID: 15, CoverW: 1200, CoverH: 1200},
		{Path: "f.flac", Writable: true, NeteaseID: 16, CoverW: 300, CoverH: 300},
	}
	a.scrapeSel = map[string]scrapeCandidate{"e.flac": {Source: "iTunes"}}

	got := a.scrapeByIDTargets()
	if len(got) != 2 || got[0].Path != "a.flac" || got[1].Path != "f.flac" {
		t.Fatalf("按 ID 补全的目标挑错了：%+v", got)
	}

	// 跟随当前筛选：低清只剩 f
	a.scrapeFilter = 2
	got = a.scrapeByIDTargets()
	if len(got) != 1 || got[0].Path != "f.flac" {
		t.Fatalf("筛选后目标不对：%+v", got)
	}

	// 「覆盖选中」是用户明确要重来一遍：写过的也要算进来
	a.scrapeFilter = 0
	a.scrapeMode = 1
	got = a.scrapeByIDTargets()
	if len(got) != 3 || got[0].Path != "a.flac" || got[1].Path != "d.flac" || got[2].Path != "f.flac" {
		t.Fatalf("覆盖选中应当连已写入的也纳入：%+v", got)
	}
}

// 计划里没有候选时不要塞一个空 candidate 进去，Python 那边靠“有没有 candidate”决定按 ID 取。
func TestScrapePlanJSONOmitsEmptyCandidate(t *testing.T) {
	cand := scrapeCandidate{Source: "网易云", Title: "晴天", SongID: 11}
	withCand, err := json.Marshal(scrapePlanItem{Path: "a", Candidate: &cand,
		Fields: map[string]bool{"cover": true}, Mode: "fill"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(withCand), `"candidate"`) {
		t.Fatalf("有候选却没写进计划：%s", withCand)
	}

	byID, err := json.Marshal(scrapePlanItem{Path: "a", NeteaseID: 7,
		CoverSize: 1200, Fields: map[string]bool{"cover": true}, Mode: "fill"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(byID), `"candidate"`) {
		t.Fatalf("按 ID 的计划不该带空候选：%s", byID)
	}
	if !strings.Contains(string(byID), `"netease_id":7`) {
		t.Fatalf("按 ID 的计划少了 netease_id：%s", byID)
	}
	if !strings.Contains(string(byID), `"cover_size":1200`) {
		t.Fatalf("封面档位没写进计划：%s", byID)
	}
}

// 候选角标写的是实测像素，不是请求值——所以要先量得出来。
func TestProbeImageSize(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 320, 200))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(buf.Bytes())
	}))
	defer srv.Close()

	if w, h := probeImageSize(srv.URL); w != 320 || h != 200 {
		t.Fatalf("量出来的尺寸不对：%dx%d", w, h)
	}

	missing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer missing.Close()
	if w, h := probeImageSize(missing.URL); w != 0 || h != 0 {
		t.Fatalf("取不到时应该是 0×0，拿到 %dx%d", w, h)
	}
}

func TestScrapeCoverLabelUsesMeasuredSize(t *testing.T) {
	a := newApp()
	a.scrapeSizes = map[string][2]int{"u": {390, 390}, "big": {1600, 1600}}
	if got := a.scrapeCoverLabel("u"); got != "390×390" {
		t.Fatalf("角标应该写实测尺寸，拿到 %q", got)
	}
	if got := a.scrapeCoverLabel(""); got != "无图" {
		t.Fatalf("没有地址时角标不对：%q", got)
	}
	// 选了 1200 档，写进去的就被夹到 1200，角标不能还写 1600
	a.scrapeQuality = 1
	if got := a.scrapeCoverLabel("big"); got != "1200×1200" {
		t.Fatalf("1200 档的角标应该封顶，拿到 %q", got)
	}
	a.scrapeQuality = 0
	if got := a.scrapeCoverLabel("big"); got != "1600×1600" {
		t.Fatalf("原图档应该照实显示，拿到 %q", got)
	}
}

// 按钮常驻，但「仅补缺失」下写过的就不再纳入——0 首要能从 scrapeIDTotal 看出来。
func TestScrapeIDTotalCountsWrittenOnes(t *testing.T) {
	a := newApp()
	a.scrapeItems = []scrapeTrack{
		{Path: "a.flac", Writable: true, NeteaseID: 11, WriteState: "done"},
		{Path: "b.flac", Writable: true, NeteaseID: 12},
		{Path: "c.flac", Writable: true},
		{Path: "d.flac", Writable: false, NeteaseID: 13},
	}
	if got := a.scrapeIDTotal(); got != 2 {
		t.Fatalf("带 ID 且可写的应该有 2 首，拿到 %d", got)
	}
	if got := len(a.scrapeByIDTargets()); got != 1 {
		t.Fatalf("仅补缺失下应该只剩 1 首没写过的，拿到 %d", got)
	}
	a.scrapeMode = 1
	if got := len(a.scrapeByIDTargets()); got != 2 {
		t.Fatalf("覆盖选中下两首都该算，拿到 %d", got)
	}
}

// 写完一首后它从当前筛选里消失了（封面升到 ≥600px），选中要顺延到下一首。
func TestScrapeFollowsNextAfterWrite(t *testing.T) {
	newCase := func() *app {
		a := newApp()
		a.scrapeItems = []scrapeTrack{
			{Path: "a.flac", Writable: true, CoverW: 300, CoverH: 300},
			{Path: "b.flac", Writable: true, CoverW: 300, CoverH: 300},
			{Path: "c.flac", Writable: true, CoverW: 300, CoverH: 300},
		}
		a.scrapeFilter = 2 // 低清
		a.scrapeCur = 1
		a.scrapeCandPath = "b.flac"
		// 命中候选缓存，免得顺延时真去起 python 搜索
		a.scrapeCandCache = map[string][]scrapeCandidate{
			"c.flac": {{Source: "网易云", Title: "c"}},
		}
		a.scrapeFollowPath, a.scrapeFollowPos = "b.flac", 1
		return a
	}

	// 1) b 升级成 1200，不再属于低清 → 跳到同一位置上的 c
	a := newCase()
	a.scrapeItems[1].CoverW, a.scrapeItems[1].CoverH = 1200, 1200
	a.scrapeFollowWritten()
	if a.scrapeCur != 2 || a.scrapeItems[a.scrapeCur].Path != "c.flac" {
		t.Fatalf("应该顺延到 c.flac，实际 %q", a.scrapeItems[a.scrapeCur].Path)
	}

	// 2) b 还在低清里（源图就这么小）→ 选中不动
	a = newCase()
	a.scrapeFollowWritten()
	if a.scrapeCur != 1 {
		t.Fatalf("还在列表里就不该跳，实际到了 %q", a.scrapeItems[a.scrapeCur].Path)
	}

	// 3) 原来是最后一首且它消失了 → 落到新的最后一首
	a = newCase()
	a.scrapeCur = 2
	a.scrapeCandPath = "c.flac"
	a.scrapeFollowPath, a.scrapeFollowPos = "c.flac", 2
	a.scrapeItems[2].CoverW, a.scrapeItems[2].CoverH = 1200, 1200
	a.scrapeFollowWritten()
	if got := a.scrapeItems[a.scrapeCur].Path; got != "b.flac" {
		t.Fatalf("末尾消失后应落到新的最后一首 b.flac，实际 %q", got)
	}
}

// 「按网易云补全」只挑没有 ID、可写、没写过、也没手工挑过的曲目。
func TestScrapeNeteaseTargets(t *testing.T) {
	a := newApp()
	a.scrapeItems = []scrapeTrack{
		{Path: "a.flac", Writable: true},
		{Path: "b.flac", Writable: true, NeteaseID: 5},
		{Path: "c.flac", Writable: false},
		{Path: "d.flac", Writable: true, WriteState: "done"},
		{Path: "e.flac", Writable: true},
	}
	a.scrapeSel = map[string]scrapeCandidate{"e.flac": {Source: "iTunes"}}

	got := a.scrapeNeteaseTargets()
	if len(got) != 1 || got[0].Path != "a.flac" {
		t.Fatalf("按网易云补全的目标挑错了：%+v", got)
	}

	a.scrapeMode = 1 // 覆盖选中：写过的也重来
	got = a.scrapeNeteaseTargets()
	if len(got) != 2 || got[0].Path != "a.flac" || got[1].Path != "d.flac" {
		t.Fatalf("覆盖选中下应含已写入的：%+v", got)
	}
}

// 计划里要明确写 resolve=netease，后端才知道这是「搜出来比对」而不是按 ID。
func TestScrapePlanCarriesResolveFlag(t *testing.T) {
	data, err := json.Marshal(scrapePlanItem{Path: "a", Resolve: "netease",
		Fields: map[string]bool{"year": true}, Mode: "fill"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"resolve":"netease"`) {
		t.Fatalf("计划里少了 resolve：%s", data)
	}
	if strings.Contains(string(data), `"candidate"`) || strings.Contains(string(data), `"netease_id"`) {
		t.Fatalf("按网易云补全不该带候选或 ID：%s", data)
	}
}

// 左栏右键「复制全部信息」的排版：第一行能直接粘进搜索框，其余带标签。
func TestScrapeClipAllFormat(t *testing.T) {
	track := scrapeTrack{
		Path: `E:/cloudy music\a.flac`, Name: "a.flac", Title: "月がきれい",
		Artist: "東山奈央", Album: "月がきれい (アニメ盤)", Year: "2009", NeteaseID: 278148,
	}
	want := "月がきれい - 東山奈央\n专辑：月がきれい (アニメ盤)\n年份：2009\nID：278148\n" +
		`路径：E:/cloudy music\a.flac`
	if got := scrapeClipAll(track); got != want {
		t.Fatalf("复制内容不对：\n%q\nwant\n%q", got, want)
	}

	// 没有标签的曲目：歌名退回文件名，空的专辑/年份/ID 不占行
	bare := scrapeTrack{Path: `E:/m/b.flac`, Name: "b.flac"}
	if got := scrapeTitleOf(bare); got != "b" {
		t.Fatalf("没有标签时应退回文件名，拿到 %q", got)
	}
	if got := scrapeClipAll(bare); got != "b\n"+`路径：E:/m/b.flac` {
		t.Fatalf("空字段不该占行：%q", got)
	}
}

// 左栏右键菜单：真开一次菜单、点「复制歌手」，看剪贴板里是什么。
func TestScrapeRowRightClickCopies(t *testing.T) {
	a := newApp()
	path := filepath.Join(t.TempDir(), "月がきれい.flac")
	a.scrapeItems = []scrapeTrack{{
		Path: path, Name: "月がきれい.flac", Title: "月がきれい", Artist: "東山奈央",
		Album: "月がきれい (アニメ盤)", Year: "2009", NeteaseID: 278148,
		Writable: true, CoverW: 800, CoverH: 800,
	}}
	a.scrapeCur = 0
	a.page = pageScrape

	tt := ui.NewTester(a.view, 1360, 860)
	tt.Frame()

	row, ok := tt.Find("月がきれい")
	if !ok {
		t.Fatal("找不到曲目")
	}
	x, y := row.X+row.W/2, row.Y+row.H/2
	tt.RightClickAt(x, y)
	tt.Frame()
	if a.menu == nil || a.menu.track == nil || a.menu.track.Path != path {
		t.Fatal("右键菜单没有打开")
	}
	if item, ok := tt.Find("复制歌手"); !ok || item.X <= x {
		t.Fatalf("菜单应出现在鼠标右侧，鼠标 x=%v，菜单项=%+v", x, item)
	}

	if err := tt.Click("复制歌手"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := tt.Clipboard(); got != "東山奈央" {
		t.Fatalf("复制歌手拿到 %q", got)
	}

	if err := tt.RightClick("月がきれい"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("复制全部信息"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got, want := tt.Clipboard(), scrapeClipAll(a.scrapeItems[0]); got != want {
		t.Fatalf("复制全部信息拿到 %q，想要 %q", got, want)
	}
}

// 搜索结果的两种形状：{items,warning} 和旧的裸数组。
func TestParseScrapeSearch(t *testing.T) {
	obj := `{"items":[{"source":"iTunes","title":"桃色タイフーン","song_id":0}],` +
		`"warning":"网易云限速了，这次只有 iTunes 候选","auth":"Cloudy 登录态"}`
	res, err := parseScrapeSearch([]byte(obj))
	if err != nil || len(res.Items) != 1 || res.Items[0].Title != "桃色タイフーン" {
		t.Fatalf("对象格式解析不对：%+v %v", res, err)
	}
	if res.Warning != "网易云限速了，这次只有 iTunes 候选" {
		t.Fatalf("告警没带出来：%q", res.Warning)
	}
	if res.Auth != "Cloudy 登录态" {
		t.Fatalf("身份没带出来：%q", res.Auth)
	}

	// 老格式（数组）照旧能用
	res, err = parseScrapeSearch([]byte(`[{"source":"网易云","title":"晴天"}]`))
	if err != nil || len(res.Items) != 1 || res.Warning != "" {
		t.Fatalf("数组格式解析不对：%+v %v", res, err)
	}

	// 空结果 + 告警
	res, err = parseScrapeSearch([]byte(`{"items":[],"warning":"限速"}`))
	if err != nil || len(res.Items) != 0 || res.Warning != "限速" {
		t.Fatalf("空结果解析不对：%+v %v", res, err)
	}
}

// 被网易云限速后进入冷却：切歌不再自动去搜，批量补全也先拦住。
func TestScrapeCoolsDownAfterRateLimit(t *testing.T) {
	a := newApp()
	a.scrapeItems = []scrapeTrack{{Path: "a.flac", Writable: true, NeteaseID: 0}}
	a.scrapeCoolUntil = time.Now().Add(time.Minute)

	a.selectScrapeTrack(0)
	if a.scrapeCands != nil {
		t.Fatal("冷却期不该发起搜索")
	}
	if a.scrapeWarn == "" {
		t.Fatal("冷却期要告诉用户为什么没搜")
	}

	a.toast = ""
	a.applyScrapeByNetease()
	if a.scrapeApplying {
		t.Fatal("冷却期不该开始批量补全")
	}
	if !strings.Contains(a.toast, "冷却") {
		t.Fatalf("批量补全应提示冷却，实际 %q", a.toast)
	}
}

// ---------- 清理歌词：只走回收站 ----------

// 回收站报错 / 用户取消时，绝不能顺手改用 os.Remove 把文件删掉。
func TestCleanupNeverFallsBackToRemove(t *testing.T) {
	dir := t.TempDir()
	var files []string
	for _, name := range []string{"a.lrc", "b.lrc"} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		files = append(files, p)
	}

	old := recycleFilesFn
	t.Cleanup(func() { recycleFilesFn = old })
	recycleFilesFn = func(paths []string) ([]string, error) {
		return nil, errors.New("操作被取消")
	}

	a := newApp()
	done, left, reason := a.runCleanup(false, files)
	if len(done) != 0 || len(left) != len(files) {
		t.Fatalf("回收站失败时应当一个都不算处理：done=%v left=%v", done, left)
	}
	if reason == "" {
		t.Fatal("失败原因要带出来")
	}
	for _, f := range files {
		if _, err := os.Stat(f); err != nil {
			t.Fatalf("文件被删掉了：%v", err)
		}
	}

	msg := summarizeCleanup(false, done, left, reason)
	if !strings.Contains(msg, "没动") || !strings.Contains(msg, "操作被取消") {
		t.Fatalf("结果里要写清楚剩了几个、为什么：%q", msg)
	}
}

// 用户中途取消：已经进回收站的那几个照实报，剩下的原样留着。
func TestCleanupReportsPartialRecycle(t *testing.T) {
	dir := t.TempDir()
	var files []string
	for _, name := range []string{"a.lrc", "b.lrc", "c.lrc"} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		files = append(files, p)
	}

	old := recycleFilesFn
	t.Cleanup(func() { recycleFilesFn = old })
	recycleFilesFn = func(paths []string) ([]string, error) {
		return paths[:1], nil // 只成功了一个
	}

	a := newApp()
	done, left, _ := a.runCleanup(false, files)
	msg := summarizeCleanup(false, done, left, "")
	if len(done) != 1 || len(left) != 2 {
		t.Fatalf("应当 1 成 2 留，拿到 done=%v left=%v", done, left)
	}
	if !strings.Contains(msg, "已移入回收站 1 个") || !strings.Contains(msg, "2 个没动") {
		t.Fatalf("汇总不对：%q", msg)
	}
}

// 永久删除是独立入口：只有 permanent=true 才真的 os.Remove。
func TestCleanupPermanentDeletesOnlyOnRequest(t *testing.T) {
	dir := t.TempDir()
	var files []string
	for _, name := range []string{"a.lrc", "b.lrc"} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		files = append(files, p)
	}

	old := recycleFilesFn
	t.Cleanup(func() { recycleFilesFn = old })
	recycleFilesFn = func(paths []string) ([]string, error) {
		t.Fatal("永久删除不该去碰回收站")
		return nil, nil
	}

	a := newApp()
	done, left, _ := a.runCleanup(true, files)
	if len(done) != 2 || len(left) != 0 {
		t.Fatalf("永久删除应当全成：done=%v left=%v", done, left)
	}
	for _, f := range files {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Fatalf("文件还在：%v", f)
		}
	}
}

// ---------- 刮削：限速冷却与暂停续跑 ----------

func TestScrapeCooldownBlocksEveryEntry(t *testing.T) {
	a := newApp()
	a.scrapeCoolUntil = time.Now().Add(time.Minute)

	a.scrapeItems = []scrapeTrack{{Path: "a.flac", Writable: true, NeteaseID: 5}}
	a.applyScrapeByID()
	if a.scrapeApplying {
		t.Fatal("冷却期内不该发起「按 ID」补全")
	}
	if !strings.Contains(a.toast, "冷却") {
		t.Fatalf("应当提示还在冷却，实际 %q", a.toast)
	}

	a.toast = ""
	a.scrapeItems = []scrapeTrack{{Path: "b.flac", Writable: true}}
	a.applyScrapeByNetease()
	if a.scrapeApplying {
		t.Fatal("冷却期内不该发起「按网易云」补全")
	}
	if !strings.Contains(a.toast, "冷却") {
		t.Fatalf("应当提示还在冷却，实际 %q", a.toast)
	}
}

func TestScrapePauseKeepsRemainingPlan(t *testing.T) {
	a := newApp()
	plan := []scrapePlanItem{
		{Path: "a.flac", Resolve: "netease"},
		{Path: "b.flac", Resolve: "netease"},
		{Path: "c.flac", Resolve: "netease"},
	}
	a.scheduleScrapeResume(plan, 2, 1, 1, 0) // 第 2 首起没跑，1 秒后理论上续

	if len(a.scrapePending) != 2 || a.scrapePending[0].Path != "b.flac" {
		t.Fatalf("没跑完的计划要留下：%+v", a.scrapePending)
	}
	if a.scrapeCoolUntil.IsZero() {
		t.Fatal("暂停后要记住冷却截止时间，否则下一次又去撞限速")
	}
	if !strings.Contains(a.scrapeWarn, "限速") || !strings.Contains(a.scrapeWarn, "取消续跑") {
		t.Fatalf("提示要说明暂停和可取消：%q", a.scrapeWarn)
	}

	a.cancelScrapeResume()
	if len(a.scrapePending) != 0 || a.scrapeWarn != "" {
		t.Fatalf("取消后不该还留着续跑：%+v %q", a.scrapePending, a.scrapeWarn)
	}
}

// 进度行与汇总行的 JSON 形状（ok / skipped 类型不同的那个坑）。
func TestScrapeProgressJSONShapes(t *testing.T) {
	var item scrapeProgress
	line := `{"i":3,"total":5,"path":"E://m//a.flac","written":false,"noop":true,` +
		`"needs_pick":true,"note":"多条都对得上","readonly":false}`
	if err := json.Unmarshal([]byte(line), &item); err != nil {
		t.Fatalf("逐首行解不动：%v", err)
	}
	if item.Written || !item.NeedsPick || item.I != 3 || item.Total != 5 {
		t.Fatalf("逐首字段映射不对：%+v", item)
	}

	var sum scrapeProgress
	summary := `{"summary":true,"total":5,"ok":2,"skipped":2,"failed":1,` +
		`"needs_pick_count":1,"paused":true,"retry_after":95,"resume_from":3}`
	if err := json.Unmarshal([]byte(summary), &sum); err != nil {
		t.Fatalf("汇总行解不动：%v", err)
	}
	if !sum.Paused || sum.RetryAfter != 95 || sum.ResumeFrom != 3 || sum.NeedsPickCount != 1 {
		t.Fatalf("汇总字段映射不对：%+v", sum)
	}
}

// 借用 Cloudy 登录态默认关；打开后才把开关透给 Python。
func TestCloudyAuthOptInReachesPython(t *testing.T) {
	a := newApp()
	if a.setCloudyAuth {
		t.Fatal("默认不该借别人的登录态")
	}
	has := func(args ...string) bool {
		for _, e := range a.pythonCmd(args...).Env {
			if e == "KIRALRC_CLOUDY_AUTH=1" {
				return true
			}
		}
		return false
	}
	if has("scrape", "search", "x") {
		t.Fatal("没打开开关却把 KIRALRC_CLOUDY_AUTH 传下去了")
	}
	a.setCloudyAuth = true
	if !has("scrape", "search", "x") {
		t.Fatal("打开开关后要透传 KIRALRC_CLOUDY_AUTH=1")
	}
}

// 用户在系统对话框里按了取消：后面的批次别再弹窗。
func TestCleanupStopsBatchesAfterCancel(t *testing.T) {
	dir := t.TempDir()
	files := make([]string, 0, 450) // 200 一批，正常会分 3 批
	for i := 0; i < 450; i++ {
		files = append(files, filepath.Join(dir, fmt.Sprintf("f%d.lrc", i)))
	}

	old := shellDelete
	t.Cleanup(func() { shellDelete = old })
	calls := 0
	shellDelete = func(op *shFileOpStruct) uintptr {
		calls++
		op.fAnyOperationsAborted = 1
		return 0
	}

	moved, err := recycleFiles(files)
	if err == nil || !strings.Contains(err.Error(), "取消") {
		t.Fatalf("取消要如实报出来，拿到 %v", err)
	}
	if calls != 1 {
		t.Fatalf("取消后还弹了 %d 次对话框", calls)
	}
	if len(moved) != 0 {
		t.Fatalf("一个都没进回收站，却报了 %d 个", len(moved))
	}
}

// 「需要手选」的首数：有汇总就用汇总的，不能和逐行的加起来。
func TestScrapePickCountPrefersSummary(t *testing.T) {
	if got := scrapePickCount(2, 2, true); got != 2 {
		t.Fatalf("汇总与逐行数的是同一件事，不该翻倍，拿到 %d", got)
	}
	if got := scrapePickCount(0, 3, false); got != 3 {
		t.Fatalf("没有汇总时要用逐行计数，拿到 %d", got)
	}
}

// 一直被限速时别在后台无限续跑：连续三次就停手，交回给用户。
func TestScrapeResumeStopsAfterRepeatedRateLimits(t *testing.T) {
	a := newApp()
	plan := []scrapePlanItem{{Path: "a.flac", Resolve: "netease"}}

	a.scrapeResumeTries = 2
	a.scheduleScrapeResume(plan, 1, 1, 1, 0)
	if len(a.scrapePending) != 1 {
		t.Fatal("第三次还该再试一次")
	}
	a.scrapePending = nil

	a.scheduleScrapeResume(plan, 1, 1, 1, 0) // 第三次用完了
	if len(a.scrapePending) != 0 {
		t.Fatalf("连续被限速还排续跑：%+v", a.scrapePending)
	}
	if !strings.Contains(a.toast, "停止自动续跑") {
		t.Fatalf("要告诉用户已经停手，实际 %q", a.toast)
	}
}

// 「按专辑补全」只挑：有专辑 ID、可写、没写过（覆盖选中除外）、没手工挑过。
func TestScrapeAlbumTargets(t *testing.T) {
	a := newApp()
	a.scrapeItems = []scrapeTrack{
		{Path: "a.flac", Writable: true, AlbumID: 100},
		{Path: "b.flac", Writable: true, AlbumID: 100},
		{Path: "c.flac", Writable: true},                // 没有专辑 ID
		{Path: "d.flac", Writable: false, AlbumID: 101}, // 只读
		{Path: "e.flac", Writable: true, AlbumID: 102, WriteState: "done"},
		{Path: "f.flac", Writable: true, AlbumID: 103},
	}
	a.scrapeSel = map[string]scrapeCandidate{"f.flac": {Source: "iTunes"}}

	got := a.scrapeAlbumTargets()
	if len(got) != 2 || got[0].Path != "a.flac" || got[1].Path != "b.flac" {
		t.Fatalf("按专辑的目标挑错了：%+v", got)
	}
	if n := scrapeAlbumCount(got); n != 1 {
		t.Fatalf("两首同属一张专辑，应算 1 张，拿到 %d", n)
	}

	// 覆盖选中：写过的也要重来
	a.scrapeMode = 1
	got = a.scrapeAlbumTargets()
	if len(got) != 3 || scrapeAlbumCount(got) != 2 {
		t.Fatalf("覆盖选中下应含已写入的：%+v（%d 张）", got, scrapeAlbumCount(got))
	}
}

// 计划里要明确写 album_id + resolve=album，后端才知道去取专辑曲目表。
func TestScrapePlanCarriesAlbum(t *testing.T) {
	a := newApp()
	a.scrapeItems = []scrapeTrack{{Path: "a.flac", Writable: true, AlbumID: 16953}}
	a.scrapeMode = 0
	plan := []scrapePlanItem{{Path: "a.flac", AlbumID: 16953, Resolve: "album",
		Fields: map[string]bool{"netease_id": true}, Mode: "fill"}}
	data, err := json.Marshal(map[string]any{"items": plan})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(data, &payload); err != nil || len(payload.Items) != 1 {
		t.Fatalf("计划 JSON 解不动：%s", data)
	}
	item := payload.Items[0]
	if item["album_id"] != float64(16953) || item["resolve"] != "album" {
		t.Fatalf("计划里少了专辑信息：%s", data)
	}
	for _, key := range []string{"candidate", "netease_id"} {
		if _, ok := item[key]; ok {
			t.Fatalf("按专辑的计划不该带 %s：%s", key, data)
		}
	}
}

// 检查输出里的 album_id 要能落到 scrapeTrack 上（inspect → 界面 → 计划）。
func TestScrapeInspectAlbumID(t *testing.T) {
	var items []scrapeTrack
	line := `[{"path":"E://m//a.flac","name":"a.flac","writable":true,` +
		`"title":"晴天","album_id":16953,"netease_id":167870,"cover_w":1200,"cover_h":1200}]`
	if err := json.Unmarshal([]byte(line), &items); err != nil {
		t.Fatalf("inspect 输出解不动：%v", err)
	}
	if items[0].AlbumID != 16953 {
		t.Fatalf("album_id 没落到结构体上：%+v", items[0])
	}
}

// 切目录时上一批的缩略图/尺寸探测 goroutine 可能正好回来写：
// 表被换掉也不能崩（assignment to entry in nil map）。
func TestScrapeCacheWritesTolerateReset(t *testing.T) {
	a := newApp()

	// 老写法留下的现场：表是 nil，goroutine 回来写
	a.scrapeThumbs = nil
	a.scrapeThumbBusy = map[string]bool{"u": true}
	a.putScrapeThumb("u", nil) // 以前直接 a.scrapeThumbs[url] = nil → panic
	if a.scrapeThumbs == nil {
		t.Fatal("写回后缩略图表不该还是 nil")
	}
	if a.scrapeThumbBusy["u"] {
		t.Fatal("busy 标记要清掉，否则同一张图永远不再重试")
	}

	a.scrapeSizes = nil
	a.putScrapeSize("u", 1200, 1200)
	if got := a.scrapeSizes["u"]; got != [2]int{1200, 1200} {
		t.Fatalf("尺寸没写进去：%v", got)
	}
}

// 重置要换成空表而不是 nil：后台 goroutine 还会往里写。
func TestResetScrapeCachesKeepsMapsUsable(t *testing.T) {
	a := newApp()
	a.scrapeThumbs = map[string]*ui.Bitmap{"x": nil}
	a.scrapeSizes = map[string][2]int{"x": {1, 1}}
	a.scrapeCovers = map[string]*ui.Bitmap{"x": nil}

	a.resetScrapeCaches()
	a.scrapeMu.Lock()
	defer a.scrapeMu.Unlock()
	if a.scrapeThumbs == nil || a.scrapeThumbBusy == nil ||
		a.scrapeCovers == nil || a.scrapeSizes == nil || a.scrapeSizeBusy == nil {
		t.Fatal("重置后不该出现 nil map")
	}
	if len(a.scrapeThumbs) != 0 || len(a.scrapeSizes) != 0 || len(a.scrapeCovers) != 0 {
		t.Fatal("重置后应当是空的")
	}
}
