package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
)

// traceRect 只在 KIRALRC_RECT_LOG 打开时工作：把控件在窗口坐标系里的
// 矩形写进 %TEMP%\kiralrc-rects.txt。调试点击位置时用它，比截图量像素可靠。
var (
	rectLogMu   sync.Mutex
	rectLogLast time.Time
	rectLogSeen = map[string]string{}   // 名字 → 最后一次的矩形
)

func (a *app) traceRect(name string, e ui.Element) {
	if os.Getenv("KIRALRC_RECT_LOG") == "" {
		return
	}
	r := e.Bounds()
	rectLogMu.Lock()
	rectLogSeen[name] = fmt.Sprintf("%s\t%.1f\t%.1f\t%.1f\t%.1f", name, r.X, r.Y, r.W, r.H)
	if time.Since(rectLogLast) > 200*time.Millisecond {
		rectLogLast = time.Now()
		lines := make([]string, 0, len(rectLogSeen))
		for _, v := range rectLogSeen {
			lines = append(lines, v)
		}
		path := filepath.Join(os.TempDir(), "kiralrc-rects.txt")
		_ = os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
	}
	rectLogMu.Unlock()
}

// tracePointer 记录指针相对某个控件的本地坐标与是否命中，用来标定
// 「窗口原点 + DPI 缩放」到底该怎么换算。
func (a *app) tracePointer(name string, e ui.Element) {
	if os.Getenv("KIRALRC_RECT_LOG") == "" {
		return
	}
	x, y, over := e.PointerPosition()
	line := fmt.Sprintf("%s\t%.1f\t%.1f\t%v", name, x, y, over)
	path := filepath.Join(os.TempDir(), "kiralrc-pointer.txt")
	_ = os.WriteFile(path, []byte(line), 0o644)
}
