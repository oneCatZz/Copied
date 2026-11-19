package main

import (
	"context"
	"log"
	"os"
	"slices"
	"strconv"
	"time"

	"copied/locales"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// Updates come from the releases of updates.github in mygo.json, signed
// with the key whose public half is updates.publicKey. They are downloaded
// and staged in the background; the new version runs from the next launch.
// Copied lives in the menu bar and rarely quits, so it restarts into the
// update by itself once the user has left it alone for a while, in the
// background as at login: nothing shows, and the history and settings are
// on disk. The tray menu and the settings show the version and offer to
// check at once, and to restart into an update that is ready.
const (
	updateFirstCheck = 30 * time.Second // after launch, not to slow it down
	updateEvery      = 6 * time.Hour
	updateIdleCheck  = time.Minute
	updateIdle       = 5 * time.Minute // since the panel last hid
	updateQuiet      = 2 * time.Minute // since the clipboard last changed

	// backgroundArg starts the app without showing the panel, as at login.
	backgroundArg = "--background"
)

// updateState is what the updater does, or what its last check found:
// not checked yet, asking the release feed, no newer version, downloading
// updateVersion (updateProgress percent), the check or the download
// failed, or a build that cannot update itself. An update staged before
// shows as ready whatever a later check finds, but while a newer one
// downloads.
type updateState int

const (
	updateUnchecked updateState = iota
	updateChecking
	updateLatest
	updateDownloading
	updateFailed
	updateUnavailable
)

// startedInBackground reports whether the app should start hidden: at
// login, or restarting into an update.
func startedInBackground() bool {
	return mygo.App.WasOpenedAtLogin() || slices.Contains(os.Args[1:], backgroundArg)
}

// appVersion is the running version, as mygo.json gives it.
func appVersion() string {
	if v := mygo.App.Version(); v != "" {
		return v
	}
	return "dev"
}

// checkUpdates installs the new versions as they come out, and when the
// user asks (checkNow), until the app quits. Builds without updates, such
// as development builds and apps that cannot write where they are (a disk
// image, App Translocation), never check.
func (a *app) checkUpdates() {
	if !mygo.Updater.Enabled() {
		a.setUpdate(func() { a.updateState = updateUnavailable })
		return
	}
	timer := time.NewTimer(updateFirstCheck)
	for {
		select {
		case <-timer.C:
		case <-a.updateNow:
			timer.Stop()
		}
		a.installUpdate()
		timer.Reset(updateEvery)
	}
}

// checkNow checks for updates at once, unless a check or a download is
// under way. Main thread only.
func (a *app) checkNow() {
	switch a.updateState {
	case updateChecking, updateDownloading, updateUnavailable:
		return
	}
	select {
	case a.updateNow <- struct{}{}:
		a.updateState = updateChecking
		a.refreshUpdateUI()
	default:
	}
}

// installUpdate downloads and stages a version newer than the running one,
// unless it is staged already.
func (a *app) installUpdate() {
	a.setUpdate(func() { a.updateState = updateChecking })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	up, err := mygo.Updater.Check(ctx)
	if err != nil {
		log.Print("checking for updates: ", err)
		a.setUpdate(func() { a.updateState, a.updateChecked = updateFailed, time.Now() })
		return
	}
	var staged string
	mygo.RunOnMain(func() { staged = a.updateReady })
	if up == nil || staged == up.Version {
		a.setUpdate(func() { a.updateState, a.updateChecked = updateLatest, time.Now() })
		return
	}
	a.setUpdate(func() { a.updateState, a.updateVersion, a.updateProgress = updateDownloading, up.Version, 0 })
	last := -1
	err = up.Install(ctx, func(downloaded, total int64) {
		if total <= 0 {
			return
		}
		if p := int(downloaded * 100 / total); p != last {
			last = p
			a.setUpdate(func() { a.updateProgress = p })
		}
	})
	if err != nil {
		log.Print("installing the update: ", err)
		a.setUpdate(func() { a.updateState, a.updateChecked = updateFailed, time.Now() })
		return
	}
	log.Printf("update %s installed; it runs from the next launch", up.Version)
	var wait bool
	a.setUpdate(func() {
		a.updateState, a.updateChecked = updateLatest, time.Now()
		a.updateReady = up.Version
		wait = !a.updateWaiting
		a.updateWaiting = true
	})
	if wait {
		go a.restartWhenIdle()
	}
}

// setUpdate changes the update state on the main thread, and shows it.
func (a *app) setUpdate(change func()) {
	mygo.RunOnMain(func() {
		change()
		a.refreshUpdateUI()
	})
}

// refreshUpdateUI draws the tray menu and the panel again, where they show.
func (a *app) refreshUpdateUI() {
	a.updateTrayMenu()
	if a.panel != nil && !a.panel.IsDestroyed() && a.panel.IsVisible() {
		a.panel.Invalidate()
	}
}

// updateShowsReady reports whether an update is staged, to offer to
// restart into it. Main thread only.
func (a *app) updateShowsReady() bool {
	return a.updateReady != "" && a.updateState != updateDownloading
}

// trayUpdateRow is the row of the tray menu that shows the version and the
// update state, and checks at once when clicked; a staged update shows at
// the top of the menu instead.
func (a *app) trayUpdateRow(c *ui.Context, tx locales.Messages, ink ui.Color) {
	label, detail, action := tx.CheckForUpdates(), appVersion(), a.checkNow
	switch a.updateState {
	case updateChecking:
		label = tx.CheckingForUpdates()
	case updateLatest:
		label = tx.UpToDate()
	case updateDownloading:
		label, detail = tx.DownloadingUpdate(a.updateVersion), strconv.Itoa(a.updateProgress)+"%"
	case updateFailed:
		label = tx.RetryUpdateCheck()
	case updateUnavailable:
		label = tx.UpdatesUnavailable()
	}
	trayAction(c, "check-update", trayUpdate, label, detail, ink, action)
}

// updateStatus shows the version and the update state at the foot of the
// settings, with what the user can do about it, and reports whether that
// is restarting into a staged update, the dialog's main action then.
func (a *app) updateStatus(c *ui.Context) bool {
	t := c.Theme()
	tx := locales.In(c.Locale())
	ready := a.updateShowsReady()
	text, ink := "Copied "+appVersion(), t.TextMuted
	button, action := tx.CheckForUpdates(), a.checkNow
	switch {
	case ready:
		text, ink = tx.UpdateReady(a.updateReady), t.Text
		button, action = tx.RestartNow(), a.restartForUpdate
	case a.updateState == updateChecking:
		text, button = text+" · "+tx.CheckingForUpdates(), ""
	case a.updateState == updateLatest:
		text += " · " + tx.UpToDateAt(a.updateChecked.Format("15:04"))
	case a.updateState == updateDownloading:
		text += " · " + tx.DownloadingUpdate(a.updateVersion) + " · " + strconv.Itoa(a.updateProgress) + "%"
		button = ""
	case a.updateState == updateFailed:
		text, button = text+" · "+tx.UpdateFailed(), tx.Retry()
	case a.updateState == updateUnavailable:
		text, button = text+" · "+tx.UpdatesUnavailable(), ""
	}
	ui.Row(c).Grow(1).Basis(0).MinWidth(0).Gap(6).AlignItems(ui.Center).Children(func() {
		if ready {
			ui.Box(c).Size(7, 7).Shrink(0).Radius(3.5).Background(t.Accent)
		}
		ui.Text(c, text).FontSize(12).TextColor(ink).SingleLine()
	})
	if button != "" && dialogButton(c, "update-action", button, ready).Clicked() {
		action()
	}
	return ready
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
// nothing: no newer update downloads, the panel and the tray menu are
// closed and have been for a while, nothing was copied lately (a copy
// during the restart would not be recorded), the Stack, kept in memory
// only, is empty, and recording is not paused, which a restart would
// resume. Main thread only.
func (a *app) idleForUpdate() bool {
	if !a.updateShowsReady() || a.quitting {
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
