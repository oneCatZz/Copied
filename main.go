// Copied is a clipboard manager for Windows and macOS: it records what is
// copied, text, links, pictures and files, and a global shortcut shows the
// history in a panel centered on the screen, over every app, to
// search it and paste a clip into the app the user is in. It lives in the
// menu bar (the notification area on Windows).
package main

import (
	_ "embed"
	"log"
	"runtime"
	"runtime/debug"
	"slices"
	"sync/atomic"
	"time"

	"copied/locales"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// defaultShortcuts are tried in turn, after the one the user chose: the
// first one free shows the panel.
var defaultShortcuts = []string{"CmdOrCtrl+Shift+V", "Alt+Shift+V", "CmdOrCtrl+Alt+Shift+V"}

const (
	panelWidth     = 960 // DIPs, at most
	panelHeight    = 580 // DIPs, at most
	panelMargin    = 12  // around the panel, in the work area
	pollEvery      = 400 * time.Millisecond
	ownWrite       = time.Second // the watcher ignores the clipboard so long after the app wrote it
	panelIdleDelay = 2 * time.Second
)

// app holds what the panel shows. It is read and changed on the main
// thread, by the view and the callbacks of the panel, the tray and the
// shortcut; the watcher's goroutine reaches it through Window.Update.
type app struct {
	store    *Store
	panel    *mygo.Window
	tray     *mygo.Tray
	settings Settings
	shortcut string // the global shortcut registered, if any

	trayFlyout    *mygo.Window
	trayTarget    uintptr // the app to paste into, before the flyout takes focus
	trayFocus     ui.Handle
	trayRecording bool
	trayLogin     bool
	trayError     string

	shownAt        time.Time     // when the panel last showed, for its entrance
	since          time.Duration // since then, in the frame being built
	settingsOpen   bool          // the settings dialog shows
	recording      bool          // the dialog waits for the keys of a new shortcut
	recordingStack bool          // the recorder edits the stack shortcut
	recordingSource bool         // the recorder edits the window-local app selector
	recordError    string
	quitting       bool // the app quits: the panel closes rather than hides
	panelIdle      *time.Timer
	panelEpoch     uint64

	recordCapture string // temporary global shortcut used while recording

	clips         []*Clip
	filter        Filter
	query         string
	source        string
	sources       []string
	chosen        int
	picked        []int64 // clips picked to paste together, in the order picked
	pickRange     *pickRange
	scrollTo      bool    // the chosen card scrolls into view in the next frame
	input         ui.Handle
	thumbs        map[int64]*thumb // the pictures of the image clips in view, by clip
	cardMenuID    int64            // the clip whose context menu is open; zero closes it
	searchSession uint64           // a fresh input identity dismisses its built-in edit menu

	groups          []Group
	groupID         int64
	typeMenuOpen    bool
	typeHighlight   Filter
	typeMovePending bool
	groupMenuOpen   bool
	sourceMenuOpen  bool
	groupDialogOpen bool
	groupMode       string
	groupEditID     int64
	groupClipID     int64
	groupName       string
	groupError      string
	groupMembers    map[int64]*bool
	groupSession    uint64

	sourceQuery       string
	sourceSearch      ui.Handle
	sourceOpenPending bool
	sourceIcons       map[string]*sourceIcon
	sourceIconSlots   chan struct{}

	filterFlow filterFlow
	thumbSlots chan struct{} // bounds background thumbnail reads and decoding
	thumbClock uint64
	thumbLimit int

	stack     []int64          // clips to paste in turn, first to last
	stackKeys string           // the stack accelerator currently registered
	shortcuts shortcutRegistry // nil uses the native global shortcut registry

	pasteQueue chan pasteRequest // panel and stack pastes share the clipboard

	listScroll ui.ScrollState
	preview    clipPreview // the whole text of the chosen clip, as it loads
	codeWrap   bool        // the preview of code wraps its lines

	target   uintptr      // the window the panel was shown over (Windows)
	paused   atomic.Bool  // the watcher records nothing
	ownUntil atomic.Int64 // Unix milliseconds, see ownWrite

	lastUsed       time.Time     // when the panel last hid, see idleForUpdate
	lastCopy       atomic.Int64  // Unix milliseconds of the last clipboard change
	updateState    updateState   // what the updater does, or found
	updateVersion  string        // the version downloading
	updateProgress int           // its percentage downloaded
	updateChecked  time.Time     // when the last check ended
	updateReady    string        // the version installed, which runs from the next launch
	updateWaiting  bool          // restartWhenIdle runs
	updateNow      chan struct{} // asks checkUpdates to check at once
}

// reload lists the clips of the filter and the query again.
func (a *app) reload() {
	groups, err := a.store.Groups()
	if err != nil {
		log.Print("listing groups: ", err)
		return
	}
	a.groups = groups
	if a.groupID != 0 && !a.hasGroup(a.groupID) {
		a.groupID = 0
	}
	sources, err := a.store.Sources()
	if err != nil {
		log.Print("listing source apps: ", err)
		return
	}
	a.sources = sources
	if a.source != "" && !slices.Contains(sources, a.source) {
		a.source = ""
	}
	clips, err := a.store.list(a.filter, a.query, a.source, a.groupID)
	if err != nil {
		log.Print("listing clips: ", err)
		return
	}
	a.clips = clips
	a.chosen = max(0, min(a.chosen, len(clips)-1))
	a.pickRange = nil
	listed := make(map[int64]bool, len(clips))
	for _, c := range clips {
		listed[c.ID] = true
	}
	for id, th := range a.thumbs {
		if !listed[id] {
			th.cancel()
			delete(a.thumbs, id)
		}
	}
	if !listed[a.cardMenuID] {
		a.cardMenuID = 0
	}
	a.picked = slices.DeleteFunc(a.picked, func(id int64) bool { return !listed[id] })
}

func (a *app) choose(i int) {
	a.chosen = max(0, min(i, len(a.clips)-1))
	a.scrollTo = true
}

func (a *app) setFilter(f Filter, _ time.Time) {
	a.cardMenuID = 0
	a.typeMenuOpen = false
	a.sourceMenuOpen = false
	a.filter = f
	a.chosen = 0
	a.scrollTo = true
	a.reload()
}

// copy puts a clip on the clipboard and moves it to the front of the
// history.
func (a *app) copy(i int, plain bool) bool {
	if i < 0 || i >= len(a.clips) {
		return false
	}
	if ids := a.batchIDs(i); ids != nil {
		return a.copyBatch(ids, plain)
	}
	return a.copyClip(a.clips[i].ID, plain)
}

// copyClip puts the clip with an ID on the clipboard and moves it to the
// front of the history.
func (a *app) copyClip(id int64, plain bool) bool {
	clip, err := a.store.Get(id)
	if err != nil || clip == nil {
		log.Print("reading clip: ", err)
		return false
	}
	a.ownUntil.Store(time.Now().Add(ownWrite).UnixMilli())
	if err := writeClipboard(clip, plain); err != nil {
		log.Print("writing the clipboard: ", err)
		return false
	}
	if err := a.store.Touch(id); err != nil {
		log.Print(err)
	}
	return true
}

// paste queues a single clip or a batch for the app the user was in.
func (a *app) paste(i int, plain bool) {
	if i < 0 || i >= len(a.clips) {
		return
	}
	target := a.target
	var writes []func() bool
	if ids := a.batchIDs(i); ids != nil {
		writes = a.batchPasteWrites(ids, target, plain)
	} else {
		id, kind := a.clips[i].ID, a.clips[i].Kind
		writes = []func() bool{func() bool { return a.copyForPaste(id, kind, target, plain) }}
	}
	a.queuePaste(pasteRequest{target: target, writes: writes, panel: a.panel, epoch: a.panelEpoch})
}

func (a *app) copyForPaste(id int64, kind Kind, target uintptr, plain bool) bool {
	if kind == KindImage && (plain || terminalWindow(target)) && a.copyImagePath(id, plain) {
		return true
	}
	return a.copyClip(id, plain)
}

// copyOnly copies a clip and hides the panel, to paste by hand.
func (a *app) copyOnly(i int) {
	if a.copy(i, false) {
		a.panel.Hide()
	}
}

func (a *app) remove(i int) {
	if i < 0 || i >= len(a.clips) {
		return
	}
	id := a.clips[i].ID
	if err := a.store.Delete(id); err != nil {
		log.Print(err)
	}
	if n := slices.Index(a.stack, id); n >= 0 {
		a.stack = slices.Delete(a.stack, n, n+1)
		a.syncStackShortcut()
	}
	a.reload()
}

func (a *app) togglePin(i int) {
	if i < 0 || i >= len(a.clips) {
		return
	}
	if err := a.store.SetPinned(a.clips[i].ID, !a.clips[i].Pinned); err != nil {
		log.Print(err)
	}
	a.reload()
}

// watch records what is copied, until the app quits. It asks the system
// whether the clipboard changed, which costs nothing, and reads it only
// then; where the system does not tell, it reads it every time and keeps
// what differs from the last.
func (a *app) watch() {
	var lastSeq int64 = -1
	var lastHash string
	first := true
	for range time.Tick(pollEvery) {
		var seq int64
		var known bool
		mygo.RunOnMain(func() { seq, known = clipboardSeq() })
		if known && seq == lastSeq {
			continue
		}
		lastSeq = seq
		a.lastCopy.Store(time.Now().UnixMilli())
		skip := first || a.paused.Load() || time.Now().UnixMilli() < a.ownUntil.Load()
		first = false
		if known && skip {
			continue
		}
		clip := readClipboard()
		if clip == nil {
			lastHash = ""
			continue
		}
		if !known && clip.Hash == lastHash {
			continue
		}
		lastHash = clip.Hash
		if skip {
			continue
		}
		source, sourcePath := clipboardSource()
		clip.Source = source
		if err := a.store.saveSourcePath(source, sourcePath); err != nil {
			log.Print("recording source app: ", err)
		}
		large := len(clip.Data) > 0
		if err := a.store.Add(clip); err != nil {
			log.Print("recording a clip: ", err)
			continue
		}
		mygo.RunOnMain(func() {
			if a.panel != nil && a.panel.IsVisible() {
				a.panel.Update(a.reload)
			}
		})
		if clip = nil; large {
			debug.FreeOSMemory() // a picture decoded takes many times its file
		}
	}
}

// show shows the panel centered on the display with the pointer, with the
// whole history and the newest clip chosen.
func (a *app) show() {
	a.cardMenuID = 0
	a.searchSession++
	a.cancelPanelRelease()
	if a.panel == nil || a.panel.IsDestroyed() {
		a.newPanel()
	}
	target := frontWindow()
	if a.trayFlyout != nil {
		target = a.trayTarget
		a.closeTrayMenu()
	}
	a.target = target
	d := mygo.Screen.DisplayNearestPoint(mygo.Screen.CursorScreenPoint())
	width := min(panelWidth, d.WorkArea.Width-2*panelMargin)
	height := min(panelHeight, d.WorkArea.Height-2*panelMargin)
	a.panel.SetSize(width, height)
	a.panel.SetPosition(d.WorkArea.X+(d.WorkArea.Width-width)/2, d.WorkArea.Y+(d.WorkArea.Height-height)/2)
	a.query, a.filter, a.chosen, a.scrollTo = "", FilterAll, 0, true
	a.source = ""
	a.picked = nil
	// Reveal alone can leave the first row flush against the viewport edge.
	// Reset its offset so the padding returns too.
	a.listScroll = ui.ScrollState{Handle: a.listScroll.Handle}
	a.preview.reset()
	a.groupID = 0
	a.dismissGroupUI()
	a.filterFlow = filterFlow{}
	a.shownAt = time.Now()
	a.reload()
	a.input.Focus()
	a.panel.Show()
}

func (a *app) toggle() {
	if a.panel != nil && a.panel.IsVisible() {
		a.panel.Hide()
	} else {
		a.show()
	}
}

func (a *app) ready() {
	var err error
	if a.store, err = openStore(); err != nil {
		log.Print("opening the history: ", err)
		mygo.App.Quit()
		return
	}
	a.thumbs = map[int64]*thumb{}
	a.thumbSlots = make(chan struct{}, 2)
	a.settings = loadSettings()
	a.configureLanguage()
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) {
		a.quitting = true
		a.cancelPanelRelease()
	})

	a.registerShortcut()
	a.setUpTray()
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) {
		a.clearThumbs()
		a.clearSourceIcons()
		if a.tray != nil {
			a.tray.Destroy()
		}
		a.store.Close()
	})
	go a.watch()
	a.updateNow = make(chan struct{}, 1)
	go a.checkUpdates()
	if !startedInBackground() {
		a.show() // started by the user: show what it does
	}
}

func (a *app) newPanel() {
	panel := mygo.NewWindow(mygo.WindowOptions{
		Title:         "Copied",
		Panel:         true,
		Hidden:        true,
		Frameless:     true,
		Transparent:   true,
		Vibrancy:      mygo.VibrancyGlass,
		CornerRadius:  18,
		Width:         panelWidth,
		Height:        panelHeight,
		DisableResize: true,
		Content:       ui.View(a.view),
	})
	a.panel = panel
	// Hide on Escape, Cmd-W or Alt-F4, and when the user goes elsewhere.
	// A short grace period keeps quick reopenings warm. Quitting destroys it.
	// Quit asks every window to close, and a close prevented cancels it.
	panel.OnClose(func(e *mygo.CloseEvent) {
		if a.quitting {
			return
		}
		e.PreventDefault()
		panel.Hide()
	})
	panel.OnBlur(panel.Hide)
	panel.OnHide(func() { a.panelHidden(panel) })
}

func (a *app) cancelPanelRelease() {
	a.panelEpoch++
	if a.panelIdle != nil {
		a.panelIdle.Stop()
		a.panelIdle = nil
	}
}

func (a *app) panelHidden(panel *mygo.Window) {
	if a.panel != panel {
		return
	}
	restoreFront()
	a.lastUsed = time.Now()
	if a.recording {
		a.stopRecording("")
	}
	a.settingsOpen = false
	a.dismissGroupUI()
	a.cardMenuID = 0
	a.searchSession++
	a.recordError = ""
	a.clips, a.picked = nil, nil
	a.preview.reset()
	a.clearThumbs()
	a.clearSourceIcons()
	a.cancelPanelRelease()
	epoch := a.panelEpoch
	a.panelIdle = time.AfterFunc(panelIdleDelay, func() {
		mygo.RunOnMain(func() {
			if a.panelEpoch == epoch && a.panel == panel && !panel.IsVisible() && !a.quitting {
				a.panelIdle = nil
				panel.Destroy() // Release the UI runtime and its GPU caches too.
				go debug.FreeOSMemory()
			}
		})
	})
	go debug.FreeOSMemory()
}

// registerShortcut registers the shortcut the user chose, or the first
// default that is free.
func (a *app) registerShortcut() {
	candidates := defaultShortcuts
	if a.settings.Shortcut != "" {
		candidates = append([]string{a.settings.Shortcut}, candidates...)
	}
	a.shortcut = ""
	for _, s := range candidates {
		if sameShortcut(s, a.stackShortcut()) {
			continue
		}
		if err := a.globalShortcuts().Register(s, a.toggle); err == nil {
			a.shortcut = s
			return
		} else {
			log.Printf("shortcut %s: %v", s, err)
		}
	}
}

// startRecording waits for the keys of a new shortcut. The current one
// is let go meanwhile, so that pressing it again keeps it rather than
// hiding the panel.
func (a *app) startRecording(stack bool) {
	if a.recording {
		a.stopRecording("")
	}
	a.recording, a.recordError = true, ""
	a.recordingStack = stack
	a.recordingSource = false
	if a.shortcut != "" {
		a.globalShortcuts().Unregister(a.shortcut)
	}
	a.syncStackShortcut()
	if runtime.GOOS == "darwin" {
		// AppKit takes Cmd+` for window cycling before the recorder sees it.
		// Reserve it while recording, using the same API as the saved shortcut.
		const shortcut = "CmdOrCtrl+`"
		if err := a.globalShortcuts().Register(shortcut, func() {
			if a.recording {
				a.stopRecording(shortcut)
				if a.panel != nil {
					a.panel.Invalidate()
				}
			}
		}); err == nil {
			a.recordCapture = shortcut
		}
	}
}

// stopRecording takes the shortcut recorded, or keeps the current one
// when there is none or the system refuses it.
func (a *app) stopRecording(shortcut string) {
	stack := a.recordingStack
	source := a.recordingSource
	a.recording = false
	a.recordingStack = false
	a.recordingSource = false
	defer a.resumeShortcuts()
	if a.recordCapture != "" {
		a.globalShortcuts().Unregister(a.recordCapture)
		a.recordCapture = ""
	}
	if shortcut != "" {
		if source {
			if a.changeSourceShortcut(shortcut) {
				a.saveSettings()
			}
		} else if a.changeShortcut(shortcut, stack) {
			a.saveSettings()
		}
	}
}

// resetShortcut goes back to the default shortcut.
func (a *app) resetShortcut() {
	if a.recording {
		a.stopRecording("")
	}
	for _, shortcut := range defaultShortcuts {
		if a.changeShortcut(shortcut, false) {
			a.settings.Shortcut = ""
			a.saveSettings()
			return
		}
	}
}

func (a *app) saveSettings() {
	if err := a.settings.save(); err != nil {
		log.Print("saving the settings: ", err)
	}
	a.updateTrayMenu()
}

// openSettings shows the panel with its settings dialog.
func (a *app) openSettings() {
	if a.panel == nil || !a.panel.IsVisible() {
		a.show()
	}
	a.dismissGroupUI()
	a.settingsOpen = true
	a.panel.Invalidate()
}

func (a *app) setUpTray() {
	icon, template := trayIconPNG, false
	if runtime.GOOS == "darwin" {
		icon, template = trayTemplatePNG, true
	}
	var err error
	a.tray, err = mygo.NewTray(mygo.TrayOptions{ID: "copied", Icon: icon, IconIsTemplate: template, ToolTip: "Copied"})
	if err != nil {
		log.Print("no tray icon: ", err)
		a.tray = nil
		return
	}
	a.updateTrayMenu()
	if runtime.GOOS != "linux" {
		a.tray.OnClick(a.toggleTrayMenu)
		a.tray.OnRightClick(a.toggleTrayMenu)
	}
}

// updateTrayMenu refreshes the flyout, or the native fallback on Linux.
func (a *app) updateTrayMenu() {
	if a.tray == nil {
		return
	}
	if runtime.GOOS != "linux" {
		if a.trayFlyout != nil {
			a.trayFlyout.Invalidate()
		}
		return
	}
	tx := locales.In(mygo.App.Locale())
	a.tray.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
		{Label: tx.OpenPanel(), Click: func(*mygo.MenuItem, *mygo.Window) { a.show() }},
		{Label: tx.Settings(), Click: func(*mygo.MenuItem, *mygo.Window) { a.openSettings() }},
		mygo.Separator(),
		{Label: tx.PauseRecording(), Type: mygo.MenuItemCheckbox, Checked: a.paused.Load(), Click: func(it *mygo.MenuItem, _ *mygo.Window) {
			a.paused.Store(it.Checked)
		}},
		{Label: tx.StartAtLogin(), Type: mygo.MenuItemCheckbox, Checked: mygo.App.OpenAtLogin(),
			Click: func(it *mygo.MenuItem, _ *mygo.Window) {
				if err := mygo.App.SetOpenAtLogin(it.Checked); err != nil {
					log.Print(err)
				}
			}},
		mygo.Separator(),
		// The pinned clips stay.
		{Label: tx.ClearHistory(), Click: func(*mygo.MenuItem, *mygo.Window) { a.clearHistory() }},
		mygo.Separator(),
		{Label: tx.Quit(), Click: func(*mygo.MenuItem, *mygo.Window) { mygo.App.Quit() }},
	}))
}

// trayIconPNG keeps the app's colored tile in the Windows notification area.
//
//go:embed resources/tray.png
var trayIconPNG []byte

// trayTemplatePNG lets macOS tint the outline to match its menu bar.
//
//go:embed resources/tray-template.png
var trayTemplatePNG []byte

func main() {
	if !mygo.App.RequestSingleInstanceLock() {
		return // the running app shows its panel (OnSecondInstance)
	}
	a := &app{}
	mygo.App.SetName("Copied")
	// No Dock icon or app switcher entry (macOS).
	mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	mygo.App.OnWindowAllClosed(func() {}) // keep running without windows
	mygo.App.OnSecondInstance(func([]string, string) {
		if a.store != nil {
			a.show()
		}
	})
	mygo.App.WhenReady(a.ready)
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
