package app

import (
	"strings"
	"time"

	"github.com/egoist/mygo/kiralrcmenu"
	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"
)

var iconCopy = strokeIcon(`<rect x="8" y="8" width="13" height="13" rx="2"/><path d="M16 8V5a2 2 0 0 0-2-2H5a2 2 0 0 0-2 2v9a2 2 0 0 0 2 2h3"/>`)

type contextItem struct {
	label               string
	icon                *ui.SVG
	separator, disabled bool
	run                 func(*ui.Context)
}

type contextMenu struct {
	x, y       float32
	openedAt   time.Time
	windowDIPs bool
	items      []contextItem
	track      *scrapeTrack
}

var (
	lastContextMenuX    float32
	lastContextMenuY    float32
	lastContextMenuDIPs bool
)

func (a *app) openWindowMenu(x, y float32, items []kiralrcmenu.Item) {
	lastContextMenuX, lastContextMenuY, lastContextMenuDIPs = x, y, true
	a.libFolderMenuOpen = false
	a.libRowMenuPath = ""
	a.scrapeCfgOpen, a.scrapeAutoOpen = false, false
	a.queueCfgOpen = false
	m := &contextMenu{x: x, y: y, windowDIPs: true}
	for _, it := range items {
		item := contextItem{label: it.Label, disabled: it.Disabled, separator: it.Separator}
		switch it.Label {
		case "下载歌词":
			item.icon = iconDownload
		case "刮削标签":
			item.icon = iconTag
		case "新建对齐":
			item.icon = iconPlay
		case "打开校对":
			item.icon = iconEdit
		case "在资源管理器中显示":
			item.icon = iconFolder
		case "复制文件路径":
			item.icon = iconCopy
		case "编辑此行歌词":
			item.icon = iconEdit
		}
		if it.Choose != nil {
			item.run = func(*ui.Context) { it.Choose() }
		}
		m.items = append(m.items, item)
	}
	a.menu = m
}

// openDownloadSourceMenu 在右键位置弹出平台选择菜单，供单曲按默认顺序或指定来源下载歌词。
func (a *app) openDownloadSourceMenu(tr trackItem) {
	items := []contextItem{
		{
			label: "按默认顺序下载",
			icon:  iconDownload,
			run:   func(*ui.Context) { a.downloadTrackFromSource(tr, "") },
		},
		{separator: true},
	}
	for _, key := range normalizeDownloadSources(a.setDownloadSources) {
		k := key
		items = append(items, contextItem{
			label: downloadSourceLabel(k),
			icon:  iconLink,
			run:   func(*ui.Context) { a.downloadTrackFromSource(tr, k) },
		})
	}
	a.menu = &contextMenu{
		x:          lastContextMenuX,
		y:          lastContextMenuY,
		windowDIPs: lastContextMenuDIPs,
		items:      items,
	}
}

func (m *contextMenu) width(c *ui.Context) float32 {
	width := float32(0)
	for _, item := range m.items {
		if item.separator {
			continue
		}
		w, _ := c.MeasureText(0, ui.Span{Text: item.label, Size: 13.5, Weight: 500})
		if item.icon != nil {
			w += 16 + 11
		}
		width = max(width, w+2*(6+10)+2) // panel padding, row padding and border
	}
	if it := m.track; it != nil {
		title, _ := c.MeasureText(0, ui.Span{Text: scrapeTitleOf(*it), Size: 13.5, Weight: 600})
		artist, _ := c.MeasureText(0, ui.Span{Text: strings.TrimSpace(it.Artist), Size: 12})
		width = max(width, min(268, 82+max(title, artist)))
	}
	return max(88, width)
}

func (a *app) viewContextMenu(c *ui.Context) {
	m := a.menu
	if m == nil {
		return
	}
	ink := ui.Hex("#1d1d1f")
	tint := ui.RGBA(250, 250, 252, 0.58)
	border := ui.RGBA(0, 0, 0, 0.1)
	muted, hover, press := ink.Alpha(0.62), ink.Alpha(0.07), ink.Alpha(0.12)
	if a.pal.dark {
		ink, tint, border = white, ui.RGBA(14, 14, 18, 0.62), white.Alpha(0.14)
		muted, hover, press = ink.Alpha(0.6), ink.Alpha(0.1), ink.Alpha(0.16)
	}
	winW, winH := c.Size()
	width := min(m.width(c), winW-16)
	h := float32(12)
	if m.track != nil {
		h += 68.5
	}
	for _, item := range m.items {
		if item.separator {
			h += 9.5
		} else {
			h += 39
		}
	}
	h--
	px, py := m.x, m.y
	if m.windowDIPs {
		px /= c.Zoom()
		py /= c.Zoom()
	}
	x, y := max(8, min(px, winW-width-8)), max(8, py)
	originY := float32(0)
	if y+h > winH-8 {
		y = max(8, py-h)
		originY = 1
	}
	ui.Overlay(c, func() {
		backdrop := ui.Box(c.Key("context-menu-backdrop")).Absolute().Left(0).Top(0).Right(0).Bottom(0).Modal()
		backdrop.Clicked() // Capture outside presses before they reach the underlying view.
		backdrop.Children(func() {
			panel := ui.Column(c.Key("context-menu")).Absolute().Left(x).Top(y).Width(width).Padding(6).Gap(1).
				Role(ui.RoleMenu).Label("菜单").FocusGroup(ui.Vertical).TextColor(ink).Radius(20).
				TransformOrigin(clamp01((px-x)/width), originY).
				Material(glass.Glass{Tint: tint}).Border(0.5, border).Shadow(0, 18, 44, -12, ui.RGBA(0, 0, 0, 0.45))
			if m.openedAt.IsZero() {
				m.openedAt = c.Now()
			}
			elapsed := c.Now().Sub(m.openedAt)
			if elapsed < motionPane.Duration && !c.Preferences().ReduceMotion {
				progress := motionEase(clamp01(float32(elapsed) / float32(motionPane.Duration)))
				panel.Scale(lerp(0.86, 1, progress), lerp(0.72, 1, progress)).Opacity(lerp(0.5, 1, progress))
				c.AnimationFrame()
			}
			panel.Transition(ui.ElementTransition{Animation: &motionPane, Exit: &ui.Motion{Scale: 0.97}})
			panel.Children(func() {
				line := func() { ui.Box(c).Height(0.5).Margin(4, 10).Background(ink.Alpha(0.1)) }
				if it := m.track; it != nil {
					ui.Row(c).Height(58).PaddingX(8).Gap(11).AlignItems(ui.Center).Children(func() {
						if cover := a.scrapeEmbeddedCover(it.Path); cover != nil {
							ui.Image(c, cover).Size(40, 40).Radius(8).Fit(ui.Cover).Shrink(0)
						} else {
							artCover(c, hueForString(it.Path), 40, 8, iconNote)
						}
						artist := strings.TrimSpace(it.Artist)
						if artist == "" {
							artist = "未知歌手"
						}
						ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
							ui.Text(c, scrapeTitleOf(*it)).FontSize(13.5).FontWeight(600).SingleLine()
							ui.Text(c, artist).FontSize(12).TextColor(muted).SingleLine()
						})
					})
					line()
				}
				for _, it := range m.items {
					if it.separator {
						line()
						continue
					}
					b := ui.ButtonBase(c.Key(it.label)).Role(ui.RoleMenuItem).Label(it.label).FillWidth().Height(38).PaddingX(10).Gap(11).Radius(14).
						AlignItems(ui.Center).Justify(ui.Start).FocusRing(false).Disabled(it.disabled)
					fg, bg := ink, ui.Transparent
					switch {
					case it.disabled:
						fg = ink.Alpha(0.42)
					case b.Pressed():
						bg = press
					case b.Hovered() || b.FocusVisible():
						bg = hover
					}
					b.Background(b.AnimateColor("bg", bg, ui.Animation{Duration: 100 * time.Millisecond}))
					b.Children(func() {
						if it.icon != nil {
							ui.Icon(c, it.icon).Size(16, 16).Shrink(0).TextColor(fg.Alpha(0.62))
						}
						ui.Text(c, it.label).FontSize(13.5).FontWeight(500).SingleLine().TextColor(fg)
					})
					if b.Clicked() && !it.disabled {
						a.menu = nil
						it.run(c)
					}
				}
			})
			if panel.PressedOutside() || backdrop.OverlayShortcut(0, ui.KeyEscape) {
				a.menu = nil
			}
		})
	})
}
