package main

import (
	"context"

	"lumegit/internal/git"
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

	// File list of the selected commit.
	files        []git.CommitFile
	filesLoading bool
	filesErr     string
	selectedFile string // Path of the file whose diff is shown

	// Diff of the selected file (shown in the shared diff pane).
	diff        *git.DiffInfo
	diffLoading bool
	diffErr     string

	// gen fences stale async results after a repo switch or refresh.
	gen int

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
}

// ensureLoaded starts a log load when the repo changes (or nothing was
// loaded yet). Called from the view each frame; cheap once loaded.
func (h *historyModel) ensureLoaded(repoPath string, w updater) {
	if repoPath == "" {
		return
	}
	if h.lastLoadedRepo == repoPath && !h.loading {
		return
	}
	h.load(repoPath, w)
}

// load fetches the commit list in a worker.
func (h *historyModel) load(repoPath string, w updater) {
	h.gen++
	gen := h.gen
	h.lastLoadedRepo = repoPath
	h.loading = true
	h.err = ""
	h.filterActive = h.filterBranch != "" || h.filterAuthor != "" ||
		h.filterGrep != "" || h.filterPath != "" || h.filterAll
	go func() {
		repo := git.NewRepo(repoPath)
		commits, err := repo.LogFiltered(context.Background(), git.LogOptions{
			Max:    300,
			Branch: h.filterBranch,
			Author: h.filterAuthor,
			Grep:   h.filterGrep,
			Path:   h.filterPath,
			All:    h.filterAll,
		})
		// Unpushed set for highlight (best-effort — failure just means
		// no highlight, not an error the user cares about here).
		unpushed, _ := repo.UnpushedSet(context.Background())
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
			h.commits = commits
			h.graph = git.BuildGraph(commits)
			h.unpushed = unpushed
			h.ensureSelection(repoPath, w)
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
	gen := h.gen
	go func() {
		repo := git.NewRepo(repoPath)
		files, err := repo.CommitFiles(context.Background(), oid)
		w.update(func() {
			if gen != h.gen || h.selectedID != oid {
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

// selectCommitFile records the file pick and loads its diff.
func (h *historyModel) selectCommitFile(repoPath, oid string, f git.CommitFile, w updater) {
	h.selectedFile = f.Path
	h.diff = nil
	h.diffErr = ""
	h.diffLoading = true
	gen := h.gen
	go func() {
		repo := git.NewRepo(repoPath)
		d, err := repo.CommitFileDiff(context.Background(), oid, f)
		w.update(func() {
			if gen != h.gen || h.selectedID != oid || h.selectedFile != f.Path {
				return
			}
			h.diffLoading = false
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
	h.gen++
}

// reloadFiltered re-runs the log load with the current filter fields.
func (h *historyModel) reloadFiltered(repoPath string, w updater) {
	h.lastLoadedRepo = ""
	h.load(repoPath, w)
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
