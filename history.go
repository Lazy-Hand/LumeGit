package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"

	"lumegit/internal/git"
	"lumegit/internal/svn"
	"lumegit/internal/vcs"
)

// historyModel holds the Log tab's state for one window: the loaded
// commit list, the selected commit's file list, and the selected file's
// diff. All fields are touched only on the UI thread; background jobs
// publish through updater like commitModel does.
type historyModel struct {
	// Commit list.
	commits    []git.Commit
	loading    bool
	err        string
	selectedID string // OID of the highlighted commit

	// Pagination: pageSize commits per fetch; hasMore when the last page
	// came back full (IDEA lazy-loads the log on scroll).
	pageSize int
	hasMore  bool

	// File list of the selected commit.
	files        []git.CommitFile
	filesLoading bool
	filesErr     string
	selectedFile string // Path of the file whose diff is shown

	// Diff of the selected file (shown in the shared diff pane).
	diff        *git.DiffInfo
	diffLoading bool
	diffErr     string
	diffCancel  context.CancelFunc // cancels an in-flight diff read
	filesCancel context.CancelFunc // cancels an in-flight svn log for the file list

	// gen fences stale async results after a repo switch or refresh.
	gen int
	// filesGen fences the selected commit's file-list and diff loads —
	// bumping it on a new selection keeps an older commit's file list
	// from landing after the user already picked a different commit.
	filesGen int

	// lastLoadedRepo avoids reloading when the tab re-renders for the
	// repo already in memory.
	lastLoadedRepo string

	// Filter bar (IDEA's log filter). Empty = unfiltered.
	filterBranch string
	filterAuthor string
	filterGrep   string
	filterPath   string
	filterAll    bool
	filterActive bool // any filter applied

	// Unpushed commit OIDs for highlighting.
	unpushed map[string]bool

	// Graph lanes for the log, aligned with commits[i].
	graph []git.GraphLine

	// Go-to (hash/branch/tag) jump.
	gotoOpen  bool
	gotoInput string
	gotoErr   string

	// listState is the virtual list's scroll place + row identities.
	listState ui.ListState
	// scrollTarget is an OID scrollToCommit is still paging toward.
	scrollTarget string
	// filesList is the selected-commit file list's virtual-list state.
	filesList ui.ListState

	// svnWC is the backend when the open repo is an svn working copy;
	// nil on git. svnRel is the WC's repo-relative path ("^/trunk") used
	// to strip changed-paths down to WC-relative files; it's filled
	// lazily under svnRelMu (a worker may do the first fetch).
	svnWC        *svn.WC
	svnRel       string
	svnRelFailed bool       // info call already failed; stop retrying
	svnRelMu     sync.Mutex // guards svnRel write/read pairing
}

// setBackend records which VCS the history tab reads; called when the
// repo opens so log/file/diff loads pick the right commands. svnRel is
// resolved lazily by svnRelPath on first use — a synchronous svn info
// here would hold the repo-open path hostage to a slow server.
func (h *historyModel) setBackend(b *vcs.Backend) {
	h.svnRelMu.Lock()
	defer h.svnRelMu.Unlock()
	if b != nil && b.Kind == vcs.KindSVN {
		h.svnWC = b.SVN
	} else {
		h.svnWC = nil
	}
	h.svnRel = ""
	h.svnRelFailed = false
}

// svnRelPath returns the WC's repo-relative prefix ("^/trunk"), filled
// lazily by the first caller that needs it. A mutex covers the
// worker-write/UI-read pairing; once filled it never re-queries (a
// failed svn info caches "" rather than retrying every click).
func (h *historyModel) svnRelPath(ctx context.Context) string {
	if h.svnWC == nil {
		return ""
	}
	h.svnRelMu.Lock()
	defer h.svnRelMu.Unlock()
	if h.svnRel != "" || h.svnRelFailed {
		return h.svnRel
	}
	if info, err := h.svnWC.Info(ctx); err == nil {
		h.svnRel = info.RelativeURL
	} else {
		h.svnRelFailed = true
	}
	return h.svnRel
}

// ensureLoaded starts a log load when the repo changes (or nothing was
// loaded yet). Called from the view each frame; while a load is in
// flight for this repo it stays a no-op — otherwise every repaint
// launched a fresh `git log` and fenced the in-flight one, so a slow
// log could never finish.
func (h *historyModel) ensureLoaded(repoPath string, w updater) {
	if repoPath == "" {
		return
	}
	if h.loading && h.lastLoadedRepo == repoPath {
		return // in-flight load for this repo — let it land
	}
	if h.lastLoadedRepo == repoPath && !h.loading {
		return
	}
	h.load(repoPath, w)
}

// svnRevOID encodes an svn revision as the pseudo-OID the commit list
// carries — the view only ever displays it via ShortOID ("r123"), so
// git's hex-shape expectations never see it.
func svnRevOID(rev int64) string { return "r" + strconv.FormatInt(rev, 10) }

// svnRevNum decodes it back for svn calls; 0 on a non-rN oid.
func svnRevNum(oid string) int64 {
	if !strings.HasPrefix(oid, "r") {
		return 0
	}
	n, _ := strconv.ParseInt(oid[1:], 10, 64)
	return n
}

// load fetches the commit list in a worker. The unpushed set runs
// concurrently — IDEA fans these reads out so the log doesn't wait on
// upstream bookkeeping. SVN fills commits from `svn log -v` with
// r<N>-shaped pseudo-OIDs; unpushed/graph stay empty (svn has no
// upstream tracking or branch graph).
func (h *historyModel) load(repoPath string, w updater) {
	h.gen++
	gen := h.gen
	h.lastLoadedRepo = repoPath
	h.loading = true
	h.err = ""
	h.filterActive = h.filterBranch != "" || h.filterAuthor != "" ||
		h.filterGrep != "" || h.filterPath != "" || h.filterAll
	if h.pageSize == 0 {
		h.pageSize = 50
	}
	go func() {
		if h.svnWC != nil {
			h.svnLoad(repoPath, w, gen)
			return
		}
		repo := git.NewRepo(repoPath)
		type logResult struct {
			commits []git.Commit
			err     error
		}
		logCh := make(chan logResult, 1)
		unpushedCh := make(chan map[string]bool, 1)
		go func() {
			// Ask for one extra row so we know whether a next page exists.
			commits, err := repo.LogFiltered(context.Background(), git.LogOptions{
				Max:    h.pageSize + 1,
				Branch: h.filterBranch,
				Author: h.filterAuthor,
				Grep:   h.filterGrep,
				Path:   h.filterPath,
				All:    h.filterAll,
			})
			logCh <- logResult{commits, err}
		}()
		go func() {
			// Unpushed set for highlight (best-effort — failure just means
			// no highlight, not an error the user cares about here).
			unpushed, _ := repo.UnpushedSet(context.Background())
			unpushedCh <- unpushed
		}()
		lr := <-logCh
		unpushed := <-unpushedCh
		w.update(func() {
			if gen != h.gen {
				return
			}
			h.loading = false
			if lr.err != nil {
				h.err = lr.err.Error()
				h.commits = nil
				return
			}
			h.err = ""
			h.hasMore = len(lr.commits) > h.pageSize
			if h.hasMore {
				lr.commits = lr.commits[:h.pageSize]
			}
			h.commits = lr.commits
			h.graph = git.BuildGraph(lr.commits)
			h.unpushed = unpushed
			h.ensureSelection(repoPath, w)
			if h.scrollTarget != "" {
				h.scrollToCommit(h.scrollTarget, repoPath, w)
			}
		})
	}()
}

// loadMore appends the next page of commits (IDEA: scrolling the log to
// the bottom fetches older entries). No-op while a load is running or
// when the last page came back short.
func (h *historyModel) loadMore(repoPath string, w updater) {
	if repoPath == "" || h.loading || !h.hasMore || len(h.commits) == 0 {
		return
	}
	h.loading = true
	gen := h.gen
	skip := len(h.commits)
	go func() {
		if h.svnWC != nil {
			h.svnLoadMore(repoPath, w, gen, skip)
			return
		}
		repo := git.NewRepo(repoPath)
		commits, err := repo.LogFiltered(context.Background(), git.LogOptions{
			Max:    h.pageSize + 1,
			Skip:   skip,
			Branch: h.filterBranch,
			Author: h.filterAuthor,
			Grep:   h.filterGrep,
			Path:   h.filterPath,
			All:    h.filterAll,
		})
		w.update(func() {
			if gen != h.gen {
				return
			}
			h.loading = false
			if err != nil {
				h.err = err.Error()
				return
			}
			h.hasMore = len(commits) > h.pageSize
			if h.hasMore {
				commits = commits[:h.pageSize]
			}
			h.commits = append(h.commits, commits...)
			h.graph = git.BuildGraph(h.commits)
			// A pending scrollToCommit keeps paging until the target
			// shows up or the log runs out.
			if h.scrollTarget != "" {
				h.scrollToCommit(h.scrollTarget, repoPath, w)
			}
		})
	}()
}

// ensureSelection keeps a valid commit selected: keep the current one
// if still present, else pick the newest.
func (h *historyModel) ensureSelection(repoPath string, w updater) {
	for i := range h.commits {
		if h.commits[i].OID == h.selectedID {
			return
		}
	}
	// A reflog "查看" may have picked a commit on an unloaded page —
	// don't reset the selection while scrollToCommit is still paging.
	if h.scrollTarget != "" {
		return
	}
	h.selectedID = ""
	h.files = nil
	h.selectedFile = ""
	h.diff = nil
	if len(h.commits) > 0 {
		h.selectCommit(repoPath, h.commits[0].OID, w)
	}
}

// selectCommit records the pick and loads its file list.
func (h *historyModel) selectCommit(repoPath, oid string, w updater) {
	if h.svnWC != nil {
		h.svnSelectCommit(repoPath, oid, w)
		return
	}
	if h.selectedID == oid && h.files != nil {
		return
	}
	h.selectedID = oid
	h.files = nil
	h.filesErr = ""
	h.filesLoading = true
	h.selectedFile = ""
	h.diff = nil
	h.diffErr = ""
	h.filesGen++
	gen := h.filesGen
	go func() {
		repo := git.NewRepo(repoPath)
		files, err := repo.CommitFiles(context.Background(), oid)
		w.update(func() {
			if gen != h.filesGen || h.selectedID != oid {
				return
			}
			h.filesLoading = false
			if err != nil {
				h.filesErr = err.Error()
				return
			}
			h.files = files
			// Auto-select the first file so the diff pane shows content.
			if len(files) > 0 {
				h.selectCommitFile(repoPath, oid, files[0], w)
			}
		})
	}()
}

// selectCommitFile records the file pick and loads its diff. Selecting
// another file cancels the previous read — a big diff no longer burns
// CPU producing output nobody will see.
func (h *historyModel) selectCommitFile(repoPath, oid string, f git.CommitFile, w updater) {
	if h.svnWC != nil {
		h.svnSelectCommitFile(repoPath, oid, f, w)
		return
	}
	if h.diffCancel != nil {
		h.diffCancel()
		h.diffCancel = nil
	}
	h.selectedFile = f.Path
	h.diff = nil
	h.diffErr = ""
	h.diffLoading = true
	gen := h.filesGen // same gen as the file-list load that spawned us
	ctx, cancel := context.WithCancel(context.Background())
	h.diffCancel = cancel
	go func() {
		repo := git.NewRepo(repoPath)
		d, err := repo.CommitFileDiff(ctx, oid, f)
		w.update(func() {
			cancel()
			if gen != h.filesGen || h.selectedID != oid || h.selectedFile != f.Path {
				return
			}
			h.diffLoading = false
			h.diffCancel = nil
			if err != nil {
				h.diffErr = err.Error()
				return
			}
			h.diff = d
		})
	}()
}

// reset clears all loaded state; called when the repo switches so the
// next ensureLoaded sees a clean model.
func (h *historyModel) reset() {
	h.commits = nil
	h.selectedID = ""
	h.files = nil
	h.selectedFile = ""
	h.diff = nil
	h.err = ""
	h.filesErr = ""
	h.diffErr = ""
	h.lastLoadedRepo = ""
	h.scrollTarget = ""
	h.gen++
	h.filesGen++
}

// reloadFiltered re-runs the log load with the current filter fields.
func (h *historyModel) reloadFiltered(repoPath string, w updater) {
	h.lastLoadedRepo = ""
	h.load(repoPath, w)
}

// scrollToCommit scrolls the virtual list to the row showing oid. When
// the commit isn't in the loaded pages yet it kicks loadMore and tries
// again once that page lands — the pending OID rides in scrollTarget
// so successive pages keep hunting until found or the log runs out.
func (h *historyModel) scrollToCommit(oid, repoPath string, w updater) {
	for i := range h.commits {
		if h.commits[i].OID == oid {
			h.listState.ScrollIntoView(i)
			h.scrollTarget = ""
			return
		}
	}
	if h.hasMore {
		h.scrollTarget = oid
		h.loadMore(repoPath, w)
	} else {
		h.scrollTarget = ""
	}
}

// gotoCommit jumps the selection to a hash prefix, branch or tag name.
func (h *historyModel) gotoCommit(repoPath, what string, w updater) {
	want := what
	// Match a loaded commit by oid prefix / ref name / short oid.
	for i := range h.commits {
		cm := &h.commits[i]
		if cm.OID == want || cm.ShortOID == want ||
			len(want) >= 7 && len(cm.OID) >= len(want) && cm.OID[:len(want)] == want {
			h.selectCommit(repoPath, cm.OID, w)
			h.gotoOpen = false
			return
		}
		for _, r := range cm.Refs {
			if r == want {
				h.selectCommit(repoPath, cm.OID, w)
				h.gotoOpen = false
				return
			}
		}
	}
	h.gotoErr = "找不到：" + want
}

// --- svn backend ---------------------------------------------------------

// svnLogOptions maps the shared filter bar onto svn log flags. svn has
// no per-branch filter — Target bounds by path instead.
func (h *historyModel) svnLogOptions() svn.LogOptions {
	// HEAD:0 — a mixed-revision WC's BASE can be older than HEAD (a
	// commit bumps only the committed path's BASE), so omitting -r
	// silently drops newer commits from the log view.
	o := svn.LogOptions{Verbose: true, Rev: "HEAD:0"}
	if h.filterPath != "" {
		o.Target = h.filterPath
	}
	if h.filterGrep != "" {
		o.Search = h.filterGrep
	}
	return o
}

// svnCommitShape converts an svn Revision to the git.Commit the list
// renders: OID/ShortOID carry "r<N>", Refs stay empty (no branches).
func svnCommitShape(r svn.Revision) git.Commit {
	oid := svnRevOID(r.Rev)
	return git.Commit{
		OID:      oid,
		ShortOID: oid,
		Author:   r.Author,
		When:     r.Date,
		Subject:  firstLine(r.Message),
	}
}

// svnRevBounds computes the -r N:M window for a page. Rev 0 is a valid
// lower bound (repo creation); stopOnCopy keeps the branch-local view.
func svnRevBounds(skip, limit int64) string {
	// HEAD:something descending — newest first, same as git log.
	return "HEAD:0"
}

// svnLoad fills commits for an svn working copy. Runs inside load's
// goroutine — gen fencing already ran.
func (h *historyModel) svnLoad(repoPath string, w updater, gen int) {
	o := h.svnLogOptions()
	o.Limit = h.pageSize + 1
	revs, err := h.svnWC.Log(context.Background(), o)
	w.update(func() {
		if gen != h.gen {
			return
		}
		h.loading = false
		if err != nil {
			h.err = err.Error()
			h.commits = nil
			return
		}
		h.err = ""
		h.hasMore = len(revs) > h.pageSize
		if h.hasMore {
			revs = revs[:h.pageSize]
		}
		h.commits = make([]git.Commit, 0, len(revs))
		for _, r := range revs {
			h.commits = append(h.commits, svnCommitShape(r))
		}
		h.graph = nil // svn has no merge graph to draw
		h.unpushed = nil
		h.ensureSelection(repoPath, w)
	})
}

// svnLoadMore pages older revisions: svn log is a linear timeline, so
// paging is just a larger limit — no skip offset exists. Re-asks with
// the bigger window and keeps the tail.
func (h *historyModel) svnLoadMore(repoPath string, w updater, gen int, skip int) {
	o := h.svnLogOptions()
	o.Limit = skip + h.pageSize + 1
	revs, err := h.svnWC.Log(context.Background(), o)
	w.update(func() {
		if gen != h.gen {
			return
		}
		h.loading = false
		if err != nil {
			h.err = err.Error()
			return
		}
		if len(revs) <= skip {
			// Filter changed mid-page or the log shrank — nothing to
			// append; indexing revs[skip:] would panic.
			h.hasMore = false
			return
		}
		h.hasMore = len(revs) > skip+h.pageSize
		var next []git.Commit
		for _, r := range revs[skip:] {
			next = append(next, svnCommitShape(r))
		}
		if h.hasMore && len(next) > h.pageSize {
			next = next[:h.pageSize]
		}
		h.commits = append(h.commits, next...)
	})
}

// svnFilesForRev lists one revision's changed paths as CommitFile rows.
// cp.Path is repo-root-absolute ("/trunk/x.go"); the WC may be rooted at
// a subpath ("/trunk"), so strip that prefix to get the WC-relative path
// the diff/open calls resolve. Files outside the WC's subtree are
// skipped — they belong to a sibling branch/tag the user can't see here.
func svnFilesForRev(r svn.Revision, wcRel string) []git.CommitFile {
	// wcRel is the WC's repo-relative path "^/trunk" → "trunk/".
	prefix := strings.TrimPrefix(wcRel, "^/")
	prefix = strings.TrimSuffix(prefix, "/")
	if prefix != "" {
		prefix += "/"
	}
	var out []git.CommitFile
	for _, cp := range r.ChangedPaths {
		repoPath := strings.TrimPrefix(cp.Path, "/")
		// Only files under the WC's subtree are relevant.
		if prefix != "" && !strings.HasPrefix(repoPath+"/", prefix) && repoPath != strings.TrimSuffix(prefix, "/") {
			continue
		}
		rel := strings.TrimPrefix(repoPath, prefix)
		orig := strings.TrimPrefix(strings.TrimPrefix(cp.CopyFrom, "/"), prefix)
		status := byte('M')
		switch cp.Action {
		case "A":
			status = 'A'
		case "D":
			status = 'D'
		case "R":
			status = 'R'
		}
		out = append(out, git.CommitFile{
			Path:     rel,
			OrigPath: orig,
			Status:   status,
		})
	}
	return out
}

// svnSelectCommit loads the file list for one svn revision. Parallel
// to selectCommit but reads the rev's changed-paths instead of
// `git show --name-status`. A re-pick cancels the in-flight log —
// svn log on a remote server can sit in the 45s timeout long after
// the user clicked away.
func (h *historyModel) svnSelectCommit(repoPath, oid string, w updater) {
	if h.selectedID == oid && h.files != nil {
		return
	}
	h.selectedID = oid
	h.files = nil
	h.filesErr = ""
	h.filesLoading = true
	h.selectedFile = ""
	h.diff = nil
	h.diffErr = ""
	if h.filesCancel != nil {
		h.filesCancel()
	}
	gen := h.gen
	rev := svnRevNum(oid)
	// File-list fetches get a tighter cap than commit ops — 45s feels
	// broken to a user picking commits one after another.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	h.filesCancel = cancel
	go func() {
		defer cancel()
		// -c rev on the WC gives this rev's changed-paths alone.
		revs, err := h.svnWC.Log(ctx, svn.LogOptions{
			Rev:     strconv.FormatInt(rev, 10),
			Verbose: true,
		})
		rel := h.svnRelPath(ctx)
		w.update(func() {
			if gen != h.gen || h.selectedID != oid {
				return
			}
			h.filesLoading = false
			h.filesCancel = nil
			if err != nil {
				if ctx.Err() == context.Canceled {
					return // newer pick already replaced this state
				}
				h.filesErr = err.Error()
				return
			}
			if len(revs) == 0 {
				h.files = nil
				return
			}
			h.files = svnFilesForRev(revs[0], rel)
			if len(h.files) > 0 {
				h.svnSelectCommitFile(repoPath, oid, h.files[0], w)
			}
		})
	}()
}

// svnSelectCommitFile diffs one file inside a revision: cat at rev-1
// vs rev, or "" for adds/deletes — same two-side DiffInfo the pane
// renders for git.
func (h *historyModel) svnSelectCommitFile(repoPath, oid string, f git.CommitFile, w updater) {
	if h.diffCancel != nil {
		h.diffCancel()
		h.diffCancel = nil
	}
	h.selectedFile = f.Path
	h.diff = nil
	h.diffErr = ""
	h.diffLoading = true
	gen := h.gen
	ctx, cancel := context.WithCancel(context.Background())
	h.diffCancel = cancel
	rev := svnRevNum(oid)
	go func() {
		defer cancel()
		var d *git.DiffInfo
		var err error
		wc := h.svnWC
		oldPath := f.Path
		if f.OrigPath != "" {
			oldPath = f.OrigPath
		}
		var oldT, newT string
		if f.Status != 'A' {
			oldT, err = wc.Cat(ctx, oldPath, rev-1)
			if err != nil {
				goto done
			}
		}
		if f.Status != 'D' {
			newT, err = wc.Cat(ctx, f.Path, rev)
			if err != nil {
				goto done
			}
		}
		d = &git.DiffInfo{
			Path:     f.Path,
			OldPath:  f.OrigPath,
			Old:      []byte(oldT),
			New:      []byte(newT),
			OldLabel: fmt.Sprintf("r%d", rev-1),
			NewLabel: oid,
			Renamed:  f.OrigPath != "" && f.OrigPath != f.Path,
			Binary:   looksBinary(oldT) || looksBinary(newT),
		}
	done:
		w.update(func() {
			if gen != h.gen || h.selectedID != oid || h.selectedFile != f.Path {
				return
			}
			h.diffLoading = false
			h.diffCancel = nil
			if err != nil {
				h.diffErr = err.Error()
				return
			}
			h.diff = d
		})
	}()
}

// firstLine truncates a multi-line svn message to the subject row.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
