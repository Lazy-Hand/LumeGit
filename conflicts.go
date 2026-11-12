package main

import (
	"context"
	"os"
	"path/filepath"

	"lumegit/internal/git"
	"lumegit/internal/svn"
	"lumegit/internal/vcs"
)

// conflictModel backs the three-way resolver modal.
type conflictModel struct {
	open bool
	path string

	// backend: svn WC when the open repo is svn — sides come from the
	// marker file + svn's .mine/.rN files instead of index stages.
	backend *vcs.Backend

	sides    *git.ConflictDiff
	loading  bool
	err      string
	busy     bool
	merged   string // editable merged text for ResolveMerge
	editMode bool   // user chose "manual edit"

	gen int
}

// setBackend records which VCS this resolver reads; called on repo open.
func (cm *conflictModel) setBackend(b *vcs.Backend) { cm.backend = b }

func (cm *conflictModel) isSVN() bool {
	return cm.backend != nil && cm.backend.Kind == vcs.KindSVN
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
	backend := cm.backend
	go func() {
		var sides *git.ConflictDiff
		var err error
		if backend != nil && backend.Kind == vcs.KindSVN {
			sides, err = svnConflictSides(backend, repoPath, path)
		} else {
			repo := git.NewRepo(repoPath)
			sides, err = repo.ConflictSides(context.Background(), path)
		}
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
			if sides.TreeConflict {
				return // no side files — merged text stays empty
			}
			// Pre-fill the merge editor with what git already resolved:
			// the worktree file contains auto-merged hunks plus the
			// remaining conflict markers — overwriting that with the
			// whole "ours" blob silently drops the clean merges.
			if wt, rerr := os.ReadFile(filepath.Join(repoPath, filepath.FromSlash(path))); rerr == nil && len(wt) > 0 {
				cm.merged = string(wt)
			} else {
				cm.merged = string(sides.Ours)
			}
		})
	}()
}

// svnConflictSides reads an svn conflict's pieces: the worktree file
// itself (markers + local edits = "ours"), the .mine/.rN byproducts
// svn leaves beside it for base/theirs.
func svnConflictSides(b *vcs.Backend, repoPath, path string) (*git.ConflictDiff, error) {
	full := filepath.Join(repoPath, filepath.FromSlash(path))
	read := func(p string) []byte {
		b, _ := os.ReadFile(p)
		return b
	}
	// svn leaves <file>.mine plus <file>.r<old>/.r<new> — the .mine is
	// pre-update local, the file itself holds markers. Find the highest
	// .rN = newest (theirs), lowest = base.
	var revs []os.DirEntry
	dir := filepath.Dir(full)
	base := filepath.Base(full)
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if len(e.Name()) > len(base)+2 && e.Name()[:len(base)+2] == base+".r" {
			revs = append(revs, e)
		}
	}
	sortRevs(revs)
	var theirs, anc []byte
	if len(revs) > 0 {
		anc = read(filepath.Join(dir, revs[0].Name()))
		theirs = read(filepath.Join(dir, revs[len(revs)-1].Name()))
	}
	ours := read(full)
	mine := read(full + ".mine")
	if mine != nil {
		ours = mine
	}
	// A tree conflict leaves no .mine/.rN byproducts — every side reads
	// empty, and letting the merge editor "save" would write a zero-byte
	// file over whatever is on disk.
	treeConflict := mine == nil && len(revs) == 0
	return &git.ConflictDiff{
		Path:         path,
		Base:         anc,
		Ours:         ours,
		Their:        theirs,
		TreeConflict: treeConflict,
	}, nil
}

// sortRevs orders <name>.r<N> by N ascending.
func sortRevs(es []os.DirEntry) {
	num := func(e os.DirEntry) int64 {
		n := int64(0)
		for _, ch := range e.Name() {
			if ch < '0' || ch > '9' {
				continue
			}
			n = n*10 + int64(ch-'0')
		}
		return n
	}
	for i := 1; i < len(es); i++ {
		for j := i; j > 0 && num(es[j]) < num(es[j-1]); j-- {
			es[j], es[j-1] = es[j-1], es[j]
		}
	}
}

// resolve applies one side or the merged text, marks resolved, closes.
// svn maps: ours → working, theirs → theirs-full, merged → write then
// resolve --accept=working (svn considers the file fixed once it holds
// no markers the user left).
func (cm *conflictModel) resolve(repoPath string, how git.ConflictResolution, w updater, after func()) {
	if cm.busy {
		return
	}
	cm.busy = true
	gen := cm.gen
	path := cm.path
	merged := []byte(cm.merged)
	backend := cm.backend
	go func() {
		var err error
		if backend != nil && backend.Kind == vcs.KindSVN {
			err = svnResolve(backend, repoPath, path, how, merged)
		} else {
			repo := git.NewRepo(repoPath)
			err = repo.ResolveConflict(context.Background(), path, how, merged)
		}
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

// svnResolve maps git's resolution choices onto svn resolve --accept.
func svnResolve(b *vcs.Backend, repoPath, path string, how git.ConflictResolution, merged []byte) error {
	full := filepath.Join(repoPath, filepath.FromSlash(path))
	var acc svn.Accept
	switch how {
	case git.ResolveOurs:
		acc = svn.AcceptMineFull
	case git.ResolveTheirs:
		acc = svn.AcceptTheirsFull
	default: // ResolveMerge — write the merged text, accept working.
		if err := os.WriteFile(full, merged, 0o644); err != nil {
			return err
		}
		acc = svn.AcceptWorking
	}
	res, err := b.SVN.Resolve(context.Background(), path, acc)
	if err == nil && res != nil && !res.ExitOK() {
		err = &svn.Error{Args: res.Args, Stderr: res.OutputOfStderr()}
	}
	return err
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

	backend *vcs.Backend
}

// setBackend records which VCS this dialog reads; called on repo open.
func (cm *conflictsModel) setBackend(b *vcs.Backend) { cm.backend = b }

func (cm *conflictsModel) isSVN() bool {
	return cm.backend != nil && cm.backend.Kind == vcs.KindSVN
}

// openFor loads the unmerged list for the current repo.
func (cm *conflictsModel) openFor(repoPath string, w updater) {
	cm.open = true
	cm.load(repoPath, w)
}

func (cm *conflictsModel) close() { cm.open = false }

// load refreshes the entries. On svn the list comes from `svn status`
// conflicted rows instead of `git ls-files -u`.
func (cm *conflictsModel) load(repoPath string, w updater) {
	cm.gen++
	gen := cm.gen
	cm.loading = true
	cm.err = ""
	backend := cm.backend
	go func() {
		var list []git.ConflictEntry
		var err error
		if backend != nil && backend.Kind == vcs.KindSVN {
			list, err = svnConflictList(backend)
		} else {
			repo := git.NewRepo(repoPath)
			list, err = repo.ConflictsDetailed(context.Background())
		}
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

// svnConflictList lists conflicted paths from svn status — text- and
// prop-conflicts both count; svn has no index stages, so the stage
// flags just reflect what svn knows (base + both sides exist).
func svnConflictList(b *vcs.Backend) ([]git.ConflictEntry, error) {
	items, err := b.SVN.Status(context.Background(), false)
	if err != nil {
		return nil, err
	}
	var out []git.ConflictEntry
	for _, it := range items {
		if it.TextStatus != 'C' && it.PropStatus != 'C' && !it.TreeConflict {
			continue
		}
		kind := "冲突"
		if it.TreeConflict {
			kind = "树冲突"
		}
		out = append(out, git.ConflictEntry{
			Path:    it.Path,
			HasBase: true, HasOurs: true, HasTheir: true,
			Kind: kind,
		})
	}
	return out, nil
}

// resolveOne takes ours/theirs for a single path, then reloads so the
// row disappears once marked resolved.
func (cm *conflictsModel) resolveOne(repoPath, path string, how git.ConflictResolution, w updater, after func()) {
	if cm.busy {
		return
	}
	cm.busy = true
	gen := cm.gen
	backend := cm.backend
	go func() {
		var err error
		var list []git.ConflictEntry
		if backend != nil && backend.Kind == vcs.KindSVN {
			err = svnResolve(backend, repoPath, path, how, nil)
			list, _ = svnConflictList(backend)
		} else {
			repo := git.NewRepo(repoPath)
			err = repo.ResolveConflict(context.Background(), path, how, nil)
			list, _ = repo.ConflictsDetailed(context.Background())
		}
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
	backend := cm.backend
	go func() {
		var err error
		var list []git.ConflictEntry
		if backend != nil && backend.Kind == vcs.KindSVN {
			// svn has no "resolve all" — run each path's resolve in turn.
			var items []svn.Item
			items, err = backend.SVN.Status(context.Background(), false)
			if err == nil {
				acc := svn.AcceptMineFull
				if how == git.ResolveTheirs {
					acc = svn.AcceptTheirsFull
				}
				for _, it := range items {
					if it.TextStatus != 'C' && it.PropStatus != 'C' && !it.TreeConflict {
						continue
					}
					if _, rerr := backend.SVN.Resolve(context.Background(), it.Path, acc); rerr != nil {
						err = rerr
						break
					}
				}
			}
			list, _ = svnConflictList(backend)
		} else {
			repo := git.NewRepo(repoPath)
			if how == git.ResolveTheirs {
				err = repo.ResolveAllTheirs(context.Background())
			} else {
				err = repo.ResolveAllOurs(context.Background())
			}
			list, _ = repo.ConflictsDetailed(context.Background())
		}
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
