package git

import (
	"context"
	"testing"
)

// TestLFSTrackedParsing covers the pattern → path matcher.
func TestLFSTrackedParsing(t *testing.T) {
	tr := &LFSTracked{Patterns: []string{"*.psd", "assets/*", "exact.bin"}}
	cases := []struct {
		path string
		want bool
	}{
		{"img.psd", true},
		{"deep/img.psd", true},
		{"img.png", false},
		{"assets/big.bin", true},
		{"exact.bin", true},
		{"other/exact.bin", true},
		{"exact.bin.bak", false},
	}
	for _, tc := range cases {
		if got := tr.LFSPathTracked(tc.path); got != tc.want {
			t.Errorf("LFSPathTracked(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

// TestHasLFS exercises the version probe — depends on environment.
func TestHasLFS(t *testing.T) {
	repo, _ := setupTempRepo(t)
	_ = repo.HasLFS(context.Background()) // just shouldn't panic
}
