package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui/editor"

	"lumegit/internal/git"
	"lumegit/internal/store"
	"lumegit/internal/svn"
	"lumegit/internal/vcs"
)

// isSVN reports whether the open repo is an svn working copy — the one
// gate every pane/dialog consults before showing a git-only feature
// (staging, push, stash, reflog, bisect, branch/tag submenus…).
func (m *commitModel) isSVN() bool {
	return m.VCS != nil && m.VCS.Kind == vcs.KindSVN
}

// isGit is the inverse — true when the backend is git or not yet known.
func (m *commitModel) isGit() bool { return !m.isSVN() }

// commitModel holds all workspace state for the Commit tab of one
// window. Everything UI-visible lives here; the view function reads it
// rebuild-time, and background jobs publish changes through
// publish (set to win.Update in main.go).
type commitModel struct {
	RepoPath string // empty = no repo open yet
	// VCS is the detected backend (git or svn). All repo ops go through
	// it; Git-only fields stay nil on an svn WC so view code can hide
	// the git-shaped affordances (staging area, push, rebase…).
	VCS *vcs.Backend

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
	// *vcs.Snapshot — git and svn fill the same row set; Snap.Git keeps
	// the raw git snapshot for git-only affordances (staging area,
	// detached HEAD, in-progress op, upstream counts) and is nil on svn.
	Snap *vcs.Snapshot

	// Sorted rows only change when the immutable snapshot is replaced.
	rowsSnapshot *vcs.Snapshot
	sortedRows   []fileRow

	// File list filter text.
	filter string

	// Selection state. checks holds per-path checkbox bindings
	// (ui.Checkbox binds *bool; map entries aren't addressable);
	// selectedFile is the diff shown on the right.
	checks       checkboxes
	selectedFile string

	// Diff for the selected file, as displayed.
	diffLoading  bool
	diff         *git.DiffInfo
	diffErr      string
	diffPrepared *editor.DiffState // built on a worker, owned by UI after publication
	// diffPath is which file diff belongs to — when selectedFile moves
	// on but the new diff hasn't landed, the pane keeps showing the old
	// one (greyed) instead of flashing a spinner; the header shows the
	// incoming file's name. Keeps rapid arrow/click switching smooth.
	diffPath string

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

	// pollCancel stops the refs-fingerprint poller for the current repo.
	// nil while no repo is open or the poller isn't running.
	pollCancel context.CancelFunc

	// refreshRunning makes refresh() and the poller's quiet refresh
	// mutually exclusive — a poller tick while a manual refresh is in
	// flight is skipped instead of stacking a second `git status`.
	refreshRunning bool

	// lastFingerprint is the refs/index fingerprint from the most recent
	// poll; a change means refs moved externally.
	lastFingerprint string

	// onRefsChanged, when set, runs on the UI thread after a fingerprint
	// change — windows use it to invalidate cached branch/tag lists.
	onRefsChanged func()

	// preferNoUntracked switches `git status` to -uno once we've seen an
	// untracked flood (VSCode's huge-repo degradation). Sticky per repo
	// for this window; the flag persists on the model, not the Repo, so
	// it survives across the per-call NewRepo instances.
	preferNoUntracked bool

	// isActive, when set, reports whether the window is focused. The
	// poller polls at 2s while focused and backs off to 10s unfocused —
	// nobody reads a window they can't see (lazygit's RefreshInterval
	// is 10s by default anyway).
	isActive func() bool

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

	// svn:ignore panel — svn has no .gitignore file to open, so the
	// app lists every directory's ignore patterns in one dialog.
	ignoreOpen    bool
	ignores       []svn.IgnoreEntry
	ignoreLoading bool
	ignoreErr     string
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
// destructive worktree actions. Run, when set, is the op to execute on
// confirm (hard reset, stash drop, …) — the Paths/Delete branches cover
// the file-scoped reverts.
type revertAsk struct {
	Open   bool
	Title  string
	Body   string
	Staged bool     // revert staged side vs worktree side
	Paths  []string // for revert
	Delete string   // single path for delete (untracked)
	Run    func()   // custom confirm action (takes precedence over Paths/Delete)
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

// fileRows reuses the order while the immutable snapshot stays the same.
// Callers must not modify the returned slice or its rows.
func (m *commitModel) fileRows() []fileRow {
	if m.rowsSnapshot != m.Snap {
		m.rowsSnapshot = m.Snap
		m.sortedRows = rowsFor(m.Snap)
	}
	return m.sortedRows
}

// groups organizes the visible rows into their changelists, in order:
// named lists first (creation order), Default last so unmatched files
// always show at the bottom — IDEA-style "everything else" bucket.
// svn: 已忽略 文件拆成独立只读组垫底 —— 不参与提交、不挂变更列表。
func (m *commitModel) groups() []listGroup {
	all := m.fileRows()
	var rows, ignored []fileRow
	for _, r := range all {
		if r.change.Kind == vcs.KindIgnored {
			ignored = append(ignored, r)
		} else {
			rows = append(rows, r)
		}
	}
	if m.lists == nil {
		// No store loaded: single synthetic group holds everything.
		g := listGroup{Name: store.DefaultName, Active: true, Rows: rows}
		for _, r := range rows {
			if m.checks.byPath[r.change.Path] != nil && m.checks.byPath[r.change.Path].checked {
				g.CheckedN++
			}
		}
		out := []listGroup{g}
		if len(ignored) > 0 {
			out = append(out, listGroup{Name: "已忽略", Rows: ignored})
		}
		return out
	}
	// Build the reverse index once instead of scanning every named list's
	// paths for every row. Preserve ListOf's first-list-wins behavior.
	assigned := make(map[string]string, len(rows))
	for _, l := range m.lists.Lists {
		for _, path := range l.Paths {
			if _, exists := assigned[path]; !exists {
				assigned[path] = l.Name
			}
		}
	}
	byList := map[string][]fileRow{}
	checked := m.checks.checkedSet()
	for _, r := range rows {
		name, ok := assigned[r.change.Path]
		if !ok {
			name = m.lists.Default
		}
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
	if len(ignored) > 0 {
		out = append(out, listGroup{Name: "已忽略", Rows: ignored})
	}
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
		if f.Kind == vcs.KindIgnored {
			continue // ignored paths never join a changelist
		}
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
	assigned := make(map[string]bool, len(live))
	for _, l := range m.lists.Lists {
		for _, path := range l.Paths {
			assigned[path] = true
		}
	}
	target := m.lists.Find(active)
	if target == nil {
		return
	}
	for _, f := range m.Snap.Files {
		if f.Kind != vcs.KindIgnored && !assigned[f.Path] {
			target.Paths = append(target.Paths, f.Path)
			assigned[f.Path] = true
		}
	}
}

// rows builds the display order: conflicts first, then by category
// (renames/mods/added/deleted), then untracked; alphabetical inside
// each group. IDEA-style ordering: files that block commit surface
// first.
func rowsFor(snap *vcs.Snapshot) []fileRow {
	if snap == nil {
		return nil
	}
	kindRank := func(k vcs.FileKind) int {
		switch k {
		case vcs.KindUnmerged:
			return 0
		case vcs.KindModified, vcs.KindTypeChange:
			return 1
		case vcs.KindRenamed:
			return 2
		case vcs.KindAdded:
			return 3
		case vcs.KindDeleted:
			return 4
		case vcs.KindUntracked:
			return 5
		case vcs.KindIgnored:
			return 7 // 已忽略垫底 —— 查看用，不可提交
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

// openRepo validates a directory as a git worktree or svn working copy
// and loads the first snapshot. Runs on a worker; the UI thread calls
// this via win.Update publication only.
func openRepo(ctx context.Context, dir string) (*vcs.Snapshot, *vcs.Backend, error) {
	if _, err := os.Stat(dir); err != nil {
		return nil, nil, fmt.Errorf("目录不存在：%v", err)
	}
	b, err := vcs.Open(ctx, dir)
	if err != nil {
		return nil, nil, err
	}
	switch b.Kind {
	case vcs.KindGit:
		// Warm git's own file cache for this repo — cheap, idempotent,
		// and every subsequent `status` gets faster.
		b.Git.EnableFSMonitor(ctx)
	}
	snap, err := b.Snapshot(ctx)
	if err != nil {
		return nil, b, err
	}
	return snap, b, nil
}

// refresh loads a fresh snapshot of the current repo. Generation
// fencing: results from an older gen are dropped on arrival. One
// refresh at a time — a call while another is in flight is dropped
// (the in-flight result is fresher than anything queued behind it).
func (m *commitModel) refresh(ctx context.Context, w updater) {
	if m.RepoPath == "" {
		return
	}
	if m.refreshRunning {
		return
	}
	m.refreshRunning = true
	gen := m.gen
	m.setBusy(w, true, "正在获取状态…")
	noUntracked := m.preferNoUntracked
	isSVN := m.isSVN()
	go func() {
		var snap *vcs.Snapshot
		var err error
		if isSVN {
			snap, err = m.VCS.Snapshot(ctx)
		} else {
			repo := git.NewRepo(m.RepoPath)
			repo.PreferNoUntracked = noUntracked
			var gs *git.Snapshot
			gs, err = repo.Snapshot(ctx)
			snap = vcs.WrapGit(gs)
		}
		w.update(func() {
			m.refreshRunning = false
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
				m.checkUntrackedFlood()
				m.reconcileLists()
				m.pruneChecked()
				m.ensureSelectedFile()
				// The file the user is diffing may have changed under
				// them (their own edit, an external tool) — refresh the
				// pane when it's still in the new snapshot.
				m.reloadSelectedDiff()
			}
		})
	}()
}

// checkUntrackedFlood counts untracked entries in the snapshot; past
// the threshold the next refresh switches to -uno (cheap mode). Runs
// on the UI thread.
func (m *commitModel) checkUntrackedFlood() {
	if m.preferNoUntracked || m.Snap == nil {
		return
	}
	n := 0
	for _, f := range m.Snap.Files {
		if f.Kind == vcs.KindUntracked {
			n++
		}
	}
	const hugeUntracked = 2000
	if n > hugeUntracked {
		m.preferNoUntracked = true
	}
}

// refreshQuiet is refresh without the busy banner — background triggers
// (refs poll, window focus) shouldn't flash "正在获取状态…". Shares
// refreshRunning with refresh() so the two never stack.
func (m *commitModel) refreshQuiet(ctx context.Context, w updater) {
	if m.RepoPath == "" {
		return
	}
	if m.refreshRunning {
		return
	}
	m.refreshRunning = true
	gen := m.gen
	noUntracked := m.preferNoUntracked
	isSVN := m.isSVN()
	go func() {
		var snap *vcs.Snapshot
		var err error
		if isSVN {
			snap, err = m.VCS.Snapshot(ctx)
		} else {
			repo := git.NewRepo(m.RepoPath)
			repo.PreferNoUntracked = noUntracked
			var gs *git.Snapshot
			gs, err = repo.Snapshot(ctx)
			snap = vcs.WrapGit(gs)
		}
		w.update(func() {
			m.refreshRunning = false
			if gen != m.gen {
				return
			}
			if err != nil {
				m.lastErr = err.Error()
				m.Snap = nil
			} else {
				m.lastErr = ""
				m.Snap = snap
				m.checkUntrackedFlood()
				m.reconcileLists()
				m.pruneChecked()
				m.ensureSelectedFile()
				m.reloadSelectedDiff()
			}
		})
	}()
}

// startPolling launches the refs-fingerprint poller for the open repo
// — the lazygit pattern: instead of watching the worktree (fsnotify
// storm, fd cost), every 2s we ask git "did refs/index change?" and
// only then pay for a full Snapshot. Poll failures are ignored; the
// next tick retries.
func (m *commitModel) startPolling(w updater) {
	if m.pollCancel != nil || m.RepoPath == "" {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.pollCancel = cancel
	repoPath := m.RepoPath
	isSVN := m.isSVN()
	var wc *svn.WC
	if isSVN {
		wc = m.VCS.SVN // captured here: the worker must not read m.*
	}
	isActive := m.isActive
	go func() {
		// git: the refs probe is cheap, 2s focused / 10s background.
		// svn: there are no refs — the probe is a full local `svn
		// status` (a tree walk, like `git status`), so it runs less
		// often; the server round trip (`status -u`, which reveals new
		// remote revisions) only once a minute.
		focused, background := 2*time.Second, 10*time.Second
		if isSVN {
			focused, background = 5*time.Second, 30*time.Second
		}
		const svnRemoteEvery = time.Minute
		var remoteFP string
		var lastRemote time.Time
		interval := focused
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Adaptive cadence by window focus.
				want := focused
				if isActive != nil && !isActive() {
					want = background
				}
				if want != interval {
					interval = want
					ticker.Reset(interval)
					continue
				}
				var fp string
				var err error
				if isSVN {
					fp, err = svnLocalFingerprint(ctx, wc)
					if err == nil && time.Since(lastRemote) >= svnRemoteEvery {
						// Remote part is cached between checks so the
						// combined fingerprint only flips on real change.
						if rfp, rerr := svnRemoteFingerprint(ctx, wc); rerr == nil {
							remoteFP = rfp
						}
						lastRemote = time.Now()
					}
					fp += "\x00" + remoteFP
				} else {
					repo := git.NewRepo(repoPath)
					fp, err = repo.RefsFingerprint(ctx)
				}
				if err != nil {
					continue
				}
				w.update(func() {
					if m.RepoPath != repoPath {
						return // repo changed under us
					}
					if m.lastFingerprint == "" {
						m.lastFingerprint = fp
						return // baseline — don't refresh on first tick
					}
					if fp != m.lastFingerprint {
						m.lastFingerprint = fp
						m.refreshQuiet(context.Background(), w)
						if m.onRefsChanged != nil {
							m.onRefsChanged()
						}
					}
				})
			}
		}
	}()
}

// svnLocalFingerprint summarizes the working copy's local status (no
// server contact): any edit, add, delete or prop change flips it.
func svnLocalFingerprint(ctx context.Context, wc *svn.WC) (string, error) {
	items, err := wc.Status(ctx, false)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, it := range items {
		b.WriteString(it.Path)
		b.WriteByte(byte(it.TextStatus))
		b.WriteByte(byte(it.PropStatus))
		b.WriteByte(0)
	}
	return b.String(), nil
}

// svnRemoteFingerprint captures only the server-side half of
// `status -u` (out-of-date entries + the revision compared against), so
// a new remote commit flips it without local edits interfering.
func svnRemoteFingerprint(ctx context.Context, wc *svn.WC) (string, error) {
	items, err := wc.Status(ctx, true)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, it := range items {
		if !it.OOD {
			continue
		}
		b.WriteString(it.Path)
		b.WriteByte(byte(it.ReposTextStatus))
		b.WriteByte(byte(it.ReposPropStatus))
		b.WriteByte(0)
	}
	return b.String(), nil
}

// stopPolling kills the poller. Called when the repo closes.
func (m *commitModel) stopPolling() {
	if m.pollCancel != nil {
		m.pollCancel()
		m.pollCancel = nil
	}
	m.lastFingerprint = ""
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
// restore / svn revert), then refreshes. Confirmed destructive: bytes
// never staged or committed are lost. For a staged path the caller may
// pass staged=true to also rewind the index to HEAD (git only).
func (m *commitModel) revertPaths(ctx context.Context, w updater, paths []string, staged bool) {
	if m.busy || len(paths) == 0 {
		return
	}
	m.gen++
	gen := m.gen
	m.setBusy(w, true, "正在回滚…")
	go func() {
		var err error
		var snap *vcs.Snapshot
		if m.isSVN() {
			for _, p := range paths {
				if _, rerr := m.VCS.SVN.Revert(ctx, p, true); rerr != nil {
					err = rerr
					break
				}
			}
			snap, _ = m.VCS.Snapshot(context.Background())
		} else {
			repo := git.NewRepo(m.RepoPath)
			if staged {
				err = repo.RevertStagedPaths(ctx, paths)
			} else {
				err = repo.RevertPaths(ctx, paths)
			}
			snap = vcs.WrapGit(mustSnap(repo, ctx))
		}
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

// mustSnap re-reads a git snapshot after an op; the error is swallowed
// because the op's own error already reached the UI.
func mustSnap(repo *git.Repo, ctx context.Context) *git.Snapshot {
	s, _ := repo.Snapshot(ctx)
	return s
}

// deleteUntracked removes an untracked file (only allowed on untracked
// records — anything tracked goes through revert, which keeps the file).
// On svn an unversioned path just gets deleted from disk; a versioned
// path runs `svn delete` (scheduled for removal, file gone from disk).
func (m *commitModel) deleteUntracked(ctx context.Context, w updater, path string) {
	if m.busy || path == "" {
		return
	}
	m.gen++
	gen := m.gen
	repoPath := m.RepoPath
	m.setBusy(w, true, "正在删除…")
	go func() {
		var err error
		var snap *vcs.Snapshot
		if m.isSVN() {
			if f := m.Snap.Find(path); f != nil && f.Kind != vcs.KindUntracked {
				_, err = m.VCS.SVN.Delete(ctx, path, false)
			} else {
				err = os.Remove(filepath.Join(repoPath, filepath.FromSlash(path)))
			}
			snap, _ = m.VCS.Snapshot(context.Background())
		} else {
			repo := git.NewRepo(repoPath)
			err = repo.DeletePath(ctx, path)
			snap = vcs.WrapGit(mustSnap(repo, context.Background()))
		}
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
				m.Snap = vcs.WrapGit(snap)
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
				m.Snap = vcs.WrapGit(snap)
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
				m.Snap = vcs.WrapGit(snap)
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
				m.Snap = vcs.WrapGit(snap)
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

// undoHeadCommit reverses the HEAD commit only (IDEA: "撤消提交…").
// The changes stay in the worktree staged for the next commit.
func (m *commitModel) undoHeadCommit(ctx context.Context, w updater) {
	m.runOp(ctx, w, "正在撤消提交…", "已撤消，改动已留在工作区",
		func(ctx context.Context, r *git.Repo) (*git.RunResult, error) {
			return r.UndoHeadCommit(ctx)
		}, nil)
}

// pushUpTo pushes the current branch's history up to oid — IDEA's
// "推送此前所有提交…" on a log row.
func (m *commitModel) pushUpTo(ctx context.Context, w updater, oid string) {
	m.runOp(ctx, w, "正在推送…", "已推送",
		func(ctx context.Context, r *git.Repo) (*git.RunResult, error) {
			return r.PushUpTo(ctx, oid, "")
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
// Snap.Find is linear, so this builds the live set once — the per-path
// scan version was O(checks × files) on every refresh.
func (m *commitModel) pruneChecked() {
	if m.checks.byPath == nil || m.Snap == nil {
		return
	}
	live := make(map[string]bool, len(m.Snap.Files))
	for i := range m.Snap.Files {
		live[m.Snap.Files[i].Path] = true
	}
	for p := range m.checks.byPath {
		if !live[p] {
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
		m.diffGen++
		if m.diffCancel != nil {
			m.diffCancel()
			m.diffCancel = nil
		}
		m.selectedFile = ""
		m.diff = nil
		m.diffPrepared = nil
		m.diffPath = ""
		m.diffErr = ""
		m.diffLoading = false
		m.hunks = nil
		m.hunkCheck = nil
	}
}

// selectFile records the selection and starts its diff load in the
// background. Rapid re-picks cancel the previous worker via a gen bump,
// so stale results can't overwrite the newest selection's diff and the
// UI doesn't flicker through an old file's content.
func (m *commitModel) selectFile(f git.FileChange) {
	if m.selectedFile == f.Path {
		if m.diffLoading {
			// Same file still loading — don't restart its worker, that
			// only defers the result and is what makes a double-click on
			// one row look like "selection didn't take".
			return
		}
		if m.diff != nil {
			return // already loaded: nothing to do
		}
		// diff == nil && !diffLoading: a previous load errored — retry.
	}
	m.diffGen++ // cancel any in-flight diff worker
	gen := m.diffGen
	if m.diffCancel != nil {
		m.diffCancel() // abort the previous file's git work
	}
	m.selectedFile = f.Path
	// Drop the old diff the moment the pick changes — the pane switches
	// to its loading state this same frame instead of repainting the
	// previous file's DiffView (that repaint is the click lag).
	m.diff = nil
	m.diffPrepared = nil
	m.diffPath = ""
	m.diffErr = ""
	m.diffLoading = true
	m.hunks = nil
	m.hunkCheck = nil
	path, origPath := f.Path, f.OrigPath
	repoPath := m.RepoPath
	update := m.publish
	if update == nil {
		// Tests drive models without a window; a nil publish used to
		// panic inside the worker's update closure.
		update = func(fn func()) { fn() }
	}
	w := updater{update: update}
	ctx, cancel := context.WithCancel(context.Background())
	m.diffCancel = cancel

	isSVN := m.isSVN()
	backend := m.VCS
	go func() {
		defer cancel()
		var d *git.DiffInfo
		var derr error
		var hunks []git.Hunk
		if isSVN {
			d, derr = svnFileDiff(ctx, backend.SVN, path)
		} else {
			d, derr = loadDiffCtx(ctx, repoPath, path, origPath)
		}
		var prepared *editor.DiffState
		if ctx.Err() == nil && derr == nil && d != nil && !d.Binary {
			prepared = prepareDiffState(ctx, d)
		}
		if ctx.Err() != nil {
			return
		}
		// Show the diff as soon as it is ready. The separate Git hunk
		// query is for the optional partial-commit controls and must not
		// delay the primary diff pane.
		w.update(func() {
			if gen != m.diffGen || m.selectedFile != path {
				return
			}
			m.diffLoading = false
			if derr != nil && d == nil {
				m.diffErr = derr.Error()
				return
			}
			m.diff, m.diffErr = d, ""
			m.diffPrepared = prepared
			m.diffPath = path
		})
		if !isSVN && derr == nil && origPath == "" && d != nil && !d.Binary && ctx.Err() == nil {
			repo := git.NewRepo(repoPath)
			hunks, _ = repo.FileHunks(ctx, path)
			if ctx.Err() != nil {
				return
			}
			w.update(func() {
				if gen != m.diffGen || m.selectedFile != path {
					return
				}
				m.hunks = hunks
				m.hunkCheck = nil // lazily default all-ticked
			})
		}
	}()
}

// Only one worker at a time may run the editor's full, non-cancellable
// line comparison. Queued selections can still be cancelled immediately.
var diffPrepareSlot = make(chan struct{}, 1)

// prepareDiffState constructs and warms the editor model off the UI
// thread. The caller transfers exclusive ownership after publication;
// a cancelled or superseded state is never exposed to the view.
func prepareDiffState(ctx context.Context, d *git.DiffInfo) *editor.DiffState {
	select {
	case diffPrepareSlot <- struct{}{}:
	case <-ctx.Done():
		return nil
	}
	defer func() { <-diffPrepareSlot }()
	if ctx.Err() != nil {
		return nil
	}
	state := newDiffState(d)
	state.Hunks()
	if ctx.Err() != nil {
		return nil
	}
	return state
}

// inlineMaxLines caps how many changed lines a diff may carry before its
// word-level highlight is skipped: inline diff pairs every changed old
// line with a new one (alignBySimilarity + per-line inlineDiff) on the
// draw path, the single largest frame cost of a huge pick. Diffs past
// the cap still get the added/removed line tints, just not the word
// marks inside them.
const inlineMaxLines = 2000

// newDiffState builds the viewer state for one file diff, degrading the
// word-level highlight to DiffInlineNone when the change is too big to
// compute per frame. Every DiffState in the app comes from here so all
// diff panes share the same budget.
func newDiffState(d *git.DiffInfo) *editor.DiffState {
	var old, new string
	if d != nil {
		old, new = string(d.Old), string(d.New)
	}
	s := &editor.DiffState{
		Old: editor.NewBuffer(old),
		New: editor.NewBuffer(new),
	}
	// A changed line can't exceed the shorter side's size, so the raw
	// payload bounds the inline work without running the diff twice.
	if d != nil && min(bytes.Count(d.Old, []byte{'\n'}), bytes.Count(d.New, []byte{'\n'})) > inlineMaxLines {
		s.Inline = editor.DiffInlineNone
	}
	return s
}

// loadDiffCtx resolves one file's diff for the UI from a background
// goroutine; ctx cancels stale work. Git only — svn goes through
// svnFileDiff.
func loadDiffCtx(ctx context.Context, repoPath, path, origPath string) (*git.DiffInfo, error) {
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
	return d, nil
}

// svnFileDiff fills the diff pane for an svn row: BASE vs worktree.
// Untracked/unversioned paths have no BASE side — cat returns "".
func svnFileDiff(ctx context.Context, wc *svn.WC, path string) (*git.DiffInfo, error) {
	dt, err := wc.FileDiffWorktree(ctx, path)
	if err != nil {
		return nil, err
	}
	return &git.DiffInfo{
		Path:     dt.Path,
		Old:      []byte(dt.Old),
		New:      []byte(dt.New),
		OldLabel: dt.OldLabel,
		NewLabel: dt.NewLabel,
		Binary:   looksBinary(dt.Old) || looksBinary(dt.New),
	}, nil
}

// looksBinary flags content that isn't readable text — NULs or a big
// share of control bytes mean it's an archive/class/etc., and the diff
// view shows the binary placeholder instead of garbage.
func looksBinary(s string) bool {
	if len(s) == 0 {
		return false
	}
	const limit = 8192
	n := len(s)
	if n > limit {
		n = limit
	}
	var bad int
	for i := 0; i < n; i++ {
		b := s[i]
		if b == 0 {
			return true
		}
		// control bytes other than \t \n \r count toward "not text"
		if b < 0x20 && b != '\t' && b != '\n' && b != '\r' {
			bad++
		}
	}
	return bad > n/10
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
		if !checked[row.change.Path] || row.change.Kind == vcs.KindIgnored {
			continue // ignored 行永远没有勾选状态，也不该进提交
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
	if m.isSVN() {
		all := append(append([]string{}, paths...), untracked...)
		m.svnCommit(ctx, w, all)
		return
	}
	m.gen++
	gen := m.gen
	repoPath := m.RepoPath
	msg := m.message
	amend := m.amend
	partial, partialHunks := m.partialSelection(paths)
	noVerify := m.noVerify
	m.setBusy(w, true, "正在提交…")

	go func() {
		repo := git.NewRepo(repoPath)
		out, cerr := repo.Commit(ctx, git.CommitRequest{
			Paths:        paths,
			Untracked:    untracked,
			Partial:      partial,
			PartialHunks: partialHunks,
			Message:      msg,
			Amend:        amend,
			NoVerify:     noVerify,
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
				m.Snap = vcs.WrapGit(snap)
				m.pruneChecked()
				m.ensureSelectedFile()
			}
			if errors.Is(cerr, git.ErrPartialStale) {
				m.reloadSelectedDiff()
			}
		})
	}()
}

// svnCommit runs an svn commit for the checked paths. Unversioned
// (untracked) paths get `svn add` first — svn commit skips them
// silently otherwise. Commit IS the push for svn, so unlike git there
// is no staged vs pushed split.
func (m *commitModel) svnCommit(ctx context.Context, w updater, paths []string) {
	m.gen++
	gen := m.gen
	msg := m.message
	m.setBusy(w, true, "正在提交…")
	snap := m.Snap // captured on the UI thread — the worker must not read m.*
	vcsB := m.VCS
	repoPath := m.RepoPath
	go func() {
		wc := vcsB.SVN
		var err error
		// Auto-add unversioned paths so the commit includes them.
		for _, p := range paths {
			if f := snap.Find(p); f != nil && f.Kind == vcs.KindUntracked {
				if _, aerr := wc.Add(ctx, p, true); aerr != nil {
					err = fmt.Errorf("svn add %s: %w", p, aerr)
					break
				}
			}
		}
		// `svn commit <dir>` recurses — a checked directory would sweep in
		// every uncommitted descendant the user did NOT tick. Directories
		// get --depth empty (their own prop-changes still commit); files
		// keep the default depth.
		var dirPaths, filePaths []string
		for _, p := range paths {
			if fi, serr := os.Stat(filepath.Join(repoPath, filepath.FromSlash(p))); serr == nil && fi.IsDir() {
				dirPaths = append(dirPaths, p)
			} else {
				filePaths = append(filePaths, p)
			}
		}
		var res *svn.CommitResult
		if err == nil {
			if len(filePaths) > 0 || len(dirPaths) == 0 {
				res, err = wc.Commit(ctx, svn.CommitOptions{Message: msg, Paths: filePaths})
			}
			if err == nil && len(dirPaths) > 0 {
				var dres *svn.CommitResult
				dres, err = wc.Commit(ctx, svn.CommitOptions{Message: msg, Paths: dirPaths, Depth: "empty"})
				if err == nil && res == nil {
					res = dres
				}
			}
		}
		snap, _ := vcsB.Snapshot(context.Background())
		w.update(func() {
			if gen != m.gen {
				return
			}
			m.setBusyQuiet()
			switch {
			case err != nil:
				m.lastErr = err.Error()
				w.Notify("error", "提交失败："+m.lastErr)
			case res != nil && res.Failed:
				m.lastErr = res.Reason
				w.Notify("error", "提交失败："+m.lastErr)
			default:
				m.lastErr = ""
				m.message = ""
				m.checks = checkboxes{}
				rev := ""
				if res != nil && res.Rev > 0 {
					rev = fmt.Sprintf(" (r%d)", res.Rev)
				}
				w.Notify("success", "已提交"+rev)
			}
			if snap != nil {
				m.Snap = snap
				m.pruneChecked()
				m.ensureSelectedFile()
			}
		})
	}()
}

// svnAdd brings an unversioned path under version control.
func (m *commitModel) svnAdd(ctx context.Context, w updater, path string) {
	if m.busy || path == "" {
		return
	}
	m.gen++
	gen := m.gen
	m.setBusy(w, true, "正在添加…")
	go func() {
		_, err := m.VCS.SVN.Add(ctx, path, true)
		snap, _ := m.VCS.Snapshot(context.Background())
		w.update(func() {
			if gen != m.gen {
				return
			}
			m.setBusyQuiet()
			if err != nil {
				m.lastErr = "添加失败：" + err.Error()
			} else {
				m.lastErr = ""
				m.syncOK = "已加入版本控制"
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

// svnUnignorePath drops the ignore rule that covers path — walks the
// parent chain's svn:ignore and removes the matching pattern.
func (m *commitModel) svnUnignorePath(ctx context.Context, w updater, path string) {
	if m.busy || path == "" {
		return
	}
	m.gen++
	gen := m.gen
	m.setBusy(w, true, "正在取消忽略…")
	go func() {
		err := m.VCS.SVN.Unignore(ctx, path)
		snap, _ := m.VCS.Snapshot(context.Background())
		w.update(func() {
			if gen != m.gen {
				return
			}
			m.setBusyQuiet()
			if err != nil {
				m.lastErr = "取消忽略失败：" + err.Error()
			} else {
				m.lastErr = ""
				m.syncOK = "已取消忽略"
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

// svnIgnoreAdd appends path's basename to its parent's svn:ignore —
// the "加入 .gitignore" counterpart, per-directory in svn.
func (m *commitModel) svnIgnoreAdd(ctx context.Context, w updater, path string) {
	if m.busy || path == "" {
		return
	}
	m.gen++
	gen := m.gen
	m.setBusy(w, true, "正在更新 svn:ignore…")
	go func() {
		res, err := m.VCS.SVN.AddIgnore(ctx, path)
		if err == nil && res != nil && !res.ExitOK() {
			err = &svn.Error{Args: res.Args, Stderr: res.OutputOfStderr()}
		}
		snap, _ := m.VCS.Snapshot(context.Background())
		w.update(func() {
			if gen != m.gen {
				return
			}
			m.setBusyQuiet()
			if err != nil {
				m.lastErr = "忽略失败：" + err.Error()
			} else {
				m.lastErr = ""
				m.syncOK = "已加入 svn:ignore"
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

// --- svn ignore panel --------------------------------------------------
//
// svn scatters ignore rules in per-directory svn:ignore properties —
// nothing like a gitignore file to open. The panel lists every dir's
// patterns so the user can see (and later edit) them in one place.

// svnLoadIgnores refreshes the ignore dialog's rows.
func (m *commitModel) svnLoadIgnores(ctx context.Context, w updater) {
	m.gen++
	gen := m.gen
	m.ignoreLoading = true
	m.ignoreErr = ""
	go func() {
		list, err := m.VCS.SVN.Ignores(ctx)
		w.update(func() {
			if gen != m.gen {
				return
			}
			m.ignoreLoading = false
			if err != nil {
				m.ignoreErr = err.Error()
				return
			}
			m.ignores = list
		})
	}()
}

// svnIgnoreRemove deletes one pattern from a dir's svn:ignore and
// reloads the list.
func (m *commitModel) svnIgnoreRemove(ctx context.Context, w updater, dir, pattern string) {
	m.gen++
	gen := m.gen
	m.ignoreLoading = true
	go func() {
		d := dir
		if d == "" {
			d = "."
		}
		cur, err := m.VCS.SVN.PropGet(ctx, "svn:ignore", d)
		var list []svn.IgnoreEntry
		if err == nil {
			var keep []string
			for _, line := range strings.Split(cur, "\n") {
				line = strings.TrimSpace(line)
				if line != "" && line != pattern {
					keep = append(keep, line)
				}
			}
			var res *svn.RunResult
			res, err = m.VCS.SVN.PropSet(ctx, "svn:ignore", strings.Join(keep, "\n")+"\n", d)
			if err == nil && res != nil && !res.ExitOK() {
				err = &svn.Error{Args: res.Args, Stderr: res.OutputOfStderr()}
			}
		}
		if err == nil {
			list, err = m.VCS.SVN.Ignores(ctx)
		}
		snap, _ := m.VCS.Snapshot(context.Background())
		w.update(func() {
			if gen != m.gen {
				return
			}
			m.ignoreLoading = false
			if err != nil {
				m.ignoreErr = err.Error()
				return
			}
			m.ignores = list
			if snap != nil {
				m.Snap = snap
				m.reconcileLists()
				m.pruneChecked()
			}
		})
	}()
}

// svnUpdateToRev rolls the WC back/forward to a chosen revision —
// svn's "checkout this commit". Conflicts land as 'C' rows like a
// normal update.
func (a *app) svnUpdateToRev(oid string) {
	m := &a.commit
	if m.busy || m.RepoPath == "" {
		return
	}
	rev := svnRevNum(oid)
	if rev <= 0 {
		return
	}
	m.gen++
	gen := m.gen
	w := a.updater()
	m.setBusy(w, true, "正在更新到 r"+fmt.Sprint(rev)+"…")
	go func() {
		res, err := m.VCS.SVN.Update(context.Background(), svn.UpdateOptions{Rev: rev, Accept: svn.AcceptPostpone})
		snap, _ := m.VCS.Snapshot(context.Background())
		w.update(func() {
			if gen != m.gen {
				return
			}
			m.setBusyQuiet()
			switch {
			case err != nil:
				m.lastErr = "更新失败：" + err.Error()
				w.Notify("error", m.lastErr)
			default:
				m.lastErr = ""
				m.syncOK = fmt.Sprintf("已更新到 r%d", res.Revision)
				w.Notify("success", m.syncOK)
			}
			if snap != nil {
				m.Snap = snap
				m.reconcileLists()
				m.pruneChecked()
				m.ensureSelectedFile()
			}
			a.history.lastLoadedRepo = "" // HEAD moved — refresh log view
		})
	}()
}

// svnUpdate runs `svn update` — svn's pull. Conflicts land as 'C'
// status entries the user resolves through the normal conflict flow;
// there is no merge-in-progress state to continue/abort.
func (m *commitModel) svnUpdate(ctx context.Context, w updater) {
	if m.busy || m.RepoPath == "" {
		return
	}
	m.gen++
	gen := m.gen
	m.setBusy(w, true, "正在更新…")
	go func() {
		res, err := m.VCS.SVN.Update(ctx, svn.UpdateOptions{Accept: svn.AcceptPostpone})
		snap, _ := m.VCS.Snapshot(context.Background())
		w.update(func() {
			if gen != m.gen {
				return
			}
			m.setBusyQuiet()
			switch {
			case err != nil:
				m.lastErr = "更新失败：" + err.Error()
				w.Notify("error", m.lastErr)
			default:
				m.lastErr = ""
				if res != nil {
					m.syncOK = fmt.Sprintf("已更新到 r%d", res.Revision)
					if len(res.Conflicted) > 0 {
						w.Notify("warn", fmt.Sprintf("更新产生 %d 个冲突", len(res.Conflicted)))
					} else {
						w.Notify("success", m.syncOK)
					}
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
				m.Snap = vcs.WrapGit(snap)
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
	m.openRebaseDialogPreset(w, onto, "", "")
}

// openRebaseDialogPreset opens the same panel but pre-sets one entry's
// action — IDEA's "编辑提交消息…" (reword), "删除提交" (drop) and
// "压缩提交…" (squash) all land here with the commit pre-marked, so
// the user confirms the plan rather than rebuilding it by hand.
func (m *commitModel) openRebaseDialogPreset(w updater, onto, targetOID string, action git.RebaseAction) {
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
			if targetOID != "" && action != "" {
				found := false
				for i := range entries {
					if entries[i].OID == targetOID || entries[i].Short == targetOID {
						entries[i].Action = action
						found = true
						break
					}
				}
				// The target must be inside onto..HEAD — a filtered or
				// merged-out commit can't be reworded/dropped this way,
				// and opening the dialog with nothing marked would
				// silently run a plain rebase.
				if !found {
					m.lastErr = "变基计划：目标提交 " + targetOID + " 不在 " + onto + "..HEAD 范围内"
					return
				}
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
			case snap != nil && snap.InProgress != "":
				// 'edit' or a conflict paused mid-sequence: not done —
				// the sequencer still needs continue/abort.
				m.syncOK = "变基已暂停，等待继续/中止"
				w.Notify("info", m.syncOK)
			default:
				m.syncOK = "变基完成"
				w.Notify("success", m.syncOK)
			}
			if snap != nil {
				m.Snap = vcs.WrapGit(snap)
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
			// remote — the dialog shows a warning then, but ticking
			// force-with-lease must stay the user's own click.
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
		if !checked[row.change.Path] || row.change.Kind == vcs.KindIgnored {
			continue // ignored 行永远没有勾选状态，也不该进提交
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
	partial, partialHunks := m.partialSelection(paths)
	noVerify := m.noVerify
	pushReq := git.PushRequest{
		ForceWithLease: m.pushDlg.Force,
		Tags:           m.pushDlg.PushTags,
	}
	m.setBusy(w, true, "正在提交并推送…")
	go func() {
		repo := git.NewRepo(repoPath)
		out, cerr := repo.Commit(ctx, git.CommitRequest{
			Paths:        paths,
			Untracked:    untracked,
			Partial:      partial,
			PartialHunks: partialHunks,
			Message:      msg,
			Amend:        amend,
			NoVerify:     noVerify,
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
				m.Snap = vcs.WrapGit(snap)
				m.pruneChecked()
				m.ensureSelectedFile()
			}
			if errors.Is(cerr, git.ErrPartialStale) {
				m.reloadSelectedDiff()
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
// exists (the common whole-file case stays on the fast path). The file
// must be part of this commit (in paths): a hunk pick on a file the user
// then unticked must not sneak into the index. The second map carries
// the hunks the picks index into, so Commit can refuse if the file
// changed since.
func (m *commitModel) partialSelection(paths []string) (map[string][]bool, map[string][]git.Hunk) {
	if m.selectedFile == "" || m.diffLoading || m.diffPath != m.selectedFile || len(m.hunks) == 0 || len(m.hunkCheck) == 0 {
		return nil, nil
	}
	inCommit := false
	for _, p := range paths {
		if p == m.selectedFile {
			inCommit = true
			break
		}
	}
	if !inCommit {
		return nil, nil
	}
	all := true
	for _, on := range m.hunkCheck {
		if !on {
			all = false
			break
		}
	}
	if all {
		return nil, nil
	}
	// Copy so the worker isn't racing the UI's next tick.
	sel := make([]bool, len(m.hunkCheck))
	copy(sel, m.hunkCheck)
	hunks := make([]git.Hunk, len(m.hunks))
	copy(hunks, m.hunks)
	return map[string][]bool{m.selectedFile: sel}, map[string][]git.Hunk{m.selectedFile: hunks}
}

// reloadSelectedDiff forces the selected file's diff + hunk list to be
// re-read (e.g. after a commit refused a stale hunk selection).
func (m *commitModel) reloadSelectedDiff() {
	if m.selectedFile == "" || m.Snap == nil {
		return
	}
	if f := m.Snap.Find(m.selectedFile); f != nil {
		m.diff = nil
		m.diffLoading = false
		m.selectFile(*f)
	}
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
				m.Snap = vcs.WrapGit(snap)
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
		var err error
		var out string
		if m.isSVN() {
			// svn diff already emits unified-diff — write it as the patch.
			// commitOID holds "r<N>"; a non-rN oid falls back to worktree.
			if rev := svnRevNum(commitOID); rev > 0 {
				out, err = m.VCS.SVN.PatchRev(ctx, rev, dest)
			} else {
				out, err = m.VCS.SVN.PatchWorktree(ctx, paths, dest)
			}
		} else {
			repo := git.NewRepo(repoPath)
			if commitOID != "" {
				out, err = repo.CreatePatchFromCommit(ctx, commitOID, dest)
			} else {
				err = repo.CreatePatchFromWorktree(ctx, paths, dest)
			}
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
// files, git apply otherwise; svn patch for an svn working copy.
func (m *commitModel) applyPatch(ctx context.Context, w updater, patchFile string) {
	m.setBusy(w, true, "正在应用补丁…")
	m.gen++
	gen := m.gen
	repoPath := m.RepoPath
	go func() {
		var err error
		var snap *vcs.Snapshot
		var stderr string
		if m.isSVN() {
			// `svn patch` returns a normal *RunResult (E155xxx errors come
			// back as exit≠0 with stderr, same contract as git's).
			// dryRun=false — this menu item means "apply", and a dry run
			// reported success without changing a byte.
			res, serr := m.VCS.SVN.Patch(ctx, patchFile, false)
			err = serr
			if res != nil && !res.ExitOK() {
				stderr = res.OutputOfStderr()
			}
			snap, _ = m.VCS.Snapshot(context.Background())
		} else {
			repo := git.NewRepo(repoPath)
			data, rerr := os.ReadFile(patchFile)
			var res *git.RunResult
			if rerr != nil {
				err = rerr
			} else if git.LooksLikeMailPatch(data) {
				res, err = repo.ApplyMailPatch(ctx, patchFile)
			} else {
				res, err = repo.ApplyPatch(ctx, patchFile)
			}
			if res != nil && !res.ExitOK() {
				stderr = res.OutputOfStderr()
			}
			snap = vcs.WrapGit(mustSnap(repo, context.Background()))
		}
		w.update(func() {
			if gen != m.gen {
				return
			}
			m.setBusyQuiet()
			switch {
			case err != nil:
				m.lastErr = "应用补丁：" + err.Error()
			case stderr != "":
				m.lastErr = stderr
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
