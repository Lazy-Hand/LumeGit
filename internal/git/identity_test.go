package git

import (
	"context"
	"testing"
)

// TestIdentity covers the repo-scope read + write of user.name/email,
// including the empty-value unsets that let git fall back to global.
func TestIdentity(t *testing.T) {
	dir := setupWorkRepo(t) // seeds local user.name=tester, email=t@t
	ctx := context.Background()
	repo := NewRepo(dir)

	id, err := repo.Identity(ctx, true)
	if err != nil {
		t.Fatalf("Identity local: %v", err)
	}
	if id.Name != "tester" || id.Email != "t@t" {
		t.Fatalf("local identity = %+v", id)
	}

	// Effective (non-local) resolution sees the same local values.
	if eff, _ := repo.Identity(ctx, false); eff.Name != "tester" {
		t.Fatalf("effective identity = %+v", eff)
	}

	// Write new pair.
	if err := repo.SetIdentity(ctx, Identity{Name: "dev", Email: "dev@x.com"}); err != nil {
		t.Fatalf("SetIdentity: %v", err)
	}
	id, _ = repo.Identity(ctx, true)
	if id.Name != "dev" || id.Email != "dev@x.com" {
		t.Fatalf("after set = %+v", id)
	}

	// Empty name unsets it (falls back); email stays.
	if err := repo.SetIdentity(ctx, Identity{Name: "", Email: "dev@x.com"}); err != nil {
		t.Fatalf("SetIdentity unset: %v", err)
	}
	local, _ := repo.Identity(ctx, true)
	if local.Name != "" || local.Email != "dev@x.com" {
		t.Fatalf("after unset = %+v", local)
	}
}
