package app

import (
	"crypto/md5"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
	"golang.org/x/image/draw"
)

type coverIndex struct {
	byDir  map[string]string
	byStem map[string][]string
}

func indexCovers() coverIndex {
	root := filepath.Join(appRootDir(), "work")
	index := coverIndex{make(map[string]string), make(map[string][]string)}
	entries, _ := os.ReadDir(root)
	for _, entry := range entries {
		// Track names may contain underscores or periods. The suffix is always the last ten hex digits.
		sep := strings.LastIndexByte(entry.Name(), '_')
		if !entry.IsDir() || sep < 0 || len(entry.Name())-sep-1 != 10 {
			continue
		}
		stem := entry.Name()[:sep]
		path := filepath.Join(root, entry.Name(), "cover.jpg")
		if info, err := os.Stat(path); err == nil && info.Size() > 512 {
			index.byDir[entry.Name()] = path
			index.byStem[stem] = append(index.byStem[stem], path)
		}
	}
	return index
}

func (index coverIndex) thumbnail(audioPath string) *ui.Bitmap {
	if audioPath == "" {
		return nil
	}
	resolved, err := filepath.EvalSymlinks(audioPath)
	if err != nil {
		return nil
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return nil
	}
	stem := []rune(strings.TrimSuffix(filepath.Base(audioPath), filepath.Ext(audioPath)))
	name := string(stem[:min(len(stem), 40)])
	hash := md5.Sum([]byte(resolved))
	path := index.byDir[fmt.Sprintf("%s_%x", name, hash[:5])]
	if path == "" && len(index.byStem[name]) == 1 {
		path = index.byStem[name][0]
	}
	info, err := os.Stat(path)
	audioInfo, audioErr := os.Stat(audioPath)
	if err != nil || audioErr != nil || audioInfo.ModTime().After(info.ModTime()) {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	imageData, _, err := image.Decode(file)
	if err != nil {
		return nil
	}
	bounds := imageData.Bounds()
	size := min(bounds.Dx(), bounds.Dy())
	if size <= 0 {
		return nil
	}
	x, y := bounds.Min.X+(bounds.Dx()-size)/2, bounds.Min.Y+(bounds.Dy()-size)/2
	thumbnail := image.NewRGBA(image.Rect(0, 0, 128, 128))
	draw.ApproxBiLinear.Scale(thumbnail, thumbnail.Bounds(), imageData, image.Rect(x, y, x+size, y+size), draw.Src, nil)
	return ui.NewBitmap(thumbnail)
}
