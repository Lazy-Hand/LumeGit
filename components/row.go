package components

import (
	"github.com/egoist/mygo/ui"

	"lumegit/internal/git"
)

// RowBg: selected rows get the selection tint.
func RowBg(t *ui.Theme, sel bool) ui.Color {
	if sel {
		return t.Selection
	}
	return ui.Transparent
}

// RowLabelColor dims unselected row text into the theme's plain color.
func RowLabelColor(t *ui.Theme, sel bool) ui.Color {
	if sel {
		return t.Accent
	}
	return t.Text
}

// StatusColor picks a row's status color (for non-tag text uses).
func StatusColor(t *ui.Theme, k git.FileKind) ui.Color {
	switch k {
	case git.KindUnmerged, git.KindDeleted:
		return t.Danger
	case git.KindUntracked, git.KindAdded:
		return t.Success
	default:
		return t.Text
	}
}

// Placeholder centers helper text in a pane.
func Placeholder(c *ui.Context, t *ui.Theme, text string) {
	ui.Column(c).Fill().Center().Children(func() {
		ui.Text(c, text).TextColor(t.TextMuted)
	})
}

var graphColors = []ui.Color{
	ui.RGB(0x5b, 0x8d, 0xef), ui.RGB(0x53, 0xc1, 0x8d), ui.RGB(0xe0, 0x9f, 0x3e),
	ui.RGB(0xcf, 0x67, 0xcc), ui.RGB(0xef, 0x6b, 0x6b), ui.RGB(0x3f, 0xa6, 0xc3),
}

// GraphCell draws the 24-DIP lane column for one history row: vertical
// lines, the commit node, and any merge/fork connectors.
func GraphCell(c *ui.Context, gl *git.GraphLine) ui.Element {
	return ui.Row(c).Width(24).Fill().Draw(func(p *ui.Painter, r ui.Rect) {
		if gl == nil {
			return
		}
		laneX := func(l int) float32 { return r.X + 6 + float32(l)*8 }
		cy := r.Y + r.H/2
		// Vertical pass-throughs.
		for l, v := range gl.Lanes {
			if v == 0 || l == gl.Lane {
				continue
			}
			col := graphColors[l%len(graphColors)]
			path := &ui.Path{}
			path.MoveTo(laneX(l), r.Y).LineTo(laneX(l), r.Y+r.H)
			p.StrokePath(path, 1.5, col)
		}
		// Edge connectors to parents on other lanes (merge/fork).
		for _, e := range gl.Edges {
			x0, x1 := laneX(e[0]), laneX(e[1])
			col := graphColors[e[1]%len(graphColors)]
			path := &ui.Path{}
			path.MoveTo(x0, cy).
				CubeTo(x0, cy+8, x1, cy-8, x1, r.Y+r.H) // down-right/left curve
			p.StrokePath(path, 1.5, col)
		}
		// Lanes that were also waiting for this commit converge into
		// the node from the top of the row and end here.
		for _, j := range gl.Joins {
			x0, x1 := laneX(j), laneX(gl.Lane)
			col := graphColors[j%len(graphColors)]
			path := &ui.Path{}
			path.MoveTo(x0, r.Y).
				CubeTo(x0, cy-8, x1, cy+8, x1, cy)
			p.StrokePath(path, 1.5, col)
		}
		// Commit node.
		col := graphColors[0]
		if gl.ColorIdx >= 0 {
			col = graphColors[gl.ColorIdx%len(graphColors)]
		}
		node := &ui.Path{}
		node.Circle(laneX(gl.Lane), cy, 3.5)
		p.FillPath(node, col)
		// Line into/out of the node (the lane's own vertical).
		path := &ui.Path{}
		path.MoveTo(laneX(gl.Lane), r.Y).LineTo(laneX(gl.Lane), cy-3.5)
		p.StrokePath(path, 1.5, col)
		path2 := &ui.Path{}
		path2.MoveTo(laneX(gl.Lane), cy+3.5).LineTo(laneX(gl.Lane), r.Y+r.H)
		p.StrokePath(path2, 1.5, col)
	})
}

// BlameColor picks a stable per-author tint for blame rows from the
// commit oid so adjacent same-author lines read as one group.
func BlameColor(t *ui.Theme, oid string) ui.Color {
	if oid == "" {
		return ui.Transparent
	}
	var h uint32
	for i := 0; i < len(oid) && i < 8; i++ {
		h = h*31 + uint32(oid[i])
	}
	palette := []ui.Color{
		t.Selection,
		t.Accent.Alpha(0.08),
		t.Success.Alpha(0.08),
		t.Warning.Alpha(0.08),
	}
	return palette[h%uint32(len(palette))]
}
