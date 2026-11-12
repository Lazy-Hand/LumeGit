package main

import (
	"context"
	"fmt"
	"strings"

	"lumegit/internal/git"
	"lumegit/internal/svn"
	"lumegit/internal/vcs"
)

// fileHistModel backs the file-history overlay: one file's commit
// trail and its blame view. Opened from a file row's context menu.
type fileHistModel struct {
	open     bool
	lastOpen bool // previous frame's open — the closing edge bumps gen
	path     string
	repoPath string // retained so the blame tab can load on demand

	// Backend: svn WC when the open repo is svn — history/blame then
	// come from `svn log <path>` / `svn blame`, and the blame sub-tab
	// stays but reads svn's per-line revs.
	backend *vcs.Backend

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

// openFor starts loading the file's history. Blame stays unloaded —
// it costs a full-file annotate most users never open, so it only runs
// when the 溯源 sub-tab is picked (see ensureBlame).
func (f *fileHistModel) openFor(repoPath, path string, w updater) {
	if f.path != path {
		f.selectedID = ""
		f.commits = nil
		f.blame = nil
		f.diff = nil
	}
	f.open = true
	f.path = path
	f.repoPath = repoPath
	f.gen++
	f.loadHistory(repoPath, w)
}

// ensureBlame kicks the blame load the first time the 溯源 tab opens —
// and never again for this file (blame != nil means loaded; the error
// path leaves blameErr set for the pane to show).
func (f *fileHistModel) ensureBlame(repoPath string, w updater) {
	if repoPath == "" || f.blame != nil || f.blameLoading || f.blameErr != "" {
		return
	}
	f.loadBlame(repoPath, w)
}

func (f *fileHistModel) close() { f.open = false }

// setBackend records the VCS for this overlay; called on repo open.
func (f *fileHistModel) setBackend(b *vcs.Backend) { f.backend = b }

// isSVN reports the open repo is an svn working copy.
func (f *fileHistModel) isSVN() bool {
	return f.backend != nil && f.backend.Kind == vcs.KindSVN
}

// loadHistory fetches the file's commit trail.
func (f *fileHistModel) loadHistory(repoPath string, w updater) {
	gen := f.gen
	path := f.path
	f.loading = true
	f.err = ""
	if f.isSVN() {
		f.svnLoadHistory(repoPath, w, gen)
		return
	}
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
	if f.isSVN() {
		f.svnSelectCommit(repoPath, oid, w)
		return
	}
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
	if f.isSVN() {
		f.svnLoadBlame(repoPath, w, gen)
		return
	}
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
	f.repoPath = ""
	f.commits = nil
	f.blame = nil
	f.diff = nil
	f.selectedID = ""
	f.backend = nil
	f.gen++
}

// --- svn backend -------------------------------------------------------

// svnLoadHistory runs `svn log <path>` — the file's own rev trail.
// svn log on a WC path walks the repos for revs touching it, same as
// git log --follow (copy history included by default).
func (f *fileHistModel) svnLoadHistory(repoPath string, w updater, gen int) {
	wc := f.backend.SVN
	path := f.path
	go func() {
		revs, err := wc.Log(context.Background(), svn.LogOptions{
			Limit:  200,
			Target: path,
		})
		if err != nil && svnPathAbsent(err) {
			err = nil // 未纳入版本控制：有磁盘文件但 svn 不认识它
		}
		w.update(func() {
			if gen != f.gen || f.path != path {
				return
			}
			f.loading = false
			if err != nil {
				f.err = err.Error()
				return
			}
			f.commits = make([]git.Commit, 0, len(revs))
			for _, r := range revs {
				f.commits = append(f.commits, svnCommitShape(r))
			}
			if len(f.commits) > 0 && f.selectedID == "" {
				f.selectCommit(repoPath, f.commits[0].OID, w)
			}
		})
	}()
}

// svnSelectCommit diffs the file at rev vs rev-1 — same two-side
// DiffInfo the git path produces.
func (f *fileHistModel) svnSelectCommit(repoPath, oid string, w updater) {
	f.selectedID = oid
	f.diff = nil
	f.diffErr = ""
	f.diffLoading = true
	gen := f.gen
	path := f.path
	rev := svnRevNum(oid)
	wc := f.backend.SVN
	go func() {
		oldT, err := wc.Cat(context.Background(), path, rev-1)
		var newT string
		if err == nil {
			newT, err = wc.Cat(context.Background(), path, rev)
		}
		w.update(func() {
			if gen != f.gen || f.selectedID != oid || f.path != path {
				return
			}
			f.diffLoading = false
			if err != nil {
				f.diffErr = err.Error()
				return
			}
			f.diff = &git.DiffInfo{
				Path:     path,
				Old:      []byte(oldT),
				New:      []byte(newT),
				OldLabel: fmt.Sprintf("r%d", rev-1),
				NewLabel: oid,
				Binary:   looksBinary(oldT) || looksBinary(newT),
			}
		})
	}()
}

// svnLoadBlame reads `svn blame` (BASE-annotated) into git.BlameLine
// rows so the overlay's 溯源 sub-tab renders both backends alike.
func (f *fileHistModel) svnLoadBlame(repoPath string, w updater, gen int) {
	wc := f.backend.SVN
	path := f.path
	go func() {
		lines, err := wc.BlameWithText(context.Background(), path)
		if err != nil && svnPathAbsent(err) {
			err = nil // 未纳入版本控制：没有可溯源的行
		}
		w.update(func() {
			if gen != f.gen || f.path != path {
				return
			}
			f.blameLoading = false
			if err != nil {
				f.blameErr = err.Error()
				return
			}
			out := make([]git.BlameLine, len(lines))
			for i, l := range lines {
				oid := svnRevOID(l.Rev)
				out[i] = git.BlameLine{
					OID:      oid,
					ShortOID: oid,
					Author:   l.Author,
					LineNo:   i + 1,
					Filename: path,
					Text:     l.Text,
				}
			}
			f.blame = out
		})
	}()
}

// svnPathAbsent reports whether err is svn's "the node was not found" —
// E155010 for `svn log`, W155010/E200009 for `svn blame`. On an
// unversioned path these mean "no history to show", not a failure the
// user must read, so callers map them to the empty state.
func svnPathAbsent(err error) bool {
	s := err.Error()
	return strings.Contains(s, "E155010") ||
		strings.Contains(s, "W155010") ||
		strings.Contains(s, "E200009") ||
		strings.Contains(s, "was not found")
}
