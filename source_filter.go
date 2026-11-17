package main

import (
	"strings"

	"copied/locales"

	"github.com/egoist/mygo/ui"
)

var sourceCloseIcon = svg(`<path d="m6 6 12 12M18 6 6 18"/>`)

// sourceFilter is a token inside the search field, before its text input.
func (a *app) sourceFilter(c *ui.Context, t *ui.Theme) ui.Element {
	tx := locales.In(c.Locale())
	width, _ := c.Size()
	if !a.sourceMenuOpen {
		a.sourceSearch.CancelFocus(c)
	}
	token := ui.Row(c.Key("source-token")).Height(26).Shrink(0).Gap(2).PaddingX(4).
		Radius(7).Background(t.Accent.Alpha(0.09)).Border(1, t.Accent.Alpha(0.22)).Children(func() {
			trigger := ui.ButtonBase(c.Key(headerMenuKey{a.searchSession, "source"})).Height(24).PaddingX(3).Gap(5).
				MaxWidth(min(float32(220), width*0.3)).AlignItems(ui.Center).Cursor(ui.CursorPointer).
				Label(tx.SourceApps()).Description(tx.SourceAppShortcut(strings.Join(keycaps(a.sourceShortcut()), "+"))).
				OnClick(func() {
					if a.sourceMenuOpen {
						a.sourceMenuOpen = false
						a.input.Focus()
					} else {
						a.openSourcePicker("")
					}
				})
			trigger.Children(func() {
				if a.source == "" {
					ui.Text(c, "@").FontSize(13).FontWeight(600).TextColor(t.Accent)
					ui.Text(c, tx.Apps()).FontSize(12).TextColor(t.Accent).SingleLine()
					ui.Icon(c, iconChevron).FontSize(10).TextColor(t.Accent)
				} else {
					a.sourceIconView(c, a.source, 16, t)
					ui.Text(c, a.source).FontSize(12).FontWeight(550).TextColor(t.Accent).SingleLine().Grow(1).MinWidth(0)
				}
			})
			if a.source != "" {
				ui.ButtonBase(c.Key("clear-source")).Size(18, 20).Center().Radius(5).Cursor(ui.CursorPointer).
					Label(tx.ClearSourceApp()).OnClick(func() { a.selectSource("") }).Children(func() {
						ui.Icon(c, sourceCloseIcon).FontSize(10).TextColor(t.Accent)
					})
			}
		})
	return token
}

// sourcePicker uses a real text input. Composition, candidate confirmation
// and navigation belong to the combobox, including when an IME is active.
func (a *app) sourcePicker(c *ui.Context, t *ui.Theme, token ui.Element) {
	tx := locales.In(c.Locale())
	cb := ui.ComboboxBase(c.Key(headerMenuKey{a.searchSession, "source-input"}), &a.sourceQuery)
	cb.Input.Bind(&a.sourceSearch).Grow(1).MinWidth(0).FontSize(13).CaretColor(t.Accent).
		Placeholder(tx.SearchSourceApps()).Label(tx.SourceApps()).AutoComplete(ui.AutoCompleteList).
		OnTextKey(func(k ui.TextKey) bool {
			if a.isSourceShortcut(k.Mods, k.Key) {
				a.sourceMenuOpen = false
				a.input.Focus()
				return true
			}
			return false
		})
	if a.sourceOpenPending {
		cb.SetOpen(true)
		// MyGo v0.9.0 opens with its widget-local highlight at -1. Seed the
		// first option once; later typing and navigation stay with the combobox.
		*ui.Local(cb.Input, "highlight", func() int { return -1 }) = 0
		a.sourceOpenPending = false
	}
	width, height := c.Size()
	cb.Popup(func(panel ui.Element) {
		popupSurface(c, panel).AttachTo(token, ui.AnchorBottomStart, ui.AnchorTopStart).
			MinWidth(0).Width(min(float32(380), width-44)).Padding(10).Radius(14).Margin(8, 0, 0, 0).Gap(8)
		ui.Text(c, tx.SourceApps()).FontSize(10.5).FontWeight(550).TextColor(t.TextMuted).Padding(2, 4)
		options := a.matchingSources(a.sourceQuery, tx.All())
		listHeight := min(float32(len(options)*40-4), max(float32(36), min(float32(320), height-190)))
		if len(options) == 0 {
			ui.Text(c, tx.NoSourceApps()).FontSize(12).TextColor(t.TextMuted).Padding(10)
		} else {
			ui.Scroll(c.Key("source-options")).Height(listHeight).Gap(4).Children(func() {
				for _, source := range options {
					label := source
					if source == "" {
						label = tx.All()
					}
					item := cb.Item(source).Height(36).Shrink(0).PaddingX(9).Gap(8).AlignItems(ui.Center).
						Radius(10).Cursor(ui.CursorPointer).Label(label).Description(label).Border(1, ui.Transparent)
					if item.Highlighted() {
						item.Background(t.SurfaceHover).Border(1, t.Border.Alpha(0.7))
					}
					selected := a.source == source
					text := t.Text
					if selected {
						item.Gradient(t.Accent.Alpha(0.12), t.Accent.Alpha(0.04), 135).Border(1, t.Accent.Alpha(0.3))
						text = t.Accent
					}
					item.Children(func() {
						a.sourceIconView(c, source, 24, t)
						ui.Text(c, label).FontSize(12).FontWeight(550).TextColor(text).SingleLine().Grow(1)
						if selected {
							ui.Icon(c, filterCheckIcon).FontSize(12).Shrink(0).TextColor(t.Accent)
						}
					})
				}
			})
		}
		ui.Text(c, tx.SourceAppKeys()).FontSize(10.5).TextColor(t.TextMuted).Padding(2, 4)
	})
	if source, chosen := cb.Chosen(); chosen {
		a.selectSource(source)
	} else if !cb.Open() {
		a.sourceMenuOpen = false
		if a.sourceSearch.Focused(c) {
			a.input.Focus()
		}
	}
}

func (a *app) matchingSources(query, all string) []string {
	q := strings.ToLower(strings.TrimSpace(query))
	var starts, contains []string
	for _, source := range append([]string{""}, a.sources...) {
		label := source
		if label == "" {
			label = all
		}
		label = strings.ToLower(label)
		if strings.HasPrefix(label, q) {
			starts = append(starts, source)
		} else if strings.Contains(label, q) {
			contains = append(contains, source)
		}
	}
	return append(starts, contains...)
}

func (a *app) openSourcePicker(query string) {
	a.cardMenuID, a.typeMenuOpen, a.groupMenuOpen = 0, false, false
	a.sourceQuery = query
	a.sourceMenuOpen, a.sourceOpenPending = true, true
	a.sourceSearch.Focus()
}

func (a *app) selectSource(source string) {
	a.source, a.chosen, a.scrollTo = source, 0, true
	a.cardMenuID, a.typeMenuOpen, a.groupMenuOpen = 0, false, false
	a.sourceMenuOpen, a.sourceOpenPending = false, false
	a.sourceQuery = ""
	a.reload()
	a.input.Focus()
}
