package components

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/display"
	"github.com/lobehub/lobe-mygo/pkg/layout"
	"github.com/lobehub/lobe-mygo/pkg/theme"

	"lumegit/internal/git"
)

// ConflictSide renders one read-only column of the three-way resolver
// (ours / base / theirs) — a titled, bordered, scrollable text box.
func ConflictSide(c *ui.Context, t *ui.Theme, title string, side []byte) {
	tk := theme.TokensOf(c)
	ui.Column(c).Grow(1).Basis(0).Gap(4).Children(func() {
		ui.Text(c, title).FontSize(11).Bold().TextColor(t.TextMuted)
		ui.Column(c).Border(1, tk.BorderSecondary).Radius(6).Padding(6).MaxHeight(180).Children(func() {
			layout.ScrollArea(c, layout.ScrollAreaProps{}, func() {
				ui.Text(c, string(side)).FontSize(11).SingleLine()
			}).Grow(1)
		})
	})
}

// CommitSideList renders one side of a compare ("only in A / only in
// B"): a labelled tag plus at most six one-line commit rows, with a
// "…还有 N 条" footer when it truncates.
func CommitSideList(c *ui.Context, t *ui.Theme, label string, list []git.Commit) {
	ui.Column(c).FillWidth().Gap(1).Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Padding(3, 0).Children(func() {
			ui.Text(c, label).FontSize(11).Bold()
			display.Tag(c, fmt.Sprintf("%d", len(list)), display.TagProps{
				Variant: display.TagBorderless, Color: display.TagInfo,
			})
		})
		if len(list) == 0 {
			ui.Text(c, "（无独有提交）").FontSize(11).TextColor(t.TextMuted).Padding(0, 0, 6, 0)
			return
		}
		show := list
		truncated := 0
		if len(show) > 6 {
			truncated = len(show) - 6
			show = show[:6]
		}
		for i := range show {
			cm := &show[i]
			ui.Row(c).Key("cmps-"+cm.OID).FillWidth().Gap(6).Padding(2, 0).Children(func() {
				ui.Text(c, cm.ShortOID).FontSize(11).TextColor(t.TextMuted).Width(58).SingleLine()
				ui.Text(c, cm.Subject).FontSize(11).SingleLine().Grow(1)
			})
		}
		if truncated > 0 {
			ui.Text(c, fmt.Sprintf("…还有 %d 条", truncated)).FontSize(11).TextColor(t.TextMuted).
				Padding(0, 0, 4, 0)
		}
	})
}
