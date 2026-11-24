package app

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

const sidebarWidth = 232

func (a *app) pageMotion(page ui.Element) {
	in := &ui.Motion{Y: 12}
	switch {
	case a.pageDir > 0:
		in = &ui.Motion{X: 32}
	case a.pageDir < 0:
		in = &ui.Motion{X: -24}
	}
	page.Transition(ui.ElementTransition{Animation: &motionPage, Enter: in})
}

func (a *app) navRowContent(c *ui.Context, button ui.Element, selected bool, padding float32, content func()) {
	p := a.pal
	button.AlignSelf(ui.Stretch).Children(func() {
		if selected {
			alpha := float32(0.13)
			if p.dark {
				alpha = 0.18
			}
			pill := ui.Box(c.Key("nav-pill")).Absolute().Top(0).Left(0).Right(0).Bottom(0).Radius(9).PassThrough().
				Background(p.accent.Alpha(alpha))
			pill.Draw(func(pt *ui.Painter, r ui.Rect) {
				bar := ui.Rect{X: r.X + 3, Y: r.Y + (r.H-16)/2, W: 3.5, H: 16}
				pt.Fill(bar, p.accent, 2)
			})
			pill.Transition(ui.ElementTransition{Animation: &motionPane, Match: navPillMatch{}})
		}
		ui.Row(c.Key("nav-content")).Absolute().Top(0).Left(0).Right(0).Bottom(0).
			PaddingX(padding).Gap(10).AlignItems(ui.Center).PassThrough().Children(content)
	})
}

func (a *app) view(c *ui.Context) {
	t := *c.Theme()
	if a.appearance == 1 {
		t.Dark = true
	} else if a.appearance == 0 {
		t.Dark = false
	}
	a.pal = paletteFor(t.Dark)
	if a.accentHex != "" {
		a.pal = themeAccent(a.pal, a.accentHex)
	}
	p := a.pal
	t.Accent, t.AccentText = p.accent, p.onAccent
	t.Selection = p.accent.Alpha(0.28)
	c.SetTheme(&t)

	root := ui.Row(c).Fill().AlignItems(ui.Stretch).TextColor(p.text)
	rootBg := root.AnimateColor("root-bg", p.bg, ui.Animation{Duration: 450 * time.Millisecond})
	root.Background(rootBg)

	root.Children(func() {
		a.viewSidebar(c)
		main := ui.Box(c.Key("main")).Grow(1).MinWidth(0).MinHeight(0)
		mainBg := main.AnimateColor("main-bg", p.bg, ui.Animation{Duration: 450 * time.Millisecond})
		main.Background(mainBg)
		main.Children(func() {
			// Keyed by a.page so Cloudy's pageMotion animates every page transition smoothly.
			// Non-editor scrollable pages start below the 46px titlebar strip so scrolled content never overlaps _ □ ×.
			pageTop := float32(buttonsHeight)
			if a.page == pageEditor && a.edFile != "" && len(a.edLines) > 0 {
				pageTop = 0
			}
			page := ui.Box(c.Key(a.page)).Absolute().Top(pageTop).Left(0).Right(0).Bottom(0)
			a.pageMotion(page)
			page.Children(func() {
				switch a.page {
				case pageLibrary:
					a.viewLibrary(c)
				case pageAlign:
					a.viewAlign(c)
				case pageQueue:
					a.viewQueue(c)
				case pageEditor:
					a.viewEditor(c)
				case pageTools:
					a.viewTools(c)
				case pageSeparate:
					a.viewSeparate(c)
				case pageCleanup:
					a.viewCleanup(c)
				case pageScrape:
					a.viewScrape(c)
				}
			})

			if !a.settingsOpen && a.envChecked && !a.envInfo.Ready {
				banner := ui.Row(c.Key("env-setup-banner")).Absolute().Top(buttonsHeight + 4).Left(38).Right(38).Height(44).
					PaddingX(16).Radius(12).Gap(10).AlignItems(ui.Center).Background(p.warnBg)
				banner.Draw(func(pt *ui.Painter, r ui.Rect) { pt.Stroke(r, p.warn.Alpha(0.35), 12, 1) })
				banner.Children(func() {
					msg := "检测到 Python 运行环境尚未就绪，对齐功能暂不可用"
					if a.envInstalling {
						msg = "正在后台初始化运行环境 / 下载模型，可前往设置页查看实时进度"
					} else if len(a.envInfo.Missing) > 0 {
						msg = fmt.Sprintf("检测到当前 Python 缺少 %d 个核心依赖（%s）", len(a.envInfo.Missing), strings.Join(a.envInfo.Missing[:min(4, len(a.envInfo.Missing))], "、"))
					}
					ui.Text(c, msg).FontSize(12.5).FontWeight(600).TextColor(p.warn).Grow(1).SingleLine()
					btnTxt := "前往一键安装"
					if a.envInstalling {
						btnTxt = "查看进度"
					}
					if pillButton(c.Key("env-banner-go"), nil, btnTxt, p.accent, p.onAccent).Clicked() {
						a.openSettings(settingsRuntime)
					}
				})
			}

			if (a.page == pageLibrary || a.page == pageAlign || a.page == pageQueue || a.page == pageTools) &&
				(a.edFile != "" || a.queueActiveTitle != "") {
				a.viewGlassDock(c)
			}

			// Top drag bar on non-editor pages (Editor's own top toolbar already has DragWindow on empty space)
			if a.page != pageEditor || a.edFile == "" || len(a.edLines) == 0 {
				ui.Box(c.Key("top-drag")).Absolute().Top(0).Left(0).Right(0).Height(buttonsHeight).DragWindow()
			}

			// Top-right theme toggle button (aligned with the 46px native Windows caption bar)
			ui.Row(c.Key("top-theme")).Absolute().Top(0).Right(c.TitleBar().Right + 10).Height(buttonsHeight).AlignItems(ui.Center).Children(func() {
				if iconButton(c.Key("theme-toggle"), iconSun, 15, p.muted, "切换深浅色").Clicked() {
					if a.appearance == 1 {
						a.appearance = 0
					} else {
						a.appearance = 1
					}
				}
			})

			// Global floating toast banner (renders any a.toast across all pages, auto-dismisses after 4s)
			if a.toast != a.toastLast {
				a.toastLast = a.toast
				a.toastSeq++
				if a.toast != "" && a.win != nil {
					seq := a.toastSeq
					time.AfterFunc(4500*time.Millisecond, func() {
						a.post(func() {
							if a.toastSeq == seq {
								a.toast = ""
								a.toastLast = ""
							}
						})
					})
				}
			}
			if a.toast != "" && !a.settingsOpen {
				wrap := ui.Row(c.Key("global-toast-wrap")).Absolute().Top(54).Left(0).Right(0).AlignItems(ui.Center).PassThrough()
				wrap.Transition(ui.ElementTransition{Animation: &motionToast,
					Enter: &ui.Motion{Y: -8, Opacity: 0, Scale: 0.97}})
				wrap.Children(func() {
					ui.Box(c).Grow(1).PassThrough()
					pill := ui.Row(c.Key("global-toast")).Padding(9, 14, 9, 14).Gap(10).Radius(12).AlignItems(ui.Center).Background(p.raised)
					pill.Draw(func(pt *ui.Painter, r ui.Rect) {
						pt.Shadow(r, 12, 0, 6, 18, 0, p.cardShade)
						pt.Stroke(r, p.accent.Alpha(0.35), 12, 1)
					})
					pill.Children(func() {
						ui.Box(c).Size(7, 7).Radius(4).Background(p.accent).Shrink(0)
						ui.Text(c, a.toast).FontSize(12.5).FontWeight(600).TextColor(p.text).SingleLine()
						if !a.settingsOpen && strings.Contains(a.toast, "设置") {
							goBtn := ui.ButtonBase(c.Key("toast-go-settings")).Height(24).PaddingX(10).Radius(999).Center().
								Background(p.accent).Label("去设置").FocusRing(false)
							goBtn.Children(func() {
								ui.Text(c, "去设置").FontSize(11.5).FontWeight(700).TextColor(p.onAccent).SingleLine()
							})
							if goBtn.Clicked() {
								a.toast = ""
								a.toastLast = ""
								a.openSettings(settingsTranslation)
							}
						}
						closeBtn := ui.ButtonBase(c.Key("toast-close")).Size(20, 20).Radius(999).Center().
							Label("关闭提示").FocusRing(false)
						if closeBtn.Hovered() {
							closeBtn.Background(p.hover)
						}
						closeBtn.Children(func() {
							ui.Icon(c, iconX).Size(12, 12).TextColor(p.muted)
						})
						if closeBtn.Clicked() {
							a.toast = ""
							a.toastLast = ""
						}
					})
					ui.Box(c).Grow(1).PassThrough()
				})
			}
		})
	})
	a.viewLibraryNoLyricsDialog(c)
	a.viewToolOutputDialog(c)
	a.viewSettings(c)
	a.viewContextMenu(c)
}

func (a *app) viewSidebar(c *ui.Context) {
	p := a.pal
	sw := a.sideWidthPx
	if sw < 200 {
		sw = sidebarWidth
	}
	side := ui.Column(c.Key("sidebar")).Width(sw).Shrink(0).Padding(16, 12, 14, 12).Gap(3)
	sideBg := side.AnimateColor("side-bg", p.sidebar, ui.Animation{Duration: 450 * time.Millisecond})
	side.Background(sideBg)
	side.Draw(func(pt *ui.Painter, r ui.Rect) {
		pt.Fill(ui.Rect{X: r.X + r.W - 1, Y: r.Y, W: 1, H: r.H}, p.hairline, 0)
	})
	side.Children(func() {
		// Brand header
		ui.Row(c.Key("brand")).Height(36).Padding(0, 8, 10, 8).Gap(10).AlignItems(ui.Center).DragWindow().Children(func() {
			mark := ui.Box(c).Size(28, 28).Radius(8).Center().Background(p.accent)
			mark.Children(func() {
				ui.Icon(c, iconNote).Size(15, 15).TextColor(p.onAccent)
			})
			ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "Kiralrc").FontSize(15.5).FontWeight(800).SingleLine()
				beta := ui.Box(c).Height(16).PaddingX(4).Margin(0, 0, 6, 0).Radius(5).Center().
					Background(p.accent.Alpha(0.08)).Border(1, p.accent.Alpha(0.30))
				beta.Children(func() {
					ui.Text(c, "BETA").FontSize(10).FontWeight(600).TextColor(p.accent).SingleLine()
				})
			})
			ui.Box(c).Grow(1)
			ui.Text(c, "v"+appVersion).FontSize(8).FontWeight(700).TextColor(p.muted).SingleLine()
		})

		// Real live search input (filters tracks in 曲库)
		sbox := ui.Row(c.Key("side-search")).Height(34).PaddingX(10).Margin(0, 0, 12, 0).Gap(8).Radius(10).
			AlignItems(ui.Center).Background(p.surface)
		sbox.Draw(func(pt *ui.Painter, r ui.Rect) {
			pt.Stroke(r, p.hairline, 10, 1)
		})
		sbox.Children(func() {
			ui.Icon(c, iconSearch).Size(14, 14).TextColor(p.faint)
			inp := ui.TextInputBase(c.Key("side-search-in"), &a.libQuery).Grow(1).MinWidth(0).Height(28).
				FontSize(12.5).TextColor(p.text).Background(ui.Transparent).Placeholder("搜索曲库歌曲 / 歌手").FocusRing(false)
			if inp.Changed() {
				a.libPage = 0 // 搜索词一变就回第一页
				a.libRowMenuPath = ""
				if a.libQuery != "" && a.page != pageLibrary {
					a.navigate(pageLibrary, -1)
				}
			}
		})

		queueBadge := ""
		totalQueue := len(a.queueItems)
		if totalQueue > 0 {
			queueBadge = fmt.Sprintf("%d", totalQueue)
		}
		navItems := []struct {
			kind  pageKind
			label string
			icon  *ui.SVG
			badge string
		}{
			{pageLibrary, "曲库", iconLibrary, ""},
			{pageAlign, "新建对齐", iconPlus, ""},
			{pageQueue, "任务队列", iconQueue, queueBadge},
			{pageEditor, "编辑与校对", iconEdit, ""},
			{pageTools, "工具箱", iconTools, ""},
		}
		for _, item := range navItems {
			it := item
			sel := a.page == it.kind
			row := ui.ButtonBase(c.Key("nav-" + it.label)).Height(36).Radius(9).Label(it.label).Selected(sel).FocusRing(false)
			bg := ui.Transparent
			if row.Hovered() && !sel {
				bg = p.hover
			}
			row.Background(row.AnimateColor("bg", bg, ui.Animation{Duration: 120 * time.Millisecond}))
			pose := ui.Pose{}
			if row.Pressed() {
				pose.Scale = 0.97
			}
			row.AnimatePose(pose, ui.Snappy)

			icCol, txCol, weight := p.muted, p.muted, 600
			if sel {
				icCol, txCol, weight = p.accent, p.text, 700
			} else if row.Hovered() {
				icCol, txCol = p.text, p.text
			}
			a.navRowContent(c, row, sel, 12, func() {
				ui.Icon(c, it.icon).Size(16, 16).TextColor(icCol)
				ui.Text(c, it.label).FontSize(13.5).FontWeight(weight).TextColor(txCol).SingleLine()
				if it.badge != "" {
					ui.Box(c).Grow(1)
					b := ui.Box(c).Height(18).PaddingX(6).Radius(999).Center().Background(p.accent)
					b.Children(func() {
						ui.Text(c, it.badge).FontSize(11).FontWeight(700).TextColor(p.onAccent).SingleLine()
					})
				}
			})
			if row.Clicked() {
				a.navigate(it.kind, 0)
			}
		}

		ui.Row(c).FillWidth().PaddingX(10).Margin(16, 0, 4, 0).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "最近校对").FontSize(11).FontWeight(700).TextColor(p.faint).Grow(1)
			if len(a.recents) > 0 {
				btn := ui.ButtonBase(c.Key("clear-editor-recents")).Height(22).PaddingX(6).Radius(6).
					Label("清除最近校对记录").FocusRing(false).Center()
				if btn.Hovered() {
					btn.Background(p.hover)
				}
				btn.Children(func() {
					ui.Text(c, "清除记录").FontSize(11).TextColor(p.muted).SingleLine()
				})
				if btn.Clicked() {
					a.recents = nil
					a.savePrefs()
					a.toast = "已清除最近校对记录"
				}
			}
		})
		if len(a.recents) == 0 {
			ui.Text(c, "暂无校对记录").FontSize(12).TextColor(p.faint).Margin(2, 10, 0, 10)
		} else {
			for _, rc := range a.recents {
				r := rc
				activeRec := a.page == pageEditor && a.edFile == r.File
				btn := ui.ButtonBase(c.Key("rec-" + r.Path)).FillWidth().Height(33).PaddingX(10).Gap(9).Radius(8).
					AlignItems(ui.Center).Justify(ui.Start).Label(r.Name).Selected(activeRec).FocusRing(false)
				bg := ui.Transparent
				if activeRec {
					bg = p.accent.Alpha(0.11)
				} else if btn.Hovered() {
					bg = p.hover
				}
				btn.Background(btn.AnimateColor("bg", bg, ui.Animation{Duration: 120 * time.Millisecond}))
				pose := ui.Pose{}
				if btn.Pressed() {
					pose.Scale = 0.97
				}
				btn.AnimatePose(pose, ui.Snappy)
				txCol := p.muted
				if activeRec || btn.Hovered() {
					txCol = p.text
				}
				btn.Children(func() {
					if r.Cover != nil {
						ui.Image(c, r.Cover).Size(20, 20).Radius(5).Fit(ui.Cover).Shrink(0)
					} else {
						artCover(c, r.Hue, 20, 5, nil)
					}
					ui.Text(c, r.Name).FontSize(12.5).FontWeight(500).TextColor(txCol).SingleLine()
				})
				if btn.Clicked() {
					_ = a.loadLrcFile(r.Path)
					a.navigate(pageEditor, 1)
				}
			}
		}

		ui.Box(c).Grow(1)

		// Active job mini card (bottom-left) — only shown when a task is actually in the queue
		if a.queueActiveTitle != "" {
			nowCard := ui.ButtonBase(c.Key("side-now")).Padding(9, 10, 9, 10).Gap(10).Radius(12).
				AlignItems(ui.Center).Label("当前任务：" + a.queueActiveTitle).FocusRing(false)
			nowBg := p.surface
			if nowCard.Hovered() {
				nowBg = p.surface2
			}
			nowCard.Background(nowCard.AnimateColor("bg", nowBg, ui.Animation{Duration: 120 * time.Millisecond}))
			nowPose := ui.Pose{}
			if nowCard.Pressed() {
				nowPose.Scale = 0.97
			}
			nowCard.AnimatePose(nowPose, ui.Snappy)
			nowCard.Children(func() {
				artCover(c, hueForString(a.queueActiveTitle), 34, 9, iconMic)
				curIdx := 0
				runningCnt := 0
				doneCnt := 0
				for i, it := range a.queueItems {
					if it.Status == "running" {
						runningCnt++
						if it.Title == a.queueActiveTitle || curIdx == 0 {
							curIdx = i + 1
						}
					} else if it.Status == "done" || it.Status == "failed" || it.Status == "skipped" {
						doneCnt++
					}
				}
				if curIdx == 0 {
					curIdx = doneCnt + 1
				}
				if curIdx > totalQueue && totalQueue > 0 {
					curIdx = totalQueue
				}
				stageLabels := []string{"队列准备中", "定歌词文本", "定每句位置", "人声分离中", "主唱检测中", "逐字对齐中", "输出校验中"}
				stageTxt := stageLabels[0]
				if a.queueStage >= 1 && a.queueStage < len(stageLabels) {
					stageTxt = stageLabels[a.queueStage]
				}
				topLine := fmt.Sprintf("第 %d/%d 首 · %s", curIdx, totalQueue, a.queueActiveTitle)
				subLine := stageTxt
				if runningCnt > 1 {
					topLine = fmt.Sprintf("并发中（%d 首）· 共 %d 首", runningCnt, totalQueue)
					subLine = fmt.Sprintf("已完成 %d/%d · %s", doneCnt, totalQueue, stageTxt)
				}
				ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
					ui.Text(c, topLine).FontSize(12.5).FontWeight(700).TextColor(p.text).SingleLine()
					ui.Text(c, subLine).FontSize(11).TextColor(p.muted).SingleLine()
					bar := ui.Box(c).Height(3).Radius(2).Margin(3, 0, 0, 0).Background(p.surface2)
					bar.Draw(func(pt *ui.Painter, r ui.Rect) {
						pt.Fill(ui.Rect{X: r.X, Y: r.Y, W: r.W * a.queuePct, H: r.H}, p.accent, 2)
					})
				})
			})
			if nowCard.Clicked() {
				a.navigate(pageQueue, 0)
			}
		}

		// Settings nav item (shares navPillMatch so the selection pill glides down to Settings too!)
		setSel := a.settingsOpen
		setBtn := ui.ButtonBase(c.Key("nav-设置")).Height(36).Margin(4, 0, 0, 0).Radius(9).
			Label("设置").Selected(setSel).FocusRing(false)
		setBg := ui.Transparent
		if setBtn.Hovered() && !setSel {
			setBg = p.hover
		}
		setBtn.Background(setBtn.AnimateColor("bg", setBg, ui.Animation{Duration: 120 * time.Millisecond}))
		setPose := ui.Pose{}
		if setBtn.Pressed() {
			setPose.Scale = 0.97
		}
		setBtn.AnimatePose(setPose, ui.Snappy)
		icCol, txCol, weight := p.muted, p.muted, 600
		if setSel {
			icCol, txCol, weight = p.accent, p.text, 700
		} else if setBtn.Hovered() {
			icCol, txCol = p.text, p.text
		}
		a.navRowContent(c, setBtn, setSel, 12, func() {
			ui.Icon(c, iconSettings).Size(16, 16).TextColor(icCol)
			ui.Text(c, "设置").FontSize(13.5).FontWeight(weight).TextColor(txCol).SingleLine()
		})
		if setBtn.Clicked() {
			a.openSettings(a.settingsSection)
		}

		// Draggable right edge of the sidebar (Cloudy sidebarResizer)
		edge := ui.Box(c.Key("sidebar-edge")).Absolute().Right(0).Top(0).Bottom(0).Width(6).
			Cursor(ui.CursorResizeEW).Label("调整侧边栏宽度")
		if edge.DoubleClicked() {
			a.sideWidthPx = sidebarWidth
		}
		if dx, _, ok := edge.Dragged(); ok && dx != 0 {
			a.sideWidthPx = min(max(sw+dx, 200), 360)
		}
		if edge.Hovered() {
			edge.Draw(func(pt *ui.Painter, r ui.Rect) {
				pt.Fill(ui.Rect{X: r.X + r.W/2 - 1, Y: r.Y, W: 2, H: r.H}, p.accent.Alpha(0.55), 1)
			})
		}
	})
}

// viewGlassDock renders Cloudy's floating liquid-glass capsule dock at the bottom.
func (a *app) viewGlassDock(c *ui.Context) {
	p := a.pal
	dock := ui.Row(c.Key("glass-dock")).Absolute().Left(28).Right(28).Bottom(16).Height(66).
		Padding(0, 16, 0, 12).Gap(16).Radius(999).AlignItems(ui.Center)
	dock.Transition(ui.ElementTransition{
		Animation: &motionPane,
		Enter:     &ui.Motion{Y: 20, Scale: 0.96},
		Exit:      &ui.Motion{Y: 20, Scale: 0.96},
	})
	g := barGlass{dark: p.dark, clear: 0.55, bend: 0.5, surface: true, translucent: true}
	dock.Draw(func(pt *ui.Painter, r ui.Rect) {
		pt.Shadow(r, 33, 0, 14, 34, -10, p.cardShade)
		g.PaintMaterial(pt, r, [4]float32{33, 33, 33, 33})
	})
	dockTitle := a.edFile
	dockSub := a.edSub
	if dockTitle == "" {
		dockTitle = a.queueActiveTitle
		dockSub = a.queueActiveSub
	}
	dock.Children(func() {
		// Left: spinning vinyl disc + song info (bounded so long titles truncate cleanly and never push right controls off-screen)
		songBtn := ui.ButtonBase(c.Key("dock-song")).Grow(1).MinWidth(0).Gap(11).AlignItems(ui.Center).Background(ui.Transparent).
			Label(dockTitle).FocusRing(false)
		pose := ui.Pose{}
		if songBtn.Pressed() {
			pose.Scale = 0.97
		}
		songBtn.AnimatePose(pose, ui.Snappy)
		songBtn.Children(func() {
			disc := ui.Box(c).Size(44, 44).Radius(22).Shrink(0)
			disc.Draw(func(pt *ui.Painter, r ui.Rect) {
				now := pt.Now()
				if a.edPlaying {
					if !a.spinLast.IsZero() {
						dt := float32(now.Sub(a.spinLast).Seconds())
						if dt > 0 && dt < 0.2 {
							a.spinAngle += dt * 1.4
						}
					}
					a.spinLast = now
					pt.After(16 * time.Millisecond)
				} else {
					a.spinLast = time.Time{}
				}
				pt.Fill(r, ui.Hex("#1a1a1f"), 22)
				pt.Stroke(ui.Rect{X: r.X + 5, Y: r.Y + 5, W: 34, H: 34}, ui.RGBA(255, 255, 255, 0.08), 17, 1)
				lbl := ui.Rect{X: r.X + 14, Y: r.Y + 14, W: 16, H: 16}
				pt.Fill(lbl, p.accent, 8)
				// Rotating highlight notch on the vinyl label
				cx, cy := r.X+22, r.Y+22
				nx := cx + float32(math.Cos(float64(a.spinAngle)))*5
				ny := cy + float32(math.Sin(float64(a.spinAngle)))*5
				pt.Fill(ui.Rect{X: nx - 1.5, Y: ny - 1.5, W: 3, H: 3}, white.Alpha(0.75), 1.5)
				hole := ui.Rect{X: r.X + 20, Y: r.Y + 20, W: 4, H: 4}
				pt.Fill(hole, white, 2)
			})
			ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
				ui.Text(c, dockTitle).FontSize(13).FontWeight(700).TextColor(p.text).SingleLine()
				ui.Text(c, dockSub).FontSize(11.5).TextColor(p.muted).SingleLine()
			})
		})
		if songBtn.Clicked() {
			a.navigate(pageEditor, 1)
		}

		// Center: transport controls + glowing progress bar
		ui.Column(c.Key("dock-center")).Width(340).Gap(4).AlignItems(ui.Center).Children(func() {
			ui.Row(c).Gap(14).AlignItems(ui.Center).Children(func() {
				if iconButton(c.Key("dock-prev"), iconPrev, 13, p.muted, "上一句").Clicked() && a.edSelectedLine > 0 {
					a.selectEditorLine(a.edSelectedLine - 1)
					a.seekPlayback(a.edLines[a.edSelectedLine].Start)
				}
				playBtn := ui.ButtonBase(c.Key("dock-play")).Size(34, 34).Radius(17).Center().
					Background(p.text).TextColor(p.bg).Label("播放/暂停").FocusRing(false)
				pPose := ui.Pose{}
				switch {
				case playBtn.Pressed():
					pPose.Scale = 0.90
				case playBtn.Hovered():
					pPose.Scale = 1.07
				}
				playBtn.AnimatePose(pPose, ui.Snappy)
				playBtn.Children(func() {
					ic := iconPlay
					if a.edPlaying {
						ic = iconPause
					}
					ui.Icon(c, ic).Size(14, 14)
				})
				if playBtn.Clicked() {
					a.togglePlayback()
				}
				if iconButton(c.Key("dock-next"), iconNext, 13, p.muted, "下一句").Clicked() && a.edSelectedLine+1 < len(a.edLines) {
					a.selectEditorLine(a.edSelectedLine + 1)
					a.seekPlayback(a.edLines[a.edSelectedLine].Start)
				}
			})
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				dur := max(a.dockDur, 1.0)
				curMin := int(a.dockPos) / 60
				curSec := int(a.dockPos) % 60
				ui.Text(c, fmt.Sprintf("%02d:%02d", curMin, curSec)).FontSize(10.5).TextColor(p.faint).SingleLine()
				scrub := ui.SliderBase(c.Key("dock-scrub"), &a.dockPos, 0, dur).Grow(1).Height(16).
					Cursor(ui.CursorPointer).Label("播放进度").FocusRing(false)
				if scrub.Changed() {
					a.seekPlayback(a.dockPos)
				}
				hot := scrub.Hovered() || scrub.Pressed()
				thick := scrub.AnimateTo("th", map[bool]float32{true: 6, false: 4}[hot], ui.Snappy)
				knob := scrub.AnimateTo("kn", b2f(hot), ui.Snappy)
				scrub.Draw(func(pt *ui.Painter, r ui.Rect) {
					curPos := a.dockPos
					if a.edPlaying && a.player != nil {
						if lp, ld := a.player.Position(); ld > 0 {
							curPos = lp
							dur = ld
						}
						pt.After(16 * time.Millisecond)
					}
					frac := clamp01(float32(curPos / dur))
					y := r.Y + (r.H-thick)/2
					pt.Fill(ui.Rect{X: r.X, Y: y, W: r.W, H: thick}, p.surface2, thick/2)
					pw := max(thick, r.W*frac)
					pt.Fill(ui.Rect{X: r.X, Y: y, W: pw, H: thick}, p.accent, thick/2)
					if knob > 0.01 {
						d := 10 * knob
						kx := r.X + r.W*frac - d/2
						ky := r.Y + (r.H-d)/2
						kRect := ui.Rect{X: kx, Y: ky, W: d, H: d}
						pt.Shadow(kRect, d/2, 0, 1, 4, 0, ui.RGBA(0, 0, 0, 0.3))
						pt.Fill(kRect, white, d/2)
					}
				})
				ui.Text(c, fmt.Sprintf("%02d:%02d", int(dur)/60, int(dur)%60)).FontSize(10.5).TextColor(p.faint).SingleLine()
			})
		})

		ui.Box(c).Grow(1)

		// Right: volume + status pill + enter editor button
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			a.volumeControl(c, "dock-vol")
			statusPill(c, "人声轨就绪", p.okBg, p.ok)
			if pillButton(c.Key("dock-open-ed"), nil, "进入校对", p.surface2, p.text).Clicked() {
				a.navigate(pageEditor, 1)
			}
		})
	})
}

func (a *app) volumeControl(c *ui.Context, keyPrefix string) {
	p := a.pal
	ui.Row(c.Key(keyPrefix)).Gap(6).AlignItems(ui.Center).Children(func() {
		ic := iconVolume
		label := "静音"
		if a.muted || a.volume <= 0.001 {
			ic = iconVolumeMute
			label = "取消静音"
		}
		if iconButton(c.Key(keyPrefix+"-btn"), ic, 15, p.muted, label).Clicked() {
			a.toggleMute()
		}
		vs := ui.SliderBase(c.Key(keyPrefix+"-sl"), &a.volume, 0, 1).Width(58).Height(18).
			Cursor(ui.CursorPointer).Label("音量").FocusRing(false)
		if vs.Changed() {
			a.setVolume(a.volume)
		}
		hot := vs.Hovered() || vs.Pressed()
		thick := vs.AnimateTo("th", map[bool]float32{true: 6, false: 4}[hot], ui.Snappy)
		knob := vs.AnimateTo("kn", b2f(hot), ui.Snappy)
		vs.Draw(func(pt *ui.Painter, r ui.Rect) {
			frac := clamp01(float32(a.volume))
			y := r.Y + (r.H-thick)/2
			pt.Fill(ui.Rect{X: r.X, Y: y, W: r.W, H: thick}, p.surface2, thick/2)
			if frac > 0.001 {
				pw := max(thick, r.W*frac)
				pt.Fill(ui.Rect{X: r.X, Y: y, W: pw, H: thick}, p.accent, thick/2)
			}
			if knob > 0.01 {
				d := 10 * knob
				kx := r.X + r.W*frac - d/2
				ky := r.Y + (r.H-d)/2
				kRect := ui.Rect{X: kx, Y: ky, W: d, H: d}
				pt.Shadow(kRect, d/2, 0, 1, 4, 0, ui.RGBA(0, 0, 0, 0.3))
				pt.Fill(kRect, white, d/2)
			}
		})
	})
}

// ==================== 1. 曲库 ====================
func (a *app) viewLibrary(c *ui.Context) {
	p := a.pal
	// 调试用：KIRALRC_SCROLL_TO=bottom 直接滚到底（滚动位置就是 App 的状态，
	// 不需要去伪造滚轮事件）
	if os.Getenv("KIRALRC_SCROLL_TO") == "bottom" {
		a.libScroll.Y = math.MaxFloat32
	}
	sc := ui.Scroll(c.Key("lib-scroll")).TrackScroll(&a.libScroll).Fill()
	sc.Children(func() {
		col := ui.Column(c).FillWidth().Padding(pageGap, 38, 108, 38).Gap(16)
		col.Children(func() {
			if len(a.libFolders) == 0 && !a.libScanning {
				a.viewLibraryEmpty(c)
				return
			}
			// 已有列表时，刷新只在标题旁提示一句，不要整页换成扫描卡片
			if a.libScanning && len(a.tracks) == 0 {
				ui.Column(c).Gap(2).Children(func() {
					ui.Text(c, strings.Join(a.libFolders, " · ")+" · 正在扫描…").FontSize(11.5).FontWeight(600).TextColor(p.muted)
					ui.Text(c, "曲库").FontSize(28).FontWeight(800)
				})
				card := ui.Column(c.Key("lib-scanning")).FillWidth().Padding(44, 32, 44, 32).Gap(10).Radius(20).
					AlignItems(ui.Center).Background(p.surface)
				card.Draw(func(pt *ui.Painter, r ui.Rect) { pt.Stroke(r, p.hairline, 20, 1) })
				card.Children(func() {
					ui.Text(c, "正在读取本地音乐文件夹…").FontSize(17).FontWeight(700)
					ui.Text(c, fmt.Sprintf("已扫描 %d / %d 首音频，正在分析 LRC 逐字标签并排除纯音乐 / OST / 伴奏", a.libScanDone, a.libScanAll)).
						FontSize(13).TextColor(p.muted)
				})
				return
			}

			st := a.libStats
			vocalPct := 0.0
			if st.Vocal > 0 {
				vocalPct = float64(st.Enhanced) * 100.0 / float64(st.Vocal)
			}

			ui.Row(c).AlignItems(ui.End).Gap(8).Children(func() {
				ui.Column(c).Grow(1).Gap(2).Children(func() {
					libTrail := strings.Join(a.libFolders, " · ")
					if a.libScanTime != "" {
						libTrail += " · 上次扫描 " + a.libScanTime
					}
					ui.Text(c, libTrail).FontSize(11.5).FontWeight(600).TextColor(p.muted)
					ui.Text(c, "曲库").FontSize(28).FontWeight(800)
				})
				if a.libScanning {
					ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
						winFiveDots(c, p.accent)
						ui.Text(c, fmt.Sprintf("统计中 · 已加载 %d / %d", a.libScanDone, a.libScanAll)).
							FontSize(11.5).FontWeight(600).TextColor(p.muted)
					})
				}
				if pillButton(c.Key("lib-refresh-top"), iconRefresh, "刷新", p.surface2, p.text).Clicked() {
					a.refreshLibrary()
				}
				if pillButton(c.Key("lib-add-folder"), iconFolder, "添加文件夹", p.surface2, p.text).Shrink(0).Clicked() {
					a.libFolderMenuOpen = false
					a.pickLibraryFolder()
				}
				a.viewLibraryFolders(c)
			})

			a.viewLibraryOverview(c, st, vocalPct)

			ui.Row(c.Key("filter-bar")).Gap(6).Margin(4, 0, 0, 0).AlignItems(ui.Center).Children(func() {
				counts := a.filterCounts()
				tabs := []struct {
					label  string
					filter int
				}{
					{fmt.Sprintf("有效人声 %d", counts[0]), 0},
					{fmt.Sprintf("官方逐字 %d", counts[5]), 5},
					{fmt.Sprintf("对齐逐字 %d", counts[2]), 2},
					{fmt.Sprintf("仅整行 %d", counts[1]), 1},
					{fmt.Sprintf("无歌词 %d", counts[3]), 3},
					{fmt.Sprintf("纯音乐 / OST / 伴奏 %d", counts[4]), 4},
				}
				for _, tab := range tabs {
					idx, label := tab.filter, tab.label
					on := a.libFilter == idx
					btn := ui.ButtonBase(c.Key(fmt.Sprintf("tab-%d", idx))).Height(32).PaddingX(14).Radius(999).
						Center().Label(label).Selected(on).FocusRing(false)
					bg := ui.Transparent
					if btn.Hovered() && !on {
						bg = p.hover
					}
					btn.Background(btn.AnimateColor("bg", bg, ui.Animation{Duration: 120 * time.Millisecond}))
					pose := ui.Pose{}
					if btn.Pressed() {
						pose.Scale = 0.95
					}
					btn.AnimatePose(pose, ui.Snappy)
					fg := p.muted
					if on {
						fg = p.onAccent
					} else if btn.Hovered() {
						fg = p.text
					}
					btn.Children(func() {
						if on {
							pill := ui.Box(c.Key("tab-pill")).Absolute().Top(0).Left(0).Right(0).Bottom(0).Radius(999).
								PassThrough().Background(p.accent)
							pill.Transition(ui.ElementTransition{Animation: &motionPane, Match: tabPillMatch{}})
						}
						ui.Box(c.Key("tab-tx")).Absolute().Top(0).Left(0).Right(0).Bottom(0).Center().PassThrough().Children(func() {
							ui.Text(c, label).FontSize(13).FontWeight(700).TextColor(fg).SingleLine()
						})
						ui.Text(c, label).FontSize(13).FontWeight(700).TextColor(ui.Transparent).SingleLine()
					})
					if btn.Clicked() {
						a.setLibFilter(idx)
					}
				}
				ui.Box(c).Grow(1)
				if a.libFilter == 4 {
					statusPill(c, fmt.Sprintf("纯音乐 %d", st.InstPure), p.surface2, p.muted)
					statusPill(c, fmt.Sprintf("OST 原声 %d", st.InstOST), p.surface2, p.muted)
					statusPill(c, fmt.Sprintf("伴奏 %d", st.InstAccomp), p.surface2, p.muted)
				}
			})

			// Dynamic batch action bar
			selTracks := a.selectedLibraryTracks()
			selCount := len(selTracks)
			actionBar := ui.Row(c.Key("batch-bar")).FillWidth().Padding(10, 14, 10, 14).Gap(10).Radius(14).Wrap().
				AlignItems(ui.Center).Background(p.surface)
			actionBar.Draw(func(pt *ui.Painter, r ui.Rect) { pt.Stroke(r, p.hairline, 14, 1) })
			actionBar.Children(func() {
				if a.libFilter == 3 {
					ui.Text(c, fmt.Sprintf("已选择 %d 首无歌词歌曲", selCount)).FontSize(13).FontWeight(700)
				} else {
					ui.Text(c, fmt.Sprintf("已选择 %d 首歌曲", selCount)).FontSize(13).FontWeight(700)
				}
				if pillButton(c.Key("lib-sel-page"), nil, "全选本页", p.surface2, p.text).Clicked() {
					a.setPageSelected(true)
				}
				if pillButton(c.Key("lib-sel-all"), nil, "全选全部", p.surface2, p.text).Clicked() {
					a.setVisibleTracksSelected(true)
				}
				if selCount > 0 {
					if pillButton(c.Key("lib-sel-none"), nil, "取消全选", p.surface2, p.muted).Clicked() {
						a.setVisibleTracksSelected(false)
					}
				}
				ui.Box(c).Grow(1)
				if a.libFilter == 3 {
					if pillButton(c.Key("lib-batch-id"), nil, "批量填 ID", p.surface2, p.text).Clicked() {
						a.openNoLyricsDialog("batch_id", nil)
					}
					if selCount > 0 {
						if pillButton(c.Key("lib-nl-download-lyrics"), iconDownload, "下载歌词", p.surface2, p.text).Clicked() {
							a.downloadLyricsOf(selTracks)
						}
						if pillButton(c.Key("lib-nl-scrape"), iconTag, "刮削标签", p.surface2, p.text).Clicked() {
							paths := make([]string, 0, len(selTracks))
							for _, t := range selTracks {
								paths = append(paths, t.FullPath)
							}
							a.openScrapeTracks(paths)
						}
					}
					if pillButton(c.Key("lib-retry-search"), nil, "重试全网", p.surface2, p.text).Clicked() {
						a.enqueueNoLyricsRetrySearch(selTracks)
					}
					if pillButton(c.Key("lib-batch-asr"), iconMic, "本地 ASR 识别并对齐", p.surface2, p.text).Clicked() {
						a.enqueueNoLyricsAsr(selTracks)
					}
				} else if a.libFilter == 4 {
					if selCount > 0 {
						if pillButton(c.Key("lib-inst-scrape"), iconTag, "刮削标签", p.surface2, p.text).Clicked() {
							paths := make([]string, 0, len(selTracks))
							for _, t := range selTracks {
								paths = append(paths, t.FullPath)
							}
							a.openScrapeTracks(paths)
						}
					} else {
						ui.Text(c, "勾选下方曲目后即可一键批量刮削标签").FontSize(12).TextColor(p.muted)
					}
				} else if selCount > 0 {
					if pillButton(c.Key("lib-download-lyrics"), iconDownload, "下载歌词", p.surface2, p.text).Clicked() {
						a.downloadLyricsOf(selTracks)
					}
					if pillButton(c.Key("lib-scrape"), iconTag, "刮削标签", p.surface2, p.text).Clicked() {
						paths := make([]string, 0, len(selTracks))
						for _, t := range selTracks {
							paths = append(paths, t.FullPath)
						}
						a.openScrapeTracks(paths)
					}
					if pillButton(c.Key("lib-start-align"), iconPlay, "对齐歌词", p.surface2, p.text).Clicked() {
						a.enqueueFromLibrary()
						a.navigate(pageQueue, 1)
					}
				} else {
					ui.Text(c, "勾选下方歌曲后即可一键批量对齐歌词").FontSize(12).TextColor(p.muted)
				}
			})

			listCard := ui.Column(c.Key("track-table")).FillWidth().Gap(3)
			listCard.Children(func() {
				isNlTab := a.libFilter == 3
				const actColW = 84
				ui.Row(c.Key("thead")).Height(34).PaddingX(12).Gap(12).AlignItems(ui.Center).Children(func() {
					ui.Box(c).Width(22).Shrink(0)
					ui.Text(c, "曲目").Grow(2).Basis(0).MinWidth(0).FontSize(11.5).FontWeight(600).TextColor(p.faint)

					ui.Text(c, "歌词状态").Width(92).Shrink(0).FontSize(11.5).FontWeight(600).TextColor(p.faint)
					ui.Text(c, "语言").Width(48).Shrink(0).FontSize(11.5).FontWeight(600).TextColor(p.faint)

					ui.Text(c, "备注").Grow(1).Basis(0).MinWidth(0).FontSize(11.5).FontWeight(600).TextColor(p.faint)
					ui.Text(c, "操作").Width(actColW).Shrink(0).FontSize(11.5).FontWeight(600).TextColor(p.faint)
				})
				// 先算出当前筛选下的全部行，再按页取一段
				visible := make([]int, 0, len(a.tracks))
				for i := range a.tracks {
					if a.trackMatchesFilter(&a.tracks[i]) {
						visible = append(visible, i)
					}
				}
				size := a.pageSize()
				pages := (len(visible) + size - 1) / size
				if pages < 1 {
					pages = 1
				}
				if a.libPage >= pages {
					a.libPage = pages - 1
				}
				if a.libPage < 0 {
					a.libPage = 0
				}
				start := a.libPage * size
				end := min(start+size, len(visible))
				for _, idx := range visible[start:end] {
					tr := &a.tracks[idx]
					sel := a.isTrackSelected(tr.FullPath)

					row := ui.Row(c.Key(fmt.Sprintf("tr-%d", idx))).FillWidth().Height(54).PaddingX(12).Gap(12).Radius(12).
						AlignItems(ui.Center)
					targetBg := ui.Transparent
					if sel {
						targetBg = p.accent.Alpha(0.11)
					} else if row.Hovered() {
						targetBg = p.hover
					}
					row.Background(row.AnimateColor("bg", targetBg, ui.Animation{Duration: 150 * time.Millisecond}))
					row.Draw(func(pt *ui.Painter, r ui.Rect) {
						if sel {
							bar := ui.Rect{X: r.X + 3, Y: r.Y + (r.H-20)/2, W: 3.5, H: 20}
							pt.Fill(bar, p.accent, 2)
						}
					})

					row.Children(func() {
						mainBtn := ui.ButtonBase(c.Key(fmt.Sprintf("tr-main-%d", idx))).Grow(1).MinWidth(0).FillHeight().Gap(12).
							AlignItems(ui.Center).Background(ui.Transparent).Label(tr.Title).FocusRing(false)
						mainBtn.ContextMenu(func(m *ui.Menu) {
							if m.Item("下载歌词").Chosen() {
								a.openDownloadSourceMenu(*tr)
							}
							if m.Item("刮削标签").Chosen() {
								a.openScrapeTracks([]string{tr.FullPath})
							}
							if m.Item("新建对齐").Chosen() {
								a.setAlignAudioPath(tr.FullPath)
								a.navigate(pageAlign, 1)
							}
							if m.Item("打开校对").Chosen() {
								a.openTrackInEditor(tr)
							}
							if m.Item("在资源管理器中显示").Chosen() {
								a.revealInExplorer(tr.FullPath)
							}
							if m.Item("复制文件路径").Chosen() {
								c.WriteClipboard(tr.FullPath)
								a.toast = "已复制文件路径"
							}
						})
						mainBtn.Children(func() {
							cb := ui.Box(c).Size(18, 18).Margin(0, 2, 0, 2).Radius(6).Shrink(0).Center()
							cbFill := cb.AnimateColor("cbf", map[bool]ui.Color{true: p.accent, false: ui.Transparent}[sel], ui.Animation{Duration: 140 * time.Millisecond})
							cb.Background(cbFill)
							cb.Draw(func(pt *ui.Painter, r ui.Rect) {
								if !sel {
									pt.Stroke(r, p.faint, 6, 1.5)
								}
							})
							if sel {
								cb.Children(func() { ui.Icon(c, iconCheck).Size(12, 12).TextColor(p.onAccent) })
							}

							trackCell := ui.Row(c.Key("track")).Grow(2).Basis(0).MinWidth(0).Gap(10).AlignItems(ui.Center)
							trackCell.Children(func() {
								if cover, _ := a.lookupCover(tr.FullPath); cover != nil {
									ui.Image(c, cover).Size(36, 36).Radius(8).Fit(ui.Cover).Shrink(0)
								} else {
									artCover(c, tr.Hue, 36, 8, nil)
								}
								ui.Column(c).MinWidth(0).Grow(1).Gap(1).Children(func() {
									ui.Text(c, tr.Title).FontSize(13.5).FontWeight(700).TextColor(p.text).SingleLine()
									ui.Text(c, tr.Artist+" · "+tr.File).FontSize(11.5).TextColor(p.muted).SingleLine()
								})
							})
							ui.Box(c).Width(92).Shrink(0).AlignItems(ui.Start).Children(func() {
								switch tr.Status {
								case "official":
									statusPill(c, "官方逐字", p.okBg, p.ok)
								case "aligned":
									statusPill(c, "对齐逐字", p.okBg, p.ok)
								case "unknown":
									statusPill(c, "来源未知", p.surface2, p.muted)
								case "line_only":
									statusPill(c, "仅整行", p.accent.Alpha(0.14), p.accent)
								case "nolyrics_notfound":
									statusPill(c, "全网无词", p.warnBg, p.warn)
								case "missing":
									statusPill(c, "本地缺词", p.surface2, p.muted)
								case "inst_pure":
									statusPill(c, "纯音乐", p.surface2, p.muted)
								case "inst_ost":
									statusPill(c, "OST 原声", p.surface2, p.muted)
								case "inst_accomp":
									statusPill(c, "伴奏", p.surface2, p.muted)
								default:
									statusPill(c, "已跳过", p.surface2, p.muted)
								}
							})
							ui.Box(c).Width(48).Shrink(0).AlignItems(ui.Start).Children(func() {
								statusPill(c, tr.Lang, p.surface2, p.muted)
							})
							ui.TooltipBase(c, trackCell, func(tip ui.Element) {
								tip.Padding(6, 10).Radius(8).Background(p.raised).Border(1, p.hairline).
									Shadow(0, 6, 18, 0, ui.RGBA(0, 0, 0, 0.22)).MaxWidth(640)
								tip.Children(func() {
									ui.Text(c, tr.FullPath).FontSize(12).TextColor(p.text)
								})
							})
							ui.Text(c, tr.Note).Grow(1).Basis(0).MinWidth(0).FontSize(12).TextColor(p.muted).SingleLine()
						})
						// 只切换勾选：整行是一个按钮，连点两下会被当成双击，
						// 于是勾选没做成，人却跳进了校对页。进校对走铅笔按钮或底部按钮。
						if mainBtn.Clicked() {
							a.toggleTrackSelected(tr.FullPath)
						}

						if isNlTab {
							ui.Row(c).Width(actColW).Shrink(0).Justify(ui.End).Children(func() {
								a.viewLibraryLyricsMenu(c, idx, tr)
							})
						} else {
							ui.Row(c).Width(actColW).Shrink(0).Gap(4).Justify(ui.End).Children(func() {
								if iconButton(c.Key(fmt.Sprintf("tr-align-%d", idx)), iconPlay, 13, p.muted, "对齐 "+tr.Title).Clicked() {
									a.setAlignAudioPath(tr.FullPath)
									a.navigate(pageAlign, 1)
								}
								if iconButton(c.Key(fmt.Sprintf("tr-edit-%d", idx)), iconEdit, 14, p.muted, "校对 "+tr.Title).Clicked() {
									a.openTrackInEditor(tr)
								}
							})
						}
					})

				}

				// 按需加载当前页封面，并预取下一页。
				want := make([]string, 0, size*2)
				for _, idx := range visible[start:end] {
					want = append(want, a.tracks[idx].FullPath)
				}
				for _, idx := range visible[end:min(end+size, len(visible))] {
					want = append(want, a.tracks[idx].FullPath)
				}
				a.requestCovers(want)

				// 分页：页码按钮 + 每页条数下拉
				ui.Row(c.Key("lib-pager")).FillWidth().AlignItems(ui.Center).Gap(6).
					Margin(8, 0, 0, 0).Children(func() {
					ui.Text(c, fmt.Sprintf("共 %d 首", len(visible))).FontSize(12).TextColor(p.muted)

					arrow := func(key string, icon *ui.SVG, target int, enabled bool) {
						col := p.text
						if !enabled {
							col = p.faint.Alpha(0.4)
						}
						b := ui.ButtonBase(c.Key(key)).Size(30, 30).Radius(9).Center().Label(key)
						if b.Hovered() && enabled {
							b.Background(p.hover)
						}
						b.Children(func() { ui.Icon(c, icon).Size(14, 14).TextColor(col) })
						if enabled && b.Clicked() {
							a.libPage = target
							a.libRowMenuPath = ""
							if a.win != nil {
								a.win.Invalidate()
							}
						}
					}
					arrow("page-prev", iconChevronL, a.libPage-1, a.libPage > 0)

					for _, n := range pageNumbers(a.libPage+1, pages, 5) {
						if n == 0 {
							ui.Text(c, "…").FontSize(12).TextColor(p.faint).Width(14).Shrink(0)
							continue
						}
						idx := n - 1
						cur := idx == a.libPage
						pb := ui.ButtonBase(c.Key(fmt.Sprintf("page-n-%d", n))).Size(30, 30).Radius(9).Center().
							Label(fmt.Sprintf("第 %d 页", n))
						switch {
						case cur:
							pb.Background(p.surface2)
						case pb.Hovered():
							pb.Background(p.hover)
						}
						txCol, weight := p.muted, 500
						if cur {
							txCol, weight = p.text, 700
						}
						pb.Children(func() {
							ui.Text(c, fmt.Sprintf("%d", n)).FontSize(12.5).FontWeight(weight).TextColor(txCol)
						})
						if pb.Clicked() {
							a.libPage = idx
							a.libRowMenuPath = ""
							if a.win != nil {
								a.win.Invalidate()
							}
						}
					}

					arrow("page-next", iconChevronR, a.libPage+1, a.libPage < pages-1)

					// 每页条数：按钮显示当前值，点开从正上方居中弹出动画菜单
					sizeBtn := ui.ButtonBase(c.Key("page-size-btn")).Height(30).PaddingX(12).Radius(999).
						Gap(5).AlignItems(ui.Center).Label(fmt.Sprintf("每页 %d 条", a.pageSize())).
						Background(p.accent.Alpha(0.10))
					menuProgress := b2f(a.pageMenuOpen)
					if !c.Preferences().ReduceMotion {
						menuProgress = clamp01(sizeBtn.AnimateTo("menu-open", menuProgress, motionPane))
					}
					sizeBtn.Children(func() {
						ui.Text(c, fmt.Sprintf("%d / 页", a.pageSize())).FontSize(12).FontWeight(600).
							TextColor(p.accent).SingleLine()
						chev := ui.Icon(c, iconChevronD).Size(11, 11).TextColor(p.accent)
						if menuProgress > 0 {
							chev.Scale(1, 1-2*menuProgress)
						}
					})
					if sizeBtn.Clicked() {
						a.pageMenuOpen = !a.pageMenuOpen
						if a.win != nil {
							a.win.Invalidate()
						}
					}

					if menuProgress > 0 || a.pageMenuOpen {
						shown := true
						ui.PopoverBase(c.Key("page-size-popover"), sizeBtn, &shown, func(panel ui.Element) {
							s := lerp(0.92, 1, menuProgress)
							panel.AttachTo(sizeBtn, ui.AnchorTop, ui.AnchorBottom).
								Width(86).Margin(0, 0, 6, 0).Padding(4).Radius(12).
								Background(p.raised).Border(1, p.hairline).Shadow(0, 6, 18, 0, p.cardShade).
								TransformOrigin(0.5, 1).Opacity(menuProgress).Scale(s, s)
							ui.Column(c).FillWidth().Gap(2).Children(func() {
								for _, n := range []int{50, 100} {
									size := n
									cur := a.pageSize() == size
									item := ui.ButtonBase(c.Key(fmt.Sprintf("page-size-%d", size))).FillWidth().Height(28).PaddingX(8).
										Gap(4).Radius(8).AlignItems(ui.Center).Label(fmt.Sprintf("每页 %d 条", size)).FocusRing(false)
									if cur {
										item.Background(p.accent.Alpha(0.12))
									} else if item.Hovered() {
										item.Background(p.hover)
									}
									item.Children(func() {
										fg := p.text
										if cur {
											fg = p.accent
										}
										ui.Text(c, fmt.Sprintf("%d / 页", size)).Grow(1).FontSize(12).FontWeight(600).TextColor(fg).SingleLine()
										if cur {
											ui.Icon(c, iconCheck).Size(13, 13).TextColor(p.accent)
										}
									})
									if item.Clicked() {
										a.setPageSize(size)
										a.pageMenuOpen = false
										if a.win != nil {
											a.win.Invalidate()
										}
									}
								}
							})
						})
						if !shown && a.pageMenuOpen {
							a.pageMenuOpen = false
						}
					}
				})
			})
		})
	})
}

// pageNumbers 生成要显示的页码（0 表示省略号）：首页、末页、当前页左右各一个。
func pageNumbers(cur, total, width int) []int {
	if total <= width+2 {
		out := make([]int, 0, total)
		for i := 1; i <= total; i++ {
			out = append(out, i)
		}
		return out
	}
	set := map[int]bool{1: true, total: true}
	for d := -1; d <= 1; d++ {
		if n := cur + d; n >= 1 && n <= total {
			set[n] = true
		}
	}
	nums := make([]int, 0, len(set))
	for n := range set {
		nums = append(nums, n)
	}
	sort.Ints(nums)
	out := make([]int, 0, len(nums)+2)
	for i, n := range nums {
		if i > 0 && n-nums[i-1] > 1 {
			out = append(out, 0)
		}
		out = append(out, n)
	}
	return out
}

// viewLibraryEmpty renders Cloudy's clean empty state before any music folder is added.
func (a *app) viewLibraryEmpty(c *ui.Context) {
	p := a.pal
	ui.Column(c).Gap(2).Children(func() {
		ui.Text(c, "本地音乐文件夹 · 尚未添加").FontSize(11.5).FontWeight(600).TextColor(p.muted)
		ui.Text(c, "曲库").FontSize(28).FontWeight(800)
	})

	box := ui.Column(c.Key("lib-empty-card")).FillWidth().Padding(56, 36, 56, 36).Gap(12).Radius(22).
		AlignItems(ui.Center).Background(p.surface)
	box.Draw(func(pt *ui.Painter, r ui.Rect) {
		pt.Stroke(r, p.hairline, 22, 1)
	})
	box.Children(func() {
		badge := ui.Box(c).Size(58, 58).Radius(18).Center().Background(p.accent.Alpha(0.14))
		badge.Children(func() {
			ui.Icon(c, iconFolder).Size(26, 26).TextColor(p.accent)
		})
		ui.Text(c, "还没有添加本地音乐文件夹").FontSize(19).FontWeight(800).Margin(4, 0, 0, 0)
		ui.Text(c, "打开软件时不会自动扫描磁盘。请选择一个或多个音乐文件夹，扫描时将自动识别并排除纯音乐、OST 原声与伴奏轨。").
			FontSize(13).TextColor(p.muted).MaxWidth(520)

		ui.Row(c).Gap(10).Margin(10, 0, 0, 0).AlignItems(ui.Center).Children(func() {
			if pillButton(c.Key("empty-add-folder"), iconPlus, "添加文件夹", p.accent, p.onAccent).Clicked() {
				a.pickLibraryFolder()
			}
		})
	})
}

// ==================== 4. 编辑与校对 ====================
