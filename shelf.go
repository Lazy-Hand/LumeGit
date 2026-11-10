package main

import (
	"context"

	"lumegit/internal/git"
)

// shelfModel holds the Stash/Shelf tab's state: stash list, selected
// entry's files, and the selected file's diff. Same async pattern as
// historyModel — UI thread owns fields, workers publish via updater.
type shelfModel struct {
	stashes    []git.Stash
	shelf      []git.ShelfEntry // patch-based shelf (IDEA's true Shelf)
	loading    bool
	err        string
	selectedID string // stash name, e.g. "stash@{0}", or "shelf:name"
	isShelf    bool   // selected row is a shelf patch, not a stash

	files        []git.StashFile
	filesLoading bool
	filesErr     string
	selectedFile string

	diff        *git.DiffInfo
	diffLoading bool
	diffErr     string

	// New-stash form.
	msg        string
	untracked  bool
	keepIndex  bool // --keep-index
	busy       bool
	lastLoaded string
	gen        int
	syncOK     string // transient success note
}

// ensureLoaded triggers the first load for this repo.
func (s *shelfModel) ensureLoaded(repoPath string, w updater) {
	if repoPath == "" || (s.lastLoaded == repoPath && !s.loading) {
		return
	}
	s.load(repoPath, w)
}

// load fetches the stash list and the patch shelf — the two queries run
// concurrently so total time is max(stash, shelf), not their sum.
func (s *shelfModel) load(repoPath string, w updater) {
	s.gen++
	gen := s.gen
	s.lastLoaded = repoPath
	s.loading = true
	s.err = ""
	go func() {
		repo := git.NewRepo(repoPath)
		type stashResult struct {
			stashes []git.Stash
			err     error
		}
		stashCh := make(chan stashResult, 1)
		shelfCh := make(chan []git.ShelfEntry, 1)
		go func() {
			stashes, err := repo.Stashes(context.Background())
			stashCh <- stashResult{stashes, err}
		}()
		go func() {
			shelfEntries, _ := repo.Shelf(context.Background())
			shelfCh <- shelfEntries
		}()
		sr := <-stashCh
		shelfEntries := <-shelfCh
		w.update(func() {
			if gen != s.gen {
				return
			}
			s.loading = false
			if sr.err != nil {
				s.err = sr.err.Error()
				return
			}
			s.stashes = sr.stashes
			s.shelf = shelfEntries
			// Keep selection valid; else pick the newest.
			found := false
			for _, st := range sr.stashes {
				if st.Name == s.selectedID {
					found = true
					break
				}
			}
			if !found && len(sr.stashes) > 0 {
				s.selectStash(repoPath, sr.stashes[0].Name, w)
			}
			if len(sr.stashes) == 0 && len(shelfEntries) == 0 {
				s.selectedID = ""
				s.files = nil
				s.diff = nil
			}
		})
	}()
}

// selectShelfEntry picks a patch-shelf entry and lists its files.
func (s *shelfModel) selectShelfEntry(repoPath, name string, w updater) {
	s.selectedID = "shelf:" + name
	s.isShelf = true
	s.files = nil
	s.filesErr = ""
	s.filesLoading = true
	s.selectedFile = ""
	s.diff = nil
	gen := s.gen
	go func() {
		repo := git.NewRepo(repoPath)
		paths, err := repo.ShelfPatchFiles(context.Background(), name)
		w.update(func() {
			if gen != s.gen || s.selectedID != "shelf:"+name {
				return
			}
			s.filesLoading = false
			if err != nil {
				s.filesErr = err.Error()
				return
			}
			s.files = nil
			for _, p := range paths {
				s.files = append(s.files, git.StashFile{Path: p, Status: 'M'})
			}
			if len(s.files) > 0 {
				s.selectShelfFile(repoPath, name, s.files[0], w)
			}
		})
	}()
}

// selectShelfFile shows a patch file's content as the diff.
func (s *shelfModel) selectShelfFile(repoPath, name string, f git.StashFile, w updater) {
	s.selectedFile = f.Path
	s.diff = nil
	s.diffErr = ""
	s.diffLoading = true
	gen := s.gen
	go func() {
		repo := git.NewRepo(repoPath)
		patch, err := repo.ShelfPatchText(context.Background(), name)
		w.update(func() {
			if gen != s.gen || s.selectedFile != f.Path {
				return
			}
			s.diffLoading = false
			if err != nil {
				s.diffErr = err.Error()
				return
			}
			// Present the raw patch as the "new" side of a pseudo-diff;
			// IDEA shows the patch applied, but the text is the honest
			// artifact stored on the shelf.
			s.diff = &git.DiffInfo{
				Path:     f.Path,
				OldLabel: "shelf:" + name,
				NewLabel: "patch",
				Old:      nil,
				New:      patch,
			}
		})
	}()
}

// selectStash picks an entry and loads its file list.
func (s *shelfModel) selectStash(repoPath, name string, w updater) {
	s.selectedID = name
	s.isShelf = false
	s.files = nil
	s.filesErr = ""
	s.filesLoading = true
	s.selectedFile = ""
	s.diff = nil
	gen := s.gen
	go func() {
		repo := git.NewRepo(repoPath)
		files, err := repo.StashFiles(context.Background(), name)
		w.update(func() {
			if gen != s.gen || s.selectedID != name {
				return
			}
			s.filesLoading = false
			if err != nil {
				s.filesErr = err.Error()
				return
			}
			s.files = files
			if len(files) > 0 {
				s.selectFile(repoPath, name, files[0], w)
			}
		})
	}()
}

// selectFile picks a file and loads its diff.
func (s *shelfModel) selectFile(repoPath, stashName string, f git.StashFile, w updater) {
	s.selectedFile = f.Path
	s.diff = nil
	s.diffErr = ""
	s.diffLoading = true
	gen := s.gen
	go func() {
		repo := git.NewRepo(repoPath)
		d, err := repo.StashFileDiff(context.Background(), stashName, f)
		w.update(func() {
			if gen != s.gen || s.selectedFile != f.Path {
				return
			}
			s.diffLoading = false
			if err != nil {
				s.diffErr = err.Error()
				return
			}
			s.diff = d
		})
	}()
}

// op runs one stash mutation (push/apply/pop/drop) then reloads both
// the stash list and the workspace snapshot (the worktree may have
// changed underneath the commit tab too).
func (s *shelfModel) op(repoPath string, w updater, busyText, doneText string, fn func(*git.Repo) error, after func()) {
	if s.busy {
		return
	}
	s.busy = true
	_ = busyText
	_ = doneText
	s.gen++
	gen := s.gen
	go func() {
		repo := git.NewRepo(repoPath)
		err := fn(repo)
		stashes, _ := repo.Stashes(context.Background())
		shelfEntries, _ := repo.Shelf(context.Background())
		w.update(func() {
			if gen != s.gen {
				return
			}
			s.busy = false
			if err != nil {
				s.err = err.Error()
				s.syncOK = ""
				return
			}
			s.err = ""
			s.syncOK = doneText
			s.stashes = stashes
			s.shelf = shelfEntries
			if after != nil {
				after()
			}
		})
	}()
}

// reset clears state on repo switch.
func (s *shelfModel) reset() {
	s.stashes = nil
	s.shelf = nil
	s.selectedID = ""
	s.isShelf = false
	s.files = nil
	s.selectedFile = ""
	s.diff = nil
	s.err = ""
	s.lastLoaded = ""
	s.gen++
}
