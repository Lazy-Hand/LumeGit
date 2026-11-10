package main

import (
	"context"

	"lumegit/internal/git"
)

// conflictModel backs the three-way resolver modal.
type conflictModel struct {
	open bool
	path string

	sides    *git.ConflictDiff
	loading  bool
	err      string
	busy     bool
	merged   string // editable merged text for ResolveMerge
	editMode bool   // user chose "manual edit"

	gen int
}

// openFor loads a conflicted file's three sides.
func (cm *conflictModel) openFor(repoPath, path string, w updater) {
	cm.open = true
	cm.path = path
	cm.gen++
	gen := cm.gen
	cm.loading = true
	cm.err = ""
	cm.sides = nil
	cm.editMode = false
	cm.merged = ""
	go func() {
		repo := git.NewRepo(repoPath)
		sides, err := repo.ConflictSides(context.Background(), path)
		w.update(func() {
			if gen != cm.gen {
				return
			}
			cm.loading = false
			if err != nil {
				cm.err = err.Error()
				return
			}
			cm.sides = sides
			// Pre-fill the merge editor with ours (user edits from there).
			cm.merged = string(sides.Ours)
		})
	}()
}

// resolve applies one side or the merged text, marks resolved, closes.
func (cm *conflictModel) resolve(repoPath string, how git.ConflictResolution, w updater, after func()) {
	if cm.busy {
		return
	}
	cm.busy = true
	gen := cm.gen
	path := cm.path
	merged := []byte(cm.merged)
	go func() {
		repo := git.NewRepo(repoPath)
		err := repo.ResolveConflict(context.Background(), path, how, merged)
		w.update(func() {
			if gen != cm.gen {
				return
			}
			cm.busy = false
			if err != nil {
				cm.err = err.Error()
				return
			}
			cm.open = false
			if after != nil {
				after()
			}
		})
	}()
}

func (cm *conflictModel) close() { cm.open = false }

// --- Resolve Conflicts dialog (IDEA's "Resolve Conflicts…") -----------

// conflictsModel backs the list-style dialog that opens from the
// commit tab's "解决冲突…" button: all unmerged paths at once, with
// per-file / batch "ours / theirs / merge" actions.
type conflictsModel struct {
	open    bool
	entries []git.ConflictEntry
	loading bool
	err     string
	busy    bool
	gen     int
}

// openFor loads the unmerged list for the current repo.
func (cm *conflictsModel) openFor(repoPath string, w updater) {
	cm.open = true
	cm.load(repoPath, w)
}

func (cm *conflictsModel) close() { cm.open = false }

// load refreshes the entries.
func (cm *conflictsModel) load(repoPath string, w updater) {
	cm.gen++
	gen := cm.gen
	cm.loading = true
	cm.err = ""
	go func() {
		repo := git.NewRepo(repoPath)
		list, err := repo.ConflictsDetailed(context.Background())
		w.update(func() {
			if gen != cm.gen {
				return
			}
			cm.loading = false
			if err != nil {
				cm.err = err.Error()
				return
			}
			cm.entries = list
		})
	}()
}

// resolveOne takes ours/theirs for a single path, then reloads so the
// row disappears once marked resolved.
func (cm *conflictsModel) resolveOne(repoPath, path string, how git.ConflictResolution, w updater, after func()) {
	if cm.busy {
		return
	}
	cm.busy = true
	gen := cm.gen
	go func() {
		repo := git.NewRepo(repoPath)
		err := repo.ResolveConflict(context.Background(), path, how, nil)
		list, _ := repo.ConflictsDetailed(context.Background())
		w.update(func() {
			if gen != cm.gen {
				return
			}
			cm.busy = false
			if err != nil {
				cm.err = err.Error()
				return
			}
			cm.entries = list
			if after != nil {
				after()
			}
		})
	}()
}

// resolveAll applies ours/theirs to every remaining entry.
func (cm *conflictsModel) resolveAll(repoPath string, how git.ConflictResolution, w updater, after func()) {
	if cm.busy {
		return
	}
	cm.busy = true
	gen := cm.gen
	go func() {
		repo := git.NewRepo(repoPath)
		var err error
		if how == git.ResolveTheirs {
			err = repo.ResolveAllTheirs(context.Background())
		} else {
			err = repo.ResolveAllOurs(context.Background())
		}
		list, _ := repo.ConflictsDetailed(context.Background())
		w.update(func() {
			if gen != cm.gen {
				return
			}
			cm.busy = false
			if err != nil {
				cm.err = err.Error()
				return
			}
			cm.entries = list
			if after != nil {
				after()
			}
		})
	}()
}
