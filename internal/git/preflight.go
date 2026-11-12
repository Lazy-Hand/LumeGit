package git

import (
	"context"
	"fmt"
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
