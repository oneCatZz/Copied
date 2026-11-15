package main

import (
	"log"
	"slices"
	"time"

	"copied/locales"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// stackShortcut pastes the next clip of the stack from any app. It is
// registered only while the stack holds clips, so it borrows the keys briefly.
const stackShortcut = "CmdOrCtrl+Alt+V"

var iconStack = svg(`<path d="m12 3 9 5-9 5-9-5 9-5z"/><path d="m3 13 9 5 9-5"/>`)

// stackOrder is a clip's place in the stack, from 1, or 0 when it is not in it.
func (a *app) stackOrder(id int64) int {
	return slices.Index(a.stack, id) + 1
}

// toggleStack adds a clip to the end of the stack, or takes it out.
func (a *app) toggleStack(i int) {
	if i < 0 || i >= len(a.clips) {
		return
	}
	if ids := a.batchIDs(i); ids != nil {
		for _, id := range ids {
			if !slices.Contains(a.stack, id) {
				a.stack = append(a.stack, id)
			}
		}
		a.syncStackShortcut()
		return
	}
	id := a.clips[i].ID
	if n := slices.Index(a.stack, id); n >= 0 {
		a.stack = slices.Delete(a.stack, n, n+1)
	} else {
		a.stack = append(a.stack, id)
	}
	a.syncStackShortcut()
}

func (a *app) clearStack() {
	a.stack = nil
	a.syncStackShortcut()
}

// pruneStack drops the clips that are no longer in the history.
func (a *app) pruneStack() {
	a.stack = slices.DeleteFunc(a.stack, func(id int64) bool { return !a.store.Has(id) })
	a.syncStackShortcut()
}

// syncStackShortcut holds the stack's shortcut while the stack has clips.
// Recording a panel shortcut lets it go, so the recorder sees those keys.
func (a *app) syncStackShortcut() {
	want := len(a.stack) > 0 && !a.recording
	if want == a.stackKeys {
		return
	}
	if want {
		if err := mygo.GlobalShortcut.Register(stackShortcut, a.pasteNext); err != nil {
			log.Printf("shortcut %s: %v", stackShortcut, err)
			return
		}
	} else {
		mygo.GlobalShortcut.Unregister(stackShortcut)
	}
	a.stackKeys = want
}

// pasteNext pastes the first clip of the stack into the app the user is
// in, and takes it off the stack.
func (a *app) pasteNext() {
	target := frontWindow()
	if a.panel != nil && a.panel.IsVisible() {
		target = a.target
		a.panel.Hide()
	}
	for len(a.stack) > 0 {
		id := a.stack[0]
		a.stack = a.stack[1:]
		if a.store.Has(id) {
			a.queueStackPaste(stackPaste{id: id, target: target})
			break
		}
	}
	a.syncStackShortcut()
}

// stackPasteGap lets the app read a clip from the clipboard before the next
// clip of the stack replaces it.
const stackPasteGap = 150 * time.Millisecond

// stackPaste is a clip of the stack on its way to the app the user is in.
type stackPaste struct {
	id     int64
	target uintptr
}

// queueStackPaste pastes the clips of the stack one at a time, however fast
// the shortcut is pressed: each takes the clipboard only once the last one
// was pasted.
func (a *app) queueStackPaste(p stackPaste) {
	if a.stackPastes == nil {
		a.stackPastes = make(chan stackPaste, 64)
		go func() {
			for p := range a.stackPastes {
				var ok bool
				mygo.RunOnMain(func() { ok = a.copyClip(p.id, false) })
				if !ok {
					continue
				}
				pasteInto(p.target)
				time.Sleep(stackPasteGap)
			}
		}()
	}
	select {
	case a.stackPastes <- p:
	default: // never block the main thread, which the paster waits on
		log.Print("stack: too many pastes queued")
	}
}

var iconClose = svg(`<path d="M6 6l12 12M18 6 6 18"/>`)

// stackStatus shows how many clips wait in the stack, the keys that paste
// the next one, and a button that empties it.
func (a *app) stackStatus(c *ui.Context, width float32) {
	t, tx := c.Theme(), locales.In(c.Locale())
	ui.Row(c).Height(24).PaddingStart(8).PaddingEnd(4).Gap(6).AlignItems(ui.Center).Radius(12).
		Background(t.Accent.Alpha(0.1)).Children(func() {
		ui.Icon(c, iconStack).FontSize(12).TextColor(t.Accent)
		ui.Text(c, tx.StackCount(len(a.stack))).FontSize(11).FontWeight(600).TextColor(t.Accent).SingleLine()
		if a.stackKeys && width >= 1100 {
			for _, key := range keycaps(stackShortcut) {
				keycap(c, key)
			}
			ui.Text(c, tx.PasteNext()).FontSize(11).TextColor(t.TextMuted).SingleLine()
		}
		clear := ui.ButtonBase(c.Key("clear-stack")).Size(18, 18).Shrink(0).Center().Radius(9).
			Cursor(ui.CursorPointer).Label(tx.ClearStack()).Description(tx.ClearStack()).
			OnClick(a.clearStack)
		if clear.Hovered() || clear.FocusVisible() {
			clear.Background(t.Accent.Alpha(0.16)).TextColor(t.Accent)
		} else {
			clear.TextColor(t.TextMuted)
		}
		clear.Children(func() { ui.Icon(c, iconClose).FontSize(10) })
	})
}
