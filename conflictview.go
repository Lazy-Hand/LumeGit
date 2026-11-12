package main

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/action"
	"github.com/lobehub/lobe-mygo/pkg/display"
	"github.com/lobehub/lobe-mygo/pkg/feedback"
	"github.com/lobehub/lobe-mygo/pkg/field"
	"github.com/lobehub/lobe-mygo/pkg/layout"
	"github.com/lobehub/lobe-mygo/pkg/theme"

	"lumegit/components"
	"lumegit/internal/git"
)

// --- 冲突解决 ---------------------------------------------------------

// conflictOverlay renders the three-way resolver modal.
func (a *app) conflictOverlay(c *ui.Context, t *ui.Theme) {
	m := &a.conflict
	feedback.Modal(c, &m.open, feedback.ModalProps{
		Title:    "解决冲突 — " + m.path,
		Width:    960,
		NoFooter: true,
	}, func() {
		switch {
		case m.loading:
			components.Placeholder(c, t, "正在加载冲突内容…")
		case m.err != "":
			feedback.Alert(c, m.err, feedback.AlertProps{Type: feedback.Error, Variant: feedback.Soft})
		case m.sides == nil:
			components.Placeholder(c, t, "无冲突内容。")
		default:
			a.conflictResolver(c, t)
		}
	})
}

// conflictResolver draws base/ours/theirs + merge editor + actions.
func (a *app) conflictResolver(c *ui.Context, t *ui.Theme) {
	m := &a.conflict
	// stage2 "ours" is HEAD's side during merge — but during rebase git
	// swaps the roles (ours = the onto target, theirs = your replayed
	// commits). Label from the in-progress op, not the raw stage name.
	oursLabel, theirsLabel := "当前分支（ours）", "合并进来（theirs）"
	oursAction, theirsAction := "采用当前分支", "采用对方"
	oursResolve, theirsResolve := "采用当前分支并解决", "采用对方并解决"
	if a.commit.Snap != nil && a.commit.Snap.Git != nil &&
		strings.HasPrefix(a.commit.Snap.Git.InProgress, "rebase") {
		oursLabel, theirsLabel = "目标分支（ours）", "当前分支提交（theirs）"
		oursAction, theirsAction = "采用目标分支", "采用当前分支提交"
		oursResolve, theirsResolve = "采用目标分支并解决", "采用当前分支提交并解决"
	}
	ui.Column(c).Gap(10).Children(func() {
		if m.sides.TreeConflict {
			// svn tree conflict: no side files exist — pick an svn
			// accept choice instead of a 3-way merge (saving "merged"
			// text would write an empty file).
			feedback.Alert(c,
				"SVN 树冲突：该条目没有可对比的文件内容（移动/删除与本地修改冲突）。用 svn 的接受方式解决。",
				feedback.AlertProps{Type: feedback.Warning, Variant: feedback.Soft})
		} else {
			// Three sides side-by-side (read-only diffs against base).
			ui.Row(c).Gap(8).Children(func() {
				components.ConflictSide(c, t, oursLabel, m.sides.Ours)
				components.ConflictSide(c, t, "基础版本（base）", m.sides.Base)
				components.ConflictSide(c, t, theirsLabel, m.sides.Their)
			})
			// Merge editor.
			ui.Column(c).Gap(4).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "合并结果").Bold()
					ui.Text(c, "").Grow(1)
					action.Button(c, oursAction, action.ButtonProps{Variant: action.Text, Size: theme.Small}).
						OnClick(func() { m.merged = string(m.sides.Ours) })
					action.Button(c, theirsAction, action.ButtonProps{Variant: action.Text, Size: theme.Small}).
						OnClick(func() { m.merged = string(m.sides.Their) })
				})
				field.TextArea(c, &m.merged, field.TextAreaProps{
					MinRows: 8,
					MaxRows: 16,
				}).Grow(1)
			})
		}
		// Actions.
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			action.Button(c, oursResolve, action.ButtonProps{Variant: action.Default, Size: theme.Middle}).
				Disabled(m.busy).OnClick(func() {
				m.resolve(a.commit.RepoPath, git.ResolveOurs, a.updater(), func() {
					a.commit.refresh(appCTX, a.updater())
				})
			})
			action.Button(c, theirsResolve, action.ButtonProps{Variant: action.Default, Size: theme.Middle}).
				Disabled(m.busy).OnClick(func() {
				m.resolve(a.commit.RepoPath, git.ResolveTheirs, a.updater(), func() {
					a.commit.refresh(appCTX, a.updater())
				})
			})
			if !m.sides.TreeConflict {
				action.Button(c, "保存合并结果", action.ButtonProps{Variant: action.Primary, Size: theme.Middle}).
					Disabled(m.busy).OnClick(func() {
					m.resolve(a.commit.RepoPath, git.ResolveMerge, a.updater(), func() {
						a.commit.refresh(appCTX, a.updater())
					})
				})
			}
		})
	})
}

// --- Resolve Conflicts 列表（IDEA 的 Git → Resolve Conflicts…） ----------

// conflictsOverlay renders the all-files conflict list: per-row
// "ours / theirs / merge…" plus a batch action row, mirroring IDEA's
// dedicated dialog.
func (a *app) conflictsOverlay(c *ui.Context, t *ui.Theme) {
	m := &a.conflicts
	feedback.Modal(c, &m.open, feedback.ModalProps{
		Title:    "解决冲突",
		Width:    720,
		NoFooter: true,
	}, func() {
		ui.Column(c).Gap(10).MinHeight(320).Children(func() {
			switch {
			case m.loading:
				components.Placeholder(c, t, "正在加载冲突文件…")
			case m.err != "":
				feedback.Alert(c, m.err, feedback.AlertProps{Type: feedback.Error, Variant: feedback.Soft})
			case len(m.entries) == 0:
				components.Placeholder(c, t, "没有冲突 — 所有文件都已解决。")
			default:
				// Summary + batch actions (IDEA's toolbar).
				ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, fmt.Sprintf("共 %d 个冲突文件", len(m.entries))).Grow(1)
					action.Button(c, "全部采用当前分支", action.ButtonProps{Variant: action.Default, Size: theme.Small}).
						Disabled(m.busy).OnClick(func() {
						m.resolveAll(a.commit.RepoPath, git.ResolveOurs, a.updater(), func() {
							a.commit.refresh(appCTX, a.updater())
						})
					})
					action.Button(c, "全部采用对方", action.ButtonProps{Variant: action.Default, Size: theme.Small}).
						Disabled(m.busy).OnClick(func() {
						m.resolveAll(a.commit.RepoPath, git.ResolveTheirs, a.updater(), func() {
							a.commit.refresh(appCTX, a.updater())
						})
					})
				})
				// File list.
				layout.ScrollArea(c, layout.ScrollAreaProps{}, func() {
					ui.Column(c).FillWidth().Children(func() {
						for i := range m.entries {
							a.conflictEntryRow(c, t, &m.entries[i])
						}
					})
				}).Grow(1)
				// Footer.
				ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
					action.Button(c, "关闭", action.ButtonProps{Variant: action.Default, Size: theme.Middle}).
						OnClick(m.close)
				})
			}
		})
	})
}

// conflictEntryRow: one unmerged path + its actions.
func (a *app) conflictEntryRow(c *ui.Context, t *ui.Theme, e *git.ConflictEntry) {
	repoPath := a.commit.RepoPath
	ui.Row(c).Key("cf-"+e.Path).FillWidth().Padding(6, 10).Gap(10).AlignItems(ui.Center).
		Children(func() {
			display.Tag(c, e.Kind, display.TagProps{
				Variant: display.TagBorderless,
				Color:   display.TagWarning,
			})
			ui.Column(c).Grow(1).Gap(1).Children(func() {
				ui.Text(c, e.Path).SingleLine()
				ui.Text(c, fmt.Sprintf("%d 处冲突标记", e.Hunks)).FontSize(11).TextColor(t.TextMuted)
			})
			action.Button(c, "采用当前", action.ButtonProps{Variant: action.Text, Size: theme.Small}).
				OnClick(func() {
					a.conflicts.resolveOne(repoPath, e.Path, git.ResolveOurs, a.updater(), func() {
						a.commit.refresh(appCTX, a.updater())
					})
				})
			action.Button(c, "采用对方", action.ButtonProps{Variant: action.Text, Size: theme.Small}).
				OnClick(func() {
					a.conflicts.resolveOne(repoPath, e.Path, git.ResolveTheirs, a.updater(), func() {
						a.commit.refresh(appCTX, a.updater())
					})
				})
			action.Button(c, "合并…", action.ButtonProps{Variant: action.Default, Size: theme.Small}).
				OnClick(func() {
					a.conflict.openFor(repoPath, e.Path, a.updater())
				})
		})
}
