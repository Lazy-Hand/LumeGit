package git

import (
	"context"
	"fmt"
	"io"
	"os"
)

// sniffBytes caps how much of a file binary detection reads — looksBinary
// only needs a representative head, and full reads of multi-GB assets
// were the diff pane's memory blowout.
const sniffBytes = 64 << 10

// readWorktreeFileHead reads at most n bytes of the worktree file —
// enough for binary sniffing, never the whole blob.
func readWorktreeFileHead(ctx context.Context, r *Repo, path string, n int64) ([]byte, error) {
	_ = ctx
	full := r.Worktree + "/" + path
	if st, err := os.Stat(full); err == nil && st.IsDir() {
		return nil, fmt.Errorf("%q is a directory, not a file", path)
	}
	f, err := os.Open(full)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, n))
}

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
