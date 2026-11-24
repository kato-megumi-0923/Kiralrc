package app

import (
	"testing"
	"time"

	"github.com/egoist/mygo/kiralrcmenu"
	"github.com/egoist/mygo/kiralrcmenu/menutest"
	"github.com/egoist/mygo/ui"
)

// A field menu above settings must take the keyboard and return it to the field.
func TestContextMenuAboveSettings(t *testing.T) {
	a := newApp()
	a.settingsOpen = true
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Advance(motionPane.Duration)
	if err := tt.Click("搜索设置"); err != nil {
		t.Fatal(err)
	}
	var calls int
	open := func() {
		a.openWindowMenu(1096, 716, []kiralrcmenu.Item{
			{Label: "复制", Disabled: true},
			{Separator: true},
			{Label: "粘贴", Choose: func() { calls++ }},
		})
		tt.Frame()
		tt.Advance(motionPane.Duration)
		r, ok := tt.Find("粘贴")
		if !ok || r.X+r.W > 1100 || r.Y+r.H > 720 {
			t.Fatalf("菜单项超出窗口: %+v", r)
		}
	}
	open()
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.menu != nil || !a.settingsOpen || calls != 0 || !tt.Focused("搜索设置") {
		t.Fatal("Esc 应只关闭最上层菜单")
	}
	open()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.menu != nil || calls != 1 {
		t.Fatal("Enter 应执行可用的菜单项")
	}
}

// Measure the painted card, including on a second opening at the same point.
func TestLibraryMenuUnfoldsToContentWidth(t *testing.T) {
	a := newApp()
	a.pal = paletteFor(true)
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Box(c).Fill().Background(white)
		a.viewContextMenu(c)
	}, 400, 240)
	extent := func() (int, int) {
		im := tt.Image()
		right, bottom := 40, 40
		for x := 40; x < 380; x++ {
			r, _, _, _ := im.At(x, 65).RGBA()
			if r < 0xc000 {
				right = x
			}
		}
		for y := 40; y < 230; y++ {
			r, _, _, _ := im.At(68, y).RGBA()
			if r < 0xc000 {
				bottom = y
			}
		}
		return right - 40 + 1, bottom - 40 + 1
	}
	for range 2 {
		a.openWindowMenu(40, 40, []kiralrcmenu.Item{{Label: "在资源管理器中显示"}, {Label: "复制文件路径"}})
		tt.Frame()
		w0, h0 := extent()
		tt.Advance(60 * time.Millisecond)
		w1, h1 := extent()
		tt.Advance(motionPane.Duration)
		w2, h2 := extent()
		t.Logf("菜单首帧、中间帧、末帧 %dx%d, %dx%d, %dx%d", w0, h0, w1, h1, w2, h2)
		if !(w0 < w1 && w1 < w2 && h0 < h1 && h1 < h2 && w2 < 200 && w0 > 100) {
			t.Fatal("曲库菜单应从点击处展开并按最长命令收紧宽度")
		}
		tt.Key(0, ui.KeyEscape)
		tt.Frame()
	}
}

func TestLibraryContextMenuClickCopiesPath(t *testing.T) {
	a := newApp()
	const path = `C:\Music\Evergreen.mp3`
	a.libFolders = []string{`C:\Music`}
	a.tracks = []trackItem{{Title: "Evergreen", FullPath: path, File: "mp3", Status: "missing"}}
	got := menutest.Choose(t, kiralrcmenu.Content{View: ui.View(a.view), Open: a.openWindowMenu}, "Evergreen", "复制文件路径")
	if got != path {
		t.Fatalf("复制文件路径得到 %q，期望 %q", got, path)
	}
}
