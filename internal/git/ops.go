package git

import (
	"context"
	"strings"
)

// Rebase rewrites the current branch onto the target (a branch, tag,
// or commit). Conflicts pause the op; the caller drives continue/abort
// through Snapshot.Progress().
func (r *Repo) Rebase(ctx context.Context, onto string) (*RunResult, error) {
	return r.Run(ctx, "rebase", onto)
}

// CherryPick applies one commit's changes onto HEAD.
func (r *Repo) CherryPick(ctx context.Context, oid string) (*RunResult, error) {
	return r.Run(ctx, "cherry-pick", oid)
}

// Revert undoes one commit with a new inverse commit.
func (r *Repo) Revert(ctx context.Context, oid string) (*RunResult, error) {
	return r.Run(ctx, "revert", "--no-edit", oid)
}

// ResetMode picks how far Reset moves each tree.
type ResetMode string

const (
	ResetSoft  ResetMode = "--soft"  // HEAD only; index+worktree keep changes
	ResetMixed ResetMode = "--mixed" // HEAD+index; worktree keeps changes
	ResetHard  ResetMode = "--hard"  // all three; worktree changes discarded
	ResetKeep  ResetMode = "--keep"  // like hard but safe for dirty worktree
)

// Reset moves HEAD to the target; mode picks what happens to index and
// worktree. Hard destroys uncommitted work — the UI must confirm first.
func (r *Repo) Reset(ctx context.Context, target string, mode ResetMode) (*RunResult, error) {
	if mode == "" {
		mode = ResetMixed
	}
	return r.Run(ctx, "reset", string(mode), target)
}

// OpOutput renders a RunResult for the operation log.
func OpOutput(res *RunResult) string {
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
