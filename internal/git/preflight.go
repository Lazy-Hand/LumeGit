package git

import (
	"context"
	"fmt"
	"strings"
)

// checkPreCommitState implements the pre-commit gate: staged content,
// conflicts, or a sequencer operation mid-flight all block a plain
// changelist-mode commit (conservative first-version rule from the
// design notes).
func (r *Repo) checkPreCommitState(ctx context.Context) error {
	s, err := r.Snapshot(ctx)
	if err != nil {
		return fmt.Errorf("commit: snapshot state: %w", err)
	}
	switch {
	case s.InProgress != "":
		return ErrConflictInProgress
	}
	for i := range s.Files {
		f := &s.Files[i]
		switch {
		case f.Conflict():
			return ErrConflictInProgress
		case f.Staged():
			return ErrStagedChangesPending
		}
	}
	return nil
}

// treeOID returns HEAD's tree object id ("" for unborn HEAD).
func (r *Repo) treeOID(ctx context.Context) (string, error) {
	res, err := r.Run(ctx, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return "", err
	}
	if !res.ExitOK() {
		// Unborn HEAD has no tree.
		if strings.Contains(res.OutputOfStderr(), "unknown revision") ||
			strings.Contains(res.OutputOfStderr(), "bad revision") {
			return "", nil
		}
		return "", &GitError{Args: []string{"rev-parse", "HEAD^{tree}"}, Stderr: res.OutputOfStderr()}
	}
	return strings.TrimSpace(res.Output()), nil
}
