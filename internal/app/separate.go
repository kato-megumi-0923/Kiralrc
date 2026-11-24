package app

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// 人声分离：曲库外的临时产物 + 一个专门的进度页。
//
// 分离结果写在 work/sep/<歌名>_<哈希>/，从不落到曲库目录；
// 关掉 app 就清空，开着最多留三小时，设置里也能手动清。

type sepStem struct {
	Name   string // vocals / drums / bass / other
	Label  string
	Path   string
	Size   int64
	First  float64 // 第一次出声的位置（秒）
	Dur    float64 // 总长（秒）
	Silent bool    // 整轨没声音（此时 First 无意义）
}

var sepLabels = map[string]string{
	"vocals":        "人声",
	"accompaniment": "伴奏",
	"drums":         "鼓",
	"bass":          "贝斯",
	"other":         "其他",
}

func purgeSepCache() int {
	root := filepath.Join(appRootDir(), "work", "sep")
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			if os.RemoveAll(filepath.Join(root, e.Name())) == nil {
				n++
			}
		}
	}
	return n
}

// purgeOldSepCache 只清超过三小时没动过的。
func purgeOldSepCache() int {
	root := filepath.Join(appRootDir(), "work", "sep")
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil || time.Since(info.ModTime()) < 3*time.Hour {
			continue
		}
		if os.RemoveAll(filepath.Join(root, e.Name())) == nil {
			n++
		}
	}
	return n
}

// revealSepCache 在资源管理器里打开分离缓存，省得人去找路径。
func (a *app) revealSepCache() {
	dir := filepath.Join(appRootDir(), "work", "sep")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		a.toast = "打不开目录：" + err.Error()
		return
	}
	if err := exec.Command("explorer.exe", dir).Start(); err != nil {
		a.toast = "打不开目录：" + err.Error()
	}
}

// revealInExplorer 在 Windows 资源管理器中打开文件所在目录并选中该文件。
func (a *app) revealInExplorer(targetPath string) {
	p := strings.TrimSpace(targetPath)
	if p == "" {
		return
	}
	p = filepath.Clean(p)
	if _, err := os.Stat(p); err == nil {
		if err := newExplorerSelectCmd(p).Start(); err != nil {
			a.toast = "打不开目录：" + err.Error()
		}
		return
	}
	if err := exec.Command("explorer.exe", filepath.Dir(p)).Start(); err != nil {
		a.toast = "打不开目录：" + err.Error()
	}
}

func (a *app) clearSepCache() {
	purgeSepCache()
	a.sepStems = nil
	a.sepAudio = ""
	a.toast = "已清除分离缓存"
}

// startSeparate 选好歌就跳到分离页，边跑边更新进度。
func (a *app) startSeparate(audioPath string) {
	if a.sepRunning {
		a.toast = "正在分离中，请稍候"
		return
	}
	purgeOldSepCache()
	a.sepAudio = audioPath
	a.sepStems = nil
	a.sepRunning = true
	a.sepStage = "准备中"
	a.sepPct = 0
	a.navigate(pageSeparate, 0)

	go func() {
		cmd := a.pythonCmd("separate", audioPath, "--json")
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			a.finishSeparate(err.Error(), nil)
			return
		}
		cmd.Stderr = nil // HF 的警告别混进 JSON 里
		if err := cmd.Start(); err != nil {
			a.finishSeparate(err.Error(), nil)
			return
		}
		var stems []sepStem
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if !strings.HasPrefix(line, "{") {
				continue
			}
			var msg struct {
				Stage    string   `json:"stage"`
				Pct      int      `json:"pct"`
				Stem     string   `json:"stem"`
				Path     string   `json:"path"`
				Size     int64    `json:"size"`
				First    *float64 `json:"first"`
				Duration float64  `json:"duration"`
			}
			if json.Unmarshal([]byte(line), &msg) != nil {
				continue
			}
			if msg.Stem != "" {
				label := sepLabels[msg.Stem]
				if label == "" {
					label = msg.Stem
				}
				one := sepStem{Name: msg.Stem, Label: label, Path: msg.Path,
					Size: msg.Size, Dur: msg.Duration, Silent: msg.First == nil}
				if msg.First != nil {
					one.First = *msg.First
				}
				stems = append(stems, one)
				found := append([]sepStem{}, stems...)
				a.post(func() { a.sepStems = found })
				continue
			}
			stage, pct := msg.Stage, msg.Pct
			if stage != "" {
				a.post(func() {
					a.sepStage = stageName(stage)
					a.sepPct = pct
				})
			}
		}
		err = cmd.Wait()
		if err != nil && len(stems) == 0 {
			a.finishSeparate("分离失败", stems)
			return
		}
		a.finishSeparate("", stems)
	}()
}

func stageName(stage string) string {
	switch stage {
	case "decode":
		return "读取音频"
	case "model":
		return "加载模型"
	case "separate":
		return "分离中"
	case "write":
		return "写出音轨"
	}
	return stage
}

func (a *app) finishSeparate(errMsg string, stems []sepStem) {
	a.post(func() {
		a.sepRunning = false
		if errMsg != "" {
			a.sepStage = errMsg
			a.toast = errMsg
			return
		}
		a.sepStage = "完成"
		a.sepPct = 100
		if len(stems) > 0 {
			a.toast = fmt.Sprintf("分离完成，共 %d 条音轨", len(stems))
		}
	})
}

// exportStem 让用户选格式与位置，再把音轨转码写出去。
func (a *app) exportStem(stem sepStem) {
	go func() {
		target, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
			Parent:      a.win,
			Title:       "导出" + stem.Label,
			ButtonLabel: "导出",
			DefaultPath: stem.Label + ".flac",
			Filters: []mygo.FileFilter{
				{Name: "FLAC 无损", Extensions: []string{"flac"}},
				{Name: "WAV 无损", Extensions: []string{"wav"}},
				{Name: "MP3", Extensions: []string{"mp3"}},
			},
		})
		if err != nil || strings.TrimSpace(target) == "" {
			return
		}
		if filepath.Ext(target) == "" {
			target += ".flac"
		}
		cmd := a.pythonCmd("export-stem", stem.Path, target)
		cmd.Stderr = nil
		if err := cmd.Run(); err != nil {
			a.post(func() { a.toast = "导出失败：" + err.Error() })
			return
		}
		a.post(func() { a.toast = "已导出：" + filepath.Base(target) })
	}()
}

// clockOf 把秒数写成 m:ss，给「起声」这类标注用。
func clockOf(sec float64) string {
	if sec <= 0 {
		return "0:00"
	}
	total := int(sec + 0.5)
	return fmt.Sprintf("%d:%02d", total/60, total%60)
}

func sizeText(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// viewSeparate 分离页：进度 + 四条音轨 + 试听/导出。
func (a *app) viewSeparate(c *ui.Context) {
	p := a.pal
	title := filepath.Base(a.sepAudio)
	stem := strings.TrimSuffix(title, filepath.Ext(title))

	col := ui.Column(c).FillWidth().Padding(pageGap, 38, 108, 38).Gap(16)
	col.Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(12).Children(func() {
			if pillButton(c.Key("sep-back"), iconPrev, "返回工具箱", p.surface2, p.text).Clicked() {
				a.navigate(pageTools, 0)
			}
			ui.Column(c).Gap(2).Children(func() {
				ui.Text(c, "人声分离").FontSize(26).FontWeight(800)
				ui.Text(c, stem).FontSize(12.5).TextColor(p.muted).SingleLine()
			})
		})

		// 进度
		card := ui.Column(c.Key("sep-progress")).FillWidth().Padding(20, 20, 20, 20).Gap(10).Radius(16).Background(p.surface)
		card.Draw(func(pt *ui.Painter, r ui.Rect) { pt.Stroke(r, p.hairline, 16, 1) })
		card.Children(func() {
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				ui.Text(c, a.sepStage).FontSize(14).FontWeight(700).Grow(1)
				ui.Text(c, fmt.Sprintf("%d%%", a.sepPct)).FontSize(14).FontWeight(700).TextColor(p.accent)
			})
			bar := ui.Box(c).FillWidth().Height(6).Radius(999).Background(p.surface2)
			bar.Draw(func(pt *ui.Painter, r ui.Rect) {
				if a.sepPct <= 0 {
					return
				}
				w := r.W * float32(a.sepPct) / 100
				pt.Fill(ui.Rect{X: r.X, Y: r.Y, W: w, H: r.H}, p.accent, 999)
			})
			ui.Text(c, "音轨只留在分离缓存里，不进曲库；关掉程序即清空，开着最多留三小时。").
				FontSize(11.5).TextColor(p.muted)
		})

		// 结果
		if len(a.sepStems) == 0 {
			ui.Text(c, "分离完成后，这里会出现人声与伴奏两条音轨。").
				FontSize(12.5).TextColor(p.muted)
			return
		}
		ui.Column(c).Gap(8).Children(func() {
			for _, item := range a.sepStems {
				stemRow := item
				playing := a.sepPlaying == stemRow.Name
				row := ui.Column(c.Key("sep-"+stemRow.Name)).FillWidth().Padding(14, 20, 12, 20).
					Gap(9).Radius(14).Background(p.surface)
				row.Draw(func(pt *ui.Painter, r ui.Rect) { pt.Stroke(r, p.hairline, 14, 1) })
				row.Children(func() {
					ui.Row(c).AlignItems(ui.Center).Gap(14).Children(func() {
						ui.Text(c, stemRow.Label).Width(64).Shrink(0).FontSize(14).FontWeight(700)
						ui.Text(c, sizeText(stemRow.Size)).Width(90).Shrink(0).FontSize(12.5).TextColor(p.muted)
						ui.Text(c, filepath.Base(stemRow.Path)).Grow(1).MinWidth(0).FontSize(12).
							TextColor(p.faint).SingleLine()
						playLabel, playIcon := "试听", iconPlay
						if playing && !a.sepPaused {
							playLabel, playIcon = "暂停", iconPause
						} else if playing && a.sepPaused {
							playLabel, playIcon = "继续", iconPlay
						}
						if pillButton(c.Key("sep-play-"+stemRow.Name), playIcon, playLabel, p.surface2, p.text).Clicked() {
							a.previewStem(stemRow)
						}
						if pillButton(c.Key("sep-export-"+stemRow.Name), iconDownload, "导出", p.accent, p.onAccent).Clicked() {
							a.exportStem(stemRow)
						}
					})
					// 进度条：可点可拖；竖线标出第一次出声的位置
					ui.Row(c).AlignItems(ui.Center).Gap(10).Children(func() {
						track := ui.Box(c).Grow(1).MinWidth(0).Height(14)
						track.Draw(func(pt *ui.Painter, r ui.Rect) {
							if stemRow.Dur <= 0 {
								return
							}
							line := ui.Rect{X: r.X, Y: r.Y + (r.H-4)/2, W: r.W, H: 4}
							pt.Fill(line, p.surface2, 999)
							if playing {
								pos, _ := a.player.Position()
								if pos > 0 {
									w := line.W * float32(pos/stemRow.Dur)
									if w > line.W {
										w = line.W
									}
									pt.Fill(ui.Rect{X: line.X, Y: line.Y, W: w, H: line.H}, p.accent, 999)
								}
							}
							if !stemRow.Silent && stemRow.First < stemRow.Dur {
								x := line.X + line.W*float32(stemRow.First/stemRow.Dur)
								pt.Fill(ui.Rect{X: x - 1, Y: line.Y - 4, W: 2, H: line.H + 8}, p.warn, 1)
							}
						})
						if x, _, over := track.PointerPosition(); over && track.Pressed() {
							if w := track.Bounds().W; w > 0 {
								a.seekStem(stemRow, float64(x/w))
							}
						}
						label := "起声 " + clockOf(stemRow.First)
						if stemRow.Silent {
							label = "全程无声"
						}
						jump := pillButton(c.Key("sep-first-"+stemRow.Name), nil, label, p.surface2, p.muted)
						if !stemRow.Silent && jump.Clicked() {
							// 略微提前一点，能听见第一个音起头
							at := stemRow.First - 0.3
							if at < 0 {
								at = 0
							}
							a.seekStem(stemRow, at/stemRow.Dur)
						}
					})
				})
			}
		})
	})
}

// seekStem 按比例跳转：没加载就先加载，跳过去直接播。
func (a *app) seekStem(stem sepStem, ratio float64) {
	if a.player == nil || a.win == nil || stem.Dur <= 0 {
		return
	}
	if ratio < 0 {
		ratio = 0
	} else if ratio > 1 {
		ratio = 1
	}
	sec := ratio * stem.Dur
	if a.sepPlaying != stem.Name {
		a.sepPlaying = stem.Name
		a.sepPaused = false
		name := stem.Name
		go func() {
			if _, err := a.player.Load(stem.Path); err != nil {
				a.post(func() {
					a.sepPlaying = ""
					a.toast = "加载失败：" + err.Error()
				})
				return
			}
			_ = a.player.Seek(sec)
			_ = a.player.Play()
			a.pollStem(name)
		}()
		return
	}
	_ = a.player.Seek(sec)
	if a.sepPaused {
		a.sepPaused = false
		_ = a.player.Play()
		a.pollStem(stem.Name)
	}
	a.win.Invalidate()
}

// previewStem 试听：同一行再点一次暂停，再点一次继续。
func (a *app) previewStem(stem sepStem) {
	if a.player == nil || a.win == nil {
		return
	}
	if a.sepPlaying == stem.Name {
		if a.sepPaused {
			a.sepPaused = false
			_ = a.player.Play()
			a.pollStem(stem.Name)
		} else {
			a.sepPaused = true
			_ = a.player.Pause()
		}
		a.win.Invalidate()
		return
	}
	name := stem.Name
	go func() {
		if _, err := a.player.Load(stem.Path); err != nil {
			a.post(func() { a.toast = "试听失败：" + err.Error() })
			return
		}
		_ = a.player.Play()
		a.post(func() {
			a.sepPlaying = name
			a.sepPaused = false
		})
		a.pollStem(name)
	}()
}

// pollStem 播放期间每 120ms 重绘一次进度条，停了就收尾。
func (a *app) pollStem(name string) {
	go func() {
		last := -1.0
		for a.player.Playing() {
			pos, _ := a.player.Position()
			if pos != last {
				last = pos
				a.post(func() { a.win.Invalidate() })
			}
			time.Sleep(120 * time.Millisecond)
		}
		a.post(func() {
			if a.sepPlaying == name && !a.sepPaused {
				a.sepPlaying = ""
			}
			a.win.Invalidate()
		})
	}()
}
