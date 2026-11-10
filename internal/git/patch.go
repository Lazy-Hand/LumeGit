package git

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// CreatePatchFromWorktree writes the working-tree diff for paths to
// destFile (IDEA: "Create Patch from Local Changes"). Empty paths
// means the whole worktree diff.
func (r *Repo) CreatePatchFromWorktree(ctx context.Context, paths []string, destFile string) error {
	args := []string{"diff", "--binary", "--no-color"}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}
	res, err := r.Run(ctx, args...)
	if err != nil {
		return err
	}
	if !res.ExitOK() {
		return &GitError{Args: args, Stderr: res.OutputOfStderr()}
	}
	if len(bytes.TrimSpace(res.Stdout)) == 0 {
		return fmt.Errorf("没有可导出的改动")
	}
	return os.WriteFile(destFile, res.Stdout, 0o644)
}

// CreatePatchFromCommit exports one commit as a format-patch file
// (IDEA: commit right-click → "Create Patch"). Returns the file git
// wrote (git names it <seq>-<subject>.patch inside destDir).
func (r *Repo) CreatePatchFromCommit(ctx context.Context, oid, destDir string) (string, error) {
	res, err := r.Run(ctx, "format-patch", "-1", oid, "-o", destDir)
	if err != nil {
		return "", err
	}
	if !res.ExitOK() {
		return "", &GitError{Args: []string{"format-patch", oid}, Stderr: res.OutputOfStderr()}
	}
	return res.Output(), nil
}

// ApplyPatch applies a .patch file to the worktree (git apply — for
// patches produced from local changes).
func (r *Repo) ApplyPatch(ctx context.Context, patchFile string) (*RunResult, error) {
	abs, err := filepath.Abs(patchFile)
	if err != nil {
		return nil, err
	}
	return r.RunOK(ctx, "apply", "--whitespace=nowarn", abs)
}

// ApplyMailPatch applies a format-patch file as a commit (git am —
// preserves author/message, like IDEA's "Apply Patch" on a commit
// patch).
func (r *Repo) ApplyMailPatch(ctx context.Context, patchFile string) (*RunResult, error) {
	abs, err := filepath.Abs(patchFile)
	if err != nil {
		return nil, err
	}
	return r.RunOK(ctx, "am", "--keep-cr", abs)
}

// LooksLikeMailPatch reports whether file starts like a format-patch
// output (header "From <sha>" + a Subject line), i.e. should go
// through git am rather than git apply.
func LooksLikeMailPatch(data []byte) bool {
	head := data
	if len(head) > 256 {
		head = head[:256]
	}
	if !bytes.HasPrefix(head, []byte("From ")) {
		return false
	}
	return bytes.Contains(head, []byte("\nSubject:")) ||
		bytes.HasPrefix(head, []byte("From "))
}
