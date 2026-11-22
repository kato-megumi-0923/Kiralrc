package app

import (
	"image/png"
	"os"
	"path/filepath"
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
	wasSel := a.tracks[tIdx].Selected
	if err := tt.Click("突然的自我"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.tracks[tIdx].Selected == wasSel {
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

	// Verify nolyrics_notfound sorts ahead of missing, and creating .lrc auto-clears notFoundPaths
	a3 := filepath.Join(tmp, "新歌 - 本地缺词.flac")
	_ = os.WriteFile(a3, []byte("RIFF"), 0o644)
	a.tracks, a.libStats = a.scanLibrarySync()
	if len(a.tracks) < 2 || a.tracks[0].Status != "nolyrics_notfound" || a.tracks[1].Status != "missing" {
		t.Fatalf("expected nolyrics_notfound before missing, got %+v", a.tracks)
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
