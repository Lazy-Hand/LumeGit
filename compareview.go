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

	// worktreeMode marks a ref↔working-tree compare ("与工作区比较"):
	// the file list comes from `git diff ref` instead of ref↔HEAD.
	// worktreePath scopes it to one file ("" = whole repo).
	worktreeMode bool
	worktreePath string
}

// openFor starts a branch-vs-HEAD comparison (IDEA: "与当前分支比较" /
// "Compare with Current"). It shares the two-sided CompareBranches
// result — commits each side has that the other doesn't plus the
// merge-base→ref file diff — so the modal shows the same shape as
// "Compare with 'xxx'".
func (cm *compareModel) openFor(repoPath, ref string, w updater) {
	cm.open = true
	cm.ref = ref
	cm.refB = ""
	cm.cmp = nil
	cm.files = nil
	cm.selected = ""
	cm.diff = nil
	cm.diffErr = ""
	cm.worktreeMode = false
	cm.worktreePath = ""
	cm.load(repoPath, w)
}

// openForWorktree compares ref against the working tree (IDEA:
// "Compare with Local"). path non-empty restricts to one file —
// invoked from a commit-file context menu, where the compare opens
// straight onto the diff.
func (cm *compareModel) openForWorktree(repoPath, ref, path string, w updater) {
	cm.open = true
	cm.ref = ref
	cm.refB = ""
	cm.cmp = nil
	cm.files = nil
	cm.selected = ""
	cm.diff = nil
	cm.diffErr = ""
	cm.worktreeMode = true
	cm.worktreePath = path
	if path != "" {
		// Single-file mode: the row is fixed, load its diff at once.
		cm.selectFile(repoPath, path, w)
		return
	}
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
	cm.worktreeMode = false
	cm.worktreePath = ""
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
	ref, path := cm.ref, cm.worktreePath
	worktree := cm.worktreePath != "" || cm.worktreeMode
	go func() {
		repo := git.NewRepo(repoPath)
		if !worktree {
			// Single mode: CompareBranches(HEAD, ref) — the file list is
			// merge-base→b=ref, "what the ref would bring into HEAD",
			// matching IDEA's "Compare with Current".
			cmp, err := repo.CompareBranches(context.Background(), "HEAD", ref)
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
			return
		}
		files, err := repo.CompareFilesWorktree(context.Background(), ref, path)
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
	// Re-clicking the file already shown (double-click, keyboard
	// re-select) must not bounce the pane through a blank reload.
	if cm.selected == path && cm.diff != nil && !cm.diffLoad && cm.diffErr == "" {
		return
	}
	cm.selected = path
	cm.diff = nil
	cm.diffErr = ""
	cm.diffLoad = true
	cm.gen++
	gen := cm.gen
	if cm.worktreeMode {
		// ref↔working tree: the new side is the file on disk — a path
		// deleted locally shows old-only (CompareFileDiff tolerates a
		// missing worktree side). catFile errors like "path does not
		// exist in 'ref'" used to surface as a diffErr the user read as
		// "nothing happened"; a file absent on a side is a valid diff.
		ref := cm.ref
		go func() {
			repo := git.NewRepo(repoPath)
			d, err := repo.CompareFileDiff(context.Background(), ref, path)
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
		return
	}
	// The diff must cover the same endpoints the file list was built
	// from, or a listed file opens blank.
	var from, to string
	if cm.cmp != nil && cm.cmp.FilesFrom != "" {
		from, to = cm.cmp.FilesFrom, cm.cmp.FilesTo
	} else if cm.refB != "" {
		from, to = cm.ref, cm.refB
	} else {
		from, to = cm.ref, "HEAD"
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
