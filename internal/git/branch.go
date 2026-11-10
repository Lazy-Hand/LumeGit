package git

import (
	"context"
	"fmt"
	"strings"
)

// Branch is one local or remote ref.
type Branch struct {
	Name     string // short name ("main", "origin/dev")
	Current  bool   // checked out now
	Remote   bool   // refs/remotes/*
	OID      string
	Upstream string // upstream short name, when set
	Ahead    int
	Behind   int
	HasTrack bool // upstream tracking info present
}

// Branches lists local and remote branches.
func (r *Repo) Branches(ctx context.Context) (local []Branch, remote []Branch, err error) {
	// Local: name US upstream US trackshort US HEAD US oid RS.
	// trackshort is locale-free ("<" behind, ">" ahead, "<>" both, "=" sync).
	res, err := r.Run(ctx, "branch", "-vv",
		"--format=%(refname:short)%1f%(upstream:short)%1f%(upstream:trackshort)%1f%(HEAD)%1f%(objectname:short)%1e")
	if err != nil {
		return nil, nil, err
	}
	if !res.ExitOK() {
		return nil, nil, &GitError{Args: []string{"branch", "-vv"}, Stderr: res.OutputOfStderr()}
	}
	for _, rec := range strings.Split(res.Output(), "\x1e") {
		rec = strings.TrimSpace(rec)
		if rec == "" {
			continue
		}
		f := strings.Split(rec, "\x1f")
		if len(f) < 5 {
			continue
		}
		b := Branch{Name: f[0], OID: f[4], Current: f[3] == "*"}
		if f[1] != "" {
			b.Upstream = f[1]
			b.HasTrack = true
			// trackshort is a compact symbol pair, no parsing needed
			// beyond checking for each direction's glyph.
			ts := f[2]
			b.Ahead, b.Behind = aheadBehindSymbols(ts)
		}
		local = append(local, b)
	}

	// Remote tracking refs.
	res2, err := r.Run(ctx, "branch", "-r",
		"--format=%(refname:short)%1f%(objectname:short)%1e")
	if err != nil {
		return local, nil, err
	}
	if res2.ExitOK() {
		for _, rec := range strings.Split(res2.Output(), "\x1e") {
			rec = strings.TrimSpace(rec)
			if rec == "" {
				continue
			}
			f := strings.Split(rec, "\x1f")
			if len(f) < 2 || strings.HasSuffix(f[0], "/HEAD") {
				continue
			}
			remote = append(remote, Branch{Name: f[0], OID: f[1], Remote: true})
		}
	}
	return local, remote, nil
}

// Checkout switches to a branch (creating it first when create is set).
func (r *Repo) Checkout(ctx context.Context, name string, create bool) (*RunResult, error) {
	args := []string{"switch"}
	if create {
		args = append(args, "-c")
	}
	args = append(args, name)
	return r.RunOK(ctx, args...)
}

// DeleteBranch removes a local branch. force maps to -D (unmerged).
func (r *Repo) DeleteBranch(ctx context.Context, name string, force bool) (*RunResult, error) {
	flag := "-d"
	if force {
		flag = "-D"
	}
	return r.RunOK(ctx, "branch", flag, name)
}

// RenameBranch renames a local branch.
func (r *Repo) RenameBranch(ctx context.Context, old, new string) (*RunResult, error) {
	if new == "" {
		return nil, fmt.Errorf("new branch name required")
	}
	return r.RunOK(ctx, "branch", "-m", old, new)
}

// MergeIntoCurrent merges the named branch into HEAD.
func (r *Repo) MergeIntoCurrent(ctx context.Context, name string) (*RunResult, error) {
	return r.RunOK(ctx, "merge", "--no-edit", name)
}

// SetUpstream points the current branch at a remote-tracking ref.
func (r *Repo) SetUpstream(ctx context.Context, upstream string) (*RunResult, error) {
	return r.RunOK(ctx, "branch", "--set-upstream-to", upstream)
}

// CurrentBranch returns the checked-out branch name or "" if detached.
func (r *Repo) CurrentBranch(ctx context.Context) (string, error) {
	return r.headName(ctx)
}

// aheadBehindSymbols turns git's trackshort glyphs into rough counts.
// "=" stays 0/0; we can't get exact numbers from the symbol alone, so
// when precise counts matter callers use Upstream() instead.
func aheadBehindSymbols(ts string) (ahead, behind int) {
	switch {
	case strings.Contains(ts, "<>"):
		return 1, 1 // both — exact counts need rev-list
	case strings.HasPrefix(ts, ">"):
		return 1, 0
	case strings.HasPrefix(ts, "<"):
		return 0, 1
	}
	return 0, 0
}

// BranchAheadBehind gets exact ahead/behind for a local branch vs its
// upstream, using rev-list (trackshort only gives symbols).
func (r *Repo) BranchAheadBehind(ctx context.Context, name string) (ahead, behind int, err error) {
	res, err := r.Run(ctx, "rev-list", "--left-right", "--count", name+"...@{upstream}")
	if err != nil {
		return 0, 0, err
	}
	if !res.ExitOK() {
		return 0, 0, nil // no upstream
	}
	f := strings.Fields(res.Output())
	if len(f) >= 2 {
		fmt.Sscanf(f[0], "%d", &ahead)
		fmt.Sscanf(f[1], "%d", &behind)
	}
	return ahead, behind, nil
}
