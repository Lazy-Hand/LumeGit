package git

import (
	"context"
	"fmt"
	"strings"
)

// Worktree is one entry from `git worktree list --porcelain`. The main
// checkout is row 0; linked worktrees let the user keep several
// branches checked out side by side (IDEA: Git → Manage Worktrees).
type Worktree struct {
	Path     string // absolute path of the worktree root
	OID      string // HEAD commit (may be short or full)
	Branch   string // attached branch ("main"); empty when detached
	Detached bool
	Bare     bool
	IsMain   bool // the primary checkout
}

// Worktrees lists every registered worktree.
func (r *Repo) Worktrees(ctx context.Context) ([]Worktree, error) {
	res, err := r.Run(ctx, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	if !res.ExitOK() {
		return nil, &GitError{Args: []string{"worktree", "list"}, Stderr: res.OutputOfStderr()}
	}
	var out []Worktree
	var cur *Worktree
	flush := func() {
		if cur != nil {
			out = append(out, *cur)
			cur = nil
		}
	}
	for _, ln := range strings.Split(res.Output(), "\n") {
		if ln == "" {
			flush()
			continue
		}
		if strings.HasPrefix(ln, "worktree ") {
			flush()
			cur = &Worktree{Path: strings.TrimSpace(ln[len("worktree "):])}
			continue
		}
		if cur == nil {
			continue
		}
		switch {
		case strings.HasPrefix(ln, "HEAD "):
			cur.OID = strings.TrimSpace(ln[len("HEAD "):])
		case strings.HasPrefix(ln, "branch "):
			cur.Branch = strings.TrimPrefix(strings.TrimSpace(ln[len("branch "):]), "refs/heads/")
		case ln == "detached":
			cur.Detached = true
		case ln == "bare":
			cur.Bare = true
		}
	}
	flush()
	if len(out) > 0 {
		out[0].IsMain = true
	}
	return out, nil
}

// AddWorktree checks out ref at path, optionally on a new branch.
// Equivalent to `git worktree add [-b <newBranch>] <path> [<ref>]`.
func (r *Repo) AddWorktree(ctx context.Context, path, ref, newBranch string) (*RunResult, error) {
	if path == "" {
		return nil, fmt.Errorf("worktree path required")
	}
	args := []string{"worktree", "add"}
	if newBranch != "" {
		args = append(args, "-b", newBranch)
	}
	args = append(args, path)
	if ref != "" {
		args = append(args, ref)
	}
	return r.RunOK(ctx, args...)
}

// RemoveWorktree removes a linked worktree (IDEA's "Remove"). The main
// worktree cannot be removed — callers should hide that action on
// row 0.
func (r *Repo) RemoveWorktree(ctx context.Context, path string, force bool) (*RunResult, error) {
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, path)
	return r.RunOK(ctx, args...)
}

// PruneWorktrees drops stale worktree registrations whose directories
// were deleted outside git (IDEA exposes this under Manage Worktrees).
func (r *Repo) PruneWorktrees(ctx context.Context) (*RunResult, error) {
	return r.RunOK(ctx, "worktree", "prune")
}
