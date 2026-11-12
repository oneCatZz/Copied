package main

import (
	"log"
	"runtime"
	"strings"

	"copied/locales"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const trayMenuWidth = 252 // DIPs; room for labels and the full shortcut

var (
	trayKeyboard = svg(`<rect x="3" y="5" width="18" height="14" rx="3"/><path d="M7 9h.01M12 9h.01M17 9h.01M7 12h.01M12 12h.01M17 12h.01M8 15h8"/>`)
	trayPower    = svg(`<path d="M12 3v8M6.3 5.7a8 8 0 1 0 11.4 0"/>`)
	trayTrash    = svg(`<path d="M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13M10 11v5M14 11v5"/>`)
	trayExit     = svg(`<path d="M9 5H5v14h4M10 12h11M17 8l4 4-4 4"/>`)
)

func (a *app) toggleTrayMenu() {
	if a.trayFlyout != nil {
		a.closeTrayMenu()
		return
	}
	a.trayTarget = frontWindow()
	if a.panel.IsVisible() {
		a.trayTarget = a.target
		a.panel.Hide()
	}
	a.trayRecording = !a.paused.Load()
	a.trayLogin = mygo.App.OpenAtLogin()
	a.trayError = ""
	window := mygo.NewFlyout(mygo.FlyoutOptions{
		Tray:       a.tray,
		Placement:  mygo.PlacementBottom,
		Gap:        6,
		Width:      trayMenuWidth,
		Height:     281,
		FitContent: true,
		MinWidth:   trayMenuWidth,
		MaxWidth:   360,
		Focusable:  true,
		Shadow:     true,
		Popover:    true,
		Content:    ui.View(a.trayMenuView),
	})
	a.trayFlyout = window
	window.OnClosed(func() {
		if a.trayFlyout == window {
			a.trayFlyout = nil
		}
	})
	a.trayFocus.Focus()
}

func (a *app) closeTrayMenu() {
	if a.trayFlyout != nil {
		a.trayFlyout.Close()
	}
}

func (a *app) trayMenuView(c *ui.Context) {
	tx := locales.In(c.Locale())
	t := lightTheme
	if c.Theme().Dark {
		t = darkTheme
	}
	c.Root().Background(ui.Transparent)
	box := ui.Themed(c, t).Padding(6).FocusGroup(ui.Vertical)
	if c.Vibrancy() {
		box.Background(ui.Transparent) // AppKit supplies the popover's material.
	} else {
		box.Background(t.Surface) // Windows supplies the outline and rounded corners.
	}
	box.Children(func() {
		ui.Row(c).Height(32).Shrink(0).PaddingX(10).Gap(5).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Copied").FontSize(12).FontWeight(650).TextColor(t.TextMuted).Grow(1)
			status, ink := tx.Recording(), ui.Hex("#119b80")
			if a.paused.Load() {
				status, ink = tx.Paused(), t.TextMuted
			}
			ui.Box(c).Size(6, 6).Shrink(0).Radius(3).Background(ink)
			ui.Text(c, status).FontSize(10.5).TextColor(t.TextMuted).SingleLine()
		})

		shortcut := menuShortcut(a.shortcut)
		trayAction(c, "open", iconLogo, tx.OpenPanel(), shortcut, t.Accent, a.show).Bind(&a.trayFocus)
		trayAction(c, "shortcut", trayKeyboard, tx.Settings(), "", t.TextMuted, a.openSettings)
		trayDivider(c)

		trayToggle(c, "recording", iconLogo, tx.ClipboardRecording(), &a.trayRecording, func() {
			a.paused.Store(!a.trayRecording)
		})
		trayToggle(c, "login", trayPower, tx.StartAtLogin(), &a.trayLogin, func() {
			a.trayError = ""
			if err := mygo.App.SetOpenAtLogin(a.trayLogin); err != nil {
				log.Print("setting open at login: ", err)
				a.trayLogin = mygo.App.OpenAtLogin()
				a.trayError = "loginFailed"
			}
		})
		trayDivider(c)

		trayAction(c, "clear", trayTrash, tx.ClearHistory(), tx.KeepFavorites(), t.TextMuted, a.clearHistory)
		if a.trayError != "" {
			ui.Text(c, locales.Catalog.Format(c.Locale(), a.trayError, nil)).FontSize(11).TextColor(t.Danger).Padding(4, 10).SingleLine()
		}
		trayDivider(c)
		trayAction(c, "quit", trayExit, tx.QuitCopied(), "", t.Danger, mygo.App.Quit).TextColor(t.Danger)
	})
}

// trayAction returns only this frame's button; callbacks capture app state.
func trayAction(c *ui.Context, key string, icon *ui.SVG, label, detail string, ink ui.Color, action func()) ui.Element {
	return menuActionRow(c, ui.ButtonBase(c.Key(key)).OnClick(action), icon, label, detail, ink)
}

// Both tray actions and context menu items use the same visual treatment;
// their base widgets retain their own keyboard and accessibility behavior.
func menuActionRow(c *ui.Context, row ui.Element, icon *ui.SVG, label, detail string, ink ui.Color) ui.Element {
	t := c.Theme()
	row.Height(34).PaddingX(10).Gap(9).Justify(ui.Start).AlignItems(ui.Center).
		Radius(6).Cursor(ui.CursorPointer).Label(label).FocusRing(false)
	if row.Hovered() || row.FocusVisible() || row.Highlighted() {
		row.Background(t.SurfaceHover)
	}
	row.Children(func() {
		ui.Icon(c, icon).FontSize(15).Shrink(0).TextColor(ink)
		ui.Text(c, label).FontSize(13).SingleLine().Shrink(0)
		ui.Spacer(c)
		if detail != "" {
			ui.Text(c, detail).FontSize(10.5).SingleLine().Shrink(0).TextColor(t.TextMuted)
		}
	})
	return row
}

func menuShortcut(shortcut string) string {
	caps := keycaps(shortcut)
	if runtime.GOOS == "darwin" {
		for i, key := range caps {
			switch key {
			case "Enter":
				caps[i] = "↵"
			case "Backspace":
				caps[i] = "⌫"
			}
		}
		return strings.Join(caps, "")
	}
	return strings.Join(caps, "+")
}

// The whole row is a switch, including its label; Space and Enter toggle it.
func trayToggle(c *ui.Context, key string, icon *ui.SVG, label string, on *bool, change func()) {
	t := c.Theme()
	row := ui.SwitchBase(c.Key(key), on).Height(34).PaddingX(10).Gap(9).AlignItems(ui.Center).
		Radius(6).Cursor(ui.CursorPointer).Label(label).FocusRing(false).OnChange(change).
		OnShortcut(0, ui.KeyEnter, func() {
			*on = !*on
			change()
		})
	if row.Hovered() || row.FocusVisible() {
		row.Background(t.SurfaceHover)
	}
	row.Children(func() {
		ui.Icon(c, icon).FontSize(15).Shrink(0).TextColor(t.TextMuted)
		ui.Text(c, label).FontSize(13).SingleLine().Shrink(0)
		ui.Spacer(c)
		ink, position := t.Border.Mix(t.Text, 0.15), ui.Start
		if *on {
			ink, position = t.Accent, ui.End
		}
		ui.Row(c).Size(32, 18).Shrink(0).Padding(2).Radius(9).Background(ink).
			AlignItems(ui.Center).Justify(position).Children(func() {
			ui.Box(c).Size(14, 14).Radius(7).Background(ui.RGB(255, 255, 255))
		})
	})
}

func trayDivider(c *ui.Context) {
	ui.Box(c).Height(1).Shrink(0).Margin(5, 4).Background(c.Theme().Border.Alpha(0.7))
}

func (a *app) clearHistory() {
	if err := a.store.Clear(); err != nil {
		log.Print("clearing the history: ", err)
		a.trayError = "clearFailed"
		if a.trayFlyout != nil {
			a.trayFlyout.Invalidate()
		}
		return
	}
	a.closeTrayMenu()
	if a.panel.IsVisible() {
		a.reload()
		a.panel.Invalidate()
	}
}
