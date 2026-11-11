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

// RefsAll is one git for-each-ref call returning every local branch,
// remote-tracking branch, and tag — IDEA reads refs through the same
// single shot instead of separate branch/tag commands. The four-tuple
// result lets callers grab all three ref kinds in one parse.
func (r *Repo) RefsAll(ctx context.Context) (local, remote []Branch, tags []Tag, err error) {
	// %(HEAD) is "*" on the checked-out branch — for-each-ref doesn't
	// have it, but %(symref) does nothing for detached HEAD anyway.
	// We still need to know the current branch so the UI can star it:
	// detect it via `symbolic-ref HEAD` (cheap, single call).
	//
	// Format per record:  shortname US refname US oid US objtype US
	// upstream US track US trackshort US subject RS
	//
	// %(upstream:track) gives "[ahead N, behind M]" in one shot — the
	// exact counts the branch popup shows — while trackshort alone only
	// has glyphs. One for-each-ref beats per-branch rev-list calls.
	res, err := r.Run(ctx, "for-each-ref",
		"--format=%(refname:short)%1f%(refname)%1f%(objectname:short)%1f%(objecttype)%1f%(upstream:short)%1f%(upstream:track)%1f%(upstream:trackshort)%1f%(subject)%1e",
		"refs/heads", "refs/remotes", "refs/tags")
	if err != nil {
		return nil, nil, nil, err
	}
	if !res.ExitOK() {
		return nil, nil, nil, &GitError{Args: []string{"for-each-ref"}, Stderr: res.OutputOfStderr()}
	}
	current, _ := r.headName(ctx) // detached HEAD → ""
	for _, rec := range strings.Split(res.Output(), "\x1e") {
		rec = strings.TrimSpace(rec)
		if rec == "" {
			continue
		}
		f := strings.Split(rec, "\x1f")
		if len(f) < 8 {
			continue
		}
		short, refname, oid, objtype := f[0], f[1], f[2], f[3]
		upstream, track, trackshort := f[4], f[5], f[6]
		switch {
		case strings.HasPrefix(refname, "refs/heads/"):
			b := Branch{Name: short, OID: oid, Current: short == current}
			if upstream != "" {
				b.Upstream = upstream
				b.HasTrack = true
				b.Ahead, b.Behind = parseTrackCounts(track)
				if b.Ahead == 0 && b.Behind == 0 {
					b.Ahead, b.Behind = aheadBehindSymbols(trackshort)
				}
			}
			local = append(local, b)
		case strings.HasPrefix(refname, "refs/remotes/"):
			if strings.HasSuffix(short, "/HEAD") {
				continue
			}
			remote = append(remote, Branch{Name: short, OID: oid, Remote: true})
		case strings.HasPrefix(refname, "refs/tags/"):
			t := Tag{Name: short, OID: oid}
			if objtype == "tag" {
				t.Annotated = true
				t.Message = f[7]
			}
			tags = append(tags, t)
		}
	}
	return local, remote, tags, nil
}

// Branches lists local and remote branches. Kept for callers that don't
// want tags; internally it uses RefsAll so there's still one git call.
func (r *Repo) Branches(ctx context.Context) (local []Branch, remote []Branch, err error) {
	local, remote, _, err = r.RefsAll(ctx)
	return local, remote, err
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

// parseTrackCounts reads "[ahead N, behind M]" from %(upstream:track).
// Empty/other strings return 0,0 so the caller can fall back to
// trackshort's glyphs.
func parseTrackCounts(track string) (ahead, behind int) {
	// Expected forms: "", "[ahead 2]", "[behind 1]", "[ahead 2, behind 3]",
	// "[gone]".
	if !strings.HasPrefix(track, "[") {
		return 0, 0
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(track, "["), "]")
	for _, part := range strings.Split(inner, ", ") {
		var n int
		if _, err := fmt.Sscanf(part, "ahead %d", &n); err == nil {
			ahead = n
			continue
		}
		if _, err := fmt.Sscanf(part, "behind %d", &n); err == nil {
			behind = n
		}
	}
	return ahead, behind
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
