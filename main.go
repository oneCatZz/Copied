// Copied is a clipboard manager for Windows and macOS: it records what is
// copied, text, links, pictures and files, and a global shortcut shows the
// history in a panel along the bottom of the screen, over every app, to
// search it and paste a clip into the app the user is in. It lives in the
// menu bar (the notification area on Windows).
package main

import (
	_ "embed"
	"log"
	"runtime"
	"runtime/debug"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// defaultShortcuts are tried in turn, after the one the user chose: the
// first one free shows the panel.
var defaultShortcuts = []string{"CmdOrCtrl+Shift+V", "Alt+Shift+V", "CmdOrCtrl+Alt+Shift+V"}

const (
	panelHeight = 372 // DIPs
	panelMargin = 12  // around the panel, in the work area
	pollEvery   = 400 * time.Millisecond
	ownWrite    = time.Second // the watcher ignores the clipboard so long after the app wrote it
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

	shownAt      time.Time     // when the panel last showed, for its entrance
	since        time.Duration // since then, in the frame being built
	settingsOpen bool          // the settings dialog shows
	recording    bool          // the dialog waits for the keys of a new shortcut
	recordError  string
	quitting     bool // the app quits: the panel closes rather than hides

	clips    []*Clip
	filter   Filter
	query    string
	chosen   int
	scrollTo bool // the chosen card scrolls into view in the next frame
	input    ui.Handle
	thumbs   map[int64]*thumb // the pictures of the image clips in view, by clip

	target   uintptr      // the window the panel was shown over (Windows)
	paused   atomic.Bool  // the watcher records nothing
	ownUntil atomic.Int64 // Unix milliseconds, see ownWrite
}

// reload lists the clips of the filter and the query again.
func (a *app) reload() {
	clips, err := a.store.List(a.filter, a.query)
	if err != nil {
		log.Print("listing clips: ", err)
		return
	}
	a.clips = clips
	a.chosen = max(0, min(a.chosen, len(clips)-1))
	listed := make(map[int64]bool, len(clips))
	for _, c := range clips {
		listed[c.ID] = true
	}
	for id := range a.thumbs {
		if !listed[id] {
			delete(a.thumbs, id)
		}
	}
}

func (a *app) choose(i int) {
	a.chosen = max(0, min(i, len(a.clips)-1))
	a.scrollTo = true
}

func (a *app) setFilter(f Filter) {
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
	id := a.clips[i].ID
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

// paste copies a clip, hides the panel and pastes into the app the user
// was in.
func (a *app) paste(i int, plain bool) {
	if !a.copy(i, plain) {
		return
	}
	target := a.target
	a.panel.Hide()
	go pasteInto(target)
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
	if err := a.store.Delete(a.clips[i].ID); err != nil {
		log.Print(err)
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
		large := len(clip.Data) > 0
		if err := a.store.Add(clip); err != nil {
			log.Print("recording a clip: ", err)
			continue
		}
		if a.panel.IsVisible() {
			a.panel.Update(a.reload)
		}
		if clip = nil; large {
			debug.FreeOSMemory() // a picture decoded takes many times its file
		}
	}
}

// show shows the panel along the bottom of the display with the pointer,
// with the whole history and the newest clip chosen.
func (a *app) show() {
	target := frontWindow()
	if a.trayFlyout != nil {
		target = a.trayTarget
		a.closeTrayMenu()
	}
	a.target = target
	d := mygo.Screen.DisplayNearestPoint(mygo.Screen.CursorScreenPoint())
	width := d.WorkArea.Width - 2*panelMargin
	a.panel.SetSize(width, panelHeight)
	a.panel.SetPosition(d.WorkArea.X+panelMargin, d.WorkArea.Y+d.WorkArea.Height-panelHeight-panelMargin)
	a.query, a.filter, a.chosen, a.scrollTo = "", FilterAll, 0, true
	a.shownAt = time.Now()
	a.reload()
	a.input.Focus()
	a.panel.Show()
}

func (a *app) toggle() {
	if a.panel.IsVisible() {
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
	a.panel = mygo.NewWindow(mygo.WindowOptions{
		Title:         "Copied",
		Panel:         true,
		Hidden:        true,
		Frameless:     true,
		Transparent:   true,
		Vibrancy:      mygo.VibrancyGlass,
		CornerRadius:  18,
		Width:         1200,
		Height:        panelHeight,
		DisableResize: true,
		Content:       ui.View(a.view),
	})
	// Hide rather than close, on Escape, Cmd-W or Alt-F4, and when the user
	// goes elsewhere: the panel shows again at once. Quitting destroys it.
	// Quit asks every window to close, and a close prevented cancels it.
	a.panel.OnClose(func(e *mygo.CloseEvent) {
		if a.quitting {
			return
		}
		e.PreventDefault()
		a.panel.Hide()
	})
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { a.quitting = true })
	a.panel.OnBlur(a.panel.Hide)
	a.panel.OnHide(func() {
		if a.recording {
			a.stopRecording("")
		}
		a.settingsOpen = false
		// A hidden panel keeps nothing: it lists the clips again as it
		// shows, and their pictures go back to the system meanwhile.
		a.clips, a.thumbs = nil, map[int64]*thumb{}
		go debug.FreeOSMemory()
	})

	a.settings = loadSettings()
	a.registerShortcut()
	a.setUpTray()
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) {
		if a.tray != nil {
			a.tray.Destroy()
		}
		a.store.Close()
	})
	go a.watch()
	if !mygo.App.WasOpenedAtLogin() {
		a.show() // started by the user: show what it does
	}
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
		if err := mygo.GlobalShortcut.Register(s, a.toggle); err == nil {
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
func (a *app) startRecording() {
	a.recording, a.recordError = true, ""
	if a.shortcut != "" {
		mygo.GlobalShortcut.Unregister(a.shortcut)
	}
}

// stopRecording takes the shortcut recorded, or keeps the current one
// when there is none or the system refuses it.
func (a *app) stopRecording(shortcut string) {
	a.recording = false
	if shortcut != "" && shortcut != a.shortcut {
		if err := mygo.GlobalShortcut.Register(shortcut, a.toggle); err == nil {
			a.shortcut, a.settings.Shortcut = shortcut, shortcut
			a.saveSettings()
			return
		}
		a.recordError = "这个快捷键已被占用，换一个试试"
	}
	if a.shortcut != "" {
		if err := mygo.GlobalShortcut.Register(a.shortcut, a.toggle); err != nil {
			log.Printf("shortcut %s: %v", a.shortcut, err)
		}
	}
}

// resetShortcut goes back to the default shortcut.
func (a *app) resetShortcut() {
	if a.shortcut != "" {
		mygo.GlobalShortcut.Unregister(a.shortcut)
	}
	a.settings.Shortcut, a.recordError = "", ""
	a.registerShortcut()
	a.saveSettings()
}

func (a *app) saveSettings() {
	if err := a.settings.save(); err != nil {
		log.Print("saving the settings: ", err)
	}
	a.updateTrayMenu()
}

// openSettings shows the panel with its settings dialog.
func (a *app) openSettings() {
	if !a.panel.IsVisible() {
		a.show()
	}
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
	a.tray.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
		{Label: "打开面板", Click: func(*mygo.MenuItem, *mygo.Window) { a.show() }},
		{Label: "快捷键…", Click: func(*mygo.MenuItem, *mygo.Window) { a.openSettings() }},
		mygo.Separator(),
		{Label: "暂停记录", Type: mygo.MenuItemCheckbox, Checked: a.paused.Load(), Click: func(it *mygo.MenuItem, _ *mygo.Window) {
			a.paused.Store(it.Checked)
		}},
		{Label: "开机启动", Type: mygo.MenuItemCheckbox, Checked: mygo.App.OpenAtLogin(),
			Click: func(it *mygo.MenuItem, _ *mygo.Window) {
				if err := mygo.App.SetOpenAtLogin(it.Checked); err != nil {
					log.Print(err)
				}
			}},
		mygo.Separator(),
		// The pinned clips stay.
		{Label: "清空历史", Click: func(*mygo.MenuItem, *mygo.Window) { a.clearHistory() }},
		mygo.Separator(),
		{Label: "退出", Click: func(*mygo.MenuItem, *mygo.Window) { mygo.App.Quit() }},
	}))
}

// trayIconPNG keeps the app's colored tile in the Windows notification area.
//go:embed resources/tray.png
var trayIconPNG []byte

// trayTemplatePNG lets macOS tint the outline to match its menu bar.
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
		if a.panel != nil {
			a.show()
		}
	})
	mygo.App.WhenReady(a.ready)
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
