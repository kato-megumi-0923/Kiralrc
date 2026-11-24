package app

import (
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
)

func (a *app) viewEditor(c *ui.Context) {
	p := a.pal
	previousTheme := c.Theme()
	editorTheme := *previousTheme
	editorTheme.Text = p.text
	c.SetTheme(&editorTheme)
	defer c.SetTheme(previousTheme)
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
		a.viewEditorHeader(c)

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
									if in.Shortcut(0, ui.KeyEscape) {
										a.edEditingLine = -1
									} else if in.Submitted() {
										a.commitEditLyricLine()
									}
									okBtn := ui.ButtonBase(c.Key(fmt.Sprintf("ed-ln-ok-%d", idx))).Size(22, 22).Radius(6).Center().
										Background(p.accent).Label("确认修改").FocusRing(false)
									okBtn.Children(func() { ui.Icon(c, iconCheck).Size(12, 12).TextColor(p.onAccent) })
									if okBtn.Clicked() {
										a.commitEditLyricLine()
									}
									if iconButton(c.Key(fmt.Sprintf("ed-ln-cancel-%d", idx)), iconX, 12, p.muted, "取消修改").Clicked() {
										a.edEditingLine = -1
									}
								})
								continue
							}
							row := ui.ButtonBase(c.Key(fmt.Sprintf("ed-ln-%d", idx))).FillWidth().Height(editorLineHeight(line)).PaddingX(10).Gap(6).Radius(8).
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
			stageW := stage.LayoutBounds().W
			compact := stageW > 0 && stageW < 460
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
					if !compact || a.edPreviewMode == 0 {
						fontSize := float32(12)
						if compact {
							fontSize = 11
						}
						ui.Text(c, fmtSec(curGlyph.Start)+" / "+fmtSec(curLine.End)).Grow(1).MinWidth(0).
							FontSize(fontSize).TextColor(p.muted).TextAlign(ui.End).SingleLine()
					}
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
					previewArea := ui.Box(c.Key("ed-pv-area")).Grow(1).Basis(0).MinHeight(0).FillWidth().Clip()
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
							previewW := previewArea.LayoutBounds().W
							if previewW == 0 {
								previewW = 560
							}
							if compact {
								charFontSize = min(charFontSize, max(16, previewW/12))
							}
							cellPx := float64(charFontSize) + 8.0
							if a.edShowPinyin && !compact && cellPx < 39.0 {
								cellPx = 39.0
							}
							maxRowUnits := max(6.0, float64(previewW-28)/cellPx)

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
					if !compact {
						ui.Text(c, "倍速").FontSize(12).TextColor(p.muted).Margin(0, 0, 0, 2).Shrink(0).SingleLine()
					}
					prevSpd := a.edSpeedMode
					segmented(c, &a.edSpeedMode, "0.5×", "0.75×", "1×")
					if a.edSpeedMode != prevSpd {
						a.setPlaybackSpeed(a.edSpeedMode)
					}
					if !compact {
						ui.Box(c).Grow(1)
						a.viewEditorPreviewOptions(c)
					}
				})
				if compact {
					ui.Row(c.Key("ed-preview-options")).FillWidth().Height(30).Gap(6).AlignItems(ui.Center).Justify(ui.End).Children(func() {
						a.viewEditorPreviewOptions(c)
					})
				}
			})

			a.viewEditorInspector(c, curLine, curGlyph)
		})

		a.viewEditorTimeline(c)
	})
	a.editorShortcuts(c)
}

func (a *app) viewEditorPreviewOptions(c *ui.Context) {
	a.volumeControl(c, "ed-vol")
	ui.Text(c, "时间").FontSize(12).TextColor(a.pal.muted).Margin(0, 0, 0, 2).Shrink(0).SingleLine()
	settingSwitch(c.Key("st-py"), &a.edShowPinyin, "显示逐字时间")
}

func (a *app) viewEditorHeader(c *ui.Context) {
	p := a.pal
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
					slide := time.Duration(float64(run) / speed * float64(time.Second))
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
						ui.Text(c, title).Width(tw+8).Shrink(0).FontSize(13).FontWeight(700).NoWrap().Translate(-off, 0)
					})
				}
				ui.Text(c, a.edSub).FontSize(11).TextColor(p.muted).SingleLine()
			})
		})
		ui.Box(c).Grow(1).FillHeight().DragWindow()
		prevTrack := a.edTrackMode
		segmented(c, &a.edTrackMode, "原曲", "人声轨")
		if a.edTrackMode != prevTrack {
			a.switchTrackMode(a.edTrackMode)
		}
		a.viewEditorMore(c)
		saveBg, saveFg := p.surface2, p.text
		if a.edModified {
			saveBg, saveFg = p.accent, p.onAccent
		}
		if pillButton(c.Key("ed-export"), nil, "保存 LRC", saveBg, saveFg).Shrink(0).Clicked() {
			a.saveLrcFile()
		}
	})
}

func (a *app) viewEditorInspector(c *ui.Context, curLine lyricLine, curGlyph lyricChar) {
	p := a.pal
	// Right: Character inspector & validation issues (compact 232px for a wider center stage)
	right := ui.Column(c.Key("ed-props")).Width(232).MinHeight(0).Shrink(0).Padding(12, 12, 12, 12).Gap(8).Clip()
	right.Draw(func(pt *ui.Painter, r ui.Rect) {
		pt.Fill(ui.Rect{X: r.X, Y: r.Y, W: 1, H: r.H}, p.hairline, 0)
	})
	right.Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			gb := ui.Box(c).MinWidth(50).Height(50).PaddingX(8).Radius(14).Center().Background(p.accent.Alpha(0.14))
			gb.Children(func() {
				disp := strings.TrimSpace(curGlyph.Ch)
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

		ui.Text(c, fmt.Sprintf("[%s] · 尾 <%s> · 共 %d 字", fmtSec(curLine.Start), fmtSec(curLine.End), len(a.edGlyphs))).FontSize(11.5).TextColor(p.muted)
		ui.Box(c).FillWidth().Height(1).Shrink(0).Background(p.hairline)
		ui.Text(c, "整首整体偏移").FontSize(12).FontWeight(700).TextColor(p.muted)
		ui.Row(c).Gap(8).Children(func() {
			if pillButton(c.Key("ed-shift-back"), nil, "-50ms", p.surface2, p.text).Grow(1).Basis(0).Justify(ui.Center).Clicked() {
				a.shiftAllLyrics(-0.05)
			}
			if pillButton(c.Key("ed-shift-fwd"), nil, "+50ms", p.surface2, p.text).Grow(1).Basis(0).Justify(ui.Center).Clicked() {
				a.shiftAllLyrics(0.05)
			}
		})

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
}

func (a *app) viewEditorTimeline(c *ui.Context) {
	p := a.pal
	// 3) Bottom multi-track waveform & interactive character timeline (228px)
	tl := ui.Column(c.Key("ed-timeline")).FillWidth().Height(228).Shrink(0)
	tl.Draw(func(pt *ui.Painter, r ui.Rect) {
		pt.Fill(ui.Rect{X: r.X, Y: r.Y, W: r.W, H: 1}, p.hairline, 0)
	})
	tl.Children(func() {
		ui.Row(c).FillWidth().Height(32).PaddingX(14).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "联动后续字").FontSize(12).TextColor(p.muted)
			settingSwitch(c.Key("tl-rip"), &a.edRipple, "联动后续字")
			ui.Box(c).Grow(1)
			ui.Text(c, "点击或左右拖动下方字块实时调整起止时间 · 拖动上方波形移动播放头").MinWidth(0).FontSize(11.5).TextColor(p.muted).SingleLine()
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
				trackW := max(120.0, float64(trArea.LayoutBounds().W-24))
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
						dt := float64(dx) * (lineSpan / trackW)
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
								disp := strings.TrimSpace(ch)
								if disp == "" {
									disp = "·"
								}
								ui.Text(c, disp).FontSize(13).FontWeight(700).TextColor(fg).SingleLine()
							})
						})
						// Dragging the block adjusts its duration (and neighbor boundary) in real time
						if dx, _, ok := b.Dragged(); ok && dx != 0 {
							a.edSelectedChar = idx
							dt := float64(dx) * (lineSpan / trackW)
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
								dt := float64(dx) * (lineSpan / trackW)
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
						dt := float64(dx) * (lineSpan / trackW)
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
}

func (a *app) viewEditorMore(c *ui.Context) {
	p := a.pal
	more := popoverButton(c.Key("ed-more-btn"), p, nil, "更多", a.edMenuOpen, false)
	if more.Clicked() {
		a.edMenuOpen = !a.edMenuOpen
	}
	anchoredPopover(c.Key("ed-more-popover"), p, more, &a.edMenuOpen, 188, false, func() {
		ui.Column(c).Gap(2).FillWidth().Children(func() {
			item := func(key, label string, disabled bool, action func()) {
				fg := p.text
				if disabled {
					fg = p.faint
				}
				if popoverItem(c.Key(key), p, label, fg).Disabled(disabled).Clicked() {
					a.edMenuOpen = false
					action()
				}
			}
			hasPath := a.edPath != ""
			item("ed-open", "打开 LRC", false, a.pickLrcFile)
			item("ed-refresh", "从磁盘重新加载", !hasPath, a.refreshEditor)
			item("ed-valid", "校验当前歌词", !hasPath, a.validateCurrentLrc)
			item("ed-heart", "双语译文加 ❤️", !hasPath, a.heartsCurrentLrc)
			if a.edModified && hasPath {
				ui.Box(c).FillWidth().Height(1).Background(p.hairline)
				if popoverItem(c.Key("ed-revert"), p, "放弃未保存修改", p.err).Clicked() {
					a.edMenuOpen = false
					a.edEditingLine = -1
					a.cancelEditLrcSource()
					a.refreshEditor()
				}
			}
		})
	})
}

func (a *app) editorShortcuts(c *ui.Context) {
	if a.page != pageEditor || a.settingsOpen || a.menu != nil || a.edMenuOpen || a.edEditingLine >= 0 {
		return
	}
	// Window shortcuts let focused buttons and text fields handle their own keys first.
	if !a.edSrcEditing && c.Shortcut(0, ui.KeySpace) {
		a.togglePlayback()
	}
	if a.edPath != "" && c.Shortcut(ui.Ctrl, ui.KeyS) {
		if a.edSrcEditing {
			a.saveEditLrcSource()
		} else {
			a.saveLrcFile()
		}
	}
}

// Recheck the validator's character-based length and tail warnings using the
// English word blocks displayed in the editor.
func editorValidationIssues(issues []string, lines []lyricLine) []string {
	const maxWords = 30
	englishLengths := make(map[string]string)
	englishLines := make(map[string]lyricLine)
	for _, line := range lines {
		if editorEnglishWordCount(line.Text) > 0 {
			englishLines["["+fmtSec(line.Start)+"] "+line.Text] = line
		}
		for _, text := range []string{line.Text, line.Translation} {
			chars := []rune(text)
			if len(chars) <= maxWords {
				continue
			}
			words := editorEnglishWordCount(text)
			if words == 0 {
				continue
			}
			original := fmt.Sprintf("过长 %d 字: %s", len(chars), strings.TrimSpace(string(chars[:20])))
			englishLengths[original] = ""
			if words > maxWords {
				fields := strings.Fields(text)
				excerpt := strings.Join(fields[:min(len(fields), 8)], " ")
				englishLengths[original] = fmt.Sprintf("过长 %d 个单词: %s", words, excerpt)
			}
		}
	}
	filtered := make([]string, 0, len(issues))
	for _, issue := range issues {
		if replacement, ok := englishLengths[issue]; ok {
			issue = replacement
		} else if strings.HasPrefix(issue, "[人声] 第 ") {
			issue = editorEnglishVocalIssue(issue, englishLines)
		}
		if issue != "" {
			filtered = append(filtered, issue)
		}
	}
	return filtered
}

func editorEnglishVocalIssue(issue string, lines map[string]lyricLine) string {
	const cramped = "尾字挤压，末字时间内仍有人声"
	split := strings.LastIndex(issue, "：")
	if split < 0 || !strings.Contains(issue[split:], cramped) {
		return issue
	}
	header := issue[:split]
	_, lineKey, _ := strings.Cut(header, " 行 ")
	line, ok := lines[strings.TrimSpace(lineKey)]
	if !ok {
		return issue
	}
	var starts []float64
	for _, block := range line.Chars {
		if startsWithEditorWord(block.Ch) {
			starts = append(starts, block.Start)
		}
	}
	closeStarts := 0
	if len(starts) >= 4 {
		for i := len(starts) - 3; i < len(starts); i++ {
			if gap := starts[i] - starts[i-1]; gap >= 0 && gap <= 0.065 {
				closeStarts++
			}
		}
	}
	reasons := strings.Split(strings.TrimSpace(issue[split+len("："):]), "、")
	for i, reason := range reasons {
		if reason == cramped {
			if closeStarts >= 2 {
				reasons[i] = "尾词挤压，末词时间内仍有人声"
			} else {
				reasons = append(reasons[:i], reasons[i+1:]...)
			}
			break
		}
	}
	if len(reasons) == 0 {
		return ""
	}
	return header + "：" + strings.Join(reasons, "、")
}

func editorEnglishWordCount(text string) int {
	latin := false
	for _, r := range text {
		if unicode.IsLetter(r) {
			if !unicode.Is(unicode.Latin, r) {
				return 0
			}
			latin = true
		}
	}
	if !latin {
		return 0
	}
	words := 0
	for _, part := range lyricBlockTexts(text) {
		if startsWithEditorWord(part) {
			words++
		}
	}
	return words
}

func startsWithEditorWord(text string) bool {
	r, _ := utf8.DecodeRuneInString(strings.TrimSpace(text))
	return unicode.Is(unicode.Latin, r) || ('0' <= r && r <= '9')
}
