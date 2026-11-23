package app

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
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
//	KIRALRC_SHOT_PAGES=library,tools,settings  只截这几页（含 scrape / 刮削页）
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

	pages := []struct {
		nav, name string
		setup     func(*app) // 不在侧边栏的页面：自己摆状态、自己跳过去
	}{
		{"曲库", "library", nil},
		{"新建对齐", "align", nil},
		{"任务队列", "queue", nil},
		{"编辑与校对", "editor", nil},
		{"工具箱", "tools", nil},
		{"设置", "settings", nil},
		{"", "scrape", seedScrapeDemo},
		{"", "cleanup", seedCleanupDemo},
	}
	made := 0
	for _, p := range pages {
		if len(only) > 0 && !only[p.name] {
			continue
		}
		if p.setup != nil {
			// 不在侧边栏的页面没法 Click 过去：先把状态摆好，再换一台在首帧
			// 之前就已是这页的 Tester——否则页面淡入没走完，拍出来是空白。
			p.setup(a)
			tt = ui.NewTester(a.view, width, height)
			tt.SetScale(scale)
		} else if err := tt.Click(p.nav); err != nil {
			t.Logf("跳过 %s：%v", p.nav, err)
			continue
		}
		tt.Advance(400 * time.Millisecond) // 等页面淡入走完
		tt.Frame()
		tt.Frame() // 再走一帧，过渡才算落定（直接设 page 的页面尤其需要）
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
		label := p.nav
		if label == "" {
			label = p.name // 直接设 page 的页面没有导航文字
		}
		t.Logf("%-10s → %s  %dx%d", label, out, img.Rect.Dx(), img.Rect.Dy())
	}
	if made == 0 {
		t.Fatal("一张都没截出来")
	}
	fmt.Printf("共 %d 张，输出目录 %s\n", made, dir)
}

// seedScrapeDemo 给刮削页摆一桌演示数据。
//
// 这页不在侧边栏，没法用 tt.Click 导航，所以直接设 page 并灌好左栏曲目、
// 右栏候选与一张「已确认」；候选封面用本地生成的渐变图塞进 scrapeThumbs，
// 截图不联网也能看到真实卡片版式。
func seedScrapeDemo(a *app) {
	const dir = `E:/cloudy music\WHITE ALBUM 2`
	items := []scrapeTrack{
		{Name: "届かない恋.flac", Title: "届かない恋", Artist: "上原れな", Album: "WHITE ALBUM 2 ORIGINAL SOUNDTRACK"},
		{Name: "幸せな記憶.flac", Title: "幸せな記憶", Artist: "上原れな", Album: "WHITE ALBUM 2 ORIGINAL SOUNDTRACK",
			Year: "2011", CoverW: 1200, CoverH: 1200},
		{Name: "Twinkle Snow.flac", Title: "Twinkle Snow", Artist: "津田朱里", Album: "WHITE ALBUM 2 ORIGINAL SOUNDTRACK",
			Year: "2011", NeteaseID: 28718123, CoverW: 300, CoverH: 300},
		{Name: "answer.flac", Title: "answer", Artist: "上原れな", Album: "WHITE ALBUM 2 ORIGINAL SOUNDTRACK",
			Writable: false},
		{Name: "POWDER SNOW.flac", Title: "POWDER SNOW", Artist: "米澤円", Album: "WHITE ALBUM 2 ORIGINAL SOUNDTRACK",
			NeteaseID: 28718124, CoverW: 1200, CoverH: 1200},
		{Name: "After All.flac", Title: "After All", Artist: "上原れな", Album: "WHITE ALBUM 2 ORIGINAL SOUNDTRACK",
			Year: "2011", NeteaseID: 28718125, CoverW: 600, CoverH: 600, WriteState: "done"},
	}
	for i := range items {
		items[i].Path = filepath.Join(dir, items[i].Name)
		items[i].Format = "flac"
		items[i].Writable = items[i].Title != "answer" // answer 演示「只读跳过」
	}

	cands := []scrapeCandidate{
		{Source: "网易云", Title: "届かない恋", Artist: "上原れな", Album: "WHITE ALBUM 2 ORIGINAL SOUNDTRACK",
			Year: "2011", Duration: 292, SongID: 28718123, ThumbURL: "demo://netease/1", CoverURL: "https://p1.music.126.net/demo-1.jpg"},
		{Source: "iTunes", Title: "届かない恋", Artist: "Various Artists", Album: "WHITE ALBUM 2 Original Soundtrack",
			Year: "2011", Duration: 291, ThumbURL: "demo://itunes/1", CoverURL: "https://x/demo-2.jpg"},
		{Source: "网易云", Title: "届かない恋 '13", Artist: "小木曽雪菜", Album: "届かない恋 '13",
			Year: "2013", Duration: 302, SongID: 26508034, ThumbURL: "demo://netease/2", CoverURL: "https://x/demo-3.jpg"},
		{Source: "iTunes", Title: "Kodoku na I Love You", Artist: "Rena Uehara", Album: "WHITE ALBUM 2",
			Year: "2011", Duration: 260, ThumbURL: "demo://itunes/2", CoverURL: "https://x/demo-4.jpg"},
		{Source: "网易云", Title: "幸せな記憶", Artist: "上原れな", Album: "WHITE ALBUM 2 ORIGINAL SOUNDTRACK",
			Year: "2011", Duration: 268, SongID: 28718124, ThumbURL: "demo://netease/3", CoverURL: "https://x/demo-5.jpg"},
	}

	a.page = pageScrape
	a.scrapeDir = dir
	a.scrapePaths = nil
	a.scrapeItems = items
	a.scrapeCur = 0
	a.scrapeQuery = "届かない恋 上原れな"
	a.scrapeAuth = "Cloudy 登录态"
	a.scrapeCands = cands
	a.scrapeCandPath = items[0].Path
	a.scrapeSel = map[string]scrapeCandidate{items[0].Path: cands[1]}
	a.scrapeFields = [5]bool{true, true, true, false, false}

	hues := []float64{8, 32, 210, 280, 150}
	// 实测像素也一起摆好：截图不联网，卡片上才有真实尺寸可看
	sizes := [][2]int{{1600, 1600}, {1200, 1200}, {390, 390}, {3000, 3000}, {800, 800}}
	a.scrapeMu.Lock()
	a.scrapeThumbs = map[string]*ui.Bitmap{}
	a.scrapeSizes = map[string][2]int{}
	for i, c := range cands {
		if c.ThumbURL != "" {
			a.scrapeThumbs[c.ThumbURL] = ui.NewBitmap(demoCover(hues[i%len(hues)]))
		}
		if c.CoverURL != "" {
			a.scrapeSizes[c.CoverURL] = sizes[i%len(sizes)]
		}
	}
	a.scrapeMu.Unlock()
}

// demoCover 造一张纯渐变的方形封面，替掉联网缩略图，截图才有图可看。
func demoCover(hue float64) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 240, 240))
	r1, g1, b1 := hsvToRGB(hue, 0.55, 0.95)
	r2, g2, b2 := hsvToRGB(hue+35, 0.62, 0.55)
	for y := 0; y < 240; y++ {
		t := float64(y) / 239
		for x := 0; x < 240; x++ {
			k := t * 0.85
			img.Set(x, y, color.RGBA{
				R: uint8(r1 + (r2-r1)*k),
				G: uint8(g1 + (g2-g1)*k),
				B: uint8(b1 + (b2-b1)*k),
				A: 255,
			})
		}
	}
	return img
}

func hsvToRGB(h, s, v float64) (float64, float64, float64) {
	h = math.Mod(math.Mod(h, 360)+360, 360) / 60
	c := v * s
	x := c * (1 - math.Abs(math.Mod(h, 2)-1))
	m := v - c
	var r, g, b float64
	switch int(h) {
	case 0:
		r, g, b = c, x, 0
	case 1:
		r, g, b = x, c, 0
	case 2:
		r, g, b = 0, c, x
	case 3:
		r, g, b = 0, x, c
	case 4:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	return (r + m) * 255, (g + m) * 255, (b + m) * 255
}

// seedCleanupDemo 给「清理歌词文件」页摆一桌演示数据：
// 三组已选 / 未选的歌词，外加一条上一次操作的真实结果。
func seedCleanupDemo(a *app) {
	dir := `E://cloudy music\WHITE ALBUM 2`
	a.page = pageCleanup
	a.cleanupDir = dir
	a.cleanupScanned = true
	a.cleanupResult = "已移入回收站 12 个，3 个没动（操作被取消）。"
	a.cleanupItems = []cleanupGroup{
		{Ext: ".lrc", Files: []string{
			filepath.Join(dir, "届かない恋.lrc"),
			filepath.Join(dir, "幸せな記憶.lrc"),
			filepath.Join(dir, "Twinkle Snow.lrc"),
		}, Sel: true},
		{Ext: ".vtt", Files: []string{filepath.Join(dir, "answer.vtt")}},
		{Ext: ".ttml", Files: []string{
			filepath.Join(dir, "POWDER SNOW.ttml"),
			filepath.Join(dir, "After All.ttml"),
		}},
		{Ext: ".txt", Files: []string{
			filepath.Join(dir, "歌词txt", "说明.txt"),
			filepath.Join(dir, "歌词txt", "备份.txt"),
		}, Sel: true},
	}
}
