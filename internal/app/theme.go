package app

import (
	"math"
	"sync"
	"time"

	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/shader"
)

var white = ui.Hex("#ffffff")

var (
	motionEase = ui.CubicBezier(0.22, 0.8, 0.24, 1)
	motionFast = ui.Animation{Spring: ui.SpringOptions{Duration: 200 * time.Millisecond}}
	motionPane = ui.Animation{Duration: 280 * time.Millisecond, Ease: motionEase}
	motionPage = ui.Animation{Duration: 340 * time.Millisecond, Ease: motionEase}
)

type navPillMatch struct{}
type tabPillMatch struct{}
type linePillMatch struct{}
type candPillMatch struct{}
type charPillMatch struct{}

type palette struct {
	dark      bool
	bg        ui.Color
	sidebar   ui.Color
	surface   ui.Color
	surface2  ui.Color
	raised    ui.Color
	text      ui.Color
	muted     ui.Color
	faint     ui.Color
	hairline  ui.Color
	hover     ui.Color
	press     ui.Color
	accent    ui.Color
	onAccent  ui.Color
	ok        ui.Color
	okBg      ui.Color
	warn      ui.Color
	warnBg    ui.Color
	err       ui.Color
	errBg     ui.Color
	cardShade ui.Color
}

var accentColors = []struct{ id, label, hex string }{
	{"red", "网易云红", ""},
	{"clay", "陶土", "#D97757"},
	{"amber", "琥珀", "#E0A526"},
	{"green", "青草", "#4E9E60"},
	{"teal", "湖水", "#2E9191"},
	{"blue", "海蓝", "#3D7BD9"},
	{"violet", "紫罗兰", "#7C5CD6"},
	{"rose", "玫瑰", "#D6608E"},
}

func paletteFor(dark bool) palette {
	if dark {
		return palette{
			dark:      true,
			bg:        ui.Hex("#141416"),
			sidebar:   ui.Hex("#1c1c1f"),
			surface:   ui.RGBA(255, 255, 255, 0.045),
			surface2:  ui.RGBA(255, 255, 255, 0.075),
			raised:    ui.Hex("#222226"),
			text:      ui.Hex("#f5f5f7"),
			muted:     ui.RGBA(235, 235, 245, 0.58),
			faint:     ui.RGBA(235, 235, 245, 0.32),
			hairline:  ui.RGBA(255, 255, 255, 0.075),
			hover:     ui.RGBA(255, 255, 255, 0.06),
			press:     ui.RGBA(255, 255, 255, 0.10),
			accent:    ui.Oklch(0.66, 0.2, 22),
			onAccent:  ui.Hex("#ffffff"),
			ok:        ui.Hex("#5cd68d"),
			okBg:      ui.RGBA(92, 214, 141, 0.15),
			warn:      ui.Hex("#f5b942"),
			warnBg:    ui.RGBA(245, 185, 66, 0.15),
			err:       ui.Hex("#ff7a7a"),
			errBg:     ui.RGBA(255, 122, 122, 0.16),
			cardShade: ui.RGBA(0, 0, 0, 0.45),
		}
	}
	return palette{
		bg:        ui.Hex("#ffffff"),
		sidebar:   ui.Hex("#f4f4f6"),
		surface:   ui.RGBA(0, 0, 0, 0.034),
		surface2:  ui.RGBA(0, 0, 0, 0.056),
		raised:    ui.Hex("#ffffff"),
		text:      ui.Hex("#1d1d1f"),
		muted:     ui.RGBA(60, 60, 67, 0.64),
		faint:     ui.RGBA(60, 60, 67, 0.34),
		hairline:  ui.RGBA(0, 0, 0, 0.072),
		hover:     ui.RGBA(0, 0, 0, 0.042),
		press:     ui.RGBA(0, 0, 0, 0.08),
		accent:    ui.Oklch(0.6, 0.21, 24),
		onAccent:  ui.Hex("#ffffff"),
		ok:        ui.Hex("#2e8b57"),
		okBg:      ui.RGBA(46, 139, 87, 0.12),
		warn:      ui.Hex("#c47a00"),
		warnBg:    ui.RGBA(217, 140, 0, 0.13),
		err:       ui.Hex("#d12c2c"),
		errBg:     ui.RGBA(209, 44, 44, 0.12),
		cardShade: ui.RGBA(0, 0, 0, 0.16),
	}
}

func themeAccent(p palette, chosen string) palette {
	if chosen == "" {
		return p
	}
	c := ui.Hex(chosen)
	p.accent = accentFor(c, p.dark)
	p.onAccent = inkOnAccent(p.accent)
	bg, side := float32(0.08), float32(0.14)
	if p.dark {
		bg, side = 0.10, 0.15
	}
	p.bg = p.bg.Mix(c, bg)
	p.sidebar = p.sidebar.Mix(c, side)
	p.raised = p.raised.Mix(c, bg*0.6)
	return p
}

func accentFor(c ui.Color, dark bool) ui.Color {
	l, ch, h := oklchOf(c)
	if dark {
		l = min(max(l, 0.64), 0.82)
	} else {
		l = min(max(l, 0.48), 0.62)
	}
	return ui.Oklch(float32(l), float32(min(ch, 0.2)), float32(h))
}

func inkOnAccent(accent ui.Color) ui.Color {
	if l, _, _ := oklchOf(accent); l > 0.72 {
		return ui.Hex("#1F1E1D")
	}
	return white
}

func oklchOf(c ui.Color) (l, ch, h float64) {
	lin := func(v uint8) float64 {
		x := float64(v) / 255
		if x <= 0.04045 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	r, g, b := lin(c.R), lin(c.G), lin(c.B)
	lr := cbrt(0.4122214708*r + 0.5363325363*g + 0.0514459929*b)
	lg := cbrt(0.2119034982*r + 0.6806995451*g + 0.1073969566*b)
	lb := cbrt(0.0883024619*r + 0.2817188376*g + 0.6299787005*b)
	l = 0.2104542553*lr + 0.7936177850*lg - 0.0040720468*lb
	a := 1.9779984951*lr - 2.4285922050*lg + 0.4505937099*lb
	bb := 0.0259040371*lr + 0.7827717662*lg - 0.8086757660*lb
	ch = math.Hypot(a, bb)
	h = math.Atan2(bb, a) * 180 / math.Pi
	if h < 0 {
		h += 360
	}
	return l, ch, h
}

func cbrt(x float64) float64 {
	if x < 0 {
		return -math.Cbrt(-x)
	}
	return math.Cbrt(x)
}

func b2f(b bool) float32 {
	if b {
		return 1
	}
	return 0
}

func clamp01(x float32) float32 { return min(max(x, 0), 1) }
func lerp(a, b, t float32) float32 { return a + (b-a)*t }

// barGlass is Cloudy's liquid-glass material for floating capsule bars.
type barGlass struct {
	dark        bool
	clear, bend float32
	surface     bool
	translucent bool
}

const (
	barGlassSpread  = 0.05
	barGlassRim     = 0.5
	barGlassRimWide = 1.1
)

const barGlassSource = `package main

var Bend float
var Bezel float
var Spread float
var Rim float
var RimWide float
var Tone vec4
var Ink vec4
var Corner float
var Surface float
var Film float

func edge(pos, half vec2) vec3 {
	q := pos - half
	r := min(Corner, min(half.x, half.y))
	a := abs(q) - (half - vec2(r))
	m := max(a, vec2(0))
	inside := r - length(m) - min(max(a.x, a.y), 0)
	n := vec2(0)
	if m.x > 0.0 || m.y > 0.0 {
		n = sign(q) * m / length(m)
	} else if a.x > a.y {
		n.x = sign(q.x)
	} else {
		n.y = sign(q.y)
	}
	return vec3(n, max(inside, 0))
}

func lens(x float) float {
	u := clamp(1.0-x, 0.0, 1.0)
	v := sqrt(max(1.0-u*u, 0.0001))
	n := sqrt(u*u + v*v)
	s := u / n
	c := v / n
	st := s / 1.5
	ct := sqrt(1.0 - st*st)
	return (s*ct - c*st) / (c*ct + s*st) / 1.118034
}

func tone(c vec3) vec3 {
	l := dot(c, vec3(0.2126, 0.7152, 0.0722))
	t := Tone.x + (Tone.y-Tone.x)*(1.0-pow(max(1.0-l, 0.0), Tone.z))
	k := (Tone.y - Tone.x) * Tone.w
	return clamp(vec3(t)+(c-vec3(l))*k, vec3(0), vec3(1))
}

func Fragment(dst vec4, pos vec2) vec4 {
	half := rectSize() / 2.0
	e := edge(pos, half)
	bezel := min(half.x, half.y) * Bezel
	if Surface > 0.0 {
		bezel = min(bezel, 36.0)
	}
	n := e.xy
	t := e.z
	bend := vec2(0)
	if t < bezel {
		bend = n * Bend * lens(t/bezel)
	}
	r := backdropSample(pos - bend*(1.0+Spread))
	g := backdropSample(pos - bend)
	b := backdropSample(pos - bend*(1.0-Spread))
	c := tone(vec3(r.r, g.g, b.b))
	alpha := Film
	if Film < 1.0 {
		rgb := vec3(r.r/max(r.a, 0.01), g.g/max(g.a, 0.01), b.b/max(b.a, 0.01))
		c = mix(Ink.rgb, tone(rgb), g.a)
		alpha = mix(Film, 1.0, g.a)
	}
	width := rectSize().x
	side := max(width*0.5-220.0, 130.0)
	under := max(1.0-smoothstep(side*0.72, side*1.02, pos.x), 0.7*smoothstep(width-side*0.6, width-side*0.35, pos.x))
	c = mix(c, Ink.rgb, Ink.a*mix(under, 0.18, Surface))
	if Rim > 0.0 && t < 3.0*RimWide {
		x := t / RimWide
		c += (vec3(abs(n.y)) - c) * Rim * exp(-x*x)
	}
	return vec4(c * alpha, alpha)
}
`

var barGlassShader = sync.OnceValue(func() *shader.Shader { return shader.MustCompile(barGlassSource) })

func (g barGlass) PaintMaterial(p *ui.Painter, box ui.Rect, radii [4]float32) {
	if !p.Accelerated() {
		alpha := float32(0.76)
		tint := white.Alpha(alpha)
		if g.dark {
			tint = ui.RGBA(24, 24, 28, 0.82)
		}
		glass.Paint(p, box, radii[0], glass.Glass{Tint: tint})
		p.Stroke(box, ui.RGBA(255, 255, 255, 0.22), radii[0], 1)
		return
	}
	tone := [4]float32{0.62 - 0.4*g.clear, 1, 1.2, 1.35}
	if g.dark {
		tone = [4]float32{0.03, 0.32 + 0.48*g.clear, 1.5, 2.4}
	}
	blur := 2.8 * (1 - g.clear) * (1 - g.clear)
	ink := [4]float32{0.97, 0.97, 0.98, 0.85}
	if g.dark {
		ink = [4]float32{0.05, 0.05, 0.06, 0.85}
	}
	film, surface, bend := float32(1), float32(0), 144*g.bend
	if g.translucent {
		film = lerp(0.8, 0.36, clamp01(g.clear))
	}
	if g.surface {
		surface = 1
		bend = min(bend, 1.2*min(box.W, box.H)*g.bend, 76*g.bend)
	}
	options := shader.Options{Radius: radii[0], BackdropBlur: blur, Uniforms: map[string]any{
		"Bend": bend, "Bezel": 0.33 + 0.66*g.bend, "Spread": barGlassSpread,
		"Rim": barGlassRim, "RimWide": barGlassRimWide, "Tone": tone[:], "Ink": ink[:],
		"Corner": radii[0], "Surface": surface, "Film": film,
	}}
	barGlassShader().Draw(p, box, options)
}

// Icons (Lucide 24pt grid)
func strokeIcon(paths string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">` + paths + `</svg>`))
}

func fillIcon(paths string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor">` + paths + `</svg>`))
}

var (
	iconPlay       = fillIcon(`<path d="M7 4.6v14.8c0 .8.9 1.3 1.6.9l12-7.4c.6-.4.6-1.3 0-1.7l-12-7.4C7.9 3.3 7 3.8 7 4.6z"/>`)
	iconVolume     = strokeIcon(`<polygon points="11 5 6 9 2 9 2 15 6 15 11 19 11 5"/><path d="M15.54 8.46a5 5 0 0 1 0 7.07"/><path d="M19.07 4.93a10 10 0 0 1 0 14.14"/>`)
	iconVolumeMute = strokeIcon(`<polygon points="11 5 6 9 2 9 2 15 6 15 11 19 11 5"/><line x1="23" y1="9" x2="17" y2="15"/><line x1="17" y1="9" x2="23" y2="15"/>`)
	iconPause    = fillIcon(`<rect x="5.5" y="4" width="4.5" height="16" rx="1.4"/><rect x="14" y="4" width="4.5" height="16" rx="1.4"/>`)
	iconNext     = fillIcon(`<path d="M3 5.6v12.8c0 .8.9 1.3 1.6.8l9.4-6.4c.6-.4.6-1.2 0-1.6L4.6 4.8C3.9 4.3 3 4.8 3 5.6z"/><path d="M12 5.6v12.8c0 .8.9 1.3 1.6.8l9.4-6.4c.6-.4.6-1.2 0-1.6l-9.4-6.4c-.7-.5-1.6 0-1.6.8z" transform="translate(-1.5 0)"/>`)
	iconPrev     = fillIcon(`<g transform="matrix(-1 0 0 1 24 0)"><path d="M3 5.6v12.8c0 .8.9 1.3 1.6.8l9.4-6.4c.6-.4.6-1.2 0-1.6L4.6 4.8C3.9 4.3 3 4.8 3 5.6z"/><path d="M12 5.6v12.8c0 .8.9 1.3 1.6.8l9.4-6.4c.6-.4.6-1.2 0-1.6l-9.4-6.4c-.7-.5-1.6 0-1.6.8z" transform="translate(-1.5 0)"/></g>`)
	iconNote     = strokeIcon(`<path d="M9 18V5l12-2v13"/><circle cx="6" cy="18" r="3"/><circle cx="18" cy="16" r="3"/>`)
	iconSearch   = strokeIcon(`<circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5"/>`)
	iconLibrary  = strokeIcon(`<path d="M4 19.5v-15A2.5 2.5 0 0 1 6.5 2H20v20H6.5a2.5 2.5 0 0 1 0-5H20"/>`)
	iconPlus     = strokeIcon(`<circle cx="12" cy="12" r="9"/><path d="M12 8v8M8 12h8"/>`)
	iconQueue    = strokeIcon(`<path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8"/><path d="M3 3v5h5"/><path d="M12 7v5l3 2"/>`)
	iconEdit     = strokeIcon(`<path d="M12 20h9"/><path d="M16.5 3.5a2.12 2.12 0 1 1 3 3L7 19l-4 1 1-4Z"/>`)
	iconTools    = strokeIcon(`<path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76Z"/>`)
	iconSettings = strokeIcon(`<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9c.26.6.85 1 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1Z"/>`)
	iconCheck    = strokeIcon(`<path d="M20 6 9 17l-5-5"/>`)
	iconUnlock   = strokeIcon(`<rect x="3" y="11" width="18" height="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 9.9-1"/>`)
	iconWarn     = strokeIcon(`<path d="m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3Z"/><path d="M12 9v4M12 17h.01"/>`)
	iconLink     = strokeIcon(`<path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/><path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>`)
	iconMic      = strokeIcon(`<rect x="9" y="2" width="6" height="12" rx="3"/><path d="M19 10v1a7 7 0 0 1-14 0v-1"/><path d="M12 18v4"/>`)
	iconHeart    = strokeIcon(`<path d="M19 14c1.49-1.46 3-3.21 3-5.5A5.5 5.5 0 0 0 16.5 3c-1.76 0-3 .5-4.5 2-1.5-1.5-2.74-2-4.5-2A5.5 5.5 0 0 0 2 8.5c0 2.3 1.5 4.05 3 5.5l7 7Z"/>`)
	iconDownload = strokeIcon(`<path d="M12 3v10"/><path d="m7.5 8.5 4.5 4.5 4.5-4.5"/><path d="M4 15v4a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-4"/>`)
	iconWave     = strokeIcon(`<polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/>`)
	iconFolder   = strokeIcon(`<path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/>`)
	iconRefresh  = strokeIcon(`<path d="M21 12a9 9 0 1 1-3-6.7L21 8"/><path d="M21 3v5h-5"/>`)
	iconSun      = strokeIcon(`<circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M6.34 17.66l-1.41 1.41M19.07 4.93l-1.41 1.41"/>`)
	iconX        = strokeIcon(`<path d="M18 6 6 18M6 6l12 12"/>`)
)

func roundButton(c *ui.Context, size float32, color ui.Color, label string) ui.Element {
	b := ui.ButtonBase(c).Size(size+14, size+14).Center().Radius(999).Label(label).TextColor(color).FocusRing(false)
	bg := ui.Transparent
	if b.Hovered() {
		bg = ui.RGBA(128, 128, 128, 0.14)
	}
	b.Background(b.AnimateColor("bg", bg, ui.Animation{Duration: 120 * time.Millisecond}))
	pose := ui.Pose{}
	if b.Pressed() {
		pose.Scale = 0.88
	}
	b.AnimatePose(pose, ui.Snappy)
	return b
}

func iconButton(c *ui.Context, icon *ui.SVG, size float32, color ui.Color, label string) ui.Element {
	b := roundButton(c, size, color, label)
	b.Children(func() { ui.Icon(c, icon).Size(size, size) })
	return b
}

func pillButton(c *ui.Context, icon *ui.SVG, label string, bg, fg ui.Color) ui.Element {
	b := ui.ButtonBase(c).Height(34).PaddingX(16).Gap(7).Radius(999).AlignItems(ui.Center).TextColor(fg).Label(label).FocusRing(false)
	fill := bg
	if b.Hovered() {
		fill = bg.Mix(ui.RGB(255, 255, 255), 0.12)
	}
	b.Background(b.AnimateColor("bg", fill, ui.Animation{Duration: 120 * time.Millisecond}))
	pose := ui.Pose{}
	if b.Pressed() {
		pose.Scale = 0.96
	}
	b.AnimatePose(pose, ui.Snappy)
	b.Children(func() {
		if icon != nil {
			ui.Icon(c, icon).Size(14, 14)
		}
		ui.Text(c, label).FontSize(13).FontWeight(600).SingleLine()
	})
	return b
}

func settingSwitch(c *ui.Context, on *bool, label string) ui.Element {
	t := c.Theme()
	sw := ui.SwitchBase(c, on).Size(40, 22).Radius(11).Shrink(0).FocusRing(false).Label(label)
	pos := sw.AnimateTo("knob", b2f(*on), ui.Snappy)
	off := ui.RGBA(120, 120, 128, 0.28)
	if t.Dark {
		off = ui.RGBA(120, 120, 128, 0.42)
	}
	sw.Background(off.Mix(t.Accent, min(max(pos, 0), 1)))
	sw.Draw(func(p *ui.Painter, r ui.Rect) {
		const in = 2
		d := r.H - 2*in
		knob := ui.Rect{X: r.X + in + pos*(r.W-r.H), Y: r.Y + in, W: d, H: d}
		p.Shadow(knob, d/2, 0, 1, 3, 0, ui.RGBA(0, 0, 0, 0.26))
		p.Fill(knob, white, d/2)
	})
	return sw
}

func segmented(c *ui.Context, selected *int, labels ...string) bool {
	t := c.Theme()
	const height, inset, radius = 28, 3, 10
	width := float32(0)
	for _, l := range labels {
		w, _ := c.MeasureText(0, ui.Span{Text: l, Size: 12.5})
		width = max(width, w+22)
	}
	track, capsule, press := ui.RGBA(0, 0, 0, 0.055), ui.RGB(255, 255, 255), ui.RGBA(0, 0, 0, 0.06)
	if t.Dark {
		track, capsule, press = ui.RGBA(255, 255, 255, 0.075), ui.RGBA(255, 255, 255, 0.18), ui.RGBA(255, 255, 255, 0.07)
	}
	before := *selected
	parts := ui.SegmentedBase(c, selected, len(labels))
	at := parts.Track.AnimateTo("at", float32(*selected), ui.Animation{Spring: ui.SpringOptions{Duration: 260 * time.Millisecond, Bounce: 0.08}})
	dark := t.Dark
	parts.Track.Height(height).Padding(inset).Radius(radius).Background(track).Draw(func(p *ui.Painter, r ui.Rect) {
		pill := ui.Rect{X: r.X + inset + at*width, Y: r.Y + inset, W: width, H: r.H - 2*inset}
		if dark {
			p.Shadow(pill, radius-inset, 0, 1, 3, 0, ui.RGBA(0, 0, 0, 0.28))
		} else {
			p.Shadow(pill, radius-inset, 0, 1, 3, 0, ui.RGBA(0, 0, 0, 0.12))
		}
		p.Fill(pill, capsule, radius-inset)
	}).Children(func() {
		for i, l := range labels {
			seg := parts.Segment(i).Width(width).Height(height - 2*inset).Center().Radius(radius - inset)
			on := i == *selected
			if seg.Pressed() && !on {
				seg.Background(press)
			}
			color, weight := t.Text.Alpha(0.62), 500
			if on {
				color, weight = t.Text, 700
			} else if seg.Hovered() {
				color = t.Text
			}
			seg.Children(func() { ui.Text(c, l).FontSize(12.5).FontWeight(weight).TextColor(color).SingleLine() })
		}
	})
	return *selected != before
}

// artCover draws Cloudy's light-gradient album cover tile.
func artCover(c *ui.Context, hue float32, size, radius float32, icon *ui.SVG) ui.Element {
	c1 := ui.Oklch(0.68, 0.17, hue)
	c2 := ui.Oklch(0.52, 0.16, hue+32)
	box := ui.Box(c).Size(size, size).Radius(radius).Center().Shrink(0)
	box.Draw(func(p *ui.Painter, r ui.Rect) {
		p.FillGradient(r, ui.LinearGradient{
			Angle: 135,
			Stops: []ui.GradientStop{{At: 0, Color: c1}, {At: 1, Color: c2}},
		}, radius)
		p.Stroke(r, ui.RGBA(255, 255, 255, 0.18), radius, 1)
	})
	if icon != nil {
		box.Children(func() {
			ui.Icon(c, icon).Size(size*0.42, size*0.42).TextColor(white)
		})
	}
	return box
}

// statusPill draws a soft rounded pill badge.
func statusPill(c *ui.Context, label string, bg, fg ui.Color) ui.Element {
	b := ui.Box(c).Height(22).PaddingX(9).Radius(999).Center().Background(bg).Shrink(0)
	b.Children(func() {
		ui.Text(c, label).FontSize(11.5).FontWeight(700).TextColor(fg).SingleLine()
	})
	return b
}
