package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"lumegit/internal/git"
	"lumegit/internal/store"
)

// commitModel holds all workspace state for the Commit tab of one
// window. Everything UI-visible lives here; the view function reads it
// rebuild-time, and background jobs publish changes through
// publish (set to win.Update in main.go).
type commitModel struct {
	RepoPath string // empty = no repo open yet

	// Repo manager: recent + pinned repositories.
	repos *store.Manager

	// Mode: 0 = Changelist (勾选=提交范围, our stage+commit);
	// 1 = Git Staging (index is the commit, files split by
	// staged/unstaged, explicit stage/unstage actions).
	mode int

	// Changelist grouping for the current repo (from store, reconciled
	// against the live snapshot each refresh).
	lists *store.Changelists
	// collapsed holds changelist names the user folded in the UI.
	collapsed map[string]bool
	// groupChecks holds one *bool per changelist for the group
	// checkbox's bound value (map values aren't addressable).
	groupChecks map[string]*bool
	// newListOpen drives the inline "new changelist" input.
	newListOpen bool
	newListName string
	// moveTo holds the file path awaiting a changelist pick.
	moveTo string

	// Clone dialog state.
	cloneOpen   bool
	cloneURL    string
	cloneDest   string
	cloneBranch string
	cloneDepth  string // "0" = full clone; "1","5","50"…
	cloneSubMod bool   // --recurse-submodules
	cloneBusy   bool
	cloneErr    string

	// "新建仓库" init dialog (IDEA: Get from VCS → New).
	initOpen bool
	initPath string
	initErr  string
	initBusy bool

	// Latest snapshot. Immutable once published; replaced wholesale.
	Snap *git.Snapshot

	// File list filter text.
	filter string

	// Selection state. checks holds per-path checkbox bindings
	// (ui.Checkbox binds *bool; map entries aren't addressable);
	// selectedFile is the diff shown on the right.
	checks       checkboxes
	selectedFile string

	// Diff for the selected file, as displayed.
	diffLoading bool
	diff        *git.DiffInfo
	diffErr     string

	// Commit draft per repo (single-repo first version: plain fields).
	message  string
	amend    bool
	noVerify bool // --no-verify (IDEA's "Skip Git hooks")

	// Pre-commit checks drawer (IDEA's "Checks" panel). Lazily loaded;
	// refreshed on file selection or explicit refresh.
	preChecks       *git.PrecommitChecks
	preChecksLoad   bool
	preChecksOpen   bool // drawer expanded
	preChecksLoaded bool // we've fetched at least once

	// Operation state.
	busy     bool
	busyText string
	lastOut  *git.CommitOutcome
	lastErr  string
	syncOK   string // transient success note from fetch/pull/push

	// Generation counter: cancels stale async results after a repo
	// switch or refresh.
	gen int
	// diffGen fences diff workers only, so picking a new file doesn't
	// poison the shared gen used by refresh/stage/sync.
	diffGen int
	// diffCancel aborts the previous file's diff worker so a rapid
	// re-pick doesn't queue stale git subprocesses.
	diffCancel context.CancelFunc

	// publish posts a state-changing closure onto the UI thread. Set
	// by main.go (win.Update); headless tests substitute their own.
	publish func(fn func())

	// deadCtx cancels background git work for this window.
	deadCtx context.CancelFunc

	// openErr records a failed repo open for the picker UI.
	openErr string

	// pendingRevert holds the paths awaiting a destructive-action
	// confirmation (revert / delete). The modal confirms then calls the
	// stored action.
	pendingRevert revertAsk

	// watchCancel stops the filesystem watcher for the current repo.
	// nil while no repo is open or the watcher failed to start.
	watchCancel context.CancelFunc

	// quietRunning throttles refreshQuiet so a burst of fsnotify events
	// doesn't pile up concurrent Snapshot workers and their git fds.
	quietRunning bool

	// pushDlg backs the Push confirmation dialog (IDEA: push preview).
	pushDlg pushDialog

	// rebaseDlg backs the interactive-rebase plan panel.
	rebaseDlg rebaseDialog

	// hunkSel tracks the selected file's hunk checkboxes for partial
	// commits (IDEA: tick the chunks you want in the commit). Loaded
	// alongside the diff; a nil/empty map means "whole file".
	hunks     []git.Hunk
	hunkCheck []bool

	// pullDlg backs IDEA's "Update Project" dialog (merge/rebase + stash).
	pullDlg pullDialog

	// LFS state — lazily loaded once per repo; LFSTracked.Patterns nil
	// when the repo doesn't use LFS or git-lfs isn't installed.
	lfs          *git.LFSTracked
	lfsLocks     []git.LFSLock
	lfsLocksLoad bool
	lfsLocksOpen bool // locks dialog
	lfsLoaded    bool

	// bisect state (IDEA's Git → Bisect panel).
	bisect     *git.BisectStatus
	bisectOpen bool
	bisectGood string // start dialog input
	bisectBad  string
	bisectErr  string
	bisectBusy bool
}

// rebaseDialog is the "Interactively Rebase from Here" plan state.
type rebaseDialog struct {
	Open    bool
	Onto    string // rebase target commit (exclusive)
	Entries []git.RebaseEntry
	Err     string
}

// pullDialog holds Update Project's options.
type pullDialog struct {
	Open      bool
	Rebase    bool // pull --rebase vs merge
	Autostash bool // stash dirty worktree first
	Dirty     bool // snapshot said the tree is dirty
	Upstream  string
	Branch    string
	Err       string
}

// pushDialog holds the state for the Push modal: what will be sent
// and the user's choices. IDEA's dialog shows the target, the commit
// list, and force/tag toggles before anything leaves the machine.
type pushDialog struct {
	Open        bool
	Loading     bool
	Upstream    string // target ref, or "" when none
	HasUpstream bool
	Diverged    bool // ahead+behind both >0: history rewritten (amend/rebase)
	Commits     []git.Commit
	Force       bool // --force-with-lease
	PushTags    bool
	Err         string
	afterCommit bool // opened from "提交并推送": commit first
}

// revertAsk is the data behind the "are you sure" dialog for
// destructive worktree actions.
type revertAsk struct {
	Open   bool
	Title  string
	Body   string
	Staged bool     // revert staged side vs worktree side
	Paths  []string // for revert
	Delete string   // single path for delete (untracked)
}

// fileRow is one row of the changed-files list.
type fileRow struct {
	change   git.FileChange
	checked  bool
	selected bool
	list     string // changelist name this file belongs to
}

// listGroup is one collapsible changelist section in the UI.
type listGroup struct {
	Name     string
	Active   bool
	Rows     []fileRow
	CheckedN int // how many rows are ticked
}

// groups organizes the visible rows into their changelists, in order:
// named lists first (creation order), Default last so unmatched files
// always show at the bottom — IDEA-style "everything else" bucket.
func (m *commitModel) groups() []listGroup {
	rows := rowsFor(m.Snap)
	if m.lists == nil {
		// No store loaded: single synthetic group holds everything.
		g := listGroup{Name: store.DefaultName, Active: true, Rows: rows}
		for _, r := range rows {
			if m.checks.byPath[r.change.Path] != nil && m.checks.byPath[r.change.Path].checked {
				g.CheckedN++
			}
		}
		return []listGroup{g}
	}
	byList := map[string][]fileRow{}
	checked := m.checks.checkedSet()
	for _, r := range rows {
		name := m.lists.ListOf(r.change.Path)
		r.list = name
		byList[name] = append(byList[name], r)
	}
	var out []listGroup
	for _, l := range m.lists.Lists {
		g := listGroup{Name: l.Name, Active: l.Active, Rows: byList[l.Name]}
		for _, r := range g.Rows {
			if checked[r.change.Path] {
				g.CheckedN++
			}
		}
		out = append(out, g)
	}
	// Default list last.
	g := listGroup{Name: m.lists.Default, Active: m.lists.Active() == m.lists.Default, Rows: byList[m.lists.Default]}
	for _, r := range g.Rows {
		if checked[r.change.Path] {
			g.CheckedN++
		}
	}
	out = append(out, g)
	return out
}

// reconcileLists drops assignments for paths no longer in the snapshot
// and auto-assigns brand-new paths to the active list (IDEA semantics:
// new changes land on the active task group).
func (m *commitModel) reconcileLists() {
	if m.lists == nil || m.Snap == nil {
		return
	}
	live := map[string]bool{}
	for _, f := range m.Snap.Files {
		live[f.Path] = true
	}
	m.lists.Reconcile(live)
	// Auto-assign new files to the active list. A path with no
	// assignment belongs to Default already, so only move files that
	// were previously assigned elsewhere and now reappear — leave
	// Default files in Default (that's the catch-all).
	active := m.lists.Active()
	if active == m.lists.Default {
		return
	}
	for _, f := range m.Snap.Files {
		if m.lists.ListOf(f.Path) == m.lists.Default {
			m.lists.Assign(f.Path, active)
		}
	}
}

// rows builds the display order: conflicts first, then by category
// (renames/mods/added/deleted), then untracked; alphabetical inside
// each group. IDEA-style ordering: files that block commit surface
// first.
func rowsFor(snap *git.Snapshot) []fileRow {
	if snap == nil {
		return nil
	}
	kindRank := func(k git.FileKind) int {
		switch k {
		case git.KindUnmerged:
			return 0
		case git.KindModified, git.KindTypechange:
			return 1
		case git.KindRenamed:
			return 2
		case git.KindAdded:
			return 3
		case git.KindDeleted:
			return 4
		case git.KindUntracked:
			return 5
		}
		return 6
	}
	rows := make([]fileRow, 0, len(snap.Files))
	for _, f := range snap.Files {
		rows = append(rows, fileRow{change: f})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		ri, rj := rows[i].change, rows[j].change
		if kindRank(ri.Kind) != kindRank(rj.Kind) {
			return kindRank(ri.Kind) < kindRank(rj.Kind)
		}
		return ri.Path < rj.Path
	})
	return rows
}

// openRepo validates a directory as a git worktree and loads the first
// snapshot. Runs on a worker; the UI thread calls this via win.Update
// publication only.
func openRepo(ctx context.Context, dir string) (*git.Snapshot, string, error) {
	if _, err := os.Stat(dir); err != nil {
		return nil, "", fmt.Errorf("目录不存在：%v", err)
	}
	if cmd, err := exec.LookPath("git"); err != nil {
		// Pin a precise error for the UI.
		return nil, "", fmt.Errorf("找不到 git 可执行文件（查找路径 %q）", cmd)
	}
	repo, err := git.Open(ctx, dir)
	if err != nil {
		return nil, "", err
	}
	snap, err := repo.Snapshot(ctx)
	if err != nil {
		return nil, repo.Worktree, err
	}
	return snap, repo.Worktree, nil
}

// refresh loads a fresh snapshot of the current repo. Generation
// fencing: results from an older gen are dropped on arrival.
func (m *commitModel) refresh(ctx context.Context, w updater) {
	if m.RepoPath == "" {
		return
	}
	gen := m.gen
	m.setBusy(w, true, "正在获取状态…")
	go func() {
		repo := git.NewRepo(m.RepoPath)
		snap, err := repo.Snapshot(ctx)
		w.update(func() {
			if gen != m.gen {
				return // repo switched or superseded
			}
			m.setBusyQuiet()
			if err != nil {
				m.lastErr = err.Error()
				m.Snap = nil
			} else {
				m.lastErr = ""
				m.Snap = snap
				m.reconcileLists()
				m.pruneChecked()
				m.ensureSelectedFile()
			}
		})
	}()
}

// startWatch launches the fsnotify watcher for the open repo; a no-op
// if one is already running or the repo isn't open. Watch failures
// aren't fatal (the refresh button still works), so we don't surface
// them.
func (m *commitModel) startWatch(w updater) {
	if m.watchCancel != nil || m.RepoPath == "" {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.watchCancel = cancel
	repo := git.NewRepo(m.RepoPath)
	if err := repo.Watch(ctx, func() {
		// The watcher fires on its own goroutine; queue a refresh
		// through the normal update path so it joins gen fencing.
		w.update(func() {
			m.refreshQuiet(context.Background(), w)
		})
	}); err != nil {
		cancel()
		m.watchCancel = nil
	}
}

// refreshQuiet is refresh without the busy banner — background events
// shouldn't flash "正在获取状态…" over the UI. A quiet refresh already
// in flight is not stacked: the watcher fires bursts of events during
// one logical change, and spawning a Snapshot for each just burns fd.
func (m *commitModel) refreshQuiet(ctx context.Context, w updater) {
	if m.RepoPath == "" {
		return
	}
	if m.quietRunning {
		return // a refresh is already running — one is enough
	}
	m.quietRunning = true
	gen := m.gen
	go func() {
		repo := git.NewRepo(m.RepoPath)
		snap, err := repo.Snapshot(ctx)
		w.update(func() {
			m.quietRunning = false
			if gen != m.gen {
				return
			}
			if err != nil {
				m.lastErr = err.Error()
				m.Snap = nil
			} else {
				m.lastErr = ""
				m.Snap = snap
				m.reconcileLists()
				m.pruneChecked()
				m.ensureSelectedFile()
			}
		})
	}()
}

// stopWatch kills the watcher. Called when the repo closes.
func (m *commitModel) stopWatch() {
	if m.watchCancel != nil {
		m.watchCancel()
		m.watchCancel = nil
	}
}

// checkboxState carries the *bool ui.Checkbox binds for one path. Map
// index expressions aren't addressable, so we wrap.
type checkboxState struct {
	checked bool
}

// checkboxes holds one checkboxState per path, created on demand.
type checkboxes struct {
	byPath map[string]*checkboxState
}

// checkboxFor returns the bind target for path, creating it first time.
// The state must survive between frames (Element identity comes from
// the row Key; the *bool is the value holder).
func (cb *checkboxes) checkboxFor(path string) *bool {
	if cb.byPath == nil {
		cb.byPath = map[string]*checkboxState{}
	}
	st := cb.byPath[path]
	if st == nil {
		st = &checkboxState{}
		cb.byPath[path] = st
	}
	return &st.checked
}

// checkedSet returns the checked-path set for the commit job.
func (cb *checkboxes) checkedSet() map[string]bool {
	out := map[string]bool{}
	for p, st := range cb.byPath {
		if st.checked {
			out[p] = true
		}
	}
	return out
}

// revertPaths discards worktree changes for the given paths (git
// restore), then refreshes. Confirmed destructive: bytes never staged
// or committed are lost. For a staged path the caller may pass
// staged=true to also rewind the index to HEAD.
func (m *commitModel) revertPaths(ctx context.Context, w updater, paths []string, staged bool) {
	if m.busy || len(paths) == 0 {
		return
	}
	m.gen++
	gen := m.gen
	repoPath := m.RepoPath
	m.setBusy(w, true, "正在回滚…")
	go func() {
		repo := git.NewRepo(repoPath)
		var err error
		if staged {
			err = repo.RevertStagedPaths(ctx, paths)
		} else {
			err = repo.RevertPaths(ctx, paths)
		}
		snap, _ := repo.Snapshot(context.Background())
		w.update(func() {
			if gen != m.gen {
				return
			}
			m.setBusyQuiet()
			if err != nil {
				m.lastErr = "回滚失败：" + err.Error()
			} else {
				m.lastErr = ""
				m.syncOK = "已回滚"
				m.clearChecked(paths)
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

// deleteUntracked removes an untracked file (only allowed on untracked
// records — anything tracked goes through revert, which keeps the file).
func (m *commitModel) deleteUntracked(ctx context.Context, w updater, path string) {
	if m.busy || path == "" {
		return
	}
	m.gen++
	gen := m.gen
	repoPath := m.RepoPath
	m.setBusy(w, true, "正在删除…")
	go func() {
		repo := git.NewRepo(repoPath)
		err := repo.DeletePath(ctx, path)
		snap, _ := repo.Snapshot(context.Background())
		w.update(func() {
			if gen != m.gen {
				return
			}
			m.setBusyQuiet()
			if err != nil {
				m.lastErr = "删除失败：" + err.Error()
			} else {
				m.lastErr = ""
				m.syncOK = "已删除"
				if m.selectedFile == path {
					m.selectedFile = ""
					m.diff = nil
				}
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

// ignorePath appends a pattern to .gitignore and refreshes.
func (m *commitModel) ignorePath(ctx context.Context, w updater, path string) {
	if m.busy || path == "" {
		return
	}
	m.gen++
	gen := m.gen
	repoPath := m.RepoPath
	m.setBusy(w, true, "正在更新 .gitignore…")
	go func() {
		repo := git.NewRepo(repoPath)
		err := repo.AddToGitignore(ctx, path)
		snap, _ := repo.Snapshot(context.Background())
		w.update(func() {
			if gen != m.gen {
				return
			}
			m.setBusyQuiet()
			if err != nil {
				m.lastErr = "添加忽略失败：" + err.Error()
			} else {
				m.lastErr = ""
				m.syncOK = "已加入 .gitignore"
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

// shelvePaths stashes just the listed files (pathspec-limited stash push).
func (m *commitModel) shelvePaths(ctx context.Context, w updater, paths []string, includeUntracked bool) {
	if m.busy || len(paths) == 0 {
		return
	}
	m.gen++
	gen := m.gen
	repoPath := m.RepoPath
	m.setBusy(w, true, "正在搁置…")
	go func() {
		repo := git.NewRepo(repoPath)
		res, err := repo.StashPushPaths(ctx, "", paths, includeUntracked)
		if err == nil && !res.ExitOK() {
			err = &git.GitError{Args: res.Args, Stderr: res.OutputOfStderr()}
		}
		snap, _ := repo.Snapshot(context.Background())
		w.update(func() {
			if gen != m.gen {
				return
			}
			m.setBusyQuiet()
			if err != nil {
				m.lastErr = "搁置失败：" + err.Error()
			} else {
				m.lastErr = ""
				m.syncOK = "已搁置到 stash"
				m.clearChecked(paths)
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

// shelveToShelf writes the paths' diff to a patch on the shelf and
// reverts the worktree side — IDEA's true "Shelve" (patch-based,
// distinct from git stash).
func (m *commitModel) shelveToShelf(ctx context.Context, w updater, paths []string) {
	if m.busy || len(paths) == 0 {
		return
	}
	m.gen++
	gen := m.gen
	repoPath := m.RepoPath
	m.setBusy(w, true, "正在搁置到搁置架…")
	go func() {
		repo := git.NewRepo(repoPath)
		name := "shelf-" + time.Now().Format("0102-150405")
		err := repo.ShelvePaths(ctx, name, paths)
		snap, _ := repo.Snapshot(context.Background())
		w.update(func() {
			if gen != m.gen {
				return
			}
			m.setBusyQuiet()
			if err != nil {
				m.lastErr = "搁置失败：" + err.Error()
			} else {
				m.lastErr = ""
				m.syncOK = "已搁置到搁置架"
				m.clearChecked(paths)
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

// checkNone clears every checked path — used by "仅提交此变更列表"
// to scope the commit to a single changelist.
func (m *commitModel) checkNone() {
	for _, st := range m.checks.byPath {
		st.checked = false
	}
}

// clearChecked drops checkmarks for paths that just changed sides
// (staged/unstaged), so a batch selection doesn't outlive its action.
func (m *commitModel) clearChecked(paths []string) {
	for _, p := range paths {
		if st := m.checks.byPath[p]; st != nil {
			st.checked = false
		}
	}
}

// runOp executes one git operation that may leave the repo in a
// conflicted state (rebase/cherry-pick/revert/reset). The UI resolves
// conflicts through the normal conflict flow afterwards.
func (m *commitModel) runOp(ctx context.Context, w updater, busyText, doneText string,
	fn func(context.Context, *git.Repo) (*git.RunResult, error), after func()) {
	if m.busy || m.RepoPath == "" {
		return
	}
	m.gen++
	gen := m.gen
	repoPath := m.RepoPath
	m.setBusy(w, true, busyText)
	go func() {
		repo := git.NewRepo(repoPath)
		res, err := fn(ctx, repo)
		snap, _ := repo.Snapshot(context.Background())
		w.update(func() {
			if gen != m.gen {
				return
			}
			m.setBusyQuiet()
			switch {
			case err != nil:
				m.lastErr = err.Error()
				m.syncOK = ""
			case res != nil && !res.ExitOK():
				// Nonzero exit: conflict-pause or a real failure.
				stderr := strings.TrimSpace(res.OutputOfStderr())
				if snap != nil && (len(snap.Conflicts()) > 0 || snap.InProgress != "") {
					m.lastErr = "操作产生冲突 — 请在文件列表中解决"
				} else {
					m.lastErr = stderr
				}
				m.syncOK = ""
			default:
				m.lastErr = ""
				m.syncOK = doneText
			}
			if snap != nil {
				m.Snap = snap
				m.reconcileLists()
				m.pruneChecked()
				m.ensureSelectedFile()
			}
			if after != nil {
				after()
			}
		})
	}()
}

// rebase onto a branch/commit.
func (m *commitModel) rebase(ctx context.Context, w updater, onto string) {
	m.runOp(ctx, w, "正在变基…", "变基完成",
		func(ctx context.Context, r *git.Repo) (*git.RunResult, error) {
			return r.Rebase(ctx, onto)
		}, nil)
}

// cherryPick applies a commit.
func (m *commitModel) cherryPick(ctx context.Context, w updater, oid string) {
	m.runOp(ctx, w, "正在挑选…", "已应用提交",
		func(ctx context.Context, r *git.Repo) (*git.RunResult, error) {
			return r.CherryPick(ctx, oid)
		}, nil)
}

// revertCommit undoes a commit.
func (m *commitModel) revertCommit(ctx context.Context, w updater, oid string) {
	m.runOp(ctx, w, "正在还原…", "已还原提交",
		func(ctx context.Context, r *git.Repo) (*git.RunResult, error) {
			return r.Revert(ctx, oid)
		}, nil)
}

// resetTo moves HEAD to a commit (UI confirms mode first).
func (m *commitModel) resetTo(ctx context.Context, w updater, oid string, mode git.ResetMode) {
	m.runOp(ctx, w, "正在重置…", "已重置",
		func(ctx context.Context, r *git.Repo) (*git.RunResult, error) {
			return r.Reset(ctx, oid, mode)
		}, nil)
}

// groupCheckBind returns the *bool the group checkbox binds. Its
// value is the previous frame's checked state; the view compares it
// against the group's actual CheckedN each frame to decide the next
// toggle direction.
func (m *commitModel) groupCheckBind(name string) *bool {
	if m.groupChecks == nil {
		m.groupChecks = map[string]*bool{}
	}
	b := m.groupChecks[name]
	if b == nil {
		b = new(bool)
		m.groupChecks[name] = b
	}
	return b
}

// syncGroupCheck keeps the group checkbox's bound value in step with
// the rows' actual checked state (after checkGroup runs or refresh).
func (m *commitModel) syncGroupCheck(name string, checkedN, total int) {
	b := m.groupCheckBind(name)
	*b = checkedN == total && total > 0
}

// checkGroup ticks or unticks every row in one changelist.
func (m *commitModel) checkGroup(name string, on bool) {
	for _, g := range m.groups() {
		if g.Name != name {
			continue
		}
		for _, r := range g.Rows {
			m.checks.checkboxFor(r.change.Path)
			m.checks.byPath[r.change.Path].checked = on && !r.change.Conflict()
		}
	}
}

// checkGroupPaths ticks or unticks every file in one explicit set —
// the staging-mode group checkbox operates on the file slice the view
// passes, not on a staged/unstaged partition.
func (m *commitModel) checkGroupPaths(files []git.FileChange, on bool) {
	for _, f := range files {
		m.checks.checkboxFor(f.Path)
		m.checks.byPath[f.Path].checked = on && !f.Conflict()
	}
}

// checkedIn reports how many of files are ticked (drives the group
// checkbox's indeterminate/full state).
func (m *commitModel) checkedIn(files []git.FileChange) int {
	n := 0
	for _, f := range files {
		if st := m.checks.byPath[f.Path]; st != nil && st.checked {
			n++
		}
	}
	return n
}

// checkedPaths returns the ticked subset of files.
func (m *commitModel) checkedPaths(files []git.FileChange) []string {
	var out []string
	for _, f := range files {
		if st := m.checks.byPath[f.Path]; st != nil && st.checked {
			out = append(out, f.Path)
		}
	}
	return out
}

// moveFile assigns path to a changelist and saves.
func (m *commitModel) moveFile(path, list string) {
	if m.lists == nil {
		return
	}
	m.lists.Assign(path, list)
	m.saveLists()
}

// newList creates a changelist (optionally active) and saves.
func (m *commitModel) newList(name string, activate bool) {
	if m.lists == nil || name == "" {
		return
	}
	m.lists.Create(name)
	if activate {
		m.lists.SetActive(name)
	}
	m.saveLists()
}

// deleteList removes a changelist and saves.
func (m *commitModel) deleteList(name string) {
	if m.lists == nil {
		return
	}
	m.lists.Remove(name)
	m.saveLists()
}

// setActiveList marks one list active and saves.
func (m *commitModel) setActiveList(name string) {
	if m.lists == nil {
		return
	}
	m.lists.SetActive(name)
	m.saveLists()
}

// saveLists persists changelist metadata through the repo store.
func (m *commitModel) saveLists() {
	if m.repos != nil {
		_ = m.repos.Save() // changelists ride in the same repos.json
	}
}

// pruneChecked drops checkmarks whose file no longer appears in the
// snapshot (deleted elsewhere, renamed away, or committed outside).
func (m *commitModel) pruneChecked() {
	if m.checks.byPath == nil {
		return
	}
	for p := range m.checks.byPath {
		if m.Snap.Find(p) == nil {
			delete(m.checks.byPath, p)
		}
	}
}

// ensureSelectedFile keeps the selected file on the right in sync: if
// it vanished, select the first file, else clear the pane.
func (m *commitModel) ensureSelectedFile() {
	if m.selectedFile != "" && m.Snap.Find(m.selectedFile) != nil {
		return
	}
	if rows := rowsFor(m.Snap); len(rows) > 0 {
		m.selectFile(rows[0].change)
	} else {
		m.selectedFile = ""
		m.diff = nil
		m.diffErr = ""
	}
}

// selectFile records the selection and starts its diff load in the
// background. Rapid re-picks cancel the previous worker via a gen bump,
// so stale results can't overwrite the newest selection's diff and the
// UI doesn't flicker through an old file's content.
func (m *commitModel) selectFile(f git.FileChange) {
	if m.selectedFile == f.Path && !m.diffLoading && m.diff != nil {
		return // re-picking the same loaded file: nothing to do
	}
	m.diffGen++ // cancel any in-flight diff worker
	gen := m.diffGen
	if m.diffCancel != nil {
		m.diffCancel() // abort the previous file's git work
	}
	m.selectedFile = f.Path
	m.diff = nil
	m.diffErr = ""
	m.diffLoading = true
	path, origPath := f.Path, f.OrigPath
	repoPath := m.RepoPath
	w := updater{update: m.publish}
	ctx, cancel := context.WithCancel(context.Background())
	m.diffCancel = cancel

	go func() {
		defer cancel()
		d, derr := loadDiffCtx(ctx, repoPath, path, origPath, gen, m)
		// Load the file's hunks for partial-commit checkboxes — only for
		// a tracked modify (untracked/new/deleted files are all-or-nothing).
		var hunks []git.Hunk
		if derr == nil && origPath == "" {
			repo := git.NewRepo(repoPath)
			hunks, _ = repo.FileHunks(ctx, path)
		}
		w.update(func() {
			if gen != m.diffGen {
				return
			}
			m.diffLoading = false
			if derr != nil && d == nil {
				if m.selectedFile == path {
					m.diffErr = derr.Error()
					m.diff = nil
					m.hunks = nil
					m.hunkCheck = nil
				}
				return
			}
			if m.selectedFile == path {
				m.diff, m.diffErr = d, ""
				m.hunks = hunks
				m.hunkCheck = nil // reset: lazily default all-ticked on commit
			}
		})
	}()
}

// loadDiffCtx resolves one file's diff for the UI from a background
// goroutine; ctx cancels stale work.
func loadDiffCtx(ctx context.Context, repoPath, path, origPath string, wantGen int, m *commitModel) (*git.DiffInfo, error) {
	repo := git.NewRepo(repoPath)
	var d *git.DiffInfo
	var err error
	if origPath != "" {
		d, err = repo.FileDiffRename(ctx, path, origPath, git.TreeHead, git.TreeWorktree)
	} else {
		d, err = repo.FileDiff(ctx, git.FileDiffRequest{Path: path, From: git.TreeHead, To: git.TreeWorktree})
	}
	if err != nil {
		return nil, err
	}
	// A stale result is caught by the caller's gen check.
	_ = wantGen
	return d, nil
}

// commitJobs drives the actual commit. Sequential guarded job per the
// design notes' "explicit guarded jobs" rule: only one at a time,
// always terminated by a publication that clears busy.
func (m *commitModel) commitSelected(ctx context.Context, w updater) {
	if m.busy || m.RepoPath == "" {
		return
	}
	// Snapshot the checked set once: the job's inputs are fixed at
	// dispatch, later checkbox ticks belong to the NEXT commit.
	checked := m.checks.checkedSet()
	var paths, untracked []string
	rows := rowsFor(m.Snap)
	for i := range rows {
		row := &rows[i]
		if !checked[row.change.Path] {
			continue
		}
		if row.change.Kind == git.KindUntracked {
			untracked = append(untracked, row.change.Path)
		} else if row.change.Path != "" {
			paths = append(paths, row.change.Path)
		}
	}
	if len(paths) == 0 && len(untracked) == 0 {
		return
	}
	if m.message == "" && !m.amend {
		m.lastErr = "提交信息为空"
		return
	}
	m.gen++
	gen := m.gen
	repoPath := m.RepoPath
	msg := m.message
	amend := m.amend
	partial := m.partialSelection()
	m.setBusy(w, true, "正在提交…")

	go func() {
		repo := git.NewRepo(repoPath)
		out, cerr := repo.Commit(ctx, git.CommitRequest{
			Paths:     paths,
			Untracked: untracked,
			Partial:   partial,
			Message:   msg,
			Amend:     amend,
			NoVerify:  m.noVerify,
		})
		// Refresh regardless, so the UI reflects reality either way.
		snap, serr := repo.Snapshot(context.Background())
		w.update(func() {
			if gen != m.gen {
				return
			}
			m.setBusyQuiet()
			m.lastOut = out
			if cerr != nil {
				// Pre-commit gate, staging verify, or parse error.
				m.lastErr = cerr.Error()
				m.lastOut = nil
				w.Notify("error", "提交失败："+m.lastErr)
			} else if out.Error != nil {
				// git exited nonzero (hook rejection, bad message …).
				m.lastErr = out.Error.Error()
				w.Notify("error", "提交失败："+m.lastErr)
			} else {
				m.lastErr = ""
				// Successful commit: clear the draft, reset selection.
				m.message = ""
				m.checks = checkboxes{}
				w.Notify("success", "已提交")
			}
			if serr == nil {
				m.Snap = snap
				m.pruneChecked()
				m.ensureSelectedFile()
			}
		})
	}()
}

// sync runs one remote operation (fetch/pull/push) in a worker, then
// refreshes the snapshot so ahead/behind and file state stay current.
// kind is "fetch", "pull" or "push"; extra carries per-op options.
func (m *commitModel) sync(ctx context.Context, w updater, kind string, fn func(context.Context, *git.Repo) *git.SyncResult) {
	if m.busy || m.RepoPath == "" {
		return
	}
	label := map[string]string{"fetch": "正在获取…", "pull": "正在拉取…", "push": "正在推送…"}[kind]
	if label == "" {
		label = "正在同步…"
	}
	m.gen++
	gen := m.gen
	repoPath := m.RepoPath
	m.setBusy(w, true, label)
	go func() {
		repo := git.NewRepo(repoPath)
		res := fn(ctx, repo)
		snap, _ := repo.Snapshot(context.Background())
		w.update(func() {
			if gen != m.gen {
				return
			}
			m.setBusyQuiet()
			if res != nil && res.Err != nil {
				m.lastErr = res.Err.Error()
				m.syncOK = ""
				w.Notify("error", m.lastErr)
			} else {
				m.lastErr = ""
				m.syncOK = map[string]string{
					"fetch": "已获取最新远程状态",
					"pull":  "拉取完成",
					"push":  "推送完成",
				}[kind]
				w.Notify("success", m.syncOK)
			}
			if snap != nil {
				m.Snap = snap
				m.pruneChecked()
				m.ensureSelectedFile()
			}
		})
	}()
}

// Fetch updates remote-tracking branches.
func (m *commitModel) fetch(ctx context.Context, w updater) {
	m.sync(ctx, w, "fetch", func(ctx context.Context, r *git.Repo) *git.SyncResult {
		return r.Fetch(ctx)
	})
}

// Pull integrates upstream changes (merge; rebase option later).
// IDEA opens "Update Project" first — openPullDialog collects the
// options; doPull is the actual run.
func (m *commitModel) pull(ctx context.Context, w updater) {
	m.sync(ctx, w, "pull", func(ctx context.Context, r *git.Repo) *git.SyncResult {
		return r.Pull(ctx, git.PullRequest{})
	})
}

// openPullDialog gathers the current branch/upstream/dirty state and
// opens the Update Project modal.
func (m *commitModel) openPullDialog(w updater) {
	if m.RepoPath == "" {
		return
	}
	repoPath := m.RepoPath
	go func() {
		repo := git.NewRepo(repoPath)
		up, _ := repo.Upstream(context.Background())
		head, _ := repo.CurrentBranch(context.Background())
		snap, _ := repo.Snapshot(context.Background())
		dirty := snap != nil && len(snap.Files) > 0
		w.update(func() {
			m.pullDlg = pullDialog{
				Open:      true,
				Branch:    head,
				Upstream:  up.Name,
				Dirty:     dirty,
				Autostash: dirty, // pre-tick when there's something to stash
			}
		})
	}()
}

// doPull runs the pull with the dialog's options.
func (m *commitModel) doPull(ctx context.Context, w updater) {
	req := git.PullRequest{
		Rebase:    m.pullDlg.Rebase,
		Autostash: m.pullDlg.Autostash,
	}
	m.sync(ctx, w, "pull", func(ctx context.Context, r *git.Repo) *git.SyncResult {
		return r.Pull(ctx, req)
	})
}

// Push publishes the current branch (sets upstream when absent).
// IDEA opens a preview dialog first — openPushDialog collects the
// unpushed list; doPush is what actually runs.
func (m *commitModel) push(ctx context.Context, w updater) {
	m.sync(ctx, w, "push", func(ctx context.Context, r *git.Repo) *git.SyncResult {
		return r.Push(ctx, git.PushRequest{})
	})
}

// openRebaseDialog loads the plan for onto..HEAD and opens the panel.
func (m *commitModel) openRebaseDialog(w updater, onto string) {
	if m.RepoPath == "" {
		return
	}
	repoPath := m.RepoPath
	go func() {
		repo := git.NewRepo(repoPath)
		entries, err := repo.RebasePlan(context.Background(), onto)
		w.update(func() {
			if err != nil {
				m.lastErr = "变基计划：" + err.Error()
				return
			}
			m.rebaseDlg = rebaseDialog{Open: true, Onto: onto, Entries: entries}
		})
	}()
}

// runInteractiveRebase executes the plan the user arranged.
func (m *commitModel) runInteractiveRebase(ctx context.Context, w updater) {
	d := &m.rebaseDlg
	onto := d.Onto
	entries := d.Entries
	m.setBusy(w, true, "正在变基…")
	m.gen++
	gen := m.gen
	repoPath := m.RepoPath
	go func() {
		repo := git.NewRepo(repoPath)
		res, err := repo.InteractiveRebase(ctx, onto, entries)
		snap, _ := repo.Snapshot(context.Background())
		w.update(func() {
			if gen != m.gen {
				return
			}
			m.setBusyQuiet()
			m.rebaseDlg.Open = false
			switch {
			case err != nil:
				m.lastErr = err.Error()
				w.Notify("error", "变基失败："+m.lastErr)
			case res != nil && !res.ExitOK():
				m.lastErr = res.OutputOfStderr()
				w.Notify("error", "变基失败："+m.lastErr)
			default:
				m.syncOK = "变基完成"
				w.Notify("success", m.syncOK)
			}
			if snap != nil {
				m.Snap = snap
			}
		})
	}()
}

// opens the modal. afterCommit marks it as the second half of
// "commit & push" — doPush then commits first.
func (m *commitModel) openPushDialog(w updater, afterCommit bool) {
	if m.RepoPath == "" {
		return
	}
	m.pushDlg = pushDialog{Open: true, Loading: true, afterCommit: afterCommit}
	repoPath := m.RepoPath
	go func() {
		repo := git.NewRepo(repoPath)
		up, _ := repo.Upstream(context.Background())
		commits, err := repo.UnpushedCommits(context.Background(), 50)
		w.update(func() {
			d := &m.pushDlg
			d.Loading = false
			d.HasUpstream = up.HasUpstream
			d.Upstream = up.Name
			d.Diverged = up.HasUpstream && up.Ahead > 0 && up.Behind > 0
			if err != nil {
				d.Err = err.Error()
				return
			}
			d.Commits = commits
			// A rewritten history (amend/rebase) diverged from the
			// remote — IDEA enables force push in that case. Offer
			// force-with-lease pre-ticked, never a bare --force.
			if d.Diverged {
				d.Force = true
			}
		})
	}()
}

// doPush runs the actual push with the dialog's options.
func (m *commitModel) doPush(ctx context.Context, w updater) {
	req := git.PushRequest{
		ForceWithLease: m.pushDlg.Force,
		Tags:           m.pushDlg.PushTags,
	}
	m.sync(ctx, w, "push", func(ctx context.Context, r *git.Repo) *git.SyncResult {
		return r.Push(ctx, req)
	})
}

// commitThenPush commits the selection and pushes on success. The two
// steps stay separate git operations so a hook rejection never pushes
// half-formed state; the push only fires after a clean commit.
func (m *commitModel) commitThenPush(ctx context.Context, w updater) {
	if m.busy || m.RepoPath == "" {
		return
	}
	checked := m.checks.checkedSet()
	var paths, untracked []string
	rows := rowsFor(m.Snap)
	for i := range rows {
		row := &rows[i]
		if !checked[row.change.Path] {
			continue
		}
		if row.change.Kind == git.KindUntracked {
			untracked = append(untracked, row.change.Path)
		} else if row.change.Path != "" {
			paths = append(paths, row.change.Path)
		}
	}
	if len(paths) == 0 && len(untracked) == 0 {
		return
	}
	if m.message == "" && !m.amend {
		m.lastErr = "提交信息为空"
		return
	}
	m.gen++
	gen := m.gen
	repoPath := m.RepoPath
	msg := m.message
	amend := m.amend
	partial := m.partialSelection()
	pushReq := git.PushRequest{
		ForceWithLease: m.pushDlg.Force,
		Tags:           m.pushDlg.PushTags,
	}
	m.setBusy(w, true, "正在提交并推送…")
	go func() {
		repo := git.NewRepo(repoPath)
		out, cerr := repo.Commit(ctx, git.CommitRequest{
			Paths:     paths,
			Untracked: untracked,
			Partial:   partial,
			Message:   msg,
			Amend:     amend,
			NoVerify:  m.noVerify,
		})
		var push *git.SyncResult
		if cerr == nil && (out == nil || out.Error == nil) {
			push = repo.Push(ctx, pushReq)
		}
		snap, _ := repo.Snapshot(context.Background())
		w.update(func() {
			if gen != m.gen {
				return
			}
			m.setBusyQuiet()
			m.lastOut = out
			switch {
			case cerr != nil:
				m.lastErr = cerr.Error()
				m.lastOut = nil
				w.Notify("error", "提交失败："+m.lastErr)
			case out != nil && out.Error != nil:
				m.lastErr = out.Error.Error()
				w.Notify("error", "提交失败："+m.lastErr)
			case push != nil && push.Err != nil:
				m.lastErr = "提交成功，推送失败：" + push.Err.Error()
				m.message = ""
				m.checks = checkboxes{}
				w.Notify("error", m.lastErr)
			default:
				m.lastErr = ""
				m.syncOK = "已提交并推送"
				m.message = ""
				w.Notify("success", m.syncOK)
				m.checks = checkboxes{}
			}
			if snap != nil {
				m.Snap = snap
				m.pruneChecked()
				m.ensureSelectedFile()
			}
		})
	}()
}

// updater wraps the window-update callback so models do not import
// mygo directly (keeps them testable with ui.NewTester). notify posts a
// toast (nil in tests and headless use).
type updater struct {
	update func(fn func())
	notify func(kind, text string)
}

func (u updater) Update(fn func()) { u.update(fn) }

// Notify queues a bottom-right toast when a notification channel exists.
func (u updater) Notify(kind, text string) {
	if u.notify != nil {
		u.notify(kind, text)
	}
}

// setBusy marks an in-flight operation and marks the UI via w.
func (m *commitModel) setBusy(w updater, on bool, text string) {
	m.busy = on
	m.busyText = text
	w.update(func() {})
}

// setBusyQuiet clears busy without touching the UI publication (the
// caller publishes state right after).
func (m *commitModel) setBusyQuiet() {
	m.busy = false
	m.busyText = ""
}

// partialSelection returns the CommitRequest.Partial map for the
// selected file only, or nil when every hunk is ticked / no hunk list
// exists (the common whole-file case stays on the fast path).
func (m *commitModel) partialSelection() map[string][]bool {
	if m.selectedFile == "" || len(m.hunks) == 0 || len(m.hunkCheck) == 0 {
		return nil
	}
	all := true
	for _, on := range m.hunkCheck {
		if !on {
			all = false
			break
		}
	}
	if all {
		return nil
	}
	// Copy so the worker isn't racing the UI's next tick.
	sel := make([]bool, len(m.hunkCheck))
	copy(sel, m.hunkCheck)
	return map[string][]bool{m.selectedFile: sel}
}

// loadLFS lazily fetches the tracked-pattern list + locks once per
// repo. Called from the view — cheap after the first fetch.
func (m *commitModel) loadLFS(w updater) {
	if m.RepoPath == "" || m.lfsLoaded {
		return
	}
	m.lfsLoaded = true
	repoPath := m.RepoPath
	go func() {
		repo := git.NewRepo(repoPath)
		if !repo.HasLFS(context.Background()) {
			return // no git-lfs installed — UI hides LFS affordances
		}
		tracked, _ := repo.LFSTracked(context.Background())
		locks, _ := repo.LFSLocks(context.Background())
		w.update(func() {
			m.lfs = tracked
			m.lfsLocks = locks
		})
	}()
}

// loadBisectStatus refreshes the bisect panel's state.
func (m *commitModel) loadBisectStatus(w updater) {
	if m.RepoPath == "" {
		return
	}
	repoPath := m.RepoPath
	go func() {
		repo := git.NewRepo(repoPath)
		st, err := repo.BisectStatus(context.Background())
		w.update(func() {
			if err == nil {
				m.bisect = st
			}
		})
	}()
}

// bisectOp runs one bisect verb then reloads status + snapshot.
func (m *commitModel) bisectOp(ctx context.Context, w updater, fn func(context.Context, *git.Repo) (*git.RunResult, error)) {
	if m.bisectBusy || m.RepoPath == "" {
		return
	}
	m.bisectBusy = true
	m.gen++
	gen := m.gen
	repoPath := m.RepoPath
	m.setBusy(w, true, "正在执行二分…")
	go func() {
		repo := git.NewRepo(repoPath)
		res, err := fn(ctx, repo)
		st, _ := repo.BisectStatus(context.Background())
		snap, _ := repo.Snapshot(context.Background())
		w.update(func() {
			if gen != m.gen {
				return
			}
			m.bisectBusy = false
			m.setBusyQuiet()
			if err != nil {
				m.bisectErr = err.Error()
				w.Notify("error", "bisect："+err.Error())
			} else if res != nil && !res.ExitOK() {
				m.bisectErr = res.OutputOfStderr()
				w.Notify("error", "bisect："+m.bisectErr)
			} else {
				m.bisectErr = ""
				m.bisect = st
				if st != nil && st.Done {
					w.Notify("success", "bisect 完成：首个坏提交 "+shortOIDStr(st.FirstBadOID))
				}
			}
			if snap != nil {
				m.Snap = snap
			}
		})
	}()
}

func shortOIDStr(oid string) string {
	if len(oid) > 7 {
		return oid[:7]
	}
	return oid
}

// loadPreChecks fetches the advisory report in a worker.
func (m *commitModel) loadPreChecks(w updater) {
	if m.RepoPath == "" || m.preChecksLoad {
		return
	}
	m.gen++
	gen := m.gen
	repoPath := m.RepoPath
	m.preChecksLoad = true
	go func() {
		repo := git.NewRepo(repoPath)
		pc, err := repo.PrecommitCheck(context.Background(), nil)
		w.update(func() {
			if gen != m.gen {
				return
			}
			m.preChecksLoad = false
			m.preChecksLoaded = true
			if err == nil {
				m.preChecks = pc
			}
		})
	}()
}

// loadTemplate reads commit.template into the draft message.
func (m *commitModel) loadTemplate(w updater) {
	if m.RepoPath == "" {
		return
	}
	repoPath := m.RepoPath
	go func() {
		repo := git.NewRepo(repoPath)
		text, err := repo.CommitTemplate(context.Background())
		w.update(func() {
			if err == nil && text != "" {
				m.message = text
			}
		})
	}()
}

// hunkCheckedBind lazily materialises the hunkCheck slice so a row's
// checkbox has a *bool to bind, defaulting every hunk on (IDEA ticks
// all chunks of a checked file).
func (m *commitModel) hunkCheckedBind(i int) *bool {
	if len(m.hunkCheck) != len(m.hunks) {
		m.hunkCheck = make([]bool, len(m.hunks))
		for j := range m.hunkCheck {
			m.hunkCheck[j] = true
		}
	}
	return &m.hunkCheck[i]
}

// createPatch exports the checked worktree changes (or one commit) to a
// .patch file the user picked (IDEA: Create Patch…).
func (m *commitModel) createPatch(ctx context.Context, w updater, paths []string, commitOID, dest string) {
	m.setBusy(w, true, "正在导出补丁…")
	m.gen++
	gen := m.gen
	repoPath := m.RepoPath
	go func() {
		repo := git.NewRepo(repoPath)
		var err error
		var out string
		if commitOID != "" {
			out, err = repo.CreatePatchFromCommit(ctx, commitOID, dest)
		} else {
			err = repo.CreatePatchFromWorktree(ctx, paths, dest)
		}
		w.update(func() {
			if gen != m.gen {
				return
			}
			m.setBusyQuiet()
			if err != nil {
				m.lastErr = "导出补丁：" + err.Error()
				return
			}
			m.lastErr = ""
			if out != "" {
				m.syncOK = "补丁已导出：" + filepath.Base(out)
			} else {
				m.syncOK = "补丁已导出"
			}
		})
	}()
}

// applyPatch applies a .patch the user picked: git am for format-patch
// files, git apply otherwise (IDEA: Apply Patch…).
func (m *commitModel) applyPatch(ctx context.Context, w updater, patchFile string) {
	m.setBusy(w, true, "正在应用补丁…")
	m.gen++
	gen := m.gen
	repoPath := m.RepoPath
	go func() {
		repo := git.NewRepo(repoPath)
		data, rerr := os.ReadFile(patchFile)
		var res *git.RunResult
		var err error
		if rerr != nil {
			err = rerr
		} else if git.LooksLikeMailPatch(data) {
			res, err = repo.ApplyMailPatch(ctx, patchFile)
		} else {
			res, err = repo.ApplyPatch(ctx, patchFile)
		}
		snap, _ := repo.Snapshot(context.Background())
		w.update(func() {
			if gen != m.gen {
				return
			}
			m.setBusyQuiet()
			switch {
			case err != nil:
				m.lastErr = "应用补丁：" + err.Error()
			case res != nil && !res.ExitOK():
				m.lastErr = res.OutputOfStderr()
			default:
				m.syncOK = "补丁已应用"
			}
			if snap != nil {
				m.Snap = snap
				m.pruneChecked()
				m.ensureSelectedFile()
			}
		})
	}()
}
