// LumeGit: a desktop git client in the IntelliJ IDEA spirit, built on
// MyGo's native UI. Entry point. The commit workspace lives in
// commitview.go (view) and commitws.go (state, background work).
package main

import (
	"context"
	"log"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"lumegit/internal/store"
)

// app is the application state (declared fully in commitview.go).

// shouldStopApp cancels background git work at quit. Assigned in main.
var appCancel context.CancelFunc

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	appCTX = ctx
	appCancel = cancel

	a := &app{}
	a.filesPaneW = 280
	a.histListW = 380
	a.histFilesW = 260
	a.shelfListW = 300
	a.shelfFileW = 240
	a.branchListW = 320
	// Recent/pinned repository list; a failed load just starts empty.
	a.commit.repos, _ = store.Load()

	mygo.App.WhenReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:     "LumeGit",
			Width:     1080,
			Height:    720,
			MinWidth:  720,
			MinHeight: 480,
			// Opens where the user left it last time.
			StateKey: "main",
			Content:  ui.View(a.view),
		})
		a.win = win
		a.winUpdate = win.Update
		// The model publishes through the same channel: background
		// git jobs post their results onto the main thread here.
		a.commit.publish = win.Update
		// Stop in-flight background git work when the window closes.
		off := win.OnClosed(func() {
			if appCancel != nil {
				appCancel()
			}
		})
		_ = off
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
