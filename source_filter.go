package main

import (
	"copied/locales"

	"github.com/egoist/mygo/ui"
)

func (a *app) sourceFilter(c *ui.Context) {
	tx := locales.In(c.Locale())
	name := a.source
	if name == "" {
		name = tx.All()
	}
	button := ui.ButtonBase(c.Key(headerMenuKey{a.searchSession, "source"}))
	selectTrigger(c, button, tx.AppFilter(name), iconChevron).Width(148).Label(tx.SourceApps())
	button.PopoverMenuButton(func(m *ui.Menu) {
		if m.Item(tx.All()).Radio(a.source == "").Chosen() {
			a.selectSource("")
		}
		for _, source := range a.sources {
			if m.Item(source).Radio(a.source == source).Chosen() {
				a.selectSource(source)
			}
		}
	}, ui.PopoverMenuButtonOptions{Open: &a.sourceMenuOpen})
}

func (a *app) selectSource(source string) {
	a.source, a.chosen, a.scrollTo = source, 0, true
	a.cardMenuID, a.typeMenuOpen, a.groupMenuOpen = 0, false, false
	a.sourceMenuOpen = false
	a.reload()
	a.input.Focus()
}
