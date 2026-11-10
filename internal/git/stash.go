package git

import (
	"context"
	"fmt"
	"strings"
)

// Stash is one entry in `git stash list`.
type Stash struct {
	Index   int    // N in stash@{N}
	Name    string // "stash@{0}"
	Branch  string // branch it was created on
	Message string // user's -m text (or "WIP on ...")
}

// StashFile is one file changed inside a stash entry.
type StashFile struct {
	Path      string
	OrigPath  string
	Status    byte // 'A' 'M' 'D' 'R' 'T'
	Untracked bool // came from the untracked-files commit (^3)
}

// Stashes lists the stash, newest first.
func (r *Repo) Stashes(ctx context.Context) ([]Stash, error) {
	res, err := r.Run(ctx, "stash", "list", "--format=%gd%x1f%gs")
	if err != nil {
		return nil, err
	}
	if !res.ExitOK() {
		return nil, &GitError{Args: []string{"stash", "list"}, Stderr: res.OutputOfStderr()}
	}
	var out []Stash
	for _, line := range strings.Split(strings.TrimSpace(res.Output()), "\n") {
		if line == "" {
			continue
		}
		f := strings.SplitN(line, "\x1f", 2)
		if len(f) < 2 {
			continue
		}
		s := Stash{Name: f[0]}
		fmt.Sscanf(f[0], "stash@{%d}", &s.Index)
		// "%gs" looks like "On main: wip work" or a custom message.
		subj := f[1]
		if rest, ok := strings.CutPrefix(subj, "On "); ok {
			if i := strings.Index(rest, ":"); i >= 0 {
				s.Branch = rest[:i]
				s.Message = strings.TrimSpace(rest[i+1:])
			}
		}
		if s.Message == "" {
			s.Message = subj
		}
		out = append(out, s)
	}
	return out, nil
}

// StashPushRequest carries the flags IDEA's Stash dialog exposes.
type StashPushRequest struct {
	Message          string
	IncludeUntracked bool
	KeepIndex        bool // --keep-index: stash unstaged changes only
}

// StashPush saves the worktree (and optionally untracked files) into a
// new stash entry.
func (r *Repo) StashPush(ctx context.Context, req StashPushRequest) (*RunResult, error) {
	args := []string{"stash", "push"}
	if req.IncludeUntracked {
		args = append(args, "--include-untracked")
	}
	if req.KeepIndex {
		args = append(args, "--keep-index")
	}
	if req.Message != "" {
		args = append(args, "-m", req.Message)
	}
	return r.RunOK(ctx, args...)
}

// StashClear drops every entry (IDEA's "Clear" on the stash panel —
// destructive, call it behind a confirmation).
func (r *Repo) StashClear(ctx context.Context) (*RunResult, error) {
	return r.RunOK(ctx, "stash", "clear")
}

// StashShow returns one stash's full unified-diff text (IDEA's diff
// viewer on a stash row). It's the raw `git stash show -p` output;
// callers render it as text.
func (r *Repo) StashShow(ctx context.Context, name string) (string, error) {
	res, err := r.Run(ctx, "stash", "show", "-p", name)
	if err != nil {
		return "", err
	}
	if !res.ExitOK() {
		return "", &GitError{Args: []string{"stash", "show", "-p", name}, Stderr: res.OutputOfStderr()}
	}
	return res.Output(), nil
}

// StashFiles lists what a stash changed. The stash commit is a merge:
// ^1 is the base, ^2 the index, ^3 the untracked-files commit. We diff
// base→stash for tracked changes and ls-tree ^3 for untracked.
func (r *Repo) StashFiles(ctx context.Context, name string) ([]StashFile, error) {
	var files []StashFile
	// Tracked changes: base vs the stash merge result.
	res, err := r.Run(ctx, "diff", "--name-status", "-z", name+"^1", name)
	if err != nil {
		return nil, err
	}
	if res.ExitOK() {
		for _, cf := range parseNameStatus(res.Stdout) {
			files = append(files, StashFile{
				Path: cf.Path, OrigPath: cf.OrigPath, Status: cf.Status,
			})
		}
	}
	// Untracked: the third parent's whole tree (may not exist).
	res2, err := r.Run(ctx, "ls-tree", "-r", "--name-only", "-z", name+"^3")
	if err != nil {
		return nil, err
	}
	if res2.ExitOK() {
		for _, p := range strings.Split(string(res2.Stdout), "\x00") {
			if p != "" {
				files = append(files, StashFile{Path: p, Status: 'A', Untracked: true})
			}
		}
	}
	return files, nil
}

// StashFileDiff returns old/new content for one file inside a stash.
// old = base commit (^1), new = stash commit.
func (r *Repo) StashFileDiff(ctx context.Context, name string, f StashFile) (*DiffInfo, error) {
	info := &DiffInfo{
		Path:     f.Path,
		OldPath:  f.OrigPath,
		Renamed:  f.Status == 'R',
		OldLabel: "原内容",
		NewLabel: "搁置内容",
	}
	// Old side (tracked files read from the base commit).
	if !f.Untracked && f.Status != 'A' {
		oldPath := f.Path
		if f.OrigPath != "" {
			oldPath = f.OrigPath
		}
		b, err := r.catFile(ctx, name+"^1:"+oldPath)
		if err != nil {
			return nil, err
		}
		info.Old = b
	}
	// New side: untracked files live in ^3, tracked in the stash commit.
	newRev := name
	if f.Untracked {
		newRev = name + "^3"
	}
	if f.Status != 'D' {
		b, err := r.catFile(ctx, newRev+":"+f.Path)
		if err != nil {
			return nil, err
		}
		info.New = b
	}
	info.Binary = looksBinary(info.Old) || looksBinary(info.New)
	return info, nil
}

// StashApply reinstates a stash (keeps the entry).
func (r *Repo) StashApply(ctx context.Context, name string) (*RunResult, error) {
	return r.RunOK(ctx, "stash", "apply", name)
}

// StashPop applies then drops the entry.
func (r *Repo) StashPop(ctx context.Context, name string) (*RunResult, error) {
	return r.RunOK(ctx, "stash", "pop", name)
}

// StashDrop deletes the entry without applying.
func (r *Repo) StashDrop(ctx context.Context, name string) (*RunResult, error) {
	return r.RunOK(ctx, "stash", "drop", name)
}

// StashBranch creates a branch from the stash's base commit and applies.
func (r *Repo) StashBranch(ctx context.Context, branch, name string) (*RunResult, error) {
	if branch == "" {
		return nil, fmt.Errorf("branch name required")
	}
	return r.RunOK(ctx, "stash", "branch", branch, name)
}

// parseStashIndex extracts N from "stash@{N}".
func parseStashIndex(name string) int {
	var n int
	if _, err := fmt.Sscanf(name, "stash@{%d}", &n); err != nil {
		return -1
	}
	return n
}
