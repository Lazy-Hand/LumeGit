package main

import (
	"context"

	"lumegit/internal/git"
)

// fileHistModel backs the file-history overlay: one file's commit
// trail and its blame view. Opened from a file row's context menu.
type fileHistModel struct {
	open bool
	path string

	// Sub-tab: 0 = 历史 (commits touching the file), 1 = 溯源 (blame).
	mode int

	// History list.
	commits    []git.Commit
	loading    bool
	err        string
	selectedID string

	// Diff for the selected commit.
	diff        *git.DiffInfo
	diffLoading bool
	diffErr     string

	// Blame.
	blame        []git.BlameLine
	blameLoading bool
	blameErr     string

	gen int
}

// openFor starts loading history+blame for path.
func (f *fileHistModel) openFor(repoPath, path string, w updater) {
	if f.path != path {
		f.selectedID = ""
		f.commits = nil
		f.blame = nil
		f.diff = nil
	}
	f.open = true
	f.path = path
	f.gen++
	f.loadHistory(repoPath, w)
	f.loadBlame(repoPath, w)
}

func (f *fileHistModel) close() { f.open = false }

// loadHistory fetches the file's commit trail.
func (f *fileHistModel) loadHistory(repoPath string, w updater) {
	gen := f.gen
	path := f.path
	f.loading = true
	f.err = ""
	go func() {
		repo := git.NewRepo(repoPath)
		commits, err := repo.FileLog(context.Background(), path, 200)
		w.update(func() {
			if gen != f.gen || f.path != path {
				return
			}
			f.loading = false
			if err != nil {
				f.err = err.Error()
				return
			}
			f.commits = commits
			if len(commits) > 0 && f.selectedID == "" {
				f.selectCommit(repoPath, commits[0].OID, w)
			}
		})
	}()
}

// selectCommit picks a commit and loads the file's diff at it.
func (f *fileHistModel) selectCommit(repoPath, oid string, w updater) {
	f.selectedID = oid
	f.diff = nil
	f.diffErr = ""
	f.diffLoading = true
	gen := f.gen
	path := f.path
	go func() {
		repo := git.NewRepo(repoPath)
		// Diff = parent vs commit for this file.
		cf := git.CommitFile{Path: path, Status: 'M'}
		d, err := repo.CommitFileDiff(context.Background(), oid, cf)
		w.update(func() {
			if gen != f.gen || f.selectedID != oid {
				return
			}
			f.diffLoading = false
			if err != nil {
				f.diffErr = err.Error()
				return
			}
			f.diff = d
		})
	}()
}

// loadBlame fetches the line-level attribution.
func (f *fileHistModel) loadBlame(repoPath string, w updater) {
	gen := f.gen
	path := f.path
	f.blameLoading = true
	f.blameErr = ""
	go func() {
		repo := git.NewRepo(repoPath)
		lines, err := repo.Blame(context.Background(), "HEAD", path)
		w.update(func() {
			if gen != f.gen || f.path != path {
				return
			}
			f.blameLoading = false
			if err != nil {
				f.blameErr = err.Error()
				return
			}
			f.blame = lines
		})
	}()
}

// reset clears state on repo switch.
func (f *fileHistModel) reset() {
	f.open = false
	f.path = ""
	f.commits = nil
	f.blame = nil
	f.diff = nil
	f.selectedID = ""
	f.gen++
}
