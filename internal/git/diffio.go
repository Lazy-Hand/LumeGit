package git

import (
	"context"
	"fmt"
	"os"
)

// readWorktreeFile reads one file from the working tree.
func readWorktreeFile(ctx context.Context, r *Repo, path string) ([]byte, error) {
	_ = ctx
	full := r.Worktree + "/" + path
	if st, err := os.Stat(full); err == nil && st.IsDir() {
		// A collapsed "dir/" record slipping through (foreign index
		// state, a stale snapshot) must not surface as "is a
		// directory" — there is simply no file content to diff.
		return nil, fmt.Errorf("%q is a directory, not a file", path)
	}
	b, err := os.ReadFile(full)
	if err != nil {
		if os.IsNotExist(err) {
			// Deleted in the worktree: the missing side is the diff.
			return nil, nil
		}
		return nil, err
	}
	return b, nil
}
