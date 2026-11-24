package app

import (
	"fmt"
	"math"
	"strings"

	"github.com/egoist/mygo/ui"
)

func (a *app) viewAlign(c *ui.Context) {
	p := a.pal
	ui.Scroll(c.Key("align-scroll")).TrackScroll(&a.alignScroll).Fill().Children(func() {
		ui.Column(c).FillWidth().MaxWidth(1036).Padding(pageGap, 38, 108, 38).Gap(14).Children(func() {
			ui.Text(c, "新建对齐").FontSize(28).FontWeight(800)
			a.viewAlignAudio(c)
			a.viewAlignLyrics(c)
			a.viewAlignAdvanced(c)

			output := a.alignOut
			if output == "" {
				output = "默认输出至同目录同名 .lrc"
			}
			bar := ui.Row(c.Key("align-actions")).FillWidth().Padding(10, 16).Gap(8).Radius(14).
				AlignItems(ui.Center).Background(p.surface).Border(1, p.hairline)
			bar.Children(func() {
				ui.Text(c, "输出："+output).Grow(1).MinWidth(0).FontSize(12).TextColor(p.muted).SingleLine()
				enqueue := pillButton(c.Key("btn-enq"), nil, "加入队列", p.surface2, p.text).Shrink(0)
				start := pillButton(c.Key("btn-start-align"), iconPlay, "开始对齐", p.accent, p.onAccent).Shrink(0)
				if enqueue.Clicked() || start.Clicked() {
					if a.alignAudio == "" && len(a.alignBatchTracks) == 0 {
						if a.alignBatchDir == "" {
							a.pickAlignFolder()
						} else {
							a.toast = "该文件夹没有待对齐的人声歌曲"
						}
						return
					}
					a.enqueueCurrentAlign()
					if start.Clicked() {
						a.navigate(pageQueue, 1)
					} else {
						a.toast = "已加入对齐队列"
					}
				}
			})
		})
	})
}

func (a *app) viewAlignAudio(c *ui.Context) {
	p := a.pal
	ui.Column(c.Key("step1")).FillWidth().Padding(14, 18).Gap(10).Radius(16).
		Background(p.surface).Border(1, p.hairline).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "音频来源").Grow(1).FontSize(14).FontWeight(700)
			if pillButton(c.Key("chg-folder"), iconFolder, "选择文件夹（批量）", p.surface2, p.text).Clicked() {
				a.pickAlignFolder()
			}
			label := "选择单个音频"
			if a.alignAudio != "" {
				label = "更换音频"
			}
			if pillButton(c.Key("chg-audio"), iconNote, label, p.surface2, p.text).Clicked() {
				a.pickAlignAudio()
			}
		})
		ui.Row(c).FillWidth().Padding(8, 12).Gap(10).Radius(12).
			AlignItems(ui.Center).Background(p.bg).Border(1, p.hairline).Children(func() {
			switch {
			case a.alignBatchDir != "":
				artCover(c, hueForString(a.alignBatchDir), 40, 10, iconFolder)
				ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
					ui.Text(c, fmt.Sprintf("批量文件夹 · 共 %d 首待升级人声歌曲", len(a.alignBatchTracks))).FontSize(13).FontWeight(700).SingleLine()
					ui.Text(c, a.alignBatchDir).FontSize(11.5).TextColor(p.muted).SingleLine()
				})
			case a.alignAudio == "":
				artCover(c, 25, 40, 10, iconNote)
				ui.Text(c, "尚未选择本地音频或文件夹").FontSize(13).TextColor(p.muted)
			default:
				artCover(c, hueForString(a.alignAudio), 40, 10, iconNote)
				ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
					ui.Text(c, a.alignAudio).FontSize(13).FontWeight(700).SingleLine()
					ui.Text(c, a.alignArtist+" · "+a.alignTitle).FontSize(11.5).TextColor(p.muted).SingleLine()
				})
				ui.Box(c).Width(160).Height(34).Draw(func(pt *ui.Painter, r ui.Rect) {
					const bars = 46
					bw := r.W / bars
					for i := range bars {
						amp := float32(0.25 + 0.65*math.Abs(math.Sin(float64(i)*0.42)))
						bh := max(4, amp*(r.H-4))
						pt.Fill(ui.Rect{X: r.X + float32(i)*bw, Y: r.Y + (r.H-bh)/2, W: max(2, bw-2), H: bh},
							p.accent.Alpha(0.55), 1.5)
					}
				})
			}
		})
	})
}

func (a *app) viewAlignLyrics(c *ui.Context) {
	p := a.pal
	ui.Column(c.Key("step2")).FillWidth().Padding(14, 18).Gap(10).Radius(16).
		Background(p.surface).Border(1, p.hairline).Children(func() {
		ui.Text(c, "歌词来源与语言").FontSize(14).FontWeight(700)
		segmented(c.Key("align-source"), &a.alignSource, "优先本地 LRC（无则搜网易云）", "手动指定参考文件", "强制搜网易云")
		ui.Row(c.Key("align-reference")).FillWidth().Padding(6, 10).Gap(8).Radius(10).
			AlignItems(ui.Center).Background(p.bg).Border(1, p.hairline).Children(func() {
			switch {
			case a.alignSource == 1:
				hint := "尚未选择手动参考 LRC 文件"
				if a.alignRefLrc != "" {
					hint = a.alignRefLrc
					statusPill(c, "已选参考", p.okBg, p.ok)
				}
				ui.Text(c, hint).Grow(1).MinWidth(0).FontSize(12).TextColor(p.muted).SingleLine()
				if pillButton(c.Key("pick-ref-lrc"), iconFolder, "选择参考 LRC", p.surface2, p.text).Clicked() {
					a.pickAlignRefLrc()
				}
			case a.alignSource == 0 && a.alignLocalLrc != "":
				statusPill(c, "命中本地 LRC", p.okBg, p.ok)
				ui.Text(c, a.alignLocalLrc).Grow(1).MinWidth(0).FontSize(12).TextColor(p.muted).SingleLine()
			default:
				hint := "选择音频后按所选来源查找歌词"
				if a.alignBatchDir != "" {
					hint = "按每首歌曲查找参考歌词"
				} else if a.alignAudio != "" {
					hint = fmt.Sprintf("搜索网易云歌词 · %s", a.alignSearch)
				}
				ui.Text(c, hint).FontSize(12).TextColor(p.muted).SingleLine()
			}
		})
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "网易云歌曲 ID").FontSize(12).FontWeight(600)
			ui.Row(c).Grow(1).MinWidth(0).Height(32).PaddingX(8).Radius(9).AlignItems(ui.Center).
				Background(p.bg).Border(1, p.hairline).Children(func() {
				ui.TextInputBase(c.Key("netease-id-in"), &a.alignNeteaseID).Grow(1).MinWidth(0).Height(24).
					FontSize(12.5).TextColor(p.text).Background(ui.Transparent).
					Placeholder("可选 · 歌曲 ID 或链接").Label("网易云歌曲 ID").FocusRing(false)
			})
		})
		segmented(c.Key("align-language"), &a.alignLang, "中文 (HubertFA)", "日文 (MMS 歌声)", "英文 (Echora MMS)", "其它")
		engines := [...]struct{ name, detail string }{
			{"HubertFA", "中文歌声逐字对齐"},
			{"NextFire MMS + MeCab", "日语逐音素对齐与词法读音"},
			{"Echora-300M MMS", "英文按词或字母对齐"},
			{"Echora-300M MMS", "多语种逐字或逐词对齐"},
		}
		engine := engines[min(max(a.alignLang, 0), len(engines)-1)]
		ui.Row(c).FillWidth().Padding(6, 10).Gap(8).Radius(10).AlignItems(ui.Center).
			Background(p.bg).Children(func() {
			artCover(c, 270, 28, 8, iconMic)
			ui.Text(c, engine.name).FontSize(12.5).FontWeight(700)
			ui.Text(c, engine.detail).Grow(1).MinWidth(0).FontSize(11.5).TextColor(p.muted).SingleLine()
			if a.envChecked && !a.envInfo.Ready {
				statusPill(c, "环境未就绪", p.warnBg, p.warn)
			} else {
				statusPill(c, "就绪", p.okBg, p.ok)
			}
		})
	})
}

func (a *app) viewAlignAdvanced(c *ui.Context) {
	p := a.pal
	ui.Column(c.Key("align-advanced")).FillWidth().Padding(8, 18).Gap(8).Radius(16).
		Background(p.surface).Border(1, p.hairline).Children(func() {
		button := ui.ButtonBase(c.Key("align-advanced-toggle")).FillWidth().Height(32).Gap(8).
			AlignItems(ui.Center).Label("高级与输出选项")
		if button.Clicked() {
			a.alignAdvOpen = !a.alignAdvOpen
		}
		progress := b2f(a.alignAdvOpen)
		if !c.Preferences().ReduceMotion {
			progress = clamp01(button.AnimateTo("chevron", progress, motionPane))
		}
		button.Children(func() {
			ui.Text(c, "高级与输出选项").FontSize(14).FontWeight(700)
			summary := []string{fmt.Sprintf("%d 位小数", 3-a.alignDecimals)}
			if a.alignEndTag {
				summary = append(summary, "含行尾标签")
			}
			if a.alignAutoValid {
				summary = append(summary, "自动校验")
			}
			ui.Box(c).Grow(1)
			ui.Text(c, strings.Join(summary, " · ")).MinWidth(0).FontSize(12).TextColor(p.muted).SingleLine()
			ui.Icon(c, iconChevronD).Size(12, 12).TextColor(p.muted).Scale(1, 1-2*progress)
		})
		if !a.alignAdvOpen {
			return
		}
		switchRow := func(key, label string, value *bool) {
			ui.Row(c.Key(key)).FillWidth().AlignItems(ui.Center).Children(func() {
				ui.Text(c, label).Grow(1).FontSize(13)
				settingSwitch(c.Key("switch"), value, "切换"+label)
			})
		}
		heading := func(label string) {
			ui.Box(c).FillWidth().Height(1).Background(p.hairline)
			ui.Text(c, label).FontSize(12).FontWeight(700).TextColor(p.muted)
		}
		heading("参考时间策略")
		switchRow("sw-ignore-refts", "忽略参考时间戳（强制 ASR 重定位）", &a.alignIgnoreRefTimes)
		if !a.alignIgnoreRefTimes {
			switchRow("sw-ignore-refends", "仅使用参考句首", &a.alignIgnoreRefEnds)
		}
		heading("歌声与多语选项")
		switchRow("sw-poly", "多音字读音检查", &a.alignPolyCheck)
		switchRow("sw-waveform-first", "鬼畜 / 波形起音优先", &a.alignWaveformFirst)
		switchRow("sw-sustain", "按人声能量做时长保真", &a.alignSustain)
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "英文粒度").Grow(1).FontSize(13)
			segmented(c.Key("align-en-mode"), &a.alignEnMode, "按词", "按字母均分")
		})
		heading("输出格式")
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "时间戳精度").Grow(1).FontSize(13)
			segmented(c.Key("align-decimals"), &a.alignDecimals, "3 位", "2 位")
		})
		switchRow("sw-endtag", "行尾结束标签", &a.alignEndTag)
		switchRow("sw-autovalid", "完成后自动校验", &a.alignAutoValid)
	})
}
