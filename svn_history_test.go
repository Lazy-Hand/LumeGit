package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"lumegit/internal/svn"
)

// svnDo runs one svn command in dir, fails on error (mirrors svn_test).
func svnDo(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("svn", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("svn %v: %v\n%s", args, err, out)
	}
}

// TestSVNHistoryFileList drives svnSelectCommit against a real working
// copy: pick a revision, expect the file list to fill (no spinner).
func TestSVNHistoryFileList(t *testing.T) {
	if _, err := svn.DetectSVN(context.Background()); err != nil {
		t.Skip("svn not on PATH")
	}
	base := t.TempDir()
	repoDir := filepath.Join(base, "repo")
	if out, err := exec.Command("svnadmin", "create", repoDir).CombinedOutput(); err != nil {
		t.Skipf("svnadmin: %v\n%s", err, out)
	}
	repoURL := "file://" + repoDir
	// seed trunk + f.txt
	seed := filepath.Join(base, "seed")
	svnDo(t, base, "checkout", "-q", repoURL, seed)
	os.MkdirAll(filepath.Join(seed, "trunk"), 0755)
	svnDo(t, seed, "add", "trunk")
	os.WriteFile(filepath.Join(seed, "trunk", "f.txt"), []byte("a\n"), 0644)
	svnDo(t, seed, "add", "trunk/f.txt")
	svnDo(t, seed, "commit", "-q", "-m", "init")
	os.WriteFile(filepath.Join(seed, "trunk", "f.txt"), []byte("a\nb\n"), 0644)
	svnDo(t, seed, "commit", "-q", "-m", "second")

	wcDir := filepath.Join(base, "wc")
	svnDo(t, base, "checkout", "-q", repoURL+"/trunk", wcDir)

	wc, err := svn.Open(context.Background(), wcDir)
	if err != nil {
		t.Fatalf("svn.Open: %v", err)
	}
	h := &historyModel{}
	h.svnWC = wc
	h.svnRel = "^/trunk"
	// Publish on the test goroutine like a real updater would (queued
	// then drained) — running the closure inside the worker goroutine
	// makes test reads race worker writes.
	var mu sync.Mutex
	var queue []func()
	up := updater{update: func(fn func()) {
		mu.Lock()
		queue = append(queue, fn)
		mu.Unlock()
	}}
	drain := func() {
		mu.Lock()
		q := queue
		queue = nil
		mu.Unlock()
		for _, fn := range q {
			fn()
		}
	}

	// Pick r2 — should list f.txt.
	h.svnSelectCommit(wcDir, "r2", up)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		drain()
		if !h.filesLoading {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	drain() // catch the last publish if the flag cleared between drains
	if h.filesLoading {
		t.Fatalf("svnSelectCommit hung: filesLoading still true after 10s")
	}
	if len(h.files) == 0 {
		t.Fatalf("no files for r2: filesErr=%q", h.filesErr)
	}
	found := false
	for _, f := range h.files {
		if f.Path == "f.txt" {
			found = true
		}
	}
	if !found {
		t.Fatalf("files = %+v, want f.txt", h.files)
	}
}
