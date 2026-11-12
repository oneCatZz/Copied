package main

import "github.com/egoist/mygo/ui"

var (
	menuPaste = svg(`<rect x="5" y="5" width="14" height="16" rx="2"/><rect x="9" y="3" width="6" height="4" rx="1"/><path d="M9 13h6M12 10v6"/>`)
	menuCopy  = svg(`<rect x="8" y="8" width="12" height="13" rx="2"/><path d="M16 8V5a2 2 0 0 0-2-2H5a2 2 0 0 0-2 2v9a2 2 0 0 0 2 2h3"/>`)
)

// The open clip ID belongs to the app, so hiding the panel can dismiss its
// menu even when its renderer is kept warm for a quick reopening.
func (a *app) cardMenu(c *ui.Context, card ui.Element, i int, clip *Clip) (action func()) {
	open := a.cardMenuID == clip.ID
	m := ui.ContextMenuBase(c, card, &open)
	if open && a.cardMenuID != clip.ID {
		a.chosen = i // The displayed shortcuts act on the right-clicked card.
	}
	t := c.Theme()
	m.Popup(func(panel ui.Element) {
		panel.Width(trayMenuWidth).Padding(6).Radius(8).Background(t.Surface).
			Border(1, t.Border).Shadow(0, 8, 24, -4, ui.RGB(0, 0, 0).Alpha(0.18))
		if menuActionRow(c, m.Item(), menuPaste, "粘贴", menuShortcut("Enter"), t.Accent).Clicked() {
			action = func() { a.paste(i, false) }
		}
		if clip.Kind != KindImage && clip.Kind != KindFiles {
			if menuActionRow(c, m.Item(), iconText, "粘贴为纯文本", menuShortcut("Shift+Enter"), t.TextMuted).Clicked() {
				action = func() { a.paste(i, true) }
			}
		}
		if menuActionRow(c, m.Item(), menuCopy, "仅复制", menuShortcut("CmdOrCtrl+Enter"), t.TextMuted).Clicked() {
			action = func() { a.copyOnly(i) }
		}
		trayDivider(c)
		label, ink := "收藏", t.TextMuted
		if clip.Pinned {
			label, ink = "取消收藏", t.Accent
		}
		if menuActionRow(c, m.CheckItem(clip.Pinned), iconPin, label, menuShortcut("CmdOrCtrl+P"), ink).Clicked() {
			action = func() { a.togglePin(i) }
		}
		trayDivider(c)
		if menuActionRow(c, m.Item().TextColor(t.Danger), trayTrash, "删除", menuShortcut("CmdOrCtrl+Backspace"), t.Danger).Clicked() {
			action = func() { a.remove(i) }
		}
	})
	if open {
		a.cardMenuID = clip.ID
	} else if a.cardMenuID == clip.ID {
		a.cardMenuID = 0
	}
	return action
}
