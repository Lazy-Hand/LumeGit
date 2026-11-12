package main

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"lumegit/internal/git"
	"lumegit/internal/store"
	"lumegit/internal/vcs"
)

func benchmarkFileModel(n int) *commitModel {
	m := &commitModel{
		Snap:  &vcs.Snapshot{Files: make([]git.FileChange, 0, n)},
		lists: &store.Changelists{Default: store.DefaultName},
	}
	list := &store.Changelist{Name: "任务"}
	for i := 0; i < n; i++ {
		path := fmt.Sprintf("src/file%05d.go", n-i)
		m.Snap.Files = append(m.Snap.Files, git.FileChange{Path: path, Kind: git.KindModified, WorktreeState: 'M'})
		list.Paths = append(list.Paths, path)
	}
	m.lists.Lists = append(m.lists.Lists, list)
	return m
}

func BenchmarkFileGroups(b *testing.B) {
	m := benchmarkFileModel(2000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.groups()
	}
}

func BenchmarkFilePaneFrame(b *testing.B) {
	a := &app{commit: *benchmarkFileModel(2000)}
	a.commit.RepoPath = "/fake/repo"
	tt := ui.NewTester(a.view, 1080, 720)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tt.Frame()
	}
}

// BenchmarkDiffFirstFrame isolates the frame that receives a different
// large diff, with and without the worker's precomputed editor state.
func BenchmarkDiffFirstFrame(b *testing.B) {
	old := strings.Repeat("one line of source code\n", 10000)
	newText := old + "changed line\n"
	for _, prepared := range []bool{false, true} {
		name := "ui-build"
		if prepared {
			name = "worker-prepared"
		}
		b.Run(name, func(b *testing.B) {
			a := &app{}
			a.commit.RepoPath = "/fake/repo"
			a.commit.Snap = &vcs.Snapshot{}
			a.commit.selectedFile = "file.go"
			tt := ui.NewTester(a.view, 1080, 720)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				d := &git.DiffInfo{Path: "file.go", Old: []byte(old), New: []byte(newText)}
				var state *editor.DiffState
				if prepared {
					state = prepareDiffState(context.Background(), d)
				}
				a.commit.diff, a.commit.diffPrepared = d, state
				b.StartTimer()
				tt.Frame()
			}
		})
	}
}
