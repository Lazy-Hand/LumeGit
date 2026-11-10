package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ConflictFile is one path with unmerged index entries.
type ConflictFile struct {
	Path     string
	HasBase  bool // stage 1 present (false = both added)
	HasOurs  bool // stage 2 present
	HasTheir bool // stage 3 present
}

// ConflictDiff carries one conflicted file's three sides for the
// three-way resolver.
type ConflictDiff struct {
	Path  string
	Base  []byte // common ancestor (stage 1)
	Ours  []byte // current branch (stage 2)
	Their []byte // incoming side (stage 3)
}

// Conflicts returns the files still in conflict from the snapshot.
func (s *Snapshot) ConflictFiles() []FileChange {
	return s.Conflicts()
}

// ConflictSides reads a conflicted file's base/ours/theirs blobs from
// the index's stage entries.
func (r *Repo) ConflictSides(ctx context.Context, path string) (*ConflictDiff, error) {
	d := &ConflictDiff{Path: path}
	var err error
	if d.Base, err = r.stageBlob(ctx, 1, path); err != nil {
		return nil, err
	}
	if d.Ours, err = r.stageBlob(ctx, 2, path); err != nil {
		return nil, err
	}
	if d.Their, err = r.stageBlob(ctx, 3, path); err != nil {
		return nil, err
	}
	return d, nil
}

// stageBlob reads :N:path or nil when that stage is absent.
func (r *Repo) stageBlob(ctx context.Context, stage int, path string) ([]byte, error) {
	res, err := r.Run(ctx, "ls-files", "-u", "-z", "--", path)
	if err != nil {
		return nil, err
	}
	if !res.ExitOK() {
		return nil, &GitError{Args: []string{"ls-files", "-u"}, Stderr: res.OutputOfStderr()}
	}
	// Records look like "100644 <oid> <stage>\tpath" NUL-separated.
	want := fmt.Sprintf("%d", stage)
	for _, rec := range strings.Split(string(res.Stdout), "\x00") {
		if rec == "" {
			continue
		}
		f := strings.Fields(rec)
		if len(f) >= 3 && f[2] == want {
			return r.catFileOID(ctx, f[1])
		}
	}
	return nil, nil
}

// catFileOID reads a blob by object ID.
func (r *Repo) catFileOID(ctx context.Context, oid string) ([]byte, error) {
	res, err := r.Run(ctx, "cat-file", "blob", oid)
	if err != nil {
		return nil, err
	}
	if !res.ExitOK() {
		return nil, &GitError{Args: []string{"cat-file", oid}, Stderr: res.OutputOfStderr()}
	}
	return res.Stdout, nil
}

// ConflictResolution chooses how to settle one file.
type ConflictResolution int

const (
	ResolveOurs   ConflictResolution = iota // take stage 2 (current side)
	ResolveTheirs                           // take stage 3 (incoming)
	ResolveMerge                            // user's merged text (write to worktree)
)

// ResolveConflict applies one side (or a merged text) to the file and
// marks it resolved in the index. For ResolveMerge the caller supplies
// merged content which we write to the worktree before `git add`.
func (r *Repo) ResolveConflict(ctx context.Context, path string, how ConflictResolution, merged []byte) error {
	switch how {
	case ResolveOurs, ResolveTheirs:
		flag := "--ours"
		if how == ResolveTheirs {
			flag = "--theirs"
		}
		res, err := r.RunOK(ctx, "checkout", flag, "--", path)
		if err != nil {
			return err
		}
		if !res.ExitOK() {
			return &GitError{Args: res.Args, Stderr: res.OutputOfStderr()}
		}
	case ResolveMerge:
		full := filepath.Join(r.Worktree, path)
		if err := os.WriteFile(full, merged, 0o644); err != nil {
			return fmt.Errorf("write merged file: %w", err)
		}
	}
	// Mark resolved.
	res, err := r.RunOK(ctx, "add", "--", path)
	if err != nil {
		return err
	}
	if !res.ExitOK() {
		return &GitError{Args: res.Args, Stderr: res.OutputOfStderr()}
	}
	return nil
}

// ConflictProgress reports the in-progress op's continue/abort verbs.
type ConflictProgress struct {
	Kind         string // "merge" | "rebase" | "cherry-pick" | "revert"
	CanContinue  bool   // all conflicts resolved
	ContinueArgs []string
	AbortArgs    []string
}

// Progress returns how to advance or abandon the in-progress op.
func (s *Snapshot) Progress() ConflictProgress {
	kind := ""
	switch {
	case strings.Contains(s.InProgress, "merge"):
		kind = "merge"
	case strings.Contains(s.InProgress, "rebase"):
		kind = "rebase"
	case strings.Contains(s.InProgress, "cherry"):
		kind = "cherry-pick"
	case strings.Contains(s.InProgress, "revert"):
		kind = "revert"
	}
	return ConflictProgress{
		Kind:         kind,
		CanContinue:  len(s.Conflicts()) == 0,
		ContinueArgs: []string{kind, "--continue"},
		AbortArgs:    []string{kind, "--abort"},
	}
}

// Continue resumes the in-progress merge/rebase/cherry-pick.
// GIT_EDITOR=true keeps the default message without spawning an editor:
// a GUI client (and CI) has no terminal for git to open one in.
func (r *Repo) Continue(ctx context.Context, kind string) (*RunResult, error) {
	res, err := r.runEnv(ctx, []string{"GIT_EDITOR=true"}, kind, "--continue")
	if err != nil {
		return res, err
	}
	if !res.ExitOK() {
		return res, &GitError{Args: []string{kind, "--continue"}, Stderr: strings.TrimSpace(res.OutputOfStderr())}
	}
	return res, nil
}

// Abort cancels the in-progress op, restoring the pre-op state.
func (r *Repo) Abort(ctx context.Context, kind string) (*RunResult, error) {
	return r.RunOK(ctx, kind, "--abort")
}
