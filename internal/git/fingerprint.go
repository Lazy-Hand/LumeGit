package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// RefsFingerprint is a cheap "did anything change?" probe for a repo —
// the lazygit pattern. It costs one `for-each-ref` over local heads plus
// reading .git/HEAD, far cheaper than `git status`, so a background
// poller can run it every couple of seconds without burning CPU. When
// the returned string differs from the last call, refs moved (commit,
// checkout, merge, fetch wrote refs) and the caller does a full refresh.
//
// It deliberately does NOT fingerprint the index or worktree files —
// those change constantly during a build and watching them was the CPU
// problem. Worktree edits surface on window focus or manual refresh.
func (r *Repo) RefsFingerprint(ctx context.Context) (string, error) {
	// refname + objectname for every local head; remotes/tags move on
	// fetch too, so include them — still a single process.
	res, err := r.Run(ctx, "for-each-ref",
		"--format=%(refname)%1f%(objectname)", "refs/heads", "refs/remotes", "refs/tags")
	if err != nil || !res.ExitOK() {
		return "", err
	}
	var b strings.Builder
	b.Write(res.Stdout)

	// HEAD itself: branch switch changes .git/HEAD, detached checkouts
	// change the OID it points at. Read the file directly instead of
	// spawning `symbolic-ref` — one less process in the hot path.
	gitDir, derr := r.gitDir(ctx)
	if derr == nil {
		if data, rerr := os.ReadFile(filepath.Join(gitDir, "HEAD")); rerr == nil {
			b.WriteString("HEAD=")
			b.Write(data)
		}
		// index mtime: a commit/stage/merge rewrites the index. Catching
		// it keeps staged-state fresh without diffing contents.
		if st, serr := os.Stat(filepath.Join(gitDir, "index")); serr == nil {
			b.WriteString("index=")
			b.WriteString(st.ModTime().UTC().String())
		}
	}
	return b.String(), nil
}
