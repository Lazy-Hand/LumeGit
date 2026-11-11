// Package git wraps the git command-line tool for LumeGit.
//
// Design rules (see the commit-workspace design notes):
//
//   - Never build shell command strings from user-controlled paths. Git
//     receives arguments as a slice, and literals like "--" separate
//     options from pathspecs.
//   - Machine-readable, NUL-delimited output only for parsing status and
//     listings; human formats are not parsed.
//   - Refreshing state must never take the index lock persistently: every
//     read-only invocation sets --no-optional-locks.
//   - The wrapped git decides hooks, credential helpers and filters; this
//     layer adds no --no-verify and never invents commits.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Open verifies that dir is inside a git working tree and returns a
// Repo anchored there. Top level repo is resolved by git, which also
// handles .git files (linked worktrees) and nested git roots.
func Open(ctx context.Context, dir string) (*Repo, error) {
	res, err := Cmd.run(ctx, NewRepo(dir), []string{"rev-parse", "--path-format=absolute", "--show-toplevel"})
	if err != nil || !res.ExitOK() {
		msg := strings.TrimSpace(strings.TrimSuffix(res.OutputOfStderr(), "\n"))
		if msg == "" {
			msg = "not a git repository"
		}
		return nil, fmt.Errorf("open %s: %s", dir, msg)
	}
	top := strings.TrimSpace(res.Output())
	if top == "" {
		return nil, fmt.Errorf("open %s: git returned no top level", dir)
	}
	return &Repo{Worktree: top}, nil
}

// DetectGit returns the path of the git binary if one is available.
func DetectGit(ctx context.Context) (string, error) {
	cmd := exec.Command("git", "--version")
	out, err := cmd.Output()
	if err != nil {
		return "", ErrGitMissing
	}
	_ = out
	return "git", nil
}

// ErrGitMissing reports that no usable git executable was found.
var ErrGitMissing = errors.New("git executable not found")

// Repo is one checked-out working tree plus its git directory. Linked
// worktrees have per-worktree HEADs and indexes, so identity is tied to
// the worktree path, resolved by git itself — never assumed to be a
// ".git" directory.
type Repo struct {
	// Worktree is the absolute path of the working tree.
	Worktree string

	// PreferNoUntracked asks Snapshot to run `status -uno` — report
	// untracked entries at directory granularity instead of enumerating
	// every file. Set it when a repo's untracked set is huge (VSCode's
	// statusLimit is 10000): per-file stat()ing is the dominant cost of
	// a refresh on such trees.
	PreferNoUntracked bool
}

// NewRepo returns a Repo for the working tree at dir. It does not verify
// the path here; callers go through Open.
func NewRepo(dir string) *Repo {
	return &Repo{Worktree: dir}
}

// RunResult captures one git invocation for the operation log shown in
// the UI. Stdout/Stderr keep the raw bytes because hook output and
// error messages are shown to the user, not parsed.
type RunResult struct {
	Args   []string
	Stdout []byte
	Stderr []byte
	Exit   int // -1 when the process could not be started
}

// ExitOK reports whether git exited successfully.
func (r *RunResult) ExitOK() bool { return r.Exit == 0 }

// Output returns stdout as text for display.
func (r *RunResult) Output() string { return string(r.Stdout) }

// OutputOfStderr returns stderr as text for display.
func (r *RunResult) OutputOfStderr() string { return string(r.Stderr) }

// FullLog renders the invocation and both streams for the command log.
func (r *RunResult) FullLog() string {
	var b strings.Builder
	b.WriteString("$ git")
	for _, a := range r.Args {
		b.WriteString(" ")
		b.WriteString(a)
	}
	if len(r.Stderr) > 0 {
		b.WriteString("\n-- stderr --\n")
		b.Write(r.Stderr)
	}
	if len(r.Stdout) > 0 {
		b.WriteString("\n-- stdout --\n")
		b.Write(r.Stdout)
	}
	return b.String()
}

// runner executes git. Tests swap it out.
type runner interface {
	run(ctx context.Context, repo *Repo, args []string) (*RunResult, error)
	runEnv(ctx context.Context, repo *Repo, extraEnv []string, args []string) (*RunResult, error)
}

// commandRunner shells out to the local git binary.
type commandRunner struct {
	bin string
}

func (c *commandRunner) run(ctx context.Context, repo *Repo, args []string) (*RunResult, error) {
	return c.runEnv(ctx, repo, nil, args)
}

// runEnv is run with extra environment variables prepended (nil =
// inherit os.Environ via cmd.Environ()).
func (c *commandRunner) runEnv(ctx context.Context, repo *Repo, extraEnv []string, args []string) (*RunResult, error) {
	full := append([]string{"--no-optional-locks"}, args...)
	cmd := exec.CommandContext(ctx, c.bin, full...)
	if repo != nil {
		cmd.Dir = repo.Worktree
	}
	// Pin C locale: git's error messages must be predictable for
	// structured handling independent of the user's system language.
	// User-visible git output gets re-shown as-is; hooks that print
	// localized text are unaffected (they set their own env).
	env := cmd.Environ()
	// extraEnv OVERRIDES ambient vars: duplicate keys are replaced in
	// place because execve passes the whole list and getenv returns
	// the first match (a user's GIT_EDITOR=true would silently beat
	// our scripted editor if we merely appended).
	for _, extra := range extraEnv {
		key := extra[:strings.IndexByte(extra, '=')+1]
		replaced := false
		for i, e := range env {
			if strings.HasPrefix(e, key) {
				env[i] = extra
				replaced = true
			}
		}
		if !replaced {
			env = append(env, extra)
		}
	}
	env = append(env, "LC_ALL=C", "LANG=C")
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	res := &RunResult{Args: args, Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), Exit: 0}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			res.Exit = ee.ExitCode()
		} else {
			res.Exit = -1
			return res, fmt.Errorf("%w: run %q in %q: %v", ErrGitMissing, c.bin, repoWorktree(repo), err)
		}
	}
	return res, nil
}

// runStdin is run with a byte stream on stdin.
func (c *commandRunner) runStdin(ctx context.Context, repo *Repo, stdin []byte, args []string) (*RunResult, error) {
	full := append([]string{"--no-optional-locks"}, args...)
	cmd := exec.CommandContext(ctx, c.bin, full...)
	if repo != nil {
		cmd.Dir = repo.Worktree
	}
	env := cmd.Environ()
	env = append(env, "LC_ALL=C", "LANG=C")
	cmd.Env = env
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	res := &RunResult{Args: args, Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), Exit: 0}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			res.Exit = ee.ExitCode()
		} else {
			res.Exit = -1
			return res, fmt.Errorf("%w: run %q in %q: %v", ErrGitMissing, c.bin, repoWorktree(repo), err)
		}
	}
	return res, nil
}

// gitDir returns the absolute .git dir for the repo (per-worktree for
// linked worktrees).
func (r *Repo) gitDir(ctx context.Context) (string, error) {
	res, err := r.Run(ctx, "rev-parse", "--path-format=absolute", "--git-dir")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(res.Output()), nil
}

func repoWorktree(r *Repo) string {
	if r == nil {
		return "(no worktree)"
	}
	return r.Worktree
}

// Cmd is the package-level git interface. All state-changing operations
// go through it; snapshot reads use Snapshot.
var Cmd runner = &commandRunner{bin: "git"}

// Run executes git with args in the repo and returns the result. A
// nonzero exit is not an error: callers inspect Exit and Stderr.
func (r *Repo) Run(ctx context.Context, args ...string) (*RunResult, error) {
	return Cmd.run(ctx, r, args)
}

// runEnv executes git with extra env vars (e.g. GIT_SEQUENCE_EDITOR
// for scripted interactive rebase).
func (r *Repo) runEnv(ctx context.Context, env []string, args ...string) (*RunResult, error) {
	return Cmd.runEnv(ctx, r, env, args)
}

// RunStdin executes git feeding data on stdin ("-" args read it).
func (r *Repo) RunStdin(ctx context.Context, stdin []byte, args ...string) (*RunResult, error) {
	if cr, ok := Cmd.(*commandRunner); ok {
		return cr.runStdin(ctx, r, stdin, args)
	}
	return Cmd.run(ctx, r, args) // fallback: custom runners ignore stdin
}

// RunOK runs git and returns an error mentioning stderr on nonzero exit.
func (r *Repo) RunOK(ctx context.Context, args ...string) (*RunResult, error) {
	res, err := r.Run(ctx, args...)
	if err != nil {
		return res, err
	}
	if !res.ExitOK() {
		return res, &GitError{Args: args, Stderr: strings.TrimSpace(res.OutputOfStderr())}
	}
	return res, nil
}

// GitError is a failed git invocation, safe to show to the user.
type GitError struct {
	Args   []string
	Stderr string
}

func (e *GitError) Error() string {
	if e.Stderr == "" {
		return fmt.Sprintf("git %s failed", strings.Join(e.Args, " "))
	}
	return fmt.Sprintf("git %s: %s", strings.Join(e.Args, " "), e.Stderr)
}
