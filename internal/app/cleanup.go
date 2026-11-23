package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"unsafe"

	"github.com/egoist/mygo/ui"
)

// 批量清理歌词：选目录 → 独立页面列出各类歌词文件 → 勾选后删除。
//
// 删除一律先走回收站（SHFileOperation + FOF_ALLOWUNDO），回收站不可用时
// 才退回直接删除，并在提示里说明。

type cleanupGroup struct {
	Ext   string   // .lrc / .vtt / ...
	Files []string // 绝对路径
	Sel   bool
}

var lyricExts = []string{".lrc", ".vtt", ".ttml", ".srt", ".ass", ".ssa", ".txt"}

func (a *app) openCleanup(dir string) {
	a.cleanupDir = dir
	a.cleanupItems = nil
	a.cleanupConfirm = false
	a.cleanupScanned = false
	a.navigate(pageCleanup, 0)
	a.scanCleanup()
}

// scanCleanup 递归找出目录下的歌词类文件，按扩展名分组（默认不勾选）。
func (a *app) scanCleanup() {
	dir := a.cleanupDir
	if dir == "" {
		return
	}
	a.cleanupScanned = false
	go func() {
		groups := map[string][]string{}
		_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				name := d.Name()
				if strings.HasPrefix(name, ".") || name == "work" || name == "models" {
					return filepath.SkipDir
				}
				return nil
			}
			ext := strings.ToLower(filepath.Ext(path))
			for _, want := range lyricExts {
				if ext == want {
					groups[ext] = append(groups[ext], path)
					break
				}
			}
			return nil
		})
		list := make([]cleanupGroup, 0, len(groups))
		for ext, files := range groups {
			sort.Strings(files)
			list = append(list, cleanupGroup{Ext: ext, Files: files})
		}
		sort.Slice(list, func(i, j int) bool { return list[i].Ext < list[j].Ext })
		a.post(func() {
			a.cleanupItems = list
			a.cleanupScanned = true
		})
	}()
}

func (a *app) cleanupSelectedCount() int {
	n := 0
	for _, g := range a.cleanupItems {
		if g.Sel {
			n += len(g.Files)
		}
	}
	return n
}

// deleteCleanupFiles 删除勾选格式的全部文件（默认进回收站）。
func (a *app) deleteCleanupFiles() {
	var targets []string
	for _, g := range a.cleanupItems {
		if g.Sel {
			targets = append(targets, g.Files...)
		}
	}
	if len(targets) == 0 {
		return
	}
	a.cleanupConfirm = false
	go func() {
		trashed, hard := 0, 0
		if err := recycleFiles(targets); err == nil {
			trashed = len(targets)
		} else {
			for _, f := range targets {
				if os.Remove(f) == nil {
					hard++
				}
			}
		}
		a.post(func() {
			switch {
			case trashed > 0:
				a.toast = fmt.Sprintf("已把 %d 个歌词文件移入回收站", trashed)
			case hard > 0:
				a.toast = fmt.Sprintf("已删除 %d 个歌词文件（回收站不可用，直接删除）", hard)
			default:
				a.toast = "没有删除任何文件"
			}
			a.cleanupConfirm = false
			a.scanCleanup()
		})
	}()
}

// ---------- 回收站 ----------

var (
	shell32            = syscall.NewLazyDLL("shell32.dll")
	procSHFileOperation = shell32.NewProc("SHFileOperationW")
	user32              = syscall.NewLazyDLL("user32.dll")
	procMessageBoxW     = user32.NewProc("MessageBoxW")
)

type shFileOpStruct struct {
	hwnd                  uintptr
	wFunc                 uint32
	pFrom                 *uint16
	pTo                   *uint16
	fFlags                uint16
	fAnyOperationsAborted int32
	hNameMappings         uintptr
	lpszProgressTitle     *uint16
}

const (
	foDelete      = 0x0003
	fofSilent     = 0x0004
	fofNoConfirm  = 0x0010
	fofAllowUndo  = 0x0040
	fofNoErrorUI  = 0x1000
)

// recycleFiles 用 SHFileOperation 把文件丢进回收站。
// 路径列表是「每个路径以 \0 结尾、整体再补一个 \0」，所以不能走 UTF16PtrFromString
// （它会因为串里有 NUL 而报 invalid argument），得自己拼 []uint16。
func recycleFiles(paths []string) error {
	var buf []uint16
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			continue
		}
		u, err := syscall.UTF16FromString(abs)   // 末尾自带一个 0
		if err != nil {
			continue
		}
		buf = append(buf, u...)
	}
	if len(buf) == 0 {
		return fmt.Errorf("没有可删除的路径")
	}
	buf = append(buf, 0)                          // 列表结束的第二个 0

	op := shFileOpStruct{
		wFunc:  foDelete,
		pFrom:  &buf[0],
		fFlags: fofAllowUndo | fofNoConfirm | fofSilent | fofNoErrorUI,
	}
	ret, _, _ := procSHFileOperation.Call(uintptr(unsafe.Pointer(&op)))
	if ret != 0 {
		return fmt.Errorf("SHFileOperation 返回 %d", ret)
	}
	if op.fAnyOperationsAborted != 0 {
		return fmt.Errorf("操作被取消")
	}
	return nil
}

// confirmDialog 系统消息框，返回是否点了「是」。
func confirmDialog(title, text string) bool {
	const mbYesNo, mbIconQuestion, mbTopMost = 0x00000004, 0x00000020, 0x00040000
	t, err1 := syscall.UTF16PtrFromString(text)
	c, err2 := syscall.UTF16PtrFromString(title)
	if err1 != nil || err2 != nil {
		return false
	}
	ret, _, _ := procMessageBoxW.Call(0, uintptr(unsafe.Pointer(t)), uintptr(unsafe.Pointer(c)),
		mbYesNo|mbIconQuestion|mbTopMost)
	return ret == 6 // IDYES
}

// ---------- 页面 ----------

func (a *app) viewCleanup(c *ui.Context) {
	p := a.pal
	col := ui.Column(c).FillWidth().Padding(pageGap, 38, 108, 38).Gap(16)
	col.Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(12).Children(func() {
			if pillButton(c.Key("cleanup-back"), iconPrev, "返回工具箱", p.surface2, p.text).Clicked() {
				a.navigate(pageTools, 0)
			}
			ui.Column(c).Gap(2).Children(func() {
				ui.Text(c, "清理歌词文件").FontSize(26).FontWeight(800)
				ui.Text(c, a.cleanupDir).FontSize(12).TextColor(p.muted).SingleLine()
			})
			ui.Box(c).Grow(1)
			if pillButton(c.Key("cleanup-rescan"), nil, "重新扫描", p.surface2, p.text).Clicked() {
				a.scanCleanup()
			}
			if pillButton(c.Key("cleanup-pick"), iconFolder, "换目录", p.surface2, p.text).Clicked() {
				go func() {
					dir, err := chooseFolder(a.win, "选择要清理歌词的目录", "")
					if err != nil || dir == "" {
						return
					}
					a.post(func() { a.openCleanup(dir) })
				}()
			}
		})

		if !a.cleanupScanned {
			ui.Text(c, "正在扫描目录下的歌词文件…").FontSize(13).TextColor(p.muted)
			return
		}
		if len(a.cleanupItems) == 0 {
			ui.Text(c, "这个目录里没有找到歌词文件（.lrc / .vtt / .ttml / .srt / .ass / .txt）。").
				FontSize(13).TextColor(p.muted)
			return
		}

		card := ui.Column(c.Key("cleanup-list")).FillWidth().Padding(18, 20, 16, 20).Gap(4).Radius(16).Background(p.surface)
		card.Draw(func(pt *ui.Painter, r ui.Rect) { pt.Stroke(r, p.hairline, 16, 1) })
		card.Children(func() {
			ui.Text(c, "勾选要删除的歌词格式").FontSize(12).FontWeight(700).TextColor(p.muted).Margin(0, 0, 6, 0)
			for i := range a.cleanupItems {
				g := &a.cleanupItems[i]
				rowKey := "cleanup-" + g.Ext
				row := ui.Row(c.Key(rowKey)).FillWidth().Height(46).PaddingX(8).Gap(12).Radius(10).AlignItems(ui.Center)
				if row.Hovered() {
					row.Background(p.hover)
				}
				row.Children(func() {
					box := ui.Box(c.Key(rowKey+"-box")).Size(18, 18).Radius(5).Center().PassThrough()
					bg := p.bg
					if g.Sel {
						bg = p.accent
					}
					box.Background(bg)
					box.Draw(func(pt *ui.Painter, r ui.Rect) {
						pt.Stroke(r, p.hairline, 5, 1)
						if g.Sel {
							pt.Fill(r, p.accent, 5)
							// 勾：两条短线
							pt.Fill(ui.Rect{X: r.X + r.W*0.24, Y: r.Y + r.H*0.52, W: r.W*0.2, H: 2}, p.onAccent, 1)
							pt.Fill(ui.Rect{X: r.X + r.W*0.40, Y: r.Y + r.H*0.40, W: r.W*0.38, H: 2}, p.onAccent, 1)
						}
					})
					ui.Text(c, g.Ext).Width(70).Shrink(0).FontSize(13).FontWeight(700).PassThrough()
					ui.Text(c, fmt.Sprintf("%d 个文件", len(g.Files))).Width(110).Shrink(0).
						FontSize(12.5).TextColor(p.muted).PassThrough()
					sample := g.Files[0]
					if len(g.Files) > 1 {
						sample = filepath.Base(g.Files[0]) + " …"
					} else {
						sample = filepath.Base(g.Files[0])
					}
					ui.Text(c, sample).Grow(1).MinWidth(0).FontSize(12).TextColor(p.faint).SingleLine().PassThrough()
				})
				a.traceRect("cleanup-row-"+g.Ext, row)
				a.tracePointer("cleanup-row-"+g.Ext, row)
				if row.Clicked() {
					g.Sel = !g.Sel
					a.cleanupConfirm = false
					if a.win != nil {
						a.win.Invalidate()
					}
				}
			}
		})

		selCount := a.cleanupSelectedCount()
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(12).Children(func() {
			ui.Text(c, fmt.Sprintf("已选 %d 个文件", selCount)).FontSize(12.5).TextColor(p.muted)
		})

		if selCount > 0 {
			// 整条可点：第一下变成确认态，第二下真删。
			// （用 Row 而不是 pillButton —— 这套布局里 Row 的点击才稳定。）
			bg, fg := p.surface2, p.text
			label := fmt.Sprintf("删除这 %d 个文件", selCount)
			if a.cleanupConfirm {
				bg, fg = p.accent, p.onAccent
				label = fmt.Sprintf("确认删除 %d 个？点了就没了", selCount)
			}
			bar := ui.Row(c.Key("cleanup-do")).FillWidth().Height(52).PaddingX(18).Gap(10).
				Radius(14).AlignItems(ui.Center).Background(bg).Children(func() {
				ui.Icon(c, iconTrash).Size(16, 16).TextColor(fg)
				ui.Text(c, label).FontSize(13.5).FontWeight(700).TextColor(fg)
			})
			a.traceRect("cleanup-do", bar)
			if bar.Clicked() {
				if a.cleanupConfirm {
					a.deleteCleanupFiles()
				} else {
					a.cleanupConfirm = true
				}
				if a.win != nil {
					a.win.Invalidate()
				}
			}
		}
		ui.Text(c, "删除先进回收站，回收站不可用时才会直接删除。歌词可以重新下载，但删错了要去回收站翻。").
			FontSize(11.5).TextColor(p.faint)
	})
}
