package main

import (
	"testing"

	"lumegit/internal/git"
)

// fakeSnapshot builds a snapshot without touching a real repo.
func fakeSnapshot(t *testing.T) *git.Snapshot {
	t.Helper()
	return &git.Snapshot{
		Worktree: "/fake/repo",
		HeadName: "main",
		HeadOID:  "0123456789abcdef0123456789abcdef01234567",
		Files: []git.FileChange{
			{Path: "a.txt", Kind: git.KindModified, IndexState: ' ', WorktreeState: 'M'},
			{Path: "b.txt", Kind: git.KindAdded, IndexState: 'A', WorktreeState: ' '},
			{Path: "new.txt", Kind: git.KindUntracked, IndexState: ' ', WorktreeState: '?'},
		},
	}
}
