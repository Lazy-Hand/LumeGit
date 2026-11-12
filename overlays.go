package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/lobehub/lobe-mygo/pkg/action"
	"github.com/lobehub/lobe-mygo/pkg/display"
	"github.com/lobehub/lobe-mygo/pkg/feedback"
	"github.com/lobehub/lobe-mygo/pkg/field"
	"github.com/lobehub/lobe-mygo/pkg/layout"
	"github.com/lobehub/lobe-mygo/pkg/theme"

	"lumegit/components"
	"lumegit/internal/git"
)

func (a *app) openWorktreeCompare(path, ref string) {
	a.cmpDiffState = nil
	a.compare.openForWorktree(a.commit.RepoPath, ref, path, a.updater())
}

// bisectOverlay renders IDEA's Bisect panel: start form when inactive,
// good/bad/skip/reset controls plus the current candidate when running.
func (a *app) bisectOverlay(c *ui.Context, t *ui.Theme) {
	m := &a.commit
	feedback.Modal(c, &m.bisectOpen, feedback.ModalProps{
		Title:    "Bisect — 二分查找",
		Width:    560,
		NoFooter: true,
	}, func() {
		ui.Column(c).Gap(12).MinHeight(200).Children(func() {
			if m.bisectErr != "" {
				feedback.Alert(c, m.bisectErr, feedback.AlertProps{Type: feedback.Error, Variant: feedback.Soft})
			}
			st := m.bisect
			if st == nil || !st.Active {
				// Start form.
				ui.Text(c, "标记一个「好」提交和一个「坏」提交，Git 会检出中间点让你逐一验证。").
					FontSize(12).TextColor(t.TextMuted)
				field.Input(c, &m.bisectGood, field.InputProps{
					Placeholder: "好提交（如 v1.0 或 abc1234）…", Size: theme.Middle,
				}).Grow(1)
				field.Input(c, &m.bisectBad, field.InputProps{
					Placeholder: "坏提交（默认 HEAD）…", Size: theme.Middle,
				}).Grow(1)
				ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
					action.Button(c, "开始", action.ButtonProps{Variant: action.Primary, Size: theme.Middle}).
						Disabled(m.bisectBusy || strings.TrimSpace(m.bisectGood) == "").
						OnClick(func() {
							good := strings.TrimSpace(m.bisectGood)
							bad := strings.TrimSpace(m.bisectBad)
							if bad == "" {
								bad = "HEAD"
							}
							m.bisectOp(appCTX, a.updater(), func(ctx context.Context, r *git.Repo) (*git.RunResult, error) {
								return r.BisectStart(ctx, good, bad)
							})
						})
				})
				return
			}
			// Active session.
			if st.Done {
				feedback.Alert(c, "已定位首个坏提交："+st.FirstBadOID,
					feedback.AlertProps{Type: feedback.Success, Variant: feedback.Soft})
			} else {
				ui.Column(c).Gap(4).Children(func() {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "当前候选").Bold()
						display.Tag(c, components.ShortOID(st.CurrentOID), display.TagProps{Variant: display.TagOutlined, Color: display.TagInfo})
					})
					ui.Text(c, st.CurrentSub).FontSize(12).SingleLine()
					ui.Text(c, fmt.Sprintf("已标记 %d 步", st.Steps)).FontSize(11).TextColor(t.TextMuted)
				})
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if !st.Done {
					action.Button(c, "好", action.ButtonProps{Variant: action.Default, Size: theme.Middle}).
						Disabled(m.bisectBusy).OnClick(func() {
						m.bisectOp(appCTX, a.updater(), func(ctx context.Context, r *git.Repo) (*git.RunResult, error) {
							return r.BisectMark(ctx, true)
						})
					})
					action.Button(c, "坏", action.ButtonProps{Variant: action.Default, Size: theme.Middle}).
						Disabled(m.bisectBusy).OnClick(func() {
						m.bisectOp(appCTX, a.updater(), func(ctx context.Context, r *git.Repo) (*git.RunResult, error) {
							return r.BisectMark(ctx, false)
						})
					})
					action.Button(c, "跳过", action.ButtonProps{Variant: action.Text, Size: theme.Middle}).
						Disabled(m.bisectBusy).OnClick(func() {
						m.bisectOp(appCTX, a.updater(), func(ctx context.Context, r *git.Repo) (*git.RunResult, error) {
							return r.BisectSkip(ctx)
						})
					})
				}
				action.Button(c, "结束", action.ButtonProps{Variant: action.Text, Size: theme.Middle}).
					Disabled(m.bisectBusy).OnClick(func() {
					m.bisectOp(appCTX, a.updater(), func(ctx context.Context, r *git.Repo) (*git.RunResult, error) {
						return r.BisectReset(ctx)
					})
				})
			})
		})
	})
}

// lfsLocksOverlay lists the repo's LFS locks (IDEA: Git → LFS → Locks).
func (a *app) lfsLocksOverlay(c *ui.Context, t *ui.Theme) {
	m := &a.commit
	feedback.Modal(c, &m.lfsLocksOpen, feedback.ModalProps{
		Title:    "LFS Locks",
		Width:    640,
		NoFooter: true,
	}, func() {
		ui.Column(c).Gap(10).MinHeight(200).Children(func() {
			ui.Row(c).FillWidth().Children(func() {
				ui.Text(c, fmt.Sprintf("%d 个锁", len(m.lfsLocks))).Grow(1)
				action.Button(c, "刷新", action.ButtonProps{Variant: action.Text, Size: theme.Small}).
					OnClick(func() {
						m.lfsLocksLoad = true
						m.loadLFS(a.updater())
					})
			})
			if len(m.lfsLocks) == 0 {
				components.Placeholder(c, t, "没有 LFS 锁。")
				return
			}
			layout.ScrollArea(c, layout.ScrollAreaProps{}, func() {
				ui.Column(c).FillWidth().Children(func() {
					for i := range m.lfsLocks {
						lk := &m.lfsLocks[i]
						ui.Row(c).Key("lfs-"+lk.ID).FillWidth().Padding(6, 10).Gap(10).AlignItems(ui.Center).Children(func() {
							ui.Column(c).Grow(1).Gap(1).Children(func() {
								ui.Text(c, lk.Path).SingleLine()
								ui.Text(c, lk.Owner.Name+" · "+lk.LockedAt).FontSize(11).TextColor(t.TextMuted)
							})
							action.Button(c, "解锁", action.ButtonProps{Variant: action.Text, Size: theme.Small}).
								OnClick(func() {
									a.lfsUnlock(lk.Path)
								})
						})
					}
				})
			}).Grow(1)
		})
	})
}

// lfsUnlock releases one LFS lock then reloads the list.
func (a *app) lfsUnlock(path string) {
	m := &a.commit
	repoPath := m.RepoPath
	go func() {
		repo := git.NewRepo(repoPath)
		res, err := repo.LFSUnlock(context.Background(), path, false)
		locks, _ := repo.LFSLocks(context.Background())
		a.update(func() {
			if err == nil && res != nil && res.ExitOK() {
				m.lfsLocks = locks
				a.notify("success", "已解锁 "+path)
				return
			}
			msg := ""
			if err != nil {
				msg = err.Error()
			} else if res != nil {
				msg = res.OutputOfStderr()
			}
			a.notify("error", "解锁失败："+msg)
		})
	}()
}

// stashBranchDialog collects the branch name for `git stash branch`.
func (a *app) stashBranchDialog(c *ui.Context, t *ui.Theme) {
	if feedback.Modal(c, &a.stashBranchOpen, feedback.ModalProps{
		Title: "从 " + a.stashBranchFrom + " 建分支", Width: 420, OkText: "创建",
	}, func() {
		ui.Column(c).Gap(8).Children(func() {
			ui.Text(c, "在 stash 的基底提交上新建分支并应用 stash").FontSize(12).TextColor(t.TextMuted)
			field.Input(c, &a.stashBranchName, field.InputProps{
				Placeholder: "新分支名…", Size: theme.Middle,
			}).Grow(1).OnSubmit(func() { a.confirmStashBranch() })
		})
	}) {
		a.confirmStashBranch()
	}
}

func (a *app) confirmStashBranch() bool {
	name := strings.TrimSpace(a.stashBranchName)
	if name == "" {
		return false
	}
	stashName := a.stashBranchFrom
	a.stashBranchOpen = false
	a.shelf.op(a.commit.RepoPath, a.updater(), "正在建分支…", "已建分支",
		func(r *git.Repo) error {
			res, err := r.StashBranch(context.Background(), name, stashName)
			if err != nil {
				return err
			}
			if !res.ExitOK() {
				return &git.GitError{Args: res.Args, Stderr: res.OutputOfStderr()}
			}
			return nil
		}, func() {
			a.stashBranchName = ""
			a.commit.refresh(appCTX, a.updater())
		})
	return true
}

// compareOverlay is the branch/worktree compare modal. Single and
// two-ref modes share one shape (IDEA "Compare with Current" / "Compare
// with 'xxx'"): unique-commit lists on top when a BranchCompare is
// loaded, the file diff list under them, diff on the right. Worktree
// mode skips the commit lists — there's nothing to merge-base against.
func (a *app) compareOverlay(c *ui.Context, t *ui.Theme) {
	m := &a.compare
	title := "比较 — " + m.ref
	switch {
	case m.worktreeMode:
		title = "与工作区比较 — " + m.ref
	case m.refB != "":
		title = "比较 — " + m.ref + " ↔ " + m.refB
	}
	feedback.Modal(c, &m.open, feedback.ModalProps{
		Title:    title,
		Width:    1100,
		NoFooter: true,
	}, func() {
		ui.Row(c).Fill().MinHeight(520).MaxHeight(640).Children(func() {
			// Left pane: unique commits on top (bounded — a long list
			// used to stretch the modal past the window), files below.
			ui.Column(c).Width(340).Fill().Children(func() {
				if m.cmp != nil {
					ui.Column(c).FillWidth().MaxHeight(170).Children(func() {
						layout.ScrollArea(c, layout.ScrollAreaProps{}, func() {
							a.branchCompareSides(c, t)
						})
					})
				}
				switch {
				case m.loading:
					components.Placeholder(c, t, "正在加载文件…")
				case m.err != "":
					feedback.Alert(c, m.err, feedback.AlertProps{Type: feedback.Error, Variant: feedback.Soft})
				case len(m.files) == 0 && m.selected == "":
					components.Placeholder(c, t, "没有差异文件。")
				case len(m.files) == 0:
					// Single-file mode: show the file itself.
					a.compareFileRow(c, t, git.CommitFile{Path: m.selected, Status: 'M'})
				default:
					ui.Text(c, "差异文件").FontSize(11).Bold().TextColor(t.TextMuted).
						Padding(4, 0)
					layout.ScrollArea(c, layout.ScrollAreaProps{}, func() {
						ui.Column(c).FillWidth().Children(func() {
							for i := range m.files {
								a.compareFileRow(c, t, m.files[i])
							}
						})
					}).Grow(1).MinHeight(120)
				}
			})
			// Right: diff. MinWidth keeps the pane from collapsing into
			// a sliver when the commit lists are long; MinHeight gives
			// the Modal's unconstrained height a floor to grow into.
			ui.Column(c).Grow(1).MinWidth(300).Fill().MinHeight(440).Padding(0, 0, 0, 12).Children(func() {
				switch {
				case m.selected == "":
					components.Placeholder(c, t, "选择文件查看差异。")
				case m.diffLoad:
					components.Placeholder(c, t, "正在加载差异…")
				case m.diffErr != "":
					feedback.Alert(c, m.diffErr, feedback.AlertProps{Type: feedback.Error, Variant: feedback.Soft})
				case m.diff == nil:
					components.Placeholder(c, t, "无差异。")
				case m.diff.Binary:
					components.Placeholder(c, t, "二进制文件。")
				case string(m.diff.Old) == string(m.diff.New):
					// The file row appeared under a merge-base diff but
					// both tips hold identical content — say so rather
					// than paint an all-context diff that reads as blank.
					components.Placeholder(c, t, "两端内容相同。")
				default:
					a.cmpDiffView(c, t)
				}
			})
		})
	})
}

// branchCompareDialog collects the second ref for "与 'xxx' 比较…".
func (a *app) branchCompareDialog(c *ui.Context, t *ui.Theme) {
	if feedback.Modal(c, &a.branchCmpOpen, feedback.ModalProps{
		Title: "与 '" + a.branchCmpFrom + "' 比较", Width: 420, OkText: "比较",
	}, func() {
		ui.Column(c).Gap(8).Children(func() {
			ui.Text(c, "选择另一个分支/tag/commit 进行对比").FontSize(12).TextColor(t.TextMuted)
			field.Input(c, &a.branchCmpInput, field.InputProps{
				Placeholder: "分支/tag/commit…",
				Size:        theme.Middle,
			}).Grow(1).OnSubmit(func() { a.confirmBranchCompare() })
		})
	}) {
		a.confirmBranchCompare()
	}
}

func (a *app) confirmBranchCompare() bool {
	other := strings.TrimSpace(a.branchCmpInput)
	if other == "" {
		return false
	}
	a.cmpDiffState = nil
	a.compare.openForBranches(a.commit.RepoPath, a.branchCmpFrom, other, a.updater())
	a.branchCmpInput = ""
	a.branchCmpOpen = false
	return true
}

// branchCompareSides renders the unique-commits lists (IDEA's
// "commits in A not in B" panels). Two-ref mode labels them with both
// refs; single mode labels the second side "当前分支" (HEAD).
func (a *app) branchCompareSides(c *ui.Context, t *ui.Theme) {
	m := &a.compare
	if m.cmp == nil {
		return
	}
	other := m.refB
	if other == "" {
		other = "当前分支"
	}
	// CompareBranches is always called with a=HEAD-equivalent side
	// first: single mode CompareBranches(HEAD, ref) puts ref's unique
	// commits in BOnly; two-ref openForBranches(a, b) puts a's in AOnly.
	ours, theirs := m.cmp.AOnly, m.cmp.BOnly
	if m.refB == "" {
		ours, theirs = m.cmp.BOnly, m.cmp.AOnly
	}
	ui.Column(c).FillWidth().Gap(2).Children(func() {
		ui.Text(c, "独有提交").FontSize(11).Bold().TextColor(t.TextMuted).Padding(4, 0)
		components.CommitSideList(c, t, m.ref+" 独有", ours)
		components.CommitSideList(c, t, other+" 独有", theirs)
		if m.cmp.Unrelated {
			feedback.Alert(c, "两个分支没有共同祖先", feedback.AlertProps{Type: feedback.Warning, Variant: feedback.Soft})
		}
	})
}

// compareFileRow: one changed file in the compare list.
func (a *app) compareFileRow(c *ui.Context, t *ui.Theme, f git.CommitFile) {
	m := &a.compare
	sel := m.selected == f.Path
	label := f.Path
	if f.OrigPath != "" {
		label = f.OrigPath + " → " + f.Path
	}
	ui.Row(c).Key("cmp-"+f.Path).FillWidth().Padding(6, 14).Gap(8).AlignItems(ui.Center).
		Background(components.RowBg(t, sel)).
		OnClick(func() {
			if m.selected != f.Path || m.diff == nil || m.diffLoad || m.diffErr != "" {
				a.cmpDiffState = nil
			}
			m.selectFile(a.commit.RepoPath, f.Path, a.updater())
		}).
		Children(func() {
			display.Tag(c, components.StatusGlyph(f.StatusKind()), display.TagProps{
				Variant: display.TagBorderless,
				Color:   components.StatusTagColor(f.StatusKind()),
			})
			ui.Text(c, label).FontSize(12).SingleLine()
		})
}

// cmpDiffView renders the compare diff with its own state.
func (a *app) cmpDiffView(c *ui.Context, t *ui.Theme) {
	d := a.compare.diff
	if a.cmpDiffState == nil {
		a.cmpDiffState = newDiffState(d)
	}
	a.cmpDiffState.Layout = editor.DiffUnified
	// Height (not Grow): inside the Modal's own scroll a Grow child is
	// unconstrained — the diff must own the pane's fixed height so its
	// top stays pinned while the file list scrolls on the left.
	editor.DiffView(c, a.cmpDiffState).Fill().MinHeight(440)
}

// amendDialog edits the HEAD commit's message (IDEA: "编辑提交消息…").
func (a *app) amendDialog(c *ui.Context, t *ui.Theme) {
	confirmed := feedback.Modal(c, &a.amendOpen, feedback.ModalProps{
		Title:  "编辑提交消息",
		Width:  480,
		OkText: "确定",
	}, func() {
		ui.Column(c).Gap(8).Children(func() {
			ui.Text(c, "修改最近一条提交的提交信息").FontSize(12).TextColor(t.TextMuted)
			field.TextArea(c, &a.amendInput, field.TextAreaProps{
				Placeholder: "提交信息…",
				MinRows:     3,
				MaxRows:     8,
			}).Grow(1)
		})
	})
	if confirmed && strings.TrimSpace(a.amendInput) != "" {
		msg := a.amendInput
		a.amendOpen = false
		a.commit.runOp(appCTX, a.updater(), "正在修改…", "已修改提交信息",
			func(ctx context.Context, r *git.Repo) (*git.RunResult, error) {
				return r.AmendMessage(ctx, msg)
			}, func() {
				a.history.lastLoadedRepo = "" // subject changed — reload log
			})
	}
}

// svnNewBranchDialog creates an svn branch: server-side copy of the
// current URL into branches/<name>. It's the svn counterpart of git's
// "新建分支", minus checkout — the user switches explicitly after.
func (a *app) svnNewBranchDialog(c *ui.Context, t *ui.Theme) {
	m := &a.branches
	confirmed := feedback.Modal(c, &a.svnNewBranchOpen, feedback.ModalProps{
		Title:  "新建分支（svn copy）",
		Width:  460,
		OkText: "创建",
	}, func() {
		ui.Column(c).Gap(8).Children(func() {
			desc := "把当前 URL 复制为 branches/<名称>（服务器端复制，不切换）"
			if a.svnNewBranchRev > 0 {
				desc = fmt.Sprintf("把当前 URL 在 r%d 的状态复制为 branches/<名称>（服务器端复制，不切换）", a.svnNewBranchRev)
			}
			ui.Text(c, desc).
				FontSize(12).TextColor(t.TextMuted)
			field.Input(c, &a.svnNewBranchName, field.InputProps{
				Placeholder: "分支名…",
			})
			if a.svnNewBranchErr != "" {
				ui.Text(c, a.svnNewBranchErr).FontSize(11).TextColor(t.Danger)
			}
		})
	})
	if confirmed {
		name := strings.TrimSpace(a.svnNewBranchName)
		if name == "" {
			a.svnNewBranchErr = "分支名不能为空"
			return
		}
		a.svnNewBranchErr = ""
		a.svnNewBranchOpen = false
		rev := a.svnNewBranchRev
		a.svnNewBranchRev = 0 // one-shot — later opens copy HEAD again
		m.svnNewBranch(name, "new branch "+name, rev, a.updater(), func(errStr string) {
			if errStr != "" {
				a.svnNewBranchErr = errStr
				a.svnNewBranchOpen = true
				return
			}
			m.lastLoaded = "" // refresh the popup's branch list
		})
	}
}

// svnIgnoreDialog lists every directory's svn:ignore patterns — the
// thing a .gitignore file would show on git. Each pattern row can be
// removed; new rules come from a file row's "加入忽略" menu item.
func (a *app) svnIgnoreDialog(c *ui.Context, t *ui.Theme) {
	m := &a.commit
	feedback.Modal(c, &m.ignoreOpen, feedback.ModalProps{
		Title:    "忽略规则（svn:ignore）",
		Width:    560,
		NoFooter: true,
	}, func() {
		ui.Column(c).Gap(8).MinHeight(200).Children(func() {
			ui.Text(c, "svn 把忽略规则存在每个目录的 svn:ignore 属性上 —— 没有 .gitignore 文件。").
				FontSize(11).TextColor(t.TextMuted)
			switch {
			case m.ignoreLoading:
				components.Placeholder(c, t, "正在读取忽略规则…")
			case m.ignoreErr != "":
				feedback.Alert(c, m.ignoreErr, feedback.AlertProps{Type: feedback.Error, Variant: feedback.Soft})
			case len(m.ignores) == 0:
				components.Placeholder(c, t, "没有忽略规则。右键未版本化文件 → 加入忽略。")
			default:
				layout.ScrollArea(c, layout.ScrollAreaProps{}, func() {
					ui.Column(c).FillWidth().Children(func() {
						for i := range m.ignores {
							e := &m.ignores[i]
							dir := e.Dir
							if dir == "" {
								dir = "（根目录）"
							}
							ui.Text(c, dir).Bold().FontSize(12)
							for _, p := range e.Patterns {
								pat := p
								ui.Row(c).Key("ig-"+e.Dir+":"+pat).FillWidth().Padding(2, 16).Gap(8).AlignItems(ui.Center).
									Children(func() {
										ui.Text(c, pat).SingleLine().Grow(1)
										action.Button(c, "移除", action.ButtonProps{Variant: action.Text, Size: theme.Small}).
											OnClick(func() {
												m.svnIgnoreRemove(appCTX, a.updater(), e.Dir, pat)
											})
									})
							}
						}
					})
				}).Grow(1)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				action.Button(c, "刷新", action.ButtonProps{Variant: action.Text, Size: theme.Small}).
					OnClick(func() { m.svnLoadIgnores(appCTX, a.updater()) })
				action.Button(c, "关闭", action.ButtonProps{Variant: action.Default, Size: theme.Middle}).
					OnClick(func() { m.ignoreOpen = false })
			})
		})
	})
}

// pushUpToDialog confirms "推送此前所有提交…": pushes the current
// branch's commits up to (and including) a chosen commit.
func (a *app) pushUpToDialog(c *ui.Context, t *ui.Theme) {
	confirmed := feedback.Modal(c, &a.pushUpToOpen, feedback.ModalProps{
		Title:  "推送此前所有提交",
		Width:  440,
		OkText: "推送",
	}, func() {
		ui.Column(c).Gap(8).Children(func() {
			ui.Text(c, "把当前分支在 "+a.pushUpToShort+" 及之前的所有提交推送到远端").
				FontSize(12).TextColor(t.TextMuted)
		})
	})
	if confirmed {
		oid := a.pushUpToOID
		a.pushUpToOpen = false
		a.commit.pushUpTo(appCTX, a.updater(), oid)
	}
}

// renameDialog edits a local branch's name (IDEA: branch → Rename).
func (a *app) renameDialog(c *ui.Context, t *ui.Theme) {
	m := &a.branches
	confirmed := feedback.Modal(c, &m.renameOpen, feedback.ModalProps{
		Title:  "重命名分支 — " + m.renameOld,
		Width:  420,
		OkText: "重命名",
	}, func() {
		ui.Column(c).Gap(8).Children(func() {
			field.Input(c, &m.renameNew, field.InputProps{
				Placeholder: "新的分支名…",
				Size:        theme.Middle,
			}).Grow(1).OnSubmit(func() {
				a.doRenameBranch()
			})
		})
	})
	if confirmed {
		a.doRenameBranch()
	}
}

// doRenameBranch runs git branch -m.
func (a *app) doRenameBranch() {
	m := &a.branches
	newName := strings.TrimSpace(m.renameNew)
	if newName == "" || newName == m.renameOld {
		return
	}
	old := m.renameOld
	m.op(a.commit.RepoPath, a.updater(), "正在重命名…", "已重命名",
		func(r *git.Repo) (*git.RunResult, error) {
			return r.RenameBranch(context.Background(), old, newName)
		},
		func() {
			m.renameOpen = false
			a.commit.refresh(appCTX, a.updater()) // current branch may have been renamed
		})
}

// remoteDialog is IDEA's "Git Remotes" manager: list + add/edit/delete.
func (a *app) remoteDialog(c *ui.Context, t *ui.Theme) {
	m := &a.branches
	feedback.Modal(c, &m.remoteOpen, feedback.ModalProps{
		Title:    "Git 远程仓库",
		Width:    620,
		NoFooter: true,
	}, func() {
		ui.Column(c).Gap(10).Children(func() {
			if m.remoteErr != "" {
				feedback.Alert(c, m.remoteErr, feedback.AlertProps{Type: feedback.Error, Variant: feedback.Soft})
			}
			ui.Column(c).Border(1, theme.TokensOf(c).BorderSecondary).Radius(6).Children(func() {
				layout.ScrollArea(c, layout.ScrollAreaProps{}, func() {
					ui.Column(c).FillWidth().Children(func() {
						if len(m.remoteList) == 0 {
							ui.Row(c).Padding(10, 14).Children(func() {
								ui.Text(c, "没有配置远程仓库。").FontSize(12).TextColor(t.TextMuted)
							})
						}
						for i := range m.remoteList {
							a.remoteRow(c, t, &m.remoteList[i])
						}
					})
				}).Grow(1).MinHeight(120).MaxHeight(240)
			})
			// Add / edit form.
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				field.Input(c, &m.remoteName, field.InputProps{
					Placeholder: "名称（如 origin）", Size: theme.Small,
				}).Width(140).Disabled(m.remoteEdit != "")
				field.Input(c, &m.remoteURL, field.InputProps{
					Placeholder: "URL…", Size: theme.Small,
				}).Grow(1)
				label := "添加"
				if m.remoteEdit != "" {
					label = "保存"
				}
				action.Button(c, label, action.ButtonProps{Variant: action.Primary, Size: theme.Small}).
					OnClick(func() { a.saveRemote() })
				if m.remoteEdit != "" {
					action.Button(c, "取消", action.ButtonProps{Variant: action.Text, Size: theme.Small}).
						OnClick(func() {
							m.remoteEdit = ""
							m.remoteName, m.remoteURL = "", ""
						})
				}
			})
			ui.Row(c).Justify(ui.End).Children(func() {
				action.Button(c, "关闭", action.ButtonProps{Variant: action.Default, Size: theme.Small}).
					OnClick(func() { m.remoteOpen = false })
			})
		})
	})
}

// remoteRow: one remote + edit/delete actions.
func (a *app) remoteRow(c *ui.Context, t *ui.Theme, r *git.Remote) {
	m := &a.branches
	ui.Row(c).FillWidth().Padding(6, 14).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Gap(1).Children(func() {
			ui.Text(c, r.Name).Bold()
			ui.Text(c, r.FetchURL).FontSize(11).TextColor(t.TextMuted).SingleLine()
		})
		action.Button(c, "编辑", action.ButtonProps{Variant: action.Text, Size: theme.Small}).
			OnClick(func() {
				m.remoteEdit = r.Name
				m.remoteName = r.Name
				m.remoteURL = r.FetchURL
			})
		action.Button(c, "删除", action.ButtonProps{Variant: action.Text, Size: theme.Small}).
			OnClick(func() {
				name := r.Name
				m.remoteOp(a.commit.RepoPath, a.updater(),
					func(rp *git.Repo) (*git.RunResult, error) {
						return rp.RemoveRemote(context.Background(), name)
					})
			})
	})
}

// saveRemote adds or updates the form's remote.
func (a *app) saveRemote() {
	m := &a.branches
	url := strings.TrimSpace(m.remoteURL)
	if url == "" {
		m.remoteErr = "请输入 URL"
		return
	}
	if m.remoteEdit != "" {
		name := m.remoteEdit
		m.remoteOp(a.commit.RepoPath, a.updater(),
			func(rp *git.Repo) (*git.RunResult, error) {
				return rp.SetRemoteURL(context.Background(), name, url)
			})
	} else {
		name := strings.TrimSpace(m.remoteName)
		if name == "" {
			m.remoteErr = "请输入名称"
			return
		}
		m.remoteOp(a.commit.RepoPath, a.updater(),
			func(rp *git.Repo) (*git.RunResult, error) {
				return rp.AddRemote(context.Background(), name, url)
			})
	}
	m.remoteEdit = ""
	m.remoteName, m.remoteURL = "", ""
}
