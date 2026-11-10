package git

import (
	"context"
	"strings"
)

// Submodule is one entry from `.gitmodules` + `git submodule status`.
type Submodule struct {
	Path     string
	Name     string // .gitmodules name (usually == Path)
	OID      string // checked-out commit (short), empty when not initialized
	Init     bool   // initialized (has a .git dir / worktree)
	Dirty    bool   // worktree differs from the recorded commit
	Unmerged bool
}

// Submodules lists the repo's submodules. A repo without .gitmodules
// returns nil.
func (r *Repo) Submodules(ctx context.Context) ([]Submodule, error) {
	// `git submodule status` prints one line per module:
	//   <sp>oid path        — clean
	//   -oid path          — not initialized
	//   +oid path          — checked-out commit ≠ recorded
	//   Uoid path          — merge conflicts
	res, err := r.Run(ctx, "submodule", "status", "--recursive")
	if err != nil {
		return nil, err
	}
	if !res.ExitOK() {
		stderr := res.OutputOfStderr()
		// No submodules: git says nothing or "no submodule mapping".
		if strings.Contains(stderr, "no submodule") ||
			strings.TrimSpace(stderr) == "" && res.Output() == "" {
			return nil, nil
		}
		return nil, &GitError{Args: []string{"submodule", "status"}, Stderr: stderr}
	}
	var out []Submodule
	for _, ln := range strings.Split(res.Output(), "\n") {
		if ln == "" {
			continue
		}
		sm := Submodule{Init: true}
		rest := ln
		switch ln[0] {
		case '-':
			sm.Init = false
			rest = ln[1:]
		case '+':
			sm.Dirty = true
			rest = ln[1:]
		case 'U':
			sm.Unmerged = true
			rest = ln[1:]
		case ' ':
			rest = ln[1:]
		}
		f := strings.Fields(rest)
		if len(f) >= 2 {
			sm.OID = f[0]
			sm.Path = f[1]
			sm.Name = f[1]
		}
		out = append(out, sm)
	}
	return out, nil
}

// SubmoduleUpdate runs `git submodule update --init` for one path
// (or all when path is empty) — IDEA's "Update" on a submodule.
func (r *Repo) SubmoduleUpdate(ctx context.Context, path string) (*RunResult, error) {
	args := []string{"submodule", "update", "--init", "--recursive"}
	if path != "" {
		args = append(args, "--", path)
	}
	return r.RunOK(ctx, args...)
}
