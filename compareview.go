package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"lumegit/internal/git"
	"lumegit/internal/svn"
	"lumegit/internal/vcs"
)

// compareModel backs the "与分支比较" overlay. Two modes share the
// same shape IDEA uses: single-ref (ref↔HEAD changed files, "Compare
// with Current") and two-ref (branch↔branch unique commits + diff,
// "Compare with 'xxx'"). On an svn repo the refs are revs ("r<N>" or a
// number) and dirs (trunk/branches/…) instead of git refs.
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

	// backend: svn WC when the open repo is svn.
	backend *vcs.Backend
}

// setBackend records which VCS this overlay compares; called on repo open.
func (cm *compareModel) setBackend(b *vcs.Backend) { cm.backend = b }

func (cm *compareModel) isSVN() bool {
	return cm.backend != nil && cm.backend.Kind == vcs.KindSVN
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
	backend := cm.backend
	go func() {
		var cmp *git.BranchCompare
		var err error
		if backend != nil && backend.Kind == vcs.KindSVN {
			cmp, err = cm.svnCompareRefs(a, b)
		} else {
			repo := git.NewRepo(repoPath)
			cmp, err = repo.CompareBranches(context.Background(), a, b)
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
	backend := cm.backend
	go func() {
		if backend != nil && backend.Kind == vcs.KindSVN {
			cm.svnLoad(ref, path, worktree, w, gen)
			return
		}
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
	backend := cm.backend
	if cm.worktreeMode {
		ref := cm.ref
		go func() {
			var d *git.DiffInfo
			var err error
			if backend != nil && backend.Kind == vcs.KindSVN {
				d, err = cm.svnFileDiffRevWorktree(ref, path)
			} else {
				repo := git.NewRepo(repoPath)
				d, err = repo.CompareFileDiff(context.Background(), ref, path)
			}
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
	// A renamed row's old side lives under OrigPath — the from-ref blob
	// lookup must use it or a rename reads as "new file".
	origPath := ""
	for i := range cm.files {
		if cm.files[i].Path == path {
			origPath = cm.files[i].OrigPath
			break
		}
	}
	go func() {
		var d *git.DiffInfo
		var err error
		if backend != nil && backend.Kind == vcs.KindSVN {
			d, err = cm.svnFileDiffRefs(from, to, path)
		} else {
			repo := git.NewRepo(repoPath)
			d, err = repo.CompareCommitsDiff(context.Background(), from, to, path, origPath)
		}
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

// --- svn backend -------------------------------------------------------
//
// svn refs are "r<N>" (or a bare number) for revisions; a "/" name
// (trunk, branches/foo) is a repo-relative directory. Revision diffs
// come from cat at N vs M, dir diffs from `svn diff --summarize`.

// svnCompareRefs produces the two-ref result for svn — svn has no
// merge-base/unique-commit concept, so only the file list is filled:
// `svn diff --summarize` between the two refs (URLs for dirs, revs
// for rN) gives the changed file list.
func (cm *compareModel) svnCompareRefs(a, b string) (*git.BranchCompare, error) {
	files, err := cm.svnChangedFiles(a, b, "")
	if err != nil {
		return nil, err
	}
	return &git.BranchCompare{
		Files:     files,
		FilesFrom: a,
		FilesTo:   b,
	}, nil
}

// svnChangedFiles lists paths that differ between two svn refs. ref
// "r<N>" diffs WC revs via `svn diff -r N:M`; a dir name diffs two repo
// URLs via `svn diff --summarize <root>/<a> <root>/<b>`.
func (cm *compareModel) svnChangedFiles(from, to, scope string) ([]git.CommitFile, error) {
	wc := cm.backend.SVN
	ra, raIsRev := svnRefRev(from)
	rb, rbIsRev := svnRefRev(to)
	var args []string
	switch {
	case raIsRev && rbIsRev:
		args = []string{"diff", "--summarize", "-r",
			strconv.FormatInt(ra, 10) + ":" + strconv.FormatInt(rb, 10)}
		if scope != "" {
			args = append(args, svn.Peg(scope))
		}
	default:
		// One side is a dir or mixed — resolve each to a URL@rev form.
		ua, err := cm.svnRefURL(from)
		if err != nil {
			return nil, err
		}
		ub, err := cm.svnRefURL(to)
		if err != nil {
			return nil, err
		}
		args = []string{"diff", "--summarize", ua, ub}
	}
	res, err := wc.Run(context.Background(), args...)
	if err != nil {
		return nil, err
	}
	if !res.ExitOK() {
		return nil, &vcsErr{args: args, stderr: strings.TrimSpace(res.OutputOfStderr())}
	}
	return parseSummarize(string(res.Stdout)), nil
}

// parseSummarize decodes `svn diff --summarize` rows: "<A|M|D>  path".
func parseSummarize(out string) []git.CommitFile {
	var files []git.CommitFile
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if len(line) < 3 {
			continue
		}
		status := byte('M')
		switch line[0] {
		case 'A':
			status = 'A'
		case 'D':
			status = 'D'
		case 'M':
			status = 'M'
		default:
			continue
		}
		path := strings.TrimLeft(line[1:], " ")
		// svn prints URL forms for remote diffs — keep just the tail
		// the UI can resolve against the WC.
		files = append(files, git.CommitFile{Path: path, Status: status})
	}
	return files
}

// svnRefRev parses "r123" or "123" into a revision number.
func svnRefRev(ref string) (int64, bool) {
	s := strings.TrimPrefix(ref, "r")
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil
}

// svnRefURL resolves an svn ref to a repo URL for `svn diff` when the
// ref isn't a plain rev — a dir name maps to <root>/<name>, and a bare
// number maps to the WC's URL @ that rev.
func (cm *compareModel) svnRefURL(ref string) (string, error) {
	if n, ok := svnRefRev(ref); ok {
		info, err := cm.backend.SVN.Info(context.Background())
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s@%d", strings.TrimSuffix(info.URL, "/"), n), nil
	}
	info, err := cm.backend.SVN.Info(context.Background())
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(info.Root, "/") + "/" + strings.TrimPrefix(ref, "/"), nil
}

// svnLoad fills the file list for svn single/worktree modes.
func (cm *compareModel) svnLoad(ref, path string, worktree bool, w updater, gen int) {
	wc := cm.backend.SVN
	var files []git.CommitFile
	var err error
	if worktree {
		// ref↔worktree: `svn diff --summarize -r N` (against BASE/wc).
		if rev, ok := svnRefRev(ref); ok {
			args := []string{"diff", "--summarize", "-r", strconv.FormatInt(rev, 10)}
			if path != "" {
				args = append(args, svn.Peg(path))
			}
			r, rerr := wc.Run(context.Background(), args...)
			if rerr != nil {
				err = rerr
			} else if !r.ExitOK() {
				err = &vcsErr{args: args, stderr: strings.TrimSpace(r.OutputOfStderr())}
			} else {
				files = parseSummarize(string(r.Stdout))
			}
		} else {
			err = fmt.Errorf("svn 仅支持与修订或工作区比较")
		}
	} else {
		// Single ref vs the WC's current HEAD-rev state.
		files, err = cm.svnChangedFiles(ref, "HEAD", path)
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
		cm.files = files
	})
}

// svnFileDiffRevWorktree diffs one file at ref vs the worktree file.
func (cm *compareModel) svnFileDiffRevWorktree(ref, path string) (*git.DiffInfo, error) {
	wc := cm.backend.SVN
	rev, ok := svnRefRev(ref)
	if !ok {
		return nil, fmt.Errorf("svn 暂不支持目录与工作区的文件比较")
	}
	dt, err := wc.FileDiffRev(context.Background(), path, rev, -1) // -1 = worktree
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

// svnFileDiffRefs diffs one file between two refs — revs cat at each
// rev; a dir ref cats at that URL. Both sides go through Cat/URL cats.
func (cm *compareModel) svnFileDiffRefs(from, to, path string) (*git.DiffInfo, error) {
	wc := cm.backend.SVN
	oldT, newT, err := "", "", error(nil)
	ra, aIsRev := svnRefRev(from)
	rb, bIsRev := svnRefRev(to)
	if aIsRev && bIsRev {
		oldT, err = wc.Cat(context.Background(), path, ra)
		if err == nil {
			newT, err = wc.Cat(context.Background(), path, rb)
		}
	} else {
		// Mixed/dir — cat each side at its URL.
		var ua, ub string
		ua, err = cm.svnRefURL(from)
		if err == nil {
			ub, err = cm.svnRefURL(to)
		}
		if err == nil {
			oldT, err = wc.CatURL(context.Background(), ua+"/"+path)
		}
		if err == nil {
			newT, err = wc.CatURL(context.Background(), ub+"/"+path)
		}
	}
	if err != nil {
		return nil, err
	}
	return &git.DiffInfo{
		Path:     path,
		Old:      []byte(oldT),
		New:      []byte(newT),
		OldLabel: from,
		NewLabel: to,
		Binary:   looksBinary(oldT) || looksBinary(newT),
	}, nil
}

// vcsErr mirrors git.GitError for the svn-side failures above — the
// pane only reads .Error(), so a small local type keeps imports light.
type vcsErr struct {
	args   []string
	stderr string
}

func (e *vcsErr) Error() string {
	if e.stderr == "" {
		return "svn " + strings.Join(e.args, " ")
	}
	return e.stderr
}
