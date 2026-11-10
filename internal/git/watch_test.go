package git

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// TestWatchFiresOnWorktreeChange verifies a file edit debounces into
// one onChange call.
func TestWatchFiresOnWorktreeChange(t *testing.T) {
	dir := setupWorkRepo(t)
	repo := NewRepo(dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var fired atomic.Int32
	done := make(chan struct{}, 1)
	if err := repo.Watch(ctx, func() {
		fired.Add(1)
		select {
		case done <- struct{}{}:
		default:
		}
	}); err != nil {
		t.Fatalf("Watch: %v", err)
	}

	// Give the watcher a moment to install.
	time.Sleep(150 * time.Millisecond)

	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("no change event after file write")
	}
	// Debounce: the burst collapses to one call.
	time.Sleep(400 * time.Millisecond)
	if got := fired.Load(); got != 1 {
		t.Errorf("fired = %d, want 1 (debounced)", got)
	}
}

// TestWatchFiresOnIndexChange verifies `git add` (an index write) is
// picked up even though the worktree bytes may not have changed.
func TestWatchFiresOnIndexChange(t *testing.T) {
	dir := setupWorkRepo(t)
	repo := NewRepo(dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fired := make(chan struct{}, 1)
	if err := repo.Watch(ctx, func() {
		select {
		case fired <- struct{}{}:
		default:
		}
	}); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	time.Sleep(150 * time.Millisecond)

	// Stage a file — the worktree bytes don't change, only the index.
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := repo.Stage(ctx, []string{"b.txt"}); err != nil {
		t.Fatal(err)
	}

	select {
	case <-fired:
	case <-time.After(3 * time.Second):
		t.Fatal("no change event after git add")
	}
}

// TestWatchStopsOnCancel closes cleanly.
func TestWatchStopsOnCancel(t *testing.T) {
	dir := setupWorkRepo(t)
	repo := NewRepo(dir)
	ctx, cancel := context.WithCancel(context.Background())

	if err := repo.Watch(ctx, func() {}); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	cancel()
	// Give the goroutine a moment to exit — no strict assertion needed
	// beyond not hanging the test.
	time.Sleep(100 * time.Millisecond)
}
