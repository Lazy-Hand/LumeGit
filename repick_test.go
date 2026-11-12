package main

// Reproduction for "连续点击多个文件后选不中" — the diff-load gen fence
// must let the LATEST pick win, and selection must stay responsive
// while an earlier diff is still in flight.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"lumegit/internal/git"
	"lumegit/internal/vcs"
)

func mkRepo(t *testing.T, files int) string {
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "a@b.c")
	run("config", "user.name", "t")
	for i := 0; i < files; i++ {
		os.WriteFile(filepath.Join(dir, string(rune('a'+i))+".txt"), []byte("x\n"), 0644)
	}
	run("add", "-A")
	run("commit", "-qm", "init")
	// modify each so they all appear as changed
	for i := 0; i < files; i++ {
		p := filepath.Join(dir, string(rune('a'+i))+".txt")
		os.WriteFile(p, []byte("x\ny\n"), 0644)
	}
	return dir
}

// testPublisher queues publish callbacks instead of running them inside
// the diff worker — same contract the real UI thread gives. drain()
// applies them on the test goroutine so field reads never race writes.
type testPublisher struct {
	mu    sync.Mutex
	queue []func()
}

func (p *testPublisher) publish(fn func()) {
	p.mu.Lock()
	p.queue = append(p.queue, fn)
	p.mu.Unlock()
}

func (p *testPublisher) drain() {
	p.mu.Lock()
	q := p.queue
	p.queue = nil
	p.mu.Unlock()
	for _, fn := range q {
		fn()
	}
}

// waitDiff settles the diff worker: drains publishes until diffLoading
// clears or the deadline passes.
func (p *testPublisher) waitDiff(m *commitModel) {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		p.drain()
		if !m.diffLoading {
			p.drain()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestRepickSelectsLatest clicks file a, then b while a's diff is in
// flight, and asserts b stays selected and its diff lands.
func TestRepickSelectsLatest(t *testing.T) {
	dir := mkRepo(t, 3)
	pub := &testPublisher{}
	m := &commitModel{
		RepoPath: dir,
		publish:  pub.publish,
	}
	m.Snap = vcs.WrapGit(mustSnap(git.NewRepo(dir), context.Background()))
	m.VCS = &vcs.Backend{Kind: vcs.KindGit, Git: git.NewRepo(dir), Root: dir}

	rows := rowsFor(m.Snap)
	if len(rows) < 3 {
		t.Fatalf("rows = %d", len(rows))
	}
	// Click a.txt then b.txt quickly — b must win.
	m.selectFile(rows[0].change)
	m.selectFile(rows[1].change)
	if m.selectedFile != rows[1].change.Path {
		t.Fatalf("selectedFile = %q, want %q", m.selectedFile, rows[1].change.Path)
	}
	pub.waitDiff(m)
	if m.diffLoading {
		t.Fatal("diffLoading stuck after repick")
	}
	if m.diff == nil || m.diff.Path != rows[1].change.Path {
		t.Fatalf("diff = %+v, want path %q", m.diff, rows[1].change.Path)
	}
}

// TestRepickBackAndForth clicks a → b → a rapidly; final pick must win
// and the shown diff must be a's, never a stale b.
func TestRepickBackAndForth(t *testing.T) {
	dir := mkRepo(t, 3)
	pub := &testPublisher{}
	m := &commitModel{
		RepoPath: dir,
		publish:  pub.publish,
	}
	m.Snap = vcs.WrapGit(mustSnap(git.NewRepo(dir), context.Background()))
	m.VCS = &vcs.Backend{Kind: vcs.KindGit, Git: git.NewRepo(dir), Root: dir}
	rows := rowsFor(m.Snap)

	m.selectFile(rows[0].change)
	m.selectFile(rows[1].change)
	m.selectFile(rows[0].change) // back to a
	if m.selectedFile != rows[0].change.Path {
		t.Fatalf("selectedFile = %q, want %q", m.selectedFile, rows[0].change.Path)
	}
	pub.waitDiff(m)
	if m.diff == nil || m.diff.Path != rows[0].change.Path {
		t.Fatalf("diff = %+v, want %q", m.diff, rows[0].change.Path)
	}
}

func TestDiffPreparationWaitCanBeCancelled(t *testing.T) {
	diffPrepareSlot <- struct{}{}
	defer func() { <-diffPrepareSlot }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := prepareDiffState(ctx, &git.DiffInfo{}); got != nil {
		t.Fatal("cancelled waiter returned a prepared state")
	}
}

func TestPrepareDiffState(t *testing.T) {
	d := &git.DiffInfo{Old: []byte("one\ntwo\n"), New: []byte("one\nchanged\n")}
	state := prepareDiffState(context.Background(), d)
	if state == nil || len(state.Hunks()) != 1 {
		t.Fatalf("prepared diff = %+v", state)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := prepareDiffState(ctx, d); got != nil {
		t.Fatal("cancelled diff preparation returned a state")
	}
}

func TestRepickClearsPreviousHunks(t *testing.T) {
	pub := &testPublisher{}
	m := &commitModel{
		RepoPath:     "/fake/repo",
		publish:      pub.publish,
		selectedFile: "a.txt",
		diffPath:     "a.txt",
		diff:         &git.DiffInfo{Path: "a.txt"},
		hunks:        []git.Hunk{{}, {}},
		hunkCheck:    []bool{false, true},
	}
	m.selectFile(git.FileChange{Path: "b.txt"})
	if len(m.hunks) != 0 || len(m.hunkCheck) != 0 {
		t.Fatal("previous file's hunk selection survived a repick")
	}
	if partial, _ := m.partialSelection([]string{"b.txt"}); partial != nil {
		t.Fatalf("old hunks were applied to new file: %+v", partial)
	}
	if m.diffCancel != nil {
		m.diffCancel()
	}
}

// TestNewDiffStateDropsInlineOnHugeDiff: past the line budget the state
// must build with word-level highlight off so the draw path never runs
// the per-line inline diff on a huge pick.
func TestNewDiffStateDropsInlineOnHugeDiff(t *testing.T) {
	line := strings.Repeat("x", 4) + "\n"
	small := &git.DiffInfo{Old: []byte(line), New: []byte("changed\n")}
	if s := newDiffState(small); s.Inline == editor.DiffInlineNone {
		t.Fatal("small diff lost its inline highlight")
	}
	big := &git.DiffInfo{
		Old: []byte(strings.Repeat(line, inlineMaxLines+10)),
		New: []byte(strings.Repeat("changed line\n", inlineMaxLines+10)),
	}
	if s := newDiffState(big); s.Inline != editor.DiffInlineNone {
		t.Fatal("huge diff kept inline highlight; draw path would stall")
	}
	if s := newDiffState(nil); s.Inline == editor.DiffInlineNone {
		t.Fatal("nil diff must stay inline-capable for empty selections")
	}
}

func TestDiffStateUsesRefreshedContent(t *testing.T) {
	a := &app{}
	a.commit.RepoPath = "/fake/repo"
	a.commit.Snap = &vcs.Snapshot{}
	a.commit.selectedFile = "a.txt"
	oldDiff := &git.DiffInfo{Path: "a.txt", Old: []byte("old\n"), New: []byte("first\n")}
	a.commit.diff = oldDiff
	a.commit.diffPrepared = prepareDiffState(context.Background(), oldDiff)
	tt := ui.NewTester(a.view, 1080, 720)
	first := a.diffState
	if first == nil || first != a.commit.diffPrepared {
		t.Fatal("first prepared diff state was not adopted")
	}
	newDiff := &git.DiffInfo{Path: "a.txt", Old: []byte("old\n"), New: []byte("second\n")}
	a.commit.diff = newDiff
	a.commit.diffPrepared = prepareDiffState(context.Background(), newDiff)
	tt.Frame()
	if a.diffState != a.commit.diffPrepared || a.diffState == first {
		t.Fatal("refresh of the same path retained stale diff state")
	}
}
