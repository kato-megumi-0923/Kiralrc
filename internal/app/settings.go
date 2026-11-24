package app

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

type settingsSection int

const (
	settingsAppearance settingsSection = iota
	settingsRuntime
	settingsAlignment
	settingsSources
	settingsTranslation
	settingsCache
)

func (a *app) openSettings(section settingsSection) {
	a.settingsOpen = true
	a.settingsSection = section
	a.settingsSearch = ""
	a.setScroll = ui.ScrollState{}
	a.menu = nil
}

func (a *app) viewSettings(c *ui.Context) {
	stage := ui.Box(c.Key("settings-motion")).Absolute().Size(0, 0).PassThrough()
	progress := b2f(a.settingsOpen)
	if !c.Preferences().ReduceMotion {
		progress = clamp01(stage.AnimateTo("open", progress, motionPane))
	}
	if progress == 0 && !a.settingsOpen {
		return
	}
	p := a.pal
	sections := [...]struct {
		title, description, keywords string
		icon                         *ui.SVG
		view                         func(*ui.Context)
	}{
		{"外观与主题色", "切换深浅模式与界面强调色。", "外观模式 浅色 深色 主题", iconSun, a.viewSettingsAppearance},
		{"模型与运行环境", "管理 Python 环境、对齐模型和模型下载。", "GPU CUDA HubertFA SOFA MMS Whisper 检测 初始化", iconTools, a.viewSettingsRuntime},
		{"对齐参数", "设置主唱检测、读音覆盖与逐字粒度。", "无主唱 自动跳过 多音字 polyphone 英文 数字", iconWave, a.viewSettingsAlignment},
		{"歌词来源", "配置 Apple Music 和网易云的歌词访问。", "官方歌词 Apple Music token 网易云 Cloudy 登录态", iconLink, a.viewSettingsSources},
		{"AI 歌词翻译", "配置翻译服务、模型和 API Key。", "OpenAI DeepSeek Ollama Gemini Base URL API Key", iconNote, a.viewSettingsTranslation},
		{"缓存管理", "查看和清理对齐与人声分离产生的缓存。", "中间产物 清理 人声分离 目录", iconFolder, a.viewSettingsCache},
	}
	w, h := c.Size()
	// Keep the modal mounted through its closing animation.
	shown := true
	ui.DialogBase(c.Key("settings-dialog"), &shown, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.30*progress))
		panel.Size(min(1200, w*0.92), min(900, h*0.9)).Padding(20).Radius(22).
			Background(p.raised).Border(1, p.hairline).Shadow(0, 12, 36, 0, ui.RGBA(0, 0, 0, 0.26)).
			Label("设置").TransformOrigin(0, 1)
		body := ui.Row(c).Fill().AlignItems(ui.Stretch).Gap(20)
		if progress < 1 {
			scale := lerp(0.92, 1, progress)
			panel.Opacity(progress).Scale(scale, scale)
			body.Cache()
		}
		body.Children(func() {
			ui.Column(c).Width(min(226, max(172, w*0.18))).Shrink(0).Gap(10).Children(func() {
				before := a.settingsSearch
				ui.Row(c).FillWidth().Height(38).PaddingX(10).Gap(8).Radius(9).
					AlignItems(ui.Center).Background(p.surface2).Children(func() {
					ui.Icon(c, iconSearch).Size(15, 15).TextColor(p.muted).Shrink(0)
					ui.TextInputBase(c.Key("settings-search"), &a.settingsSearch).
						Grow(1).MinWidth(0).FontSize(13).Background(ui.Transparent).
						TextColor(p.text).Placeholder("搜索设置").Label("搜索设置").FocusRing(false)
				})
				if before != a.settingsSearch {
					a.setScroll = ui.ScrollState{}
				}
				query := strings.ToLower(strings.TrimSpace(a.settingsSearch))
				var matches []settingsSection
				selectedMatches := false
				for i, section := range sections {
					if strings.Contains(strings.ToLower(section.title+" "+section.description+" "+section.keywords), query) {
						index := settingsSection(i)
						matches = append(matches, index)
						selectedMatches = selectedMatches || index == a.settingsSection
					}
				}
				if !selectedMatches && len(matches) > 0 {
					a.settingsSection = matches[0]
				}
				ui.Scroll(c.Key("settings-nav-scroll")).Grow(1).MinHeight(0).FillWidth().Children(func() {
					ui.Column(c).FillWidth().Gap(4).Children(func() {
						if len(matches) == 0 {
							ui.Text(c, "没有匹配的设置").FontSize(12).TextColor(p.muted)
						}
						for _, index := range matches {
							section := sections[index]
							selected := index == a.settingsSection
							button := ui.ButtonBase(c.Key(section.title)).FillWidth().Height(42).PaddingX(8).
								Gap(9).Radius(9).AlignItems(ui.Center).Justify(ui.Start).Label(section.title).Selected(selected)
							if selected {
								button.Background(p.accent.Alpha(0.14))
							} else if button.Hovered() {
								button.Background(p.hover)
							}
							button.Children(func() {
								ui.Box(c).Size(26, 26).Radius(7).Center().Background(p.surface2).Children(func() {
									ui.Icon(c, section.icon).Size(15, 15).TextColor(p.muted)
								})
								ui.Text(c, section.title).FontSize(13).FontWeight(650).TextColor(p.text).SingleLine()
							})
							if button.Clicked() && !selected {
								a.settingsSection = index
								a.setScroll = ui.ScrollState{}
							}
						}
					})
				})
			})
			ui.Box(c).Width(1).FillHeight().Shrink(0).Background(p.hairline)
			ui.Column(c).Grow(1).MinWidth(0).Gap(14).Children(func() {
				ui.Row(c).FillWidth().AlignItems(ui.Start).Gap(12).Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
						section := sections[a.settingsSection]
						ui.Text(c, section.title).FontSize(25).FontWeight(800)
						ui.Text(c, section.description).FontSize(13).TextColor(p.muted)
					})
					close := ui.ButtonBase(c.Key("settings-close")).Size(34, 34).Radius(17).Center().
						Background(p.surface2).Label("关闭设置").Shrink(0)
					if close.Hovered() {
						close.Background(p.hover)
					}
					close.Children(func() { ui.Icon(c, iconX).Size(16, 16).TextColor(p.muted) })
					if close.Clicked() {
						a.settingsOpen = false
					}
				})
				ui.Scroll(c.Key("settings-content")).TrackScroll(&a.setScroll).
					Grow(1).Basis(0).MinHeight(0).FillWidth().Children(func() {
					ui.Column(c.Key(sections[a.settingsSection].title)).FillWidth().Padding(0, 8, 8, 0).Gap(12).
						Children(func() { sections[a.settingsSection].view(c) })
				})
				if a.toast != "" {
					ui.Text(c, a.toast).FontSize(12).TextColor(p.muted).FillWidth()
				}
			})
		})
	})
	if !shown {
		a.settingsOpen = false
	}
}

func (a *app) viewSettingsAppearance(c *ui.Context) {
	p := a.pal
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
}

func (a *app) viewSettingsRuntime(c *ui.Context) {
	p := a.pal
	gpu := a.setGpuName
	if gpu == "" {
		gpu = "检测中…"
	}
	ui.Text(c, "程序目录 "+appRootDir()+" · GPU "+gpu).FontSize(11.5).TextColor(p.muted)
	ui.Row(c).FillWidth().AlignItems(ui.Center).Margin(6, 0, 0, 0).Children(func() {
		ui.Text(c, "运行状态").FontSize(14).FontWeight(700).Grow(1)
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
		ui.Row(c).FillWidth().Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
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
}

func (a *app) viewSettingsAlignment(c *ui.Context) {
	p := a.pal
	card := ui.Column(c.Key("set-alignment")).FillWidth().Padding(6, 18, 6, 18).Radius(16).Background(p.surface)
	card.Children(func() {
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
	})
}

func (a *app) viewSettingsSources(c *ui.Context) {
	p := a.pal
	card := ui.Column(c.Key("set-sources")).FillWidth().Padding(6, 18, 6, 18).Radius(16).Background(p.surface)
	card.Children(func() {
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
		ui.Row(c).Height(56).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Gap(1).Children(func() {
				ui.Text(c, "网易云登录态（借用 Cloudy 的登录）").FontSize(13.5).FontWeight(600)
				ui.Text(c, "打开后读 Cloudy 存在 Windows 凭据管理器里的 MUSIC_U，走登录态配额；Cookie 只发给 music.163.com").
					FontSize(12).TextColor(p.muted).SingleLine()
			})
			prevCloudy := a.setCloudyAuth
			settingSwitch(c.Key("set-cloudy-auth"), &a.setCloudyAuth, "借用 Cloudy 登录态")
			if a.setCloudyAuth != prevCloudy {
				a.savePrefs()
				if a.setCloudyAuth {
					a.toast = "已允许借用 Cloudy 登录态，重新搜索即可生效"
				} else {
					a.toast = "已关闭：网易云请求改回匿名"
				}
			}
		})
	})
}

func (a *app) viewSettingsTranslation(c *ui.Context) {
	p := a.pal
	card := ui.Column(c.Key("set-translation")).FillWidth().Padding(6, 18, 6, 18).Radius(16).Background(p.surface)
	card.Children(func() {
		ui.Column(c.Key("set-ai-row")).FillWidth().Padding(10, 0, 10, 0).Gap(8).Children(func() {
			ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).Gap(1).Children(func() {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "AI 歌词翻译").FontSize(13.5).FontWeight(600)
						if a.setAIMode == 0 {
							statusPill(c, "官方内置 · 已启用", p.okBg, p.ok)
						} else if a.aiTranslateConfigured() {
							statusPill(c, "自定义 · 已启用", p.okBg, p.ok)
						} else {
							statusPill(c, "未配置", p.surface2, p.muted)
						}
					})
					ui.Text(c, "在编辑页一键整首翻译日/英/韩等外语歌词；支持开箱即用的官方翻译，也可接入自己的 DeepSeek / OpenAI / Ollama").FontSize(12).TextColor(p.muted)
				})
				prevMode := a.setAIMode
				segmented(c.Key("set-ai-mode"), &a.setAIMode, "官方内置", "自定义 API")
				if a.setAIMode != prevMode {
					a.setAIURLDropOpen = false
					a.setAIModelDropOpen = false
					a.savePrefs()
				}
			})
			if a.setAIMode == 0 {
				infoBox := ui.Column(c.Key("set-ai-official-info")).FillWidth().Padding(12, 14, 12, 14).Gap(4).Radius(12).Background(p.bg)
				infoBox.Draw(func(pt *ui.Painter, r ui.Rect) { pt.Stroke(r, p.hairline, 12, 1) })
				infoBox.Children(func() {
					ui.Text(c, "当前使用 Kiralrc 官方内置翻译（DeepSeek）").FontSize(12.5).FontWeight(650).TextColor(p.text)
					ui.Text(c, "无需填写 API Key，在编辑器点「翻译」即可直接使用；单小时限 30 首。如需无限制批量翻译或使用其它模型，请切换到右侧「自定义 API」。").FontSize(12).TextColor(p.muted)
				})
			} else {
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
			}
		})
	})
}

func (a *app) viewSettingsCache(c *ui.Context) {
	p := a.pal
	card := ui.Column(c.Key("set-cache")).FillWidth().Padding(6, 18, 6, 18).Radius(16).Background(p.surface)
	card.Children(func() {
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
}
