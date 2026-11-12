package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/action"
	"github.com/lobehub/lobe-mygo/pkg/choice"
	"github.com/lobehub/lobe-mygo/pkg/feedback"
	"github.com/lobehub/lobe-mygo/pkg/field"
	"github.com/lobehub/lobe-mygo/pkg/layout"
	"github.com/lobehub/lobe-mygo/pkg/theme"
	"path/filepath"

	"lumegit/components"
	"lumegit/internal/git"
)

func (a *app) exportWorktreePatch(path string) {
	gen := a.commit.gen
	w := a.updater()
	repoPath := a.commit.RepoPath
	go func() {
		dest, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
			Parent:      a.win,
			Title:       "导出补丁",
			DefaultPath: repoPath + "/" + filepath.Base(path) + ".patch",
			Filters:     []mygo.FileFilter{{Name: "补丁", Extensions: []string{"patch"}}},
		})
		w.update(func() {
			if gen != a.commit.gen || err != nil || dest == "" {
				return
			}
			a.commit.createPatch(appCTX, w, []string{path}, "", dest)
		})
	}()
}

// exportCommitPatch asks for a destination dir then exports the commit.
func (a *app) exportCommitPatch(oid, short string) {
	gen := a.commit.gen
	w := a.updater()
	repoPath := a.commit.RepoPath
	go func() {
		dest, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
			Parent:      a.win,
			Title:       "导出补丁",
			DefaultPath: repoPath + "/" + short + ".patch",
			Filters:     []mygo.FileFilter{{Name: "补丁", Extensions: []string{"patch"}}},
		})
		w.update(func() {
			if gen != a.commit.gen || err != nil || dest == "" {
				return
			}
			a.commit.createPatch(appCTX, w, nil, oid, dest)
		})
	}()
}

// openApplyPatch picks a .patch file and applies it (IDEA: Apply Patch…).
func (a *app) openApplyPatch() {
	gen := a.commit.gen
	w := a.updater()
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:  a.win,
			Title:   "应用补丁",
			Message: "选择 .patch 文件",
			Filters: []mygo.FileFilter{{Name: "补丁", Extensions: []string{"patch", "diff"}}},
		})
		w.update(func() {
			if gen != a.commit.gen || err != nil || len(paths) == 0 {
				return
			}
			a.commit.applyPatch(appCTX, w, paths[0])
		})
	}()
}

// pullDialogView renders IDEA's "Update Project": merge/rebase choice
// and the stash-before-update option.
func (a *app) pullDialogView(c *ui.Context, t *ui.Theme) {
	m := &a.commit
	d := &m.pullDlg
	confirmed := feedback.Modal(c, &d.Open, feedback.ModalProps{
		Title:  "更新项目",
		Width:  480,
		OkText: "拉取",
	}, func() {
		ui.Column(c).Gap(10).Children(func() {
			target := d.Branch
			if d.Upstream != "" {
				target += " ← " + d.Upstream
			}
			ui.Text(c, target).Bold()
			ui.Text(c, "更新方式：").FontSize(12).TextColor(t.TextMuted)
			ui.Column(c).Gap(6).Padding(0, 0, 0, 8).Children(func() {
				choice.Radio(c, &d.Rebase, false, "合并（merge）—— 上游提交合并进来", choice.RadioProps{})
				choice.Radio(c, &d.Rebase, true, "变基（rebase）—— 本地提交重放到上游之上", choice.RadioProps{})
			})
			choice.Checkbox(c, &d.Autostash, "更新前暂存未提交的改动（--autostash）", choice.CheckboxProps{Size: 14})
			if d.Dirty {
				ui.Text(c, "检测到未提交的改动。").FontSize(11).TextColor(t.TextMuted)
			}
		})
	})
	if confirmed {
		m.doPull(appCTX, a.updater())
	}
}

// pushDialogView renders IDEA's push confirmation: target, commit
// preview, force-with-lease + tags toggles.
func (a *app) pushDialogView(c *ui.Context, t *ui.Theme) {
	m := &a.commit
	d := &m.pushDlg
	confirmed := feedback.Modal(c, &d.Open, feedback.ModalProps{
		Title:      "推送到远程",
		Width:      560,
		OkText:     "推送",
		CancelText: "取消",
		NoFooter:   false,
	}, func() {
		ui.Column(c).Gap(10).Children(func() {
			switch {
			case d.Loading:
				components.Placeholder(c, t, "正在检查上游…")
			case d.Err != "":
				feedback.Alert(c, d.Err, feedback.AlertProps{Type: feedback.Error, Variant: feedback.Soft})
			default:
				target := "origin"
				if d.HasUpstream {
					target = d.Upstream
				}
				ui.Text(c, "当前分支 → "+target).Bold()
				if !d.HasUpstream {
					ui.Text(c, "没有上游分支：将以 -u 发布到 origin。").FontSize(12).TextColor(t.TextMuted)
				}
				if d.Diverged {
					feedback.Alert(c,
						"历史已被改写（amend/变基），与远程分叉。推送需要 force-with-lease。",
						feedback.AlertProps{Type: feedback.Warning, Variant: feedback.Soft})
				}
				ui.Text(c, fmt.Sprintf("将推送 %d 个提交：", len(d.Commits))).FontSize(12)
				ui.Column(c).Border(1, theme.TokensOf(c).BorderSecondary).Radius(6).Padding(6).MaxHeight(180).Children(func() {
					layout.ScrollArea(c, layout.ScrollAreaProps{}, func() {
						ui.Column(c).Gap(2).Children(func() {
							if len(d.Commits) == 0 {
								ui.Text(c, "（没有需要推送的提交）").FontSize(12).TextColor(t.TextMuted)
							}
							for i := range d.Commits {
								cm := &d.Commits[i]
								ui.Row(c).Gap(8).Children(func() {
									ui.Text(c, cm.ShortOID).FontSize(11).TextColor(t.TextMuted)
									ui.Text(c, cm.Subject).FontSize(12).SingleLine()
								})
							}
						})
					}).Grow(1)
				})
				ui.Row(c).Gap(14).Children(func() {
					choice.Checkbox(c, &d.Force, "强制推送（--force-with-lease）", choice.CheckboxProps{Size: 14})
					choice.Checkbox(c, &d.PushTags, "同时推送标签", choice.CheckboxProps{Size: 14})
				})
			}
		})
	})
	if confirmed && !d.Loading && d.Err == "" {
		if d.afterCommit {
			m.commitThenPush(appCTX, a.updater())
		} else {
			m.doPush(appCTX, a.updater())
		}
	}
}

// rebaseDialogView renders the interactive-rebase plan (IDEA's
// "Rebasing Commits" panel): one row per commit with an action picker
// and up/down reordering.
func (a *app) rebaseDialogView(c *ui.Context, t *ui.Theme) {
	m := &a.commit
	d := &m.rebaseDlg
	confirmed := feedback.Modal(c, &d.Open, feedback.ModalProps{
		Title:  "交互式变基 — " + components.ShortOID(d.Onto),
		Width:  720,
		OkText: "开始变基",
	}, func() {
		ui.Column(c).Gap(8).Children(func() {
			ui.Text(c, "调整每条提交的动作；拖动/上移下移改变顺序（从上到下执行）。​").
				FontSize(12).TextColor(t.TextMuted)
			ui.Column(c).Border(1, theme.TokensOf(c).BorderSecondary).Radius(6).Children(func() {
				layout.ScrollArea(c, layout.ScrollAreaProps{}, func() {
					ui.Column(c).FillWidth().Children(func() {
						for i := range d.Entries {
							a.rebaseEntryRow(c, t, i)
						}
					})
				}).Grow(1).MinHeight(240)
			})
		})
	})
	if confirmed {
		m.runInteractiveRebase(appCTX, a.updater())
	}
}

// rebaseEntryRow: one commit row — action dropdown + subject + reorder.
func (a *app) rebaseEntryRow(c *ui.Context, t *ui.Theme, i int) {
	d := &a.commit.rebaseDlg
	e := &d.Entries[i]
	ui.Row(c).FillWidth().Padding(6, 10).Gap(8).AlignItems(ui.Center).
		Children(func() {
			// Action picker — IDEA shows a dropdown.
			actions := []git.RebaseAction{
				git.RebasePick, git.RebaseReword, git.RebaseEdit,
				git.RebaseSquash, git.RebaseFixup, git.RebaseDrop,
			}
			ui.Column(c).MinWidth(120).Children(func() {
				cur := string(e.Action)
				if cur == "" {
					cur = "pick"
				}
				ui.Text(c, "▾ "+cur).FontSize(12).Bold()
			}).ContextMenu(func(mn *ui.Menu) {
				for _, act := range actions {
					a := act
					if mn.Item(string(a)).Chosen() {
						e.Action = a
					}
				}
			})
			ui.Text(c, e.Short).FontSize(11).TextColor(t.TextMuted)
			ui.Text(c, e.Subject).FontSize(12).SingleLine().Grow(1)
			if e.Action == git.RebaseReword {
				field.Input(c, &e.Message, field.InputProps{
					Placeholder: "新的提交信息…",
					Size:        theme.Small,
				}).MinWidth(200)
			}
			ui.Column(c).Gap(2).Children(func() {
				action.Button(c, "↑", action.ButtonProps{Variant: action.Text, Size: theme.Small}).
					Disabled(i == 0).OnClick(func() {
					d.Entries[i-1], d.Entries[i] = d.Entries[i], d.Entries[i-1]
				})
				action.Button(c, "↓", action.ButtonProps{Variant: action.Text, Size: theme.Small}).
					Disabled(i == len(d.Entries)-1).OnClick(func() {
					d.Entries[i+1], d.Entries[i] = d.Entries[i], d.Entries[i+1]
				})
			})
		})
}

// createTag runs git tag then refreshes both panes.
func (a *app) createTag() {
	m := &a.branches
	name := strings.TrimSpace(m.tagName)
	if name == "" {
		m.tagErr = "请输入标签名"
		return
	}
	target := strings.TrimSpace(m.tagTarget)
	msg := m.tagMessage
	checkout := m.tagCheckout
	m.op(a.commit.RepoPath, a.updater(), "正在创建…", "已创建标签",
		func(r *git.Repo) (*git.RunResult, error) {
			return r.CreateTag(context.Background(), name, target, msg)
		},
		func() {
			m.tagOpen = false
			m.tagErr = ""
			if checkout {
				m.op(a.commit.RepoPath, a.updater(), "正在检出…", "已检出",
					func(r *git.Repo) (*git.RunResult, error) {
						return r.CheckoutTag(context.Background(), name)
					},
					func() { a.commit.refresh(appCTX, a.updater()) })
			}
		})
}

// openWorktreeCompare diffs path at ref (commit/branch) against the
// worktree — IDEA's "Compare with Local".
