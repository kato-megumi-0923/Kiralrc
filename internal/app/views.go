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
				case pageSettings:
					a.viewSettings(c)
				}
			})

			if a.page != pageSettings && a.envChecked && !a.envInfo.Ready {
				banner := ui.Row(c.Key("env-setup-banner")).Absolute().Top(buttonsHeight+4).Left(38).Right(38).Height(44).
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
						a.navigate(pageSettings, 1)
					}
				})
			}

			if a.page != pageEditor && (a.edFile != "" || a.queueActiveTitle != "") {
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
			if a.toast != "" {
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
						if a.page != pageSettings && strings.Contains(a.toast, "设置") {
							goBtn := ui.ButtonBase(c.Key("toast-go-settings")).Height(24).PaddingX(10).Radius(999).Center().
								Background(p.accent).Label("去设置").FocusRing(false)
							goBtn.Children(func() {
								ui.Text(c, "去设置").FontSize(11.5).FontWeight(700).TextColor(p.onAccent).SingleLine()
							})
							if goBtn.Clicked() {
								a.toast = ""
								a.toastLast = ""
								a.navigate(pageSettings, 1)
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
				a.libPage = 0      // 搜索词一变就回第一页
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
			row := ui.ButtonBase(c.Key("nav-"+it.label)).Height(36).Radius(9).Label(it.label).Selected(sel).FocusRing(false)
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
				btn := ui.ButtonBase(c.Key("rec-"+r.Path)).FillWidth().Height(33).PaddingX(10).Gap(9).Radius(8).
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
		setSel := a.page == pageSettings
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
			a.navigate(pageSettings, 0)
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
			needUpgrade := st.LineOnly + st.Missing
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
				if pillButton(c.Key("lib-clear-folders"), nil, "移除文件夹", p.surface2, p.muted).Clicked() {
					a.clearLibraryFolders()
					return
				}
			})

			// Cloudy Daily-style Hero Banner (hover animates vinyl record sliding out!)
			hero := ui.Row(c.Key("hero")).FillWidth().Height(188).Padding(0, 28, 0, 28).Radius(22).AlignItems(ui.Center)
			heroOut := hero.AnimateTo("vinyl-out", b2f(hero.Hovered()), ui.Animation{Spring: ui.SpringOptions{Duration: 420 * time.Millisecond, Bounce: 0.2}})
			hero.Draw(func(pt *ui.Painter, r ui.Rect) {
				c1 := p.accent.Mix(ui.Hex("#1f1417"), 0.55)
				c2 := p.accent.Mix(ui.Hex("#ff9966"), 0.25)
				pt.Shadow(r, 22, 0, 14, 32, -12, p.accent.Alpha(0.35))
				pt.FillGradient(r, ui.LinearGradient{
					Angle: 125,
					Stops: []ui.GradientStop{{At: 0, Color: c1}, {At: 1, Color: c2}},
				}, 22)
			})
			hero.Children(func() {
				ui.Column(c).Grow(1).Gap(4).Children(func() {
					ui.Text(c, fmt.Sprintf("本地曲库总览 · 共 %d 首音频（有效人声 %d 首 · 已排除纯音乐 / OST / 伴奏 %d 首）",
						st.Total, st.Vocal, st.InstTotal)).FontSize(12).FontWeight(600).TextColor(white.Alpha(0.78))
					heroTitle := fmt.Sprintf("还有 %d 首人声歌曲待升级或待补词", needUpgrade)
					if a.downloading {
						if a.downloadAll > 0 {
							heroTitle = fmt.Sprintf("正在下载歌词 %d / %d", a.downloadDone, a.downloadAll)
						} else {
							heroTitle = "正在下载歌词…"
						}
					}
					ui.Text(c, heroTitle).FontSize(26).FontWeight(800).TextColor(white)
					nfSub := ""
					if st.NotFound > 0 {
						nfSub = fmt.Sprintf("（含 %d 首全网未搜到）", st.NotFound)
					}
					summary := fmt.Sprintf("%d 首逐字（占人声 %.1f%% · 官方 %d / 对齐 %d / 来源未知 %d）· %d 首仅整行 · %d 首无歌词%s   |   已排除：纯音乐 %d · OST 原声 %d · 伴奏 %d",
						st.Enhanced, vocalPct, st.Official, st.Aligned, st.Unknown,
						st.LineOnly, st.Missing, nfSub,
						st.InstPure, st.InstOST, st.InstAccomp)
					if a.downloading && a.downloadNote != "" {
						summary = a.downloadNote
					}
					ui.Text(c, summary).FontSize(12.5).TextColor(white.Alpha(0.88))
					ui.Row(c).Gap(8).Margin(12, 0, 0, 0).Children(func() {
						dlLabel := "下载歌词"
						if a.downloading {
							dlLabel = "下载中…"
						}
						if pillButton(c.Key("hero-download"), iconDownload, dlLabel, white, ui.Hex("#1d1d1f")).Clicked() {
							a.startDownloadLyrics()
						}
						batchLabel := fmt.Sprintf("批量升级整行歌词（%d）", st.LineOnly)
						if a.libScanning {
							batchLabel = fmt.Sprintf("批量升级整行歌词（已加载 %d）", len(a.tracks))
						}
						if pillButton(c.Key("hero-batch"), iconPlay, batchLabel, ui.RGBA(255, 255, 255, 0.18), white).Clicked() {
							a.enqueueLineOnlyTracks()
						}
						if pillButton(c.Key("hero-folder"), iconFolder, "添加文件夹", ui.RGBA(255, 255, 255, 0.18), white).Clicked() {
							a.pickLibraryFolder()
						}
						if pillButton(c.Key("hero-scan"), iconRefresh, "重新扫描", ui.RGBA(255, 255, 255, 0.18), white).Clicked() {
							a.scanLibraryAsync()
						}
					})
				})

				// Stacked sleeves + vinyl record (glides out when hovered!)
				stack := ui.Box(c.Key("hero-stack")).Size(250, 140).Shrink(0)
				stack.Draw(func(pt *ui.Painter, r ui.Rect) {
					vRect := ui.Rect{X: r.X + 102 + heroOut*22, Y: r.Y + 12, W: 116, H: 116}
					pt.Shadow(vRect, 58, 0, 10, 22, -6, ui.RGBA(0, 0, 0, 0.5))
					pt.Fill(vRect, ui.Hex("#17171b"), 58)
					pt.Stroke(ui.Rect{X: vRect.X + 12, Y: vRect.Y + 12, W: 92, H: 92}, ui.RGBA(255, 255, 255, 0.07), 46, 1)
					pt.Fill(ui.Rect{X: vRect.X + 38, Y: vRect.Y + 38, W: 40, H: 40}, p.accent, 20)
					pt.Fill(ui.Rect{X: vRect.X + 54, Y: vRect.Y + 54, W: 8, H: 8}, white, 4)

					s3 := ui.Rect{X: r.X + 4 - heroOut*6, Y: r.Y + 20, W: 104, H: 104}
					pt.Fill(s3, ui.Oklch(0.68, 0.16, 55), 14)
					s2 := ui.Rect{X: r.X + 22 - heroOut*3, Y: r.Y + 16, W: 110, H: 110}
					pt.Fill(s2, ui.Oklch(0.60, 0.17, 250), 14)
					s1 := ui.Rect{X: r.X + 42, Y: r.Y + 12, W: 116, H: 116}
					pt.Shadow(s1, 14, 0, 10, 22, -6, ui.RGBA(0, 0, 0, 0.45))
					pt.FillGradient(s1, ui.LinearGradient{
						Angle: 135,
						Stops: []ui.GradientStop{{At: 0, Color: ui.Oklch(0.74, 0.16, 25)}, {At: 1, Color: ui.Oklch(0.56, 0.19, 350)}},
					}, 14)
				})
				stack.Children(func() {
					x := [3]float32{42, 22 - heroOut*3, 4 - heroOut*6}
					y := [3]float32{12, 16, 20}
					for i := min(2, len(a.tracks)-1); i >= 0; i-- {
						if cover, _ := a.lookupCover(a.tracks[i].FullPath); cover != nil {
							size := float32(116 - 6*i)
							ui.Image(c, cover).Absolute().Top(y[i]).Left(x[i]).Size(size, size).Radius(14).Fit(ui.Cover).PassThrough()
						}
					}
				})
			})

			// 5 张统计卡（悬停上浮、选中描边、点击切筛选）
			ui.Row(c.Key("stats")).Gap(12).AlignItems(ui.Stretch).Children(func() {
				nfBadge := ""
				if st.NotFound > 0 {
					nfBadge = fmt.Sprintf("%d 首全网无词", st.NotFound)
				}
				stats := []struct {
					key, n, badge, l string
					ic               *ui.SVG
					bg, fg           ui.Color
					filter           int
				}{
					{"official", fmt.Sprintf("%d", st.Official), "", "官方逐字 · 平台下载", iconCheck, p.okBg, p.ok, 5},
					{"ready", fmt.Sprintf("%d", st.Aligned+st.Unknown), "", fmt.Sprintf("对齐逐字 %d · 来源未知 %d", st.Aligned, st.Unknown), iconNote, p.accent.Alpha(0.12), p.accent, 2},
					{"upgrade", fmt.Sprintf("%d", st.LineOnly), "", "仅整行 LRC · 可直接升级逐字", iconNote, p.accent.Alpha(0.12), p.accent, 1},
					{"nolyrics", fmt.Sprintf("%d", st.Missing), nfBadge, "无歌词 · 支持 ID / 手填 / ASR", iconWarn, p.warnBg, p.warn, 3},
					{"inst", fmt.Sprintf("%d", st.InstTotal), "", fmt.Sprintf("已排除：纯音乐 %d · OST %d · 伴奏 %d", st.InstPure, st.InstOST, st.InstAccomp), iconWave, p.surface2, p.muted, 4},
				}
				if a.libScanning { // 数字还在长，标个「约」免得看着像在抽风
					for i := range stats {
						stats[i].n = "约 " + stats[i].n
					}
				}
				for _, sItem := range stats {
					s := sItem
					activeCard := a.libFilter == s.filter
					card := ui.ButtonBase(c.Key("stat-"+s.key)).Grow(1).Basis(0).MinWidth(0).Padding(14, 15, 14, 15).Gap(12).Radius(16).
						AlignItems(ui.Center).Label(s.l).FocusRing(false)
					cardBg := p.surface
					if card.Hovered() {
						cardBg = p.surface2
					}
					card.Background(card.AnimateColor("bg", cardBg, ui.Animation{Duration: 140 * time.Millisecond}))
					lift := ui.Pose{}
					if card.Pressed() {
						lift = ui.Pose{Scale: 0.98}
					} else if card.Hovered() {
						lift = ui.Pose{Y: -2, Scale: 1.01}
					}
					card.AnimatePose(lift, ui.Snappy)
					card.Draw(func(pt *ui.Painter, r ui.Rect) {
						if activeCard {
							pt.Stroke(r, p.accent, 16, 1.5)
						} else {
							pt.Stroke(r, p.hairline, 16, 1)
						}
					})
					card.Children(func() {
						ib := ui.Box(c).Size(38, 38).Radius(12).Shrink(0).Center().Background(s.bg)
						ib.Children(func() { ui.Icon(c, s.ic).Size(18, 18).TextColor(s.fg) })
						ui.Column(c).MinWidth(0).Grow(1).Gap(2).Children(func() {
							ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
								ui.Text(c, s.n).FontSize(22).FontWeight(800).TextColor(p.text).SingleLine()
								if s.badge != "" {
									statusPill(c, s.badge, p.warnBg, p.warn)
								}
							})
							ui.Text(c, s.l).FontSize(11.5).TextColor(p.muted).SingleLine()
						})
					})
					if card.Clicked() {
						a.setLibFilter(s.filter)
					}
				}
			})

			// 每个 Tab 对应一个筛选值，条数现算（搜索词也算在内），
			// 保证「Tab 上的数字」和「点下去列表里的行数」永远一致。
			ui.Row(c.Key("filter-bar")).Gap(6).Margin(4, 0, 0, 0).AlignItems(ui.Center).Children(func() {
				counts := a.filterCounts()
				tabs := []struct {
					label string
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
			actionBar := ui.Row(c.Key("batch-bar")).FillWidth().Padding(10, 14, 10, 14).Gap(10).Radius(14).
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
					idBtnBg, idBtnFg := p.surface2, p.text
					if a.libBatchIdOpen {
						idBtnBg, idBtnFg = p.accent.Alpha(0.14), p.accent
					}
					if pillButton(c.Key("lib-batch-id-toggle"), iconLink, "批量填网易云 ID", idBtnBg, idBtnFg).Clicked() {
						a.libBatchIdOpen = !a.libBatchIdOpen
						if a.libBatchIdOpen {
							a.libExpandedPath = ""
							a.libExpandedMode = ""
						}
					}
					if pillButton(c.Key("lib-retry-search"), nil, "重试全网搜索", p.surface2, p.text).Clicked() {
						a.enqueueNoLyricsRetrySearch(selTracks)
					}
					asrLabel := "本地 ASR 识别并对齐"
					if selCount > 0 {
						asrLabel = fmt.Sprintf("本地 ASR 识别并对齐（%d 首）", selCount)
					}
					if pillButton(c.Key("lib-batch-asr"), iconMic, asrLabel, p.accent, p.onAccent).Clicked() {
						a.enqueueNoLyricsAsr(selTracks)
					}
				} else if selCount > 0 {
					if pillButton(c.Key("lib-download-lyrics"), iconDownload, fmt.Sprintf("下载歌词（%d 首）", selCount), p.surface2, p.text).Clicked() {
						a.downloadLyricsOf(selTracks)
					}
					if pillButton(c.Key("lib-scrape"), iconTag, fmt.Sprintf("刮削标签（%d 首）", selCount), p.surface2, p.text).Clicked() {
						paths := make([]string, 0, len(selTracks))
						for _, t := range selTracks {
							paths = append(paths, t.FullPath)
						}
						a.openScrapeTracks(paths)
					}
					if pillButton(c.Key("lib-start-align"), iconPlay, fmt.Sprintf("对齐歌词（%d 首）", selCount), p.accent, p.onAccent).Clicked() {
						a.enqueueFromLibrary()
						a.navigate(pageQueue, 1)
					}
				} else {
					ui.Text(c, "勾选下方歌曲后即可一键批量对齐歌词").FontSize(12).TextColor(p.muted)
				}
			})

			// Expandable "批量填网易云 ID" Drawer (only in 无歌词 Tab when open)
			if a.libFilter == 3 && a.libBatchIdOpen {
				if a.libBatchIdInputs == nil {
					a.libBatchIdInputs = make(map[string]string)
				}
				targets := a.batchIdTargetTracks()
				filledCount := 0
				for _, t := range targets {
					if parseNeteaseID(a.libBatchIdInputs[t.FullPath]) != "" {
						filledCount++
					}
				}
				drawer := ui.Column(c.Key("batch-id-drawer")).FillWidth().Padding(16, 16, 16, 16).Gap(10).Radius(16).Background(p.surface)
				drawer.Draw(func(pt *ui.Painter, r ui.Rect) {
					pt.Stroke(r, p.accent, 16, 1.2)
				})
				drawer.Children(func() {
					ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
						ui.Column(c).Grow(1).Gap(2).Children(func() {
							ui.Text(c, "批量填写网易云歌曲 ID").FontSize(14).FontWeight(700)
							ui.Text(c, "部分冷门/特典曲搜索不到但支持按 ID 直接下载歌词，直接粘贴浏览器地址栏 URL 即可自动提取 ID").FontSize(11.5).TextColor(p.muted)
						})
						if pillButton(c.Key("batch-id-close"), nil, "收起", p.surface2, p.muted).Clicked() {
							a.libBatchIdOpen = false
						}
					})
					if len(targets) == 0 {
						ui.Text(c, "当前没有无歌词歌曲").FontSize(12.5).TextColor(p.muted)
					}
					for ti, t := range targets {
						if ti >= 20 {
							break
						}
						tPath := t.FullPath
						val := a.libBatchIdInputs[tPath]
						pid := parseNeteaseID(val)
						rowBox := ui.Row(c.Key(fmt.Sprintf("bid-row-%d", ti))).FillWidth().Padding(8, 12, 8, 12).Gap(10).Radius(12).
							AlignItems(ui.Center).Background(p.bg)
						rowBox.Draw(func(pt *ui.Painter, r ui.Rect) { pt.Stroke(r, p.hairline, 12, 1) })
						rowBox.Children(func() {
							ui.Column(c).Width(210).Shrink(0).Gap(1).Children(func() {
								ui.Text(c, t.Title).FontSize(13).FontWeight(700).SingleLine()
								ui.Text(c, fmt.Sprintf("%s · %s", t.Artist, t.Lang)).FontSize(11).TextColor(p.muted).SingleLine()
							})
							inBox := ui.Row(c).Grow(1).MinWidth(0).Padding(6, 10, 6, 10).Radius(9).AlignItems(ui.Center).Background(p.surface)
							inBox.Draw(func(pt *ui.Painter, r ui.Rect) { pt.Stroke(r, p.hairline, 9, 1) })
							inBox.Children(func() {
								ui.TextInputBase(c.Key(fmt.Sprintf("bid-in-%d", ti)), &val).Grow(1).MinWidth(0).Height(22).
									FontSize(12.5).TextColor(p.text).Background(ui.Transparent).
									Placeholder("粘贴网易云歌曲 ID 或详情页 URL（如 music.163.com/#/song?id=30967650）").FocusRing(false)
							})
							a.libBatchIdInputs[tPath] = val
							if pid != "" {
								statusPill(c, "✓ ID: "+pid, p.okBg, p.ok)
							} else {
								statusPill(c, "待填写", p.surface2, p.muted)
							}
						})
					}
					ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
						ui.Text(c, "提示：未填写的行会自动跳过，不影响其它歌曲入队").FontSize(12).TextColor(p.muted).Grow(1)
						if pillButton(c.Key("batch-id-cancel"), nil, "取消", p.surface2, p.muted).Clicked() {
							a.libBatchIdOpen = false
						}
						if pillButton(c.Key("batch-id-submit"), nil, fmt.Sprintf("一键下载并入队（已填 %d 首）", filledCount), p.accent, p.onAccent).Clicked() {
							a.enqueueBatchNetease()
						}
					})
				})
			}

			// Track list (clickable selection with animated highlight + inline drawers in 无歌词 tab)
			listCard := ui.Column(c.Key("track-table")).FillWidth().Gap(3)
			listCard.Children(func() {
				isNlTab := a.libFilter == 3
				actColW := float32(72)
				if isNlTab {
					actColW = 236
				}
				ui.Row(c.Key("thead")).Height(34).PaddingX(12).Gap(12).AlignItems(ui.Center).Children(func() {
					ui.Box(c).Width(22)
					ui.Text(c, "标题").Width(250).FontSize(11.5).FontWeight(600).TextColor(p.faint)
					ui.Text(c, "艺术家").Width(110).FontSize(11.5).FontWeight(600).TextColor(p.faint)
					ui.Text(c, "歌词状态").Width(96).FontSize(11.5).FontWeight(600).TextColor(p.faint)
					ui.Text(c, "语言").Width(56).FontSize(11.5).FontWeight(600).TextColor(p.faint)
					ui.Text(c, "备注").Grow(1).FontSize(11.5).FontWeight(600).TextColor(p.faint)
					ui.Text(c, "操作").Width(actColW).FontSize(11.5).FontWeight(600).TextColor(p.faint)
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
						mainBtn.Children(func() {
							cb := ui.Box(c).Size(18, 18).Radius(6).Shrink(0).Center()
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

							ui.Row(c).Width(250).Shrink(0).Gap(10).AlignItems(ui.Center).Children(func() {
								if cover, _ := a.lookupCover(tr.FullPath); cover != nil {
									ui.Image(c, cover).Size(36, 36).Radius(8).Fit(ui.Cover).Shrink(0)
								} else {
									artCover(c, tr.Hue, 36, 8, nil)
								}
								ui.Column(c).MinWidth(0).Grow(1).Gap(1).Children(func() {
									ui.Text(c, tr.Title).FontSize(13.5).FontWeight(700).TextColor(p.text).SingleLine()
									ui.Text(c, tr.File).FontSize(11.5).TextColor(p.muted).SingleLine()
								})
							})
							ui.Text(c, tr.Artist).Width(110).Shrink(0).FontSize(13).TextColor(p.text).SingleLine()
							ui.Box(c).Width(96).Shrink(0).AlignItems(ui.Start).Children(func() {
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
							ui.Box(c).Width(56).Shrink(0).AlignItems(ui.Start).Children(func() {
								statusPill(c, tr.Lang, p.surface2, p.muted)
							})
							ui.Text(c, tr.Note).Grow(1).MinWidth(0).FontSize(12).TextColor(p.muted).SingleLine()
						})
						// 只切换勾选：整行是一个按钮，连点两下会被当成双击，
						// 于是勾选没做成，人却跳进了校对页。进校对走铅笔按钮或底部按钮。
						if mainBtn.Clicked() {
							a.toggleTrackSelected(tr.FullPath)
						}

						if isNlTab {
							isExp := strings.EqualFold(a.libExpandedPath, tr.FullPath)
							idOn := isExp && a.libExpandedMode == "netease"
							manOn := isExp && a.libExpandedMode == "manual"
							rowPill := func(key, label string, bg, fg ui.Color) ui.Element {
								b := ui.ButtonBase(c.Key(key)).Height(28).PaddingX(10).Radius(999).Center().
									Background(bg).Label(label).FocusRing(false)
								b.Children(func() {
									ui.Text(c, label).FontSize(11.5).FontWeight(700).TextColor(fg).SingleLine()
								})
								return b
							}
							ui.Row(c).Width(actColW).Shrink(0).Gap(6).Justify(ui.End).AlignItems(ui.Center).Children(func() {
								idBg, idFg := p.surface2, p.text
								if idOn {
									idBg, idFg = p.accent, p.onAccent
								}
								if rowPill(fmt.Sprintf("nl-id-%d", idx), "网易云 ID", idBg, idFg).Clicked() {
									a.toggleInlineDrawer(tr, "netease")
								}
								manBg, manFg := p.surface2, p.text
								if manOn {
									manBg, manFg = p.accent, p.onAccent
								}
								if rowPill(fmt.Sprintf("nl-man-%d", idx), "手动填词", manBg, manFg).Clicked() {
									a.toggleInlineDrawer(tr, "manual")
								}
								if rowPill(fmt.Sprintf("nl-asr-%d", idx), "本地 ASR", p.accent.Alpha(0.13), p.accent).Clicked() {
									a.enqueueNoLyricsAsr([]trackItem{*tr})
								}
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

					// Inline Drawer under the expanded no-lyrics row (网易云 ID / 手动填词)
					if isNlTab && strings.EqualFold(a.libExpandedPath, tr.FullPath) {
						if a.libExpandedMode == "netease" {
							pid := parseNeteaseID(a.libSingleIdInput)
							dr := ui.Column(c.Key(fmt.Sprintf("dr-nid-%d", idx))).FillWidth().Margin(2, 12, 8, 12).
								Padding(12, 14, 12, 14).Gap(10).Radius(14).Background(p.surface)
							dr.Draw(func(pt *ui.Painter, r ui.Rect) { pt.Stroke(r, p.hairline, 14, 1) })
							dr.Children(func() {
								ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
									ui.Text(c, "通过网易云歌曲 ID 直接下载歌词并对齐").FontSize(12.5).FontWeight(700).Grow(1)
									if pid != "" {
										statusPill(c, "✓ 已识别 ID: "+pid, p.okBg, p.ok)
									} else {
										statusPill(c, "支持纯数字或完整歌曲链接", p.surface2, p.muted)
									}
								})
								ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
									inBox := ui.Row(c).Grow(1).MinWidth(0).Padding(7, 10, 7, 10).Radius(10).AlignItems(ui.Center).Background(p.bg)
									inBox.Draw(func(pt *ui.Painter, r ui.Rect) { pt.Stroke(r, p.hairline, 10, 1) })
									inBox.Children(func() {
										ui.TextInputBase(c.Key(fmt.Sprintf("nid-in-%d", idx)), &a.libSingleIdInput).Grow(1).MinWidth(0).Height(22).
											FontSize(12.5).TextColor(p.text).Background(ui.Transparent).
											Placeholder("例如：30967650 或 https://music.163.com/#/song?id=30967650").FocusRing(false)
									})
									if pillButton(c.Key(fmt.Sprintf("nid-go-%d", idx)), nil, "下载歌词并对齐", p.accent, p.onAccent).Clicked() {
										a.enqueueSingleNetease(tr, a.libSingleIdInput)
									}
									if pillButton(c.Key(fmt.Sprintf("nid-close-%d", idx)), nil, "收起", p.surface2, p.muted).Clicked() {
										a.toggleInlineDrawer(tr, "netease")
									}
								})
							})
						} else if a.libExpandedMode == "manual" {
							nLines := countNonEmptyLines(a.libManualText)
							dr := ui.Column(c.Key(fmt.Sprintf("dr-man-%d", idx))).FillWidth().Margin(2, 12, 8, 12).
								Padding(12, 14, 12, 14).Gap(10).Radius(14).Background(p.surface)
							dr.Draw(func(pt *ui.Painter, r ui.Rect) { pt.Stroke(r, p.hairline, 14, 1) })
							dr.Children(func() {
								ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
									ui.Text(c, "手动输入参考歌词").FontSize(12.5).FontWeight(700)
									segmented(c.Key(fmt.Sprintf("man-lang-%d", idx)), &a.libManualLang, "中文", "日文", "英文")
									ui.Box(c).Grow(1)
									if pillButton(c.Key(fmt.Sprintf("man-paste-%d", idx)), nil, "从剪贴板粘贴", p.surface2, p.text).Clicked() {
										a.pasteClipboardToManual()
									}
									if pillButton(c.Key(fmt.Sprintf("man-imp-%d", idx)), nil, "导入 .txt/.lrc", p.surface2, p.text).Clicked() {
										a.importManualLyricsFile()
									}
									if pillButton(c.Key(fmt.Sprintf("man-np-%d", idx)), nil, "用记事本编辑", p.surface2, p.text).Clicked() {
										a.openManualLyricsInNotepad(tr)
									}
									if a.libManualFilePath != "" {
										if pillButton(c.Key(fmt.Sprintf("man-reload-%d", idx)), nil, "读取记事本", p.surface2, p.accent).Clicked() {
											a.reloadManualLyricsFromFile()
										}
									}
								})
								// Multi-line preview box (shows pasted/imported/notepad lyrics) + quick single-line input fallback
								prevBox := ui.Column(c.Key(fmt.Sprintf("man-prev-%d", idx))).FillWidth().Padding(10, 12, 10, 12).Gap(3).Radius(10).Background(p.bg)
								prevBox.Draw(func(pt *ui.Painter, r ui.Rect) { pt.Stroke(r, p.hairline, 10, 1) })
								prevBox.Children(func() {
									if nLines == 0 {
										ui.Text(c, "请点击上方「从剪贴板粘贴」、「导入 .txt/.lrc」或「用记事本编辑」填入歌词（纯文本即可，无需时间戳）…").
											FontSize(12).TextColor(p.faint)
									} else {
										lines := strings.Split(strings.ReplaceAll(a.libManualText, "\r\n", "\n"), "\n")
										shownL := 0
										for _, l := range lines {
											l = strings.TrimSpace(l)
											if l == "" {
												continue
											}
											ui.Text(c, l).FontSize(12.5).TextColor(p.text).SingleLine()
											shownL++
											if shownL >= 6 {
												if nLines > 6 {
													ui.Text(c, fmt.Sprintf("… 其余 %d 行已加载", nLines-6)).FontSize(11.5).TextColor(p.muted)
												}
												break
											}
										}
									}
								})
								ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
									ui.Text(c, fmt.Sprintf("当前 %d 行 · 将自动走 ASR 句定位 + 歌声逐字强制对齐", nLines)).FontSize(11.5).TextColor(p.muted).Grow(1)
									if pillButton(c.Key(fmt.Sprintf("man-cancel-%d", idx)), nil, "取消", p.surface2, p.muted).Clicked() {
										a.toggleInlineDrawer(tr, "manual")
									}
									if pillButton(c.Key(fmt.Sprintf("man-go-%d", idx)), nil, "保存参考词并开始对齐", p.accent, p.onAccent).Clicked() {
										a.enqueueSingleManual(tr)
									}
								})
							})
						}
					}
				}

				// 封面按需加载：Hero 前 3 首 + 当前页 + 下一页预取。
				// 一帧只提交一次，worker 解完再合并重绘。
				want := make([]string, 0, size+3)
				for i := 0; i < min(3, len(a.tracks)); i++ {
					want = append(want, a.tracks[i].FullPath)
				}
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
							if a.win != nil {
								a.win.Invalidate()
							}
						}
					}

					arrow("page-next", iconChevronR, a.libPage+1, a.libPage < pages-1)

					// 每页条数：按钮显示当前值，点开再选
					sizeBtn := ui.ButtonBase(c.Key("page-size-btn")).Height(30).PaddingX(12).Radius(999).
						Gap(5).AlignItems(ui.Center).Label(fmt.Sprintf("每页 %d 条", a.pageSize())).
						Background(p.accent.Alpha(0.10))
					sizeBtn.Children(func() {
						ui.Text(c, fmt.Sprintf("%d / 页", a.pageSize())).FontSize(12).FontWeight(600).
							TextColor(p.accent).SingleLine()
						// 始终朝下：表示「可以展开」
						ui.Icon(c, iconChevronD).Size(11, 11).TextColor(p.accent)
					})
					if sizeBtn.Clicked() {
						a.pageMenuOpen = !a.pageMenuOpen
						if a.win != nil {
							a.win.Invalidate()
						}
					}

					if a.pageMenuOpen {
						for _, n := range []int{50, 100} {
							size := n
							cur := a.pageSize() == size
							bg, fg := p.surface2, p.text
							if cur {
								bg, fg = p.accent.Alpha(0.10), p.accent
							}
							if pillButton(c.Key(fmt.Sprintf("page-size-%d", size)), nil,
								fmt.Sprintf("%d / 页", size), bg, fg).Clicked() {
								a.setPageSize(size)
								a.pageMenuOpen = false
								if a.win != nil {
									a.win.Invalidate()
								}
							}
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

// ==================== 2. 新建对齐 ====================
func (a *app) viewAlign(c *ui.Context) {
	p := a.pal
	sc := ui.Scroll(c.Key("align-scroll")).TrackScroll(&a.alignScroll).Fill()
	sc.Children(func() {
		col := ui.Column(c).FillWidth().Padding(pageGap, 38, 108, 38).Gap(18)
		col.Children(func() {
			ui.Column(c).Gap(2).Children(func() {
				ui.Text(c, "音频 → 逐字（卡拉 OK）LRC · 时间由对齐器从人声轨中计算").FontSize(11.5).FontWeight(600).TextColor(p.muted)
				ui.Text(c, "新建对齐").FontSize(28).FontWeight(800)
			})

			ui.Row(c.Key("align-grid")).Gap(18).AlignItems(ui.Start).Children(func() {
				// Left column: 3 steps
				ui.Column(c.Key("align-left")).Grow(1).Basis(0).Gap(14).Children(func() {
					// Step 1
					s1 := ui.Column(c.Key("step1")).FillWidth().Padding(18, 20, 18, 20).Gap(12).Radius(16).Background(p.surface)
					s1.Draw(func(pt *ui.Painter, r ui.Rect) { pt.Stroke(r, p.hairline, 16, 1) })
					s1.Children(func() {
						ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
							if a.alignAudio != "" || a.alignBatchDir != "" {
								statusPill(c, "1 · 已就绪", p.okBg, p.ok)
							} else {
								statusPill(c, "1 · 待选择", p.surface2, p.muted)
							}
							ui.Text(c, "音频文件或文件夹").FontSize(14).FontWeight(700)
							ui.Box(c).Grow(1)
							if pillButton(c.Key("chg-folder"), iconFolder, "选择文件夹（批量）", p.surface2, p.text).Clicked() {
								a.pickAlignFolder()
							}
							btnLabel := "选择单个音频"
							if a.alignAudio != "" {
								btnLabel = "更换音频"
							}
							if pillButton(c.Key("chg-audio"), iconNote, btnLabel, p.surface2, p.text).Clicked() {
								a.pickAlignAudio()
							}
						})
						drop := ui.Row(c).FillWidth().Padding(14, 16, 14, 16).Gap(14).Radius(12).AlignItems(ui.Center).Background(p.bg)
						drop.Draw(func(pt *ui.Painter, r ui.Rect) { pt.Stroke(r, p.hairline, 12, 1) })
						drop.Children(func() {
							if a.alignBatchDir != "" {
								artCover(c, hueForString(a.alignBatchDir), 46, 11, iconFolder)
								ui.Column(c).Grow(1).Gap(2).Children(func() {
									ui.Text(c, fmt.Sprintf("批量文件夹 · 共 %d 首待升级人声歌曲", len(a.alignBatchTracks))).FontSize(13.5).FontWeight(700).SingleLine()
									ui.Text(c, a.alignBatchDir).FontSize(11.5).TextColor(p.muted).SingleLine()
									ui.Text(c, "✓ 已自动排除纯音乐 / OST / 伴奏，优先使用同目录本地 LRC").FontSize(11).TextColor(p.ok).SingleLine()
								})
							} else if a.alignAudio == "" {
								artCover(c, 25, 42, 11, iconNote)
								ui.Column(c).Grow(1).Gap(2).Children(func() {
									ui.Text(c, "尚未选择本地音频或文件夹").FontSize(13.5).FontWeight(700)
									ui.Text(c, "点击右上角「选择文件夹（批量）」或「选择单个音频」，也可在「曲库」勾选多首歌曲后一键批量对齐").FontSize(11.5).TextColor(p.muted)
								})
							} else {
								artCover(c, hueForString(a.alignAudio), 46, 11, iconNote)
								ui.Column(c).Width(240).Gap(2).Children(func() {
									ui.Text(c, a.alignAudio).FontSize(13.5).FontWeight(700).SingleLine()
									ui.Text(c, a.alignArtist+" · "+a.alignTitle).FontSize(11.5).TextColor(p.muted).SingleLine()
									ui.Text(c, "输出："+a.alignOut).FontSize(11).TextColor(p.ok).SingleLine()
								})
								waveBox := ui.Box(c).Grow(1).Height(36)
								waveBox.Draw(func(pt *ui.Painter, r ui.Rect) {
									const bars = 46
									bw := r.W / float32(bars)
									for bi := range bars {
										amp := float32(0.25 + 0.65*math.Abs(math.Sin(float64(bi)*0.42)))
										bh := max(4, amp*(r.H-4))
										pt.Fill(ui.Rect{X: r.X + float32(bi)*bw, Y: r.Y + (r.H-bh)/2, W: max(2, bw-2), H: bh}, p.accent.Alpha(0.55), 1.5)
									}
								})
							}
						})
					})

					// Step 2: Lyric source
					s2 := ui.Column(c.Key("step2")).FillWidth().Padding(18, 20, 18, 20).Gap(12).Radius(16).Background(p.surface)
					s2.Draw(func(pt *ui.Painter, r ui.Rect) { pt.Stroke(r, p.hairline, 16, 1) })
					s2.Children(func() {
						ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
							statusPill(c, "2", p.accent.Alpha(0.14), p.accent)
							ui.Text(c, "歌词文本来源").FontSize(14).FontWeight(700)
							ui.Text(c, "默认优先读取同目录同名 .lrc，缺失时再自动搜网易云").FontSize(12).TextColor(p.muted)
						})
						segmented(c, &a.alignSource, "优先本地 LRC（无则搜网易云）", "手动指定参考文件", "强制搜网易云")
						infoBox := ui.Row(c).FillWidth().Padding(12, 14, 12, 14).Gap(10).Radius(10).AlignItems(ui.Center).Background(p.bg)
						infoBox.Draw(func(pt *ui.Painter, r ui.Rect) { pt.Stroke(r, p.hairline, 10, 1) })
						infoBox.Children(func() {
							if a.alignSource == 1 {
								if a.alignRefLrc != "" {
									statusPill(c, "✓ 已选参考", p.okBg, p.ok)
									ui.Text(c, a.alignRefLrc).Grow(1).FontSize(12).TextColor(p.text).SingleLine()
								} else {
									ui.Text(c, "尚未选择手动参考 LRC 文件").Grow(1).FontSize(12).TextColor(p.muted)
								}
								if pillButton(c.Key("pick-ref-lrc"), iconFolder, "选择参考 LRC", p.surface2, p.text).Clicked() {
									a.pickAlignRefLrc()
								}
							} else if a.alignAudio == "" && a.alignBatchDir == "" {
								ui.Text(c, "选择音频文件后，将优先读取同目录同名 .lrc（复用其逐行文本与句时间戳，跳过搜索与 ASR）；仅在无本地歌词时搜索网易云。").FontSize(12).TextColor(p.muted)
							} else if a.alignSource == 0 && a.alignLocalLrc != "" {
								statusPill(c, "✓ 命中本地 LRC", p.okBg, p.ok)
								ui.Text(c, "将直接使用同目录「"+a.alignLocalLrc+"」作为参考歌词与句时间戳，不搜网易云。").FontSize(12).TextColor(p.text)
							} else {
								ui.Text(c, fmt.Sprintf("将按关键词「%s」搜索网易云官方歌词（剪辑版缺段自动跳过）。", a.alignSearch)).FontSize(12).TextColor(p.text)
							}
						})
						// 冷门歌、同人曲、翻唱常常搜不到，ID 比搜索可靠
						ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
							ui.Column(c).Gap(1).Width(150).Shrink(0).Children(func() {
								ui.Text(c, "网易云歌曲 ID").FontSize(13).FontWeight(600)
								ui.Text(c, "可选，填了就跳过搜索").FontSize(11.5).TextColor(p.muted)
							})
							box := ui.Row(c).Grow(1).MinWidth(0).Padding(10, 8, 10, 8).Gap(8).Radius(9).AlignItems(ui.Center).Background(p.bg)
							box.Draw(func(pt *ui.Painter, r ui.Rect) { pt.Stroke(r, p.hairline, 9, 1) })
							box.Children(func() {
								ui.TextInputBase(c.Key("netease-id-in"), &a.alignNeteaseID).Grow(1).MinWidth(0).Height(26).
									FontSize(12.5).TextColor(p.text).Background(ui.Transparent).
									Placeholder("30967650 或 music.163.com/song?id=...").FocusRing(false)
							})
						})
						ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
							ui.Column(c).Grow(1).Gap(1).Children(func() {
								ui.Text(c, "忽略参考时间戳（强制 ASR 重定位）").FontSize(13).FontWeight(600)
								ui.Text(c, "粗糙整秒 LRC 会自动走 ASR；若遇时间偏移、鬼畜或剪辑版也可手动强制开启").FontSize(11.5).TextColor(p.muted)
							})
							settingSwitch(c.Key("sw-ignore-refts"), &a.alignIgnoreRefTimes, "忽略参考时间戳")
						})
						if !a.alignIgnoreRefTimes {
							ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
								ui.Column(c).Grow(1).Gap(1).Children(func() {
									ui.Text(c, "仅使用参考句首").FontSize(13).FontWeight(600)
									ui.Text(c, "沿用参考句首定位，重新对齐逐字时间与句尾").FontSize(11.5).TextColor(p.muted)
								})
								settingSwitch(c.Key("sw-ignore-refends"), &a.alignIgnoreRefEnds, "仅使用参考句首")
							})
						}
					})

					// Step 3: Language, Engine, KI-3, KI-5, KI-7
					s3 := ui.Column(c.Key("step3")).FillWidth().Padding(18, 20, 18, 20).Gap(12).Radius(16).Background(p.surface)
					s3.Draw(func(pt *ui.Painter, r ui.Rect) { pt.Stroke(r, p.hairline, 16, 1) })
					s3.Children(func() {
						ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
							statusPill(c, "3", p.accent.Alpha(0.14), p.accent)
							ui.Text(c, "语言与对齐引擎").FontSize(14).FontWeight(700)
						})
						segmented(c, &a.alignLang, "中文 (HubertFA)", "日文 (MMS 歌声)", "英文 (Echora MMS)", "其它")

						ui.Row(c).FillWidth().Padding(12, 14, 12, 14).Gap(12).Radius(12).AlignItems(ui.Center).Background(p.bg).Children(func() {
							artCover(c, 270, 36, 9, iconMic)
							ui.Column(c).Grow(1).Gap(2).Children(func() {
								if a.alignLang == 0 {
									ui.Text(c, "HubertFA 中文歌声强制对齐").FontSize(13.5).FontWeight(700)
									ui.Text(c, "专为歌声训练，长音不被压扁；与声学起音误差 < 0.1 s").FontSize(11.5).TextColor(p.muted)
								} else if a.alignLang == 1 {
									ui.Text(c, "NextFire MMS 日语卡拉 OK 歌声对齐 + MeCab 词法读音").FontSize(13.5).FontWeight(700)
									ui.Text(c, "在 demucs 分离人声轨上逐音素对齐，并用人声包络恢复长拖腔").FontSize(11.5).TextColor(p.muted)
								} else if a.alignLang == 2 {
									ui.Text(c, "Echora-300M MMS 英文歌词歌声对齐 + 能量时长保真").FontSize(13.5).FontWeight(700)
									ui.Text(c, "24s 长滑窗全上下文计算发射概率，按词/字母切分并保留长拖腔").FontSize(11.5).TextColor(p.muted)
								} else {
									ui.Text(c, "Echora-300M MMS 多语歌声对齐 + 能量时长保真").FontSize(13.5).FontWeight(700)
									ui.Text(c, "支持法/德/西/韩等多语种：自动拉丁化映射并在人声轨上逐字/逐词对齐").FontSize(11.5).TextColor(p.muted)
								}
							})
							statusPill(c, "就绪", p.okBg, p.ok)
						})

						ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
							ui.Column(c).Grow(1).Gap(1).Children(func() {
								ui.Text(c, "多音字读音检查").FontSize(13).FontWeight(600)
								ui.Text(c, "「了/着/得/还」等对齐前自动套用 polyphone.toml 覆盖词表").FontSize(11.5).TextColor(p.muted)
							})
							settingSwitch(c.Key("sw-poly"), &a.alignPolyCheck, "多音字读音检查")
						})

						ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
							ui.Column(c).Grow(1).Gap(1).Children(func() {
								ui.Text(c, "鬼畜 / 波形起音优先").FontSize(13).FontWeight(600)
								ui.Text(c, "适合鬼畜调音、人力 VOCALOID、重度电音：按人声波形起音与能量谷切分，模型提供锚点").FontSize(11.5).TextColor(p.muted)
							})
							settingSwitch(c.Key("sw-waveform-first"), &a.alignWaveformFirst, "鬼畜 / 波形起音优先")
						})

						notice := ui.Column(c).FillWidth().Padding(12, 14, 12, 14).Gap(8).Radius(12).Background(p.warnBg)
						notice.Children(func() {
							ui.Text(c, "外语歌曲对齐策略：自动保留/补全双语中文译文，并按人声包络恢复长拖腔").FontSize(12.5).FontWeight(600).TextColor(p.warn)
							ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
								settingSwitch(c.Key("sw-sustain"), &a.alignSustain, "时长保真")
								ui.Text(c, "按人声能量做时长保真").FontSize(12).TextColor(p.text)
								ui.Box(c).Grow(1)
								ui.Text(c, "英文粒度").FontSize(12).TextColor(p.muted)
								segmented(c, &a.alignEnMode, "按词", "按字母均分")
							})
						})
					})
				})

				// Right column: Output options & 6-step flow preview
				ui.Column(c.Key("align-right")).Width(320).Shrink(0).Gap(14).Children(func() {
					outCard := ui.Column(c.Key("out-card")).FillWidth().Padding(18, 20, 18, 20).Gap(12).Radius(16).Background(p.surface)
					outCard.Draw(func(pt *ui.Painter, r ui.Rect) { pt.Stroke(r, p.hairline, 16, 1) })
					outCard.Children(func() {
						ui.Text(c, "输出与选项").FontSize(14).FontWeight(700)
						outLabel := a.alignOut
						if outLabel == "" {
							outLabel = "选择音频后默认输出至同目录同名 .lrc"
						}
						ui.Text(c, outLabel).FontSize(12).TextColor(p.muted).SingleLine()
						ui.Row(c).AlignItems(ui.Center).Children(func() {
							ui.Text(c, "时间戳精度").Grow(1).FontSize(13)
							segmented(c, &a.alignDecimals, "3 位", "2 位")
						})
						ui.Row(c).AlignItems(ui.Center).Children(func() {
							ui.Text(c, "行尾结束标签").Grow(1).FontSize(13)
							settingSwitch(c.Key("sw-endtag"), &a.alignEndTag, "行尾结束标签")
						})
						ui.Row(c).AlignItems(ui.Center).Children(func() {
							ui.Text(c, "完成后自动校验").Grow(1).FontSize(13)
							settingSwitch(c.Key("sw-autovalid"), &a.alignAutoValid, "完成后自动校验")
						})
					})

					flowCard := ui.Column(c.Key("flow-card")).FillWidth().Padding(18, 20, 18, 20).Gap(10).Radius(16).Background(p.surface)
					flowCard.Draw(func(pt *ui.Painter, r ui.Rect) { pt.Stroke(r, p.hairline, 16, 1) })
					flowCard.Children(func() {
						ui.Text(c, "将执行的 6 步流程").FontSize(14).FontWeight(700)
						steps := []struct{ n, t, d string }{
							{"1", "确定歌词文本", "官方歌词 / 本地参考 / ASR"},
							{"2", "确定每句位置", "自带时间戳或 ASR 句子定位"},
							{"3", "demucs 人声分离", "htdemucs 44.1 kHz 分离人声轨"},
							{"4", "主唱检测", "人声 1–4kHz 占比，无主唱跳过"},
							{"5", "逐字强制对齐", "HubertFA（中文）/ MMS 歌声（日英）"},
							{"6", "输出并校验", "三位小数 · 行尾结束标签"},
						}
						for _, st := range steps {
							s := st
							ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
								statusPill(c, s.n, p.surface2, p.text)
								ui.Column(c).Gap(1).Children(func() {
									ui.Text(c, s.t).FontSize(12.5).FontWeight(700)
									ui.Text(c, s.d).FontSize(11).TextColor(p.muted)
								})
							})
						}
					})

					ui.Row(c).FillWidth().Gap(10).Children(func() {
						if pillButton(c.Key("btn-enq"), nil, "加入队列", p.surface2, p.text).Clicked() {
							if a.alignAudio == "" && a.alignBatchDir == "" {
								a.pickAlignFolder()
							} else {
								a.enqueueCurrentAlign()
								a.navigate(pageQueue, 1)
							}
						}
						startBtn := pillButton(c.Key("btn-start-align"), iconPlay, "开始对齐", p.accent, p.onAccent).Grow(1).Justify(ui.Center)
						if startBtn.Clicked() {
							if a.alignAudio == "" && a.alignBatchDir == "" {
								a.pickAlignFolder()
							} else {
								a.enqueueCurrentAlign()
								a.navigate(pageQueue, 1)
							}
						}
					})
				})
			})
		})
	})
}

// ==================== 3. 任务队列 ====================
func (a *app) viewQueue(c *ui.Context) {
	p := a.pal
	sc := ui.Scroll(c.Key("queue-scroll")).TrackScroll(&a.queueScroll).Fill()
	sc.Children(func() {
		col := ui.Column(c).FillWidth().Padding(pageGap, 38, 108, 38).Gap(18)
		col.Children(func() {
			totalTasks := len(a.queueItems)
			ui.Row(c).AlignItems(ui.End).Gap(12).Children(func() {
				ui.Column(c).Grow(1).Gap(2).Children(func() {
					ui.Text(c, fmt.Sprintf("当前共 %d 个任务 · 支持并发批量执行与失败不中断", totalTasks)).FontSize(11.5).FontWeight(600).TextColor(p.muted)
					ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "任务队列").FontSize(28).FontWeight(800)
						statusPill(c, "批量与进度", p.accent.Alpha(0.14), p.accent)
					})
				})
				ui.Text(c, "并发数").FontSize(12.5).TextColor(p.muted).SingleLine()
				prevConc := a.queueConcurrency
				segmented(c, &a.queueConcurrency, "1", "2", "3")
				if a.queueConcurrency != prevConc {
					a.savePrefs()
					if len(a.queueItems) > 0 {
						a.startQueueRunner()
					}
				}
				ui.Text(c, "失败不中断").FontSize(12.5).TextColor(p.muted).SingleLine()
				prevKG := a.queueKeepGoing
				settingSwitch(c.Key("q-keepgoing"), &a.queueKeepGoing, "失败不中断")
				if a.queueKeepGoing != prevKG {
					a.savePrefs()
				}
				if totalTasks > 0 {
					if pillButton(c.Key("q-clear"), nil, "清空队列", p.surface2, p.text).Clicked() {
						a.queueActiveTitle = ""
						a.queueActiveSub = ""
						a.queueItems = nil
						a.queueLogs = nil
					}
				}
			})

			if a.queueActiveTitle == "" && len(a.queueItems) == 0 {
				empty := ui.Column(c.Key("queue-empty")).FillWidth().Padding(56, 36, 56, 36).Gap(12).Radius(22).
					AlignItems(ui.Center).Background(p.surface)
				empty.Draw(func(pt *ui.Painter, r ui.Rect) { pt.Stroke(r, p.hairline, 22, 1) })
				empty.Children(func() {
					badge := ui.Box(c).Size(58, 58).Radius(18).Center().Background(p.accent.Alpha(0.14))
					badge.Children(func() { ui.Icon(c, iconQueue).Size(26, 26).TextColor(p.accent) })
					ui.Text(c, "当前没有正在执行的对齐任务").FontSize(19).FontWeight(800).Margin(4, 0, 0, 0)
					ui.Text(c, "你可以在「曲库」中扫描文件夹后一键批量升级整行歌词，或在「新建对齐」中选择单首音频加入队列。").
						FontSize(13).TextColor(p.muted).MaxWidth(520)
					ui.Row(c).Gap(10).Margin(10, 0, 0, 0).AlignItems(ui.Center).Children(func() {
						if pillButton(c.Key("q-go-lib"), iconLibrary, "前往曲库", p.accent, p.onAccent).Clicked() {
							a.navigate(pageLibrary, -1)
						}
						if pillButton(c.Key("q-go-align"), iconPlus, "新建单曲对齐", p.surface2, p.text).Clicked() {
							a.navigate(pageAlign, -1)
						}
					})
				})
				return
			}

			if a.queueActiveTitle != "" {
				// Active Job Card (clicking stage pills previews stage transition!)
				job := ui.Column(c.Key("active-job")).FillWidth().Padding(20, 22, 20, 22).Gap(16).Radius(18).Background(p.surface)
				job.Draw(func(pt *ui.Painter, r ui.Rect) { pt.Stroke(r, p.hairline, 18, 1) })
				job.Children(func() {
					var runningTitles []string
					for _, it := range a.queueItems {
						if it.Status == "running" {
							runningTitles = append(runningTitles, it.Title)
						}
					}
					headTitle := a.queueActiveTitle
					headSub := a.queueActiveSub
					if len(runningTitles) > 1 {
						headTitle = fmt.Sprintf("并发对齐中（%d 首）：%s", len(runningTitles), strings.Join(runningTitles, "、"))
						headSub = fmt.Sprintf("并发数 %d · 多进程同时运行中", a.queueConcurrency+1)
					}
					ui.Row(c).Gap(14).AlignItems(ui.Center).Children(func() {
						artCover(c, hueForString(a.queueActiveTitle), 52, 13, iconNote)
						ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
							ui.Text(c, headTitle).FontSize(18).FontWeight(800).SingleLine()
							ui.Text(c, headSub).FontSize(12).TextColor(p.muted).SingleLine()
						})
					})

					// 6-stage horizontal stepper
					stages := []struct{ idx int; t, d string }{
						{1, "1. 定歌词文本", "检索 / 读取歌词"},
						{2, "2. 定每句位置", "时间戳 / ASR 定位"},
						{3, "3. 人声分离", "htdemucs 分离"},
						{4, "4. 主唱检测", "1–4kHz 占比"},
						{5, "5. 逐字对齐", "HubertFA / MMS 歌声"},
						{6, "6. 输出校验", "三位小数 · 行尾标签"},
					}
					ui.Row(c.Key("stepper")).FillWidth().Gap(8).Children(func() {
						for _, st := range stages {
							s := st
							active := s.idx == a.queueStage
							card := ui.ButtonBase(c.Key(s.t)).Grow(1).Basis(0).Padding(10, 10, 10, 10).Gap(4).Radius(12).
								Column().AlignItems(ui.Start).Label(s.t).Selected(active).FocusRing(false)
							cardBg := p.bg
							if active {
								cardBg = p.accent.Alpha(0.11)
							} else if card.Hovered() {
								cardBg = p.hover
							}
							card.Background(card.AnimateColor("bg", cardBg, ui.Animation{Duration: 140 * time.Millisecond}))
							card.Draw(func(pt *ui.Painter, r ui.Rect) {
								if active {
									pt.Stroke(r, p.accent.Alpha(0.6), 12, 1.5)
								}
							})
							bg, fg := p.surface2, p.muted
							if s.idx < a.queueStage {
								bg, fg = p.okBg, p.ok
							} else if active {
								bg, fg = p.accent, p.onAccent
							}
							card.Children(func() {
								statusPill(c, s.t, bg, fg)
								ui.Text(c, s.d).FontSize(11.5).TextColor(p.muted).SingleLine()
							})
							if card.Clicked() {
								a.queueStage = s.idx
							}
						}
					})

					if len(a.queueLogs) > 0 {
						logBox := ui.Column(c.Key("q-log")).FillWidth().Padding(12, 14, 12, 14).Gap(4).Radius(12).Background(p.bg)
						logBox.Draw(func(pt *ui.Painter, r ui.Rect) { pt.Stroke(r, p.hairline, 12, 1) })
						logBox.Children(func() {
							for _, ln := range a.queueLogs {
								ui.Text(c, ln).FontSize(12).TextColor(p.muted).SingleLine()
							}
						})
					}
				})
			}

			if len(a.queueItems) > 0 {
				ui.Text(c, fmt.Sprintf("队列明细（%d 首）", len(a.queueItems))).FontSize(16).FontWeight(800).Margin(4, 0, 0, 0)
				qCard := ui.Column(c.Key("q-items")).FillWidth().Gap(6)
				qCard.Children(func() {
					for qi, item := range a.queueItems {
						it := item
						row := ui.Row(c.Key(fmt.Sprintf("qi-%d", qi))).FillWidth().Height(56).PaddingX(14).Gap(14).Radius(14).
							AlignItems(ui.Center)
						rBg := p.surface
						if row.Hovered() {
							rBg = p.surface2
						}
						row.Background(row.AnimateColor("bg", rBg, ui.Animation{Duration: 130 * time.Millisecond}))
						row.Draw(func(pt *ui.Painter, r ui.Rect) { pt.Stroke(r, p.hairline, 14, 1) })
						row.Children(func() {
							artCover(c, it.Hue, 38, 9, nil)
							ui.Column(c).Grow(1).Gap(2).Children(func() {
								ui.Text(c, it.Title).FontSize(13.5).FontWeight(700)
								ui.Text(c, it.Sub).FontSize(11.5).TextColor(p.muted)
							})
							switch it.Status {
							case "running":
								st := it.Stage
								if st < 1 {
									st = 1
								}
								statusPill(c, fmt.Sprintf("对齐中 %d/6", st), p.accent.Alpha(0.16), p.accent)
							case "done":
								statusPill(c, "已完成", p.okBg, p.ok)
							case "skipped":
								statusPill(c, "无主唱 · 跳过", p.warnBg, p.warn)
							case "skipped_nolyrics":
								statusPill(c, "全网无词 · 已跳过", p.warnBg, p.warn)
							case "failed":
								statusPill(c, "失败", p.warnBg, p.warn)
							default:
								statusPill(c, "等待中", p.surface2, p.muted)
							}
							ui.Text(c, it.Note).Width(220).FontSize(12).TextColor(p.muted).SingleLine()
							if it.Status == "done" && it.LrcPath != "" {
								if pillButton(c.Key(fmt.Sprintf("qi-ed-%d", qi)), nil, "打开校对", p.surface2, p.text).Clicked() {
									_ = a.loadLrcFile(it.LrcPath)
									a.navigate(pageEditor, 1)
								}
							}
						})
					}
				})
			}
		})
	})
}

// ==================== 4. 编辑与校对 ====================
func (a *app) viewEditor(c *ui.Context) {
	p := a.pal
	if a.edFile == "" || len(a.edLines) == 0 || len(a.edGlyphs) == 0 {
		sc := ui.Scroll(c.Key("ed-empty-scroll")).Fill()
		sc.Children(func() {
			col := ui.Column(c).FillWidth().Padding(pageGap, 38, 108, 38).Gap(16)
			col.Children(func() {
				ui.Column(c).Gap(2).Children(func() {
					ui.Text(c, "人声波形对照 · 逐字时间戳拖拽微调").FontSize(11.5).FontWeight(600).TextColor(p.muted)
					ui.Text(c, "编辑与校对").FontSize(28).FontWeight(800)
				})
				empty := ui.Column(c.Key("ed-empty-card")).FillWidth().Padding(56, 36, 56, 36).Gap(12).Radius(22).
					AlignItems(ui.Center).Background(p.surface)
				empty.Draw(func(pt *ui.Painter, r ui.Rect) { pt.Stroke(r, p.hairline, 22, 1) })
				empty.Children(func() {
					badge := ui.Box(c).Size(58, 58).Radius(18).Center().Background(p.accent.Alpha(0.14))
					badge.Children(func() { ui.Icon(c, iconEdit).Size(26, 26).TextColor(p.accent) })
					ui.Text(c, "尚未打开任何 LRC 歌词文件").FontSize(19).FontWeight(800).Margin(4, 0, 0, 0)
					ui.Text(c, "点击下方按钮打开本地 .lrc 文件进行逐字校对，或从「曲库」列表点击任意歌曲右侧的校对图标进入。打开后的文件会自动记录到左侧「最近校对」。").
						FontSize(13).TextColor(p.muted).MaxWidth(520)
					ui.Row(c).Gap(10).Margin(10, 0, 0, 0).AlignItems(ui.Center).Children(func() {
						if pillButton(c.Key("ed-open-file"), iconFolder, "打开本地 LRC 文件", p.accent, p.onAccent).Clicked() {
							a.pickLrcFile()
						}
						if pillButton(c.Key("ed-go-lib"), iconLibrary, "前往曲库选择", p.surface2, p.text).Clicked() {
							a.navigate(pageLibrary, -1)
						}
					})
				})
			})
		})
		return
	}
	if a.edSelectedLine < 0 || a.edSelectedLine >= len(a.edLines) {
		a.edSelectedLine = 0
	}
	if a.edSelectedChar < 0 || a.edSelectedChar >= len(a.edGlyphs) {
		a.edSelectedChar = 0
	}
	curLine := a.edLines[a.edSelectedLine]
	curGlyph := a.edGlyphs[a.edSelectedChar]

	ed := ui.Column(c.Key("editor-root")).Fill()
	ed.Children(func() {
		// 1) Top command bar (DragWindow allows dragging the window from any empty space in the top bar)
		top := ui.Row(c.Key("ed-top")).FillWidth().Height(52).Padding(0, c.TitleBar().Right+52, 0, 18).Gap(8).AlignItems(ui.Center).DragWindow()
		top.Draw(func(pt *ui.Painter, r ui.Rect) {
			pt.Fill(ui.Rect{X: r.X, Y: r.Y + r.H - 1, W: r.W, H: 1}, p.hairline, 0)
		})
		top.Children(func() {
			// Align the left header block [18px pad + 250px = 268px] pixel-perfectly with the 268px 歌词行 column below!
			ui.Row(c).Width(250).Shrink(0).Gap(8).AlignItems(ui.Center).Children(func() {
				if a.edCover != nil {
					ui.Image(c, a.edCover).Size(28, 28).Radius(7).Fit(ui.Cover).Shrink(0)
				} else {
					artCover(c, hueForString(a.edFile), 28, 7, nil)
				}
				const boxW = float32(214)
				ui.Column(c).Width(boxW).Shrink(0).Children(func() {
					title := a.edFile
					if a.edModified {
						title += " · 已修改"
					}
					// 只滚动歌名，总宽度与下方 268px 歌词行右边界对齐
					mBox := ui.Box(c.Key("ed-title-marquee")).FillWidth().Height(18).ClipX()
					now := time.Now()
					if a.edMarqueeText != title || a.edMarqueeStart.IsZero() {
						a.edMarqueeText = title
						a.edMarqueeStart = now
					}
					tw, _ := c.MeasureText(0, ui.Span{Text: title, Size: 13, Weight: 700})
					run := tw - boxW
					if run <= 1 {
						mBox.Children(func() {
							ui.Text(c, title).FontSize(13).FontWeight(700).SingleLine()
						})
					} else {
						const hold = 1800 * time.Millisecond
						const speed = 34.0 // px/s
						slide := time.Duration(float64(run)/speed*float64(time.Second))
						if slide < 400*time.Millisecond {
							slide = 400 * time.Millisecond
						}
						round := 2*hold + 2*slide
						t := now.Sub(a.edMarqueeStart) % round
						var off float32
						switch {
						case t < hold:
							off = 0
							c.After(hold - t)
						case t < hold+slide:
							k := float64(t-hold) / float64(slide)
							off = run * float32(k*k*(3-2*k))
							c.AnimationFrame()
						case t < 2*hold+slide:
							off = run
							c.After(2*hold + slide - t)
						default:
							k := float64(t-2*hold-slide) / float64(slide)
							off = run * float32(1-k*k*(3-2*k))
							c.AnimationFrame()
						}
						mBox.Children(func() {
							ui.Text(c, title).Width(tw + 8).Shrink(0).FontSize(13).FontWeight(700).NoWrap().Translate(-off, 0)
						})
					}
					ui.Text(c, a.edSub).FontSize(11).TextColor(p.muted).SingleLine()
				})
			})
			if pillButton(c.Key("ed-open"), nil, "打开 LRC", p.surface, p.text).Clicked() {
				a.pickLrcFile()
			}
			refBtn := ui.ButtonBase(c.Key("ed-refresh")).Size(34, 34).Radius(17).Center().
				Label("从磁盘重新加载 LRC").FocusRing(false)
			refBg := p.surface
			if refBtn.Hovered() {
				refBg = p.surface2
			}
			refBtn.Background(refBtn.AnimateColor("bg", refBg, ui.Animation{Duration: 120 * time.Millisecond}))
			refBtn.Children(func() { ui.Icon(c, iconRefresh).Size(14, 14).TextColor(p.text) })
			if refBtn.Clicked() && a.edPath != "" {
				a.refreshEditor()
			}
			if pillButton(c.Key("ed-valid"), nil, "校验", p.surface, p.text).Clicked() && a.edPath != "" {
				a.validateCurrentLrc()
			}
			if pillButton(c.Key("ed-heart"), nil, "加 ❤️", p.surface, p.text).Clicked() && a.edPath != "" {
				a.heartsCurrentLrc()
			}
			ui.Box(c).Grow(1).FillHeight().DragWindow()
			prevTrack := a.edTrackMode
			segmented(c, &a.edTrackMode, "原曲", "人声轨")
			if a.edTrackMode != prevTrack {
				a.switchTrackMode(a.edTrackMode)
			}
			if a.edModified && a.edPath != "" {
				if pillButton(c.Key("ed-revert"), nil, "放弃修改", p.surface, p.muted).Clicked() {
					prevLine := a.edSelectedLine
					_ = a.loadLrcFile(a.edPath)
					if prevLine >= 0 && prevLine < len(a.edLines) {
						a.selectEditorLine(prevLine)
					}
				}
			}
			if pillButton(c.Key("ed-export"), nil, "保存 LRC", p.accent, p.onAccent).Clicked() {
				a.saveLrcFile()
			}
		})

		// 2) Main 3-column workspace
		mid := ui.Row(c.Key("ed-mid")).Grow(1).Basis(0).MinHeight(0).FillWidth().AlignItems(ui.Stretch).Clip()
		mid.Children(func() {
			// Left: Lyric lines list (268px) with Cloudy sliding linePillMatch
			left := ui.Column(c.Key("ed-lines")).Width(268).Shrink(0).Padding(8, 6, 8, 6).Gap(2)
			left.Draw(func(pt *ui.Painter, r ui.Rect) {
				pt.Fill(ui.Rect{X: r.X + r.W - 1, Y: r.Y, W: 1, H: r.H}, p.hairline, 0)
			})
			left.Children(func() {
				ui.Row(c).Padding(4, 8, 8, 8).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "歌词行").FontSize(13).FontWeight(700).Grow(1)
					if a.editorNeedsTranslation() {
						if a.edTranslating {
							statusPill(c, "翻译中…", p.accent.Alpha(0.14), p.accent)
						} else {
							bg, fg := p.accent.Alpha(0.14), p.accent
							if a.editorHasTranslation() {
								bg, fg = p.surface2, p.muted
							}
							aiBtn := ui.ButtonBase(c.Key("ed-ai-translate")).Height(22).PaddingX(9).Radius(999).Center().
								Label("AI 翻译歌词").FocusRing(false)
							fill := bg
							if aiBtn.Hovered() {
								if a.editorHasTranslation() {
									fill = p.hover
									fg = p.text
								} else {
									fill = p.accent.Alpha(0.22)
								}
							}
							aiBtn.Background(aiBtn.AnimateColor("bg", fill, ui.Animation{Duration: 120 * time.Millisecond}))
							pose := ui.Pose{}
							if aiBtn.Pressed() {
								pose.Scale = 0.94
							}
							aiBtn.AnimatePose(pose, ui.Snappy)
							aiBtn.Children(func() {
								ui.Text(c, "翻译").FontSize(11.5).FontWeight(700).TextColor(fg).SingleLine()
							})
							if aiBtn.Clicked() {
								a.translateCurrentLyrics()
							}
						}
					}
					ui.Text(c, fmt.Sprintf("%d 行", len(a.edLines))).FontSize(11.5).TextColor(p.muted)
				})
				lsc := ui.Scroll(c.Key("ed-line-scroll")).TrackScroll(&a.edLineScroll).Grow(1).FillWidth()
				lsc.Children(func() {
					ui.Column(c).FillWidth().Gap(2).Children(func() {
						for i, ln := range a.edLines {
							idx, line := i, ln
							sel := a.edSelectedLine == idx
							if a.edEditingLine == idx {
								editRow := ui.Row(c.Key(fmt.Sprintf("ed-ln-edit-%d", idx))).FillWidth().Height(36).PaddingX(8).Gap(6).Radius(8).
									AlignItems(ui.Center).Background(p.bg)
								editRow.Draw(func(pt *ui.Painter, r ui.Rect) {
									pt.Stroke(r, p.accent, 8, 1.5)
								})
								editRow.Children(func() {
									ui.Text(c, fmt.Sprintf("%d", line.Index)).Width(18).FontSize(11).TextColor(p.accent)
									in := ui.TextInputBase(c.Key(fmt.Sprintf("ed-ln-in-%d", idx)), &a.edEditingText).Grow(1).MinWidth(0).Height(24).
										FontSize(12.5).TextColor(p.text).Background(ui.Transparent).AutoFocus().FocusRing(false)
									if in.Submitted() {
										a.commitEditLyricLine()
									}
									okBtn := ui.ButtonBase(c.Key(fmt.Sprintf("ed-ln-ok-%d", idx))).Size(22, 22).Radius(6).Center().
										Background(p.accent).Label("确认修改").FocusRing(false)
									okBtn.Children(func() { ui.Icon(c, iconCheck).Size(12, 12).TextColor(p.onAccent) })
									if okBtn.Clicked() {
										a.commitEditLyricLine()
									}
								})
								continue
							}
							rowH := float32(31)
							if line.Translation != "" {
								rowH = 44
							}
							row := ui.ButtonBase(c.Key(fmt.Sprintf("ed-ln-%d", idx))).FillWidth().Height(rowH).PaddingX(10).Gap(6).Radius(8).
								AlignItems(ui.Center).Label(line.Text).Selected(sel).FocusRing(false)
							row.ContextMenu(func(m *ui.Menu) {
								if m.Item("编辑此行歌词").Chosen() {
									a.beginEditLyricLine(idx)
								}
							})
							bg := ui.Transparent
							if row.Hovered() && !sel {
								bg = p.hover
							}
							row.Background(row.AnimateColor("bg", bg, ui.Animation{Duration: 120 * time.Millisecond}))
							lPose := ui.Pose{}
							if row.Pressed() {
								lPose.Scale = 0.98
							}
							row.AnimatePose(lPose, ui.Snappy)
							row.Children(func() {
								if sel {
									pill := ui.Box(c.Key("line-pill")).Absolute().Top(0).Left(0).Right(0).Bottom(0).Radius(8).
										PassThrough().Background(p.accent.Alpha(0.14))
									pill.Draw(func(pt *ui.Painter, r ui.Rect) {
										bar := ui.Rect{X: r.X + 2.5, Y: r.Y + (r.H-14)/2, W: 3, H: 14}
										pt.Fill(bar, p.accent, 1.5)
									})
									pill.Transition(ui.ElementTransition{Animation: &motionPane, Match: linePillMatch{}})
								}
								ui.Text(c, fmt.Sprintf("%d", line.Index)).Width(22).FontSize(11).TextColor(p.faint)
								ui.Text(c, fmtSec(line.Start)).Width(64).FontSize(11).TextColor(p.muted).SingleLine()
								txCol, weight := p.text, 500
								if sel {
									weight = 700
								}
								if line.Translation != "" {
									ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
										ui.Text(c, line.Text).FontSize(12.5).FontWeight(weight).TextColor(txCol).SingleLine()
										ui.Text(c, line.Translation).FontSize(11).TextColor(p.muted).SingleLine()
									})
								} else {
									ui.Text(c, line.Text).Grow(1).FontSize(12.5).FontWeight(weight).TextColor(txCol).SingleLine()
								}
							})
							if row.DoubleClicked() {
								a.beginEditLyricLine(idx)
							} else if row.Clicked() {
								a.selectEditorLine(idx)
								a.seekPlayback(line.Start)
							}
						}
					})
				})
			})

			// Center: Cloudy Now-Playing-style rhythm preview stage
			stage := ui.Column(c.Key("ed-stage")).Grow(1).MinWidth(0).Padding(12, 16, 10, 16)
			stage.Draw(func(pt *ui.Painter, r ui.Rect) {
				if a.edCover == nil {
					glow := ui.Rect{X: r.X + r.W*0.18, Y: r.Y + r.H*0.2, W: r.W * 0.64, H: r.H * 0.6}
					pt.Fill(glow, p.accent.Alpha(0.06), 120)
				}
			})
			stage.Children(func() {
				ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
					prevMode := a.edPreviewMode
					segmented(c, &a.edPreviewMode, "律动预览", "LRC 源码")
					if a.edPreviewMode != prevMode && a.edPreviewMode == 0 {
						a.edSrcEditing = false
					}
					if a.edPreviewMode == 1 {
						srcBtn := func(key, label string, bg, fg ui.Color) ui.Element {
							b := ui.ButtonBase(c.Key(key)).Height(28).PaddingX(12).Radius(999).Center().
								Background(bg).Label(label).FocusRing(false)
							b.Children(func() {
								ui.Text(c, label).FontSize(12).FontWeight(700).TextColor(fg).SingleLine()
							})
							return b
						}
						if !a.edSrcEditing {
							if srcBtn("ed-src-edit", "编辑", p.surface2, p.text).Clicked() {
								a.beginEditLrcSource()
							}
						} else {
							if srcBtn("ed-src-cancel", "放弃", p.surface2, p.muted).Clicked() {
								a.cancelEditLrcSource()
							}
							if srcBtn("ed-src-save", "保存", p.accent, p.onAccent).Clicked() {
								a.saveEditLrcSource()
							}
						}
					}
					ui.Box(c).Grow(1)
					ui.Text(c, fmtSec(curGlyph.Start)+" / "+fmtSec(curLine.End)).FontSize(12).TextColor(p.muted)
				})

				if a.edPreviewMode == 1 {
					srcCard := ui.Box(c.Key("ed-src-box")).Grow(1).FillWidth().Margin(10, 0, 8, 0).Radius(14).Background(p.surface)
					srcCard.Draw(func(pt *ui.Painter, r ui.Rect) {
						if a.edSrcEditing {
							pt.Stroke(r, p.accent.Alpha(0.38), 14, 1)
						} else {
							pt.Stroke(r, p.hairline, 14, 1)
						}
					})
					srcCard.Children(func() {
						if a.edSrcEditing {
							ta := ui.TextAreaBase(c.Key("ed-src-textarea"), &a.edSrcText).Fill().Padding(14, 20, 14, 20).
								FontSize(12).LineHeight(1.75).TextColor(p.text.Alpha(0.80)).
								SelectionColor(p.accent.Alpha(0.18)).Background(ui.Transparent).AutoFocus().FocusRing(false)
							if a.edSrcNeedCaret {
								ta.SetTextSelection(a.edSrcInitCaret, a.edSrcInitCaret)
								a.edSrcNeedCaret = false
							}
						} else {
							ssc := ui.Scroll(c.Key("ed-src-scroll")).TrackScroll(&a.edSrcScroll).Fill()
							ssc.Children(func() {
								ui.Column(c).FillWidth().Padding(10, 14, 10, 14).Gap(3).Children(func() {
									for si, ln := range a.edLines {
										sIdx, line := si, ln
										isCur := sIdx == a.edSelectedLine
										bg := ui.Transparent
										fg := p.muted
										weight := 500
										if isCur {
											bg = p.accent.Alpha(0.12)
											fg = p.accent
											weight = 700
										}
										sRow := ui.ButtonBase(c.Key(fmt.Sprintf("src-ln-%d", sIdx))).FillWidth().Padding(4, 8, 4, 8).Radius(6).
											Justify(ui.Start).Background(bg).Label(line.Text).FocusRing(false)
										if isCur && a.edSrcScrollToSelected {
											sb := ssc.LayoutBounds()
											rb := sRow.LayoutBounds()
											if sb.H > 0 && rb.H > 0 {
												delta := (rb.Y + rb.H/2) - (sb.Y + sb.H/2)
												a.edSrcScroll.Y = max(0, a.edSrcScroll.Y+delta)
												a.edSrcScrollToSelected = false
											}
										}
										sRow.Children(func() {
											ui.Text(c, formatLrcLine(line)).FontSize(12).FontWeight(weight).TextColor(fg)
										})
										if sRow.Clicked() {
											a.selectEditorLine(sIdx)
											a.seekPlayback(line.Start)
										}
									}
								})
							})
						}
					})
				} else {
					previewArea := ui.Box(c.Key("ed-pv-area")).Grow(1).FillWidth().ClipX()
					previewArea.Children(func() {
						// Layer 1 (back): spinning vinyl record + circular album cover
						if a.edCover != nil {
							now := time.Now()
							if a.edPlaying {
								if !a.edVinylLast.IsZero() {
									dt := float32(now.Sub(a.edVinylLast).Seconds())
									if dt > 0 && dt < 0.25 {
										a.edVinylDeg = float32(math.Mod(float64(a.edVinylDeg+dt*18.0), 360.0))
									}
								}
								a.edVinylLast = now
								c.AnimationFrame()
							} else {
								a.edVinylLast = time.Time{}
							}
							coverWrap := ui.Box(c.Key("ed-cover-wrap")).Absolute().Top(0).Left(0).Right(0).Bottom(0).Center().PassThrough()
							coverWrap.Children(func() {
								vinyl := ui.Box(c.Key("ed-vinyl")).Size(340, 340).Radius(170).Center().Rotate(a.edVinylDeg).PassThrough()
								dark := a.appearance == 1
								vinyl.Draw(func(pt *ui.Painter, r ui.Rect) {
									discAlpha := float32(0.14)
									grooveAlpha := float32(0.08)
									rimAlpha := float32(0.14)
									if dark {
										discAlpha = 0.44
										grooveAlpha = 0.16
										rimAlpha = 0.22
									}
									pt.Fill(r, ui.Hex("#16161c").Alpha(discAlpha), 170)
									pt.Stroke(r, p.text.Alpha(rimAlpha), 170, 1.5)
									for _, inset := range []float32{12, 22, 32, 42, 52} {
										gr := ui.Rect{X: r.X + inset, Y: r.Y + inset, W: r.W - 2*inset, H: r.H - 2*inset}
										pt.Stroke(gr, p.text.Alpha(grooveAlpha), gr.W/2, 1)
									}
								})
								vinyl.Children(func() {
									artAlpha := float32(0.32)
									if dark {
										artAlpha = 0.42
									}
									artCircle := ui.Box(c.Key("ed-vinyl-art")).Size(216, 216).Radius(108).Center().PassThrough()
									artCircle.Children(func() {
										ui.Image(c, a.edCover).Absolute().Top(0).Left(0).Right(0).Bottom(0).Radius(108).Fit(ui.Cover).Opacity(artAlpha).PassThrough()
									})
									artCircle.Draw(func(pt *ui.Painter, r ui.Rect) {
										pt.Stroke(r, ui.Hex("#16161c").Alpha(0.22), 108, 2)
									})
								})
							})
						}
						// Layer 2 (front): karaoke lyrics — rendered AFTER coverWrap as Absolute so text ALWAYS paints crisp on top!
						ui.Column(c.Key("ed-pv-fg")).Absolute().Top(0).Left(0).Right(0).Bottom(0).Center().Gap(12).Children(func() {
						if a.edSelectedLine > 0 {
							ui.Text(c, a.edLines[a.edSelectedLine-1].Text).FontSize(17).FontWeight(600).TextColor(p.faint).SingleLine()
						}
						// Active character-by-character karaoke line (dynamically adapts wrap width to actual stage pixel width on window resize!)
						charFontSize := float32(30)
						if len(a.edGlyphs) > 20 {
							charFontSize = 24
						}
						stageW := previewArea.LayoutBounds().W
						if stageW < 120 {
							stageW = 560
						}
						cellPx := float64(charFontSize) + 8.0
						if a.edShowPinyin && cellPx < 39.0 {
							cellPx = 39.0
						}
						maxRowUnits := max(6.0, float64(stageW-36)/cellPx)

						glyphWidthUnits := func(s string) float64 {
							rs := []rune(s)
							if len(rs) == 0 {
								return 0.5
							}
							total := 0.0
							for _, ch := range rs {
								if ch < 128 {
									total += 0.65
								} else {
									total += 1.0
								}
							}
							return total
						}
						var glyphRows [][]int
						var curRow []int
						rowUnits := 0.0
						for gi, g := range a.edGlyphs {
							w := glyphWidthUnits(g.Ch)
							isSpace := strings.TrimSpace(g.Ch) == ""
							if len(curRow) > 0 && rowUnits+w > maxRowUnits {
								// Try to wrap cleanly at the last space in curRow so English words don't break mid-word
								splitAt := -1
								for k := len(curRow) - 1; k >= 0; k-- {
									if strings.TrimSpace(a.edGlyphs[curRow[k]].Ch) == "" {
										splitAt = k
										break
									}
								}
								if splitAt > 0 {
									glyphRows = append(glyphRows, curRow[:splitAt])
									tail := append([]int(nil), curRow[splitAt+1:]...)
									curRow = tail
									rowUnits = 0
									for _, ti := range curRow {
										rowUnits += glyphWidthUnits(a.edGlyphs[ti].Ch)
									}
								} else {
									glyphRows = append(glyphRows, curRow)
									curRow = nil
									rowUnits = 0
								}
							}
							if isSpace && len(curRow) == 0 {
								continue
							}
							curRow = append(curRow, gi)
							rowUnits += w
						}
						if len(curRow) > 0 {
							glyphRows = append(glyphRows, curRow)
						}

						ui.Column(c.Key("pv-chars")).Gap(6).AlignItems(ui.Center).Children(func() {
							for rIdx, rowIdxs := range glyphRows {
								ri, idxs := rIdx, rowIdxs
								ui.Row(c.Key(fmt.Sprintf("pv-row-%d", ri))).Gap(2).AlignItems(ui.End).Children(func() {
									for _, gIdx := range idxs {
										idx := gIdx
										glyph := a.edGlyphs[idx]
										sung := idx <= a.edSelectedChar
										isCur := idx == a.edSelectedChar
										if a.edPlaying {
											sung = a.edPlayhead >= glyph.Start
											isCur = a.edPlayhead >= glyph.Start && a.edPlayhead < glyph.End
										}
										isSpace := strings.TrimSpace(glyph.Ch) == ""
										padX := float32(2)
										if isSpace {
											padX = 4
										}
										cBtn := ui.ButtonBase(c.Key(fmt.Sprintf("pvc-%d", idx))).Padding(3, padX, 3, padX).Radius(8).
											Column().AlignItems(ui.Center).Gap(1).Background(ui.Transparent).Label(glyph.Ch).FocusRing(false)
										cPose := ui.Pose{}
										if isCur || cBtn.Hovered() {
											cPose.Y = -3
											cPose.Scale = 1.06
										}
										cBtn.AnimatePose(cPose, ui.Snappy)
										col := p.faint
										if sung {
											col = p.accent
										}
										// Show timestamp above character (avoid repeating identical timestamps on every letter of an English word)
										showTime := a.edShowPinyin && !isSpace
										if showTime && idx > 0 && math.Abs(glyph.Start-a.edGlyphs[idx-1].Start) < 0.005 && strings.TrimSpace(a.edGlyphs[idx-1].Ch) != "" {
											showTime = false
										}
										cBtn.Children(func() {
											if showTime {
												sub := glyph.Pinyin
												if sub == "" {
													sub = fmt.Sprintf("%.2fs", glyph.Start)
												}
												ui.Text(c, sub).FontSize(10).TextColor(col).SingleLine()
											}
											ui.Text(c, glyph.Ch).FontSize(charFontSize).FontWeight(800).TextColor(col).SingleLine()
										})
										if cBtn.Clicked() {
											a.edSelectedChar = idx
											a.seekPlayback(glyph.Start)
										}
									}
								})
							}
						})
						if curLine.Translation != "" {
							ui.Text(c, curLine.Translation).FontSize(15.5).FontWeight(600).TextColor(p.muted).SingleLine()
						}
						if a.edSelectedLine+1 < len(a.edLines) {
							ui.Text(c, a.edLines[a.edSelectedLine+1].Text).FontSize(16.5).FontWeight(600).TextColor(p.faint).SingleLine()
						}
					})
					})
				}

				// Full-song time progress bar (shows live position even during silent intros!)
				ui.Row(c.Key("ed-song-bar")).FillWidth().Height(22).Gap(10).AlignItems(ui.Center).Children(func() {
					songDur := a.dockDur
					if len(a.edLines) > 0 && a.edLines[len(a.edLines)-1].End > songDur {
						songDur = a.edLines[len(a.edLines)-1].End
					}
					if songDur < 1.0 {
						songDur = 1.0
					}
					curPos := max(0.0, min(a.edPlayhead, songDur))
					ui.Text(c, fmt.Sprintf("%02d:%02d", int(curPos)/60, int(curPos)%60)).Width(36).FontSize(11).TextColor(p.muted).SingleLine()
					scrub := ui.SliderBase(c.Key("ed-song-scrub"), &a.edPlayhead, 0, songDur).Grow(1).Height(18).
						Cursor(ui.CursorPointer).Label("歌曲播放进度").FocusRing(false)
					if scrub.Changed() {
						a.seekPlayback(a.edPlayhead)
						// Jump to matching lyric line if user scrubbed to another part of the song
						bestLn := 0
						for li, ln := range a.edLines {
							if a.edPlayhead >= ln.Start {
								bestLn = li
							}
						}
						if bestLn != a.edSelectedLine {
							a.selectEditorLine(bestLn)
						}
					}
					hot := scrub.Hovered() || scrub.Pressed()
					thick := scrub.AnimateTo("th", map[bool]float32{true: 6, false: 4}[hot], ui.Snappy)
					knob := scrub.AnimateTo("kn", b2f(hot), ui.Snappy)
					scrub.Draw(func(pt *ui.Painter, r ui.Rect) {
						livePos, liveDur := a.edPlayhead, songDur
						if a.edPlaying && a.player != nil {
							if lp, ld := a.player.Position(); ld > 0 {
								livePos = lp
								liveDur = ld
							}
							pt.After(16 * time.Millisecond)
						}
						frac := clamp01(float32(livePos / max(liveDur, 1.0)))
						y := r.Y + (r.H-thick)/2
						pt.Fill(ui.Rect{X: r.X, Y: y, W: r.W, H: thick}, p.surface2, thick/2)
						pw := max(thick, r.W*frac)
						pt.Fill(ui.Rect{X: r.X, Y: y, W: pw, H: thick}, p.accent, thick/2)
						if knob > 0.01 {
							d := 10 * knob
							pt.Fill(ui.Rect{X: r.X + r.W*frac - d/2, Y: r.Y + (r.H-d)/2, W: d, H: d}, p.accent, d/2)
						}
					})
					ui.Text(c, fmt.Sprintf("%02d:%02d", int(songDur)/60, int(songDur)%60)).Width(36).FontSize(11).TextColor(p.faint).SingleLine()
				})

				// Stage footer transport bar
				ui.Row(c).FillWidth().Height(38).Gap(6).AlignItems(ui.Center).Children(func() {
					if iconButton(c.Key("st-prev"), iconPrev, 13, p.muted, "上一字").Clicked() && a.edSelectedChar > 0 {
						a.edSelectedChar--
						a.seekPlayback(a.edGlyphs[a.edSelectedChar].Start)
					}
					pbtn := ui.ButtonBase(c.Key("st-play")).Size(34, 34).Radius(17).Center().
						Background(p.text).TextColor(p.bg).Label("播放").FocusRing(false)
					pPose := ui.Pose{}
					if pbtn.Pressed() {
						pPose.Scale = 0.90
					} else if pbtn.Hovered() {
						pPose.Scale = 1.07
					}
					pbtn.AnimatePose(pPose, ui.Snappy)
					pbtn.Children(func() {
						ic := iconPause
						if !a.edPlaying {
							ic = iconPlay
						}
						ui.Icon(c, ic).Size(14, 14)
					})
					if pbtn.Clicked() {
						a.togglePlayback()
					}
					if iconButton(c.Key("st-next"), iconNext, 13, p.muted, "下一字").Clicked() && a.edSelectedChar+1 < len(a.edGlyphs) {
						a.edSelectedChar++
						a.seekPlayback(a.edGlyphs[a.edSelectedChar].Start)
					}
					ui.Text(c, "倍速").FontSize(12).TextColor(p.muted).Margin(0, 0, 0, 2).Shrink(0).SingleLine()
					prevSpd := a.edSpeedMode
					segmented(c, &a.edSpeedMode, "0.5×", "0.75×", "1×")
					if a.edSpeedMode != prevSpd {
						a.setPlaybackSpeed(a.edSpeedMode)
					}
					ui.Box(c).Grow(1)
					a.volumeControl(c, "ed-vol")
					ui.Text(c, "时间").FontSize(12).TextColor(p.muted).Margin(0, 0, 0, 2).Shrink(0).SingleLine()
					settingSwitch(c.Key("st-py"), &a.edShowPinyin, "显示逐字时间")
				})
			})

			// Right: Character inspector & validation issues (compact 232px for a wider center stage)
			right := ui.Column(c.Key("ed-props")).Width(232).MinHeight(0).Shrink(0).Padding(12, 12, 12, 12).Gap(8).Clip()
			right.Draw(func(pt *ui.Painter, r ui.Rect) {
				pt.Fill(ui.Rect{X: r.X, Y: r.Y, W: 1, H: r.H}, p.hairline, 0)
			})
			right.Children(func() {
				ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
					gb := ui.Box(c).MinWidth(50).Height(50).PaddingX(8).Radius(14).Center().Background(p.accent.Alpha(0.14))
					gb.Children(func() {
						disp := curGlyph.Ch
						fsize := float32(26)
						if strings.TrimSpace(disp) == "" {
							disp = "停顿"
							fsize = 14
						} else if nRunes := len([]rune(disp)); nRunes >= 6 {
							fsize = 12.5
						} else if nRunes >= 3 {
							fsize = 15
						} else if nRunes == 2 {
							fsize = 19
						}
						ui.Text(c, disp).FontSize(fsize).FontWeight(800).TextColor(p.accent).SingleLine()
					})
					ui.Column(c).Gap(2).Children(func() {
						ui.Text(c, fmt.Sprintf("第 %d 行 · 第 %d 字", a.edSelectedLine+1, a.edSelectedChar+1)).FontSize(13.5).FontWeight(700)
						ui.Text(c, fmt.Sprintf("时长 %.3fs", curGlyph.End-curGlyph.Start)).FontSize(11.5).TextColor(p.muted)
						statusPill(c, "可拖动底部字块微调", p.okBg, p.ok)
					})
				})

				ui.Row(c).Gap(8).Children(func() {
					b1 := ui.Column(c).Grow(1).Basis(0).Padding(8, 10, 8, 10).Radius(10).AlignItems(ui.Center).Background(p.surface)
					b1.Children(func() {
						ui.Text(c, "起始").FontSize(11).TextColor(p.muted)
						ui.Text(c, fmtSec(curGlyph.Start)).FontSize(12.5).FontWeight(700)
					})
					b2 := ui.Column(c).Grow(1).Basis(0).Padding(8, 10, 8, 10).Radius(10).AlignItems(ui.Center).Background(p.surface)
					b2.Children(func() {
						ui.Text(c, "结束").FontSize(11).TextColor(p.muted)
						ui.Text(c, fmtSec(curGlyph.End)).FontSize(12.5).FontWeight(700)
					})
				})

				ui.Row(c).Gap(8).Children(func() {
					if pillButton(c.Key("ed-shift-back"), nil, "-50ms", p.surface2, p.text).Grow(1).Basis(0).Justify(ui.Center).Clicked() {
						a.shiftAllLyrics(-0.05)
					}
					if pillButton(c.Key("ed-shift-fwd"), nil, "+50ms", p.surface2, p.text).Grow(1).Basis(0).Justify(ui.Center).Clicked() {
						a.shiftAllLyrics(0.05)
					}
				})
				ui.Text(c, fmt.Sprintf("[%s] · 尾 <%s> · 共 %d 字", fmtSec(curLine.Start), fmtSec(curLine.End), len(a.edGlyphs))).FontSize(11.5).TextColor(p.muted)

				ui.Row(c).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "校验与提示").FontSize(13).FontWeight(700).Grow(1)
					statusPill(c, fmt.Sprintf("%d", len(a.edIssues)), p.surface2, p.muted)
				})
				if len(a.edIssues) == 0 {
					ui.Text(c, "当前歌词行时间戳格式正常").FontSize(12).TextColor(p.muted)
				} else {
					scroll := ui.Scroll(c.Key("ed-issue-scroll")).Grow(1).Basis(0).MinHeight(0).FillWidth().Clip()
					scroll.Children(func() {
						ui.Column(c).FillWidth().Gap(6).Children(func() {
								for ii, iss := range a.edIssues {
									var line int
									if _, err := fmt.Sscanf(iss, "[人声] 第 %d 行", &line); err == nil && line > 0 && line <= len(a.edLines) {
										btn := ui.ButtonBase(c.Key(fmt.Sprintf("iss-%d", ii))).Label(iss).FillWidth().Padding(8, 10, 8, 10).
											Radius(10).Background(p.surface).AlignItems(ui.Center).Justify(ui.Start).FocusRing(false)
										btn.Children(func() {
											ui.Text(c, iss).Grow(1).MinWidth(0).FontSize(11.5).TextColor(p.text)
										})
										if btn.Clicked() {
											a.selectEditorLine(line - 1)
											a.seekPlayback(a.edLines[line-1].Start)
										}
										continue
									}
									ic := ui.Box(c.Key(fmt.Sprintf("iss-%d", ii))).FillWidth().Padding(8, 10, 8, 10).Radius(10).Background(p.surface)
									ic.Children(func() {
										ui.Text(c, iss).FontSize(11.5).TextColor(p.text)
									})
								}
						})
					})
				}
			})
		})

		// 3) Bottom multi-track waveform & interactive character timeline (228px)
		tl := ui.Column(c.Key("ed-timeline")).FillWidth().Height(228).Shrink(0)
		tl.Draw(func(pt *ui.Painter, r ui.Rect) {
			pt.Fill(ui.Rect{X: r.X, Y: r.Y, W: r.W, H: 1}, p.hairline, 0)
		})
		tl.Children(func() {
			ui.Row(c).FillWidth().Height(32).PaddingX(14).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "吸附起音").FontSize(12).TextColor(p.muted)
				settingSwitch(c.Key("tl-snap"), &a.edSnapOnset, "吸附起音")
				ui.Text(c, "联动后续字").FontSize(12).TextColor(p.muted)
				settingSwitch(c.Key("tl-rip"), &a.edRipple, "联动后续字")
				ui.Box(c).Grow(1)
				ui.Text(c, "点击或左右拖动下方字块实时调整起止时间 · 拖动上方波形移动播放头").FontSize(11.5).TextColor(p.muted)
			})

			canvas := ui.Row(c.Key("tl-canvas")).Grow(1).FillWidth().AlignItems(ui.Stretch)
			canvas.Draw(func(pt *ui.Painter, r ui.Rect) {
				pt.Fill(ui.Rect{X: r.X, Y: r.Y, W: r.W, H: 1}, p.hairline, 0)
			})
			canvas.Children(func() {
				thCol := ui.Column(c).Width(84).Shrink(0)
				thCol.Draw(func(pt *ui.Painter, r ui.Rect) {
					pt.Fill(ui.Rect{X: r.X + r.W - 1, Y: r.Y, W: 1, H: r.H}, p.hairline, 0)
					pt.Fill(ui.Rect{X: r.X, Y: r.Y + 60, W: r.W, H: 1}, p.hairline, 0)
					pt.Fill(ui.Rect{X: r.X, Y: r.Y + 120, W: r.W, H: 1}, p.hairline, 0)
				})
				thCol.Children(func() {
					ui.Box(c).Height(60).PaddingX(12).AlignItems(ui.Center).Children(func() {
						col, w := p.muted, 600
						if a.edTrackMode == 0 {
							col, w = p.accent, 700
						}
						ui.Text(c, "原曲").FontSize(11.5).FontWeight(w).TextColor(col)
					})
					ui.Box(c).Height(60).PaddingX(12).AlignItems(ui.Center).Children(func() {
						col, w := p.muted, 600
						if a.edTrackMode == 1 {
							col, w = p.accent, 700
						}
						ui.Text(c, "人声轨").FontSize(11.5).FontWeight(w).TextColor(col)
					})
					ui.Box(c).Grow(1).PaddingX(12).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "逐字").FontSize(11.5).FontWeight(600).TextColor(p.muted)
					})
				})

				trArea := ui.Column(c.Key("tr-area")).Grow(1).FillHeight()
				tMin := a.edGlyphs[0].Start
				tMax := max(a.edGlyphs[len(a.edGlyphs)-1].End, tMin+0.5)
				a.ensureLineWaveforms(tMin, tMax)
				staticFrac := clamp01(float32((a.edPlayhead - tMin) / (tMax - tMin)))
				animFrac := trArea.AnimateTo("ph-frac", staticFrac, motionFast)
				trArea.Draw(func(pt *ui.Painter, r ui.Rect) {
					h1, h2 := float32(60), float32(60)
					y1, y2, y3 := r.Y, r.Y+h1, r.Y+h1+h2
					pt.Fill(ui.Rect{X: r.X, Y: y2, W: r.W, H: 1}, p.hairline, 0)
					pt.Fill(ui.Rect{X: r.X, Y: y3, W: r.W, H: 1}, p.hairline, 0)

					mixCol, vocCol := p.faint, p.accent.Alpha(0.85)
					if a.edTrackMode == 0 {
						mixCol, vocCol = p.accent.Alpha(0.85), p.faint
					}
					const nBars = 140
					bw := (r.W - 24) / float32(nBars)
					for bi := range nBars {
						x := r.X + 12 + float32(bi)*bw
						a1 := float32(0.12)
						if len(a.waveMix) == nBars {
							a1 = a.waveMix[bi]
						}
						bh1 := max(3, a1*(h1-12))
						pt.Fill(ui.Rect{X: x, Y: y1 + (h1-bh1)/2, W: max(1.5, bw-1.2), H: bh1}, mixCol, 1)

						a2 := float32(0.08)
						if len(a.waveVoc) == nBars {
							a2 = a.waveVoc[bi]
						} else if len(a.waveMix) == nBars {
							a2 = a.waveMix[bi] * 0.65
						}
						bh2 := max(3, a2*(h2-12))
						pt.Fill(ui.Rect{X: x, Y: y2 + (h2-bh2)/2, W: max(1.5, bw-1.2), H: bh2}, vocCol, 1)
					}

					// 60fps direct audio-clock playhead during playback (zero stutter, zero lag!)
					frac := animFrac
					if a.edPlaying && a.player != nil {
						pos, _ := a.player.Position()
						frac = clamp01(float32((pos - tMin) / (tMax - tMin)))
						pt.After(16 * time.Millisecond)
					}
					phX := r.X + 12 + (r.W-24)*frac
					pt.Fill(ui.Rect{X: phX - 1, Y: r.Y, W: 2, H: r.H}, p.accent, 0)
					pt.Fill(ui.Rect{X: phX - 5, Y: r.Y, W: 10, H: 6}, p.accent, 2)
				})
				trArea.Children(func() {
					// Draggable waveform scrubber across Mix + Vocal tracks (height 120)
					waveScrub := ui.SliderBase(c.Key("wave-scrub"), &a.edPlayhead, tMin, tMax).FillWidth().Height(120).
						Cursor(ui.CursorPointer).Label("拖动时间轴播放头").FocusRing(false)
					if waveScrub.Changed() {
						a.seekPlayback(a.edPlayhead)
					}
					if waveScrub.Changed() || waveScrub.Pressed() {
						// Sync selected character by actual timestamp
						best := 0
						for gi, g := range a.edGlyphs {
							if a.edPlayhead >= g.Start {
								best = gi
							}
						}
						a.edSelectedChar = best
					}

					// Use 6px horizontal padding so the 6px leading/trailing resize handles sit outside [12, W-12],
					// making the first/last character blocks align pixel-perfectly with the waveform and playhead!
					ui.Row(c).Grow(1).FillWidth().Padding(10, 6, 10, 6).Gap(0).AlignItems(ui.Stretch).Children(func() {
						lineSpan := max(tMax-tMin, 0.5)
						// Skip 0-second non-sung tags (e.g. 【合】【狗妈】) in the timeline blocks so they don't steal horizontal space and shift blocks off the linear playhead!
						var visIdxs []int
						for ki := range a.edGlyphs {
							if a.edGlyphs[ki].End-a.edGlyphs[ki].Start >= 0.005 {
								visIdxs = append(visIdxs, ki)
							}
						}
						if len(visIdxs) == 0 {
							for ki := range a.edGlyphs {
								visIdxs = append(visIdxs, ki)
							}
						}
						firstVis := visIdxs[0]

						// Leading draggable handle for the very first sung character's start time
						leftH := ui.Box(c.Key("chnd-left")).Width(6).Shrink(0).FillHeight().
							Cursor(ui.CursorResizeEW).Label("调整首字起始时间")
						if leftH.Hovered() || leftH.Pressed() {
							leftH.Draw(func(pt *ui.Painter, r ui.Rect) {
								pt.Fill(ui.Rect{X: r.X + 2, Y: r.Y + 6, W: 2, H: r.H - 12}, p.accent, 1)
							})
						}
						if dx, _, ok := leftH.Dragged(); ok && dx != 0 {
							a.edSelectedChar = firstVis
							dt := float64(dx) * (lineSpan / 850.0)
							newStart := max(0, min(a.edGlyphs[firstVis].End-0.05, a.edGlyphs[firstVis].Start+dt))
							for k := 0; k < firstVis; k++ {
								a.edGlyphs[k].Start = newStart
								a.edGlyphs[k].End = newStart
							}
							a.edGlyphs[firstVis].Start = newStart
							a.edLines[a.edSelectedLine].Start = newStart
							a.edLines[a.edSelectedLine].Chars = a.edGlyphs
							a.edModified = true
						}

						for vi, ki := range visIdxs {
							idx := ki
							ch := a.edGlyphs[idx].Ch
							durWeight := float32(max(a.edGlyphs[idx].End-a.edGlyphs[idx].Start, 0.005))
							sel := idx == a.edSelectedChar
							if a.edPlaying && a.edPlayhead < a.edGlyphs[firstVis].Start {
								sel = false
							}
							b := ui.ButtonBase(c.Key(fmt.Sprintf("cblk-%d", idx))).Grow(durWeight).Basis(0).MinWidth(10).Radius(7).Center().
								Cursor(ui.CursorPointer).Label("字块：" + ch).Selected(sel).FocusRing(false)
							bg := p.accent.Alpha(0.14)
							if b.Hovered() && !sel {
								bg = p.accent.Alpha(0.24)
							}
							b.Background(b.AnimateColor("bg", bg, ui.Animation{Duration: 120 * time.Millisecond}))
							bPose := ui.Pose{}
							if sel {
								bPose.Y = -2
								bPose.Scale = 1.03
							} else if b.Hovered() {
								bPose.Y = -1
							}
							b.AnimatePose(bPose, ui.Snappy)
							if !sel {
								b.Draw(func(pt *ui.Painter, r ui.Rect) {
									pt.Stroke(r, p.accent.Alpha(0.42), 7, 1)
								})
							}
							fg := p.text
							if sel {
								fg = p.onAccent
							}
							b.Children(func() {
								if sel {
									pill := ui.Box(c.Key("char-pill")).Absolute().Top(0).Left(0).Right(0).Bottom(0).Radius(7).
										PassThrough().Background(p.accent)
									pill.Transition(ui.ElementTransition{Animation: &motionFast, Match: charPillMatch{}})
								}
								ui.Box(c.Key("char-tx")).Absolute().Top(0).Left(0).Right(0).Bottom(0).Center().PassThrough().Children(func() {
									disp := ch
									if strings.TrimSpace(disp) == "" {
										disp = "·"
									}
									ui.Text(c, disp).FontSize(13).FontWeight(700).TextColor(fg).SingleLine()
								})
							})
							// Dragging the block adjusts its duration (and neighbor boundary) in real time
							if dx, _, ok := b.Dragged(); ok && dx != 0 {
								a.edSelectedChar = idx
								dt := float64(dx) * (lineSpan / 850.0)
								newEnd := max(a.edGlyphs[idx].Start+0.05, a.edGlyphs[idx].End+dt)
								if !a.edRipple && idx+1 < len(a.edGlyphs) {
									newEnd = min(newEnd, a.edGlyphs[idx+1].End-0.05)
								}
								actualDt := newEnd - a.edGlyphs[idx].End
								a.edGlyphs[idx].End = newEnd
								if idx+1 < len(a.edGlyphs) {
									if a.edRipple {
										for next := idx + 1; next < len(a.edGlyphs); next++ {
											a.edGlyphs[next].Start = max(0, a.edGlyphs[next].Start+actualDt)
											a.edGlyphs[next].End = max(a.edGlyphs[next].Start+0.05, a.edGlyphs[next].End+actualDt)
										}
									} else {
										a.edGlyphs[idx+1].Start = newEnd
									}
								}
								a.edLines[a.edSelectedLine].Chars = a.edGlyphs
								a.edLines[a.edSelectedLine].End = a.edGlyphs[len(a.edGlyphs)-1].End
								a.edModified = true
							}
							if b.Clicked() {
								a.edSelectedChar = idx
								a.seekPlayback(a.edGlyphs[idx].Start)
							}

							// Draggable boundary handle between visible character blocks for precise edge resizing
							if vi+1 < len(visIdxs) {
								nextVis := visIdxs[vi+1]
								handle := ui.Box(c.Key(fmt.Sprintf("chnd-%d", idx))).Width(6).Shrink(0).FillHeight().
									Cursor(ui.CursorResizeEW).Label("调整字块边界")
								if handle.Hovered() || handle.Pressed() {
									handle.Draw(func(pt *ui.Painter, r ui.Rect) {
										pt.Fill(ui.Rect{X: r.X + 2, Y: r.Y + 6, W: 2, H: r.H - 12}, p.accent, 1)
									})
								}
								if dx, _, ok := handle.Dragged(); ok && dx != 0 {
									a.edSelectedChar = idx
									dt := float64(dx) * (lineSpan / 850.0)
									newBound := max(a.edGlyphs[idx].Start+0.05, a.edGlyphs[idx].End+dt)
									if !a.edRipple {
										newBound = min(newBound, a.edGlyphs[nextVis].End-0.05)
									}
									actualDt := newBound - a.edGlyphs[idx].End
									a.edGlyphs[idx].End = newBound
									if a.edRipple {
										for next := idx + 1; next < len(a.edGlyphs); next++ {
											a.edGlyphs[next].Start = max(0, a.edGlyphs[next].Start+actualDt)
											a.edGlyphs[next].End = max(a.edGlyphs[next].Start, a.edGlyphs[next].End+actualDt)
										}
									} else {
										for mid := idx + 1; mid < nextVis; mid++ {
											a.edGlyphs[mid].Start = newBound
											a.edGlyphs[mid].End = newBound
										}
										a.edGlyphs[nextVis].Start = newBound
									}
									a.edLines[a.edSelectedLine].Chars = a.edGlyphs
									a.edLines[a.edSelectedLine].End = a.edGlyphs[len(a.edGlyphs)-1].End
									a.edModified = true
								}
							}
						}

						// Trailing draggable handle for the very last character's end time
						lastIdx := len(a.edGlyphs) - 1
						rightH := ui.Box(c.Key("chnd-right")).Width(6).Shrink(0).FillHeight().
							Cursor(ui.CursorResizeEW).Label("调整末字结束时间")
						if rightH.Hovered() || rightH.Pressed() {
							rightH.Draw(func(pt *ui.Painter, r ui.Rect) {
								pt.Fill(ui.Rect{X: r.X + 2, Y: r.Y + 6, W: 2, H: r.H - 12}, p.accent, 1)
							})
						}
						if dx, _, ok := rightH.Dragged(); ok && dx != 0 {
							a.edSelectedChar = lastIdx
							dt := float64(dx) * (lineSpan / 850.0)
							newEnd := max(a.edGlyphs[lastIdx].Start+0.05, a.edGlyphs[lastIdx].End+dt)
							a.edGlyphs[lastIdx].End = newEnd
							a.edLines[a.edSelectedLine].End = newEnd
							a.edLines[a.edSelectedLine].Chars = a.edGlyphs
							a.edModified = true
						}
					})
				})
			})
		})
	})
}

// ==================== 5. 工具箱 ====================
func (a *app) viewTools(c *ui.Context) {
	p := a.pal
	sc := ui.Scroll(c.Key("tools-scroll")).TrackScroll(&a.toolScroll).Fill()
	sc.Children(func() {
		col := ui.Column(c).FillWidth().Padding(pageGap, 38, 108, 38).Gap(18)
		col.Children(func() {
			ui.Column(c).Gap(2).Children(func() {
				ui.Text(c, "对已有 LRC 批量处理 · 调用本地 kiralrc 命令行工具").FontSize(11.5).FontWeight(600).TextColor(p.muted)
				ui.Text(c, "工具箱").FontSize(28).FontWeight(800)
			})

			tools := []struct {
				t, d, cmd string
				hue       float32
				ic        *ui.SVG
				args      []string
			}{
				{"校验逐字 LRC", "检查 BOM / CRLF、行首与首标签一致、标签倒序、行内跳变、行尾标签、跨行越界。", "validate", 145, iconCheck, []string{"validate", "examples/sample.lrc"}},
				{"双语歌词加 ❤️", "识别只有首尾双标签的中文翻译行，前后加 ❤️，保持其余字节不变。", "hearts", 345, iconHeart, []string{"hearts", "examples/sample.lrc"}},
				{"小数位转换", "两位小数时间戳批量转为三位（末尾补 0，数值等价），统一全库格式。", "convert", 215, iconQueue, []string{"convert", "examples/sample.lrc"}},
				{"人声分离", "demucs htdemucs 分离出人声与伴奏轨，可单独导出用于试听。", "separate", 285, iconMic, []string{"info"}},
				{"清理歌词文件", "选定目录后按格式勾选，批量删除 .lrc / .vtt / .ttml 等歌词文件，先进回收站。", "cleanup", 20, iconTrash, []string{"info"}},
				{"解密加密音频", "把网易云 NCM 解成普通音频，歌名、歌手、专辑、封面与 ID 等原始信息全部写回标签。", "decrypt", 20, iconUnlock, []string{"info"}},
				{"标签刮削", "并排看网易云与 iTunes 的候选封面，手动挑版本后再批量补高清封面、专辑年份与 NETEASE_ID。", "scrape", 200, iconTag, []string{"info"}},
			}

			// 三列等分：行先撑满，卡片用 Grow/Basis(0) 平分，文字允许收缩换行
			for rowIdx := 0; rowIdx < len(tools); rowIdx += 3 {
				rStart := rowIdx
				ui.Row(c.Key(fmt.Sprintf("trow-%d", rStart))).FillWidth().Gap(14).AlignItems(ui.Stretch).Children(func() {
					for i := rStart; i < rStart+3; i++ {
						if i >= len(tools) {
							// 空位要跟卡片一样带内边距：框架按 basis 分宽度，padding 算进 basis
							ui.Column(c).Grow(1).Basis(0).MinWidth(0).Padding(18, 20, 18, 20)
							continue
						}
						tl := tools[i]
						card := ui.Column(c.Key("tool-"+tl.cmd)).Grow(1).Basis(0).MinWidth(0).
							Padding(18, 20, 18, 20).Gap(10).Radius(16)
						cBg := p.surface
						if card.Hovered() {
							cBg = p.surface2
						}
						card.Background(card.AnimateColor("bg", cBg, ui.Animation{Duration: 150 * time.Millisecond}))
						lift := ui.Pose{}
						if card.Hovered() {
							lift = ui.Pose{Y: -4, Scale: 1.01}
						}
						card.AnimatePose(lift, ui.Snappy)
						card.Draw(func(pt *ui.Painter, r ui.Rect) { pt.Stroke(r, p.hairline, 16, 1) })
						card.Children(func() {
							artCover(c, tl.hue, 42, 12, tl.ic)
							ui.Text(c, tl.t).FontSize(15).FontWeight(700).MinWidth(0)
							ui.Text(c, tl.d).FontSize(12.5).TextColor(p.muted).Grow(1).MinWidth(0)
							ui.Row(c).AlignItems(ui.Center).Children(func() {
								ui.Text(c, tl.cmd).FontSize(11.5).TextColor(p.faint).Grow(1).MinWidth(0)
								if pillButton(c.Key("run-"+tl.cmd), nil, "执行", p.surface2, p.text).Clicked() {
									a.runToolInteractive(tl.cmd)
								}
							})
						})
					}
				})
			}

			ui.Text(c, "工具运行输出").FontSize(16).FontWeight(800).Margin(6, 0, 0, 0)
			outBox := ui.Column(c.Key("tool-output")).FillWidth().Padding(18, 20, 18, 20).Gap(8).Radius(16).Background(p.surface)
			outBox.Draw(func(pt *ui.Painter, r ui.Rect) { pt.Stroke(r, p.hairline, 16, 1) })
			outBox.Children(func() {
				if a.toolLog == "" {
					ui.Text(c, "尚未运行任何工具任务 · 点击上方任意工具卡片的「执行」按钮查看实时输出").FontSize(13).TextColor(p.muted)
				} else {
					for _, ln := range strings.Split(a.toolLog, "\n") {
						ui.Text(c, ln).FontSize(12.5).TextColor(p.text)
					}
				}
			})
		})
	})
}

// ==================== 6. 设置 ====================
func (a *app) viewSettings(c *ui.Context) {
	p := a.pal
	sc := ui.Scroll(c.Key("set-scroll")).TrackScroll(&a.setScroll).Fill()
	sc.Children(func() {
		col := ui.Column(c).FillWidth().Padding(pageGap, 38, 108, 38).Gap(16)
		col.Children(func() {
			ui.Column(c).Gap(2).Children(func() {
				gpuLbl := a.setGpuName
				if gpuLbl == "" {
					gpuLbl = "检测中…"
				}
				envLbl := "环境检测中"
				if a.envChecked {
					if a.envInfo.Ready {
						envLbl = "环境就绪 (" + a.envInfo.Source + ")"
					} else {
						envLbl = "环境待初始化"
					}
				}
				ui.Text(c, "程序目录 "+appRootDir()+" · "+envLbl+" · "+gpuLbl).FontSize(11.5).FontWeight(600).TextColor(p.muted)
				ui.Text(c, "设置").FontSize(28).FontWeight(800)
			})

			// 1) 外观与主题色（Cloudy 8 种预设色）
			ui.Text(c, "外观与主题色").FontSize(16).FontWeight(800)
			g1 := ui.Column(c.Key("set-g1")).FillWidth().Padding(6, 18, 6, 18).Radius(16).Background(p.surface)
			g1.Draw(func(pt *ui.Painter, r ui.Rect) { pt.Stroke(r, p.hairline, 16, 1) })
			g1.Children(func() {
				ui.Row(c).Height(52).AlignItems(ui.Center).Children(func() {
					ui.Column(c).Grow(1).Gap(1).Children(func() {
						ui.Text(c, "外观模式").FontSize(13.5).FontWeight(600)
						ui.Text(c, "浅色或深色主题").FontSize(12).TextColor(p.muted)
					})
					prevApp := a.appearance
					segmented(c, &a.appearance, "浅色", "深色")
					if a.appearance != prevApp {
						a.savePrefs()
					}
				})
				ui.Row(c).Height(56).AlignItems(ui.Center).Children(func() {
					ui.Column(c).Grow(1).Gap(1).Children(func() {
						ui.Text(c, "主题色").FontSize(13.5).FontWeight(600)
						ui.Text(c, "按钮、高亮以及窗口和侧栏都会带上淡淡的呼吸感染色").FontSize(12).TextColor(p.muted)
					})
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						for _, ac := range accentColors {
							item := ac
							col := ui.Oklch(0.6, 0.21, 24)
							if item.hex != "" {
								col = ui.Hex(item.hex)
							}
							on := a.accentID == item.id
							sw := ui.ButtonBase(c.Key("sw-"+item.id)).Size(24, 24).Radius(12).
								Background(col).Label("主题色：" + item.label).Selected(on).FocusRing(false)
							sPose := ui.Pose{}
							switch {
							case sw.Pressed():
								sPose.Scale = 0.88
							case on:
								sPose.Scale = 1.15
							case sw.Hovered():
								sPose.Scale = 1.10
							}
							sw.AnimatePose(sPose, ui.Snappy)
							ring := sw.AnimateTo("ring", b2f(on), ui.Snappy)
							sw.Draw(func(pt *ui.Painter, r ui.Rect) {
								if ring > 0.01 {
									pt.Stroke(r, white.Alpha(ring), 12, 2.5)
								}
							})
							if sw.Clicked() {
								a.setAccent(item.id)
								a.savePrefs()
							}
						}
					})
				})
			})

			// 2) 模型与运行环境（通用 / 中文 / 日文 / 英文 全量状态）
			ui.Row(c).FillWidth().AlignItems(ui.Center).Margin(6, 0, 0, 0).Children(func() {
				ui.Text(c, "模型与运行环境").FontSize(16).FontWeight(800).Grow(1)
				if pillButton(c.Key("m-recheck-all"), nil, "重新检测全部", p.surface2, p.text).Clicked() {
					a.refreshBackendInfo()
					a.toast = "正在重新检测模型与运行环境…"
				}
			})

			// 2a) Python 运行环境卡片（分层检测已有环境 + 一键初始化内置 .venv + 一键下载基础模型）
			envCard := ui.Column(c.Key("set-env-card")).FillWidth().Padding(14, 18, 14, 18).Radius(16).Gap(10).Background(p.surface)
			envCard.Draw(func(pt *ui.Painter, r ui.Rect) { pt.Stroke(r, p.hairline, 16, 1) })
			envCard.Children(func() {
				ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
					switch {
					case a.envInstalling:
						statusPill(c, "安装中…", p.accent.Alpha(0.14), p.accent)
					case !a.envChecked:
						statusPill(c, "检测中", p.surface2, p.muted)
					case a.envInfo.Ready:
						statusPill(c, "已就绪", p.okBg, p.ok)
					default:
						statusPill(c, "需初始化", p.warnBg, p.warn)
					}
					srcLbl := a.envInfo.Source
					if srcLbl == "" {
						srcLbl = "自动检测"
					}
					statusPill(c, srcLbl, p.surface2, p.muted)
					ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
						title := "Python 运行环境"
						if a.envInfo.Version != "" {
							title += " · Python " + a.envInfo.Version
						}
						ui.Text(c, title).FontSize(13.5).FontWeight(700).SingleLine()
						sub := a.envInfo.Path
						if sub == "" {
							sub = a.envInfo.ErrorText
						}
						if sub == "" {
							sub = "正在检测本机与内置 .venv 环境…"
						}
						if a.envChecked && !a.envInfo.Ready && len(a.envInfo.Missing) > 0 {
							sub += " · 缺少: " + strings.Join(a.envInfo.Missing, ", ")
						}
						ui.Text(c, sub).FontSize(11.5).TextColor(p.muted).SingleLine()
					})
				})
				ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
					installLbl := "一键安装/补全运行环境"
					if a.envInstalling {
						installLbl = "正在执行…"
					}
					if pillButton(c.Key("env-bootstrap"), nil, installLbl, p.accent, p.onAccent).Clicked() && !a.envInstalling {
						a.bootstrapEnv()
					}
					if pillButton(c.Key("env-fetch-models"), nil, "一键下载基础模型", p.surface2, p.text).Clicked() && !a.envInstalling {
						a.downloadBaseModels()
					}
					if pillButton(c.Key("env-pick-py"), nil, "选择自定义 Python", p.surface2, p.text).Clicked() && !a.envInstalling {
						a.pickCustomPython()
					}
					if strings.TrimSpace(a.setCustomPython) != "" {
						if pillButton(c.Key("env-reset-py"), nil, "恢复自动检测", p.surface2, p.muted).Clicked() && !a.envInstalling {
							a.setCustomPython = ""
							a.savePrefs()
							a.toast = "已恢复自动检测 Python 环境"
							a.refreshBackendInfo()
						}
					}
				})
				if strings.TrimSpace(a.envInstallLog) != "" {
					logBox := ui.Box(c.Key("env-install-log")).FillWidth().Padding(10, 12, 10, 12).Radius(10).Background(p.surface2)
					logBox.Children(func() {
						lines := strings.Split(strings.TrimSpace(a.envInstallLog), "\n")
						if len(lines) > 10 {
							lines = lines[len(lines)-10:]
						}
						ui.Text(c, strings.Join(lines, "\n")).FontSize(11).TextColor(p.muted)
					})
				}
			})

			g2 := ui.Column(c.Key("set-g2")).FillWidth().Padding(6, 18, 6, 18).Radius(16).Background(p.surface)
			g2.Draw(func(pt *ui.Painter, r ui.Rect) { pt.Stroke(r, p.hairline, 16, 1) })
			g2.Children(func() {
				for mi, m := range a.setModels {
					it := m
					_ = mi
					ui.Row(c).Height(52).Gap(10).AlignItems(ui.Center).Children(func() {
						if it.Exists {
							statusPill(c, "已就绪", p.okBg, p.ok)
						} else {
							statusPill(c, "未下载", p.warnBg, p.warn)
						}
						// 语言标签（中 / 日 / 英 / 通用）
						lBg, lFg := p.surface2, p.muted
						switch it.Lang {
						case "中文":
							lBg, lFg = p.accent.Alpha(0.14), p.accent
						case "日文":
							lBg, lFg = ui.Hex("#7C5CD6").Alpha(0.14), ui.Hex("#7C5CD6")
						case "英文":
							lBg, lFg = ui.Hex("#3878DF").Alpha(0.14), ui.Hex("#3878DF")
						}
						statusPill(c, it.Lang, lBg, lFg)
						ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
							ui.Text(c, it.Name).FontSize(13.5).FontWeight(600).SingleLine()
							ui.Text(c, it.Path).FontSize(11.5).TextColor(p.muted).SingleLine()
						})
						if it.Exists && canDeleteModelPath(it.Path) {
							mBtn := func(key, label string, bg, fg ui.Color) ui.Element {
								b := ui.ButtonBase(c.Key(key)).Height(28).PaddingX(11).Radius(999).Center().
									Background(bg).Label(label).FocusRing(false)
								b.Children(func() {
									ui.Text(c, label).FontSize(11.5).FontWeight(700).TextColor(fg).SingleLine()
								})
								return b
							}
							if a.setConfirmDeleteModel == it.Name {
								if mBtn(fmt.Sprintf("m-del-no-%d", mi), "取消", p.surface2, p.muted).Clicked() {
									a.setConfirmDeleteModel = ""
								}
								if mBtn(fmt.Sprintf("m-del-yes-%d", mi), "确认删除", p.accent, p.onAccent).Clicked() {
									a.deleteModel(it)
								}
							} else {
								if mBtn(fmt.Sprintf("m-del-%d", mi), "删除", p.surface2, p.muted).Clicked() {
									a.setConfirmDeleteModel = it.Name
								}
							}
						}
					})
				}
			})

			// 3) 对齐与默认参数
			ui.Text(c, "对齐与缓存策略").FontSize(16).FontWeight(800).Margin(6, 0, 0, 0)
			g3 := ui.Column(c.Key("set-g3")).FillWidth().Padding(6, 18, 6, 18).Radius(16).Background(p.surface)
			g3.Draw(func(pt *ui.Painter, r ui.Rect) { pt.Stroke(r, p.hairline, 16, 1) })
			g3.Children(func() {
				ui.Row(c).Height(54).Gap(12).AlignItems(ui.Center).Children(func() {
					ui.Column(c).Grow(1).Gap(1).Children(func() {
						ui.Text(c, "无主唱自动跳过").FontSize(13.5).FontWeight(600)
						ui.Text(c, "人声轨 1–4kHz 能量占比低于 0.08 时判定为纯音乐 / 哼唱").FontSize(12).TextColor(p.muted)
					})
					statusPill(c, "0.08", p.surface2, p.text)
					settingSwitch(c.Key("set-novocal"), &a.setAutoSkipNoVocal, "无主唱自动跳过")
				})
				ui.Row(c).Height(54).Gap(12).AlignItems(ui.Center).Children(func() {
					ui.Column(c).Grow(1).Gap(1).Children(func() {
						ui.Text(c, "多音字读音覆盖词表").FontSize(13.5).FontWeight(600)
						ui.Text(c, "为「了/着/得/还」提供白名单与 polyphone.toml 人工覆盖").FontSize(12).TextColor(p.muted)
					})
					if pillButton(c.Key("set-poly"), nil, "编辑 polyphone.toml", p.surface2, p.text).Clicked() {
						a.openPolyphoneToml()
					}
				})
				ui.Row(c).Height(54).Gap(12).AlignItems(ui.Center).Children(func() {
					ui.Column(c).Grow(1).Gap(1).Children(func() {
						ui.Text(c, "英文 / 数字混排粒度").FontSize(13.5).FontWeight(600)
						ui.Text(c, "Echora-300M 默认按词输出；可切换为按字母切分").FontSize(12).TextColor(p.muted)
					})
					segmented(c, &a.setEnMode, "按词", "按字母均分")
				})
				ui.Column(c.Key("set-apple-row")).FillWidth().Padding(10, 0, 10, 0).Gap(8).Children(func() {
					ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
						ui.Column(c).Grow(1).Gap(1).Children(func() {
							ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
								ui.Text(c, "Apple Music 官方歌词源（可选）").FontSize(13.5).FontWeight(600)
								if strings.TrimSpace(a.setAppleToken) != "" {
									statusPill(c, "已启用", p.okBg, p.ok)
								} else {
									statusPill(c, "未配置", p.surface2, p.muted)
								}
							})
							ui.Text(c, "填写浏览器 music.apple.com 的 media-user-token 后生效，留空则自动跳过").FontSize(12).TextColor(p.muted)
						})
						prevSf := a.setAppleStorefront
						segmented(c.Key("set-apple-sf"), &a.setAppleStorefront, "日区 jp", "国区 cn", "美区 us")
						if a.setAppleStorefront != prevSf {
							a.savePrefs()
						}
					})
					ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
						prevTok := a.setAppleToken
						inBox := ui.Row(c).Grow(1).MinWidth(0).Padding(7, 10, 7, 10).Radius(10).AlignItems(ui.Center).Background(p.bg)
						inBox.Draw(func(pt *ui.Painter, r ui.Rect) { pt.Stroke(r, p.hairline, 10, 1) })
						inBox.Children(func() {
							ui.TextInputBase(c.Key("set-apple-tok"), &a.setAppleToken).Grow(1).MinWidth(0).Height(22).
								FontSize(12).TextColor(p.text).Background(ui.Transparent).
								Placeholder("粘贴 media-user-token（自动写入偏好并透传给 Python 后端）").FocusRing(false)
						})
						if a.setAppleToken != prevTok {
							a.savePrefs()
						}
						if pillButton(c.Key("set-apple-paste"), nil, "从剪贴板粘贴", p.surface2, p.text).Clicked() {
							if txt := strings.TrimSpace(readClipboardText()); txt != "" {
								a.setAppleToken = txt
								a.savePrefs()
								a.toast = "已粘贴 Apple Music Token"
							} else {
								a.toast = "剪贴板为空"
							}
						}
						if strings.TrimSpace(a.setAppleToken) != "" {
							if pillButton(c.Key("set-apple-clear"), nil, "清空", p.surface2, p.muted).Clicked() {
								a.setAppleToken = ""
								a.savePrefs()
								a.toast = "已清空 Apple Music Token"
							}
						}
					})
				})
				ui.Column(c.Key("set-ai-row")).FillWidth().Padding(10, 0, 10, 0).Gap(8).Children(func() {
					ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
						ui.Column(c).Grow(1).Gap(1).Children(func() {
							ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
								ui.Text(c, "AI 歌词翻译（OpenAI 兼容）").FontSize(13.5).FontWeight(600)
								if a.aiTranslateConfigured() {
									statusPill(c, "已启用", p.okBg, p.ok)
								} else {
									statusPill(c, "未配置", p.surface2, p.muted)
								}
							})
							ui.Text(c, "在编辑页一键整首翻译日/英/韩等外语歌词，支持 DeepSeek、OpenAI 及各类兼容中转接口").FontSize(12).TextColor(p.muted)
						})
					})
					ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Start).Children(func() {
						// Left column: Base URL input + aligned dropdown
						ui.Column(c.Key("set-ai-url-col")).Grow(3).MinWidth(0).Gap(4).Children(func() {
							prevURL := a.setAIBaseURL
							urlBox := ui.Row(c).FillWidth().Padding(5, 6, 5, 10).Gap(6).Radius(10).AlignItems(ui.Center).Background(p.bg)
							urlBox.Draw(func(pt *ui.Painter, r ui.Rect) {
								col := p.hairline
								if a.setAIURLDropOpen {
									col = p.accent.Alpha(0.55)
								}
								pt.Stroke(r, col, 10, 1)
							})
							urlBox.Children(func() {
								ui.TextInputBase(c.Key("set-ai-url"), &a.setAIBaseURL).Grow(1).MinWidth(0).Height(24).
									FontSize(12).TextColor(p.text).Background(ui.Transparent).
									Placeholder("Base URL，可下拉选择预设或自定义").FocusRing(false)
								urlLbl := "预设 ▾"
								if mp := matchAIProviderPreset(a.setAIBaseURL); mp != nil {
									urlLbl = mp.Name + " ▾"
								}
								if a.setAIURLDropOpen {
									urlLbl = strings.TrimSuffix(urlLbl, "▾") + "▴"
								}
								dropBtn := ui.ButtonBase(c.Key("set-ai-url-drop")).Height(24).PaddingX(8).Radius(7).Center().
									Background(p.surface2).Label("选择服务商预设").FocusRing(false)
								dropBtn.Children(func() {
									ui.Text(c, urlLbl).FontSize(11.5).FontWeight(600).TextColor(p.text).SingleLine()
								})
								if dropBtn.Clicked() {
									a.setAIURLDropOpen = !a.setAIURLDropOpen
									if a.setAIURLDropOpen {
										a.setAIModelDropOpen = false
									}
								}
							})
							if a.setAIBaseURL != prevURL {
								a.savePrefs()
							}
							if a.setAIURLDropOpen {
								dropCard := ui.Column(c.Key("set-ai-url-menu")).FillWidth().Padding(5, 5, 5, 5).Gap(2).Radius(10).Background(p.bg)
								dropCard.Draw(func(pt *ui.Painter, r ui.Rect) { pt.Stroke(r, p.accent.Alpha(0.35), 10, 1) })
								dropCard.Children(func() {
									curNorm := strings.TrimRight(strings.ToLower(strings.TrimSpace(a.setAIBaseURL)), "/")
									for pi, preset := range aiProviderPresets {
										pr := preset
										selected := strings.TrimRight(strings.ToLower(pr.BaseURL), "/") == curNorm
										item := ui.ButtonBase(c.Key(fmt.Sprintf("ai-pr-%d", pi))).FillWidth().Height(30).PaddingX(9).Gap(8).Radius(7).
											AlignItems(ui.Center).Label(pr.Name).FocusRing(false)
										if selected {
											item.Background(p.accent.Alpha(0.12))
										} else if item.Hovered() {
											item.Background(p.hover)
										}
										item.Children(func() {
											fg := p.text
											if selected {
												fg = p.accent
											}
											ui.Text(c, pr.Name).Width(116).FontSize(12).FontWeight(700).TextColor(fg).SingleLine()
											ui.Text(c, pr.BaseURL).Grow(1).MinWidth(0).FontSize(11).TextColor(p.muted).SingleLine()
											if selected {
												ui.Icon(c, iconCheck).Size(13, 13).TextColor(p.accent)
											}
										})
										if item.Clicked() {
											a.setAIBaseURL = pr.BaseURL
											if len(pr.Models) > 0 {
												a.setAIModel = pr.Models[0]
											}
											a.setAIURLDropOpen = false
											a.savePrefs()
										}
									}
								})
							}
						})

						// Right column: Model input + aligned dropdown
						ui.Column(c.Key("set-ai-model-col")).Grow(2).MinWidth(0).Gap(4).Children(func() {
							prevModel := a.setAIModel
							modelBox := ui.Row(c).FillWidth().Padding(5, 6, 5, 10).Gap(6).Radius(10).AlignItems(ui.Center).Background(p.bg)
							modelBox.Draw(func(pt *ui.Painter, r ui.Rect) {
								col := p.hairline
								if a.setAIModelDropOpen {
									col = p.accent.Alpha(0.55)
								}
								pt.Stroke(r, col, 10, 1)
							})
							modelBox.Children(func() {
								ui.TextInputBase(c.Key("set-ai-model"), &a.setAIModel).Grow(1).MinWidth(0).Height(24).
									FontSize(12).TextColor(p.text).Background(ui.Transparent).
									Placeholder("模型，可下拉选择或自定义").FocusRing(false)
								mLbl := "模型 ▾"
								if a.setAIModelDropOpen {
									mLbl = "模型 ▴"
								}
								mDropBtn := ui.ButtonBase(c.Key("set-ai-model-drop")).Height(24).PaddingX(8).Radius(7).Center().
									Background(p.surface2).Label("选择推荐模型").FocusRing(false)
								mDropBtn.Children(func() {
									ui.Text(c, mLbl).FontSize(11.5).FontWeight(600).TextColor(p.text).SingleLine()
								})
								if mDropBtn.Clicked() {
									a.setAIModelDropOpen = !a.setAIModelDropOpen
									if a.setAIModelDropOpen {
										a.setAIURLDropOpen = false
									}
								}
							})
							if a.setAIModel != prevModel {
								a.savePrefs()
							}
							if a.setAIModelDropOpen {
								models := aiModelSuggestions(a.setAIBaseURL)
								dropCard := ui.Column(c.Key("set-ai-model-menu")).FillWidth().Padding(5, 5, 5, 5).Gap(2).Radius(10).Background(p.bg)
								dropCard.Draw(func(pt *ui.Painter, r ui.Rect) { pt.Stroke(r, p.accent.Alpha(0.35), 10, 1) })
								dropCard.Children(func() {
									curM := strings.TrimSpace(a.setAIModel)
									for mi, modelName := range models {
										mn := modelName
										selected := mn == curM
										item := ui.ButtonBase(c.Key(fmt.Sprintf("ai-md-%d", mi))).FillWidth().Height(28).PaddingX(9).Gap(8).Radius(7).
											AlignItems(ui.Center).Label(mn).FocusRing(false)
										if selected {
											item.Background(p.accent.Alpha(0.12))
										} else if item.Hovered() {
											item.Background(p.hover)
										}
										item.Children(func() {
											fg := p.text
											if selected {
												fg = p.accent
											}
											ui.Text(c, mn).Grow(1).MinWidth(0).FontSize(12).FontWeight(600).TextColor(fg).SingleLine()
											if selected {
												ui.Icon(c, iconCheck).Size(13, 13).TextColor(p.accent)
											}
										})
										if item.Clicked() {
											a.setAIModel = mn
											a.setAIModelDropOpen = false
											a.savePrefs()
										}
									}
								})
							}
						})
					})
					ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
						hasKey := strings.TrimSpace(a.setAIAPIKey) != ""
						if hasKey && !a.setAIKeyEditing {
							keyBox := ui.Row(c).Grow(1).MinWidth(0).Height(36).Padding(0, 10, 0, 12).Gap(8).Radius(10).AlignItems(ui.Center).Background(p.bg)
							keyBox.Draw(func(pt *ui.Painter, r ui.Rect) { pt.Stroke(r, p.hairline, 10, 1) })
							keyBox.Children(func() {
								ui.Text(c, maskAPIKey(a.setAIAPIKey)).Grow(1).MinWidth(0).FontSize(12.5).FontWeight(600).TextColor(p.text).SingleLine()
								statusPill(c, "已加密", p.okBg, p.ok)
							})
							if pillButton(c.Key("set-ai-edit"), nil, "更换", p.surface2, p.text).Clicked() {
								a.setAIKeyDraft = ""
								a.setAIKeyEditing = true
							}
							if pillButton(c.Key("set-ai-paste"), nil, "从剪贴板粘贴", p.surface2, p.text).Clicked() {
								if txt := strings.TrimSpace(readClipboardText()); txt != "" {
									a.setAIAPIKey = txt
									a.setAIKeyEditing = false
									a.setAIKeyDraft = ""
									a.savePrefs()
									a.toast = "已加密保存 AI API Key（" + maskAPIKey(txt) + "）"
								} else {
									a.toast = "剪贴板为空"
								}
							}
							if pillButton(c.Key("set-ai-clear"), nil, "清空", p.surface2, p.muted).Clicked() {
								a.setAIAPIKey = ""
								a.setAIKeyEditing = false
								a.setAIKeyDraft = ""
								a.savePrefs()
								a.toast = "已清空 AI API Key"
							}
						} else {
							keyBox := ui.Row(c).Grow(1).MinWidth(0).Padding(7, 10, 7, 10).Radius(10).AlignItems(ui.Center).Background(p.bg)
							keyBox.Draw(func(pt *ui.Painter, r ui.Rect) { pt.Stroke(r, p.hairline, 10, 1) })
							keyBox.Children(func() {
								in := ui.TextInputBase(c.Key("set-ai-key"), &a.setAIKeyDraft).Grow(1).MinWidth(0).Height(22).
									FontSize(12).TextColor(p.text).Background(ui.Transparent).Password().
									Placeholder("输入或粘贴 API Key（保存后脱敏显示并经 Windows DPAPI 全加密）").FocusRing(false)
								if in.Submitted() && strings.TrimSpace(a.setAIKeyDraft) != "" {
									a.setAIAPIKey = strings.TrimSpace(a.setAIKeyDraft)
									a.setAIKeyDraft = ""
									a.setAIKeyEditing = false
									a.savePrefs()
									a.toast = "已加密保存 AI API Key（" + maskAPIKey(a.setAIAPIKey) + "）"
								}
							})
							if strings.TrimSpace(a.setAIKeyDraft) != "" {
								if pillButton(c.Key("set-ai-save"), nil, "保存", p.accent, p.onAccent).Clicked() {
									a.setAIAPIKey = strings.TrimSpace(a.setAIKeyDraft)
									a.setAIKeyDraft = ""
									a.setAIKeyEditing = false
									a.savePrefs()
									a.toast = "已加密保存 AI API Key（" + maskAPIKey(a.setAIAPIKey) + "）"
								}
							}
							if pillButton(c.Key("set-ai-paste"), nil, "从剪贴板粘贴", p.surface2, p.text).Clicked() {
								if txt := strings.TrimSpace(readClipboardText()); txt != "" {
									a.setAIAPIKey = txt
									a.setAIKeyDraft = ""
									a.setAIKeyEditing = false
									a.savePrefs()
									a.toast = "已加密保存 AI API Key（" + maskAPIKey(txt) + "）"
								} else {
									a.toast = "剪贴板为空"
								}
							}
							if a.setAIKeyEditing {
								if pillButton(c.Key("set-ai-cancel"), nil, "取消", p.surface2, p.muted).Clicked() {
									a.setAIKeyDraft = ""
									a.setAIKeyEditing = false
								}
							}
						}
					})
				})
				ui.Row(c).Height(56).Gap(12).AlignItems(ui.Center).Children(func() {
					ui.Column(c).Grow(1).Gap(1).Children(func() {
						ui.Text(c, "中间产物缓存").FontSize(13.5).FontWeight(600)
						ui.Text(c, "对齐过程中留下的人声轨与切片，清理后不影响已保存的 LRC").FontSize(12).TextColor(p.muted)
					})
					ui.Text(c, a.setCacheSummary).FontSize(12).TextColor(p.muted)
					segmented(c, &a.setCleanDays, "7 天前", "30 天前", "全部")
					if pillButton(c.Key("set-clean"), nil, "立即清理", p.accent, p.onAccent).Clicked() {
						if a.setCleanDays == 2 {
							a.runKiralrcCLI("clean", "--all")
						} else {
							days := "7"
							if a.setCleanDays == 1 {
								days = "30"
							}
							a.runKiralrcCLI("clean", "--days", days)
						}
					}
				})
				ui.Row(c).Height(56).Gap(12).AlignItems(ui.Center).Children(func() {
					ui.Column(c).Grow(1).Gap(1).Children(func() {
						ui.Text(c, "人声分离缓存").FontSize(13.5).FontWeight(600)
						ui.Text(c, "分离出来的人声与伴奏 · 关掉程序自动清空，开着最多留三小时").FontSize(12).TextColor(p.muted)
					})
					if pillButton(c.Key("set-open-sep"), nil, "打开目录", p.surface2, p.text).Clicked() {
						a.revealSepCache()
					}
					if pillButton(c.Key("set-clear-sep"), nil, "清除分离缓存", p.surface2, p.text).Clicked() {
						a.clearSepCache()
					}
				})

			})
		})
	})
}
