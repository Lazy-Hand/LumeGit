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
	"github.com/lobehub/lobe-mygo/pkg/theme"

	"lumegit/components"
	"lumegit/internal/git"
)

// --- 分支（Branches）弹出菜单 -------------------------------------------
//
// IDEA 把分支树放在 header 分支名按钮的下拉菜单里，不是独立面板。
// 每条本地/远程分支是一个 Submenu，项即原分支页的右键操作。

// branchPopupMenu builds the branches dropdown under the header tag.
//
// IDEA's Branches popup: New Branch/Tag on top, then local branches,
// remote branches, tags, submodules and worktrees — every ref a submenu
// holding the ops the old tab's context menu had.
func (a *app) branchPopupMenu(mn *ui.Menu) {
	m := &a.branches
	repoPath := a.commit.RepoPath
	m.ensureLoaded(repoPath, a.updater())

	mn.MaxWidth(320)
	if m.isSVN() {
		a.svnBranchPopupMenu(mn)
		return
	}
	if mn.Item("新建分支…").Chosen() {
		a.newBranchFrom = ""
		a.branchDialogKind = 2 // create + checkout
		a.branchDialogOpen = true
	}
	if mn.Item("新建标签…").Chosen() {
		m.tagOpen = true
		m.tagName, m.tagTarget, m.tagMessage, m.tagErr = "", "", "", ""
	}
	if mn.Item("管理远程…").Chosen() {
		m.remoteOpen = true
		m.remoteErr = ""
		m.remoteEdit = ""
		m.remoteName, m.remoteURL = "", ""
		m.loadRemotes(repoPath, a.updater())
	}
	if mn.Item("新建工作树…").Chosen() {
		m.wtOpen = true
		m.wtPath, m.wtRef, m.wtBranch, m.wtErr = "", "", "", ""
	}
	if mn.Item("刷新").Chosen() {
		m.lastLoaded = ""
		m.ensureLoaded(repoPath, a.updater())
	}
	mn.Separator()
	if m.loading {
		mn.Item("正在加载分支…").Disabled(true)
		return
	}
	if m.err != "" {
		mn.Item("加载失败：" + m.err).Disabled(true)
	}
	if len(m.local) == 0 && len(m.remote) == 0 && len(m.tags) == 0 {
		mn.Item("（无分支）").Disabled(true)
		return
	}
	// 本地分支
	for i := range m.local {
		b := m.local[i]
		label := b.Name
		if b.Current {
			label = "✓ " + label
		}
		if b.HasTrack && (b.Ahead > 0 || b.Behind > 0) {
			label += fmt.Sprintf("  +%d −%d", b.Ahead, b.Behind)
		}
		mn.Submenu(label, func(sub *ui.Menu) { a.localBranchMenu(sub, &b) })
	}
	if len(m.remote) > 0 {
		mn.Separator()
		mn.Submenu("远程分支", func(sub *ui.Menu) {
			for i := range m.remote {
				b := m.remote[i]
				sub.Submenu(b.Name, func(s2 *ui.Menu) { a.remoteBranchMenu(s2, &b) })
			}
		})
	}
	if len(m.tags) > 0 {
		mn.Separator()
		mn.Submenu("标签", func(sub *ui.Menu) {
			for i := range m.tags {
				tg := m.tags[i]
				sub.Submenu(tg.Name, func(s2 *ui.Menu) { a.tagMenu(s2, &tg) })
			}
		})
	}
	if len(m.submodules) > 0 {
		mn.Separator()
		mn.Submenu("子模块", func(sub *ui.Menu) {
			for i := range m.submodules {
				sm := m.submodules[i]
				sub.Submenu(sm.Path, func(s2 *ui.Menu) { a.submoduleMenu(s2, &sm) })
			}
		})
	}
	if len(m.worktrees) > 0 {
		mn.Separator()
		mn.Submenu("工作树", func(sub *ui.Menu) {
			for i := range m.worktrees {
				wt := m.worktrees[i]
				label := wt.Path
				if wt.IsMain {
					label += "（主）"
				}
				sub.Submenu(label, func(s2 *ui.Menu) { a.worktreeMenu(s2, &wt) })
			}
		})
	}
}

// svnBranchPopupMenu is the branch tag's dropdown on an svn working
// copy: svn has no ref tree — "branches" and "tags" are repo dirs —
// so the menu lists them from svn list and offers switch (svn switch)
// and copy (svn copy) instead of git's branch ops.
func (a *app) svnBranchPopupMenu(mn *ui.Menu) {
	m := &a.branches
	repoPath := a.commit.RepoPath

	// Current location — which dir the WC is on.
	cur := m.svnCurrentURL()
	if cur != "" {
		mn.Item("当前：" + cur).Disabled(true)
		mn.Separator()
	}
	if mn.Item("新建分支（svn copy）…").Chosen() {
		a.svnNewBranchOpen = true
		a.svnNewBranchName = ""
		a.svnNewBranchRev = 0 // popup copies current HEAD, not a rev
	}
	if mn.Item("刷新").Chosen() {
		m.lastLoaded = ""
		m.ensureLoaded(repoPath, a.updater())
	}
	mn.Separator()
	if m.loading {
		mn.Item("正在加载分支…").Disabled(true)
		return
	}
	if m.err != "" {
		mn.Item("加载失败：" + m.err).Disabled(true)
	}
	// Branches dir.
	root := m.svnRepoRoot()
	if len(m.svnBranches) > 0 {
		mn.Submenu("分支", func(sub *ui.Menu) {
			for _, name := range m.svnBranches {
				n := name
				url := root + "/branches/" + n
				label := n
				if url == cur {
					label = "✓ " + n
				}
				if sub.Item(label).Chosen() && url != cur {
					m.svnSwitch(repoPath, url, a.updater(), a.svnAfterSwitch)
				}
			}
		})
	}
	if len(m.svnTags) > 0 {
		mn.Submenu("标签", func(sub *ui.Menu) {
			for _, name := range m.svnTags {
				n := name
				url := root + "/tags/" + n
				label := n
				if url == cur {
					label = "✓ " + n
				}
				if sub.Item(label).Chosen() && url != cur {
					m.svnSwitch(repoPath, url, a.updater(), a.svnAfterSwitch)
				}
			}
		})
	}
	// Trunk is a first-class entry — going back to trunk is the common case.
	if cur != root+"/trunk" {
		mn.Separator()
		if mn.Item("切换到 trunk").Chosen() {
			m.svnSwitch(repoPath, root+"/trunk", a.updater(), a.svnAfterSwitch)
		}
	}
}

// svnAfterSwitch refreshes everything a WC URL switch invalidates: the
// commit pane's snapshot, and the history pane's cached log + svnRel
// (the relative URL changed, so old rev rows and the prefix strip are
// stale). Same reset a repo re-open performs, without closing the tab.
func (a *app) svnAfterSwitch() {
	a.commit.refreshQuiet(appCTX, a.updater())
	a.history.reset()
	a.history.setBackend(a.commit.VCS)
}

// localBranchMenu: ops on one local branch (checkout/merge/rebase/…).
func (a *app) localBranchMenu(sub *ui.Menu, b *git.Branch) {
	m := &a.branches
	repoPath := a.commit.RepoPath
	name := b.Name
	if !b.Current {
		if sub.Item("切换到此分支").Chosen() {
			m.op(repoPath, a.updater(), "正在切换…", "已切换",
				func(r *git.Repo) (*git.RunResult, error) {
					return r.Checkout(context.Background(), name, false)
				},
				func() { a.commit.refresh(appCTX, a.updater()) })
		}
	}
	if sub.Item("合并到当前分支").Chosen() {
		m.op(repoPath, a.updater(), "正在合并…", "已合并",
			func(r *git.Repo) (*git.RunResult, error) {
				return r.MergeIntoCurrent(context.Background(), name)
			},
			func() { a.commit.refresh(appCTX, a.updater()) })
	}
	if sub.Item("变基到此分支").Chosen() {
		a.commit.rebase(appCTX, a.updater(), name)
	}
	sub.Separator()
	if sub.Item("与当前分支比较").Chosen() {
		a.cmpDiffState = nil
		a.compare.openFor(repoPath, name, a.updater())
	}
	if sub.Item("与其他分支比较…").Chosen() {
		a.cmpDiffState = nil
		a.branchCmpFrom = name
		a.branchCmpOpen = true
		a.branchCmpInput = ""
	}
	if sub.Item("重命名…").Chosen() {
		m.renameOpen = true
		m.renameOld = name
		m.renameNew = name
	}
	sub.Separator()
	if sub.Item("删除").Chosen() {
		m.op(repoPath, a.updater(), "正在删除…", "已删除",
			func(r *git.Repo) (*git.RunResult, error) {
				return r.DeleteBranch(context.Background(), name, false)
			}, nil)
	}
}

// remoteBranchMenu: the one op remote branches support — track locally.
func (a *app) remoteBranchMenu(sub *ui.Menu, b *git.Branch) {
	m := &a.branches
	repoPath := a.commit.RepoPath
	name := b.Name
	if sub.Item("从远程创建本地分支").Chosen() {
		local := strings.TrimPrefix(name, "origin/")
		m.op(repoPath, a.updater(), "正在创建…", "已创建",
			func(r *git.Repo) (*git.RunResult, error) {
				return r.RunOK(context.Background(), "switch", "-c", local, "--track", name)
			},
			func() { a.commit.refresh(appCTX, a.updater()) })
	}
}

// tagMenu: checkout / push / delete for one tag.
func (a *app) tagMenu(sub *ui.Menu, tg *git.Tag) {
	m := &a.branches
	repoPath := a.commit.RepoPath
	name := tg.Name
	if sub.Item("检出（分离 HEAD）").Chosen() {
		m.op(repoPath, a.updater(), "正在检出…", "已检出",
			func(r *git.Repo) (*git.RunResult, error) {
				return r.CheckoutTag(context.Background(), name)
			},
			func() { a.commit.refresh(appCTX, a.updater()) })
	}
	if sub.Item("推送到远程").Chosen() {
		m.op(repoPath, a.updater(), "正在推送…", "已推送",
			func(r *git.Repo) (*git.RunResult, error) {
				return r.PushTag(context.Background(), "", name)
			}, nil)
	}
	sub.Separator()
	if sub.Item("删除").Chosen() {
		m.op(repoPath, a.updater(), "正在删除…", "已删除",
			func(r *git.Repo) (*git.RunResult, error) {
				return r.DeleteTag(context.Background(), name)
			}, nil)
	}
}

// submoduleMenu: init/update for one submodule.
func (a *app) submoduleMenu(sub *ui.Menu, sm *git.Submodule) {
	m := &a.branches
	repoPath := a.commit.RepoPath
	path := sm.Path
	state := "未初始化"
	if sm.Unmerged {
		state = "冲突"
	} else if sm.Init && sm.Dirty {
		state = "已检出（有改动）"
	} else if sm.Init {
		state = "已检出"
	}
	sub.Item("状态：" + state).Disabled(true)
	if !sm.Init || sm.Dirty {
		if sub.Item("更新（init + checkout）").Chosen() {
			m.op(repoPath, a.updater(), "正在更新子模块…", "已更新",
				func(r *git.Repo) (*git.RunResult, error) {
					return r.SubmoduleUpdate(context.Background(), path)
				},
				func() { a.commit.refresh(appCTX, a.updater()) })
		}
	}
}

// worktreeMenu: open-in-window / remove for one linked worktree.
func (a *app) worktreeMenu(sub *ui.Menu, wt *git.Worktree) {
	m := &a.branches
	repoPath := a.commit.RepoPath
	if !wt.IsMain && sub.Item("在新窗口打开此 worktree").Chosen() {
		spawnWindow(wt.Path)
	}
	if wt.IsMain {
		return
	}
	sub.Separator()
	if sub.Item("移除").Chosen() {
		a.askRun("移除 Worktree", "移除 worktree "+wt.Path+"？其中的未提交改动必须先处理。", func() {
			m.op(repoPath, a.updater(), "正在移除 worktree…", "已移除",
				func(r *git.Repo) (*git.RunResult, error) {
					return r.RemoveWorktree(context.Background(), wt.Path, false)
				}, nil)
		})
	}
	if sub.Item("强制移除（丢弃改动）").Chosen() {
		a.askRun("强制移除 Worktree", "强制移除 "+wt.Path+"，其中的未提交改动会一并丢弃，不可恢复。确定继续？", func() {
			m.op(repoPath, a.updater(), "正在强制移除…", "已移除",
				func(r *git.Repo) (*git.RunResult, error) {
					return r.RemoveWorktree(context.Background(), wt.Path, true)
				}, nil)
		})
	}
}

// createBranch makes + checks out a new local branch. The op's after
// hook runs after the refresh landed — but the user shouldn't wait for
// a git call to see the new branch appear, so we optimistically insert
// it into m.local right away; the async reload reconciles.
func (a *app) createBranch() {
	m := &a.branches
	name := strings.TrimSpace(m.newName)
	if name == "" {
		return
	}
	// Optimistic insert — IDEA updates the branch tree as soon as the
	// mutation returns, before the next status reload lands.
	exists := false
	for _, b := range m.local {
		if b.Name == name {
			exists = true
			break
		}
	}
	if !exists {
		m.local = append(m.local, git.Branch{Name: name})
	}
	m.op(a.commit.RepoPath, a.updater(), "正在创建…", "已创建并切换",
		func(r *git.Repo) (*git.RunResult, error) { return r.Checkout(context.Background(), name, true) },
		func() {
			m.newName = ""
			a.commit.refresh(appCTX, a.updater()) // HEAD moved
		})
}

// worktreeDialog collects path + ref + optional new-branch for
// `git worktree add`.
func (a *app) worktreeDialog(c *ui.Context, t *ui.Theme) {
	m := &a.branches
	if feedback.Modal(c, &m.wtOpen, feedback.ModalProps{
		Title: "新建工作树", Width: 480, OkText: "添加",
	}, func() {
		ui.Column(c).Gap(8).Children(func() {
			ui.Row(c).Gap(6).Children(func() {
				field.Input(c, &m.wtPath, field.InputProps{
					Placeholder: "目标目录路径…", Size: theme.Middle,
				}).Grow(1).OnSubmit(func() { a.confirmWorktree() })
				action.Button(c, "浏览…", action.ButtonProps{Variant: action.Default, Size: theme.Middle}).
					OnClick(func() {
						dirs, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
							Title:     "选择 worktree 目录",
							Directory: true,
							Parent:    a.win,
						})
						if err == nil && len(dirs) > 0 {
							m.wtPath = dirs[0]
						}
					})
			})
			field.Input(c, &m.wtRef, field.InputProps{
				Placeholder: "基于 ref（默认 HEAD）…", Size: theme.Middle,
			}).Grow(1).OnSubmit(func() { a.confirmWorktree() })
			field.Input(c, &m.wtBranch, field.InputProps{
				Placeholder: "新分支名（可空）…", Size: theme.Middle,
			}).Grow(1).OnSubmit(func() { a.confirmWorktree() })
			if m.wtErr != "" {
				feedback.Alert(c, m.wtErr, feedback.AlertProps{Type: feedback.Error, Variant: feedback.Soft})
			}
		})
	}) {
		a.confirmWorktree()
	}
}

func (a *app) confirmWorktree() bool {
	m := &a.branches
	path := strings.TrimSpace(m.wtPath)
	if path == "" {
		m.wtErr = "请填写目录路径"
		return false
	}
	ref := strings.TrimSpace(m.wtRef)
	branch := strings.TrimSpace(m.wtBranch)
	m.wtOpen = false
	m.op(a.commit.RepoPath, a.updater(), "正在创建工作树…", "已添加",
		func(r *git.Repo) (*git.RunResult, error) {
			return r.AddWorktree(context.Background(), path, ref, branch)
		}, func() {
			m.wtErr = ""
		})
	return true
}

// tagDialog collects a new tag's name/target/message (IDEA: New Tag…).
func (a *app) tagDialog(c *ui.Context, t *ui.Theme) {
	m := &a.branches
	confirmed := feedback.Modal(c, &m.tagOpen, feedback.ModalProps{
		Title:  "新建标签",
		Width:  460,
		OkText: "创建",
	}, func() {
		ui.Column(c).Gap(10).Children(func() {
			if m.tagErr != "" {
				feedback.Alert(c, m.tagErr, feedback.AlertProps{Type: feedback.Error, Variant: feedback.Soft})
			}
			field.Input(c, &m.tagName, field.InputProps{Placeholder: "标签名（如 v1.0）"})
			field.Input(c, &m.tagTarget, field.InputProps{Placeholder: "目标提交（默认 HEAD）"})
			field.Input(c, &m.tagMessage, field.InputProps{Placeholder: "附注信息（留空 = 轻量标签）"})
			choice.Checkbox(c, &m.tagCheckout, "创建后检出", choice.CheckboxProps{Size: 14})
		})
	})
	if confirmed {
		a.createTag()
	}
}

// gotoDialogView is IDEA's "Go to Hash/Branch/Tag".
func (a *app) gotoDialogView(c *ui.Context, t *ui.Theme) {
	m := &a.history
	confirmed := feedback.Modal(c, &m.gotoOpen, feedback.ModalProps{
		Title:  "跳转到提交",
		Width:  420,
		OkText: "跳转",
	}, func() {
		ui.Column(c).Gap(8).Children(func() {
			if m.gotoErr != "" {
				feedback.Alert(c, m.gotoErr, feedback.AlertProps{Type: feedback.Error, Variant: feedback.Soft})
			}
			ui.Text(c, "提交哈希、分支名或标签名：").FontSize(12).TextColor(t.TextMuted)
			field.Input(c, &m.gotoInput, field.InputProps{
				Placeholder: "如 abc1234 / main / v1.0",
				Size:        theme.Middle,
			}).Grow(1).OnSubmit(func() {
				m.gotoCommit(a.commit.RepoPath, strings.TrimSpace(m.gotoInput), a.updater())
			})
		})
	})
	if confirmed {
		m.gotoCommit(a.commit.RepoPath, strings.TrimSpace(m.gotoInput), a.updater())
	}
}

// exportWorktreePatch writes one file's working-tree diff to a .patch.

// --- 分支/变基弹窗 -----------------------------------------------------

// branchDialog collects a branch name or rebase target.
func (a *app) branchDialog(c *ui.Context, t *ui.Theme) {
	title := "新建分支"
	okText := "创建"
	hint := "从 " + components.ShortOID(a.newBranchFrom) + " 创建分支"
	switch a.branchDialogKind {
	case 1:
		title = "变基到目标"
		okText = "变基"
		hint = "目标分支或提交"
	case 2:
		hint = "创建并切换到新分支"
	}
	if feedback.Modal(c, &a.branchDialogOpen, feedback.ModalProps{
		Title: title, Width: 420, OkText: okText,
	}, func() {
		ui.Column(c).Gap(8).Children(func() {
			ui.Text(c, hint).FontSize(12).TextColor(t.TextMuted)
			field.Input(c, &a.branchInput, field.InputProps{
				Placeholder: "分支名…",
				Size:        theme.Middle,
			}).Grow(1).OnSubmit(func() { a.confirmBranchDialog() })
		})
	}) {
		a.confirmBranchDialog()
	}
}

// confirmBranchDialog runs the dialog's action.
func (a *app) confirmBranchDialog() bool {
	name := strings.TrimSpace(a.branchInput)
	if name == "" {
		return false
	}
	switch a.branchDialogKind {
	case 1: // rebase
		a.commit.rebase(appCTX, a.updater(), name)
	case 2: // create + checkout (IDEA: New Branch in the branches popup)
		a.branches.newName = name
		a.createBranch()
	default: // new branch from commit
		from := a.newBranchFrom
		a.commit.runOp(appCTX, a.updater(), "正在创建分支…", "已创建",
			func(ctx context.Context, r *git.Repo) (*git.RunResult, error) {
				return r.RunOK(ctx, "branch", name, from)
			}, func() {
				a.branches.lastLoaded = "" // branch list reloads
			})
	}
	a.branchInput = ""
	a.branchDialogOpen = false
	return true
}
