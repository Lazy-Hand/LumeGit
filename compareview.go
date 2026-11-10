package main

import (
	"context"

	"lumegit/internal/git"
)

// compareModel backs the "与分支比较" overlay. Two modes share the
// same shape IDEA uses: single-ref (ref↔HEAD changed files, "Compare
// with Current") and two-ref (branch↔branch unique commits + diff,
// "Compare with 'xxx'").
type compareModel struct {
	open    bool
	ref     string // the other side (branch/tag/commit) — single mode
	refB    string // second ref — two-ref mode; empty = single mode
	files   []git.CommitFile
	loading bool
	err     string

	// Two-ref results (ref↔refB); nil in single mode.
	cmp *git.BranchCompare

	selected string
	diff     *git.DiffInfo
	diffLoad bool
	diffErr  string

	gen int
}

// openFor starts a branch-vs-HEAD comparison (single mode).
func (cm *compareModel) openFor(repoPath, ref string, w updater) {
	cm.open = true
	cm.ref = ref
	cm.refB = ""
	cm.cmp = nil
	cm.selected = ""
	cm.diff = nil
	cm.diffErr = ""
	cm.load(repoPath, w)
}

// openForBranches opens a two-ref compare (IDEA: "Compare with 'xxx'").
func (cm *compareModel) openForBranches(repoPath, a, b string, w updater) {
	cm.open = true
	cm.ref = a
	cm.refB = b
	cm.cmp = nil
	cm.files = nil
	cm.selected = ""
	cm.diff = nil
	cm.diffErr = ""
	cm.gen++
	gen := cm.gen
	cm.loading = true
	cm.err = ""
	go func() {
		repo := git.NewRepo(repoPath)
		cmp, err := repo.CompareBranches(context.Background(), a, b)
		w.update(func() {
			if gen != cm.gen {
				return
			}
			cm.loading = false
			if err != nil {
				cm.err = err.Error()
				return
			}
			cm.cmp = cmp
			cm.files = cmp.Files
		})
	}()
}

func (cm *compareModel) load(repoPath string, w updater) {
	cm.gen++
	gen := cm.gen
	cm.loading = true
	cm.err = ""
	go func() {
		repo := git.NewRepo(repoPath)
		files, err := repo.CompareFiles(context.Background(), cm.ref)
		w.update(func() {
			if gen != cm.gen {
				return
			}
			cm.loading = false
			if err != nil {
				cm.err = err.Error()
				return
			}
			cm.files = files
		})
	}()
}

// selectFile loads one path's diff. Single mode → ref↔HEAD;
// two-ref mode → base↔refB (what merging would bring).
func (cm *compareModel) selectFile(repoPath, path string, w updater) {
	cm.selected = path
	cm.diff = nil
	cm.diffErr = ""
	cm.diffLoad = true
	cm.gen++
	gen := cm.gen
	// Pick the right ref pair.
	from, to := cm.ref, "HEAD"
	if cm.refB != "" {
		if cm.cmp != nil && !cm.cmp.Unrelated && cm.cmp.Base != "" {
			from = cm.cmp.Base
		} else {
			from = cm.ref
		}
		to = cm.refB
	}
	go func() {
		repo := git.NewRepo(repoPath)
		d, err := repo.CompareCommitsDiff(context.Background(), from, to, path)
		w.update(func() {
			if gen != cm.gen {
				return
			}
			cm.diffLoad = false
			if err != nil {
				cm.diffErr = err.Error()
				return
			}
			cm.diff = d
		})
	}()
}
