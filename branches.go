package main

import (
	"context"

	"lumegit/internal/git"
)

// branchModel holds the Branches tab: local/remote lists and the
// selected branch's actions. Same async pattern as the other tabs.
type branchModel struct {
	local      []git.Branch
	remote     []git.Branch
	tags       []git.Tag
	submodules []git.Submodule
	worktrees  []git.Worktree
	loading    bool
	err        string
	selected   string // branch name
	isRemote   bool
	selTag     string // selected tag name (when selected == selTag and isTag)
	isTag      bool
	busy       bool
	syncOK     string
	lastLoaded string

	// New-branch form.
	newOpen bool
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

func (b *branchModel) ensureLoaded(repoPath string, w updater) {
	if repoPath == "" || (b.lastLoaded == repoPath && !b.loading) {
		return
	}
	b.load(repoPath, w)
}

func (b *branchModel) load(repoPath string, w updater) {
	b.gen++
	gen := b.gen
	b.lastLoaded = repoPath
	b.loading = true
	b.err = ""
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
	b.selected = ""
	b.isTag = false
	b.selTag = ""
	b.lastLoaded = ""
	b.err = ""
	b.gen++
}
