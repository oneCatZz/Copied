package main

import "github.com/egoist/mygo/ui"

// Session keys discard transient editing menus and tooltip state after the
// panel hides. Text editing retains its own state throughout a visible session.
type searchInputKey uint64
type settingsTipKey uint64

func popupSurface(c *ui.Context, panel ui.Element) ui.Element {
	t := c.Theme()
	return panel.Radius(8).Background(t.Surface).Border(1, t.Border).
		Shadow(0, 8, 24, -4, ui.RGB(0, 0, 0).Alpha(0.18))
}

func dialogButton(c *ui.Context, key, label string, primary bool) ui.Element {
	t := c.Theme()
	button := ui.ButtonBase(c.Key(key)).Height(34).PaddingX(12).Radius(6).Label(label).
		Cursor(ui.CursorPointer).Border(1, t.Border)
	fill, text := t.Surface, t.Text
	if primary {
		fill, text = t.Accent, t.AccentText
		if button.Pressed() {
			fill = t.AccentPressed
		} else if button.Hovered() {
			fill = t.AccentHover
		}
	} else if button.Pressed() {
		fill = t.SurfacePressed
	} else if button.Hovered() {
		fill = t.SurfaceHover
	}
	button.Background(fill).TextColor(text).Children(func() {
		ui.Text(c, label).FontSize(13).SingleLine()
	})
	return button
}
