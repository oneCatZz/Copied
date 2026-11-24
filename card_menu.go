package main

import (
	"copied/locales"
	"github.com/egoist/mygo/ui"
)

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
	tx := locales.In(c.Locale())
	m.Popup(func(panel ui.Element) {
		popupSurface(c, panel).Width(trayMenuWidth).Padding(6)
		batch := a.batchIDs(i) != nil
		pasteLabel, copyLabel := tx.Paste(), tx.CopyOnly()
		if batch {
			pasteLabel, copyLabel = tx.PasteAll(), tx.CopyAll()
		}
		if menuActionRow(c, m.Item(), menuPaste, pasteLabel, menuShortcut("Enter"), t.Accent).Clicked() {
			action = func() { a.paste(i, false) }
		}
		if clip.Kind != KindImage && clip.Kind != KindFiles && !batch {
			if menuActionRow(c, m.Item(), iconText, tx.PastePlain(), menuShortcut("Shift+Enter"), t.TextMuted).Clicked() {
				action = func() { a.paste(i, true) }
			}
		}
		if menuActionRow(c, m.Item(), menuCopy, copyLabel, menuShortcut("CmdOrCtrl+Enter"), t.TextMuted).Clicked() {
			action = func() { a.copyOnly(i) }
		}
		trayDivider(c)
		ink := t.TextMuted
		if len(clip.GroupIDs) > 0 {
			ink = t.Accent
		}
		if menuActionRow(c, m.Item(), iconFolder, tx.AddToGroups(), menuShortcut("CmdOrCtrl+P"), ink).Clicked() {
			action = func() { a.openClipGroups(i) }
		}
		stackLabel, stackInk := tx.AddToStack(), t.TextMuted
		if a.stackOrder(clip.ID) > 0 && !batch {
			stackLabel, stackInk = tx.RemoveFromStack(), t.Accent
		}
		if menuActionRow(c, m.Item(), iconStack, stackLabel, menuShortcut("CmdOrCtrl+S"), stackInk).Clicked() {
			action = func() { a.toggleStack(i) }
		}
		trayDivider(c)
		deleteLabel := tx.Delete()
		if batch {
			deleteLabel = tx.DeleteAll()
		}
		if menuActionRow(c, m.Item().TextColor(t.Danger), trayTrash, deleteLabel, menuShortcut("CmdOrCtrl+Backspace"), t.Danger).Clicked() {
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
