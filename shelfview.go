package main

import (
	"context"
	"fmt"

	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/action"
	"github.com/lobehub/lobe-mygo/pkg/choice"
	"github.com/lobehub/lobe-mygo/pkg/display"
	"github.com/lobehub/lobe-mygo/pkg/feedback"
	"github.com/lobehub/lobe-mygo/pkg/field"
	"github.com/lobehub/lobe-mygo/pkg/layout"
	"github.com/lobehub/lobe-mygo/pkg/theme"

	"lumegit/components"
	"lumegit/internal/git"
)

// --- 搁置（Stash/Shelf）页 ----------------------------------------------

// shelfView: stash list | (files | diff).
func (a *app) shelfView(c *ui.Context, t *ui.Theme) {
	m := &a.shelf
	m.ensureLoaded(a.commit.RepoPath, a.updater())

	ui.Split(c, &a.shelfListW,
		func() { a.stashListPane(c, t) },
		func() {
			ui.Split(c, &a.shelfFileW,
				func() { a.stashFilesPane(c, t) },
				func() {
					a.renderDiffPane(c, t, diffSource{
						selection: m.selectedFile,
						loading:   m.diffLoading,
						err:       m.diffErr,
						diff:      m.diff,
						emptyText: "选择搁置和文件查看变更。",
					})
				},
			).Fill()
		},
	).Fill().Grow(1)
}

// stashListPane: header + new-stash form + stash rows.
func (a *app) stashListPane(c *ui.Context, t *ui.Theme) {
	m := &a.shelf
	tk := theme.TokensOf(c)
	ui.Column(c).Fill().Gap(0).Background(tk.BgContainer).Children(func() {
		ui.Row(c).Padding(8, 12).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "搁置").Bold()
			shelfBusy := m.loading || m.filesLoading || m.diffLoading
			feedback.Spin(c, feedback.SpinProps{Size: theme.Small, Spinning: &shelfBusy}, nil)
			ui.Text(c, "").Grow(1)
			action.Button(c, "刷新", action.ButtonProps{Variant: action.Text, Size: theme.Small}).
				Disabled(m.loading).OnClick(func() {
				m.lastLoaded = ""
				m.ensureLoaded(a.commit.RepoPath, a.updater())
			})
		})
		// New-stash form.
		ui.Column(c).Padding(6, 12).Gap(6).Children(func() {
			field.Input(c, &m.msg, field.InputProps{
				Placeholder: "搁置说明…",
				Size:        theme.Small,
			}).Grow(1)
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				choice.Checkbox(c, &m.untracked, "含未跟踪", choice.CheckboxProps{Size: 12})
				choice.Checkbox(c, &m.keepIndex, "保留已暂存", choice.CheckboxProps{Size: 12})
				ui.Text(c, "").Grow(1)
				action.Button(c, "清空", action.ButtonProps{Variant: action.Text, Size: theme.Small}).
					Disabled(m.busy || len(m.stashes) == 0).OnClick(func() {
					a.askRun("清空 Stash", fmt.Sprintf("删除全部 %d 个 stash？此操作无法撤销。", len(m.stashes)), func() {
						m.op(a.commit.RepoPath, a.updater(), "正在清空…", "已清空",
							func(r *git.Repo) error {
								res, err := r.StashClear(context.Background())
								if err != nil {
									return err
								}
								if !res.ExitOK() {
									return &git.GitError{Args: res.Args, Stderr: res.OutputOfStderr()}
								}
								return nil
							}, nil)
					})
				})
				action.Button(c, "搁置", action.ButtonProps{Variant: action.Primary, Size: theme.Small}).
					Disabled(m.busy).OnClick(func() {
					m.op(a.commit.RepoPath, a.updater(), "正在搁置…", "已搁置",
						func(r *git.Repo) error {
							res, err := r.StashPush(context.Background(), git.StashPushRequest{
								Message:          m.msg,
								IncludeUntracked: m.untracked,
								KeepIndex:        m.keepIndex,
							})
							if err != nil {
								return err
							}
							if !res.ExitOK() {
								return &git.GitError{Args: res.Args, Stderr: res.OutputOfStderr()}
							}
							return nil
						},
						func() {
							m.msg = ""
							// Worktree changed — refresh the commit tab.
							a.commit.refresh(appCTX, a.updater())
						})
				})
			})
		})
		if m.err != "" {
			ui.Column(c).Padding(6, 12).Children(func() {
				feedback.Alert(c, m.err, feedback.AlertProps{Type: feedback.Error, Variant: feedback.Soft})
			})
		}
		if m.syncOK != "" {
			ui.Row(c).Padding(4, 12).Children(func() {
				display.Tag(c, "✓ "+m.syncOK, display.TagProps{Variant: display.TagBorderless, Color: display.TagSuccess})
			})
		}
		switch {
		case m.loading && len(m.stashes) == 0 && len(m.shelf) == 0:
			components.Placeholder(c, t, "正在加载搁置…")
		case len(m.stashes) == 0 && len(m.shelf) == 0:
			display.Empty(c, display.EmptyProps{
				Title:       "暂无搁置",
				Description: "把当前修改搁置起来以便稍后恢复",
			}).Padding(24)
		default:
			layout.ScrollArea(c, layout.ScrollAreaProps{}, func() {
				ui.Column(c).FillWidth().Children(func() {
					if len(m.shelf) > 0 {
						ui.Row(c).FillWidth().Padding(6, 14, 2, 14).Children(func() {
							ui.Text(c, "搁置架").FontSize(11).Bold().TextColor(t.TextMuted)
						})
						for i := range m.shelf {
							a.shelfRowView(c, t, &m.shelf[i])
						}
					}
					if len(m.stashes) > 0 {
						ui.Row(c).FillWidth().Padding(6, 14, 2, 14).Children(func() {
							ui.Text(c, "Stash").FontSize(11).Bold().TextColor(t.TextMuted)
						})
						for i := range m.stashes {
							a.stashRowView(c, t, &m.stashes[i])
						}
					}
				})
			}).Grow(1)
		}
	})
}

// shelfRowView: one patch-shelf entry + unshelve/drop menu.
func (a *app) shelfRowView(c *ui.Context, t *ui.Theme, se *git.ShelfEntry) {
	m := &a.shelf
	sel := m.isShelf && m.selectedID == "shelf:"+se.Name
	repoPath := a.commit.RepoPath
	name := se.Name
	rowEl := ui.Row(c).Key("shelf-"+name).FillWidth().Padding(7, 14).Gap(8).AlignItems(ui.Center).
		Background(components.RowBg(t, sel))
	rowEl.Children(func() {
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			ui.Text(c, "📦 "+name).SingleLine()
			ui.Text(c, se.Created.Format("01-02 15:04")).FontSize(11).TextColor(t.TextMuted)
		})
	})
	rowEl.OnClick(func() {
		m.selectShelfEntry(repoPath, name, a.updater())
	})
	rowEl.ContextMenu(func(mn *ui.Menu) {
		if mn.Item("取消搁置（应用补丁）").Chosen() {
			m.op(repoPath, a.updater(), "正在取消搁置…", "已取消搁置",
				func(r *git.Repo) error {
					res, err := r.Unshelve(context.Background(), name)
					if err != nil {
						return err
					}
					if !res.ExitOK() {
						return &git.GitError{Args: res.Args, Stderr: res.OutputOfStderr()}
					}
					return nil
				},
				func() { a.commit.refresh(appCTX, a.updater()) })
		}
		mn.Separator()
		if mn.Item("删除").Chosen() {
			a.askRun("删除搁置", "删除搁置补丁「"+name+"」？此操作无法撤销。", func() {
				m.op(repoPath, a.updater(), "正在删除…", "已删除",
					func(r *git.Repo) error { return r.DropShelf(context.Background(), name) }, nil)
			})
		}
	})
}

// stashRowView: one stash entry + context menu actions.
func (a *app) stashRowView(c *ui.Context, t *ui.Theme, st *git.Stash) {
	m := &a.shelf
	sel := m.selectedID == st.Name
	repoPath := a.commit.RepoPath
	name := st.Name
	rowEl := ui.Row(c).Key("st-"+name).FillWidth().Padding(7, 14).Gap(8).AlignItems(ui.Center).
		Background(components.RowBg(t, sel))
	rowEl.Children(func() {
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			ui.Text(c, st.Message).SingleLine().Grow(1)
			ui.Row(c).Gap(6).Children(func() {
				ui.Text(c, name).FontSize(11).TextColor(t.TextMuted)
				if st.Branch != "" {
					display.Tag(c, st.Branch, display.TagProps{
						Variant: display.TagBorderless, Color: display.TagInfo,
					})
				}
			})
		})
	})
	rowEl.OnClick(func() {
		a.diffState = nil
		m.selectStash(repoPath, name, a.updater())
	})
	rowEl.ContextMenu(func(mn *ui.Menu) {
		if mn.Item("应用").Chosen() {
			m.op(repoPath, a.updater(), "正在应用…", "已应用",
				func(r *git.Repo) error { _, err := r.StashApply(context.Background(), name); return err },
				func() { a.commit.refresh(appCTX, a.updater()) })
		}
		if mn.Item("弹出").Chosen() {
			m.op(repoPath, a.updater(), "正在弹出…", "已弹出",
				func(r *git.Repo) error { _, err := r.StashPop(context.Background(), name); return err },
				func() { a.commit.refresh(appCTX, a.updater()) })
		}
		mn.Separator()
		if mn.Item("从 stash 建分支…").Chosen() {
			a.newBranchFrom = "" // base = stash's parent commit; branch name asked below
			a.stashBranchFrom = name
			a.stashBranchOpen = true
			a.stashBranchName = ""
		}
		mn.Separator()
		if mn.Item("删除").Chosen() {
			a.askRun("删除 Stash", "删除 "+name+"？此操作无法撤销。", func() {
				m.op(repoPath, a.updater(), "正在删除…", "已删除",
					func(r *git.Repo) error { _, err := r.StashDrop(context.Background(), name); return err }, nil)
			})
		}
	})
}

// stashFilesPane: files inside the selected stash.
func (a *app) stashFilesPane(c *ui.Context, t *ui.Theme) {
	m := &a.shelf
	tk := theme.TokensOf(c)
	ui.Column(c).Fill().Gap(0).Background(tk.BgContainer).Children(func() {
		ui.Row(c).Padding(8, 12).Gap(8).Children(func() {
			ui.Text(c, "文件").Bold()
			if m.selectedID != "" {
				ui.Text(c, m.selectedID).FontSize(11).TextColor(t.TextMuted)
			}
		})
		switch {
		case m.selectedID == "":
			components.Placeholder(c, t, "选择搁置。")
		case m.filesLoading:
			components.Placeholder(c, t, "正在加载文件…")
		case m.filesErr != "":
			ui.Column(c).Padding(8, 12).Children(func() {
				feedback.Alert(c, m.filesErr, feedback.AlertProps{Type: feedback.Error, Variant: feedback.Soft})
			})
		case len(m.files) == 0:
			components.Placeholder(c, t, "无文件变更。")
		default:
			layout.ScrollArea(c, layout.ScrollAreaProps{}, func() {
				ui.Column(c).FillWidth().Children(func() {
					for i := range m.files {
						a.stashFileRowView(c, t, &m.files[i])
					}
				})
			}).Grow(1)
		}
	})
}

// stashFileRowView: status tag + path; click loads the diff.
func (a *app) stashFileRowView(c *ui.Context, t *ui.Theme, f *git.StashFile) {
	m := &a.shelf
	sel := m.selectedFile == f.Path
	repoPath, stashName := a.commit.RepoPath, m.selectedID
	file := *f
	label := f.Path
	if f.OrigPath != "" {
		label = f.OrigPath + " → " + f.Path
	}
	kind := components.StashFileKind(f)
	ui.Row(c).Key("shf-"+f.Path).FillWidth().Padding(6, 14).Gap(10).AlignItems(ui.Center).
		Background(components.RowBg(t, sel)).
		Children(func() {
			display.Tag(c, components.StatusGlyph(kind), display.TagProps{
				Variant: display.TagBorderless, Color: components.StatusTagColor(kind),
			})
			ui.Text(c, label).SingleLine().
				TextColor(components.RowLabelColor(t, sel)).
				OnClick(func() {
					a.diffState = nil
					m.selectFile(repoPath, stashName, file, a.updater())
				})
		})
}

// stashFileKind maps a stash file's status to the shared FileKind set.
