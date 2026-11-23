package app

import (
	"encoding/binary"
	"encoding/json"
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
		{"设置", pageSettings},
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
	if err := tt.Click("主题色：紫罗兰"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.accentID != "violet" || a.accentHex != "#7C5CD6" {
		t.Fatalf("got accent %s (%s), want violet", a.accentID, a.accentHex)
	}

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
		"30967650":                                    "30967650",
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
	if err := tt.Click("批量填网易云 ID"); err != nil {
		t.Fatalf("click 批量填网易云 ID: %v", err)
	}
	tt.Frame()
	if !a.libBatchIdOpen {
		t.Fatalf("expected libBatchIdOpen=true")
	}

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

	// 未配置时拦截并提示
	if err := a.translateCurrentLyricsSync(srv.Client()); err == nil {
		t.Fatalf("expected error when AI config is empty")
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
		{"native-settings", pageSettings, "clay", false},
	}
	for _, sc := range scenes {
		a := newApp()
		a.page = sc.page
		a.setAccent(sc.accent)
		if sc.dark {
			a.appearance = 1
		}
		tt := ui.NewTester(a.view, 1360, 860)
		tt.SetDark(sc.dark)
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
	out = append(out, 0x00, 0x00, 0x00, 34)          // STREAMINFO，34 字节
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
