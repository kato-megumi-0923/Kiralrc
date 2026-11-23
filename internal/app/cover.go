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
	"time"

	"github.com/egoist/mygo/ui"
	"golang.org/x/image/draw"
)

type coverIndex struct {
	byDir map[string]string // work/<歌名>_<路径哈希> → 对齐时抽出的 cover.jpg
}

// indexCovers 只认「路径哈希对得上」的缓存目录。
//
// 早先还有一个「同名兜底」：哈希对不上时，若全盘只有一个同名目录就用它。
// 但 work/ 里躺着别的曲库、别的路径留下的旧目录，同名兜底会把不相干的封面
// 贴到这首歌上（实测库里 51 首命中，且都不是自己那次对齐的产物）。
// 收紧之后：对不上就当没有，交给内嵌封面或占位图——宁可没图，不贴错图。
func indexCovers() coverIndex {
	return indexCoversIn(filepath.Join(appRootDir(), "work"))
}

func indexCoversIn(root string) coverIndex {
	index := coverIndex{make(map[string]string)}
	entries, _ := os.ReadDir(root)
	for _, entry := range entries {
		// Track names may contain underscores or periods. The suffix is always the last ten hex digits.
		sep := strings.LastIndexByte(entry.Name(), '_')
		if !entry.IsDir() || sep < 0 || len(entry.Name())-sep-1 != 10 {
			continue
		}
		path := filepath.Join(root, entry.Name(), "cover.jpg")
		if info, err := os.Stat(path); err == nil && info.Size() > 512 {
			index.byDir[entry.Name()] = path
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

// thumbnail 同步取一张缩略图（少见的同步场景用；界面上走 lookupCover）。
func (index coverIndex) thumbnail(audioPath string) *ui.Bitmap {
	if audioPath == "" {
		return nil
	}
	key := coverCacheKey(audioPath)
	if bmp, known := coverMem.get(key); known {
		return bmp
	}
	bmp := ui.NewBitmap(index.thumbImage(audioPath))
	coverMem.put(key, bmp)
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

// coverCacheKey 把「路径 + 修改时间」压成一个键：换了封面或重解过音频就自然失效。
//
// 渲染每帧都要给上百个路径取键，而取键要 os.Stat；这里记住结果，
// 重扫曲库后 resetCoverKeys() 让它们重新算（文件可能被换过）。
func coverCacheKey(audioPath string) string {
	coverKeyMu.Lock()
	key, ok := coverKeyMemo[audioPath]
	coverKeyMu.Unlock()
	if ok {
		return key
	}
	mt := int64(0)
	if info, err := os.Stat(audioPath); err == nil {
		mt = info.ModTime().UnixNano()
	}
	key = fmt.Sprintf("%s|%d", audioPath, mt)
	coverKeyMu.Lock()
	coverKeyMemo[audioPath] = key
	coverKeyMu.Unlock()
	return key
}

// resetCoverKeys 丢掉路径→键的记忆（重扫结束后调一次）。
func resetCoverKeys() {
	coverKeyMu.Lock()
	coverKeyMemo = map[string]string{}
	coverKeyMu.Unlock()
}

// coverStore 是容量固定的封面 LRU。
//
// 曲库翻几百页时，永久缓存会把位图全囤在内存里（128px 约 65KB 一张）；
// 这里限 400 张（约 26MB），淘汰最久没用的。
// 同时记「确认没有封面」的键，免得同一首在每次重绘时反复去开音频文件。
type coverStore struct {
	mu    sync.Mutex
	limit int
	order []string // 最旧 → 最新
	items map[string]*ui.Bitmap
	miss  map[string]bool
}

func newCoverStore(limit int) *coverStore {
	return &coverStore{limit: limit, items: map[string]*ui.Bitmap{}, miss: map[string]bool{}}
}

// get 返回位图与「是否已知」：已知但无封面时返回 nil, true。
func (s *coverStore) get(key string) (*ui.Bitmap, bool) {
	if key == "" {
		return nil, true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if bmp, ok := s.items[key]; ok {
		s.moveToEnd(key)
		return bmp, true
	}
	if s.miss[key] {
		return nil, true
	}
	return nil, false
}

// put 写入位图；bmp 为 nil 表示这首确实没有封面，记负结果。
func (s *coverStore) put(key string, bmp *ui.Bitmap) {
	if key == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if bmp == nil {
		s.miss[key] = true
		return
	}
	delete(s.miss, key)
	if _, ok := s.items[key]; ok {
		s.items[key] = bmp
		s.moveToEnd(key)
		return
	}
	s.items[key] = bmp
	s.order = append(s.order, key)
	for len(s.order) > s.limit {
		oldest := s.order[0]
		s.order = s.order[1:]
		delete(s.items, oldest)
	}
}

// moveToEnd 把键挪到「最新用过」的位置（调用方需持锁）。
func (s *coverStore) moveToEnd(key string) {
	for i, k := range s.order {
		if k == key {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	s.order = append(s.order, key)
}

func (s *coverStore) size() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.items)
}

func (s *coverStore) isMiss(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.miss[key]
}

var coverMem = newCoverStore(400)

var (
	coverKeyMu   sync.Mutex
	coverKeyMemo = map[string]string{}
)

// ---------- 按需加载 ----------

// work/ 下的对齐封面目录进程内只扫一次。
var (
	coverIndexOnce sync.Once
	coverIndexVal  coverIndex
)

func coverIndexCached() coverIndex {
	coverIndexOnce.Do(func() { coverIndexVal = indexCovers() })
	return coverIndexVal
}

// lookupCover 命中返回位图与 true；已确认没有封面返回 nil, true；没加载过返回 nil, false。
func (a *app) lookupCover(audioPath string) (*ui.Bitmap, bool) {
	return coverMem.get(coverCacheKey(audioPath))
}

// requestCovers 把还没加载的路径丢给后台解（4 个 worker），解完合并成一次重绘。
// 同一路径并发调用只会解一次：inFlight 去重。
func (a *app) requestCovers(paths []string) {
	for _, p := range paths {
		if p == "" {
			continue
		}
		key := coverCacheKey(p)
		if _, known := coverMem.get(key); known {
			continue
		}
		a.coverMu.Lock()
		if a.coverInFlight == nil {
			a.coverInFlight = map[string]bool{}
			a.coverSem = make(chan struct{}, 4)
		}
		if a.coverInFlight[key] {
			a.coverMu.Unlock()
			continue
		}
		a.coverInFlight[key] = true
		sem := a.coverSem
		a.coverMu.Unlock()

		go func(path, key string) {
			sem <- struct{}{}
			img := coverIndexCached().thumbImage(path)
			<-sem
			a.post(func() {
				if img == nil {
					coverMem.put(key, nil) // 负结果也记下，不重复开文件
				} else {
					coverMem.put(key, ui.NewBitmap(img)) // 位图在 UI 线程建
				}
				a.coverMu.Lock()
				delete(a.coverInFlight, key)
				a.coverMu.Unlock()
				a.coverRepaint()
			})
		}(p, key)
	}
}

// coverRepaint 把 50ms 内解完的封面合并成一次重绘：
// 一页 50 张若各自重绘一次，整页要画 50 遍。
func (a *app) coverRepaint() {
	a.coverMu.Lock()
	if a.coverPending {
		a.coverMu.Unlock()
		return
	}
	a.coverPending = true
	a.coverMu.Unlock()
	go func() {
		time.Sleep(50 * time.Millisecond)
		a.coverMu.Lock()
		a.coverPending = false
		a.coverMu.Unlock()
		a.post(func() {}) // post 自带 Invalidate
	}()
}
