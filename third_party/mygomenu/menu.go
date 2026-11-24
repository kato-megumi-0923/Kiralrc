// Package kiralrcmenu adapts the pinned MyGo window's popup hook so text
// editing commands keep their original selection, undo history and callbacks.
package kiralrcmenu

import (
	"github.com/egoist/mygo/internal/platform"
	"github.com/egoist/mygo/internal/surface"
)

type Item struct {
	Label               string
	Disabled, Separator bool
	Choose              func()
}

type Content struct {
	View surface.Content
	Open func(x, y float32, items []Item)
}

func (v Content) AttachContent(conn *surface.Conn) {
	native := conn.PopupMenu
	conn.PopupMenu = func(menu *platform.Menu, x, y float64, chosen func(int)) {
		var items []Item
		for _, item := range menu.Items {
			if !item.Visible {
				continue
			}
			switch item.Type {
			case platform.MenuItemNormal:
				// MyGo consumes the choice on the next view pass. Deliver it after
				// the overlay click frame ends, just as a native menu does.
				items = append(items, Item{Label: item.Label, Disabled: !item.Enabled, Choose: func() {
					conn.Post(func() { chosen(item.ID) })
				}})
			case platform.MenuItemSeparator:
				items = append(items, Item{Separator: true})
			default:
				native(menu, x, y, chosen)
				return
			}
		}
		conn.Post(func() {
			v.Open(float32(x), float32(y), items)
			conn.Invalidate()
		})
	}
	v.View.AttachContent(conn)
}
