package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/lobehub/lobe-mygo/pkg/action"
	"github.com/lobehub/lobe-mygo/pkg/choice"
	"github.com/lobehub/lobe-mygo/pkg/display"
	"github.com/lobehub/lobe-mygo/pkg/feedback"
	"github.com/lobehub/lobe-mygo/pkg/field"
	"github.com/lobehub/lobe-mygo/pkg/layout"
	"github.com/lobehub/lobe-mygo/pkg/theme"

	"lumegit/internal/git"
	"lumegit/internal/store"
)

// appCTX is the shared background context for git work. Set to a
// cancellable context in main; the app struct reads it only.
var appCTX context.Context = context.Background()

// app is the window-visible application state (fields shared with
// main.go, which owns the type).
type app struct {
	commit    commitModel
	history   historyModel
	shelf     shelfModel
	branches  branchModel
	fileHist  fileHistModel
	conflict  conflictModel
	conflicts conflictsModel
	reflog    reflogModel
	compare   compareModel

	// toasts queues notifications between a notify() call (which may run
	// outside a view pass) and the next frame, where they are handed to
	// ui's toast system.
	toasts    []toast
	winUpdate func(fn func()) // win.Update; nil in headless tests
	win       *mygo.Window    // used for dialog parenting; nil in tests

	// Layout persistence.
	filesPaneW  float32
	histListW   float32 // history: commit-list pane width
	histFilesW  float32 // history: file-list pane width
	shelfListW  float32 // shelf: stash-list pane width
	shelfFileW  float32 // shelf: file-list pane width
	// (branchListW removed: the branches popup replaced the list pane)
	diffSplit   float32
	diffMode    int // 0 = split, 1 = unified

	// Main tab: 0=提交, 1=历史, 2=搁置.
	tab int

	// Repo picker field.
	pickPath string

	// Branch dialog (create from commit / rebase onto).
	branchDialogOpen bool
	newBranchFrom    string // OID to branch from, "" = HEAD
	branchCmpOpen    bool   // "与其他分支比较…" dialog
	branchCmpFrom    string // the ref picked first
	branchCmpInput   string // second ref input

	// "从 stash 建分支" dialog (IDEA: stash right-click → Branch).
	stashBranchOpen  bool
	stashBranchFrom  string // stash name, e.g. stash@{0}
	stashBranchName  string // new branch name input
	branchInput      string // text field for the new name / rebase target
	branchDialogKind int    // 0 = new branch, 1 = rebase onto

	// Amend-commit-message dialog (IDEA: "编辑提交消息…" on HEAD).
	amendOpen  bool
	amendOID   string // commit being amended (must be HEAD)
	amendInput string // new message

	// "推送此前所有提交…" dialog (IDEA: Push up to here).
	pushUpToOpen bool
	pushUpToOID  string // last commit to include
	pushUpToShort string // display-only short OID

	// Diff state for the current file, rebuilt per selection.
	diffState    *editor.DiffState
	fhDiffState  *editor.DiffState // separate state for the file-history overlay
	cmpDiffState *editor.DiffState // separate state for the compare overlay

	// fileScroll tracks the commit file list's scroll offset so tests
	// (and future "scroll to top" actions) can read and drive it.
	fileScroll ui.ScrollState
}

// updater is the model's publication channel, plus toasts.
func (a *app) updater() updater {
	return updater{update: a.commit.publish, notify: a.notify}
}

// update runs fn on the UI thread: win.Update in the app, synchronous
// in headless tests.
func (a *app) update(fn func()) {
	if a.winUpdate != nil {
		a.winUpdate(fn)
		return
	}
	fn()
}

// view renders the whole window each frame.
func (a *app) view(c *ui.Context) {
	theme.Apply(c) // Lobe UI tokens + theme must be set before any component runs.
	t := c.Theme()
	if a.commit.RepoPath == "" {
		a.pickView(c, t)
		return
	}
	a.commitView(c, t)
	// File-history overlay on top of everything.
	if a.fileHist.open {
		a.fileHistOverlay(c, t)
	}
	// Conflict resolver on top of that.
	if a.conflict.open {
		a.conflictOverlay(c, t)
	}
	// Resolve Conflicts list dialog.
	if a.conflicts.open {
		a.conflictsOverlay(c, t)
	}
	// Reflog viewer.
	if a.reflog.open {
		a.reflogOverlay(c, t)
	}
	// Bisect panel.
	if a.commit.bisectOpen {
		a.bisectOverlay(c, t)
	}
	// LFS locks manager.
	if a.commit.lfsLocksOpen {
		a.lfsLocksOverlay(c, t)
	}
	// LFS data load (cheap after first fetch).
	a.commit.loadLFS(a.updater())
	// Branch/rebase target dialog.
	if a.branchDialogOpen {
		a.branchDialog(c, t)
	}
	if a.branchCmpOpen {
		a.branchCompareDialog(c, t)
	}
	if a.stashBranchOpen {
		a.stashBranchDialog(c, t)
	}
	// New-tag dialog.
	if a.branches.tagOpen {
		a.tagDialog(c, t)
	}
	// Rename-branch dialog.
	if a.branches.renameOpen {
		a.renameDialog(c, t)
	}
	// Remotes manager.
	if a.branches.remoteOpen {
		a.remoteDialog(c, t)
	}
	// Worktree add dialog.
	if a.branches.wtOpen {
		a.worktreeDialog(c, t)
	}
	// Push preview dialog.
	if a.commit.pushDlg.Open {
		a.pushDialogView(c, t)
	}
	// Update Project (pull) dialog.
	if a.commit.pullDlg.Open {
		a.pullDialogView(c, t)
	}
	// Interactive-rebase plan panel.
	if a.commit.rebaseDlg.Open {
		a.rebaseDialogView(c, t)
	}
	if a.amendOpen {
		a.amendDialog(c, t)
	}
	if a.pushUpToOpen {
		a.pushUpToDialog(c, t)
	}
	// Branch-compare overlay.
	if a.compare.open {
		a.compareOverlay(c, t)
	}
	// Go-to commit/tag dialog.
	if a.history.gotoOpen {
		a.gotoDialogView(c, t)
	}
	// Transient notifications on top of everything.
	a.toastLayer(c, t)
	// Destructive-action confirmation.
	if a.commit.pendingRevert.Open {
		a.revertDialog(c, t)
	}
}

// pickView shows the repo manager: recent list + open/clone actions.
func (a *app) pickView(c *ui.Context, t *ui.Theme) {
	tk := theme.TokensOf(c)
	repos := a.reposList()
	ui.Row(c).Fill().Background(tk.BgLayout).Children(func() {
		// Left: repo list.
		ui.Column(c).Width(280).Fill().Gap(0).Background(tk.BgContainer).Children(func() {
			ui.Row(c).Padding(12, 16).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "仓库").Bold()
				ui.Text(c, "").Grow(1)
				action.Button(c, "新建…", action.ButtonProps{Variant: action.Text, Size: theme.Small}).
					OnClick(func() {
						a.commit.initOpen = true
						a.commit.initErr = ""
						a.commit.initPath = ""
					})
				action.Button(c, "克隆…", action.ButtonProps{Variant: action.Text, Size: theme.Small}).
					OnClick(a.showCloneDialog)
			})
			layout.ScrollArea(c, layout.ScrollAreaProps{}, func() {
				ui.Column(c).FillWidth().Children(func() {
					for _, r := range repos {
						a.repoRowView(c, r)
					}
					if len(repos) == 0 {
						display.Empty(c, display.EmptyProps{
							Title:       "暂无仓库",
							Description: "打开或克隆一个 Git 仓库",
						}).Padding(24)
					}
				})
			}).Grow(1)
		})

		// Right: welcome + actions.
		ui.Column(c).Fill().Center().Gap(16).Padding(40).Children(func() {
			display.Empty(c, display.EmptyProps{
				Title:       "LumeGit",
				Description: "打开一个 Git 仓库开始提交",
				Page:        true,
			})
			if a.commit.openErr != "" {
				feedback.Alert(c, a.commit.openErr, feedback.AlertProps{
					Type:    feedback.Error,
					Variant: feedback.Soft,
				}).MaxWidth(420)
			}
			ui.Row(c).Gap(8).Children(func() {
				field.Input(c, &a.pickPath, field.InputProps{
					Placeholder: "/path/to/repo",
					Size:        theme.Middle,
				}).Width(320).OnSubmit(func() {
						if p := strings.TrimSpace(a.pickPath); p != "" {
							spawnWindow(p)
						}
					})
				action.Button(c, "打开…", action.ButtonProps{Variant: action.Primary, Size: theme.Middle}).
					OnClick(a.browseAndOpenNewWindow)
				action.Button(c, "打开", action.ButtonProps{Variant: action.Default, Size: theme.Middle}).
					OnClick(func() {
						if p := strings.TrimSpace(a.pickPath); p != "" {
							spawnWindow(p)
						}
					})
			})
		})
	})

	// Clone dialog overlay.
	if a.commit.cloneOpen {
		a.cloneDialogView(c, t)
	}
	// Init (new repo) dialog.
	if a.commit.initOpen {
		a.initDialogView(c, t)
	}
}

// openPickedRepo runs the repo open in a worker and publishes the
// snapshot; stale opens are fenced by gen.
func (a *app) openPickedRepo() {
	dir := strings.TrimSpace(a.pickPath)
	switch {
	case dir == "":
		a.commit.openErr = "请输入仓库路径，或点击「打开…」选择文件夹"
		return
	case a.commit.busy:
		return // an open is already in flight; a click does nothing
	}
	a.commit.gen++
	gen := a.commit.gen
	w := a.updater()
	a.commit.setBusy(w, true, "正在打开仓库…")
	openPath := dir
	go func() {
		snap, worktree, cerr := openRepo(appCTX, openPath)
		w.update(func() {
			if gen != a.commit.gen {
				return
			}
			a.commit.setBusyQuiet()
			if cerr != nil {
				a.commit.openErr = cerr.Error()
				return
			}
			a.pickPath = worktree
			a.commit.RepoPath = worktree
			a.commit.Snap = snap
			a.commit.openErr = ""
			a.history.reset()
			a.shelf.reset()
			a.branches.reset()
			if a.commit.repos != nil {
				a.commit.repos.Add(worktree, "")
				a.commit.lists = a.commit.repos.ChangelistsFor(worktree)
				a.saveRepos()
			}
			a.commit.reconcileLists()
			a.commit.ensureSelectedFile()
			// Refs polling: the repo we just opened wins over any
			// previous one. A fingerprint change invalidates the cached
			// branch list so the popup shows fresh refs next open.
			a.commit.stopPolling()
			a.commit.lastFingerprint = ""
			a.commit.onRefsChanged = func() {
				a.branches.lastLoaded = ""
				a.history.lastLoadedRepo = ""
			}
			if a.win != nil {
				a.commit.isActive = a.win.IsFocused
			}
			a.commit.startPolling(w)
			// Warm the other tabs' caches: each loader spawns its own
			// worker and returns at once, so the three git reads run
			// concurrently while the UI is already showing 提交.
			a.history.ensureLoaded(worktree, w)
			a.shelf.ensureLoaded(worktree, w)
			a.branches.ensureLoaded(worktree, w)
		})
	}()
}

// repoRowView: one repo in the left list.
func (a *app) repoRowView(c *ui.Context, r store.Repo) {
	selected := a.commit.RepoPath == r.Path
	row := ui.Row(c).Key("repo-"+r.Path).FillWidth().Padding(8, 16).Gap(8).AlignItems(ui.Center).
		Background(rowBg(c.Theme(), selected))
	row.Children(func() {
		if r.Pinned {
			ui.Text(c, "📌").FontSize(11)
		}
		ui.Column(c).Grow(1).Children(func() {
			ui.Text(c, r.Name).Bold()
			ui.Text(c, r.Path).FontSize(11).TextColor(c.Theme().TextMuted).SingleLine()
		})
		action.Button(c, "打开", action.ButtonProps{Variant: action.Text, Size: theme.Small}).
			OnClick(func() { spawnWindow(r.Path) })
	})
	row.ContextMenu(func(m *ui.Menu) {
		if m.Item("置顶").Chosen() {
			a.pinRepo(r.Path, !r.Pinned)
		}
		m.Separator()
		if m.Item("移除").Chosen() {
			a.removeRepo(r.Path)
		}
	})
}

// reposList returns the known repositories; nil-safe for headless
// tests and failed store loads.
func (a *app) reposList() []store.Repo {
	if a.commit.repos == nil {
		return nil
	}
	return a.commit.repos.List()
}

// pinRepo toggles a repo's pinned state.
func (a *app) pinRepo(path string, pinned bool) {
	if a.commit.repos == nil {
		return
	}
	a.commit.repos.SetPinned(path, pinned)
	a.saveRepos()
}

// removeRepo forgets a repo.
func (a *app) removeRepo(path string) {
	if a.commit.repos == nil {
		return
	}
	a.commit.repos.Remove(path)
	a.saveRepos()
}

// saveRepos persists the repo list (best effort; errors are shown as a
// transient openErr).
func (a *app) saveRepos() {
	if a.commit.repos == nil {
		return
	}
	if err := a.commit.repos.Save(); err != nil {
		a.commit.openErr = "保存仓库列表失败: " + err.Error()
	}
}

// showCloneDialog opens the clone form.
func (a *app) showCloneDialog() {
	a.commit.cloneOpen = true
	a.commit.cloneErr = ""
}

// browseAndOpenNewWindow asks for a folder, then opens it in a NEW
// window — the current window keeps its repo.
func (a *app) browseAndOpenNewWindow() {
	if a.win == nil {
		return
	}
	w := a.updater()
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:    a.win,
			Title:     "在新窗口打开仓库",
			Message:   "选择一个 Git 仓库文件夹",
			Directory: true,
		})
		w.update(func() {
			if err != nil || len(paths) == 0 {
				return
			}
			spawnWindow(paths[0])
		})
	}()
}

// commitView is the main workspace: header + tabs + active page.
func (a *app) commitView(c *ui.Context, t *ui.Theme) {
	tk := theme.TokensOf(c)
	ui.Column(c).Fill().Background(tk.BgLayout).Children(func() {
		a.headerView(c, t)
		ui.Row(c).Padding(4, 16).Background(tk.BgContainer).Children(func() {
			action.Segmented(c, &a.tab, []string{"提交", "历史", "搁置"}, action.SegmentedProps{Size: theme.Small})
		})
		if a.tab == 1 {
			a.historyView(c, t)
			return
		}
		if a.tab == 2 {
			a.shelfView(c, t)
			return
		}
		ui.Split(c, &a.filesPaneW,
			func() { a.filesPane(c, t) },
			func() { a.diffPane(c, t) },
		).Fill().Grow(1)
		a.commitBarView(c, t)
	})
}

// cloneDialogView renders the clone repository modal.
func (a *app) cloneDialogView(c *ui.Context, t *ui.Theme) {
	m := &a.commit
	confirmed := feedback.Modal(c, &m.cloneOpen, feedback.ModalProps{
		Title:  "克隆仓库",
		OkText: "克隆",
		Width:  480,
	}, func() {
		ui.Column(c).Gap(12).Children(func() {
			if m.cloneErr != "" {
				feedback.Alert(c, m.cloneErr, feedback.AlertProps{Type: feedback.Error, Variant: feedback.Soft})
			}
			ui.Column(c).Gap(4).Children(func() {
				ui.Text(c, "仓库地址").FontSize(12).TextColor(t.TextMuted)
				field.Input(c, &m.cloneURL, field.InputProps{
					Placeholder: "https://github.com/user/repo.git 或 git@github.com:user/repo.git",
					Size:        theme.Middle,
				}).Grow(1)
			})
			ui.Column(c).Gap(4).Children(func() {
				ui.Text(c, "目标目录").FontSize(12).TextColor(t.TextMuted)
				ui.Row(c).Gap(8).Children(func() {
					field.Input(c, &m.cloneDest, field.InputProps{
						Placeholder: "~/Projects",
						Size:        theme.Middle,
					}).Grow(1)
					action.Button(c, "浏览…", action.ButtonProps{Variant: action.Default, Size: theme.Middle}).
						OnClick(a.browseCloneDest)
				})
			})
			ui.Column(c).Gap(4).Children(func() {
				ui.Text(c, "分支（可选）").FontSize(12).TextColor(t.TextMuted)
				field.Input(c, &m.cloneBranch, field.InputProps{
					Placeholder: "main",
					Size:        theme.Middle,
				}).Grow(1)
			})
			ui.Row(c).Gap(12).Children(func() {
				ui.Column(c).Gap(4).Grow(1).Children(func() {
					ui.Text(c, "深度（0 = 完整克隆）").FontSize(12).TextColor(t.TextMuted)
					field.Input(c, &m.cloneDepth, field.InputProps{
						Placeholder: "0",
						Size:        theme.Middle,
					}).Grow(1)
				})
				ui.Column(c).Gap(4).Grow(1).Children(func() {
					ui.Text(c, "选项").FontSize(12).TextColor(t.TextMuted)
					choice.Checkbox(c, &m.cloneSubMod, "克隆子模块", choice.CheckboxProps{Size: 14})
				})
			})
			if m.cloneBusy {
				ui.Text(c, "正在克隆…").TextColor(t.TextMuted)
			}
		})
	})
	if confirmed && !m.cloneBusy {
		a.doClone()
	}
}

// browseCloneDest opens the directory picker for the clone destination.
func (a *app) browseCloneDest() {
	if a.win == nil {
		return
	}
	gen := a.commit.gen
	w := a.updater()
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:    a.win,
			Title:     "选择克隆目标目录",
			Directory: true,
		})
		w.update(func() {
			if gen != a.commit.gen || err != nil || len(paths) == 0 {
				return
			}
			a.commit.cloneDest = paths[0]
		})
	}()
}

// initDialogView renders the "新建仓库" modal (IDEA: Get from VCS →
// New Repository).
func (a *app) initDialogView(c *ui.Context, t *ui.Theme) {
	m := &a.commit
	confirmed := feedback.Modal(c, &m.initOpen, feedback.ModalProps{
		Title: "新建仓库", OkText: "创建", Width: 480,
	}, func() {
		ui.Column(c).Gap(12).Children(func() {
			if m.initErr != "" {
				feedback.Alert(c, m.initErr, feedback.AlertProps{Type: feedback.Error, Variant: feedback.Soft})
			}
			ui.Column(c).Gap(4).Children(func() {
				ui.Text(c, "仓库路径").FontSize(12).TextColor(t.TextMuted)
				ui.Row(c).Gap(8).Children(func() {
					field.Input(c, &m.initPath, field.InputProps{
						Placeholder: "~/Projects/my-repo",
						Size:        theme.Middle,
					}).Grow(1).OnSubmit(func() { a.doInit() })
					action.Button(c, "浏览…", action.ButtonProps{Variant: action.Default, Size: theme.Middle}).
						OnClick(func() {
							paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
								Parent:    a.win,
								Title:     "选择父目录",
								Directory: true,
							})
							if err == nil && len(paths) > 0 {
								m.initPath = paths[0]
							}
						})
				})
			})
			if m.initBusy {
				ui.Text(c, "正在创建…").TextColor(t.TextMuted)
			}
		})
	})
	if confirmed && !m.initBusy {
		a.doInit()
	}
}

// doInit runs `git init` in a worker then opens the new repo.
func (a *app) doInit() {
	m := &a.commit
	path := strings.TrimSpace(m.initPath)
	if path == "" {
		m.initErr = "请填写仓库路径"
		return
	}
	m.initBusy = true
	m.initErr = ""
	w := a.updater()
	gen := m.gen
	go func() {
		res, err := git.InitRepo(context.Background(), path)
		w.update(func() {
			if gen != m.gen {
				return
			}
			m.initBusy = false
			if err != nil {
				m.initErr = err.Error()
				return
			}
			if res != nil && !res.ExitOK() {
				m.initErr = res.OutputOfStderr()
				return
			}
			m.initOpen = false
			m.initPath = ""
			a.pickPath = path
			a.openPickedRepo()
		})
	}()
}

// doClone runs git clone in a worker.
func (a *app) doClone() {
	m := &a.commit
	url := strings.TrimSpace(m.cloneURL)
	dest := strings.TrimSpace(m.cloneDest)
	if url == "" {
		m.cloneErr = "请输入仓库地址"
		return
	}
	if dest == "" {
		m.cloneErr = "请选择目标目录"
		return
	}
	m.cloneBusy = true
	m.cloneErr = ""
	w := a.updater()
	gen := a.commit.gen
	branch := strings.TrimSpace(m.cloneBranch)
	depth := 0
	if d := strings.TrimSpace(m.cloneDepth); d != "" && d != "0" {
		if n, err := strconv.Atoi(d); err == nil && n > 0 {
			depth = n
		} else {
			m.cloneErr = "深度必须是正整数"
			return
		}
	}
	go func() {
		worktree, _, cerr := git.Clone(context.Background(), git.CloneRequest{
			URL: url, DestDir: dest, Branch: branch,
			Depth: depth, RecurseSubmodules: m.cloneSubMod,
		})
		w.update(func() {
			if gen != a.commit.gen {
				return
			}
			m.cloneBusy = false
			if cerr != nil {
				m.cloneErr = cerr.Error()
				return
			}
			m.cloneOpen = false
			m.cloneURL, m.cloneDest, m.cloneBranch, m.cloneDepth = "", "", "", ""
			a.pickPath = worktree
			a.openPickedRepo()
		})
	}()
}

// headerView: repo, branch, ahead/behind, in-progress, refresh.
func (a *app) headerView(c *ui.Context, t *ui.Theme) {
	m := &a.commit
	tk := theme.TokensOf(c)
	ui.Row(c).Padding(10, 16).Gap(12).AlignItems(ui.Center).Background(tk.BgContainer).Children(func() {
		ui.Text(c, baseName(m.RepoPath)).Bold()
		// IDEA-style "open recent in new window" — each window owns one
		// repo, so switching means spawning a new window, not reusing this.
		action.Button(c, "⌄", action.ButtonProps{Variant: action.Text, Size: theme.Small}).
			Menu(func(mn *ui.Menu) {
				for _, r := range a.reposList() {
					if r.Path == m.RepoPath {
						continue // already open here
					}
					p := r.Path
					if mn.Item("新窗口打开 " + r.Name).Chosen() {
						spawnWindow(p)
					}
				}
				mn.Separator()
				if mn.Item("浏览并打开仓库…").Chosen() {
					a.browseAndOpenNewWindow()
				}
				mn.Separator()
				if mn.Item("关闭窗口").Chosen() {
					if a.win != nil {
						a.win.Close()
					}
				}
			})
		if m.Snap != nil {
			branch := m.Snap.HeadName
			if m.Snap.Detached {
				// IDEA shows "DETACHED HEAD @ <oid>" and offers a one-click
				// "create branch here" — surface the same.
				branch = "DETACHED @ " + shortOID(m.Snap.HeadOID)
			}
			if m.Snap.Unborn {
				branch += " (未提交)"
			}
			// IDEA Branches popup: clicking the current-branch tag opens the
			// whole ref tree as a menu — checkout/merge/delete per branch.
			display.Tag(c, branch, display.TagProps{Variant: display.TagOutlined, Color: display.TagInfo}).
				Menu(func(mn *ui.Menu) { a.branchPopupMenu(mn) })
			if m.Snap.Detached {
				action.Button(c, "建分支", action.ButtonProps{Variant: action.Text, Size: theme.Small}).
					OnClick(func() {
						a.newBranchFrom = ""
						a.branchDialogOpen = true
					})
			}
			if ab := aheadBehind(m.Snap); ab != "" {
				display.Tag(c, ab, display.TagProps{Variant: display.TagBorderless})
			}
			if m.Snap.InProgress != "" {
				display.Tag(c, "进行中："+m.Snap.InProgress, display.TagProps{Variant: display.TagFilled, Color: display.TagWarning})
				// 全部冲突解决后可继续；任何时候可中止。
				prog := m.Snap.Progress()
				if !prog.CanContinue && len(m.Snap.Conflicts()) > 0 {
					action.Button(c, "解决冲突…", action.ButtonProps{Variant: action.Default, Size: theme.Small}).
						OnClick(func() {
							a.conflicts.openFor(m.RepoPath, a.updater())
						})
				}
				if prog.CanContinue {
					action.Button(c, "继续"+prog.Kind, action.ButtonProps{Variant: action.Primary, Size: theme.Small}).
						Disabled(m.busy).OnClick(func() {
						a.continueOp(prog.Kind)
					})
				}
				action.Button(c, "中止", action.ButtonProps{Variant: action.Text, Size: theme.Small}).
					Disabled(m.busy).OnClick(func() {
					a.abortOp(prog.Kind)
				})
			}
		}
		if m.syncOK != "" {
			display.Tag(c, "✓ "+m.syncOK, display.TagProps{Variant: display.TagBorderless, Color: display.TagSuccess})
		}
		// Flexible spacer pushes the remote actions to the far edge.
		ui.Text(c, "").Grow(1)
		action.Button(c, "获取", action.ButtonProps{Variant: action.Text, Size: theme.Small}).
			Disabled(m.busy).OnClick(func() { a.commit.fetch(appCTX, a.updater()) })
		action.Button(c, "拉取", action.ButtonProps{Variant: action.Text, Size: theme.Small}).
			Disabled(m.busy).OnClick(func() { a.commit.openPullDialog(a.updater()) })
		action.Button(c, "推送", action.ButtonProps{Variant: action.Text, Size: theme.Small}).
			Disabled(m.busy).OnClick(func() { a.commit.openPushDialog(a.updater(), false) })
		action.Button(c, "应用补丁…", action.ButtonProps{Variant: action.Text, Size: theme.Small}).
			Disabled(m.busy).OnClick(a.openApplyPatch)
		action.Button(c, "Reflog", action.ButtonProps{Variant: action.Text, Size: theme.Small}).
			Disabled(m.busy).OnClick(func() {
			a.reflog.openFor(m.RepoPath, a.updater())
		})
		action.Button(c, "二分", action.ButtonProps{Variant: action.Text, Size: theme.Small}).
			Disabled(m.busy).OnClick(func() {
			m.bisectOpen = true
			m.bisectErr = ""
			m.loadBisectStatus(a.updater())
		})
		action.Button(c, "刷新", action.ButtonProps{Variant: action.Text, Size: theme.Small}).
			Disabled(m.busy).OnClick(func() {
			a.commit.refresh(appCTX, a.updater())
		})
	})
}

// continueOp resumes the in-progress merge/rebase/cherry-pick.
func (a *app) continueOp(kind string) {
	m := &a.commit
	m.gen++
	gen := m.gen
	repoPath := m.RepoPath
	m.setBusy(a.updater(), true, "正在继续…")
	go func() {
		repo := git.NewRepo(repoPath)
		res, err := repo.Continue(context.Background(), kind)
		snap, _ := repo.Snapshot(context.Background())
		w := a.updater()
		w.update(func() {
			if gen != m.gen {
				return
			}
			m.setBusyQuiet()
			if err != nil {
				m.lastErr = err.Error()
			} else if res != nil && !res.ExitOK() {
				m.lastErr = res.OutputOfStderr()
			} else {
				m.lastErr = ""
				m.syncOK = "已继续"
			}
			if snap != nil {
				m.Snap = snap
				m.reconcileLists()
				m.pruneChecked()
				m.ensureSelectedFile()
			}
		})
	}()
}

// abortOp cancels the in-progress op.
func (a *app) abortOp(kind string) {
	m := &a.commit
	m.gen++
	gen := m.gen
	repoPath := m.RepoPath
	m.setBusy(a.updater(), true, "正在中止…")
	go func() {
		repo := git.NewRepo(repoPath)
		res, err := repo.Abort(context.Background(), kind)
		snap, _ := repo.Snapshot(context.Background())
		w := a.updater()
		w.update(func() {
			if gen != m.gen {
				return
			}
			m.setBusyQuiet()
			if err != nil {
				m.lastErr = err.Error()
			} else if res != nil && !res.ExitOK() {
				m.lastErr = res.OutputOfStderr()
			} else {
				m.lastErr = ""
				m.syncOK = "已中止"
			}
			if snap != nil {
				m.Snap = snap
				m.reconcileLists()
				m.pruneChecked()
				m.ensureSelectedFile()
			}
		})
	}()
}

// filesPane: mode switch + search + grouped file rows.
func (a *app) filesPane(c *ui.Context, t *ui.Theme) {
	m := &a.commit
	tk := theme.TokensOf(c)
	ui.Column(c).Fill().Gap(0).Background(tk.BgContainer).Children(func() {
		ui.Row(c).Padding(8, 12).Gap(8).AlignItems(ui.Center).Children(func() {
			prevMode := m.mode
			action.Segmented(c, &m.mode, []string{"列表", "更改"}, action.SegmentedProps{Size: theme.Small})
			if m.mode != prevMode {
				// The two modes give a check a different meaning
				// (changelist = commit scope; staging = batch pick), so
				// carried-over ticks would act on the wrong thing.
				m.checks = checkboxes{}
				m.groupChecks = nil
			}
			field.SearchBar(c, &m.filter, field.SearchBarProps{
				Placeholder: "过滤文件…",
				Size:        theme.Small,
			}).Grow(1)
			action.Button(c, "+", action.ButtonProps{Variant: action.Text, Size: theme.Small}).
				OnClick(func() { m.newListOpen = !m.newListOpen })
		})
		if m.newListOpen {
			ui.Row(c).Padding(6, 12).Gap(8).Children(func() {
				field.Input(c, &m.newListName, field.InputProps{
					Placeholder: "新变更列表名…",
					Size:        theme.Small,
				}).Grow(1).OnSubmit(func() {
					a.createListFromInput()
				})
				action.Button(c, "创建", action.ButtonProps{Variant: action.Primary, Size: theme.Small}).
					OnClick(a.createListFromInput)
			})
		}
		if m.lastErr != "" {
			feedback.Alert(c, m.lastErr, feedback.AlertProps{
				Type:    feedback.Error,
				Variant: feedback.Plain,
			}).Padding(8, 12)
		}
		layout.ScrollArea(c, layout.ScrollAreaProps{State: &a.fileScroll}, func() {
			ui.Column(c).FillWidth().Children(func() {
				if a.commit.filter != "" {
					// Filter mode: flat result list, no group chrome.
					for _, row := range a.visibleRows() {
						a.fileRowView(c, t, row)
					}
					return
				}
				if m.mode == 1 {
					a.stagingGroups(c, t)
					return
				}
				for _, g := range m.groups() {
					a.listGroupView(c, t, g)
				}
			})
		}).Grow(1)
	})
}

// createListFromInput makes the list and, when a file triggered the
// dialog via "move to new list", assigns it there.
func (a *app) createListFromInput() {
	m := &a.commit
	name := strings.TrimSpace(m.newListName)
	if name == "" {
		return
	}
	// Only auto-activate when the user opened the box from the toolbar;
	// a "move to new list" context menu keeps the current active list.
	m.newList(name, m.moveTo == "")
	if m.moveTo != "" {
		m.moveFile(m.moveTo, name)
		m.moveTo = ""
	}
	m.newListName = ""
	m.newListOpen = false
}

// stagingGroups renders the IDEA "Local Changes" split: 更改 (tracked
// changes, staged or not) / 未进行版本管理的文件 (untracked). AM files
// appear once under 更改 — they are tracked, so their worktree side
// stays a tracked change.
func (a *app) stagingGroups(c *ui.Context, t *ui.Theme) {
	m := &a.commit
	if m.Snap == nil {
		return
	}
	var tracked, untracked []git.FileChange
	for _, f := range m.Snap.Files {
		if f.Kind == git.KindUntracked {
			untracked = append(untracked, f)
		} else {
			tracked = append(tracked, f)
		}
	}
	a.stagingSection(c, t, "更改", tracked, "changes")
	a.stagingSection(c, t, "未进行版本管理的文件", untracked, "unversioned")
}

// stagingSection draws one of the two groups with its header and rows.
// groupKey is a stable id for collapse + group-checkbox state.
func (a *app) stagingSection(c *ui.Context, t *ui.Theme, title string, files []git.FileChange, groupKey string) {
	m := &a.commit
	if m.collapsed == nil {
		m.collapsed = map[string]bool{}
	}
	key := "stg-" + groupKey
	hdr := ui.Row(c).Key(key).FillWidth().Padding(5, 12).Gap(8).AlignItems(ui.Center)
	hdr.Children(func() {
		arrow := "▾"
		if m.collapsed[key] {
			arrow = "▸"
		}
		ui.Text(c, arrow).FontSize(11).TextColor(t.TextMuted).
			OnClick(func() { m.collapsed[key] = !m.collapsed[key] })
		// Group checkbox = "include this whole group in the commit",
		// IDEA's top-level Changes tick.
		checkedN := m.checkedIn(files)
		bind := m.groupCheckBind(key)
		*bind = checkedN == len(files) && len(files) > 0
		partial := checkedN > 0 && checkedN < len(files)
		choice.Checkbox(c, bind, "", choice.CheckboxProps{Size: 12, Indeterminate: partial}).
			Disabled(len(files) == 0).
			OnClick(func() {
				m.checkGroupPaths(files, *bind)
			})
		ui.Text(c, title).Bold().FontSize(12).SingleLine().Grow(1)
		ui.Text(c, fmt.Sprintf("%d", len(files))).FontSize(11).TextColor(t.TextMuted)
	})
	if m.collapsed[key] {
		return
	}
	for _, f := range files {
		a.stagingRowView(c, t, f)
	}
}

// stagingRowView: checkbox (include in commit) + click-to-diff. An AM
// file shows "A+M" so the user sees the file carries both staged and
// unstaged parts; its diff is HEAD → worktree (the full change).
func (a *app) stagingRowView(c *ui.Context, t *ui.Theme, ch git.FileChange) {
	m := &a.commit
	path := ch.Path
	cb := m.checks.checkboxFor(path)
	rowEl := ui.Row(c).Key("sfile-"+path).FillWidth().Padding(6, 12).Gap(10).
		Background(rowBg(t, m.selectedFile == path)).
		AlignItems(ui.Center)
	rowEl.Children(func() {
		choice.Checkbox(c, cb, "", choice.CheckboxProps{Size: 14}).Disabled(ch.Conflict())
		display.Tag(c, statusGlyph(ch.Kind), display.TagProps{
			Variant: display.TagBorderless,
			Color:   statusTagColor(ch.Kind),
		})
		label := path
		if ch.OrigPath != "" {
			label = ch.OrigPath + " → " + path
		}
		ui.Text(c, label).SingleLine().
			TextColor(rowLabelColor(t, m.selectedFile == path)).
			OnClick(func() {
				if ch.Conflict() {
					a.conflict.openFor(m.RepoPath, path, a.updater())
					return
				}
				a.selectRow(ch)
			})
		// "+M" marker when the file also has unstaged worktree edits.
		if ch.Staged() && ch.Unstaged() {
			ui.Text(c, "+M").FontSize(11).TextColor(t.TextMuted)
		}
		// LFS badge when the file is tracked by git-lfs.
		if m.lfs != nil && m.lfs.LFSPathTracked(path) {
			display.Tag(c, "LFS", display.TagProps{
				Variant: display.TagBorderless, Color: display.TagInfo,
			})
		}
	})
	rowEl.ContextMenu(func(mn *ui.Menu) {
		untracked := ch.Kind == git.KindUntracked
		if ch.Conflict() {
			if mn.Item("解决冲突…").Chosen() {
				a.conflict.openFor(m.RepoPath, path, a.updater())
			}
			mn.Separator()
		}
		if untracked {
			if mn.Item("搁置此文件到 stash").Chosen() {
				m.shelvePaths(appCTX, a.updater(), []string{path}, true)
			}
			if mn.Item("搁置到搁置架（patch）").Chosen() {
				m.shelveToShelf(appCTX, a.updater(), []string{path})
			}
			if mn.Item("加入 .gitignore").Chosen() {
				m.ignorePath(appCTX, a.updater(), path)
			}
			if mn.Item("删除…").Chosen() {
				a.askDelete(path)
			}
		} else {
			// Revert works on whichever side holds changes; staged-only
			// files revert the index, the rest revert the worktree.
			revertStaged := ch.Staged() && !ch.Unstaged()
			if mn.Item("回滚…").Chosen() {
				a.askRevert([]string{path}, revertStaged)
			}
			if mn.Item("搁置此文件到 stash").Chosen() {
				m.shelvePaths(appCTX, a.updater(), []string{path}, ch.Unstaged())
			}
			if mn.Item("搁置到搁置架（patch）").Chosen() {
				m.shelveToShelf(appCTX, a.updater(), []string{path})
			}
		}
		mn.Separator()
		if mn.Item("查看历史").Chosen() {
			a.fileHist.openFor(m.RepoPath, path, a.updater())
		}
		if mn.Item("查看溯源（Blame）").Chosen() {
			a.fileHist.mode = 1
			a.fileHist.openFor(m.RepoPath, path, a.updater())
		}
		mn.Separator()
		if mn.Item("在 Finder 中显示").Chosen() {
			a.revealPath(path)
		}
		if mn.Item("复制路径").Chosen() {
			a.copyPath(c, path)
		}
	})
}

// listGroupView draws one collapsible changelist header + its files.
func (a *app) listGroupView(c *ui.Context, t *ui.Theme, g listGroup) {
	m := &a.commit
	if m.collapsed == nil {
		m.collapsed = map[string]bool{}
	}
	if len(g.Rows) == 0 && g.Name != store.DefaultName {
		return // empty named lists hide (IDEA collapses them away)
	}
	name := g.Name
	// Header: fold arrow + name + count + active marker + group menu.
	hdr := ui.Row(c).Key("lg-"+name).FillWidth().Padding(5, 12).Gap(8).AlignItems(ui.Center)
	hdr.Children(func() {
		arrow := "▾"
		if m.collapsed[name] {
			arrow = "▸"
		}
		ui.Text(c, arrow).FontSize(11).TextColor(t.TextMuted).
			OnClick(func() { m.collapsed[name] = !m.collapsed[name] })
		// Group checkbox ticks every file in the list. Bind a stable
		// *bool so the click writes somewhere we can read back; the
		// post-frame toggle uses the freshly-written value.
		bind := m.groupCheckBind(name)
		m.syncGroupCheck(name, g.CheckedN, len(g.Rows)) // keep in step pre-click
		partial := g.CheckedN > 0 && g.CheckedN < len(g.Rows)
		choice.Checkbox(c, bind, "", choice.CheckboxProps{Size: 12, Indeterminate: partial}).
			OnClick(func() {
				m.checkGroup(name, *bind) // *bind is now the new state
			})
		label := name
		if g.Active {
			label += "（活动）"
		}
		ui.Text(c, label).Bold().FontSize(12).SingleLine().Grow(1)
		ui.Text(c, fmt.Sprintf("%d", len(g.Rows))).FontSize(11).TextColor(t.TextMuted)
	})
	hdr.ContextMenu(func(mn *ui.Menu) {
		// IDEA: "Commit this changelist" — tick only its files.
		if len(g.Rows) > 0 {
			if mn.Item("仅提交此变更列表").Chosen() {
				m.checkNone()
				m.checkGroup(name, true)
			}
		}
		if !g.Active && name != store.DefaultName {
			if mn.Item("设为活动").Chosen() {
				m.setActiveList(name)
			}
		}
		if name != store.DefaultName {
			mn.Separator()
			if mn.Item("删除列表").Chosen() {
				m.deleteList(name)
			}
		}
	})
	if m.collapsed[name] {
		return
	}
	for _, row := range g.Rows {
		a.fileRowView(c, t, row)
	}
}

// visibleRows is the displayed subset (filter applied).
func (a *app) visibleRows() []fileRow {
	rows := rowsFor(a.commit.Snap)
	if a.commit.filter == "" {
		return rows
	}
	needle := strings.ToLower(a.commit.filter)
	out := make([]fileRow, 0, len(rows))
	for _, r := range rows {
		if strings.Contains(strings.ToLower(r.change.Path), needle) {
			out = append(out, r)
		}
	}
	return out
}

// fileRowView: one row = checkbox (include in commit) + click-to-diff.
// Checkbox binds through checkboxFor's addressable *bool.
func (a *app) fileRowView(c *ui.Context, t *ui.Theme, row fileRow) {
	m := &a.commit
	ch := row.change
	path := ch.Path
	cb := m.checks.checkboxFor(path)
	rowEl := ui.Row(c).Key("file-"+path).FillWidth().Padding(6, 12).Gap(10).
		Background(rowBg(t, a.commit.selectedFile == path)).
		AlignItems(ui.Center)
	rowEl.Children(func() {
		choice.Checkbox(c, cb, "", choice.CheckboxProps{Size: 14}).Disabled(ch.Conflict())
		display.Tag(c, statusGlyph(ch.Kind), display.TagProps{
			Variant: display.TagBorderless,
			Color:   statusTagColor(ch.Kind),
		})
		label := path
		if ch.OrigPath != "" {
			label = ch.OrigPath + " → " + path
		}
		// The path text is the click target; conflicts open the resolver
		// instead of a diff that can't render three sides.
		ui.Text(c, label).SingleLine().
			TextColor(rowLabelColor(t, a.commit.selectedFile == path)).
			OnClick(func() {
				if ch.Conflict() {
					a.conflict.openFor(m.RepoPath, path, a.updater())
					return
				}
				a.selectRow(ch)
			})
	})
	// Right-click: move this file to another changelist + history +
	// file ops (revert / gitignore / shelve / delete).
	rowEl.ContextMenu(func(mn *ui.Menu) {
		untracked := ch.Kind == git.KindUntracked
		if ch.Conflict() {
			if mn.Item("解决冲突…").Chosen() {
				a.conflict.openFor(m.RepoPath, path, a.updater())
			}
			mn.Separator()
		}
		// File ops.
		if untracked {
			if mn.Item("搁置此文件到 stash").Chosen() {
				m.shelvePaths(appCTX, a.updater(), []string{path}, true)
			}
			if mn.Item("搁置到搁置架（patch）").Chosen() {
				m.shelveToShelf(appCTX, a.updater(), []string{path})
			}
			if mn.Item("加入 .gitignore").Chosen() {
				m.ignorePath(appCTX, a.updater(), path)
			}
			if mn.Item("删除…").Chosen() {
				a.askDelete(path)
			}
		} else {
			if ch.Staged() && !ch.Unstaged() {
				if mn.Item("回滚…").Chosen() {
					a.askRevert([]string{path}, true)
				}
			} else {
				if mn.Item("回滚…").Chosen() {
					a.askRevert([]string{path}, false)
				}
			}
			if mn.Item("搁置此文件到 stash").Chosen() {
				m.shelvePaths(appCTX, a.updater(), []string{path}, ch.Unstaged())
			}
			if mn.Item("搁置到搁置架（patch）").Chosen() {
				m.shelveToShelf(appCTX, a.updater(), []string{path})
			}
		}
		mn.Separator()
		if mn.Item("查看历史").Chosen() {
			a.fileHist.openFor(m.RepoPath, path, a.updater())
		}
		if mn.Item("查看溯源（Blame）").Chosen() {
			a.fileHist.mode = 1
			a.fileHist.openFor(m.RepoPath, path, a.updater())
		}
		mn.Separator()
		if mn.Item("在 Finder 中显示").Chosen() {
			a.revealPath(path)
		}
		if mn.Item("复制路径").Chosen() {
			a.copyPath(c, path)
		}
		if mn.Item("创建补丁…").Chosen() {
			a.exportWorktreePatch(path)
		}
		// IDEA's "move to another changelist" only applies to tracked
		// changes — untracked files aren't changelist members.
		if m.lists != nil && !untracked {
			mn.Separator()
			for _, l := range m.lists.All() {
				if l.Name == row.list {
					continue
				}
				dest := l.Name
				if mn.Item("移动到「" + dest + "」").Chosen() {
					m.moveFile(path, dest)
				}
			}
			mn.Separator()
			if mn.Item("新建列表并移动…").Chosen() {
				m.moveTo = path
				m.newListOpen = true
			}
		}
	})
}

// revertDialog confirms a destructive action (revert worktree/index or
// delete an untracked file), then runs the stored op.
func (a *app) revertDialog(c *ui.Context, t *ui.Theme) {
	m := &a.commit
	ask := &m.pendingRevert
	body := ask.Body
	if body == "" {
		switch {
		case ask.Delete != "":
			body = fmt.Sprintf("删除 %s？此操作无法撤销。", ask.Delete)
		case ask.Staged:
			body = fmt.Sprintf("回滚 %d 个已暂存更改？索引和工作区都会回到 HEAD。", len(ask.Paths))
		default:
			body = fmt.Sprintf("回滚 %d 个未暂存更改？未提交的修改会丢失。", len(ask.Paths))
		}
	}
	if feedback.Modal(c, &ask.Open, feedback.ModalProps{
		Title:  ask.Title,
		OkText: "确定",
		Width:  420,
	}, func() {
		ui.Column(c).Gap(8).Children(func() {
			feedback.Alert(c, body, feedback.AlertProps{Type: feedback.Warning, Variant: feedback.Soft})
			if len(ask.Paths) > 0 && len(ask.Paths) <= 8 {
				ui.Column(c).Gap(2).Children(func() {
					for _, p := range ask.Paths {
						ui.Text(c, "• "+p).FontSize(11).TextColor(t.TextMuted).SingleLine()
					}
				})
			}
		})
	}) {
		// Confirmed: run the op and reset the ask.
		paths := append([]string(nil), ask.Paths...)
		del := ask.Delete
		staged := ask.Staged
		m.pendingRevert = revertAsk{}
		switch {
		case del != "":
			m.deleteUntracked(appCTX, a.updater(), del)
		case staged:
			m.revertPaths(appCTX, a.updater(), paths, true)
		default:
			m.revertPaths(appCTX, a.updater(), paths, false)
		}
	}
	// Closing via ✕/Esc clears the ask.
	if !ask.Open {
		m.pendingRevert = revertAsk{}
	}
}

// askRevert opens the destructive-action dialog for a revert set.
func (a *app) askRevert(paths []string, staged bool) {
	if len(paths) == 0 {
		return
	}
	a.commit.pendingRevert = revertAsk{
		Open:   true,
		Title:  "回滚更改",
		Staged: staged,
		Paths:  paths,
	}
}

// askDelete opens the delete-confirmation for one untracked file.
func (a *app) askDelete(path string) {
	a.commit.pendingRevert = revertAsk{
		Open:   true,
		Title:  "删除文件",
		Delete: path,
	}
}

// revealPath opens the file in the OS file manager.
func (a *app) revealPath(path string) {
	if a.commit.RepoPath == "" {
		return
	}
	full := a.commit.RepoPath + "/" + path
	go func() {
		_ = exec.Command("open", "-R", full).Run()
	}()
}

// copyPath writes the repo-relative path to the clipboard. mygo's
// clipboard lives on the ui.Context, so this is invoked from a click
// handler that has c in scope.
func (a *app) copyPath(c *ui.Context, path string) {
	c.WriteClipboard(path)
}

// rowBg: selected rows get the selection tint.
func rowBg(t *ui.Theme, sel bool) ui.Color {
	if sel {
		return t.Selection
	}
	return ui.Transparent
}

func rowLabelColor(t *ui.Theme, sel bool) ui.Color {
	if sel {
		return t.Accent
	}
	return t.Text
}

// selectRow records the pick and schedules a fresh diff load. Picking
// the already-loaded file is a no-op so rapid clicks don't thrash the
// diff worker or rebuild the view.
func (a *app) selectRow(ch git.FileChange) {
	if a.commit.selectedFile == ch.Path && !a.commit.diffLoading && a.commit.diff != nil {
		return
	}
	a.diffState = nil // rebuild DiffView state for the new file
	a.commit.selectFile(ch)
}

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
			a.placeholder(c, t, "正在加载历史…")
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
var graphColors = []ui.Color{
	ui.RGB(0x5b, 0x8d, 0xef), ui.RGB(0x53, 0xc1, 0x8d), ui.RGB(0xe0, 0x9f, 0x3e),
	ui.RGB(0xcf, 0x67, 0xcc), ui.RGB(0xef, 0x6b, 0x6b), ui.RGB(0x3f, 0xa6, 0xc3),
}

// graphCell draws the 24-DIP lane column for one row: vertical lines,
// the commit node, and any merge/fork connectors.
func graphCell(c *ui.Context, gl *git.GraphLine) ui.Element {
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

// commitRowView: one history entry — graph + subject + refs + author + time.
func (a *app) commitRowView(c *ui.Context, t *ui.Theme, cm *git.Commit, gl *git.GraphLine) {
	sel := a.history.selectedID == cm.OID
	repoPath := a.commit.RepoPath
	oid := cm.OID
	rowEl := ui.Row(c).Key("cmt-"+oid).FillWidth().Padding(7, 14).Gap(10).AlignItems(ui.Center).
		Background(rowBg(t, sel))
	rowEl.OnClick(func() {
		a.diffState = nil
		a.history.selectCommit(repoPath, oid, a.updater())
	})
	rowEl.ContextMenu(func(mn *ui.Menu) {
		isHead := len(a.history.commits) > 0 && a.history.commits[0].OID == oid
		if mn.Item("复制修订号").Chosen() {
			c.WriteClipboard(oid)
			a.notify("success", "已复制 "+cm.ShortOID)
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
				a.amendInput = cm.Subject
				a.amendOpen = true
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
			a.commit.resetTo(appCTX, a.updater(), oid, git.ResetHard)
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
				ui.Text(c, relTime(cm.When)).FontSize(11).TextColor(t.TextMuted)
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
				ui.Text(c, shortOID(m.selectedID)).FontSize(11).TextColor(t.TextMuted)
			}
		})
		switch {
		case m.selectedID == "":
			a.placeholder(c, t, "选择提交。")
		case m.filesLoading:
			a.placeholder(c, t, "正在加载文件…")
		case m.filesErr != "":
			ui.Column(c).Padding(8, 12).Children(func() {
				feedback.Alert(c, m.filesErr, feedback.AlertProps{Type: feedback.Error, Variant: feedback.Soft})
			})
		case len(m.files) == 0:
			a.placeholder(c, t, "无文件变更。")
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
		Background(rowBg(t, sel))
	row.Children(func() {
		display.Tag(c, statusGlyph(f.StatusKind()), display.TagProps{
			Variant: display.TagBorderless,
			Color:   statusTagColor(f.StatusKind()),
		})
		ui.Text(c, label).SingleLine().
			TextColor(rowLabelColor(t, sel)).
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
			a.placeholder(c, t, "正在加载搁置…")
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
		Background(rowBg(t, sel))
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
			m.op(repoPath, a.updater(), "正在删除…", "已删除",
				func(r *git.Repo) error { return r.DropShelf(context.Background(), name) }, nil)
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
		Background(rowBg(t, sel))
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
			m.op(repoPath, a.updater(), "正在删除…", "已删除",
				func(r *git.Repo) error { _, err := r.StashDrop(context.Background(), name); return err }, nil)
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
			a.placeholder(c, t, "选择搁置。")
		case m.filesLoading:
			a.placeholder(c, t, "正在加载文件…")
		case m.filesErr != "":
			ui.Column(c).Padding(8, 12).Children(func() {
				feedback.Alert(c, m.filesErr, feedback.AlertProps{Type: feedback.Error, Variant: feedback.Soft})
			})
		case len(m.files) == 0:
			a.placeholder(c, t, "无文件变更。")
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
	kind := stashFileKind(f)
	ui.Row(c).Key("shf-"+f.Path).FillWidth().Padding(6, 14).Gap(10).AlignItems(ui.Center).
		Background(rowBg(t, sel)).
		Children(func() {
			display.Tag(c, statusGlyph(kind), display.TagProps{
				Variant: display.TagBorderless, Color: statusTagColor(kind),
			})
			ui.Text(c, label).SingleLine().
				TextColor(rowLabelColor(t, sel)).
				OnClick(func() {
					a.diffState = nil
					m.selectFile(repoPath, stashName, file, a.updater())
				})
		})
}

// stashFileKind maps a stash file's status to the shared FileKind set.
func stashFileKind(f *git.StashFile) git.FileKind {
	if f.Untracked {
		return git.KindUntracked
	}
	switch f.Status {
	case 'A':
		return git.KindAdded
	case 'D':
		return git.KindDeleted
	case 'R':
		return git.KindRenamed
	case 'T':
		return git.KindTypechange
	}
	return git.KindModified
}

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
				return r.PushTag(context.Background(), "origin", name)
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
		m.op(repoPath, a.updater(), "正在移除 worktree…", "已移除",
			func(r *git.Repo) (*git.RunResult, error) {
				return r.RemoveWorktree(context.Background(), wt.Path, false)
			}, nil)
	}
	if sub.Item("强制移除（丢弃改动）").Chosen() {
		m.op(repoPath, a.updater(), "正在强制移除…", "已移除",
			func(r *git.Repo) (*git.RunResult, error) {
				return r.RemoveWorktree(context.Background(), wt.Path, true)
			}, nil)
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
				a.placeholder(c, t, "正在检查上游…")
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
		Title:  "交互式变基 — " + shortOID(d.Onto),
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
						display.Tag(c, shortOID(st.CurrentOID), display.TagProps{Variant: display.TagOutlined, Color: display.TagInfo})
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
				a.placeholder(c, t, "没有 LFS 锁。")
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
					a.placeholder(c, t, "正在加载文件…")
				case m.err != "":
					feedback.Alert(c, m.err, feedback.AlertProps{Type: feedback.Error, Variant: feedback.Soft})
				case len(m.files) == 0 && m.selected == "":
					a.placeholder(c, t, "没有差异文件。")
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
					a.placeholder(c, t, "选择文件查看差异。")
				case m.diffLoad:
					a.placeholder(c, t, "正在加载差异…")
				case m.diffErr != "":
					feedback.Alert(c, m.diffErr, feedback.AlertProps{Type: feedback.Error, Variant: feedback.Soft})
				case m.diff == nil:
					a.placeholder(c, t, "无差异。")
				case m.diff.Binary:
					a.placeholder(c, t, "二进制文件。")
				case string(m.diff.Old) == string(m.diff.New):
					// The file row appeared under a merge-base diff but
					// both tips hold identical content — say so rather
					// than paint an all-context diff that reads as blank.
					a.placeholder(c, t, "两端内容相同。")
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
		a.cmpSide(c, t, m.ref+" 独有", ours)
		a.cmpSide(c, t, other+" 独有", theirs)
		if m.cmp.Unrelated {
			feedback.Alert(c, "两个分支没有共同祖先", feedback.AlertProps{Type: feedback.Warning, Variant: feedback.Soft})
		}
	})
}

// cmpSide lists one side's unique commits (compact, newest first).
func (a *app) cmpSide(c *ui.Context, t *ui.Theme, label string, list []git.Commit) {
	ui.Column(c).FillWidth().Gap(1).Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Padding(3, 0).Children(func() {
			ui.Text(c, label).FontSize(11).Bold()
			display.Tag(c, fmt.Sprintf("%d", len(list)), display.TagProps{
				Variant: display.TagBorderless, Color: display.TagInfo,
			})
		})
		if len(list) == 0 {
			ui.Text(c, "（无独有提交）").FontSize(11).TextColor(t.TextMuted).Padding(0, 0, 6, 0)
			return
		}
		show := list
		truncated := 0
		if len(show) > 6 {
			truncated = len(show) - 6
			show = show[:6]
		}
		for i := range show {
			cm := &show[i]
			ui.Row(c).Key("cmps-"+cm.OID).FillWidth().Gap(6).Padding(2, 0).Children(func() {
				ui.Text(c, cm.ShortOID).FontSize(11).TextColor(t.TextMuted).Width(58).SingleLine()
				ui.Text(c, cm.Subject).FontSize(11).SingleLine().Grow(1)
			})
		}
		if truncated > 0 {
			ui.Text(c, fmt.Sprintf("…还有 %d 条", truncated)).FontSize(11).TextColor(t.TextMuted).
				Padding(0, 0, 4, 0)
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
		Background(rowBg(t, sel)).
		OnClick(func() {
			a.cmpDiffState = nil
			m.selectFile(a.commit.RepoPath, f.Path, a.updater())
		}).
		Children(func() {
			display.Tag(c, statusGlyph(f.StatusKind()), display.TagProps{
				Variant: display.TagBorderless,
				Color:   statusTagColor(f.StatusKind()),
			})
			ui.Text(c, label).FontSize(12).SingleLine()
		})
}

// cmpDiffView renders the compare diff with its own state.
func (a *app) cmpDiffView(c *ui.Context, t *ui.Theme) {
	d := a.compare.diff
	if a.cmpDiffState == nil {
		a.cmpDiffState = &editor.DiffState{
			Old: editor.NewBuffer(string(d.Old)),
			New: editor.NewBuffer(string(d.New)),
		}
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
			a.placeholder(c, t, "正在加载冲突内容…")
		case m.err != "":
			feedback.Alert(c, m.err, feedback.AlertProps{Type: feedback.Error, Variant: feedback.Soft})
		case m.sides == nil:
			a.placeholder(c, t, "无冲突内容。")
		default:
			a.conflictResolver(c, t)
		}
	})
}

// conflictResolver draws base/ours/theirs + merge editor + actions.
func (a *app) conflictResolver(c *ui.Context, t *ui.Theme) {
	m := &a.conflict
	ui.Column(c).Gap(10).Children(func() {
		// Three sides side-by-side (read-only diffs against base).
		ui.Row(c).Gap(8).Children(func() {
			a.conflictSide(c, t, "当前分支（ours）", m.sides.Base, m.sides.Ours)
			a.conflictSide(c, t, "基础版本（base）", m.sides.Base, m.sides.Base)
			a.conflictSide(c, t, "合并进来（theirs）", m.sides.Base, m.sides.Their)
		})
		// Merge editor.
		ui.Column(c).Gap(4).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "合并结果").Bold()
				ui.Text(c, "").Grow(1)
				action.Button(c, "采用当前分支", action.ButtonProps{Variant: action.Text, Size: theme.Small}).
					OnClick(func() { m.merged = string(m.sides.Ours) })
				action.Button(c, "采用对方", action.ButtonProps{Variant: action.Text, Size: theme.Small}).
					OnClick(func() { m.merged = string(m.sides.Their) })
			})
			field.TextArea(c, &m.merged, field.TextAreaProps{
				MinRows: 8,
				MaxRows: 16,
			}).Grow(1)
		})
		// Actions.
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			action.Button(c, "采用当前分支并解决", action.ButtonProps{Variant: action.Default, Size: theme.Middle}).
				Disabled(m.busy).OnClick(func() {
				m.resolve(a.commit.RepoPath, git.ResolveOurs, a.updater(), func() {
					a.commit.refresh(appCTX, a.updater())
				})
			})
			action.Button(c, "采用对方并解决", action.ButtonProps{Variant: action.Default, Size: theme.Middle}).
				Disabled(m.busy).OnClick(func() {
				m.resolve(a.commit.RepoPath, git.ResolveTheirs, a.updater(), func() {
					a.commit.refresh(appCTX, a.updater())
				})
			})
			action.Button(c, "保存合并结果", action.ButtonProps{Variant: action.Primary, Size: theme.Middle}).
				Disabled(m.busy).OnClick(func() {
				m.resolve(a.commit.RepoPath, git.ResolveMerge, a.updater(), func() {
					a.commit.refresh(appCTX, a.updater())
				})
			})
		})
	})
}

// conflictSide renders one read-only diff column.
func (a *app) conflictSide(c *ui.Context, t *ui.Theme, title string, base, side []byte) {
	tk := theme.TokensOf(c)
	ui.Column(c).Grow(1).Basis(0).Gap(4).Children(func() {
		ui.Text(c, title).FontSize(11).Bold().TextColor(t.TextMuted)
		ui.Column(c).Border(1, tk.BorderSecondary).Radius(6).Padding(6).MaxHeight(180).Children(func() {
			layout.ScrollArea(c, layout.ScrollAreaProps{}, func() {
				ui.Text(c, string(side)).FontSize(11).SingleLine()
			}).Grow(1)
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
				a.placeholder(c, t, "正在加载冲突文件…")
			case m.err != "":
				feedback.Alert(c, m.err, feedback.AlertProps{Type: feedback.Error, Variant: feedback.Soft})
			case len(m.entries) == 0:
				a.placeholder(c, t, "没有冲突 — 所有文件都已解决。")
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
				a.placeholder(c, t, "正在加载 Reflog…")
			case m.err != "":
				feedback.Alert(c, m.err, feedback.AlertProps{Type: feedback.Error, Variant: feedback.Soft})
			case len(m.entries) == 0:
				a.placeholder(c, t, "没有 Reflog 记录。")
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
					ui.Text(c, relTime(e.When)).FontSize(11).TextColor(t.TextMuted)
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

// --- 分支/变基弹窗 -----------------------------------------------------

// branchDialog collects a branch name or rebase target.
func (a *app) branchDialog(c *ui.Context, t *ui.Theme) {
	title := "新建分支"
	okText := "创建"
	hint := "从 " + shortOID(a.newBranchFrom) + " 创建分支"
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
				a.blameView(c, t)
				return
			}
			a.fileHistListView(c, t)
		})
	})
}

// fileHistListView: commits touching the file + diff for the pick.
func (a *app) fileHistListView(c *ui.Context, t *ui.Theme) {
	m := &a.fileHist
	ui.Row(c).Fill().MinHeight(440).Children(func() {
		// Left: commit list.
		ui.Column(c).Width(320).Fill().Gap(0).Children(func() {
			switch {
			case m.loading:
				a.placeholder(c, t, "正在加载历史…")
			case m.err != "":
				feedback.Alert(c, m.err, feedback.AlertProps{Type: feedback.Error, Variant: feedback.Soft})
			case len(m.commits) == 0:
				a.placeholder(c, t, "无历史记录。")
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
				a.placeholder(c, t, "正在加载差异…")
			case m.diffErr != "":
				feedback.Alert(c, m.diffErr, feedback.AlertProps{Type: feedback.Error, Variant: feedback.Soft})
			case m.diff == nil:
				a.placeholder(c, t, "选择提交查看变更。")
			case m.diff.Binary:
				a.placeholder(c, t, "二进制文件。")
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
		Background(rowBg(t, sel)).
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
					ui.Text(c, relTime(cm.When)).FontSize(11).TextColor(t.TextMuted)
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
		a.fhDiffState = &editor.DiffState{
			Old: editor.NewBuffer(string(d.Old)),
			New: editor.NewBuffer(string(d.New)),
		}
	}
	a.fhDiffState.Layout = editor.DiffUnified
	editor.DiffView(c, a.fhDiffState).Grow(1).FillWidth().MinHeight(400)
}

// blameView renders the line-by-line attribution.
func (a *app) blameView(c *ui.Context, t *ui.Theme) {
	m := &a.fileHist
	switch {
	case m.blameLoading:
		a.placeholder(c, t, "正在加载溯源…")
	case m.blameErr != "":
		feedback.Alert(c, m.blameErr, feedback.AlertProps{Type: feedback.Error, Variant: feedback.Soft})
	case len(m.blame) == 0:
		a.placeholder(c, t, "无溯源信息。")
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
				Background(blameColor(t, bl.OID)).
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
func blameColor(t *ui.Theme, oid string) ui.Color {
	if oid == "" {
		return ui.Transparent
	}
	var h uint32
	for i := 0; i < len(oid) && i < 8; i++ {
		h = h*31 + uint32(oid[i])
	}
	// Tints from the theme's status colors at low alpha so blame rows
	// read as grouped without competing with the text.
	palette := []ui.Color{
		t.Selection,
		t.Accent.Alpha(0.08),
		t.Success.Alpha(0.08),
		t.Warning.Alpha(0.08),
	}
	return palette[h%uint32(len(palette))]
}

// shortOID abbreviates a full hash for display.
func shortOID(oid string) string {
	if len(oid) > 7 {
		return oid[:7]
	}
	return oid
}

// relTime formats a commit time like IDEA's log ("5 分钟前", "昨天").
func relTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟前", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时前", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%d 天前", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		return t.Format("1月2日")
	}
	return t.Format("2006-01-02")
}

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
		if s.partial && len(a.commit.hunks) > 1 {
			a.hunkBarView(c, t)
		}
		switch {
		case s.selection == "":
			a.placeholder(c, t, s.emptyText)
		case s.loading:
			a.placeholder(c, t, "正在加载差异…")
		case s.err != "":
			a.placeholder(c, t, "差异加载失败："+s.err)
		case s.diff == nil:
			a.placeholder(c, t, "无差异可显示。")
		case s.diff.Binary:
			a.placeholder(c, t, "二进制文件 — 不显示差异。")
		default:
			a.diffView(c, t, s.diff)
		}
	})
}

// diffView builds or reuses the DiffState for the current selection.
func (a *app) diffView(c *ui.Context, t *ui.Theme, d *git.DiffInfo) {
	if a.diffState == nil {
		old, new := "", ""
		if d != nil {
			old, new = string(d.Old), string(d.New)
		}
		a.diffState = &editor.DiffState{
			Old: editor.NewBuffer(old),
			New: editor.NewBuffer(new),
		}
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

// placeholder centers helper text in the pane.
func (a *app) placeholder(c *ui.Context, t *ui.Theme, text string) {
	ui.Column(c).Fill().Center().Children(func() {
		ui.Text(c, text).TextColor(t.TextMuted)
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
					choice.Checkbox(c, &m.amend, "修改上一次提交", choice.CheckboxProps{Size: 14})
					choice.Checkbox(c, &m.noVerify, "跳过 Git hooks", choice.CheckboxProps{Size: 14})
					ui.Text(c, "").Grow(1)
					action.Button(c, "模板", action.ButtonProps{Variant: action.Text, Size: theme.Small}).
						Disabled(m.busy).OnClick(func() { m.loadTemplate(a.updater()) })
					action.Button(c, "检查", action.ButtonProps{Variant: action.Text, Size: theme.Small}).
						Disabled(m.busy).OnClick(func() {
						m.preChecksOpen = !m.preChecksOpen
						if m.preChecksOpen && !m.preChecksLoaded {
							m.loadPreChecks(a.updater())
						}
					})
					ui.Text(c, checkedCount(m.Snap, m.checks)).TextColor(t.TextMuted)
				})
				if m.preChecksOpen {
					a.preChecksView(c, t)
				}
			})
			ui.Column(c).Gap(6).Children(func() {
				action.Button(c, "提交", action.ButtonProps{Variant: action.Primary, Size: theme.Middle}).
					Disabled(m.busy).OnClick(a.doCommit).MinWidth(110)
				action.Button(c, "提交并推送", action.ButtonProps{Variant: action.Default, Size: theme.Middle}).
					Disabled(m.busy).OnClick(a.doCommitPush).MinWidth(110)
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

// checkedCount summarizes the selection for the bar.
func checkedCount(snap *git.Snapshot, cb checkboxes) string {
	if snap == nil {
		return "0 of 0"
	}
	checked := cb.checkedSet()
	total, n := 0, 0
	for _, r := range rowsFor(snap) {
		if r.change.Conflict() {
			continue
		}
		total++
		if checked[r.change.Path] {
			n++
		}
	}
	return fmt.Sprintf("%d of %d", n, total)
}

// statusTagColor maps file status to a Tag color.
func statusTagColor(k git.FileKind) display.TagColor {
	switch k {
	case git.KindUnmerged, git.KindDeleted:
		return display.TagError
	case git.KindUntracked, git.KindAdded:
		return display.TagSuccess
	case git.KindRenamed:
		return display.TagInfo
	case git.KindModified, git.KindTypechange:
		return display.TagWarning
	default:
		return display.TagGray
	}
}

// statusColor picks a row's status color (kept for non-tag uses).
func statusColor(t *ui.Theme, k git.FileKind) ui.Color {
	switch k {
	case git.KindUnmerged, git.KindDeleted:
		return t.Danger
	case git.KindUntracked, git.KindAdded:
		return t.Success
	default:
		return t.Text
	}
}

// statusGlyph uses IDEA's per-type letters.
func statusGlyph(k git.FileKind) string {
	switch k {
	case git.KindModified:
		return "M"
	case git.KindAdded:
		return "A"
	case git.KindDeleted:
		return "D"
	case git.KindRenamed:
		return "R"
	case git.KindTypechange:
		return "T"
	case git.KindUntracked:
		return "U"
	case git.KindUnmerged:
		return "C"
	}
	return "?"
}

// aheadBehind formats "+2 −1".
func aheadBehind(s *git.Snapshot) string {
	if s.Ahead < 0 && s.Behind < 0 {
		return ""
	}
	var parts []string
	if s.Ahead > 0 {
		parts = append(parts, "+"+fmt.Sprint(s.Ahead))
	}
	if s.Behind > 0 {
		parts = append(parts, "−"+fmt.Sprint(s.Behind))
	}
	return strings.Join(parts, " ")
}

// baseName is filepath.Base.
func baseName(p string) string {
	if p == "" {
		return ""
	}
	if i := strings.LastIndexByte(p, os.PathSeparator); i >= 0 {
		return p[i+1:]
	}
	return p
}

// --- 通知 toast ---------------------------------------------------------

// toast is one transient notification (IDEA's bottom-right bubble).
type toast struct {
	text  string
	kind  string // "info" | "success" | "error"
	until time.Time
}

// notify shows a transient notification via the framework toast stack.
// The context is captured lazily on the next view render because toasts
// may be queued outside a view pass (background job completion).
func (a *app) notify(kind, text string) {
	a.toasts = append(a.toasts, toast{
		text:  text,
		kind:  kind,
		until: time.Now().Add(4 * time.Second),
	})
	a.update(func() {})
}

// toastLayer flushes queued notifications into ui's toast system and
// renders the bottom-right stack (IDEA-style).
func (a *app) toastLayer(c *ui.Context, t *ui.Theme) {
	for _, ts := range a.toasts {
		c.AddToast(ui.Toast{Title: ts.text, Type: ts.kind, Timeout: 4 * time.Second})
	}
	a.toasts = nil
	ui.ToastViewportBase(c, func(viewport ui.Element, toasts []ui.Toast) {
		viewport.Padding(16).AlignItems(ui.End).Gap(8)
		for _, ts := range toasts {
			tp := ui.ToastBase(c, ts)
			color := t.TextMuted
			icon := "ℹ"
			switch ts.Type {
			case "success":
				color = t.Success
				icon = "✓"
			case "error":
				color = t.Danger
				icon = "✕"
			}
			tp.Root.Row().Gap(10).Padding(10, 14).Radius(8).Width(340).
				Background(t.Inverse).Shadow(0, 4, 16, 0, ui.RGBA(0, 0, 0, 0.4))
			tp.Root.Children(func() {
				ui.Text(c, icon).TextColor(color)
				ui.Text(c, ts.Title).Grow(1)
				tp.CloseButton().Label("关闭").Children(func() {
					ui.Text(c, "×").TextColor(t.TextMuted)
				})
			})
		}
	})
}
