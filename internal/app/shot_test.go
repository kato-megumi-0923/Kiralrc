package app

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// TestShots 离屏截图：不弹窗口、不模拟鼠标，直接把界面渲染成 PNG。
//
//	KIRALRC_SHOT_DIR=D:\shots \
//	KIRALRC_SHOT_LIB="E:\cloudy music" \
//	go test ./internal/app -run TestShots -v
//
// 可选：
//
//	KIRALRC_SHOT_DARK=1        深色主题
//	KIRALRC_SHOT_SIZE=1360x860 画布尺寸（DIP）
//	KIRALRC_SHOT_PAGES=library,tools,settings  只截这几页
//	KIRALRC_SHOT_SCALE=2       每 DIP 几个像素（默认 1，2 适合做配图）
//
// 每个页面一张 <名字>.png；曲库页会先把封面解出来再拍。
func TestShots(t *testing.T) {
	dir := os.Getenv("KIRALRC_SHOT_DIR")
	if dir == "" {
		t.Skip("未设置 KIRALRC_SHOT_DIR，跳过离屏截图")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KIRALRC_NO_PREFS", "1")

	width, height := 1360, 860
	if size := os.Getenv("KIRALRC_SHOT_SIZE"); size != "" {
		if w, h, ok := strings.Cut(size, "x"); ok {
			width, _ = strconv.Atoi(w)
			height, _ = strconv.Atoi(h)
		}
	}

	a := newApp()
	if os.Getenv("KIRALRC_SHOT_DARK") != "" {
		a.appearance = 1
	}
	if lib := os.Getenv("KIRALRC_SHOT_LIB"); lib != "" {
		a.libFolders = []string{lib}
		a.tracks, a.libStats = a.scanLibrarySync()
		a.libScanTime = "今天 00:00"
	}

	tt := ui.NewTester(a.view, width, height)
	scale := float32(1)
	if raw := os.Getenv("KIRALRC_SHOT_SCALE"); raw != "" {
		if f, err := strconv.ParseFloat(raw, 32); err == nil && f > 0 {
			scale = float32(f)
		}
	}
	tt.SetScale(scale)
	tt.Frame()
	// Tester 的时钟是静止的：动画（页面切换的淡入）要手动推进，否则停在第一帧。
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	// 封面是按需异步解的：真等一会儿，再推时间走几帧
	for i := 0; i < 12; i++ {
		time.Sleep(60 * time.Millisecond)
		tt.Advance(60 * time.Millisecond)
		tt.Frame()
	}

	only := map[string]bool{}
	for _, n := range strings.Split(os.Getenv("KIRALRC_SHOT_PAGES"), ",") {
		if n = strings.TrimSpace(n); n != "" {
			only[n] = true
		}
	}

	pages := []struct{ nav, name string }{
		{"曲库", "library"},
		{"新建对齐", "align"},
		{"任务队列", "queue"},
		{"编辑与校对", "editor"},
		{"工具箱", "tools"},
		{"设置", "settings"},
	}
	made := 0
	for _, p := range pages {
		if len(only) > 0 && !only[p.name] {
			continue
		}
		if err := tt.Click(p.nav); err != nil {
			t.Logf("跳过 %s：%v", p.nav, err)
			continue
		}
		tt.Advance(400 * time.Millisecond) // 等页面淡入走完
		tt.Frame()
		img := tt.Image()
		out := filepath.Join(dir, p.name+".png")
		file, err := os.Create(out)
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(file, img); err != nil {
			file.Close()
			t.Fatal(err)
		}
		file.Close()
		made++
		t.Logf("%-10s → %s  %dx%d", p.nav, out, img.Rect.Dx(), img.Rect.Dy())
	}
	if made == 0 {
		t.Fatal("一张都没截出来")
	}
	fmt.Printf("共 %d 张，输出目录 %s\n", made, dir)
}
