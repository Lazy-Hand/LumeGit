package git

import (
	"context"
	"fmt"
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

// UndoHeadCommit reverses the HEAD commit only (git reset --soft
// HEAD~1) — IDEA's "Undo Commit": the changes stay in the worktree,
// the commit message is preloaded for the next commit.
func (r *Repo) UndoHeadCommit(ctx context.Context) (*RunResult, error) {
	return r.Run(ctx, "reset", "--soft", "HEAD~1")
}

// AmendMessage rewrites the HEAD commit's message (git commit --amend
// -m). For a non-HEAD commit the caller routes through the rebase plan
// (reword) instead — this is the fast path for the top commit only.
func (r *Repo) AmendMessage(ctx context.Context, msg string) (*RunResult, error) {
	return r.Run(ctx, "commit", "--amend", "-m", msg)
}

// PushUpTo pushes the current branch's history up to (and including)
// oid — IDEA's "Push all commits up to here". It maps to
// `git push <remote> <oid>:refs/heads/<branch>`.
func (r *Repo) PushUpTo(ctx context.Context, oid, remote string) (*RunResult, error) {
	if remote == "" {
		remote = "origin"
	}
	head, err := r.headName(ctx)
	if err != nil || head == "" {
		return &RunResult{}, fmt.Errorf("分离 HEAD 不能推送分支")
	}
	return r.Run(ctx, "push", remote, oid+":refs/heads/"+head)
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
