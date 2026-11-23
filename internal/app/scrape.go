package app

// 标签刮削：左栏排队、右栏并排看候选，由人挑版本后再批量写回。
//
// 这一页只做「确认」这一件事——后端（kiralrc scrape）负责读现状、搜候选、
// 保守写盘；这里负责把候选摆出来，绝不自作主张地选一张。

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif" // 内嵌封面可能是这几种，注册解码器
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"golang.org/x/image/draw"
)

// 写入字段的顺序与 scrapeFields 的下标一一对应。
var scrapeFieldLabels = [5]string{"高清封面", "专辑年份", "NETEASE_ID", "专辑名", "标题 / 歌手"}

type scrapeTrack struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	Format    string `json:"format"`
	Writable  bool   `json:"writable"`
	Title     string `json:"title"`
	Artist    string `json:"artist"`
	Album     string `json:"album"`
	Year      string `json:"year"`
	NeteaseID int    `json:"netease_id"`
	CoverW    int    `json:"cover_w"`
	CoverH    int    `json:"cover_h"`

	WriteState string `json:"-"` // "" 待选 / "done" 已写入 / "readonly" 只读跳过
}

type scrapeCandidate struct {
	Source    string `json:"source"`
	Title     string `json:"title"`
	Artist    string `json:"artist"`
	Album     string `json:"album"`
	Year      string `json:"year"`
	Duration  int    `json:"duration"`
	SongID    int    `json:"song_id"`
	ThumbURL  string `json:"thumb_url"`
	CoverURL  string `json:"cover_url"`
	CoverHint string `json:"cover_hint"`
}

type scrapePlanItem struct {
	Path      string          `json:"path"`
	Candidate scrapeCandidate `json:"candidate"`
	Fields    map[string]bool `json:"fields"`
	Mode      string          `json:"mode"`
}

// ---------- 快捷筛选 ----------

// 三种「该补」的情况分开算：缺封面（一张都没）和低清（有图但太小）
// 是两件事，混在一个胶囊里会让人以为「低清」的那些连封面都没有。

func scrapeNoCover(t scrapeTrack) bool {
	return t.CoverW <= 0 || t.CoverH <= 0
}

func scrapeLowRes(t scrapeTrack) bool {
	return !scrapeNoCover(t) && (t.CoverW < 600 || t.CoverH < 600)
}

// 缺 ID / 年份
func scrapeNeedsMeta(t scrapeTrack) bool {
	return t.NeteaseID <= 0 || strings.TrimSpace(t.Year) == ""
}

// scrapeFilterCounts 各胶囊后面的数字，跟 scrapeVisible 用的是同一套判断。
func (a *app) scrapeFilterCounts() [4]int {
	var out [4]int
	for _, t := range a.scrapeItems {
		out[0]++
		if scrapeNoCover(t) {
			out[1]++
		}
		if scrapeLowRes(t) {
			out[2]++
		}
		if scrapeNeedsMeta(t) {
			out[3]++
		}
	}
	return out
}

func (a *app) scrapeVisible() []int {
	out := make([]int, 0, len(a.scrapeItems))
	for i, t := range a.scrapeItems {
		switch a.scrapeFilter {
		case 1:
			if !scrapeNoCover(t) {
				continue
			}
		case 2:
			if !scrapeLowRes(t) {
				continue
			}
		case 3:
			if !scrapeNeedsMeta(t) {
				continue
			}
		}
		out = append(out, i)
	}
	return out
}

// ---------- 进入页面 ----------

func (a *app) openScrapeFolder(dir string) {
	a.scrapeDir = dir
	a.scrapePaths = nil
	a.navigate(pageScrape, 1)
	a.loadScrapeItems()
}

func (a *app) openScrapeTracks(paths []string) {
	if len(paths) == 0 {
		return
	}
	a.scrapeDir = ""
	a.scrapePaths = append([]string{}, paths...)
	a.navigate(pageScrape, 1)
	a.loadScrapeItems()
}

func (a *app) scrapeSubtitle() string {
	if a.scrapeDir != "" {
		return a.scrapeDir
	}
	if len(a.scrapePaths) > 0 {
		return fmt.Sprintf("曲库勾选 %d 首", len(a.scrapePaths))
	}
	return ""
}

// loadScrapeItems 让 Python 读一遍现状。目录大时这一步要几百毫秒，后台跑。
func (a *app) loadScrapeItems() {
	if a.scrapeLoading {
		return
	}
	a.scrapeLoading = true
	a.scrapeItems = nil
	a.scrapeCands = nil
	a.scrapeCandPath = ""
	a.scrapeCur = 0
	a.scrapeSel = map[string]scrapeCandidate{}
	a.scrapeCandCache = map[string][]scrapeCandidate{}

	dir := a.scrapeDir
	paths := append([]string{}, a.scrapePaths...)
	go func() {
		args := []string{"scrape", "inspect"}
		if len(paths) > 0 {
			listPath := filepath.Join(appRootDir(), "work", "scrape_paths.txt")
			_ = os.MkdirAll(filepath.Dir(listPath), 0o755)
			if err := os.WriteFile(listPath, []byte(strings.Join(paths, "\n")+"\n"), 0o644); err != nil {
				a.post(func() {
					a.scrapeLoading = false
					a.toast = "写不了文件清单：" + err.Error()
				})
				return
			}
			args = append(args, "--paths-file", listPath)
		} else {
			args = append(args, dir)
		}
		cmd := a.pythonCmd(args...)
		out, err := cmd.Output()
		var items []scrapeTrack
		if err == nil {
			err = json.Unmarshal(out, &items)
		}
		a.post(func() {
			a.scrapeLoading = false
			if err != nil {
				a.toast = "读取标签失败：" + err.Error()
				return
			}
			a.scrapeItems = items
			if len(items) > 0 {
				a.scrapeQuery = scrapeDefaultQuery(items[0])
				a.searchScrapeCandidates(false)
			}
		})
	}()
}

func scrapeDefaultQuery(t scrapeTrack) string {
	title := strings.TrimSpace(t.Title)
	artist := strings.TrimSpace(t.Artist)
	if title == "" {
		stem := strings.TrimSuffix(filepath.Base(t.Path), filepath.Ext(t.Path))
		title = strings.TrimSpace(stem)
	}
	if artist == "" {
		return title
	}
	return title + " " + artist
}

// ---------- 选中曲目 / 搜索候选 ----------

func (a *app) selectScrapeTrack(idx int) {
	if idx < 0 || idx >= len(a.scrapeItems) {
		return
	}
	if idx == a.scrapeCur && a.scrapeCandPath == a.scrapeItems[idx].Path {
		return
	}
	a.scrapeCur = idx
	it := a.scrapeItems[idx]
	a.scrapeCands = nil
	a.scrapeCandPath = ""
	a.scrapeQuery = scrapeDefaultQuery(it)
	if cached, ok := a.scrapeCandCache[it.Path]; ok {
		a.scrapeCands = cached
		a.scrapeCandPath = it.Path
		for _, c := range cached {
			a.requestScrapeThumb(c.ThumbURL)
		}
		return
	}
	a.searchScrapeCandidates(false)
}

// searchScrapeCandidates 搜当前曲目的候选；force 表示用户改了关键词重搜。
func (a *app) searchScrapeCandidates(force bool) {
	if a.scrapeCur < 0 || a.scrapeCur >= len(a.scrapeItems) {
		return
	}
	idx := a.scrapeCur
	it := a.scrapeItems[idx]
	query := strings.TrimSpace(a.scrapeQuery)
	if query == "" {
		query = scrapeDefaultQuery(it)
	}
	if force {
		delete(a.scrapeCandCache, it.Path)
	}
	a.scrapeSearching = true
	a.scrapeCandPath = it.Path
	if force {
		a.scrapeCands = nil
	}

	path, title, artist, id := it.Path, it.Title, it.Artist, it.NeteaseID
	go func() {
		args := []string{"scrape", "search", query}
		if id > 0 {
			args = append(args, "--netease-id", strconv.Itoa(id))
		}
		if artist != "" {
			args = append(args, "--artist", artist)
		}
		if title != "" {
			args = append(args, "--title", title)
		}
		cmd := a.pythonCmd(args...)
		out, err := cmd.Output()
		var cands []scrapeCandidate
		if err == nil {
			err = json.Unmarshal(out, &cands)
		}
		a.post(func() {
			if a.scrapeCandPath != path {
				return // 已经切到别的曲目，晚到的结果丢掉
			}
			a.scrapeSearching = false
			if err != nil {
				a.toast = "搜索候选失败：" + err.Error()
				return
			}
			a.scrapeCandCache[path] = cands
			a.scrapeCands = cands
			for _, c := range cands {
				a.requestScrapeThumb(c.ThumbURL)
			}
		})
	}()
}

// ---------- 候选选中 ----------

func (a *app) toggleScrapeCandidate(path string, cand scrapeCandidate) {
	if a.scrapeSel == nil {
		a.scrapeSel = map[string]scrapeCandidate{}
	}
	if cur, ok := a.scrapeSel[path]; ok && cur == cand {
		delete(a.scrapeSel, path)
		return
	}
	a.scrapeSel[path] = cand
}

func (a *app) scrapeSelectedCount() int {
	return len(a.scrapeSel)
}

func (a *app) isScrapeCandidateSelected(path string, cand scrapeCandidate) bool {
	cur, ok := a.scrapeSel[path]
	return ok && cur == cand
}

// ---------- 缩略图 ----------

func scrapeThumbDir() string {
	return filepath.Join(appRootDir(), "work", "scrape_thumbs")
}

func scrapeThumbPath(url string) string {
	sum := md5.Sum([]byte(url))
	return filepath.Join(scrapeThumbDir(), fmt.Sprintf("%x.jpg", sum[:8]))
}

// requestScrapeThumb 把候选缩略图下到 work/scrape_thumbs/ 再解码，避免每次重绘都联网。
func (a *app) requestScrapeThumb(url string) {
	if url == "" {
		return
	}
	a.scrapeMu.Lock()
	if a.scrapeThumbs == nil {
		a.scrapeThumbs = map[string]*ui.Bitmap{}
		a.scrapeThumbBusy = map[string]bool{}
	}
	if _, ok := a.scrapeThumbs[url]; ok {
		a.scrapeMu.Unlock()
		return
	}
	if a.scrapeThumbBusy[url] {
		a.scrapeMu.Unlock()
		return
	}
	a.scrapeThumbBusy[url] = true
	a.scrapeMu.Unlock()

	go func() {
		data, err := os.ReadFile(scrapeThumbPath(url))
		if err != nil {
			data = downloadScrapeThumb(url)
		}
		var img image.Image
		if len(data) > 0 {
			img = scrapeThumbBitmap(data)
		}
		a.post(func() {
			a.scrapeMu.Lock()
			delete(a.scrapeThumbBusy, url)
			a.scrapeMu.Unlock()
			if img != nil {
				a.scrapeThumbs[url] = ui.NewBitmap(img)
			} else {
				a.scrapeThumbs[url] = nil // 记个负结果，别每帧重试同一个坏链
			}
		})
	}()
}

func downloadScrapeThumb(url string) []byte {
	client := &http.Client{Timeout: 20 * time.Second}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64)")
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil || len(data) < 512 {
		return nil
	}
	path := scrapeThumbPath(url)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
		_ = os.WriteFile(path, data, 0o644)
	}
	return data
}

// scrapeThumbBitmap 居中裁成正方形再缩到 240，画廊里不必反复缩放大图。
func scrapeThumbBitmap(data []byte) image.Image {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	b := src.Bounds()
	size := min(b.Dx(), b.Dy())
	if size <= 0 {
		return nil
	}
	x, y := b.Min.X+(b.Dx()-size)/2, b.Min.Y+(b.Dy()-size)/2
	thumb := image.NewRGBA(image.Rect(0, 0, 240, 240))
	draw.ApproxBiLinear.Scale(thumb, thumb.Bounds(), src,
		image.Rect(x, y, x+size, y+size), draw.Src, nil)
	return thumb
}

func cleanupScrapeThumbs() {
	_ = os.RemoveAll(scrapeThumbDir())
}

// ---------- 写回 ----------

func (a *app) scrapePlan() []scrapePlanItem {
	fields := map[string]bool{
		"cover":        a.scrapeFields[0],
		"year":         a.scrapeFields[1],
		"netease_id":   a.scrapeFields[2],
		"album":        a.scrapeFields[3],
		"title_artist": a.scrapeFields[4],
	}
	mode := "fill"
	if a.scrapeMode == 1 {
		mode = "overwrite"
	}
	plan := make([]scrapePlanItem, 0, len(a.scrapeSel))
	for _, it := range a.scrapeItems {
		cand, ok := a.scrapeSel[it.Path]
		if !ok {
			continue
		}
		plan = append(plan, scrapePlanItem{Path: it.Path, Candidate: cand, Fields: fields, Mode: mode})
	}
	return plan
}

func (a *app) applyScrape() {
	if a.scrapeApplying {
		return
	}
	plan := a.scrapePlan()
	if len(plan) == 0 {
		a.toast = "还没有确认任何候选"
		return
	}
	payload := map[string]any{"items": plan}
	data, err := json.Marshal(payload)
	if err != nil {
		a.toast = "整理写入计划失败"
		return
	}
	planPath := filepath.Join(appRootDir(), "work", "scrape_plan.json")
	if err := os.MkdirAll(filepath.Dir(planPath), 0o755); err != nil {
		a.toast = "建不了临时目录：" + err.Error()
		return
	}
	if err := os.WriteFile(planPath, data, 0o644); err != nil {
		a.toast = "写不了写入计划：" + err.Error()
		return
	}

	a.scrapeApplying = true
	a.scrapeDone, a.scrapeTotal = 0, len(plan)

	go func() {
		cmd := a.pythonCmd("scrape", "apply", planPath)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			a.finishScrapeApply(err.Error(), nil)
			return
		}
		cmd.Stderr = nil // Python 的告警别混进 JSON
		if err := cmd.Start(); err != nil {
			a.finishScrapeApply(err.Error(), nil)
			return
		}
		results := map[string]string{}
		var ok, skipped, failed int
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if !strings.HasPrefix(line, "{") {
				continue
			}
			var msg struct {
				I        int      `json:"i"`
				Total    int      `json:"total"`
				Path     string   `json:"path"`
				Written  bool     `json:"written"`
				Changed  []string `json:"changed"`
				Note     string   `json:"note"`
				Readonly bool     `json:"readonly"`
				Summary  bool     `json:"summary"`
				OK       int      `json:"ok"`
				Failed   int      `json:"failed"`
				Skipped  int      `json:"skipped"`
			}
			if json.Unmarshal([]byte(line), &msg) != nil {
				continue
			}
			if msg.Summary {
				ok, skipped, failed = msg.OK, msg.Skipped, msg.Failed
				continue
			}
			if msg.Path != "" {
				switch {
				case msg.Written:
					results[msg.Path] = "done"
				case msg.Readonly:
					results[msg.Path] = "readonly"
				}
			}
			done, total := msg.I, msg.Total
			a.post(func() {
				a.scrapeDone, a.scrapeTotal = done, total
			})
		}
		waitErr := cmd.Wait()
		note := ""
		if waitErr != nil && ok+skipped+failed == 0 {
			note = "写入进程异常：" + waitErr.Error()
		}
		a.finishScrapeApply(note, results)
		a.post(func() {
			if note == "" {
				a.toast = fmt.Sprintf("写入完成：成功 %d 首，跳过 %d 首，失败 %d 首", ok, skipped, failed)
			}
		})
	}()
}

func (a *app) finishScrapeApply(errMsg string, results map[string]string) {
	// 先算好写成功的清单：post 里的闭包是异步执行的，别指望它填好再读
	var written []string
	for path, state := range results {
		if state == "done" {
			written = append(written, path)
		}
	}
	a.post(func() {
		a.scrapeApplying = false
		if errMsg != "" {
			a.toast = errMsg
			return
		}
		for path, state := range results {
			for i := range a.scrapeItems {
				if a.scrapeItems[i].Path != path {
					continue
				}
				a.scrapeItems[i].WriteState = state
				if state == "done" {
					delete(a.scrapeSel, path) // 已写入的不再重复提交，没成的留着重试
				}
			}
		}
		if len(a.libFolders) > 0 {
			resetCoverKeys()
			a.scanLibraryAsync()
		}
	})
	if len(written) > 0 {
		a.refreshScrapeItems(written)
	}
}

// refreshScrapeItems 只重读刚写过的这几首，把封面尺寸、年份这些显示刷新。
func (a *app) refreshScrapeItems(paths []string) {
	listPath := filepath.Join(appRootDir(), "work", "scrape_refresh.txt")
	go func() {
		_ = os.MkdirAll(filepath.Dir(listPath), 0o755)
		if err := os.WriteFile(listPath, []byte(strings.Join(paths, "\n")+"\n"), 0o644); err != nil {
			return
		}
		cmd := a.pythonCmd("scrape", "inspect", "--paths-file", listPath)
		out, err := cmd.Output()
		if err != nil {
			return
		}
		var fresh []scrapeTrack
		if json.Unmarshal(out, &fresh) != nil {
			return
		}
		a.post(func() {
			a.scrapeMu.Lock()
			a.scrapeCovers = nil // 刚写过封面，内嵌图的缩略图要重解
			a.scrapeMu.Unlock()
			for _, f := range fresh {
				for i := range a.scrapeItems {
					if a.scrapeItems[i].Path != f.Path {
						continue
					}
					f.WriteState = "done"
					a.scrapeItems[i] = f
					delete(a.scrapeCandCache, f.Path)
				}
			}
		})
	}()
}

// ---------- 页面 ----------

func (a *app) viewScrape(c *ui.Context) {
	p := a.pal
	col := ui.Column(c).Fill().Padding(pageGap, 38, 108, 38).Gap(12)
	col.Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(12).Children(func() {
			if pillButton(c.Key("scrape-back"), iconPrev, "返回工具箱", p.surface2, p.text).Clicked() {
				a.navigate(pageTools, 0)
			}
			ui.Column(c).Gap(2).Children(func() {
				ui.Text(c, "标签刮削").FontSize(26).FontWeight(800)
				ui.Text(c, a.scrapeSubtitle()).FontSize(12).TextColor(p.muted).SingleLine()
			})
			ui.Box(c).Grow(1)
			if a.scrapeLoading {
				winFiveDots(c, p.accent)
			}
			ui.Text(c, fmt.Sprintf("共 %d 首 · 已确认 %d 首", len(a.scrapeItems), a.scrapeSelectedCount())).
				FontSize(12).TextColor(p.muted).SingleLine()
			if pillButton(c.Key("scrape-reload"), iconRefresh, "重新读取", p.surface2, p.text).Clicked() {
				a.loadScrapeItems()
			}
		})

		body := ui.Row(c.Key("scrape-body")).Grow(1).Basis(0).MinHeight(0).FillWidth().
			AlignItems(ui.Stretch).Clip()
		body.Children(func() {
			a.viewScrapeList(c)
			a.viewScrapeDetail(c)
		})

		a.viewScrapeBar(c)
	})
}

func (a *app) viewScrapeList(c *ui.Context) {
	p := a.pal
	left := ui.Column(c.Key("scrape-left")).Width(280).Shrink(0).MinHeight(0).Gap(10)
	left.Children(func() {
		counts := a.scrapeFilterCounts()
		labels := [4]string{"全部", "无封面", "低清 (<600px)", "缺 ID / 年份"}
		// 两行两列：280px 里塞得下，数字让「缺封面」和「低清」不再混为一谈
		for row := 0; row < 2; row++ {
			start := row * 2
			ui.Row(c).FillWidth().Gap(6).Children(func() {
				for i := start; i < start+2; i++ {
					a.scrapeFilterChip(c, i, labels[i], counts[i])
				}
			})
		}

		if a.scrapeLoading {
			ui.Text(c, "正在读取标签…").FontSize(12.5).TextColor(p.muted)
		} else if len(a.scrapeItems) == 0 {
			ui.Text(c, "这个目录里没有可处理的音频。").FontSize(12.5).TextColor(p.muted)
		}

		sc := ui.Scroll(c.Key("scrape-list")).TrackScroll(&a.scrapeListScroll).
			Grow(1).Basis(0).MinHeight(0).FillWidth()
		sc.Children(func() {
			ui.Column(c).FillWidth().Gap(4).Children(func() {
				for _, idx := range a.scrapeVisible() {
					a.viewScrapeRow(c, idx)
				}
			})
		})
	})
}

func (a *app) viewScrapeRow(c *ui.Context, idx int) {
	p := a.pal
	it := a.scrapeItems[idx]
	key := "scrape-row-" + strconv.Itoa(idx)
	row := ui.Row(c.Key(key)).FillWidth().Height(50).PaddingX(9).Gap(8).Radius(10).AlignItems(ui.Center)
	switch {
	case idx == a.scrapeCur:
		row.Background(p.accent.Alpha(0.14))
	case row.Hovered():
		row.Background(p.hover)
	}
	row.Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			title := strings.TrimSpace(it.Title)
			if title == "" {
				title = strings.TrimSuffix(it.Name, filepath.Ext(it.Name))
			}
			ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, title).FontSize(12.5).FontWeight(650).Grow(1).MinWidth(0).SingleLine()
				label, bg, fg := a.scrapeRowState(it)
				scrapeStatePill(c, label, bg, fg)
			})
			ui.Row(c).FillWidth().Gap(5).AlignItems(ui.Center).Children(func() {
				artist := strings.TrimSpace(it.Artist)
				if artist == "" {
					artist = "未知歌手"
				}
				ui.Text(c, artist).FontSize(10.5).TextColor(p.faint).Grow(1).MinWidth(0).SingleLine()
				if tag := scrapeLocalTag(it); tag != "" {
					// 这套布局里 Row 的右内边距对子元素不生效，靠外边距留出空隙
					ui.Text(c, tag).FontSize(10).TextColor(p.muted).Shrink(0).Margin(0, 6, 0, 0)
				}
			})
		})
	})
	if row.Clicked() {
		a.selectScrapeTrack(idx)
		if a.win != nil {
			a.win.Invalidate()
		}
	}
}

// scrapeFilterChip 左栏顶部的筛选胶囊：标签 + 条数。
func (a *app) scrapeFilterChip(c *ui.Context, idx int, label string, count int) {
	p := a.pal
	on := a.scrapeFilter == idx
	bg, fg := p.surface2, p.muted
	if on {
		bg, fg = p.accent.Alpha(0.16), p.accent
	}
	b := ui.ButtonBase(c.Key("scrape-filter-" + strconv.Itoa(idx))).Grow(1).Basis(0).Height(28).
		PaddingX(9).Gap(5).Radius(999).AlignItems(ui.Center).Label(label).FocusRing(false)
	fill := bg
	if b.Hovered() {
		fill = bg.Mix(ui.RGB(255, 255, 255), 0.12)
	}
	b.Background(b.AnimateColor("bg", fill, ui.Animation{Duration: 120 * time.Millisecond}))
	b.Children(func() {
		ui.Text(c, label).FontSize(11.5).FontWeight(600).TextColor(fg).SingleLine()
		ui.Text(c, strconv.Itoa(count)).FontSize(10.5).FontWeight(700).TextColor(fg).SingleLine()
	})
	if b.Clicked() {
		a.scrapeFilter = idx
	}
}

// scrapeStatePill 比 statusPill 更小一号：左栏只有 280px，字得挤得下。
func scrapeStatePill(c *ui.Context, label string, bg, fg ui.Color) {
	b := ui.Box(c).Height(20).PaddingX(7).Radius(999).Center().Shrink(0).Background(bg)
	b.Children(func() {
		ui.Text(c, label).FontSize(10.5).FontWeight(700).TextColor(fg).SingleLine()
	})
}

// scrapeRowState 左栏右侧的状态胶囊。
func (a *app) scrapeRowState(t scrapeTrack) (string, ui.Color, ui.Color) {
	p := a.pal
	switch {
	case t.WriteState == "done":
		return "已写入", p.okBg, p.ok
	case t.WriteState == "readonly" || !t.Writable:
		return "只读跳过", p.errBg, p.err
	}
	if cand, ok := a.scrapeSel[t.Path]; ok {
		return "✓ 已选(" + cand.Source + ")", p.okBg, p.ok
	}
	return "待选", p.surface2, p.faint
}

// scrapeLocalTag 左栏的「本地现状」小字：只说最要紧的一件事，280px 才放得下。
// 细节（分辨率、年份、ID）都在右栏的现状卡里。
func scrapeLocalTag(t scrapeTrack) string {
	switch {
	case t.CoverW <= 0:
		return "无封面"
	case t.CoverW < 600 || t.CoverH < 600:
		return fmt.Sprintf("%dpx", min(t.CoverW, t.CoverH))
	case t.NeteaseID <= 0:
		return "缺ID"
	case strings.TrimSpace(t.Year) == "":
		return "缺年"
	}
	return ""
}

// viewScrapeDetail 右栏：当前曲目的现状 + 候选画廊。
func (a *app) viewScrapeDetail(c *ui.Context) {
	p := a.pal
	right := ui.Column(c.Key("scrape-right")).Grow(1).MinWidth(0).MinHeight(0).Gap(12)
	right.Children(func() {
		if a.scrapeCur < 0 || a.scrapeCur >= len(a.scrapeItems) {
			ui.Text(c, "左侧选一首歌，右边会出现候选封面。").FontSize(13).TextColor(p.muted)
			return
		}
		it := a.scrapeItems[a.scrapeCur]
		a.viewScrapeCurrent(c, it)

		if a.scrapeSearching {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				winFiveDots(c, p.accent)
				ui.Text(c, "正在搜网易云与 iTunes 的候选…").FontSize(12.5).TextColor(p.muted)
			})
			return
		}
		if len(a.scrapeCands) == 0 {
			ui.Text(c, "没有候选。改一下上面的关键词再搜一次，或直接跳过这首。").
				FontSize(12.5).TextColor(p.muted)
			return
		}

		gallery := ui.Scroll(c.Key("scrape-gallery")).TrackScroll(&a.scrapeCandScroll).
			Grow(1).Basis(0).MinHeight(0).FillWidth()
		gallery.Children(func() {
			ui.Column(c).FillWidth().Gap(12).Children(func() {
				for start := 0; start < len(a.scrapeCands); start += 3 {
					a.viewScrapeCandRow(c, it, start)
				}
			})
		})
	})
}

func (a *app) viewScrapeCurrent(c *ui.Context, it scrapeTrack) {
	p := a.pal
	card := ui.Column(c.Key("scrape-cur")).FillWidth().Padding(14, 16, 14, 16).Gap(10).
		Radius(14).Background(p.surface)
	card.Draw(func(pt *ui.Painter, r ui.Rect) { pt.Stroke(r, p.hairline, 14, 1) })
	card.Children(func() {
		ui.Row(c).FillWidth().Gap(14).AlignItems(ui.Center).Children(func() {
			// 只认文件里内嵌的封面：对齐时缓存下来的 cover.jpg 不算，
			// 否则左边写着「无封面」、右边却摆着一张图，看着像 bug。
			if bmp := a.scrapeEmbeddedCover(it.Path); bmp != nil {
				ui.Image(c, bmp).Size(76, 76).Radius(10).Fit(ui.Cover).Shrink(0)
			} else {
				artCover(c, hueForString(it.Path), 76, 10, iconNote)
			}
			ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
				title := strings.TrimSpace(it.Title)
				if title == "" {
					title = strings.TrimSuffix(it.Name, filepath.Ext(it.Name))
				}
				ui.Text(c, title).FontSize(16).FontWeight(750).SingleLine()
				sub := strings.TrimSpace(it.Artist)
				if it.Album != "" {
					if sub != "" {
						sub += " · "
					}
					sub += it.Album
				}
				ui.Text(c, sub).FontSize(12).TextColor(p.muted).SingleLine()
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					if it.CoverW > 0 {
						statusPill(c, fmt.Sprintf("%d×%d", it.CoverW, it.CoverH), p.surface2, p.muted)
					} else {
						statusPill(c, "无封面", p.warnBg, p.warn)
					}
					if strings.TrimSpace(it.Year) == "" {
						statusPill(c, "缺年份", p.warnBg, p.warn)
					} else {
						statusPill(c, it.Year, p.surface2, p.muted)
					}
					if it.NeteaseID > 0 {
						statusPill(c, "ID "+strconv.Itoa(it.NeteaseID), p.surface2, p.muted)
					} else {
						statusPill(c, "缺 ID", p.warnBg, p.warn)
					}
					if !it.Writable {
						statusPill(c, "只读格式", p.errBg, p.err)
					}
				})
				ui.Text(c, it.Name).FontSize(11).TextColor(p.faint).SingleLine()
			})
		})

		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			inBox := ui.Row(c).Grow(1).MinWidth(0).Padding(6, 10, 6, 10).Radius(10).
				AlignItems(ui.Center).Background(p.bg)
			inBox.Draw(func(pt *ui.Painter, r ui.Rect) { pt.Stroke(r, p.hairline, 10, 1) })
			inBox.Children(func() {
				ui.TextInputBase(c.Key("scrape-query"), &a.scrapeQuery).Grow(1).MinWidth(0).Height(24).
					FontSize(12.5).TextColor(p.text).Background(ui.Transparent).
					Placeholder("搜索关键词，冷门曲可以改成「歌名 歌手」").FocusRing(false)
			})
			if pillButton(c.Key("scrape-search"), iconSearch, "重新搜索", p.surface2, p.text).Clicked() {
				a.searchScrapeCandidates(true)
			}
		})
	})
}

func (a *app) viewScrapeCandRow(c *ui.Context, it scrapeTrack, start int) {
	ui.Row(c.Key("scrape-cand-row-" + strconv.Itoa(start))).FillWidth().Gap(12).
		AlignItems(ui.Stretch).Children(func() {
		for i := start; i < start+3; i++ {
			if i >= len(a.scrapeCands) {
				ui.Column(c).Grow(1).Basis(0).MinWidth(0).Padding(10, 10, 10, 10)
				continue
			}
			a.viewScrapeCandCard(c, it, a.scrapeCands[i], i)
		}
	})
}

func (a *app) viewScrapeCandCard(c *ui.Context, it scrapeTrack, cand scrapeCandidate, idx int) {
	p := a.pal
	selected := a.isScrapeCandidateSelected(it.Path, cand)
	row := ui.Row(c.Key("scrape-cand-"+strconv.Itoa(idx))).Grow(1).Basis(0).MinWidth(0).
		Padding(9, 9, 9, 9).Radius(13).AlignItems(ui.Stretch)
	bg := p.surface
	if selected {
		bg = p.accent.Alpha(0.10)
	} else if row.Hovered() {
		bg = p.surface2
	}
	row.Background(bg)
	row.Draw(func(pt *ui.Painter, r ui.Rect) {
		if selected {
			pt.Stroke(r, p.accent, 13, 2)
			return
		}
		pt.Stroke(r, p.hairline, 13, 1)
	})
	row.Children(func() {
		ui.Column(c).FillWidth().MinWidth(0).Gap(6).Children(func() {
			coverBox := ui.Box(c.Key("scrape-cand-cover-" + strconv.Itoa(idx))).FillWidth().
				Height(132).Radius(9).Clip()
			coverBox.Children(func() {
				if bmp := a.scrapeThumbOf(cand.ThumbURL); bmp != nil {
					ui.Image(c, bmp).Absolute().Top(0).Left(0).Right(0).Bottom(0).
						Fit(ui.Cover).PassThrough()
				} else {
					a.requestScrapeThumb(cand.ThumbURL)
					ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).
						Background(p.surface2).PassThrough()
				}
			})
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
				srcBg, srcFg := p.surface2, p.text
				if cand.Source == "iTunes" {
					srcBg, srcFg = p.accent.Alpha(0.14), p.accent
				}
				statusPill(c, cand.Source, srcBg, srcFg)
				ui.Box(c).Grow(1)
				ui.Text(c, cand.CoverHint).FontSize(10.5).TextColor(p.faint).SingleLine()
			})
			ui.Text(c, cand.Title).FontSize(12.5).FontWeight(700).SingleLine()
			ui.Text(c, cand.Artist).FontSize(11).TextColor(p.muted).SingleLine()
			ui.Text(c, cand.Album).FontSize(11).TextColor(p.faint).SingleLine()
			meta := cand.Year
			if cand.Duration > 0 {
				meta += fmt.Sprintf("  %d:%02d", cand.Duration/60, cand.Duration%60)
			}
			if cand.SongID > 0 {
				meta += "  ID " + strconv.Itoa(cand.SongID)
			}
			ui.Text(c, strings.TrimSpace(meta)).FontSize(10.5).TextColor(p.muted).SingleLine()
		})
	})
	if row.Clicked() {
		a.toggleScrapeCandidate(it.Path, cand)
		if a.win != nil {
			a.win.Invalidate()
		}
	}
}

func (a *app) scrapeThumbOf(url string) *ui.Bitmap {
	if url == "" {
		return nil
	}
	a.scrapeMu.Lock()
	defer a.scrapeMu.Unlock()
	return a.scrapeThumbs[url]
}

// scrapeEmbeddedCover 取文件里内嵌封面的缩略图——只认内嵌的那张。
//
// 曲库的通用封面取图会退到 work/ 下对齐时缓存的 cover.jpg，
// 放到这一页就成了「左边写无封面、右边却摆着一张图」；刮削要的是文件本身，
// 所以这里绕开缓存，键里带修改时间，换过封面自然失效。
func (a *app) scrapeEmbeddedCover(path string) *ui.Bitmap {
	if path == "" {
		return nil
	}
	mt := int64(0)
	if info, err := os.Stat(path); err == nil {
		mt = info.ModTime().UnixNano()
	}
	key := fmt.Sprintf("%s|%d", path, mt)
	a.scrapeMu.Lock()
	if bmp, ok := a.scrapeCovers[key]; ok {
		a.scrapeMu.Unlock()
		return bmp
	}
	a.scrapeMu.Unlock()

	var bmp *ui.Bitmap
	if thumb := embeddedCoverThumb(readEmbeddedCover(path)); thumb != nil {
		bmp = ui.NewBitmap(thumb)
	}
	a.scrapeMu.Lock()
	if a.scrapeCovers == nil {
		a.scrapeCovers = map[string]*ui.Bitmap{}
	}
	a.scrapeCovers[key] = bmp // 解不出来也记下 nil，别每帧重开文件
	a.scrapeMu.Unlock()
	return bmp
}

// embeddedCoverThumb 解码内嵌封面并裁成 128px 方块；没有或解不动返回 nil。
func embeddedCoverThumb(data []byte) image.Image {
	if len(data) < 32 {
		return nil
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	return thumbnailImage(img)
}

func (a *app) viewScrapeBar(c *ui.Context) {
	p := a.pal
	bar := ui.Column(c.Key("scrape-bar")).FillWidth().Padding(12, 14, 12, 14).Gap(10).
		Radius(14).Background(p.surface)
	bar.Draw(func(pt *ui.Painter, r ui.Rect) { pt.Stroke(r, p.hairline, 14, 1) })
	bar.Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(2).Children(func() {
			ui.Text(c, "写入字段").FontSize(12).FontWeight(700).TextColor(p.muted).Margin(0, 10, 0, 4)
			for i, label := range scrapeFieldLabels {
				a.scrapeFieldCheck(c, i, label)
			}
		})

		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
			hint := "只写勾选的字段；「仅补缺失」不动已有的值，也不碰 163 key 与封底图。"
			if a.scrapeApplying {
				hint = fmt.Sprintf("正在写入 %d / %d…", a.scrapeDone, a.scrapeTotal)
			}
			ui.Text(c, hint).FontSize(11.5).TextColor(p.muted).Grow(1).MinWidth(0).SingleLine()

			segmented(c.Key("scrape-mode"), &a.scrapeMode, "仅补缺失", "覆盖选中")

			n := a.scrapeSelectedCount()
			label := fmt.Sprintf("写入已确认（%d 首）", n)
			bg, fg := p.accent, p.onAccent
			if n == 0 || a.scrapeApplying {
				bg, fg = p.surface2, p.faint
				if a.scrapeApplying {
					label = fmt.Sprintf("写入中 %d/%d", a.scrapeDone, a.scrapeTotal)
				}
			}
			if pillButton(c.Key("scrape-apply"), iconCheck, label, bg, fg).Clicked() && n > 0 && !a.scrapeApplying {
				a.applyScrape()
			}
		})
	})
}

func (a *app) scrapeFieldCheck(c *ui.Context, idx int, label string) {
	p := a.pal
	on := &a.scrapeFields[idx]
	row := ui.Row(c.Key("scrape-field-" + strconv.Itoa(idx))).Height(28).PaddingX(7).Gap(6).
		Radius(8).AlignItems(ui.Center)
	if row.Hovered() {
		row.Background(p.hover)
	}
	row.Children(func() {
		box := ui.Box(c.Key("scrape-field-box-"+strconv.Itoa(idx))).Size(15, 15).Radius(4).
			Center().Shrink(0)
		box.Draw(func(pt *ui.Painter, r ui.Rect) {
			if *on {
				pt.Fill(r, p.accent, 4)
				pt.Fill(ui.Rect{X: r.X + r.W*0.24, Y: r.Y + r.H*0.52, W: r.W * 0.2, H: 2}, p.onAccent, 1)
				pt.Fill(ui.Rect{X: r.X + r.W*0.40, Y: r.Y + r.H*0.40, W: r.W * 0.38, H: 2}, p.onAccent, 1)
				return
			}
			pt.Stroke(r, p.hairline, 4, 1)
		})
		weight := 500
		if *on {
			weight = 700
		}
		ui.Text(c, label).FontSize(12).FontWeight(weight).TextColor(p.text).SingleLine()
	})
	if row.Clicked() {
		*on = !*on
	}
}
