package git

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestFileHunks parses a real two-hunk diff.
func TestFileHunks(t *testing.T) {
	dir := setupWorkRepo(t)
	repo := NewRepo(dir)
	// Build a file with 20 lines then change two regions.
	var lines []string
	for i := 1; i <= 20; i++ {
		lines = append(lines, "line"+itoa(i))
	}
	writeTestFile(t, dir, "f.txt", strings.Join(lines, "\n")+"\n")
	runGit(t, dir, "add", "f.txt")
	runGit(t, dir, "commit", "-qm", "add f")
	lines[2] = "CHANGED-top"
	lines[17] = "CHANGED-bottom"
	writeTestFile(t, dir, "f.txt", strings.Join(lines, "\n")+"\n")

	hunks, err := repo.FileHunks(context.Background(), "f.txt")
	if err != nil {
		t.Fatalf("FileHunks: %v", err)
	}
	if len(hunks) != 2 {
		t.Fatalf("hunks = %+v", hunks)
	}
	t.Logf("h0: %s old=%d+%d new=%d+%d", hunks[0].Header, hunks[0].OldStart, hunks[0].OldCount, hunks[0].NewStart, hunks[0].NewCount)
	t.Logf("h1: %s", hunks[1].Header)
}

// TestStageHunksPartial stages only one of two hunks.
func TestStageHunksPartial(t *testing.T) {
	dir := setupWorkRepo(t)
	repo := NewRepo(dir)
	var lines []string
	for i := 1; i <= 20; i++ {
		lines = append(lines, "line"+itoa(i))
	}
	writeTestFile(t, dir, "f.txt", strings.Join(lines, "\n")+"\n")
	runGit(t, dir, "add", "f.txt")
	runGit(t, dir, "commit", "-qm", "add f")
	lines[2] = "CHANGED-top"
	lines[17] = "CHANGED-bottom"
	writeTestFile(t, dir, "f.txt", strings.Join(lines, "\n")+"\n")

	ctx := context.Background()
	if err := repo.StageHunks(ctx, "f.txt", []bool{true, false}); err != nil {
		t.Fatalf("StageHunks: %v", err)
	}
	// Staged diff should contain only the top change.
	out, _ := repo.Run(ctx, "diff", "--cached")
	if !strings.Contains(out.Output(), "CHANGED-top") {
		t.Errorf("staged diff missing top hunk:\n%s", out.Output())
	}
	if strings.Contains(out.Output(), "CHANGED-bottom") {
		t.Errorf("staged diff leaked bottom hunk:\n%s", out.Output())
	}
	// Worktree diff (index→work) should still show the bottom change.
	out2, _ := repo.Run(ctx, "diff")
	if !strings.Contains(out2.Output(), "CHANGED-bottom") {
		t.Errorf("worktree diff lost bottom hunk:\n%s", out2.Output())
	}
}

// TestCommitPartialSelection commits only chosen hunks via the real
// Commit path.
func TestCommitPartialSelection(t *testing.T) {
	dir := setupWorkRepo(t)
	repo := NewRepo(dir)
	var lines []string
	for i := 1; i <= 20; i++ {
		lines = append(lines, "line"+itoa(i))
	}
	writeTestFile(t, dir, "f.txt", strings.Join(lines, "\n")+"\n")
	runGit(t, dir, "add", "f.txt")
	runGit(t, dir, "commit", "-qm", "add f")
	lines[2] = "CHANGED-top"
	lines[17] = "CHANGED-bottom"
	writeTestFile(t, dir, "f.txt", strings.Join(lines, "\n")+"\n")

	out, err := repo.Commit(context.Background(), CommitRequest{
		Paths:   []string{"f.txt"},
		Partial: map[string][]bool{"f.txt": {true, false}},
		Message: "partial",
	})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if !out.ExitOK {
		t.Fatalf("commit failed: %+v", out)
	}
	// HEAD's f.txt should contain top change but not bottom.
	show, _ := repo.Run(context.Background(), "show", "HEAD:f.txt")
	if !strings.Contains(show.Output(), "CHANGED-top") {
		t.Error("committed file missing top hunk")
	}
	if strings.Contains(show.Output(), "CHANGED-bottom") {
		t.Error("committed file leaked bottom hunk")
	}
	// Worktree keeps both.
	wt, _ := os.ReadFile(filepath.Join(dir, "f.txt"))
	if !strings.Contains(string(wt), "CHANGED-bottom") {
		t.Error("worktree lost uncommitted hunk")
	}
}

func itoa(i int) string {
	return strconv.Itoa(i)
}
