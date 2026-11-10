package git

import (
	"context"
	"os/exec"
	"testing"
)

// TestTags covers list, create (light + annotated), delete.
func TestTags(t *testing.T) {
	dir := setupWorkRepo(t)
	ctx := context.Background()
	repo := NewRepo(dir)

	tags, err := repo.Tags(ctx)
	if err != nil {
		t.Fatalf("Tags: %v", err)
	}
	if len(tags) != 0 {
		t.Fatalf("fresh repo tags = %+v", tags)
	}

	// Lightweight at HEAD.
	if _, err := repo.CreateTag(ctx, "v1.0", "", ""); err != nil {
		t.Fatalf("CreateTag light: %v", err)
	}
	// Annotated at the same target.
	if _, err := repo.CreateTag(ctx, "v2.0", "", "second release"); err != nil {
		t.Fatalf("CreateTag annotated: %v", err)
	}

	tags, err = repo.Tags(ctx)
	if err != nil {
		t.Fatalf("Tags after create: %v", err)
	}
	if len(tags) != 2 {
		t.Fatalf("tags = %+v", tags)
	}
	var light, ann *Tag
	for i := range tags {
		switch tags[i].Name {
		case "v1.0":
			light = &tags[i]
		case "v2.0":
			ann = &tags[i]
		}
	}
	if light == nil || ann == nil {
		t.Fatalf("missing tags in %+v", tags)
	}
	if light.Annotated {
		t.Errorf("v1.0 should be lightweight: %+v", light)
	}
	if !ann.Annotated || ann.Message != "second release" {
		t.Errorf("v2.0 = %+v", ann)
	}

	// Tag on a specific target: HEAD.
	head, err := repo.Run(ctx, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if light.OID == "" {
		t.Errorf("light OID empty; head=%s", head.Output())
	}

	if _, err := repo.DeleteTag(ctx, "v1.0"); err != nil {
		t.Fatalf("DeleteTag: %v", err)
	}
	tags, _ = repo.Tags(ctx)
	if len(tags) != 1 || tags[0].Name != "v2.0" {
		t.Fatalf("after delete tags = %+v", tags)
	}
}

// TestPushTag pushes a tag to a bare remote and verifies it there.
func TestPushTag(t *testing.T) {
	remote, local := setupRemotePair(t)
	ctx := context.Background()
	repo := NewRepo(local)

	if _, err := repo.CreateTag(ctx, "v9", "", ""); err != nil {
		t.Fatalf("CreateTag: %v", err)
	}
	if _, err := repo.PushTag(ctx, "origin", "v9"); err != nil {
		t.Fatalf("PushTag: %v", err)
	}
	// Remote should now carry the ref.
	cmd := exec.Command("git", "--git-dir", remote, "show-ref", "refs/tags/v9")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("remote lacks tag: %v: %s", err, out)
	}
}
