package app

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

type toolViewItem struct {
	title, detail, command, action string
	hue                            float32
	icon                           *ui.SVG
}

func (a *app) viewTools(c *ui.Context) {
	p := a.pal
	workbenches := [...]toolViewItem{
		{"标签刮削", "比对网易云与 iTunes 候选，补齐封面、年份和网易云 ID。", "scrape", "选择目录", 200, iconTag},
		{"人声分离", "用 demucs 分离人声与伴奏，试听后可分别导出。", "separate", "选择音频", 285, iconMic},
		{"清理歌词文件", "按格式筛选歌词文件，可批量移到回收站。", "cleanup", "选择目录", 20, iconTrash},
	}
	quickTools := [...]toolViewItem{
		{"解密加密音频", "将网易云 NCM 解为普通音频并保留标签。", "decrypt", "选择目录", 20, iconUnlock},
		{"校验逐字 LRC", "检查时间戳、行尾标签和跨行越界。", "validate", "选择 LRC", 145, iconCheck},
		{"双语歌词加 ❤️", "给中文翻译行加 ❤️。", "hearts", "选择 LRC", 345, iconHeart},
		{"小数位转换", "将两位小数时间戳转为三位。", "convert", "选择 LRC", 215, iconQueue},
	}
	ui.Scroll(c.Key("tools-scroll")).TrackScroll(&a.toolScroll).Fill().Children(func() {
		ui.Column(c).FillWidth().Padding(pageGap, 38, 108, 38).Gap(16).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Text(c, "工具箱").Grow(1).FontSize(28).FontWeight(800)
				if a.toolLog != "" || a.toolRunning {
					label := "上次输出"
					if a.toolRunning {
						label = "查看运行输出"
					}
					if pillButton(c.Key("tool-last-output"), nil, label, p.surface2, p.text).Clicked() {
						a.toolOutputOpen = true
					}
				}
			})
			ui.Text(c, "独立工作台").FontSize(16).FontWeight(800)
			ui.Row(c.Key("tool-workbenches")).FillWidth().Gap(14).AlignItems(ui.Stretch).Children(func() {
				for _, tool := range workbenches {
					a.viewToolCard(c, tool, false)
				}
			})
			ui.Text(c, "快捷批处理").FontSize(16).FontWeight(800).Margin(4, 0, 0, 0)
			for i := 0; i < len(quickTools); i += 2 {
				ui.Row(c.Key(i)).FillWidth().Gap(14).AlignItems(ui.Stretch).Children(func() {
					for _, tool := range quickTools[i : i+2] {
						a.viewToolCard(c, tool, true)
					}
				})
			}
		})
	})
}

func (a *app) viewToolCard(c *ui.Context, tool toolViewItem, compact bool) {
	p := a.pal
	var card ui.Element
	if compact {
		card = ui.Row(c.Key("tool-" + tool.command)).AlignItems(ui.Center).Padding(12)
	} else {
		card = ui.Column(c.Key("tool-" + tool.command)).Padding(16)
	}
	card.Grow(1).Basis(0).MinWidth(0).Gap(8).Radius(16).Border(1, p.hairline)
	bg := p.surface
	pose := ui.Pose{}
	if card.Hovered() {
		bg = p.surface2
		pose.Y = -2
	}
	card.Background(card.AnimateColor("bg", bg, ui.Animation{Duration: 150 * time.Millisecond}))
	card.AnimatePose(pose, ui.Snappy)
	card.Children(func() {
		artCover(c, tool.hue, 40, 11, tool.icon)
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			ui.Text(c, tool.title).FontSize(15).FontWeight(700)
			ui.Text(c, tool.detail).FontSize(12).TextColor(p.muted)
		})
		actions := ui.Row(c).Justify(ui.End)
		if !compact {
			actions.FillWidth()
		}
		actions.Children(func() {
			disabled := compact && a.toolRunning
			fg := p.text
			if disabled {
				fg = p.faint
			}
			if pillButton(c.Key("run-"+tool.command), nil, tool.action, p.surface2, fg).
				Shrink(0).Disabled(disabled).Clicked() {
				a.runToolInteractive(tool.command)
			}
		})
	})
}

func (a *app) viewToolOutputDialog(c *ui.Context) {
	stage := ui.Box(c.Key("tool-output-motion")).Absolute().Size(0, 0).PassThrough()
	progress := b2f(a.toolOutputOpen)
	if !c.Preferences().ReduceMotion {
		progress = clamp01(stage.AnimateTo("open", progress, motionPane))
	}
	if a.page != pageTools || (!a.toolOutputOpen && progress == 0) {
		return
	}
	p := a.pal
	w, h := c.Size()
	title := a.toolTitle
	if title == "" {
		title = "工具运行输出"
	}
	shown := true
	ui.DialogBase(c.Key("tool-output-dialog"), &shown, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.30*progress))
		scale := lerp(0.92, 1, progress)
		panel.Size(min(680, w*0.9), min(460, h*0.75)).Padding(20).Radius(20).
			Background(p.raised).Border(1, p.hairline).Shadow(0, 12, 36, 0, p.cardShade).
			Label(title).Opacity(progress).Scale(scale, scale).TransformOrigin(0.5, 1)
		ui.Column(c).Fill().Gap(12).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Text(c, title).Grow(1).FontSize(20).FontWeight(800)
				if a.toolRunning {
					winFiveDots(c, p.accent)
					ui.Text(c, "执行中…").FontSize(12).TextColor(p.muted)
				}
			})
			ui.Scroll(c.Key("tool-output-scroll")).Grow(1).MinHeight(0).FillWidth().Radius(10).
				Background(p.bg).Border(1, p.hairline).Children(func() {
				ui.Column(c).FillWidth().Padding(10, 12).Gap(4).Children(func() {
					switch {
					case a.toolLog != "":
						for _, line := range strings.Split(a.toolLog, "\n") {
							ui.Text(c, line).FillWidth().FontSize(12.5).TextColor(p.text)
						}
					case a.toolRunning:
						ui.Text(c, "正在调用本地工具处理…").FontSize(12.5).TextColor(p.muted)
					default:
						ui.Text(c, "执行完毕，无输出").FontSize(12.5).TextColor(p.muted)
					}
				})
			})
			ui.Row(c).FillWidth().Gap(8).Justify(ui.End).Children(func() {
				fg := p.text
				if a.toolLog == "" {
					fg = p.faint
				}
				if pillButton(c.Key("tool-copy"), iconCopy, "复制输出", p.surface2, fg).
					Disabled(a.toolLog == "").Clicked() {
					c.WriteClipboard(a.toolLog)
					a.toast = "已复制工具输出"
				}
				if pillButton(c.Key("tool-close"), nil, "关闭", p.accent, p.onAccent).Clicked() {
					a.toolOutputOpen = false
				}
			})
		})
	})
	if !shown {
		a.toolOutputOpen = false
	}
}
