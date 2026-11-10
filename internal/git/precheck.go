package git

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// PrecommitChecks mirrors IDEA's Commit panel "Checks" drawer: a
// read-only report of issues the user might want to fix before
// committing. None of these block the commit — they're advisory.
type PrecommitChecks struct {
	TODOCount     int      // TODO/FIXME markers across staged paths
	WhitespaceErr []string // `git diff --cached --check` findings
	HasHooks      bool     // .git/hooks/pre-commit exists and is executable
	HookPath      string   // absolute path to the hook, for display
	TemplatePath  string   // configured commit.template ("" when unset)
}

var todoRe = regexp.MustCompile(`\b(TODO|FIXME|XXX|HACK)\b`)

// PrecommitCheck runs the advisory checks against the staged index.
// paths limits the TODO scan to files the user is about to commit
// (empty = scan every staged path).
func (r *Repo) PrecommitCheck(ctx context.Context, paths []string) (*PrecommitChecks, error) {
	out := &PrecommitChecks{}

	// Whitespace errors in the staged diff.
	res, err := r.Run(ctx, "diff", "--cached", "--check")
	if err != nil {
		return nil, err
	}
	if res.ExitOK() || res.Output() != "" {
		for _, ln := range strings.Split(res.Output(), "\n") {
			if ln != "" {
				out.WhitespaceErr = append(out.WhitespaceErr, ln)
			}
		}
	}

	// TODO/FIXME count across the files being committed.
	if len(paths) == 0 {
		res, err := r.Run(ctx, "diff", "--cached", "--name-only", "-z")
		if err != nil {
			return nil, err
		}
		if res.ExitOK() {
			for _, p := range strings.Split(string(res.Stdout), "\x00") {
				if p != "" {
					paths = append(paths, p)
				}
			}
		}
	}
	for _, p := range paths {
		full := filepath.Join(r.Worktree, p)
		f, err := os.Open(full)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if todoRe.MatchString(sc.Text()) {
				out.TODOCount++
			}
		}
		_ = f.Close()
	}

	// Hooks presence — resolve .git dir via git (handles linked
	// worktrees whose .git is a file).
	if res2, err := r.Run(ctx, "rev-parse", "--git-dir"); err == nil && res2.ExitOK() {
		gitDir := strings.TrimSpace(res2.Output())
		if !filepath.IsAbs(gitDir) {
			gitDir = filepath.Join(r.Worktree, gitDir)
		}
		hook := filepath.Join(gitDir, "hooks", "pre-commit")
		if info, err := os.Stat(hook); err == nil && info.Mode()&0o111 != 0 {
			out.HasHooks = true
			out.HookPath = hook
		}
	}

	// commit.template from `git config --get commit.template`, or the
	// conventional .gitmessage at the worktree root.
	res, err = r.Run(ctx, "config", "--get", "commit.template")
	if err == nil && res.ExitOK() {
		out.TemplatePath = strings.TrimSpace(res.Output())
	}
	if out.TemplatePath == "" {
		if _, err := os.Stat(filepath.Join(r.Worktree, ".gitmessage")); err == nil {
			out.TemplatePath = ".gitmessage"
		}
	}

	return out, nil
}

// CommitTemplate loads the configured commit template's text for the
// message input (IDEA: "Load template"). Falls back to the worktree
// .gitmessage when the configured path doesn't exist or isn't set.
func (r *Repo) CommitTemplate(ctx context.Context) (string, error) {
	res, err := r.Run(ctx, "config", "--get", "commit.template")
	if err != nil {
		return "", err
	}
	path := strings.TrimSpace(res.Output())
	if path != "" && !filepath.IsAbs(path) {
		path = filepath.Join(r.Worktree, path)
	}
	b, rerr := os.ReadFile(path)
	if rerr != nil || len(b) == 0 {
		// Fall back to the conventional .gitmessage.
		alt := filepath.Join(r.Worktree, ".gitmessage")
		b, rerr = os.ReadFile(alt)
	}
	if rerr != nil {
		return "", fmt.Errorf("load commit template: %w", rerr)
	}
	// Strip comment lines (the same convention git uses).
	var lines []string
	for _, ln := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(ln), "#") {
			lines = append(lines, ln)
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n")), nil
}
