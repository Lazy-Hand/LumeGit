package git

import (
	"context"
	"testing"
)

func TestSubmodulesNone(t *testing.T) {
	dir := setupWorkRepo(t)
	repo := NewRepo(dir)
	subs, err := repo.Submodules(context.Background())
	if err != nil {
		t.Fatalf("Submodules: %v", err)
	}
	if len(subs) != 0 {
		t.Fatalf("subs = %+v", subs)
	}
}
