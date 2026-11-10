package git

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Upstream reports the current branch's remote tracking state.
type Upstream struct {
	HasUpstream bool
	Name        string // e.g. "origin/main"
	Ahead       int    // commits local is ahead (unpushed)
	Behind      int    // commits remote is ahead (unfetched)
}

// SyncResult is the outcome of one remote operation, safe to display.
type SyncResult struct {
	Op     string // "fetch" | "pull" | "push"
	Output string // combined stdout+stderr for the log
	Err    error
}

// Fetch updates remote-tracking refs without touching the worktree.
func (r *Repo) Fetch(ctx context.Context) *SyncResult {
	res, err := r.Run(ctx, "fetch", "--all", "--prune", "--verbose")
	return &SyncResult{Op: "fetch", Output: combined(res), Err: exitErr(res, err)}
}

// PullRequest chooses the update strategy.
type PullRequest struct {
	// Rebase uses `git pull --rebase` instead of merge.
	Rebase bool
	// Autostash passes --autostash: dirty worktree is stashed before the
	// pull and popped after (IDEA's "clean working tree before update"
	// option).
	Autostash bool
}

// Pull fetches and integrates the upstream branch.
func (r *Repo) Pull(ctx context.Context, req PullRequest) *SyncResult {
	args := []string{"pull"}
	if req.Rebase {
		args = append(args, "--rebase")
	}
	if req.Autostash {
		args = append(args, "--autostash")
	}
	args = append(args, "--verbose")
	res, err := r.Run(ctx, args...)
	return &SyncResult{Op: "pull", Output: combined(res), Err: exitErr(res, err)}
}

// PushRequest controls one push.
type PushRequest struct {
	// SetUpstream pushes with -u origin <branch> when no upstream exists.
	SetUpstream bool
	// ForceWithLease uses --force-with-lease (safer force push).
	ForceWithLease bool
	// Remote overrides the default remote ("origin").
	Remote string
	// Tags pushes all local tags along with the ref (git push --tags).
	Tags bool
}

// Push publishes the current branch. When the branch has no upstream,
// it pushes with -u so future ahead/behind tracking works.
func (r *Repo) Push(ctx context.Context, req PushRequest) *SyncResult {
	args := []string{"push"}
	remote := req.Remote
	if remote == "" {
		remote = "origin"
	}
	up, uerr := r.Upstream(ctx)
	switch {
	case req.SetUpstream || (uerr == nil && !up.HasUpstream):
		// No upstream yet: -u sets it and pushes the named branch.
		head, herr := r.headName(ctx)
		if herr != nil || head == "" {
			return &SyncResult{Op: "push", Err: fmt.Errorf("无法确定当前分支名（分离 HEAD 不能推送）")}
		}
		args = append(args, "-u", remote, head)
	default:
		args = append(args, remote, "HEAD")
	}
	if req.ForceWithLease {
		args = append(args, "--force-with-lease")
	}
	args = append(args, "--verbose")
	if req.Tags {
		args = append(args, "--tags")
	}
	res, err := r.Run(ctx, args...)
	return &SyncResult{Op: "push", Output: combined(res), Err: exitErr(res, err)}
}

// UnpushedCommits lists commits the push would send: @{u}..HEAD when
// an upstream exists, else the whole branch history (bounded).
func (r *Repo) UnpushedCommits(ctx context.Context, maxCount int) ([]Commit, error) {
	up, err := r.Upstream(ctx)
	if err != nil {
		return nil, err
	}
	if maxCount <= 0 {
		maxCount = 50
	}
	var res *RunResult
	if up.HasUpstream {
		res, err = r.Run(ctx, "log", "--format=%h%x1f%s%x1f%an%x1e",
			"-n", fmt.Sprintf("%d", maxCount), "@{upstream}..HEAD")
	} else {
		head, herr := r.headName(ctx)
		if herr != nil || head == "" {
			return nil, nil // detached HEAD: dialog shows a warning instead
		}
		res, err = r.Run(ctx, "log", "--format=%h%x1f%s%x1f%an%x1e",
			"-n", fmt.Sprintf("%d", maxCount), head)
	}
	if err != nil {
		return nil, err
	}
	if !res.ExitOK() {
		return nil, &GitError{Args: []string{"log"}, Stderr: res.OutputOfStderr()}
	}
	var out []Commit
	for _, rec := range strings.Split(res.Output(), "\x1e") {
		rec = strings.TrimSpace(rec)
		if rec == "" {
			continue
		}
		f := strings.Split(rec, "\x1f")
		if len(f) < 2 {
			continue
		}
		cm := Commit{ShortOID: f[0], Subject: f[1]}
		if len(f) > 2 {
			cm.Author = f[2]
		}
		out = append(out, cm)
	}
	return out, nil
}

// UnpushedSet returns the full OIDs of commits ahead of upstream —
// IDEA highlights these in the log.
func (r *Repo) UnpushedSet(ctx context.Context) (map[string]bool, error) {
	up, err := r.Upstream(ctx)
	if err != nil || !up.HasUpstream {
		return map[string]bool{}, err
	}
	res, err := r.Run(ctx, "log", "--format=%H", "@{upstream}..HEAD")
	if err != nil {
		return nil, err
	}
	if !res.ExitOK() {
		return nil, &GitError{Args: []string{"log", "@{upstream}..HEAD"}, Stderr: res.OutputOfStderr()}
	}
	out := map[string]bool{}
	for _, ln := range strings.Split(res.Output(), "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			out[ln] = true
		}
	}
	return out, nil
}

// Upstream resolves @{upstream}'s name and ahead/behind counts. A branch
// with no upstream returns HasUpstream=false, not an error.
func (r *Repo) Upstream(ctx context.Context) (Upstream, error) {
	name, err := r.Run(ctx, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	if err != nil {
		return Upstream{}, err
	}
	if !name.ExitOK() {
		return Upstream{HasUpstream: false}, nil
	}
	up := Upstream{HasUpstream: true, Name: strings.TrimSpace(name.Output())}

	res, err := r.Run(ctx, "rev-list", "--left-right", "--count", "HEAD...@{upstream}")
	if err != nil {
		return up, err
	}
	if !res.ExitOK() {
		return up, nil // counts unavailable; name still known
	}
	fields := strings.Fields(res.Output())
	if len(fields) >= 2 {
		up.Ahead, _ = strconv.Atoi(fields[0])
		up.Behind, _ = strconv.Atoi(fields[1])
	}
	return up, nil
}

// headName returns the current branch name, or "" on detached HEAD.
func (r *Repo) headName(ctx context.Context) (string, error) {
	res, err := r.Run(ctx, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(res.Output())
	if name == "HEAD" {
		return "", nil
	}
	return name, nil
}

// combined merges stdout+stderr for the operation log.
func combined(res *RunResult) string {
	if res == nil {
		return ""
	}
	var b strings.Builder
	if s := strings.TrimSpace(res.OutputOfStderr()); s != "" {
		b.WriteString(s)
	}
	if s := strings.TrimSpace(res.Output()); s != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(s)
	}
	return b.String()
}

// exitErr converts a non-zero exit into a GitError; nil on success.
func exitErr(res *RunResult, err error) error {
	if err != nil {
		return err
	}
	if res == nil {
		return fmt.Errorf("git did not run")
	}
	if !res.ExitOK() {
		return &GitError{Args: res.Args, Stderr: strings.TrimSpace(res.OutputOfStderr())}
	}
	return nil
}
