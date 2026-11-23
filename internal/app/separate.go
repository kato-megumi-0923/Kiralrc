package app

import (
	"bufio"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"os"
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
	Name  string // vocals / drums / bass / other
	Label string
	Path  string
	Size  int64
}

var sepLabels = map[string]string{
	"vocals": "人声",
	"drums":  "鼓",
	"bass":   "贝斯",
	"other":  "其他",
}

func sepCacheDir(audio string) string {
	stem := []rune(strings.TrimSuffix(filepath.Base(audio), filepath.Ext(audio)))
	name := string(stem[:min(len(stem), 60)])
	name = strings.NewReplacer("<", "_", ">", "_", ":", "_", "\"", "_", "/", "_",
		"\\", "_", "|", "_", "?", "_", "*", "_").Replace(name)
	abs, err := filepath.Abs(audio)
	if err != nil {
		abs = audio
	}
	sum := md5.Sum([]byte(abs))
	return filepath.Join(appRootDir(), "work", "sep", fmt.Sprintf("%s_%x", name, sum[:4]))
}

// purgeSepCache 删掉整个分离缓存目录，返回删了多少个。
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
				Stage string `json:"stage"`
				Pct   int    `json:"pct"`
				Stem  string `json:"stem"`
				Path  string `json:"path"`
				Size  int64  `json:"size"`
			}
			if json.Unmarshal([]byte(line), &msg) != nil {
				continue
			}
			if msg.Stem != "" {
				label := sepLabels[msg.Stem]
				if label == "" {
					label = msg.Stem
				}
				stems = append(stems, sepStem{Name: msg.Stem, Label: label, Path: msg.Path, Size: msg.Size})
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
			ui.Text(c, "音轨只写到 work/sep，不会进曲库；关掉程序即清空，开着最多留三小时。").
				FontSize(11.5).TextColor(p.muted)
		})

		// 结果
		if len(a.sepStems) == 0 {
			ui.Text(c, "分离完成后，这里会出现人声、鼓、贝斯、其他四条音轨。").
				FontSize(12.5).TextColor(p.muted)
			return
		}
		ui.Column(c).Gap(8).Children(func() {
			for _, item := range a.sepStems {
				stemRow := item
				row := ui.Row(c.Key("sep-"+stemRow.Name)).FillWidth().Height(64).Padding(16, 0, 16, 0).
					Gap(14).Radius(14).AlignItems(ui.Center).Background(p.surface)
				row.Draw(func(pt *ui.Painter, r ui.Rect) { pt.Stroke(r, p.hairline, 14, 1) })
				row.Children(func() {
					ui.Text(c, stemRow.Label).Width(64).Shrink(0).FontSize(14).FontWeight(700)
					ui.Text(c, sizeText(stemRow.Size)).Width(90).Shrink(0).FontSize(12.5).TextColor(p.muted)
					ui.Text(c, filepath.Base(stemRow.Path)).Grow(1).MinWidth(0).FontSize(12).
						TextColor(p.faint).SingleLine()
					if pillButton(c.Key("sep-play-"+stemRow.Name), iconPlay, "试听", p.surface2, p.text).Clicked() {
						a.previewStem(stemRow)
					}
					if pillButton(c.Key("sep-export-"+stemRow.Name), iconDownload, "导出", p.accent, p.onAccent).Clicked() {
						a.exportStem(stemRow)
					}
				})
			}
		})
	})
}

// previewStem 试听一条音轨（直接喂给播放器，不做临时转码）。
func (a *app) previewStem(stem sepStem) {
	if a.player == nil || a.win == nil {
		return
	}
	go func() {
		if _, err := a.player.Load(stem.Path); err != nil {
			a.post(func() { a.toast = "试听失败：" + err.Error() })
			return
		}
		_ = a.player.Play()
	}()
}
