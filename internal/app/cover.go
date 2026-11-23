package app

import (
	"bytes"
	"crypto/md5"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"sync"

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

// thumbImage 取 128px 缩略图（纯 CPU，可在后台并发跑）。
func (index coverIndex) thumbImage(audioPath string) image.Image {
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
	// 1) 对齐时抽出来的封面缓存（比音频新才用）
	if path != "" {
		info, err := os.Stat(path)
		audioInfo, audioErr := os.Stat(audioPath)
		if err == nil && audioErr == nil && !audioInfo.ModTime().After(info.ModTime()) {
			if file, err := os.Open(path); err == nil {
				defer file.Close()
				if imageData, _, err := image.Decode(file); err == nil {
					return thumbnailImage(imageData)
				}
			}
		}
	}
	// 2) 上次解码过的小图：直接读，省下解 600KB 大图的时间
	if thumbPath := coverThumbPath(audioPath); thumbPath != "" {
		if file, err := os.Open(thumbPath); err == nil {
			imageData, _, err := image.Decode(file)
			file.Close()
			if err == nil {
				if thumb := thumbnailImage(imageData); thumb != nil {
					return thumb
				}
			}
		}
	}
	// 3) 都没有才去解音频里的内嵌封面，解完顺手存一份 JPEG
	data := readEmbeddedCover(audioPath)
	if data == nil {
		return nil
	}
	imageData, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	thumb := thumbnailImage(imageData)
	if thumb != nil {
		saveCoverThumb(audioPath, imageData)
	}
	return thumb
}

func (index coverIndex) thumbnail(audioPath string) *ui.Bitmap {
	if audioPath == "" {
		return nil
	}
	if cached := cachedCover(audioPath); cached != nil {
		return cached
	}
	img := index.thumbImage(audioPath)
	if img == nil {
		return nil
	}
	bmp := ui.NewBitmap(img)
	coverCache.Store(coverCacheKey(audioPath), bmp)
	return bmp
}

// coverThumbPath 缩略图缓存在 work/covers 下，键是「路径 + 修改时间」，
// 换过封面或重解过音频，键就变了，旧文件自然失效。
func coverThumbPath(audioPath string) string {
	abs, err := filepath.Abs(audioPath)
	if err != nil {
		return ""
	}
	info, err := os.Stat(audioPath)
	if err != nil {
		return ""
	}
	sum := md5.Sum([]byte(fmt.Sprintf("%s|%d", abs, info.ModTime().UnixNano())))
	return filepath.Join(appRootDir(), "work", "covers", fmt.Sprintf("%x.jpg", sum[:8]))
}

func saveCoverThumb(audioPath string, imageData image.Image) {
	path := coverThumbPath(audioPath)
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	thumb := thumbnailImage(imageData)
	if thumb == nil {
		return
	}
	tmp := path + ".tmp"
	file, err := os.Create(tmp)
	if err != nil {
		return
	}
	if err := jpeg.Encode(file, thumb, &jpeg.Options{Quality: 88}); err != nil {
		file.Close()
		os.Remove(tmp)
		return
	}
	file.Close()
	_ = os.Rename(tmp, path)
}

// thumbnailImage 居中裁成正方形再缩到 128。
func thumbnailImage(imageData image.Image) image.Image {
	bounds := imageData.Bounds()
	size := min(bounds.Dx(), bounds.Dy())
	if size <= 0 {
		return nil
	}
	x, y := bounds.Min.X+(bounds.Dx()-size)/2, bounds.Min.Y+(bounds.Dy()-size)/2
	thumbnail := image.NewRGBA(image.Rect(0, 0, 128, 128))
	draw.ApproxBiLinear.Scale(thumbnail, thumbnail.Bounds(), imageData, image.Rect(x, y, x+size, y+size), draw.Src, nil)
	return thumbnail
}

func thumbnailOf(imageData image.Image) *ui.Bitmap {
	thumb := thumbnailImage(imageData)
	if thumb == nil {
		return nil
	}
	return ui.NewBitmap(thumb)
}

var coverCache sync.Map // 路径+修改时间 → *ui.Bitmap

func coverCacheKey(audioPath string) string {
	mt := int64(0)
	if info, err := os.Stat(audioPath); err == nil {
		mt = info.ModTime().UnixNano()
	}
	return fmt.Sprintf("%s|%d", audioPath, mt)
}

func cachedCover(audioPath string) *ui.Bitmap {
	if v, ok := coverCache.Load(coverCacheKey(audioPath)); ok {
		return v.(*ui.Bitmap)
	}
	return nil
}
