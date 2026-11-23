package app

import (
	"errors"
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
// 默认只走回收站（SHFileOperation + FOF_ALLOWUNDO）：回收站报错或用户取消了
// 系统对话框，没处理的文件就原样留着，绝不偷偷退回 os.Remove——用户点的是
// 「删除」，预期是「能捡回来」。真要彻底删除，得单独点「永久删除」再确认一次。

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
	a.cleanupHardConfirm = false
	a.cleanupResult = ""
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

// cleanupTargets 勾选的全部文件路径。
func (a *app) cleanupTargets() []string {
	var targets []string
	for _, g := range a.cleanupItems {
		if g.Sel {
			targets = append(targets, g.Files...)
		}
	}
	return targets
}

// recycleFilesFn 便于测试替换（真调 SHFileOperation 会把文件丢进回收站）。
var recycleFilesFn = recycleFiles

// runCleanup 真正干活的一步：permanent=false 只走回收站，绝不退回直接删除。
// 返回（处理掉的、没动的、失败原因）。
func (a *app) runCleanup(permanent bool, targets []string) (done, left []string, reason string) {
	if permanent {
		for _, f := range targets {
			if err := os.Remove(f); err == nil {
				done = append(done, f)
			} else {
				left = append(left, f)
				if reason == "" {
					reason = err.Error()
				}
			}
		}
		return done, left, reason
	}

	var err error
	done, err = recycleFilesFn(targets)
	if err != nil {
		reason = err.Error()
	}
	moved := make(map[string]bool, len(done))
	for _, f := range done {
		moved[f] = true
	}
	for _, f := range targets {
		if !moved[f] {
			left = append(left, f) // 还在原地：没进回收站就不算处理过
		}
	}
	return done, left, reason
}

// deleteCleanupFiles 处理勾选的歌词文件。
//
// permanent=false 只走回收站：回收站报错、或者系统对话框被取消时，没处理的
// 文件原样保留，并在界面上如实报结果。绝不因为「回收站失败」就改删文件。
func (a *app) deleteCleanupFiles(permanent bool) {
	targets := a.cleanupTargets()
	if len(targets) == 0 {
		return
	}
	a.cleanupConfirm = false
	a.cleanupHardConfirm = false
	go func() {
		done, left, reason := a.runCleanup(permanent, targets)
		summary := summarizeCleanup(permanent, done, left, reason)
		a.post(func() {
			a.cleanupResult = summary
			a.toast = summary
			a.scanCleanup()
			// 歌词被删掉了，「有词 / 无词 / 逐字就绪」都会变，曲库跟着重扫一遍
			if len(a.libFolders) > 0 {
				a.scanLibraryAsync()
			}
		})
	}()
}

// summarizeCleanup 把「成了几个 / 剩几个、为什么」写成一句话。
func summarizeCleanup(permanent bool, done, left []string, reason string) string {
	verb := "移入回收站"
	if permanent {
		verb = "永久删除"
	}
	parts := make([]string, 0, 2)
	if len(done) > 0 {
		parts = append(parts, fmt.Sprintf("已%s %d 个", verb, len(done)))
	}
	if len(left) > 0 {
		word := "没动"
		if permanent {
			word = "删除失败"
		}
		item := fmt.Sprintf("%d 个%s", len(left), word)
		if reason != "" {
			item += "（" + reason + "）"
		}
		parts = append(parts, item)
	}
	if len(parts) == 0 {
		return "没有处理任何文件"
	}
	return strings.Join(parts, "，") + "。"
}

// ---------- 回收站 ----------

var (
	shell32             = syscall.NewLazyDLL("shell32.dll")
	procSHFileOperation = shell32.NewProc("SHFileOperationW")
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
	foDelete     = 0x0003
	fofSilent    = 0x0004
	fofNoConfirm = 0x0010
	fofAllowUndo = 0x0040
	fofNoErrorUI = 0x1000
)

// recycleBatch 一批交给 SHFileOperation 的文件数：太多会让 pFrom 串很长。
const recycleBatch = 200

// shellDelete 真正调 SHFileOperation；抽成变量是为了能在测试里替换
// （真调一次就会真的把文件丢进回收站）。
var shellDelete = func(op *shFileOpStruct) uintptr {
	ret, _, _ := procSHFileOperation.Call(uintptr(unsafe.Pointer(op)))
	return ret
}

// recycleFiles 用 SHFileOperation 把文件丢进回收站，返回真正进站的那些。
//
// 不要拿返回值当「整批都成了」：用户取消时前面的可能已经进回收站了，
// 所以处理完按「文件还在不在」逐个核对，调用方据此保留没处理的文件。
// 一旦用户在系统对话框里按了取消，后面的批次立刻停下——不然会一个接一个
// 地弹窗，等于逼着他再取消 N 次。
//
// 路径列表是「每个路径以 \0 结尾、整体再补一个 \0」，所以不能走 UTF16PtrFromString
// （它会因为串里有 NUL 而报 invalid argument），得自己拼 []uint16。
func recycleFiles(paths []string) ([]string, error) {
	var moved []string
	var firstErr error
	for start := 0; start < len(paths); start += recycleBatch {
		end := min(start+recycleBatch, len(paths))
		batch := paths[start:end]

		buf := make([]uint16, 0, len(batch)*64)
		for _, p := range batch {
			abs, err := filepath.Abs(p)
			if err != nil {
				continue
			}
			u, err := syscall.UTF16FromString(abs) // 末尾自带一个 0
			if err != nil {
				continue
			}
			buf = append(buf, u...)
		}
		if len(buf) == 0 {
			continue
		}
		buf = append(buf, 0) // 列表结束的第二个 0

		// 先记下哪些本来就在：事后「不见了」才算进站，别把不存在的路径算成处理过
		existed := make(map[string]bool, len(batch))
		for _, p := range batch {
			if _, err := os.Lstat(p); err == nil {
				existed[p] = true
			}
		}

		op := shFileOpStruct{
			wFunc:  foDelete,
			pFrom:  &buf[0],
			fFlags: fofAllowUndo | fofNoConfirm | fofSilent | fofNoErrorUI,
		}
		ret := shellDelete(&op)
		aborted := op.fAnyOperationsAborted != 0
		switch {
		case ret != 0:
			if firstErr == nil {
				firstErr = fmt.Errorf("回收站操作返回 %d", ret)
			}
		case aborted:
			if firstErr == nil {
				firstErr = fmt.Errorf("操作被取消")
			}
		}
		for _, p := range batch {
			if !existed[p] {
				continue
			}
			if _, err := os.Lstat(p); errors.Is(err, os.ErrNotExist) {
				moved = append(moved, p)
			}
		}
		if aborted {
			break // 用户取消了，别再弹后面的批次
		}
	}
	return moved, firstErr
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
							pt.Fill(ui.Rect{X: r.X + r.W*0.24, Y: r.Y + r.H*0.52, W: r.W * 0.2, H: 2}, p.onAccent, 1)
							pt.Fill(ui.Rect{X: r.X + r.W*0.40, Y: r.Y + r.H*0.40, W: r.W * 0.38, H: 2}, p.onAccent, 1)
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
			label := fmt.Sprintf("删除这 %d 个文件（移入回收站）", selCount)
			if a.cleanupConfirm {
				bg, fg = p.accent, p.onAccent
				label = fmt.Sprintf("确认移入回收站：%d 个？", selCount)
			}
			bar := ui.Row(c.Key("cleanup-do")).FillWidth().Height(52).PaddingX(18).Gap(10).
				Radius(14).AlignItems(ui.Center).Background(bg).Children(func() {
				ui.Icon(c, iconTrash).Size(16, 16).TextColor(fg)
				ui.Text(c, label).FontSize(13.5).FontWeight(700).TextColor(fg)
			})
			a.traceRect("cleanup-do", bar)
			if bar.Clicked() {
				if a.cleanupConfirm {
					a.deleteCleanupFiles(false)
				} else {
					a.cleanupConfirm = true
					a.cleanupHardConfirm = false
				}
				if a.win != nil {
					a.win.Invalidate()
				}
			}

			// 永久删除单独一个入口：颜色更淡、位置更靠下、确认文案更明确。
			hardBg, hardFg := ui.Transparent, p.err
			hardLabel := fmt.Sprintf("永久删除这 %d 个文件（不进回收站，删了就没了）", selCount)
			if a.cleanupHardConfirm {
				hardBg, hardFg = p.errBg, p.err
				hardLabel = fmt.Sprintf("确认永久删除 %d 个？不可恢复", selCount)
			}
			hard := ui.Row(c.Key("cleanup-do-hard")).FillWidth().Height(40).PaddingX(18).Gap(8).
				Radius(12).AlignItems(ui.Center).Background(hardBg).Children(func() {
				ui.Text(c, hardLabel).FontSize(12).FontWeight(600).TextColor(hardFg)
			})
			hard.Draw(func(pt *ui.Painter, r ui.Rect) { pt.Stroke(r, p.hairline, 12, 1) })
			a.traceRect("cleanup-do-hard", hard)
			if hard.Clicked() {
				if a.cleanupHardConfirm {
					a.deleteCleanupFiles(true)
				} else {
					a.cleanupHardConfirm = true
					a.cleanupConfirm = false
				}
				if a.win != nil {
					a.win.Invalidate()
				}
			}
		}

		if a.cleanupResult != "" {
			ui.Text(c, a.cleanupResult).FontSize(12.5).FontWeight(600).TextColor(p.muted)
		}
		ui.Text(c, "删除一律先进回收站；回收站报错或系统对话框被取消时，没处理的文件会原样留着，不会改成直接删除。").
			FontSize(11.5).TextColor(p.faint)
	})
}
