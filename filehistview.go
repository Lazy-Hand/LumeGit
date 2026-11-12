package main

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/lobehub/lobe-mygo/pkg/action"
	"github.com/lobehub/lobe-mygo/pkg/feedback"
	"github.com/lobehub/lobe-mygo/pkg/layout"
	"github.com/lobehub/lobe-mygo/pkg/theme"

	"lumegit/components"
	"lumegit/internal/git"
)

// --- 文件历史/溯源 ---------------------------------------------------

// fileHistOverlay renders the file-history dialog: sub-tabs for the
// commit trail and the blame view.
func (a *app) fileHistOverlay(c *ui.Context, t *ui.Theme) {
	m := &a.fileHist
	feedback.Modal(c, &m.open, feedback.ModalProps{
		Title:    "文件历史 — " + m.path,
		Width:    900,
		NoFooter: true,
	}, func() {
		ui.Column(c).Gap(8).MinHeight(480).Children(func() {
			action.Segmented(c, &m.mode, []string{"历史", "溯源"}, action.SegmentedProps{Size: theme.Small})
			if m.mode == 1 {
				m.ensureBlame(m.repoPath, a.updater())
				a.blameView(c, t)
				return
			}
			a.fileHistListView(c, t)
		})
	})
	// Closing the overlay cancels the in-flight blame/history workers —
	// their results land on a model the next file inherits otherwise.
	if !m.open && m.lastOpen {
		m.gen++
	}
	m.lastOpen = m.open
}

// fileHistListView: commits touching the file + diff for the pick.
func (a *app) fileHistListView(c *ui.Context, t *ui.Theme) {
	m := &a.fileHist
	ui.Row(c).Fill().MinHeight(440).Children(func() {
		// Left: commit list.
		ui.Column(c).Width(320).Fill().Gap(0).Children(func() {
			switch {
			case m.loading:
				components.Placeholder(c, t, "正在加载历史…")
			case m.err != "":
				feedback.Alert(c, m.err, feedback.AlertProps{Type: feedback.Error, Variant: feedback.Soft})
			case len(m.commits) == 0:
				components.Placeholder(c, t, "无历史记录。")
			default:
				layout.ScrollArea(c, layout.ScrollAreaProps{}, func() {
					ui.Column(c).FillWidth().Children(func() {
						for i := range m.commits {
							a.fhCommitRow(c, t, &m.commits[i])
						}
					})
				}).Grow(1)
			}
		})
		// Right: diff of the file at that commit.
		ui.Column(c).Grow(1).Fill().Padding(0, 0, 0, 12).Children(func() {
			switch {
			case m.diffLoading:
				components.Placeholder(c, t, "正在加载差异…")
			case m.diffErr != "":
				feedback.Alert(c, m.diffErr, feedback.AlertProps{Type: feedback.Error, Variant: feedback.Soft})
			case m.diff == nil:
				components.Placeholder(c, t, "选择提交查看变更。")
			case m.diff.Binary:
				components.Placeholder(c, t, "二进制文件。")
			default:
				a.fhDiffView(c, t)
			}
		})
	})
}

// fhCommitRow: one commit in the file's trail.
func (a *app) fhCommitRow(c *ui.Context, t *ui.Theme, cm *git.Commit) {
	sel := a.fileHist.selectedID == cm.OID
	repoPath := a.commit.RepoPath
	oid := cm.OID
	ui.Row(c).Key("fh-"+oid).FillWidth().Padding(6, 10).Gap(6).AlignItems(ui.Center).
		Background(components.RowBg(t, sel)).
		OnClick(func() {
			a.fhDiffState = nil
			a.fileHist.selectCommit(repoPath, oid, a.updater())
		}).
		Children(func() {
			ui.Column(c).Grow(1).Gap(1).Children(func() {
				ui.Text(c, cm.Subject).SingleLine().Grow(1)
				ui.Row(c).Gap(6).Children(func() {
					ui.Text(c, cm.ShortOID).FontSize(11).TextColor(t.TextMuted)
					ui.Text(c, cm.Author).FontSize(11).TextColor(t.TextMuted)
					ui.Text(c, components.RelTime(cm.When)).FontSize(11).TextColor(t.TextMuted)
				})
			})
		})
}

// fhDiffState is a separate DiffState for the overlay (so it doesn't
// fight the main pane's).
// fhDiffView renders the overlay's diff with its own state.
func (a *app) fhDiffView(c *ui.Context, t *ui.Theme) {
	d := a.fileHist.diff
	if a.fhDiffState == nil {
		a.fhDiffState = newDiffState(d)
	}
	a.fhDiffState.Layout = editor.DiffUnified
	editor.DiffView(c, a.fhDiffState).Grow(1).FillWidth().MinHeight(400)
}

// blameView renders the line-by-line attribution.
func (a *app) blameView(c *ui.Context, t *ui.Theme) {
	m := &a.fileHist
	switch {
	case m.blameLoading:
		components.Placeholder(c, t, "正在加载溯源…")
	case m.blameErr != "":
		feedback.Alert(c, m.blameErr, feedback.AlertProps{Type: feedback.Error, Variant: feedback.Soft})
	case len(m.blame) == 0:
		components.Placeholder(c, t, "无溯源信息。")
	default:
		layout.ScrollArea(c, layout.ScrollAreaProps{}, func() {
			ui.Column(c).FillWidth().Children(func() {
				for i := range m.blame {
					a.blameRow(c, t, &m.blame[i])
				}
			})
		}).Grow(1)
	}
}

// blameRow: hash + author + line number + content. The gutter clicks
// through to that commit in the Log tab (IDEA: blame gutter → Show
// commit in Log).
func (a *app) blameRow(c *ui.Context, t *ui.Theme, bl *git.BlameLine) {
	ui.Row(c).Key(fmt.Sprintf("bl-%d", bl.LineNo)).FillWidth().Gap(0).AlignItems(ui.Start).
		Children(func() {
			// Gutter: hash + author, fixed-width, colored by commit.
			ui.Row(c).Width(180).Gap(6).Padding(2, 8).AlignItems(ui.Center).
				Background(components.BlameColor(t, bl.OID)).
				Children(func() {
					ui.Text(c, bl.ShortOID).FontSize(10).TextColor(t.Accent).SingleLine().
						OnClick(func() {
							a.fileHist.close()
							a.history.selectCommit(a.commit.RepoPath, bl.OID, a.updater())
						})
					ui.Text(c, bl.Author).FontSize(10).TextColor(t.Text).SingleLine().Grow(1)
				})
			ui.Row(c).Width(48).Justify(ui.End).Padding(2, 8).Children(func() {
				ui.Text(c, fmt.Sprintf("%d", bl.LineNo)).FontSize(11).TextColor(t.TextMuted)
			})
			ui.Row(c).Grow(1).Padding(2, 8).Children(func() {
				ui.Text(c, bl.Text).FontSize(12).SingleLine()
			})
		})
}

// blameColor picks a gutter tint per commit (cycles a small palette so
// adjacent commits get different hues).
