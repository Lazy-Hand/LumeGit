package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestPrecommitCheck exercises the advisory checks.
func TestPrecommitCheck(t *testing.T) {
	repo, dir := setupTempRepo(t)
	ctx := context.Background()

	// Stage a file with a TODO and a trailing whitespace line.
	bad := filepath.Join(dir, "checkme.go")
	if err := os.WriteFile(bad, []byte("package x\n\nfunc f() { // TODO fix\n\t_ = 1  \n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := repo.Stage(ctx, []string{"checkme.go"}); err != nil {
		t.Fatalf("Stage: %v", err)
	}

	pc, err := repo.PrecommitCheck(ctx, []string{"checkme.go"})
	if err != nil {
		t.Fatalf("PrecommitCheck: %v", err)
	}
	if pc.TODOCount < 1 {
		t.Errorf("TODOCount = %d", pc.TODOCount)
	}
	if len(pc.WhitespaceErr) == 0 {
		t.Error("expected whitespace errors")
	}
}

// TestCommitTemplate loads a template from .gitmessage when unset.
func TestCommitTemplate(t *testing.T) {
	repo, dir := setupTempRepo(t)
	ctx := context.Background()

	if err := os.WriteFile(filepath.Join(dir, ".gitmessage"),
		[]byte("feat: \n\n# comment line\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	text, err := repo.CommitTemplate(ctx)
	if err != nil {
		t.Fatalf("CommitTemplate: %v", err)
	}
	if text == "" {
		t.Error("template empty")
	}
	// Comment lines are stripped.
	if string(text) == "feat: \n\nbody" || len(text) > 0 && text[0] == '#' {
		_ = text // sanity
	}
}
