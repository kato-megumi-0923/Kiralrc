// Package menutest drives pointer events through the same window popup hook as the GUI.
package menutest

import (
	"testing"

	"github.com/egoist/mygo/internal/fake"
	"github.com/egoist/mygo/internal/platform"
	"github.com/egoist/mygo/internal/surface"
	"github.com/egoist/mygo/kiralrcmenu"
)

// Choose right-clicks target, clicks command in the overlay and reads the isolated clipboard.
func Choose(t *testing.T, content kiralrcmenu.Content, target, command string) string {
	t.Helper()
	backend := fake.New()
	w, err := backend.NewWindow(&platform.WindowOptions{Width: 1360, Height: 860, Surface: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := w.(*fake.Window).FakeSurface()
	var tasks []func()
	conn := &surface.Conn{Surface: s, Clipboard: backend.Clipboard(), Invalidate: s.RequestFrame,
		Post:        func(fn func()) { tasks = append(tasks, fn) },
		Preferences: func() platform.Preferences { return platform.Preferences{ReduceMotion: true} },
	}
	content.AttachContent(conn)
	t.Cleanup(conn.Detach)
	conn.Event(platform.SurfaceEvent{Kind: platform.AccessibilityOn})
	frame := func() {
		conn.Event(platform.SurfaceEvent{Kind: platform.SurfaceFrame})
		for len(tasks) > 0 {
			fn := tasks[0]
			tasks = tasks[1:]
			fn()
		}
	}
	click := func(label string, button int) {
		t.Helper()
		frame()
		tree, _ := s.Accessibility()
		for _, n := range tree.Nodes {
			if n.Label != label {
				continue
			}
			for _, kind := range []platform.SurfaceEventKind{platform.PointerDown, platform.PointerUp} {
				conn.Event(platform.SurfaceEvent{Kind: kind, X: n.Bounds.X + n.Bounds.W/2, Y: n.Bounds.Y + n.Bounds.H/2, Button: button})
				frame()
			}
			return
		}
		t.Fatalf("missing click target %q", label)
	}
	click(target, 1)
	click(command, 0)
	frame()
	return conn.Clipboard.ReadText()
}
