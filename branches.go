package main

import (
	"context"
	"strings"

	"lumegit/internal/git"
	"lumegit/internal/svn"
	"lumegit/internal/vcs"
)

// branchModel holds the Branches tab: local/remote lists and the
// selected branch's actions. Same async pattern as the other tabs.
type branchModel struct {
	// Backend: nil or KindGit → git; KindSVN → svn (branch/tag = dirs).
	backend *vcs.Backend

	// svn state: the WC's repo info (root URL + current URL) and the
	// branch/tag directory names listed from the repo.
	svnInfo     *svn.Info
	svnBranches []string
	svnTags     []string

	local      []git.Branch
	remote     []git.Branch
	tags       []git.Tag
	submodules []git.Submodule
	worktrees  []git.Worktree
	loading    bool
	err        string
	busy       bool
	syncOK     string
	lastLoaded string

	// New-branch name staging (popup menu → confirmBranchDialog kind=2 →
	// createBranch reads it).
	newName string

	// New-tag dialog.
	tagOpen     bool
	tagName     string
	tagTarget   string // empty = HEAD
	tagMessage  string // non-empty = annotated
	tagErr      string
	tagCheckout bool

	// Rename dialog (IDEA: branch right-click → Rename).
	renameOpen bool
	renameOld  string
	renameNew  string

	// Remotes manager (IDEA: Git → Manage Remotes).
	remoteOpen bool
	remoteList []git.Remote
	remoteErr  string
	remoteName string // form: name
	remoteURL  string // form: url
	remoteEdit string // non-empty = editing this remote's URL
	remoteBusy bool

	// New-worktree dialog (IDEA: Manage Worktrees → +).
	wtOpen   bool
	wtPath   string // destination directory
	wtRef    string // source ref, empty = HEAD
	wtBranch string // optional new-branch name
	wtErr    string

	gen int
}

// setBackend records which VCS the branches model serves; called on
// repo open. reset() clears it.
func (b *branchModel) setBackend(be *vcs.Backend) { b.backend = be }

// isSVN reports the open repo is an svn working copy.
func (b *branchModel) isSVN() bool {
	return b.backend != nil && b.backend.Kind == vcs.KindSVN
}

func (b *branchModel) ensureLoaded(repoPath string, w updater) {
	if repoPath == "" || (b.lastLoaded == repoPath && !b.loading) {
		return
	}
	if b.loading && b.lastLoaded == repoPath {
		return // in-flight load — don't restart and fence it
	}
	b.load(repoPath, w)
}

func (b *branchModel) load(repoPath string, w updater) {
	b.gen++
	gen := b.gen
	b.lastLoaded = repoPath
	b.loading = true
	b.err = ""
	if b.isSVN() {
		b.svnLoad(repoPath, w, gen)
		return
	}
	// Run the three queries concurrently — IDEA reads refs in one shot
	// and never blocks the UI on serial fork/exec. The slowest call
	// (usually worktree list on a big repo) bounds total time.
	go func() {
		repo := git.NewRepo(repoPath)
		type refsResult struct {
			local, remote []git.Branch
			tags          []git.Tag
			err           error
		}
		refsCh := make(chan refsResult, 1)
		subsCh := make(chan []git.Submodule, 1)
		wtsCh := make(chan []git.Worktree, 1)
		go func() {
			local, remote, tags, err := repo.RefsAll(context.Background())
			refsCh <- refsResult{local, remote, tags, err}
		}()
		go func() {
			s, _ := repo.Submodules(context.Background())
			subsCh <- s
		}()
		go func() {
			wts, _ := repo.Worktrees(context.Background())
			wtsCh <- wts
		}()
		refs := <-refsCh
		subs := <-subsCh
		wts := <-wtsCh
		w.update(func() {
			if gen != b.gen {
				return
			}
			b.loading = false
			if refs.err != nil {
				b.err = refs.err.Error()
				return
			}
			b.local, b.remote, b.tags, b.submodules, b.worktrees = refs.local, refs.remote, refs.tags, subs, wts
		})
	}()
}

// op runs one branch mutation then reloads. The three re-reads run
// concurrently, so the whole refresh costs max(submodule, worktree,
// for-each-ref) — not their sum.
func (b *branchModel) op(repoPath string, w updater, busyText, doneText string,
	fn func(*git.Repo) (*git.RunResult, error), after func()) {
	if b.busy {
		return
	}
	b.busy = true
	b.gen++
	gen := b.gen
	go func() {
		repo := git.NewRepo(repoPath)
		res, err := fn(repo)
		type refsResult struct {
			local, remote []git.Branch
			tags          []git.Tag
			err           error
		}
		refsCh := make(chan refsResult, 1)
		subsCh := make(chan []git.Submodule, 1)
		wtsCh := make(chan []git.Worktree, 1)
		go func() {
			local, remote, tags, err := repo.RefsAll(context.Background())
			refsCh <- refsResult{local, remote, tags, err}
		}()
		go func() {
			s, _ := repo.Submodules(context.Background())
			subsCh <- s
		}()
		go func() {
			wts, _ := repo.Worktrees(context.Background())
			wtsCh <- wts
		}()
		refs := <-refsCh
		subs := <-subsCh
		wts := <-wtsCh
		w.update(func() {
			if gen != b.gen {
				return
			}
			b.busy = false
			if err != nil {
				b.err = err.Error()
				b.syncOK = ""
				return
			}
			if res != nil && !res.ExitOK() {
				b.err = res.OutputOfStderr()
				b.syncOK = ""
				return
			}
			b.err = ""
			b.syncOK = doneText
			b.local, b.remote, b.tags, b.submodules, b.worktrees = refs.local, refs.remote, refs.tags, subs, wts
			if after != nil {
				after()
			}
		})
	}()
}

// loadRemotes refreshes the remotes list for the manager dialog.
func (b *branchModel) loadRemotes(repoPath string, w updater) {
	b.gen++
	gen := b.gen
	go func() {
		repo := git.NewRepo(repoPath)
		rs, err := repo.Remotes(context.Background())
		w.update(func() {
			if gen != b.gen {
				return
			}
			if err != nil {
				b.remoteErr = err.Error()
				return
			}
			b.remoteList = rs
			b.remoteErr = ""
		})
	}()
}

// remoteOp runs one remote mutation then refreshes the list.
func (b *branchModel) remoteOp(repoPath string, w updater,
	fn func(*git.Repo) (*git.RunResult, error)) {
	if b.remoteBusy {
		return
	}
	b.remoteBusy = true
	b.gen++
	gen := b.gen
	go func() {
		repo := git.NewRepo(repoPath)
		res, err := fn(repo)
		rs, _ := repo.Remotes(context.Background())
		w.update(func() {
			if gen != b.gen {
				return
			}
			b.remoteBusy = false
			if err != nil {
				b.remoteErr = err.Error()
				return
			}
			if res != nil && !res.ExitOK() {
				b.remoteErr = res.OutputOfStderr()
				return
			}
			b.remoteErr = ""
			b.remoteList = rs
		})
	}()
}

func (b *branchModel) reset() {
	b.local, b.remote = nil, nil
	b.tags = nil
	b.svnInfo = nil
	b.svnBranches, b.svnTags = nil, nil
	b.lastLoaded = ""
	b.err = ""
	b.backend = nil
	b.gen++
}

// --- svn backend -------------------------------------------------------

// svnLoad fills the popup from the repository's directory layout:
// svn info tells us root + current URL, svn list reads branches/ and
// tags/ — the two dirs SVN convention uses for branches.
func (b *branchModel) svnLoad(repoPath string, w updater, gen int) {
	wc := b.backend.SVN
	go func() {
		info, err := wc.Info(context.Background())
		var branches, tags []string
		if err == nil {
			// branches/ and tags/ live at the repo root, next to trunk/.
			base := strings.TrimSuffix(info.Root, "/")
			branches, _ = wc.ListURL(context.Background(), base+"/branches", "immediates")
			tags, _ = wc.ListURL(context.Background(), base+"/tags", "immediates")
		}
		w.update(func() {
			if gen != b.gen {
				return
			}
			b.loading = false
			if err != nil {
				b.err = err.Error()
				return
			}
			b.err = ""
			b.svnInfo = info
			b.svnBranches = branches
			b.svnTags = tags
		})
	}()
}

// svnCurrentURL is the WC's repo URL ("…/trunk" or "…/branches/foo").
func (b *branchModel) svnCurrentURL() string {
	if b.svnInfo == nil {
		return ""
	}
	return b.svnInfo.URL
}

// svnRepoRoot is the repository root URL — the parent of trunk/branches.
func (b *branchModel) svnRepoRoot() string {
	if b.svnInfo == nil {
		return ""
	}
	return strings.TrimSuffix(b.svnInfo.Root, "/")
}

// svnSwitchBranch switches the WC to another branch URL — svn's
// checkout. After it completes the workspace's whole state changes,
// so the commit tab refreshes too.
func (b *branchModel) svnSwitch(repoPath, branchURL string, w updater, refresh func()) {
	if b.busy {
		return
	}
	b.busy = true
	b.gen++
	gen := b.gen
	go func() {
		res, err := b.backend.SVN.Switch(context.Background(), branchURL, svn.UpdateOptions{Accept: svn.AcceptPostpone})
		w.update(func() {
			if gen != b.gen {
				return
			}
			b.busy = false
			if err != nil {
				b.err = "切换失败：" + err.Error()
				return
			}
			b.err = ""
			b.syncOK = "已切换"
			_ = res
			b.lastLoaded = "" // reload popup next open
			if refresh != nil {
				refresh()
			}
		})
	}()
}

// svnNewBranch copies the current URL to branches/<name> server-side —
// svn's "create branch". rev > 0 pins the copy source to that revision
// (the history row's "从此修订新建分支"); 0 copies HEAD. It does not
// switch; the user can switch after.
func (b *branchModel) svnNewBranch(name, msg string, rev int64, w updater, done func(err string)) {
	if b.busy || name == "" {
		return
	}
	b.busy = true
	b.gen++
	gen := b.gen
	src := b.svnCurrentURL()
	dst := b.svnRepoRoot() + "/branches/" + name
	go func() {
		res, err := b.backend.SVN.CopyRev(context.Background(), src, dst, msg, rev)
		w.update(func() {
			if gen != b.gen {
				return
			}
			b.busy = false
			if err != nil {
				done("新建分支失败：" + err.Error())
				return
			}
			_ = res
			b.lastLoaded = ""
			done("")
		})
	}()
}
