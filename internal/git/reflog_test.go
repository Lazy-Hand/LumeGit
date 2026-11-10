package git

import (
	"context"
	"testing"
)

// TestReflog captures HEAD's action journal after a few commits.
func TestReflog(t *testing.T) {
	repo, _ := setupTempRepo(t)
	ctx := context.Background()

	entries, err := repo.Reflog(ctx, "HEAD", 50)
	if err != nil {
		t.Fatalf("Reflog: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no reflog entries")
	}
	// First entry is the most recent action; fixture's last action is a
	// commit (setupTempRepo makes commits).
	e := entries[0]
	if e.OID == "" || e.Short == "" {
		t.Errorf("entry missing oid: %+v", e)
	}
	if e.Action == "" {
		t.Errorf("entry missing action: %+v", e)
	}
}
