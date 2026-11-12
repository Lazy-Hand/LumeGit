package main

import (
	"context"
	"fmt"
	"os/exec"
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

	"lumegit/components"
	"lumegit/internal/git"
	"lumegit/internal/store"
	"lumegit/internal/vcs"
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
	filesPaneW float32
	histListW  float32 // history: commit-list pane width
	histFilesW float32 // history: file-list pane width
	shelfListW float32 // shelf: stash-list pane width
	shelfFileW float32 // shelf: file-list pane width
	// (branchListW removed: the branches popup replaced the list pane)
	diffSplit float32
	diffMode  int // 0 = split, 1 = unified

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

	// svn "新建分支" dialog — copies current URL → branches/<name>.
	svnNewBranchOpen bool
	svnNewBranchName string
	svnNewBranchErr  string
	svnNewBranchRev  int64 // >0: pin the copy to this rev (history right-click)

	// "推送此前所有提交…" dialog (IDEA: Push up to here).
	pushUpToOpen  bool
	pushUpToOID   string // last commit to include
	pushUpToShort string // display-only short OID

	// Diff state for the current file, rebuilt per selection.
	diffState    *editor.DiffState
	diffStateFor *git.DiffInfo     // content identity; refreshes can replace a diff for the same path
	fhDiffState  *editor.DiffState // separate state for the file-history overlay
	cmpDiffState *editor.DiffState // separate state for the compare overlay

	// fileList virtualizes the grouped changes list, including headers.
	fileList ui.ListState

	// settingsOpen drives the 偏好设置 modal (menu: LumeGit → 偏好设置…,
	// CmdOrCtrl+,).
	settingsOpen bool
	// Git identity fields in the settings modal, loaded when it opens.
	gitName, gitEmail string // editable local user.name / user.email
	gitGlobalName     string // read-only global fallback, shown as hint
	gitGlobalEmail    string
	gitIdentityErr    string
	gitIdentityLoaded bool
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

// loadSettings applies persisted UI prefs at window start. Every field
// guards a zero value so a missing/old settings file falls back to the
// built-in defaults, and a window reopened from the picker keeps the
// last session's layout instead of the fresh-window constants.
func (a *app) loadSettings() {
	mgr := sharedSettings()
	if mgr == nil {
		// Config dir unreadable: run with defaults, don't block startup.
		a.filesPaneW, a.histListW, a.histFilesW = 280, 380, 260
		a.shelfListW, a.shelfFileW = 300, 240
		return
	}
	s := mgr.Get()
	a.filesPaneW = orFloat(s.FilesPaneW, 280)
	a.histListW = orFloat(s.HistListW, 380)
	a.histFilesW = orFloat(s.HistFilesW, 260)
	a.shelfListW = orFloat(s.ShelfListW, 300)
	a.shelfFileW = orFloat(s.ShelfFileW, 240)
	a.diffMode = s.DiffMode
	a.diffSplit = s.DiffSplit
	a.tab = s.Tab
	a.commit.mode = s.CommitMode
}

// saveSettings writes the current UI prefs. Called on window close;
// the store serializes the write so several windows exiting at once
// can't tear the file.
func (a *app) saveSettings() {
	mgr := sharedSettings()
	if mgr == nil {
		return
	}
	_ = mgr.Save(store.Settings{
		DiffMode:   a.diffMode,
		DiffSplit:  a.diffSplit,
		FilesPaneW: a.filesPaneW,
		HistListW:  a.histListW,
		HistFilesW: a.histFilesW,
		ShelfListW: a.shelfListW,
		ShelfFileW: a.shelfFileW,
		Tab:        a.tab,
		CommitMode: a.commit.mode,
	})
}

// orFloat returns def when v is zero (settings not set yet).
func orFloat(v, def float32) float32 {
	if v == 0 {
		return def
	}
	return v
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
	// LFS data load (cheap after first fetch; svn has no LFS — skip so
	// an svn WC never sees "git lfs" errors in the corner).
	if !a.commit.isSVN() {
		a.commit.loadLFS(a.updater())
	}
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
	if a.svnNewBranchOpen {
		a.svnNewBranchDialog(c, t)
	}
	// svn:ignore 列表（svn 没有 .gitignore，属性散在各目录上）。
	if a.commit.ignoreOpen {
		a.svnIgnoreDialog(c, t)
	}
	// Branch-compare overlay.
	if a.compare.open {
		a.compareOverlay(c, t)
	}
	// Go-to commit/tag dialog.
	if a.history.gotoOpen {
		a.gotoDialogView(c, t)
	}
	// Preferences on top of all panes (still under toasts).
	if a.settingsOpen {
		a.settingsView(c, t)
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
		snap, backend, cerr := openRepo(appCTX, openPath)
		w.update(func() {
			if gen != a.commit.gen {
				return
			}
			a.commit.setBusyQuiet()
			if cerr != nil {
				a.commit.openErr = cerr.Error()
				return
			}
			if a.commit.diffCancel != nil {
				a.commit.diffCancel()
				a.commit.diffCancel = nil
			}
			a.commit.diffGen++
			a.commit.selectedFile = ""
			a.commit.diff = nil
			a.commit.diffPrepared = nil
			a.commit.diffPath = ""
			a.commit.diffErr = ""
			a.commit.diffLoading = false
			a.commit.hunks = nil
			a.commit.hunkCheck = nil
			a.diffState = nil
			a.diffStateFor = nil
			a.fileList = ui.ListState{}
			a.pickPath = backend.Root
			a.commit.RepoPath = backend.Root
			a.commit.VCS = backend
			a.commit.Snap = snap
			a.commit.openErr = ""
			// -uno is a per-repo degradation — a flood in the last repo
			// must not hide this repo's untracked files.
			a.commit.preferNoUntracked = false
			a.history.reset()
			a.history.setBackend(backend)
			a.shelf.reset()
			a.shelf.setBackend(backend)
			a.branches.reset()
			a.branches.setBackend(backend)
			a.fileHist.reset()
			a.fileHist.setBackend(backend)
			a.conflict.setBackend(backend)
			a.conflicts.setBackend(backend)
			a.compare.setBackend(backend)
			if a.commit.repos != nil {
				a.commit.repos.Add(backend.Root, "", string(backend.Kind))
				a.commit.lists = a.commit.repos.ChangelistsFor(backend.Root)
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
			a.history.ensureLoaded(backend.Root, w)
			a.shelf.ensureLoaded(backend.Root, w)
			a.branches.ensureLoaded(backend.Root, w)
		})
	}()
}

// repoRowView: one repo in the left list.
func (a *app) repoRowView(c *ui.Context, r store.Repo) {
	selected := a.commit.RepoPath == r.Path
	row := ui.Row(c).Key("repo-"+r.Path).FillWidth().Padding(8, 16).Gap(8).AlignItems(ui.Center).
		Background(components.RowBg(c.Theme(), selected))
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
			// svn has no stash — hide the shelf tab on an svn WC so the
			// user can't land on a page that only runs git stash.
			if a.commit.isSVN() {
				action.Segmented(c, &a.tab, []string{"提交", "历史"}, action.SegmentedProps{Size: theme.Small})
				if a.tab > 1 {
					a.tab = 0
				}
			} else {
				action.Segmented(c, &a.tab, []string{"提交", "历史", "搁置"}, action.SegmentedProps{Size: theme.Small})
			}
		})
		if a.tab == 1 {
			a.historyView(c, t)
			return
		}
		if a.tab == 2 && !a.commit.isSVN() {
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
		ui.Text(c, components.BaseName(m.RepoPath)).Bold()
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
			branch := m.Snap.Head // git branch name, or svn's URL tail
			if gs := m.Snap.Git; gs != nil {
				if gs.Detached {
					// IDEA shows "DETACHED HEAD @ <oid>" and offers a
					// one-click "create branch here" — surface the same.
					branch = "DETACHED @ " + components.ShortOID(gs.HeadOID)
				}
				if gs.Unborn {
					branch += " (未提交)"
				}
			}
			// IDEA Branches popup: clicking the current-branch tag opens the
			// whole ref tree as a menu — checkout/merge/delete per branch.
			display.Tag(c, branch, display.TagProps{Variant: display.TagOutlined, Color: display.TagInfo}).
				Menu(func(mn *ui.Menu) { a.branchPopupMenu(mn) })
			if m.Snap.Rev != "" {
				display.Tag(c, m.Snap.Rev, display.TagProps{Variant: display.TagBorderless, Color: display.TagGray})
			}
			if gs := m.Snap.Git; gs != nil {
				if gs.Detached {
					action.Button(c, "建分支", action.ButtonProps{Variant: action.Text, Size: theme.Small}).
						OnClick(func() {
							a.newBranchFrom = ""
							a.branchDialogOpen = true
						})
				}
				if ab := components.AheadBehind(gs.Ahead, gs.Behind); ab != "" {
					display.Tag(c, ab, display.TagProps{Variant: display.TagBorderless})
				}
			}
			if m.Snap.Detail != "" {
				display.Tag(c, m.Snap.Detail, display.TagProps{Variant: display.TagBorderless, Color: display.TagWarning})
			}
			if gs := m.Snap.Git; gs != nil && gs.InProgress != "" {
				display.Tag(c, "进行中："+gs.InProgress, display.TagProps{Variant: display.TagFilled, Color: display.TagWarning})
				// 全部冲突解决后可继续；任何时候可中止。
				prog := gs.Progress()
				if !prog.CanContinue && len(gs.Conflicts()) > 0 {
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
		if m.isSVN() {
			// svn: commit IS the push — the only remote op is update.
			action.Button(c, "更新", action.ButtonProps{Variant: action.Text, Size: theme.Small}).
				Disabled(m.busy).OnClick(func() { a.commit.svnUpdate(appCTX, a.updater()) })
			// svn has no .gitignore file to open — the ignore rules
			// live in per-directory props, so the app lists them.
			action.Button(c, "忽略…", action.ButtonProps{Variant: action.Text, Size: theme.Small}).
				Disabled(m.busy).OnClick(func() {
				m.ignoreOpen = true
				m.svnLoadIgnores(appCTX, a.updater())
			})
		} else {
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
		}
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
				m.Snap = vcs.WrapGit(snap)
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
				m.Snap = vcs.WrapGit(snap)
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
			if m.isSVN() {
				// svn has no staging index — the 更改 (staging) view is a
				// git concept; lock the picker on 列表.
				m.mode = 0
				action.Segmented(c, &m.mode, []string{"列表"}, action.SegmentedProps{Size: theme.Small})
			} else {
				action.Segmented(c, &m.mode, []string{"列表", "更改"}, action.SegmentedProps{Size: theme.Small})
			}
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
		rows := a.filePaneRows()
		a.fileList.Key = func(i int) any { return rows[i].key }
		ui.List(c, &a.fileList, len(rows), func(i int) {
			row := rows[i]
			switch row.kind {
			case paneListHeader:
				a.listGroupView(c, t, row.group)
			case paneStagingHeader:
				a.stagingSection(c, t, row.title, row.files, row.groupKey)
			case paneStagingFile:
				a.stagingRowView(c, t, row.file.change)
			default:
				a.fileRowView(c, t, row.file)
			}
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

type filePaneRowKind uint8

const (
	paneListFile filePaneRowKind = iota
	paneListHeader
	paneStagingHeader
	paneStagingFile
)

// filePaneRow keeps headers and files in one virtual list. Only visible
// rows are built, but header actions retain the complete group contents.
type filePaneRow struct {
	key      string
	kind     filePaneRowKind
	file     fileRow
	group    listGroup
	title    string
	groupKey string
	files    []git.FileChange
}

func (a *app) filePaneRows() []filePaneRow {
	m := &a.commit
	if m.collapsed == nil {
		m.collapsed = make(map[string]bool)
	}
	rows := make([]filePaneRow, 0)
	if m.filter != "" {
		for _, row := range a.visibleRows() {
			rows = append(rows, filePaneRow{key: "file:" + row.change.Path, file: row})
		}
		return rows
	}
	if m.mode == 1 {
		if m.Snap == nil {
			return rows
		}
		var tracked, untracked []git.FileChange
		for _, f := range m.Snap.Files {
			if f.Kind == git.KindUntracked {
				untracked = append(untracked, f)
			} else {
				tracked = append(tracked, f)
			}
		}
		appendSection := func(title, key string, files []git.FileChange) {
			rows = append(rows, filePaneRow{key: "staging-header:" + key, kind: paneStagingHeader, title: title, groupKey: key, files: files})
			if !m.collapsed["stg-"+key] {
				for _, f := range files {
					rows = append(rows, filePaneRow{key: "staging-file:" + f.Path, kind: paneStagingFile, file: fileRow{change: f}})
				}
			}
		}
		appendSection("更改", "changes", tracked)
		appendSection("未进行版本管理的文件", "unversioned", untracked)
		return rows
	}
	for _, group := range m.groups() {
		if len(group.Rows) == 0 && group.Name != store.DefaultName {
			continue
		}
		rows = append(rows, filePaneRow{key: "list-header:" + group.Name, kind: paneListHeader, group: group})
		if !m.collapsed[group.Name] {
			for _, row := range group.Rows {
				rows = append(rows, filePaneRow{key: "file:" + row.change.Path, file: row})
			}
		}
	}
	return rows
}

// stagingSection draws a group header. groupKey is a stable id for
// collapse + group-checkbox state.
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
}

// stagingRowView: checkbox (include in commit) + click-to-diff. An AM
// file shows "A+M" so the user sees the file carries both staged and
// unstaged parts; its diff is HEAD → worktree (the full change).
func (a *app) stagingRowView(c *ui.Context, t *ui.Theme, ch git.FileChange) {
	m := &a.commit
	path := ch.Path
	cb := m.checks.checkboxFor(path)
	rowEl := ui.Row(c).Key("sfile-"+path).FillWidth().Padding(6, 12).Gap(10).
		Background(components.RowBg(t, m.selectedFile == path)).
		AlignItems(ui.Center)
	rowEl.Children(func() {
		choice.Checkbox(c, cb, "", choice.CheckboxProps{Size: 14}).Disabled(ch.Conflict())
		display.Tag(c, components.StatusGlyph(ch.Kind), display.TagProps{
			Variant: display.TagBorderless,
			Color:   components.StatusTagColor(ch.Kind),
		})
		label := path
		if ch.OrigPath != "" {
			label = ch.OrigPath + " → " + path
		}
		ui.Text(c, label).SingleLine().
			TextColor(components.RowLabelColor(t, m.selectedFile == path)).
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
		isSVN := m.isSVN()
		untracked := ch.Kind == git.KindUntracked
		if ch.Conflict() {
			if mn.Item("解决冲突…").Chosen() {
				a.conflict.openFor(m.RepoPath, path, a.updater())
			}
			mn.Separator()
		}
		if isSVN {
			// svn: 无 staging/stash/gitignore —— add/revert/ignore/delete。
			if untracked {
				if mn.Item("加入版本控制（svn add）").Chosen() {
					m.svnAdd(appCTX, a.updater(), path)
				}
				if mn.Item("加入忽略（svn:ignore）").Chosen() {
					m.svnIgnoreAdd(appCTX, a.updater(), path)
				}
				if mn.Item("删除…").Chosen() {
					a.askDelete(path)
				}
			} else {
				if mn.Item("回滚（svn revert）…").Chosen() {
					a.askRevert([]string{path}, false)
				}
				if mn.Item("删除并移出版本控制…").Chosen() {
					a.askDelete(path)
				}
			}
		} else if untracked {
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
		if !(isSVN && untracked) {
			if mn.Item("查看历史").Chosen() {
				a.fileHist.openFor(m.RepoPath, path, a.updater())
			}
			if mn.Item("查看溯源（Blame）").Chosen() {
				a.fileHist.mode = 1
				a.fileHist.openFor(m.RepoPath, path, a.updater())
			}
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

// listGroupView draws one collapsible changelist header.
func (a *app) listGroupView(c *ui.Context, t *ui.Theme, g listGroup) {
	m := &a.commit
	if m.collapsed == nil {
		m.collapsed = map[string]bool{}
	}
	if len(g.Rows) == 0 && g.Name != store.DefaultName {
		return // empty named lists hide (IDEA collapses them away)
	}
	name := g.Name
	readonly := name == "已忽略" // svn ignored 组：只展示，无勾选/提交
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
		if !readonly {
			bind := m.groupCheckBind(name)
			m.syncGroupCheck(name, g.CheckedN, len(g.Rows)) // keep in step pre-click
			partial := g.CheckedN > 0 && g.CheckedN < len(g.Rows)
			choice.Checkbox(c, bind, "", choice.CheckboxProps{Size: 12, Indeterminate: partial}).
				OnClick(func() {
					m.checkGroup(name, *bind) // *bind is now the new state
				})
		}
		label := name
		if g.Active {
			label += "（活动）"
		}
		ui.Text(c, label).Bold().FontSize(12).SingleLine().Grow(1)
		ui.Text(c, fmt.Sprintf("%d", len(g.Rows))).FontSize(11).TextColor(t.TextMuted)
	})
	if !readonly {
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
	}
}

// visibleRows is the displayed subset (filter applied).
func (a *app) visibleRows() []fileRow {
	rows := a.commit.fileRows()
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
	ignored := ch.Kind == vcs.KindIgnored
	rowEl := ui.Row(c).Key("file-"+path).FillWidth().Padding(6, 12).Gap(10).
		Background(components.RowBg(t, a.commit.selectedFile == path)).
		AlignItems(ui.Center)
	rowEl.Children(func() {
		if ignored {
			// 已忽略行不占提交名额 —— 留空让标签对齐。
			ui.Text(c, "").Width(14)
		} else {
			cb := m.checks.checkboxFor(path)
			choice.Checkbox(c, cb, "", choice.CheckboxProps{Size: 14}).Disabled(ch.Conflict())
		}
		display.Tag(c, components.StatusGlyph(ch.Kind), display.TagProps{
			Variant: display.TagBorderless,
			Color:   components.StatusTagColor(ch.Kind),
		})
		label := path
		if ch.OrigPath != "" {
			label = ch.OrigPath + " → " + path
		}
		// The path text is the click target; conflicts open the resolver
		// instead of a diff that can't render three sides.
		ui.Text(c, label).SingleLine().
			TextColor(components.RowLabelColor(t, a.commit.selectedFile == path)).
			OnClick(func() {
				if ignored {
					return // no diff for ignored paths — nothing tracked
				}
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
		isSVN := m.isSVN()
		untracked := ch.Kind == vcs.KindUntracked
		if ignored {
			// 取消忽略：删掉匹配该文件的 svn:ignore 条目。
			if mn.Item("取消忽略").Chosen() {
				m.svnUnignorePath(appCTX, a.updater(), path)
			}
			mn.Separator()
			if mn.Item("在 Finder 中显示").Chosen() {
				a.revealPath(path)
			}
			if mn.Item("复制路径").Chosen() {
				a.copyPath(c, path)
			}
			return
		}
		if ch.Conflict() {
			if mn.Item("解决冲突…").Chosen() {
				a.conflict.openFor(m.RepoPath, path, a.updater())
			}
			mn.Separator()
		}
		// File ops.
		if isSVN {
			// svn: no staging/stash/gitignore — add, revert, delete only.
			if untracked {
				if mn.Item("加入版本控制（svn add）").Chosen() {
					m.svnAdd(appCTX, a.updater(), path)
				}
				if mn.Item("加入忽略（svn:ignore）").Chosen() {
					m.svnIgnoreAdd(appCTX, a.updater(), path)
				}
				if mn.Item("删除…").Chosen() {
					a.askDelete(path)
				}
			} else {
				if mn.Item("回滚（svn revert）…").Chosen() {
					a.askRevert([]string{path}, false)
				}
				if mn.Item("删除并移出版本控制…").Chosen() {
					a.askDelete(path)
				}
			}
		} else if untracked {
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
		// 历史/溯源只在版本控制下有内容 —— svn 未版本化路径点开只
		// 是 E155010 报错，不如不显示。
		if !(isSVN && untracked) {
			if mn.Item("查看历史").Chosen() {
				a.fileHist.openFor(m.RepoPath, path, a.updater())
			}
			if mn.Item("查看溯源（Blame）").Chosen() {
				a.fileHist.mode = 1
				a.fileHist.openFor(m.RepoPath, path, a.updater())
			}
		}
		mn.Separator()
		if mn.Item("在 Finder 中显示").Chosen() {
			a.revealPath(path)
		}
		if mn.Item("复制路径").Chosen() {
			a.copyPath(c, path)
		}
		if !isSVN && mn.Item("创建补丁…").Chosen() {
			a.exportWorktreePatch(path)
		}
		// IDEA's "move to another changelist" only applies to tracked
		// changes — untracked files aren't changelist members.
		if !isSVN && m.lists != nil && !untracked {
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
		run := ask.Run
		m.pendingRevert = revertAsk{}
		switch {
		case run != nil:
			run()
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

// askRun opens a confirm dialog whose OK runs fn — for destructive ops
// that aren't file-scoped reverts (hard reset, stash drop, …).
func (a *app) askRun(title, body string, fn func()) {
	a.commit.pendingRevert = revertAsk{Open: true, Title: title, Body: body, Run: fn}
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

// selectRow records the pick and schedules a fresh diff load. Picking
// the already-loaded/loading file is a no-op so rapid clicks don't
// thrash the diff worker or rebuild the view.
func (a *app) selectRow(ch git.FileChange) {
	if a.commit.selectedFile == ch.Path {
		return // same row: loading or loaded, the pick didn't change
	}
	a.diffState = nil // rebuild DiffView state for the new file
	a.commit.selectFile(ch)
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
	components.ToastStack(c, t)
}
