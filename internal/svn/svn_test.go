package svn

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// svnDo runs one svn command in dir and fails on error.
func svnDo(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("svn", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("svn %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// newRepo creates a file:// SVN repo with a trunk/branches/tags layout.
func newRepo(t *testing.T) (repoURL, wcDir string) {
	t.Helper()
	base := t.TempDir()
	repoDir := filepath.Join(base, "repo")
	if out, err := exec.Command("svnadmin", "create", repoDir).CombinedOutput(); err != nil {
		t.Skipf("svnadmin create: %v\n%s", err, out)
	}
	repoURL = "file://" + repoDir

	// Seed trunk + a file at r1.
	seed := filepath.Join(base, "seed")
	svnDo(t, base, "checkout", repoURL, seed)
	os.MkdirAll(filepath.Join(seed, "trunk"), 0755)
	os.MkdirAll(filepath.Join(seed, "branches"), 0755)
	os.MkdirAll(filepath.Join(seed, "tags"), 0755)
	svnDo(t, seed, "add", "trunk", "branches", "tags")
	os.WriteFile(filepath.Join(seed, "trunk", "f.txt"), []byte("a\n"), 0644)
	svnDo(t, seed, "add", "trunk/f.txt")
	svnDo(t, seed, "commit", "-m", "init")

	wcDir = filepath.Join(base, "wc")
	svnDo(t, base, "checkout", repoURL+"/trunk", wcDir)
	return repoURL, wcDir
}

func TestOpenAndInfo(t *testing.T) {
	if _, err := DetectSVN(context.Background()); err != nil {
		t.Skip("svn not on PATH")
	}
	repoURL, wcDir := newRepo(t)
	wc, err := Open(context.Background(), wcDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	info, err := wc.Info(context.Background())
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.Revision < 1 {
		t.Fatalf("rev = %d", info.Revision)
	}
	if info.Root != repoURL {
		t.Fatalf("root = %q, want %q", info.Root, repoURL)
	}
	if info.URL != repoURL+"/trunk" {
		t.Fatalf("url = %q", info.URL)
	}
	t.Logf("info: rev=%d url=%s rel=%s", info.Revision, info.URL, info.RelativeURL)
}

func TestStatusAndCommit(t *testing.T) {
	if _, err := DetectSVN(context.Background()); err != nil {
		t.Skip("svn not on PATH")
	}
	_, wcDir := newRepo(t)
	wc, _ := Open(context.Background(), wcDir)

	// Clean WC → nothing.
	items, err := wc.Status(context.Background(), false)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	var dirty int
	for _, it := range items {
		if it.Changed() {
			dirty++
		}
	}
	if dirty != 0 {
		t.Fatalf("clean WC has %d changed items: %+v", dirty, items)
	}

	// Modify f.txt, add g.txt, leave h.txt unversioned.
	os.WriteFile(filepath.Join(wcDir, "f.txt"), []byte("a\nb\n"), 0644)
	os.WriteFile(filepath.Join(wcDir, "g.txt"), []byte("g\n"), 0644)
	os.WriteFile(filepath.Join(wcDir, "h.txt"), []byte("h\n"), 0644)
	svnDo(t, wcDir, "add", "g.txt")

	items, _ = wc.Status(context.Background(), false)
	var mod, added, unver int
	for _, it := range items {
		switch it.Kind() {
		case KindModified:
			mod++
		case KindAdded:
			added++
		case KindUnversioned:
			unver++
		}
	}
	if mod != 1 || added != 1 || unver != 1 {
		t.Fatalf("status mod=%d added=%d unver=%d items=%+v", mod, added, unver, items)
	}

	// Commit the M + A, message via stdin.
	cr, err := wc.Commit(context.Background(), CommitOptions{
		Message: "second rev",
		Paths:   []string{"f.txt", "g.txt"},
	})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if cr.Rev != 2 {
		t.Fatalf("committed rev = %d, want 2", cr.Rev)
	}

	// After commit WC is clean of those paths again.
	items, _ = wc.Status(context.Background(), false)
	for _, it := range items {
		if it.Path == "f.txt" || it.Path == "g.txt" {
			t.Fatalf("path %s still listed after commit", it.Path)
		}
	}
}

func TestLogAndDiff(t *testing.T) {
	if _, err := DetectSVN(context.Background()); err != nil {
		t.Skip("svn not on PATH")
	}
	_, wcDir := newRepo(t)
	wc, _ := Open(context.Background(), wcDir)
	os.WriteFile(filepath.Join(wcDir, "f.txt"), []byte("a\nb\n"), 0644)
	svnDo(t, wcDir, "commit", "-q", "-m", "change f")

	revs, err := wc.Log(context.Background(), LogOptions{Verbose: true, Rev: "HEAD:1"})
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	// Two commits on trunk: r1 (init) and r2 (change f). The WC is at
	// r2 after the local commit, so log HEAD:1 yields both.
	if len(revs) != 2 {
		t.Fatalf("log entries = %d, want 2: %+v", len(revs), revs)
	}
	if revs[0].Rev != 2 || revs[1].Rev != 1 {
		t.Fatalf("rev order = %v,%v", revs[0].Rev, revs[1].Rev)
	}
	if revs[0].Message != "change f" {
		t.Fatalf("msg = %q", revs[0].Message)
	}
	var sawF bool
	for _, cp := range revs[0].ChangedPaths {
		if cp.Path == "/trunk/f.txt" && cp.Action == "M" {
			sawF = true
		}
	}
	if !sawF {
		t.Fatalf("r2 changed-paths missing f.txt M: %+v", revs[0].ChangedPaths)
	}

	// File list + content diff for r2. --summarize paths are
	// WC-relative, not repo-relative — f.txt not /trunk/f.txt.
	files, err := wc.DiffFilesRev(context.Background(), 2, "")
	if err != nil {
		t.Fatalf("DiffFilesRev: %v", err)
	}
	if len(files) != 1 || files[0].Path != "f.txt" {
		t.Fatalf("DiffFilesRev = %+v", files)
	}
	d, err := wc.FileDiffRev(context.Background(), "f.txt", 1, 2)
	if err != nil {
		t.Fatalf("FileDiffRev: %v", err)
	}
	if d.Old != "a\n" || d.New != "a\nb\n" {
		t.Fatalf("diff old=%q new=%q", d.Old, d.New)
	}
}

func TestUpdateAndIncoming(t *testing.T) {
	if _, err := DetectSVN(context.Background()); err != nil {
		t.Skip("svn not on PATH")
	}
	repoURL, wcDir := newRepo(t)
	// Second WC commits a change so the first is out-of-date.
	base := filepath.Dir(wcDir)
	wc2 := filepath.Join(base, "wc2")
	svnDo(t, base, "checkout", repoURL+"/trunk", wc2)
	os.WriteFile(filepath.Join(wc2, "f.txt"), []byte("a\nb\n"), 0644)
	os.WriteFile(filepath.Join(wc2, "new.txt"), []byte("n\n"), 0644)
	svnDo(t, wc2, "add", "new.txt")
	svnDo(t, wc2, "commit", "-m", "other wc")

	wc, _ := Open(context.Background(), wcDir)
	items, err := wc.Status(context.Background(), true) // -u
	if err != nil {
		t.Fatalf("Status(-u): %v", err)
	}
	var ood int
	for _, it := range items {
		if it.OOD {
			ood++
		}
	}
	if ood < 1 {
		t.Fatalf("expected out-of-date entries, items=%+v", items)
	}

	ur, err := wc.Update(context.Background(), UpdateOptions{})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if ur.Revision != 2 {
		t.Fatalf("updated to r%d, want 2", ur.Revision)
	}
	if len(ur.Updated) == 0 && len(ur.Added) == 0 && len(ur.Merged) == 0 {
		t.Fatalf("update brought nothing: %+v", ur)
	}
	if _, err := os.Stat(filepath.Join(wcDir, "new.txt")); err != nil {
		t.Fatalf("new.txt not pulled: %v", err)
	}
}

func TestBranchCopyAndSwitch(t *testing.T) {
	if _, err := DetectSVN(context.Background()); err != nil {
		t.Skip("svn not on PATH")
	}
	repoURL, wcDir := newRepo(t)
	wc, _ := Open(context.Background(), wcDir)

	// Branch via server-side copy trunk→branches/b1.
	if _, err := wc.Copy(context.Background(),
		repoURL+"/trunk", repoURL+"/branches/b1", "branch"); err != nil {
		t.Fatalf("Copy: %v", err)
	}
	names, err := wc.ListURL(context.Background(), repoURL+"/branches", "")
	if err != nil {
		t.Fatalf("ListURL: %v", err)
	}
	found := false
	for _, n := range names {
		if n == "b1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("branches = %v, want b1", names)
	}

	// Switch the WC to the branch.
	ur, err := wc.Switch(context.Background(), repoURL+"/branches/b1", UpdateOptions{})
	if err != nil {
		t.Fatalf("Switch: %v", err)
	}
	if ur.Revision != 2 {
		t.Fatalf("switched to r%d", ur.Revision)
	}
	info, _ := wc.Info(context.Background())
	if info.URL != repoURL+"/branches/b1" {
		t.Fatalf("url after switch = %q", info.URL)
	}
}

func TestRevertAndResolve(t *testing.T) {
	if _, err := DetectSVN(context.Background()); err != nil {
		t.Skip("svn not on PATH")
	}
	repoURL, wcDir := newRepo(t)
	base := filepath.Dir(wcDir)
	// wc2 bumps f to r2 so wc1's local edit conflicts on update.
	wc2 := filepath.Join(base, "wc2")
	svnDo(t, base, "checkout", repoURL+"/trunk", wc2)
	os.WriteFile(filepath.Join(wc2, "f.txt"), []byte("server\n"), 0644)
	svnDo(t, wc2, "commit", "-m", "server edit")

	wc, _ := Open(context.Background(), wcDir)
	os.WriteFile(filepath.Join(wcDir, "f.txt"), []byte("local\n"), 0644)
	ur, err := wc.Update(context.Background(), UpdateOptions{Accept: AcceptPostpone})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(ur.Conflicted) == 0 {
		t.Fatalf("expected conflict on f.txt: %+v", ur)
	}

	// Resolve with working (keep local), then revert the rest.
	if _, err := wc.Resolve(context.Background(), "f.txt", AcceptWorking); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if _, err := wc.Revert(context.Background(), "f.txt", false); err != nil {
		t.Fatalf("Revert: %v", err)
	}
	items, _ := wc.Status(context.Background(), false)
	for _, it := range items {
		if it.Path == "f.txt" && it.TextStatus == 'C' {
			t.Fatal("f.txt still conflicted after resolve+revert")
		}
	}
}

func TestMissingSVN(t *testing.T) {
	// Point Cmd at a nonexistent binary and check ErrSVNMissing surfaces.
	old := Cmd
	defer func() { Cmd = old }()
	Cmd = &commandRunner{bin: "svn-nonexistent-binary"}
	_, err := NewWC("/tmp").Run(context.Background(), "info")
	if err == nil {
		t.Fatal("expected ErrSVNMissing")
	}
	_ = time.Now()
}
