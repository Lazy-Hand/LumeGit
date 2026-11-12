// Package svn wraps the svn command-line tool for LumeGit.
//
// Design rules (mirroring internal/git):
//
//   - Never build shell command strings from user-controlled paths; svn
//     receives arguments as a slice, "--" separates options from targets.
//   - Machine-readable output only: --xml for status/log/info/diff/
//     blame/proplist; text output (update/commit/checkout) is parsed
//     line-wise, never shown raw to the user without a wrapper.
//   - Every invocation carries --non-interactive: auth/conflict prompts
//     must fail (E170001/E215004) so the UI can prompt and retry with
//     credentials, never hang a background worker on a TTY ask.
//   - Peg-revision ambiguity: paths ending in "@" are legal filenames
//     (icon@2x.png) but svn reads @rev — call pathArg() on any path
//     passed where a URL_or_PATH is expected (NOT on svn diff, which
//     rejects a trailing @).
package svn

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// WC is one checked-out working copy anchored at its root. SVN 1.7+
// keeps metadata in a single .svn/wc.db at the root — there's no
// worktree/submodule indirection like git's.
type WC struct {
	// Root is the absolute path of the working copy root.
	Root string
}

// NewWC returns a WC for the working copy at dir. It does not verify
// the path; callers go through Open.
func NewWC(dir string) *WC {
	return &WC{Root: dir}
}

// Open verifies dir is inside an SVN working copy by asking svn info;
// a bare .svn dir check would mis-detect SVN <1.7 (per-dir metadata)
// layouts svn itself no longer reads at the root.
func Open(ctx context.Context, dir string) (*WC, error) {
	res, err := NewWC(dir).Run(ctx, "info", "--xml")
	if err != nil {
		return nil, err
	}
	if !res.ExitOK() {
		msg := strings.TrimSpace(res.OutputOfStderr())
		if msg == "" {
			msg = "not a working copy"
		}
		return nil, fmt.Errorf("open %s: %s", dir, msg)
	}
	return NewWC(dir), nil
}

// ErrSVNMissing reports that no usable svn binary was found.
var ErrSVNMissing = errors.New("svn executable not found (需要安装 Subversion 命令行工具)")

// DetectSVN returns the svn binary name if one is on PATH.
func DetectSVN(ctx context.Context) (string, error) {
	if err := exec.Command("svn", "--version", "--quiet").Run(); err != nil {
		return "", ErrSVNMissing
	}
	return "svn", nil
}

// RunResult captures one svn invocation. Stdout/Stderr keep raw bytes:
// XML output is parsed, text output (update/commit) is shown to the user.
type RunResult struct {
	Args   []string
	Stdout []byte
	Stderr []byte
	Exit   int // -1 when the process could not be started
}

// ExitOK reports whether svn exited successfully.
func (r *RunResult) ExitOK() bool { return r.Exit == 0 }

// Output returns stdout as text.
func (r *RunResult) Output() string { return string(r.Stdout) }

// OutputOfStderr returns stderr as text.
func (r *RunResult) OutputOfStderr() string { return string(r.Stderr) }

// Error is a failed svn invocation, safe to show to the user.
type Error struct {
	Args   []string
	Stderr string
}

func (e *Error) Error() string {
	if e.Stderr == "" {
		return fmt.Sprintf("svn %s failed", strings.Join(e.Args, " "))
	}
	return fmt.Sprintf("svn %s: %s", strings.Join(e.Args, " "), e.Stderr)
}

// runner executes svn. Tests swap it out.
type runner interface {
	run(ctx context.Context, wc *WC, args []string) (*RunResult, error)
}

// commandRunner shells out to the local svn binary.
type commandRunner struct{ bin string }

func (c *commandRunner) run(ctx context.Context, wc *WC, args []string) (*RunResult, error) {
	// --non-interactive: never block on an auth/conflict prompt — fail
	// so the UI can collect credentials and retry explicitly.
	full := append([]string{"--non-interactive"}, args...)
	cmd := exec.CommandContext(ctx, c.bin, full...)
	if wc != nil {
		cmd.Dir = wc.Root
	}
	env := cmd.Environ()
	env = svnEnv(env)
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
			return res, fmt.Errorf("%w: run %q in %q: %v", ErrSVNMissing, c.bin, wcRoot(wc), err)
		}
	}
	return res, nil
}

// svnEnv returns env with a UTF-8 locale so non-ASCII paths and log
// messages convert correctly (LC_ALL=C broke svn's UTF-8 handling).
// We keep LC_ALL unset-ish: C.UTF-8 is available on macOS/Linux and
// preserves deterministic English output like LC_ALL=C did.
func svnEnv(env []string) []string {
	set := func(key, val string) {
		found := false
		for i, e := range env {
			if strings.HasPrefix(e, key+"=") {
				env[i] = key + "=" + val
				found = true
			}
		}
		if !found {
			env = append(env, key+"="+val)
		}
	}
	set("LC_ALL", "C.UTF-8")
	set("LANG", "C.UTF-8")
	return env
}

// runStdin feeds bytes on stdin (commit messages via -F /dev/stdin).
func (c *commandRunner) runStdin(ctx context.Context, wc *WC, stdin []byte, args []string) (*RunResult, error) {
	full := append([]string{"--non-interactive"}, args...)
	cmd := exec.CommandContext(ctx, c.bin, full...)
	if wc != nil {
		cmd.Dir = wc.Root
	}
	env := cmd.Environ()
	env = svnEnv(env)
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
			return res, fmt.Errorf("%w: run %q in %q: %v", ErrSVNMissing, c.bin, wcRoot(wc), err)
		}
	}
	return res, nil
}

func wcRoot(w *WC) string {
	if w == nil {
		return "(no wc)"
	}
	return w.Root
}

// Cmd is the package-level svn interface.
var Cmd runner = &commandRunner{bin: "svn"}

// cmdTimeout bounds one quick svn invocation (status, info, log, cat,
// diff, props …). Those can stall on a dead server; a hard cap keeps the
// UI's worker from hanging the tab forever. --non-interactive already
// rules out auth prompts.
const cmdTimeout = 45 * time.Second

// longCmdTimeout bounds commands that move data or rewrite the working
// copy (update, commit, checkout …). Their runtime scales with repo size
// and bandwidth, and killing one mid-flight leaves the working copy
// locked (needs `svn cleanup`), so they only get a generous safety cap
// against a truly dead connection.
const longCmdTimeout = 2 * time.Hour

// longRunning lists subcommands that get longCmdTimeout.
var longRunning = map[string]bool{
	"update": true, "up": true,
	"commit": true, "ci": true,
	"checkout": true, "co": true,
	"switch": true, "sw": true,
	"merge":    true,
	"cleanup":  true,
	"relocate": true,
	"copy":     true, "cp": true,
	"export":  true,
	"import":  true,
	"upgrade": true,
}

// timeoutFor picks the cap for one invocation by its subcommand.
func timeoutFor(args []string) time.Duration {
	if len(args) > 0 && longRunning[args[0]] {
		return longCmdTimeout
	}
	return cmdTimeout
}

// Run executes svn in the working copy and returns the result. A
// nonzero exit is not an error: callers inspect Exit and Stderr.
func (w *WC) Run(ctx context.Context, args ...string) (*RunResult, error) {
	cctx, cancel := context.WithTimeout(ctx, timeoutFor(args))
	defer cancel()
	return Cmd.run(cctx, w, args)
}

// RunStdin executes svn feeding data on stdin.
func (w *WC) RunStdin(ctx context.Context, stdin []byte, args ...string) (*RunResult, error) {
	cctx, cancel := context.WithTimeout(ctx, timeoutFor(args))
	defer cancel()
	if cr, ok := Cmd.(*commandRunner); ok {
		return cr.runStdin(cctx, w, stdin, args)
	}
	return w.Run(cctx, args...)
}

// RunOK runs svn and returns an *Error mentioning stderr on nonzero exit.
func (w *WC) RunOK(ctx context.Context, args ...string) (*RunResult, error) {
	res, err := w.Run(ctx, args...)
	if err != nil {
		return res, err
	}
	if !res.ExitOK() {
		return res, &Error{Args: args, Stderr: strings.TrimSpace(res.OutputOfStderr())}
	}
	return res, nil
}

// pathArg appends "@" to a path to pin its peg revision — without it a
// filename like "icon@2x.png" is parsed as path@rev. Only valid where
// svn accepts a peg (most commands); svn diff rejects it, and callers
// that build URL@rev forms must not double-append.
func pathArg(p string) string {
	if p == "" {
		return p
	}
	return p + "@"
}
