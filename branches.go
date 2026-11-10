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
	go func() {
		repo := git.NewRepo(repoPath)
		local, remote, err := repo.Branches(context.Background())
		tags, terr := repo.Tags(context.Background())
		subs, _ := repo.Submodules(context.Background())
		wts, _ := repo.Worktrees(context.Background())
		w.update(func() {
			if gen != b.gen {
				return
			}
			b.loading = false
			if err != nil {
				b.err = err.Error()
				return
			}
			if terr != nil {
				b.err = terr.Error()
				return
			}
			b.local, b.remote, b.tags, b.submodules, b.worktrees = local, remote, tags, subs, wts
		})
	}()
}

// op runs one branch mutation then reloads.
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
		local, remote, _ := repo.Branches(context.Background())
		tags, _ := repo.Tags(context.Background())
		subs, _ := repo.Submodules(context.Background())
		wts, _ := repo.Worktrees(context.Background())
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
			b.local, b.remote, b.tags, b.submodules, b.worktrees = local, remote, tags, subs, wts
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
