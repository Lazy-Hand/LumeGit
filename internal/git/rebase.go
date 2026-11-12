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
// order the todo file uses. --no-merges: a "pick <merge>" todo is
// rejected by git and leaves the sequencer stuck mid-flight.
func (r *Repo) RebasePlan(ctx context.Context, onto string) ([]RebaseEntry, error) {
	res, err := r.Run(ctx, "log", "--reverse", "--no-merges",
		"--format=%H%x1f%h%x1f%s%x1e", onto+"..HEAD", "--")
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
	// Persist the helpers inside .git instead of /tmp: the temp dir used
	// to be deleted when rebase paused on a conflict or 'edit', so a
	// later `rebase --continue` lost every queued reword/squash message.
	// .git/lumegit-rebase survives the pause; git cleans it itself.
	gd, derr := r.gitDir(ctx)
	if derr != nil {
		return nil, derr
	}
	dir := filepath.Join(gd, "lumegit-rebase")
	_ = os.RemoveAll(dir) // stale helper set from an aborted run
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	// Only remove when rebase actually finished; on failure the helper
	// files stay so --continue still finds them. The seq script is only
	// needed once but keeping it around is harmless.
	defer func() {
		if gd2, e2 := r.gitDir(context.Background()); e2 == nil {
			if _, serr := os.Stat(filepath.Join(gd2, "rebase-merge")); os.IsNotExist(serr) {
				_ = os.RemoveAll(dir)
			}
		}
	}()

	// The sequence-editor helper: receives the todo path as argv[1],
	// writes our plan. A leading squash is illegal in git — rewrite it
	// to fixup? No: git also rejects fixup on the first entry (it has
	// nothing to fix into). Reject early with a readable error instead
	// of letting the sequencer stop with an opaque todo complaint.
	todo := filepath.Join(dir, "todo")
	var b strings.Builder
	for i, e := range entries {
		act := e.Action
		if act == "" {
			act = RebasePick
		}
		if i == 0 && (act == RebaseSquash || act == RebaseFixup) {
			return nil, fmt.Errorf("rebase: 第一条提交不能使用 %s（没有可合并的上一条）", act)
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
	// files. Git opens the editor ONCE per squash group (a run of
	// squash/fixup lines) plus once per reword — so we queue one
	// message per GROUP, not per squash entry; the old per-entry queue
	// handed a reword entry the previous group's message.
	msgDir := filepath.Join(dir, "msgs")
	if err := os.MkdirAll(msgDir, 0o755); err != nil {
		return nil, err
	}
	idx := 0
	writeMsg := func(msg string) error {
		path := filepath.Join(msgDir, fmt.Sprintf("%03d", idx))
		idx++
		return os.WriteFile(path, []byte(msg+"\n"), 0o644)
	}
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		switch e.Action {
		case RebaseReword:
			msg := e.Message
			if msg == "" {
				msg = e.Subject
			}
			if err := writeMsg(msg); err != nil {
				return nil, err
			}
		case RebaseSquash, RebaseFixup:
			// End of a squash run: message comes from the LAST entry in
			// the run (IDEA's "squash into" keeps the newest subject);
			// empty entries fall back to their subject, skipping blanks.
			j := i
			for j+1 < len(entries) && (entries[j+1].Action == RebaseSquash || entries[j+1].Action == RebaseFixup) {
				j++
			}
			msg := entries[j].Message
			if msg == "" {
				msg = entries[j].Subject
			}
			if err := writeMsg(msg); err != nil {
				return nil, err
			}
			i = j // consumed the whole group
		}
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
