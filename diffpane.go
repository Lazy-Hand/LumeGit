package main

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/lobehub/lobe-mygo/pkg/action"
	"github.com/lobehub/lobe-mygo/pkg/choice"
	"github.com/lobehub/lobe-mygo/pkg/display"
	"github.com/lobehub/lobe-mygo/pkg/field"
	"github.com/lobehub/lobe-mygo/pkg/layout"
	"github.com/lobehub/lobe-mygo/pkg/theme"

	"lumegit/components"
	"lumegit/internal/git"
)

// diffSource is the pane's data supplier: the commit workspace and the
// history tab both render through the same diffView.
type diffSource struct {
	selection string
	loading   bool
	err       string
	diff      *git.DiffInfo
	emptyText string
	// partial shows the hunk-include bar (commit workspace only).
	partial bool
}

// diffPane: mode toggle + diff or placeholder.
func (a *app) diffPane(c *ui.Context, t *ui.Theme) {
	m := &a.commit
	a.renderDiffPane(c, t, diffSource{
		selection: m.selectedFile,
		loading:   m.diffLoading,
		err:       m.diffErr,
		diff:      m.diff,
		emptyText: "选择文件查看变更。",
		partial:   true,
	})
}

// renderDiffPane draws the toggle + whatever the source resolves to.
func (a *app) renderDiffPane(c *ui.Context, t *ui.Theme, s diffSource) {
	tk := theme.TokensOf(c)
	ui.Column(c).Fill().Gap(0).Background(tk.BgContainer).Children(func() {
		ui.Row(c).Padding(8, 12).Gap(10).AlignItems(ui.Center).Children(func() {
			action.Segmented(c, &a.diffMode, []string{"并排", "统一"}, action.SegmentedProps{Size: theme.Small})
			// Flexible spacer pushes the labels to the far edge.
			ui.Text(c, "").Grow(1)
			if s.diff != nil {
				ui.Text(c, s.diff.OldLabel+"  →  "+s.diff.NewLabel).TextColor(t.TextMuted)
			}
		})
		// Hunk checkboxes for partial commit — only on the commit tab's
		// worktree diff, where ticking a chunk means "put this in the
		// commit" (IDEA's per-chunk include).
		if s.partial && !s.loading && s.diff != nil && s.err == "" && len(a.commit.hunks) > 1 {
			a.hunkBarView(c, t)
		}
		switch {
		case s.selection == "":
			components.Placeholder(c, t, s.emptyText)
		case s.loading:
			components.Placeholder(c, t, "正在加载差异…")
		case s.err != "":
			components.Placeholder(c, t, "差异加载失败："+s.err)
		case s.diff == nil:
			components.Placeholder(c, t, "无差异可显示。")
		case s.diff.Binary:
			components.Placeholder(c, t, "二进制文件 — 不显示差异。")
		default:
			if a.diffStateFor != s.diff {
				a.diffState = nil
				if s.partial {
					a.diffState = a.commit.diffPrepared
				}
				a.diffStateFor = s.diff
			}
			a.diffView(c, t, s.diff)
		}
	})
}

// diffView builds or reuses the DiffState for the current selection.
func (a *app) diffView(c *ui.Context, t *ui.Theme, d *git.DiffInfo) {
	if a.diffState == nil {
		a.diffState = newDiffState(d)
	}
	if a.diffMode == 1 {
		a.diffState.Layout = editor.DiffUnified
	} else {
		a.diffState.Layout = editor.DiffSplit
	}
	a.diffState.SplitPercent = a.diffSplit
	editor.DiffView(c, a.diffState).Grow(1).FillWidth()
	a.diffSplit = a.diffState.SplitPercent
}

// hunkBarView is the partial-commit strip: one checkbox per changed
// region of the selected file, like IDEA's per-chunk tick boxes.
func (a *app) hunkBarView(c *ui.Context, t *ui.Theme) {
	m := &a.commit
	tk := theme.TokensOf(c)
	ui.Column(c).FillWidth().Padding(4, 12).Gap(2).Background(tk.BgLayout).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "包含到提交：").FontSize(11).Bold().TextColor(t.TextMuted)
			ui.Text(c, fmt.Sprintf("%d 个改动块", len(m.hunks))).FontSize(11).TextColor(t.TextMuted)
		})
		layout.ScrollArea(c, layout.ScrollAreaProps{}, func() {
			ui.Row(c).Gap(12).FillWidth().Children(func() {
				for i := range m.hunks {
					h := &m.hunks[i]
					label := fmt.Sprintf("块 %d · %s", i+1, h.ID())
					choice.Checkbox(c, m.hunkCheckedBind(i), label, choice.CheckboxProps{Size: 13})
				}
			})
		}).MaxHeight(40)
	})
}

// commitBarView: message + options + actions.
func (a *app) commitBarView(c *ui.Context, t *ui.Theme) {
	m := &a.commit
	tk := theme.TokensOf(c)
	ui.Column(c).FillWidth().Padding(10, 16).Gap(8).Background(tk.BgContainer).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Start).Children(func() {
			ui.Column(c).Grow(1).Gap(4).Children(func() {
				field.TextArea(c, &m.message, field.TextAreaProps{
					Placeholder: "提交信息…",
					MinRows:     3,
					MaxRows:     8,
				}).Grow(1)
				ui.Row(c).Gap(14).AlignItems(ui.Center).Children(func() {
					isSVN := m.isSVN()
					if !isSVN {
						choice.Checkbox(c, &m.amend, "修改上一次提交", choice.CheckboxProps{Size: 14})
						choice.Checkbox(c, &m.noVerify, "跳过 Git hooks", choice.CheckboxProps{Size: 14})
					}
					ui.Text(c, "").Grow(1)
					if !isSVN {
						action.Button(c, "模板", action.ButtonProps{Variant: action.Text, Size: theme.Small}).
							Disabled(m.busy).OnClick(func() { m.loadTemplate(a.updater()) })
						action.Button(c, "检查", action.ButtonProps{Variant: action.Text, Size: theme.Small}).
							Disabled(m.busy).OnClick(func() {
							m.preChecksOpen = !m.preChecksOpen
							if m.preChecksOpen && !m.preChecksLoaded {
								m.loadPreChecks(a.updater())
							}
						})
					}
					ui.Text(c, components.CheckedCount(m.Snap, func(path string) bool {
						st := m.checks.byPath[path]
						return st != nil && st.checked
					})).TextColor(t.TextMuted)
				})
				if m.preChecksOpen {
					a.preChecksView(c, t)
				}
			})
			ui.Column(c).Gap(6).Children(func() {
				action.Button(c, "提交", action.ButtonProps{Variant: action.Primary, Size: theme.Middle}).
					Disabled(m.busy).OnClick(a.doCommit).MinWidth(110)
				if !m.isSVN() {
					action.Button(c, "提交并推送", action.ButtonProps{Variant: action.Default, Size: theme.Middle}).
						Disabled(m.busy).OnClick(a.doCommitPush).MinWidth(110)
				}
			})
		})
	})
}

// preChecksView renders the commit-checks drawer (IDEA's "Checks"):
// TODO count, whitespace warnings, hooks presence. Advisory only.
func (a *app) preChecksView(c *ui.Context, t *ui.Theme) {
	m := &a.commit
	tk := theme.TokensOf(c)
	ui.Column(c).FillWidth().Gap(6).Padding(8, 12).Radius(6).
		Border(1, tk.BorderSecondary).Background(tk.BgLayout).
		Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
				ui.Text(c, "提交前检查").Bold().FontSize(12)
				ui.Text(c, "").Grow(1)
				if m.preChecksLoad {
					ui.Text(c, "检查中…").FontSize(11).TextColor(t.TextMuted)
				}
			})
			if m.preChecks == nil {
				ui.Text(c, "尚未运行检查").FontSize(11).TextColor(t.TextMuted)
				return
			}
			pc := m.preChecks
			// TODO/FIXME count.
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				if pc.TODOCount == 0 {
					display.Tag(c, "✓", display.TagProps{Variant: display.TagBorderless, Color: display.TagSuccess})
					ui.Text(c, "没有 TODO/FIXME").FontSize(12)
				} else {
					display.Tag(c, fmt.Sprintf("%d", pc.TODOCount), display.TagProps{Variant: display.TagBorderless, Color: display.TagWarning})
					ui.Text(c, fmt.Sprintf("发现 %d 处 TODO/FIXME", pc.TODOCount)).FontSize(12)
				}
			})
			// Whitespace errors.
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				if len(pc.WhitespaceErr) == 0 {
					display.Tag(c, "✓", display.TagProps{Variant: display.TagBorderless, Color: display.TagSuccess})
					ui.Text(c, "没有空白问题").FontSize(12)
				} else {
					display.Tag(c, fmt.Sprintf("%d", len(pc.WhitespaceErr)), display.TagProps{Variant: display.TagBorderless, Color: display.TagWarning})
					ui.Text(c, fmt.Sprintf("%d 处空白问题", len(pc.WhitespaceErr))).FontSize(12)
				}
			})
			// Hooks.
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				if pc.HasHooks {
					display.Tag(c, "!", display.TagProps{Variant: display.TagBorderless, Color: display.TagInfo})
					ui.Text(c, "存在 pre-commit 钩子").FontSize(12)
				} else {
					display.Tag(c, "✓", display.TagProps{Variant: display.TagBorderless, Color: display.TagSuccess})
					ui.Text(c, "无 pre-commit 钩子").FontSize(12)
				}
			})
			// First few whitespace details.
			if len(pc.WhitespaceErr) > 0 {
				show := pc.WhitespaceErr
				if len(show) > 4 {
					show = show[:4]
				}
				for _, ln := range show {
					ui.Text(c, "  "+ln).FontSize(11).TextColor(t.TextMuted).SingleLine()
				}
				if len(pc.WhitespaceErr) > 4 {
					ui.Text(c, fmt.Sprintf("  …还有 %d 条", len(pc.WhitespaceErr)-4)).FontSize(11).TextColor(t.TextMuted)
				}
			}
		})
}

// doCommit runs the selective commit on the ticked set — the same path
// for both view modes (changelist and IDEA's "Local Changes" split
// differ only in grouping, not in what a tick means).
func (a *app) doCommit() {
	m := &a.commit
	if m.busy {
		return
	}
	m.commitSelected(appCTX, a.updater())
}

// doCommitPush opens the push dialog flagged "commit first" — the
// actual commit+push runs only after the user confirms in the modal.
func (a *app) doCommitPush() {
	m := &a.commit
	if m.busy {
		return
	}
	a.commit.openPushDialog(a.updater(), true)
}
