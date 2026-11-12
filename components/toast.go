package components

import (
	"github.com/egoist/mygo/ui"
)

// ToastStack renders the app's bottom-right notification stack inside
// ui's toast viewport (IDEA-style bubbles). The caller feeds ui.Toast
// values in; per-kind icon and color are themed here.
func ToastStack(c *ui.Context, t *ui.Theme) {
	ui.ToastViewportBase(c, func(viewport ui.Element, toasts []ui.Toast) {
		viewport.Padding(16).AlignItems(ui.End).Gap(8)
		for _, ts := range toasts {
			tp := ui.ToastBase(c, ts)
			color := t.TextMuted
			icon := "ℹ"
			switch ts.Type {
			case "success":
				color = t.Success
				icon = "✓"
			case "error":
				color = t.Danger
				icon = "✕"
			}
			tp.Root.Row().Gap(10).Padding(10, 14).Radius(8).Width(340).
				Background(t.Inverse).Shadow(0, 4, 16, 0, ui.RGBA(0, 0, 0, 0.4))
			tp.Root.Children(func() {
				ui.Text(c, icon).TextColor(color)
				ui.Text(c, ts.Title).Grow(1)
				tp.CloseButton().Label("关闭").Children(func() {
					ui.Text(c, "×").TextColor(t.TextMuted)
				})
			})
		}
	})
}
