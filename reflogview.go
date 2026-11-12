package main

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/action"
	"github.com/lobehub/lobe-mygo/pkg/display"
	"github.com/lobehub/lobe-mygo/pkg/feedback"
	"github.com/lobehub/lobe-mygo/pkg/layout"
	"github.com/lobehub/lobe-mygo/pkg/theme"

	"lumegit/components"
	"lumegit/internal/git"
)

// --- Reflog -----------------------------------------------------------

// reflogOverlay renders the Reflog panel: HEAD history with actions.
func (a *app) reflogOverlay(c *ui.Context, t *ui.Theme) {
	m := &a.reflog
	feedback.Modal(c, &m.open, feedback.ModalProps{
		Title:    "Reflog — HEAD 操作历史",
		Width:    780,
		NoFooter: true,
	}, func() {
		ui.Column(c).Gap(8).MinHeight(400).Children(func() {
			switch {
			case m.loading:
				components.Placeholder(c, t, "正在加载 Reflog…")
			case m.err != "":
				feedback.Alert(c, m.err, feedback.AlertProps{Type: feedback.Error, Variant: feedback.Soft})
			case len(m.entries) == 0:
				components.Placeholder(c, t, "没有 Reflog 记录。")
			default:
				layout.ScrollArea(c, layout.ScrollAreaProps{}, func() {
					ui.Column(c).FillWidth().Children(func() {
						for i := range m.entries {
							a.reflogRow(c, t, &m.entries[i])
						}
					})
				}).Grow(1)
			}
		})
	})
}

// reflogRow: index + action + message + target oid + time.
func (a *app) reflogRow(c *ui.Context, t *ui.Theme, e *git.ReflogEntry) {
	repoPath := a.commit.RepoPath
	ui.Row(c).Key(fmt.Sprintf("rl-%d-%s", e.Index, e.OID)).FillWidth().Padding(6, 10).Gap(10).AlignItems(ui.Center).
		Children(func() {
			ui.Text(c, fmt.Sprintf("HEAD@{%d}", e.Index)).FontSize(11).TextColor(t.TextMuted).Width(80).SingleLine()
			display.Tag(c, e.Action, display.TagProps{Variant: display.TagBorderless, Color: display.TagInfo})
			ui.Column(c).Grow(1).Gap(1).Children(func() {
				ui.Text(c, e.Message).SingleLine()
				ui.Row(c).Gap(6).Children(func() {
					ui.Text(c, e.Short).FontSize(11).TextColor(t.TextMuted)
					ui.Text(c, e.Who).FontSize(11).TextColor(t.TextMuted)
					ui.Text(c, components.RelTime(e.When)).FontSize(11).TextColor(t.TextMuted)
				})
			})
			// Jump to that commit in Log — also switch to the 历史 tab so
			// the user lands on it (IDEA: Reflog → Show in Log).
			action.Button(c, "查看", action.ButtonProps{Variant: action.Text, Size: theme.Small}).
				OnClick(func() {
					a.reflog.close()
					a.tab = 1
					a.history.ensureLoaded(repoPath, a.updater())
					a.history.selectCommit(repoPath, e.OID, a.updater())
					a.history.scrollToCommit(e.OID, a.commit.RepoPath, a.updater())
				})
		})
}
