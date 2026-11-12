package main

import (
	"context"
	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/action"
	"github.com/lobehub/lobe-mygo/pkg/display"
	"github.com/lobehub/lobe-mygo/pkg/feedback"
	"github.com/lobehub/lobe-mygo/pkg/field"
	"github.com/lobehub/lobe-mygo/pkg/theme"

	"lumegit/components"
	"lumegit/internal/git"
)

// --- 历史（History）页 -------------------------------------------------

// historyView: commit list | (files | diff) — IDEA's Git Log layout.
func (a *app) historyView(c *ui.Context, t *ui.Theme) {
	m := &a.history
	tk := theme.TokensOf(c)
	// Kick the load once the repo is known; idempotent while loaded.
	m.ensureLoaded(a.commit.RepoPath, a.updater())

	ui.Split(c, &a.histListW,
		func() { a.commitListPane(c, t) },
		func() {
			ui.Split(c, &a.histFilesW,
				func() { a.commitFilesPane(c, t) },
				func() {
					a.renderDiffPane(c, t, diffSource{
						selection: m.selectedFile,
						loading:   m.diffLoading,
						err:       m.diffErr,
						diff:      m.diff,
						emptyText: "选择提交和文件查看变更。",
					})
				},
			).Fill()
		},
	).Fill().Grow(1)
	_ = tk
}

// commitListPane: the scrollable log (refs + subject + author + time).
func (a *app) commitListPane(c *ui.Context, t *ui.Theme) {
	m := &a.history
	tk := theme.TokensOf(c)
	ui.Column(c).Fill().Gap(0).Background(tk.BgContainer).Children(func() {
		ui.Row(c).Padding(8, 12).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "提交").Bold()
			// Spin while log/files/diff is in flight so refresh/filter shows
			// something is happening (IDEA also spins the log toolbar).
			busy := m.loading || m.filesLoading || m.diffLoading
			feedback.Spin(c, feedback.SpinProps{Size: theme.Small, Spinning: &busy}, nil)
			ui.Text(c, "").Grow(1)
			action.Button(c, "跳转…", action.ButtonProps{Variant: action.Text, Size: theme.Small}).
				OnClick(func() {
					m.gotoOpen = true
					m.gotoInput, m.gotoErr = "", ""
				})
			action.Button(c, "刷新", action.ButtonProps{Variant: action.Text, Size: theme.Small}).
				Disabled(m.loading).OnClick(func() {
				m.lastLoadedRepo = ""
				m.ensureLoaded(a.commit.RepoPath, a.updater())
			})
		})
		// IDEA's filter bar: branch / author / message / path.
		ui.Row(c).Padding(4, 12).Gap(6).AlignItems(ui.Center).Children(func() {
			field.Input(c, &m.filterBranch, field.InputProps{
				Placeholder: "分支…", Size: theme.Small,
			}).Width(80).OnSubmit(func() { m.reloadFiltered(a.commit.RepoPath, a.updater()) })
			field.Input(c, &m.filterAuthor, field.InputProps{
				Placeholder: "作者…", Size: theme.Small,
			}).Width(80).OnSubmit(func() { m.reloadFiltered(a.commit.RepoPath, a.updater()) })
			field.Input(c, &m.filterGrep, field.InputProps{
				Placeholder: "消息…", Size: theme.Small,
			}).Grow(1).OnSubmit(func() { m.reloadFiltered(a.commit.RepoPath, a.updater()) })
			field.Input(c, &m.filterPath, field.InputProps{
				Placeholder: "路径…", Size: theme.Small,
			}).Width(80).OnSubmit(func() { m.reloadFiltered(a.commit.RepoPath, a.updater()) })
			action.Button(c, "应用", action.ButtonProps{Variant: action.Text, Size: theme.Small}).
				OnClick(func() { m.reloadFiltered(a.commit.RepoPath, a.updater()) })
			if m.filterActive {
				action.Button(c, "×", action.ButtonProps{Variant: action.Text, Size: theme.Small}).
					OnClick(func() {
						m.filterBranch, m.filterAuthor, m.filterGrep, m.filterPath = "", "", "", ""
						m.filterAll = false
						m.reloadFiltered(a.commit.RepoPath, a.updater())
					})
			}
		})
		if m.filterActive {
			ui.Row(c).Padding(2, 12).Children(func() {
				display.Tag(c, "已启用过滤", display.TagProps{Variant: display.TagBorderless, Color: display.TagInfo})
			})
		}
		switch {
		case m.loading && len(m.commits) == 0:
			components.Placeholder(c, t, "正在加载历史…")
		case m.err != "":
			ui.Column(c).Padding(8, 12).Children(func() {
				feedback.Alert(c, m.err, feedback.AlertProps{Type: feedback.Error, Variant: feedback.Soft})
			})
		case len(m.commits) == 0:
			display.Empty(c, display.EmptyProps{
				Title:       "暂无提交",
				Description: "此分支还没有提交记录",
			}).Padding(24)
		default:
			// ui.List is mygo's virtual list: it builds only the rows in
			// view (plus a few), so 5000-commit histories cost the same
			// as 50. Key = OID keeps selection/scroll stable as pages
			// append below.
			m.listState.Key = func(row int) any {
				if row < len(m.commits) {
					return m.commits[row].OID
				}
				return "loadmore"
			}
			n := len(m.commits)
			if m.hasMore {
				n++ // trailing "load more" row
			}
			ui.List(c, &m.listState, n, func(i int) {
				if i < len(m.commits) {
					var gl *git.GraphLine
					if i < len(m.graph) {
						gl = &m.graph[i]
					}
					a.commitRowView(c, t, &m.commits[i], gl)
					return
				}
				ui.Row(c).FillWidth().Padding(6, 14).Children(func() {
					if m.loading {
						ui.Text(c, "正在加载…").FontSize(11).TextColor(t.TextMuted)
					} else {
						action.Button(c, "加载更多提交…", action.ButtonProps{Variant: action.Text, Size: theme.Small}).
							OnClick(func() { m.loadMore(a.commit.RepoPath, a.updater()) })
					}
				})
			}).Grow(1)
		}
	})
}

// graphColors picks a lane's color (cycles; IDEA uses distinct colors).

// commitRowView: one history entry — graph + subject + refs + author + time.
func (a *app) commitRowView(c *ui.Context, t *ui.Theme, cm *git.Commit, gl *git.GraphLine) {
	sel := a.history.selectedID == cm.OID
	repoPath := a.commit.RepoPath
	oid := cm.OID
	rowEl := ui.Row(c).Key("cmt-"+oid).FillWidth().Padding(7, 14).Gap(10).AlignItems(ui.Center).
		Background(components.RowBg(t, sel))
	rowEl.OnClick(func() {
		if a.history.selectedID != oid || a.history.files == nil {
			a.diffState = nil
		}
		a.history.selectCommit(repoPath, oid, a.updater())
	})
	rowEl.ContextMenu(func(mn *ui.Menu) {
		isSVN := a.commit.isSVN()
		// HEAD can sit below commits[0] when the log is filtered or
		// starts at a merge — only the snapshot's real HeadOID is
		// authoritative for "is this the tip".
		isHead := a.commit.Snap != nil && a.commit.Snap.Git != nil &&
			a.commit.Snap.Git.HeadOID == oid
		if mn.Item("复制修订号").Chosen() {
			c.WriteClipboard(oid)
			a.notify("success", "已复制 "+cm.ShortOID)
		}
		if isSVN {
			// svn rev row: no rebase/cherry-pick/reset — only copy a
			// branch/tag off this rev or update the WC to it.
			mn.Separator()
			if mn.Item("更新到此修订（svn update -r）").Chosen() {
				a.svnUpdateToRev(oid)
			}
			if mn.Item("从此修订新建分支（svn copy）…").Chosen() {
				a.svnNewBranchOpen = true
				a.svnNewBranchName = ""
				a.svnNewBranchErr = ""
				a.svnNewBranchRev = svnRevNum(oid)
			}
			return
		}
		mn.Separator()
		if mn.Item("Cherry-pick 到当前分支").Chosen() {
			a.commit.cherryPick(appCTX, a.updater(), oid)
		}
		if isHead {
			if mn.Item("撤消提交…").Chosen() {
				a.commit.undoHeadCommit(appCTX, a.updater())
			}
		}
		if mn.Item("从此提交交互式变基…").Chosen() {
			a.commit.openRebaseDialog(a.updater(), oid)
		}
		if isHead {
			if mn.Item("编辑提交消息…").Chosen() {
				a.amendOID = oid
				// %B carries subject + body; the row only has the
				// subject, so fetch the full message before the dialog
				// opens — saving the subject alone would drop the body.
				a.amendInput = cm.Subject
				a.amendOpen = true
				gen := a.history.gen
				go func() {
					repo := git.NewRepo(repoPath)
					full, err := repo.CommitMessage(context.Background(), oid)
					a.update(func() {
						if err != nil || gen != a.history.gen || !a.amendOpen || a.amendOID != oid {
							return
						}
						a.amendInput = full
					})
				}()
			}
		} else {
			if mn.Item("编辑提交消息…").Chosen() {
				a.commit.openRebaseDialogPreset(a.updater(), oid+"~1", oid, git.RebaseReword)
			}
			if mn.Item("删除提交").Chosen() {
				a.commit.openRebaseDialogPreset(a.updater(), oid+"~1", oid, git.RebaseDrop)
			}
			if mn.Item("压缩提交…").Chosen() {
				a.commit.openRebaseDialogPreset(a.updater(), oid+"~1", oid, git.RebaseSquash)
			}
		}
		if mn.Item("Revert 此提交").Chosen() {
			a.commit.revertCommit(appCTX, a.updater(), oid)
		}
		mn.Separator()
		if mn.Item("与本地比较").Chosen() {
			a.compare.openForWorktree(repoPath, oid, "", a.updater())
		}
		if mn.Item("从此提交新建分支…").Chosen() {
			a.newBranchFrom = oid
			a.branchDialogKind = 0 // new branch — a leftover rebase kind would reuse the wrong dialog
			a.branchDialogOpen = true
		}
		if mn.Item("新建标签…").Chosen() {
			a.branches.tagOpen = true
			a.branches.tagName = ""
			a.branches.tagTarget = cm.ShortOID
			a.branches.tagMessage = ""
			a.branches.tagErr = ""
		}
		if mn.Item("创建补丁…").Chosen() {
			a.exportCommitPatch(oid, cm.ShortOID)
		}
		mn.Separator()
		if mn.Item("推送此前所有提交…").Chosen() {
			a.pushUpToOID = oid
			a.pushUpToShort = cm.ShortOID
			a.pushUpToOpen = true
		}
		if mn.Item("软重置到此提交").Chosen() {
			a.commit.resetTo(appCTX, a.updater(), oid, git.ResetSoft)
		}
		if mn.Item("混合重置到此提交").Chosen() {
			a.commit.resetTo(appCTX, a.updater(), oid, git.ResetMixed)
		}
		if mn.Item("硬重置到此提交").Chosen() {
			a.askRun("硬重置", "硬重置到 "+cm.ShortOID+" 会丢弃索引和工作区的所有未提交更改，不可恢复。确定继续？", func() {
				a.commit.resetTo(appCTX, a.updater(), oid, git.ResetHard)
			})
		}
		mn.Separator()
		if mn.Item("转到子提交").Chosen() {
			for _, k := range a.history.commits {
				if len(k.Parents) > 0 && k.Parents[0] == oid {
					a.history.scrollToCommit(k.OID, repoPath, a.updater())
					break
				}
			}
		}
		if len(cm.Parents) > 0 && mn.Item("转到父提交").Chosen() {
			a.history.scrollToCommit(cm.Parents[0], repoPath, a.updater())
		}
	})
	rowEl.Children(func() {
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, cm.Subject).SingleLine().Grow(1)
				// Signature badge — IDEA shows GPG verification on
				// signed commits; hide for unsigned (N / "").
				switch cm.Sig {
				case git.SigGood:
					display.Tag(c, "✓ 已签名", display.TagProps{Variant: display.TagBorderless, Color: display.TagSuccess})
				case git.SigBad, git.SigRevoked:
					display.Tag(c, "✗ 签名无效", display.TagProps{Variant: display.TagBorderless, Color: display.TagError})
				case git.SigUnknown, git.SigExpired, git.SigCannot, git.SigError:
					display.Tag(c, "? 签名", display.TagProps{Variant: display.TagBorderless, Color: display.TagWarning})
				}
				for _, ref := range cm.Refs {
					display.Tag(c, ref, display.TagProps{
						Variant: display.TagOutlined,
						Color:   display.TagInfo,
					})
				}
			})
			ui.Row(c).Gap(8).Children(func() {
				ui.Text(c, cm.ShortOID).FontSize(11).TextColor(t.TextMuted)
				ui.Text(c, cm.Author).FontSize(11).TextColor(t.TextMuted)
				ui.Text(c, components.RelTime(cm.When)).FontSize(11).TextColor(t.TextMuted)
				if a.history.unpushed[cm.OID] {
					display.Tag(c, "未推送", display.TagProps{
						Variant: display.TagBorderless,
						Color:   display.TagInfo,
					})
				}
				if cm.Merge() {
					display.Tag(c, "合并", display.TagProps{
						Variant: display.TagBorderless,
						Color:   display.TagWarning,
					})
				}
			})
		})
	})
}

// commitFilesPane: files of the selected commit.
func (a *app) commitFilesPane(c *ui.Context, t *ui.Theme) {
	m := &a.history
	tk := theme.TokensOf(c)
	ui.Column(c).Fill().Gap(0).Background(tk.BgContainer).Children(func() {
		ui.Row(c).Padding(8, 12).Gap(8).Children(func() {
			ui.Text(c, "文件").Bold()
			if m.selectedID != "" {
				ui.Text(c, components.ShortOID(m.selectedID)).FontSize(11).TextColor(t.TextMuted)
			}
		})
		switch {
		case m.selectedID == "":
			components.Placeholder(c, t, "选择提交。")
		case m.filesLoading:
			components.Placeholder(c, t, "正在加载文件…")
		case m.filesErr != "":
			ui.Column(c).Padding(8, 12).Children(func() {
				feedback.Alert(c, m.filesErr, feedback.AlertProps{Type: feedback.Error, Variant: feedback.Soft})
			})
		case len(m.files) == 0:
			components.Placeholder(c, t, "无文件变更。")
		default:
			m.filesList.Key = func(row int) any { return m.files[row].Path }
			ui.List(c, &m.filesList, len(m.files), func(i int) {
				a.commitFileRowView(c, t, &m.files[i])
			}).Grow(1)
		}
	})
}

// commitFileRowView: status tag + path; click loads the diff.
func (a *app) commitFileRowView(c *ui.Context, t *ui.Theme, f *git.CommitFile) {
	sel := a.history.selectedFile == f.Path
	repoPath, oid := a.commit.RepoPath, a.history.selectedID
	file := *f
	label := f.Path
	if f.OrigPath != "" {
		label = f.OrigPath + " → " + f.Path
	}
	row := ui.Row(c).Key("hf-"+f.Path).FillWidth().Padding(6, 14).Gap(10).AlignItems(ui.Center).
		Background(components.RowBg(t, sel))
	row.Children(func() {
		display.Tag(c, components.StatusGlyph(f.StatusKind()), display.TagProps{
			Variant: display.TagBorderless,
			Color:   components.StatusTagColor(f.StatusKind()),
		})
		ui.Text(c, label).SingleLine().
			TextColor(components.RowLabelColor(t, sel)).
			OnClick(func() {
				a.diffState = nil
				a.history.selectCommitFile(repoPath, oid, file, a.updater())
			})
	})
	// IDEA: file in a commit can be compared against the worktree.
	row.ContextMenu(func(mn *ui.Menu) {
		if mn.Item("与工作区比较").Chosen() {
			a.cmpDiffState = nil
			a.openWorktreeCompare(file.Path, oid)
		}
	})
}
