package git

import (
	"context"
	"encoding/json"
	"strings"
)

// LFSTracked reports the patterns git-lfs tracks (from
// `git lfs track` / `.gitattributes`). IDEA uses this to badge LFS
// files in the tree.
type LFSTracked struct {
	Patterns []string // e.g. "*.psd" "assets/*.bin"
}

// LFSLock is one lock row from `git lfs locks --json`.
type LFSLock struct {
	Path  string `json:"path"`
	ID    string `json:"id"`
	Owner struct {
		Name string `json:"name"`
	} `json:"owner"`
	LockedAt string `json:"locked_at"`
}

// HasLFS reports whether git-lfs is installed and this repo uses it.
func (r *Repo) HasLFS(ctx context.Context) bool {
	res, err := r.Run(ctx, "lfs", "version")
	return err == nil && res.ExitOK()
}

// LFSTracked lists the tracked patterns.
func (r *Repo) LFSTracked(ctx context.Context) (*LFSTracked, error) {
	res, err := r.Run(ctx, "lfs", "track")
	if err != nil {
		return nil, err
	}
	if !res.ExitOK() {
		return nil, &GitError{Args: []string{"lfs", "track"}, Stderr: res.OutputOfStderr()}
	}
	out := &LFSTracked{}
	for _, ln := range strings.Split(res.Output(), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		// Format: "Listing tracked patterns\n    *.psd (.gitattributes)\n"
		// Take the first token of each indented line.
		f := strings.Fields(ln)
		if len(f) >= 1 && strings.HasPrefix(ln, " ") || strings.HasPrefix(ln, "\t") {
			out.Patterns = append(out.Patterns, f[0])
		}
	}
	return out, nil
}

// LFSPathTracked reports whether path matches any tracked pattern —
// cheap substring check for the file-row badge.
func (t *LFSTracked) LFSPathTracked(path string) bool {
	for _, p := range t.Patterns {
		// Simple glob: "*.<ext>" suffix or "<dir>/" prefix or exact.
		if strings.HasPrefix(p, "*.") {
			if strings.HasSuffix(path, p[1:]) {
				return true
			}
			continue
		}
		if strings.HasSuffix(p, "/") || strings.HasSuffix(p, "/*") {
			prefix := strings.TrimSuffix(p, "*")
			if strings.HasPrefix(path, prefix) {
				return true
			}
			continue
		}
		if path == p || strings.HasSuffix(path, "/"+p) {
			return true
		}
	}
	return false
}

// LFSLocks lists the repo's current locks.
func (r *Repo) LFSLocks(ctx context.Context) ([]LFSLock, error) {
	res, err := r.Run(ctx, "lfs", "locks", "--json")
	if err != nil {
		return nil, err
	}
	if !res.ExitOK() {
		return nil, &GitError{Args: []string{"lfs", "locks"}, Stderr: res.OutputOfStderr()}
	}
	var locks []LFSLock
	if err := json.Unmarshal(res.Stdout, &locks); err != nil {
		return nil, err
	}
	return locks, nil
}

// LFSLock locks path (IDEA: file right-click → "Lock").
func (r *Repo) LFSLock(ctx context.Context, path string) (*RunResult, error) {
	return r.RunOK(ctx, "lfs", "lock", path)
}

// LFSUnlock releases the lock; force takes others' locks.
func (r *Repo) LFSUnlock(ctx context.Context, path string, force bool) (*RunResult, error) {
	args := []string{"lfs", "unlock"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, path)
	return r.RunOK(ctx, args...)
}
