package git

import (
	"context"
	"testing"
)

// TestRemotes lists, adds, edits, removes a remote on a real repo.
func TestRemotes(t *testing.T) {
	dir := setupWorkRepo(t)
	ctx := context.Background()
	repo := NewRepo(dir)

	rs, err := repo.Remotes(ctx)
	if err != nil {
		t.Fatalf("Remotes: %v", err)
	}
	if len(rs) != 0 {
		t.Fatalf("fresh repo remotes = %+v", rs)
	}

	if _, err := repo.AddRemote(ctx, "origin", "https://example.com/a.git"); err != nil {
		t.Fatalf("AddRemote: %v", err)
	}
	if _, err := repo.AddRemote(ctx, "upstream", "https://example.com/b.git"); err != nil {
		t.Fatalf("AddRemote upstream: %v", err)
	}
	rs, _ = repo.Remotes(ctx)
	if len(rs) != 2 {
		t.Fatalf("remotes = %+v", rs)
	}
	if rs[0].Name != "origin" || rs[0].FetchURL != "https://example.com/a.git" {
		t.Errorf("origin = %+v", rs[0])
	}

	if _, err := repo.SetRemoteURL(ctx, "origin", "https://example.com/c.git"); err != nil {
		t.Fatalf("SetRemoteURL: %v", err)
	}
	rs, _ = repo.Remotes(ctx)
	if rs[0].FetchURL != "https://example.com/c.git" {
		t.Errorf("after set-url = %+v", rs[0])
	}

	if _, err := repo.RemoveRemote(ctx, "upstream"); err != nil {
		t.Fatalf("RemoveRemote: %v", err)
	}
	rs, _ = repo.Remotes(ctx)
	if len(rs) != 1 || rs[0].Name != "origin" {
		t.Fatalf("after remove = %+v", rs)
	}
}
