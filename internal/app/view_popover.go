package app

import "github.com/egoist/mygo/ui"

func popoverButton(c *ui.Context, p palette, icon *ui.SVG, label string, open, disabled bool) ui.Element {
	button := ui.ButtonBase(c).Height(34).PaddingX(10).Gap(6).Radius(999).
		AlignItems(ui.Center).Label(label).Disabled(disabled).Shrink(0)
	bg, fg := p.surface2, p.text
	if disabled {
		fg = p.faint
	} else if open {
		bg, fg = p.accent.Alpha(0.14), p.accent
	} else if button.Hovered() || button.FocusVisible() {
		bg = p.hover
	}
	button.Background(bg)
	progress := b2f(open)
	if !c.Preferences().ReduceMotion {
		progress = clamp01(button.AnimateTo("chevron", progress, motionPane))
	}
	button.Children(func() {
		if icon != nil {
			ui.Icon(c, icon).Size(14, 14).TextColor(fg)
		}
		ui.Text(c, label).FontSize(12).FontWeight(700).TextColor(fg).SingleLine()
		ui.Icon(c, iconChevronD).Size(11, 11).TextColor(fg).Scale(1, 1-2*progress)
	})
	return button
}

func anchoredPopover(c *ui.Context, p palette, button ui.Element, open *bool, width float32, above bool, content func()) {
	progress := b2f(*open)
	if !c.Preferences().ReduceMotion {
		progress = clamp01(button.AnimateTo("popover", progress, motionPane))
	}
	if !*open && progress == 0 {
		return
	}
	shown := true
	ui.PopoverBase(c, button, &shown, func(panel ui.Element) {
		if above {
			panel.AttachTo(button, ui.AnchorTop, ui.AnchorBottom).TransformOrigin(0.5, 1).Margin(0, 0, 6, 0)
		} else {
			panel.AttachTo(button, ui.AnchorBottom, ui.AnchorTop).TransformOrigin(0.5, 0).Margin(6, 0, 0, 0)
		}
		scale := lerp(0.92, 1, progress)
		panel.Width(width).Padding(8).Radius(12).Background(p.raised).Border(1, p.hairline).
			Shadow(0, 6, 18, 0, p.cardShade).Opacity(progress).Scale(scale, scale)
		panel.Children(content)
	})
	if !shown {
		*open = false
	}
}

func popoverItem(c *ui.Context, p palette, label string, fg ui.Color) ui.Element {
	button := ui.ButtonBase(c).FillWidth().Height(32).PaddingX(8).Radius(8).
		AlignItems(ui.Center).Label(label)
	if button.Hovered() || button.FocusVisible() {
		button.Background(p.hover)
	}
	button.Children(func() {
		ui.Text(c, label).FontSize(12.5).TextColor(fg).SingleLine()
	})
	return button
}
