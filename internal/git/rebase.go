package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RebaseAction is one entry's verb in an interactive-rebase plan.
type RebaseAction string

const (
	RebasePick   RebaseAction = "pick"
	RebaseReword RebaseAction = "reword"
	RebaseEdit   RebaseAction = "edit"
	RebaseSquash RebaseAction = "squash"
	RebaseFixup  RebaseAction = "fixup"
	RebaseDrop   RebaseAction = "drop"
)

// RebaseEntry is one line of the plan: a commit + the action to apply.
type RebaseEntry struct {
	OID     string
	Short   string
	Subject string
	Action  RebaseAction
	// Message is the replacement subject for reword entries.
	Message string
}

// RebasePlan lists HEAD's commits back to (exclusive) onto — the set
// IDEA's "Interactively Rebase from Here" shows. Oldest first, same
// order the todo file uses.
func (r *Repo) RebasePlan(ctx context.Context, onto string) ([]RebaseEntry, error) {
	res, err := r.Run(ctx, "log", "--reverse",
		"--format=%H%x1f%h%x1f%s%x1e", onto+"..HEAD")
	if err != nil {
		return nil, err
	}
	if !res.ExitOK() {
		return nil, &GitError{Args: []string{"log", onto + "..HEAD"}, Stderr: res.OutputOfStderr()}
	}
	var out []RebaseEntry
	for _, rec := range strings.Split(res.Output(), "\x1e") {
		rec = strings.TrimSpace(rec)
		if rec == "" {
			continue
		}
		f := strings.Split(rec, "\x1f")
		if len(f) < 3 {
			continue
		}
		out = append(out, RebaseEntry{
			OID: f[0], Short: f[1], Subject: f[2], Action: RebasePick,
		})
	}
	return out, nil
}

// InteractiveRebase runs `git rebase -i` with the plan the user edited.
// We drive it non-interactively:
//
//   - GIT_SEQUENCE_EDITOR gets a helper that writes the todo file from
//     entries (pick → drop etc., order preserved).
//   - reword/squash/fixup commits need a message editor later; we
//     replay them via a second helper: reword uses the entry's Message,
//     squash/fixup concatenates the original subjects (IDEA prompts,
//     we keep it deterministic for v1 — squash takes the LAST commit's
//     message, fixup discards).
//
// Conflicts leave the sequencer mid-flight; the caller's existing
// Continue/Abort path handles that, same as merge.
func (r *Repo) InteractiveRebase(ctx context.Context, onto string, entries []RebaseEntry) (*RunResult, error) {
	if len(entries) == 0 {
		return nil, fmt.Errorf("rebase: empty plan")
	}
	dir, err := os.MkdirTemp("", "lumegit-rebase-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	// The sequence-editor helper: receives the todo path as argv[1],
	// writes our plan.
	todo := filepath.Join(dir, "todo")
	var b strings.Builder
	for _, e := range entries {
		act := e.Action
		if act == "" {
			act = RebasePick
		}
		fmt.Fprintf(&b, "%s %s %s\n", act, e.OID, e.Subject)
	}
	if err := os.WriteFile(todo, []byte(b.String()), 0o644); err != nil {
		return nil, err
	}
	seqHelper := filepath.Join(dir, "seq.sh")
	// LF line endings are required — CRLF would make git's editor
	// lookup fail silently and fall back to the default todo.
	seqScript := "#!/bin/sh\ncp " + shellescape(todo) + " \"$1\"\n"
	if err := os.WriteFile(seqHelper, []byte(seqScript), 0o755); err != nil {
		return nil, err
	}

	// The message-editor helper: serves squash/fixup/reword message
	// files. For each commit needing a message we precomputed it; the
	// helper picks them in invocation order via a counter file.
	msgDir := filepath.Join(dir, "msgs")
	if err := os.MkdirAll(msgDir, 0o755); err != nil {
		return nil, err
	}
	idx := 0
	needs := map[RebaseAction]bool{RebaseReword: true, RebaseSquash: true}
	for _, e := range entries {
		if !needs[e.Action] {
			continue
		}
		msg := e.Message
		if msg == "" {
			msg = e.Subject // keep original subject when user left it
		}
		if err := os.WriteFile(
			filepath.Join(msgDir, fmt.Sprintf("%03d", idx)),
			[]byte(msg+"\n"), 0o644); err != nil {
			return nil, err
		}
		idx++
	}
	msgHelper := filepath.Join(dir, "msg.sh")
	msgScript := "#!/bin/sh\n" +
		"d=" + shellescape(msgDir) + "\n" +
		"n=$(cat \"$d/.n\" 2>/dev/null || echo 0)\n" +
		"f=$(printf '%03d' \"$n\")\n" +
		"if [ -f \"$d/$f\" ]; then cp \"$d/$f\" \"$1\"; fi\n" +
		"echo $((n+1)) > \"$d/.n\"\n"
	if err := os.WriteFile(msgHelper, []byte(msgScript), 0o755); err != nil {
		return nil, err
	}

	// Debug trace: when set, helper scripts append to this file.
	var env []string
	env = append(env, os.Environ()...)
	env = append(env,
		"GIT_SEQUENCE_EDITOR="+seqHelper,
		"GIT_EDITOR="+msgHelper,
	)
	res, err := r.runEnv(ctx, env, "rebase", "-i", onto)
	return res, err
}

// shellescape wraps a path in single quotes for use inside a POSIX
// shell script body (we only embed absolute paths we created).
func shellescape(p string) string {
	return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
}
