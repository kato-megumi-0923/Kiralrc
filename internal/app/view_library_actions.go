package app

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

func (a *app) viewLibraryFolders(c *ui.Context) {
	p := a.pal
	button := popoverButton(c.Key("lib-folder-btn"), p, iconFolder, "文件夹", a.libFolderMenuOpen, false)
	if button.Clicked() {
		a.libFolderMenuOpen = !a.libFolderMenuOpen
		a.libRowMenuPath = ""
		a.menu = nil
	}
	anchoredPopover(c.Key("lib-folder-popover"), p, button, &a.libFolderMenuOpen, 370, false, func() {
		ui.Column(c).FillWidth().Gap(4).Children(func() {
			if len(a.libFolders) > 0 {
				ui.Scroll(c.Key("lib-folder-list")).FillWidth().Height(float32(min(len(a.libFolders), 6) * 32)).Children(func() {
					ui.Column(c).FillWidth().Children(func() {
						var removeFolder string
						for _, folder := range a.libFolders {
							ui.Row(c.Key(folder)).FillWidth().Height(32).Gap(6).AlignItems(ui.Center).Children(func() {
								path := ui.Text(c, folder).Grow(1).MinWidth(0).FontSize(12).TextColor(p.muted).SingleLine()
								ui.TooltipBase(c, path, func(tip ui.Element) {
									tip.Padding(6, 10).Radius(8).Background(p.raised).Border(1, p.hairline).MaxWidth(640)
									tip.Children(func() { ui.Text(c, folder).FontSize(12).TextColor(p.text) })
								})
								remove := ui.ButtonBase(c.Key("remove")).Height(24).PaddingX(8).Radius(8).
									Center().Shrink(0).Label("移除 " + folder)
								if remove.Hovered() || remove.FocusVisible() {
									remove.Background(p.errBg)
								}
								remove.Children(func() { ui.Text(c, "移除").FontSize(12).TextColor(p.err) })
								if remove.Clicked() {
									removeFolder = folder
								}
							})
						}
						if removeFolder != "" {
							a.removeLibraryFolder(removeFolder)
						}
					})
				})
				ui.Box(c).FillWidth().Height(1).Background(p.hairline)
			}
			if len(a.libFolders) > 0 && popoverItem(c.Key("lib-clear-folders"), p, "清空全部目录", p.err).Clicked() {
				a.clearLibraryFolders()
			}
		})
	})
}

func (a *app) viewLibraryOverview(c *ui.Context, st libStats, vocalPct float64) {
	p := a.pal
	card := ui.Row(c.Key("lib-overview")).FillWidth().Height(88).Padding(10, 16).Gap(10).
		AlignItems(ui.Center).Radius(16).Background(p.surface)
	card.Draw(func(pt *ui.Painter, r ui.Rect) { pt.Stroke(r, p.hairline, 16, 1) })
	card.Children(func() {
		approx := ""
		if a.libScanning {
			approx = "约 "
		}
		title := fmt.Sprintf("逐字覆盖 %s%.1f%% · 有效人声 %d 首", approx, vocalPct, st.Vocal)
		if a.downloading {
			title = "正在下载歌词…"
			if a.downloadAll > 0 {
				title = fmt.Sprintf("正在下载歌词 %d / %d", a.downloadDone, a.downloadAll)
			}
		}
		ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, title).FontSize(15).FontWeight(800).SingleLine()
				ui.Text(c, fmt.Sprintf("已排除纯音乐 / OST / 伴奏 %s%d 首", approx, st.InstTotal)).
					Grow(1).MinWidth(0).FontSize(11.5).TextColor(p.muted).SingleLine()
			})
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Grow(1).MinWidth(0).Height(4).Radius(2).Background(p.surface2).
					Draw(func(pt *ui.Painter, r ui.Rect) {
						r.W *= clamp01(float32(vocalPct / 100))
						if r.W > 0 {
							pt.Fill(r, p.accent, 2)
						}
					})
				detail := fmt.Sprintf("%s官方 %d · 对齐 %d", approx, st.Official, st.Aligned)
				if st.Unknown > 0 {
					detail += fmt.Sprintf(" · 其它逐字 %d", st.Unknown)
				}
				detail += fmt.Sprintf(" · 整行 %d · 无词 %d", st.LineOnly, st.Missing)
				ui.Text(c, detail).FontSize(11.5).TextColor(p.muted).SingleLine()
			})
		})
		if pillButton(c.Key("hero-download"), iconDownload, "下载歌词", p.accent, p.onAccent).
			Shrink(0).Disabled(a.downloading).Clicked() {
			a.startDownloadLyrics()
		}
		if st.Missing > 0 {
			if pillButton(c.Key("hero-missing"), iconWarn, fmt.Sprintf("处理无歌词 (%s%d)", approx, st.Missing), p.surface2, p.text).
				Shrink(0).Clicked() {
				a.setLibFilter(3)
			}
		}
	})
}

func (a *app) viewLibraryLyricsMenu(c *ui.Context, idx int, tr *trackItem) {
	p := a.pal
	open := a.libRowMenuPath == tr.FullPath
	button := popoverButton(c.Key(fmt.Sprintf("nl-more-%d", idx)), p, nil, "补词", open, false)
	if button.Clicked() {
		open = !open
		a.libRowMenuPath = ""
		if open {
			a.libRowMenuPath = tr.FullPath
		}
		a.libFolderMenuOpen = false
		a.menu = nil
	}
	anchoredPopover(c.Key("nl-more-popover"), p, button, &open, 164, false, func() {
		ui.Column(c).FillWidth().Gap(2).Children(func() {
			if popoverItem(c.Key("nl-id"), p, "填网易云 ID", p.text).Clicked() {
				a.openNoLyricsDialog("netease", tr)
			}
			if popoverItem(c.Key("nl-manual"), p, "手动贴歌词", p.text).Clicked() {
				a.openNoLyricsDialog("manual", tr)
			}
			if popoverItem(c.Key("nl-asr"), p, "本地 ASR 识别并对齐", p.text).Clicked() {
				a.libRowMenuPath = ""
				a.enqueueNoLyricsAsr([]trackItem{*tr})
			}
		})
	})
	if !open && a.libRowMenuPath == tr.FullPath {
		a.libRowMenuPath = ""
	}
}

func (a *app) viewLibraryNoLyricsDialog(c *ui.Context) {
	stage := ui.Box(c.Key("lib-lyrics-motion")).Absolute().Size(0, 0).PassThrough()
	open := a.libNoLyricsDialog != ""
	progress := b2f(open)
	if !c.Preferences().ReduceMotion {
		progress = clamp01(stage.AnimateTo("open", progress, motionPane))
	}
	if a.page != pageLibrary || (!open && progress == 0) {
		a.libClosingLyricsMode, a.libClosingLyricsTrack = "", nil
		return
	}
	if open {
		a.libClosingLyricsMode, a.libClosingLyricsTrack = a.libNoLyricsDialog, a.libDialogTrack
	}
	mode, tr := a.libClosingLyricsMode, a.libClosingLyricsTrack
	w, h := c.Size()
	p := a.pal
	width, height := float32(560), float32(226)
	title := "填网易云 ID"
	switch mode {
	case "manual":
		width, height, title = 680, 460, "手动贴歌词"
	case "batch_id":
		width, height, title = 760, h*0.78, "批量填写网易云歌曲 ID"
	}
	shown := true
	ui.DialogBase(c.Key("lib-lyrics-dialog"), &shown, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.30*progress))
		scale := lerp(0.92, 1, progress)
		panel.Size(min(width, w*0.92), min(height, h*0.78)).Padding(20).Radius(20).
			Background(p.raised).Border(1, p.hairline).Shadow(0, 12, 36, 0, p.cardShade).
			Label(title).Opacity(progress).Scale(scale, scale).TransformOrigin(0.5, 1)
		ui.Column(c.Key(mode)).Fill().Gap(12).Children(func() {
			ui.Text(c, title).FontSize(20).FontWeight(800)
			if tr != nil {
				ui.Text(c, tr.Title+" · "+tr.Artist).FontSize(13).TextColor(p.muted).SingleLine()
			}
			switch mode {
			case "netease":
				a.viewSingleLyricsID(c, tr)
			case "manual":
				a.viewManualLyrics(c, tr)
			case "batch_id":
				a.viewBatchLyricsIDs(c)
			}
		})
	})
	if !shown && open {
		a.closeNoLyricsDialog()
	}
}

func lyricsIDInput(c *ui.Context, p palette, value *string, autofocus bool) {
	ui.Row(c).Grow(1).MinWidth(0).Height(36).PaddingX(10).Radius(10).AlignItems(ui.Center).
		Background(p.bg).Border(1, p.hairline).Children(func() {
		input := ui.TextInputBase(c.Key("input"), value).Grow(1).MinWidth(0).Height(22).FontSize(12.5).
			TextColor(p.text).Background(ui.Transparent).Placeholder("网易云歌曲 ID 或歌曲链接").
			Label("网易云歌曲 ID")
		if autofocus {
			input.AutoFocus()
		}
	})
}

func (a *app) lyricsDialogFooter(c *ui.Context, label string, disabled bool, submit func()) {
	p := a.pal
	ui.Row(c).FillWidth().Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(func() {
		if pillButton(c.Key("cancel"), nil, "取消", p.surface2, p.muted).Clicked() {
			a.closeNoLyricsDialog()
		}
		bg, fg := p.accent, p.onAccent
		if disabled {
			bg, fg = p.surface2, p.faint
		}
		if pillButton(c.Key("submit"), nil, label, bg, fg).Disabled(disabled).Clicked() {
			submit()
		}
	})
}

func (a *app) viewSingleLyricsID(c *ui.Context, tr *trackItem) {
	p := a.pal
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
		lyricsIDInput(c, p, &a.libSingleIdInput, a.libNoLyricsDialog == "netease")
		if id := parseNeteaseID(a.libSingleIdInput); id != "" {
			statusPill(c, "已识别 "+id, p.okBg, p.ok)
		} else {
			statusPill(c, "等待有效 ID", p.surface2, p.muted)
		}
	})
	ui.Box(c).Grow(1)
	a.lyricsDialogFooter(c, "下载歌词并对齐", parseNeteaseID(a.libSingleIdInput) == "", func() {
		a.enqueueSingleNetease(tr, a.libSingleIdInput)
	})
}

func (a *app) viewManualLyrics(c *ui.Context, tr *trackItem) {
	p := a.pal
	ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
		ui.Text(c, "歌词语言").FontSize(12).TextColor(p.muted)
		segmented(c.Key("man-lang"), &a.libManualLang, "中文", "日文", "英文")
	})
	ui.Row(c).Gap(6).Children(func() {
		if pillButton(c.Key("man-paste"), nil, "从剪贴板粘贴", p.surface2, p.text).Clicked() {
			a.pasteClipboardToManual()
		}
		if pillButton(c.Key("man-import"), nil, "导入 .txt/.lrc", p.surface2, p.text).Clicked() {
			a.importManualLyricsFile()
		}
		if pillButton(c.Key("man-notepad"), nil, "用记事本编辑", p.surface2, p.text).Clicked() {
			a.openManualLyricsInNotepad(tr)
		}
		if a.libManualFilePath != "" && pillButton(c.Key("man-reload"), nil, "读取记事本", p.surface2, p.text).Clicked() {
			a.reloadManualLyricsFromFile()
		}
	})
	n := countNonEmptyLines(a.libManualText)
	ui.Scroll(c.Key("man-preview")).Grow(1).MinHeight(0).FillWidth().Radius(10).Background(p.bg).
		Border(1, p.hairline).Children(func() {
		ui.Column(c).FillWidth().Padding(10, 12).Gap(3).Children(func() {
			if n == 0 {
				ui.Text(c, "粘贴或导入歌词，纯文本即可，无需时间戳").FontSize(12).TextColor(p.muted)
			}
			for _, line := range strings.Split(a.libManualText, "\n") {
				if line = strings.TrimSpace(line); line != "" {
					ui.Text(c, line).FillWidth().FontSize(12.5).TextColor(p.text)
				}
			}
		})
	})
	ui.Text(c, fmt.Sprintf("当前 %d 行", n)).FontSize(11.5).TextColor(p.muted)
	a.lyricsDialogFooter(c, "保存参考词并开始对齐", n == 0 && a.libManualFilePath == "", func() {
		a.enqueueSingleManual(tr)
	})
}

func (a *app) viewBatchLyricsIDs(c *ui.Context) {
	p := a.pal
	targets := a.batchIdTargetTracks()
	filled := 0
	ui.Text(c, "可粘贴纯数字或网易云歌曲链接，未填写的歌曲会跳过。").FontSize(12).TextColor(p.muted)
	ui.Scroll(c.Key("batch-id-list")).Grow(1).MinHeight(0).FillWidth().Children(func() {
		ui.Column(c).FillWidth().Gap(6).Children(func() {
			for _, tr := range targets {
				value := a.libBatchIdInputs[tr.FullPath]
				ui.Row(c.Key(tr.FullPath)).FillWidth().Padding(6, 10).Gap(8).Radius(10).
					AlignItems(ui.Center).Background(p.surface).Children(func() {
					ui.Column(c).Width(180).Shrink(0).MinWidth(0).Children(func() {
						ui.Text(c, tr.Title).FontSize(13).FontWeight(700).SingleLine()
						ui.Text(c, tr.Artist).FontSize(11).TextColor(p.muted).SingleLine()
					})
					lyricsIDInput(c, p, &value, false)
					if id := parseNeteaseID(value); id != "" {
						filled++
						statusPill(c, "已识别 "+id, p.okBg, p.ok)
					} else {
						statusPill(c, "待填写", p.surface2, p.muted)
					}
				})
				a.libBatchIdInputs[tr.FullPath] = value
			}
		})
	})
	if len(targets) == 0 {
		ui.Text(c, "当前没有无歌词歌曲").FontSize(12).TextColor(p.muted)
	}
	a.lyricsDialogFooter(c, fmt.Sprintf("一键下载并入队（已填 %d 首）", filled), filled == 0, a.enqueueBatchNetease)
}
