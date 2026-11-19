package main

import (
	"context"
	"log"
	"os"
	"slices"
	"time"

	"github.com/egoist/mygo"
)

// Updates come from the releases of updates.github in mygo.json, signed
// with the key whose public half is updates.publicKey. They are downloaded
// and staged in the background; the new version runs from the next launch.
// Copied lives in the menu bar and rarely quits, so it restarts into the
// update by itself once the user has left it alone for a while, in the
// background as at login: nothing shows, and the history and settings are
// on disk. The tray menu offers to restart at once too.
const (
	updateFirstCheck = 30 * time.Second // after launch, not to slow it down
	updateEvery      = 6 * time.Hour
	updateIdleCheck  = time.Minute
	updateIdle       = 5 * time.Minute // since the panel last hid
	updateQuiet      = 2 * time.Minute // since the clipboard last changed

	// backgroundArg starts the app without showing the panel, as at login.
	backgroundArg = "--background"
)

// startedInBackground reports whether the app should start hidden: at
// login, or restarting into an update.
func startedInBackground() bool {
	return mygo.App.WasOpenedAtLogin() || slices.Contains(os.Args[1:], backgroundArg)
}

// checkUpdates installs the new versions as they come out, until the app
// quits. Builds without updates, such as development builds and apps that
// cannot write where they are (a disk image, App Translocation), never
// check.
func (a *app) checkUpdates() {
	if !mygo.Updater.Enabled() {
		return
	}
	time.Sleep(updateFirstCheck)
	for {
		a.installUpdate()
		time.Sleep(updateEvery)
	}
}

// installUpdate downloads and stages a version newer than the running one,
// unless it is staged already.
func (a *app) installUpdate() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	up, err := mygo.Updater.Check(ctx)
	if err != nil {
		log.Print("checking for updates: ", err)
		return
	}
	if up == nil {
		return
	}
	var staged string
	mygo.RunOnMain(func() { staged = a.updateReady })
	if staged == up.Version {
		return
	}
	if err := up.Install(ctx, nil); err != nil {
		log.Print("installing the update: ", err)
		return
	}
	log.Printf("update %s installed; it runs from the next launch", up.Version)
	var wait bool
	mygo.RunOnMain(func() {
		a.updateReady = up.Version
		wait = !a.updateWaiting
		a.updateWaiting = true
		a.updateTrayMenu()
	})
	if wait {
		go a.restartWhenIdle()
	}
}

// restartWhenIdle restarts into the staged update once the user is not
// using the app.
func (a *app) restartWhenIdle() {
	for range time.Tick(updateIdleCheck) {
		var done bool
		mygo.RunOnMain(func() {
			if done = a.idleForUpdate(); done {
				a.restartForUpdate()
			}
		})
		if done {
			return
		}
	}
}

// idleForUpdate reports whether restarting would go unnoticed and lose
// nothing: the panel and the tray menu are closed and have been for a
// while, nothing was copied lately (a copy during the restart would not be
// recorded), the Stack, kept in memory only, is empty, and recording is not
// paused, which a restart would resume. Main thread only.
func (a *app) idleForUpdate() bool {
	if a.updateReady == "" || a.quitting {
		return false
	}
	if a.panel != nil && !a.panel.IsDestroyed() && a.panel.IsVisible() {
		return false
	}
	if a.trayFlyout != nil || len(a.stack) > 0 || a.paused.Load() {
		return false
	}
	return time.Since(a.lastUsed) >= updateIdle &&
		time.Since(time.UnixMilli(a.lastCopy.Load())) >= updateQuiet
}

// restartForUpdate quits and starts the new version, in the background.
// On macOS the staged bundle replaces the app's once it has exited.
func (a *app) restartForUpdate() {
	if a.updateReady == "" {
		return
	}
	a.closeTrayMenu()
	mygo.App.Relaunch(backgroundArg)
}
