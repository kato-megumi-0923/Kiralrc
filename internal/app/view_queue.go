package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

func (a *app) viewQueue(c *ui.Context) {
	p := a.pal
	sc := ui.Scroll(c.Key("queue-scroll")).TrackScroll(&a.queueScroll).Fill()
	sc.Children(func() {
		col := ui.Column(c).FillWidth().Padding(pageGap, 38, 108, 38).Gap(18)
		col.Children(func() {
			totalTasks := len(a.queueItems)
			ui.Row(c).AlignItems(ui.End).Gap(12).Children(func() {
				ui.Column(c).Grow(1).Gap(2).Children(func() {
					ui.Text(c, fmt.Sprintf("当前共 %d 个任务", totalTasks)).FontSize(11.5).FontWeight(600).TextColor(p.muted)
					ui.Text(c, "任务队列").FontSize(28).FontWeight(800)
				})
				mode := "失败时暂停"
				if a.queueKeepGoing {
					mode = "失败不中断"
				}
				summary := fmt.Sprintf("并发 %d · %s", a.queueConcurrency+1, mode)
				cfg := popoverButton(c.Key("q-cfg-btn"), p, iconSettings, summary, a.queueCfgOpen, false).Label("队列设置")
				if cfg.Clicked() {
					a.queueCfgOpen = !a.queueCfgOpen
					a.menu = nil
				}
				anchoredPopover(c.Key("q-cfg-popover"), p, cfg, &a.queueCfgOpen, 268, false, func() {
					ui.Column(c).FillWidth().Padding(4).Gap(10).Children(func() {
						ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
							ui.Text(c, "并发数").Grow(1).FontSize(13)
							if segmented(c.Key("q-concurrency"), &a.queueConcurrency, "1", "2", "3") {
								a.savePrefs()
								if len(a.queueItems) > 0 {
									a.startQueueRunner()
								}
							}
						})
						ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
							ui.Text(c, "失败不中断").Grow(1).FontSize(13)
							previous := a.queueKeepGoing
							settingSwitch(c.Key("q-keepgoing"), &a.queueKeepGoing, "失败不中断")
							if a.queueKeepGoing != previous {
								a.savePrefs()
							}
						})
					})
				})
				if totalTasks > 0 {
					if pillButton(c.Key("q-clear"), nil, "清空队列", p.surface2, p.text).Clicked() {
						a.queueActiveTitle = ""
						a.queueActiveSub = ""
						a.queueItems = nil
						a.queueLogs = nil
						a.queueLogOpen = false
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
					ui.Text(c, "你可以在「曲库」中勾选歌曲后选择「对齐歌词」，或在「新建对齐」中选择单首音频加入队列。").
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
						if n := len(a.queueLogs); n > 0 {
							if popoverButton(c.Key("q-log-toggle"), p, nil, fmt.Sprintf("运行日志 (%d)", n), a.queueLogOpen, false).Clicked() {
								a.queueLogOpen = !a.queueLogOpen
							}
						}
					})

					// 6-stage horizontal stepper
					stages := []struct {
						idx  int
						t, d string
					}{
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
							card := ui.Column(c.Key(s.t)).Grow(1).Basis(0).MinWidth(0).Padding(10).Gap(4).Radius(12).
								AlignItems(ui.Start)
							cardBg := p.bg
							if active {
								cardBg = p.accent.Alpha(0.11)
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
						}
					})

					if a.queueLogOpen && len(a.queueLogs) > 0 {
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
