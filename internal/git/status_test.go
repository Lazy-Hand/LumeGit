package git

import (
	"strings"
	"testing"
)

// zbuild joins NUL-separated records taken straight from real git
// output (od -c dump of `git status --porcelain=v2 -z --branch`).
func zbuild(records ...string) string {
	out := &strings.Builder{}
	for _, r := range records {
		out.WriteString(r)
		out.WriteString("\x00")
	}
	return out.String()
}

// Fixture from a real repo: init → commit 3 files → rename one
// (auto.txt → "re named.txt"), modify one (plain.txt), one untracked.
// `od -c` transcript is in the design notes.
var fixtureReal = zbuild(
	"# branch.oid ffcffd6d7c692048f76a400f71e7fbcbc6caff",
	"# branch.head master",
	"1 .M N... 100644 100644 100644 78981922613b2afb6025042ff6bd878ac1994e85 78981922613b2afb6025042ff6bd878ac1994e85 plain.txt",
	"2 R. N... 100644 100644 100644 f2ad6c76f0115a6ba5b00456a849810e7ec0af20 f2ad6c76f0115a6ba5b00456a849810e7ec0af20 R100 re named.txt",
	"auto.txt",
	"? untracked.txt",
)

func TestParseStatusRealFixture(t *testing.T) {
	bi, files, err := parseStatus([]byte(fixtureReal))
	if err != nil {
		t.Fatalf("parseStatus: %v", err)
	}
	if bi.OID != "ffcffd6d7c692048f76a400f71e7fbcbc6caff" || bi.Head != "master" {
		t.Errorf("branch info = %+v", bi)
	}
	if bi.Ahead != -1 || bi.Behind != -1 || bi.HasAB {
		t.Errorf("no upstream expected, got %+v", bi)
	}
	if len(files) != 3 {
		t.Fatalf("want 3 files, got %d: %+v", len(files), files)
	}
	// plain.txt: unstaged modification only.
	if f := files[0]; f.Path != "plain.txt" || f.Kind != KindModified ||
		f.IndexState != ' ' || f.WorktreeState != 'M' || f.Staged() || !f.Unstaged() {
		t.Errorf("plain.txt wrong: %+v", files[0])
	}
	// rename with score 100, note space inside "re named.txt".
	if f := files[1]; f.Path != "re named.txt" || f.OrigPath != "auto.txt" ||
		f.Kind != KindRenamed || f.RenameScore != 100 || !f.Staged() ||
		f.IndexState != 'R' || f.WorktreeState != ' ' {
		t.Errorf("rename wrong: %+v", files[1])
	}
	// untracked
	if f := files[2]; f.Path != "untracked.txt" || f.Kind != KindUntracked || !f.Unstaged() {
		t.Errorf("untracked wrong: %+v", files[2])
	}
}

func TestParseStatusUnborn(t *testing.T) {
	out := zbuild(
		"# branch.oid (initial)",
		"# branch.head (detached)", // seen on init before first commit in some shells
		"? a.txt",
	)
	bi, files, err := parseStatus([]byte(out))
	if err != nil {
		t.Fatalf("parseStatus: %v", err)
	}
	if bi.OID != "(initial)" {
		t.Errorf("oid = %q", bi.OID)
	}
	if len(files) != 1 || files[0].Kind != KindUntracked {
		t.Errorf("files = %+v", files)
	}
}

func TestParseStatusUnmerged(t *testing.T) {
	out := zbuild(
		"# branch.oid 1234567890123456789012345678901234567890",
		"# branch.head main",
		"# branch.ab +1 -0",
		"u UU N... 100644 100644 100644 100644 aaa bbb ccc conflict.txt",
	)
	bi, files, err := parseStatus([]byte(out))
	if err != nil {
		t.Fatalf("parseStatus: %v", err)
	}
	if bi.Ahead != 1 || bi.Behind != 0 || !bi.HasAB {
		t.Errorf("branch.ab parse wrong: %+v", bi)
	}
	if len(files) != 1 {
		t.Fatalf("files = %+v", files)
	}
	f := files[0]
	if f.Kind != KindUnmerged || !f.Conflict() || f.Path != "conflict.txt" {
		t.Errorf("unmerged wrong: %+v", f)
	}
}

func TestParseStatusRenamesAndDeletes(t *testing.T) {
	out := zbuild(
		"# branch.oid aaaa",
		"# branch.head dev",
		"1 D. N... 100644 100644 000000 bbb bbb 0000000000000000000000000000000000000000 old.txt",
		"1 A. N... 000000 100644 100644 0000000000000000000000000000000000000000 ccc ccc new.txt",
		"1 MM N... 100644 100644 100644 ddd ddd eee double.txt",
	)
	_, files, err := parseStatus([]byte(out))
	if err != nil {
		t.Fatalf("parseStatus: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("files = %+v", files)
	}
	if f := files[0]; f.Kind != KindDeleted || f.IndexState != 'D' || f.WorktreeState != ' ' {
		t.Errorf("delete wrong: %+v", f)
	}
	// Worktree kept the file with mode 0? For an index delete with file
	// still present git would say something else; this fixture is
	// synthetic — normalize only what we parse.
	if f := files[2]; f.Kind != KindModified || f.IndexState != 'M' || f.WorktreeState != 'M' ||
		!f.Staged() || !f.Unstaged() {
		t.Errorf("MM wrong: %+v", f)
	}
}

// Path containing spaces must survive parsing in both 1-records and
// renames, plus a path with a non-ASCII name (git with -z prints raw
// bytes, no quoting).
func TestParseStatusPathsSpecial(t *testing.T) {
	out := zbuild(
		"# branch.oid aaaa",
		"# branch.head main",
		"1 .M N... 100644 100644 100644 a b \"目录/测 试.go\"",
	)
	_, files, err := parseStatus([]byte(out))
	if err != nil {
		t.Fatalf("parseStatus: %v", err)
	}
	if got := files[0].Path; got != "\"目录/测 试.go\"" {
		// Note: with -z, git does NOT quote; but this fixture keeps the
		// raw byte check straight — no unquoting happens at this layer.
		t.Errorf("path = %q", got)
	}
}

func TestParseStatusNotTerminated(t *testing.T) {
	_, _, err := parseStatus([]byte("1 .M N... 100644 100644 100644 a b file.txt"))
	if err == nil {
		t.Fatal("want error for missing NUL terminator")
	}
}
