package git

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// CloneRequest carries one git clone operation.
type CloneRequest struct {
	URL     string // remote URL (HTTPS or SSH)
	DestDir string // parent directory; repo goes into <DestDir>/<name>
	// Name overrides the directory name; empty uses the URL's basename
	// with ".git" stripped.
	Name string
	// Branch checks out this branch instead of the default.
	Branch string
	// Depth limits history (0 = full clone); >0 passes --depth.
	Depth int
	// RecurseSubmodules passes --recurse-submodules.
	RecurseSubmodules bool
	// NoCheckout passes -n (clone without checkout).
	NoCheckout bool
}

// RepoName derives the directory name a remote URL would clone into,
// matching git's rules: basename minus a ".git" suffix, or the last
// path segment for scp-style URLs.
func RepoName(url string) string {
	base := url
	if i := strings.LastIndexAny(base, "/:"); i >= 0 {
		base = base[i+1:]
	}
	return strings.TrimSuffix(base, ".git")
}

// Clone runs git clone in DestDir's parent and returns the new
// working-tree path. The clone happens on a goroutine by the caller —
// the command itself is synchronous here so progress is captured in
// RunResult.Output for the UI log.
func Clone(ctx context.Context, req CloneRequest) (worktree string, res *RunResult, err error) {
	name := req.Name
	if name == "" {
		name = RepoName(req.URL)
	}
	if name == "" {
		return "", nil, fmt.Errorf("clone: cannot derive a directory name from URL %q", req.URL)
	}
	dest := filepath.Join(req.DestDir, name)

	args := []string{"clone", "--progress"}
	if req.Branch != "" {
		args = append(args, "--branch", req.Branch)
	}
	if req.Depth > 0 {
		args = append(args, "--depth", strconv.Itoa(req.Depth))
	}
	if req.RecurseSubmodules {
		args = append(args, "--recurse-submodules")
	}
	if req.NoCheckout {
		args = append(args, "-n")
	}
	args = append(args, req.URL, dest)
	res, err = Cmd.run(ctx, nil, args)
	if err != nil {
		return "", res, err
	}
	if !res.ExitOK() {
		return "", res, &GitError{Args: args, Stderr: res.OutputOfStderr()}
	}
	return dest, res, nil
}

// InitRepo runs `git init` at path (creates it if missing). Returns
// the worktree path for the opener.
func InitRepo(ctx context.Context, path string) (*RunResult, error) {
	return Cmd.run(ctx, nil, []string{"init", "-b", "main", path})
}
