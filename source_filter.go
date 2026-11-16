package main

import (
	"slices"
	"strings"
	"time"

	"copied/locales"

	"github.com/egoist/mygo/ui"
)

var sourceAppIcon = svg(`<rect x="3" y="4" width="18" height="13" rx="2"/><path d="M8 21h8m-4-4v4"/>`)

func (a *app) sourceFilter(c *ui.Context) {
	t, tx := c.Theme(), locales.In(c.Locale())
	if !a.sourceMenuOpen {
		a.sourcePrefix = ""
		a.sourceMovePending = false
	}
	name := a.source
	if name == "" {
		name = tx.All()
	}
	sel := ui.SelectBaseOpen(c.Key(headerMenuKey{a.searchSession, "source"}), &a.source, &a.sourceMenuOpen)
	trigger := sel.Trigger.Row().Width(148).Height(34).Shrink(0).PaddingX(8).Gap(5).
		AlignItems(ui.Center).Radius(10).Border(1, t.Border).Background(t.Surface).
		Cursor(ui.CursorPointer).Label(tx.SourceApps()).Value(name).
		HandleInput(func(ev ui.InputEvent) bool { return a.sourceFilterInput(ev, tx.All()) }).
		OnChange(func() { a.selectSource(a.source) })
	if a.sourceMenuOpen || trigger.Hovered() || trigger.FocusVisible() {
		trigger.Border(1, t.Accent.Alpha(0.4)).Background(t.Accent.Alpha(0.05))
	}
	if !c.Preferences().ReduceMotion {
		trigger.Transition(fadeMotion)
	}
	trigger.Children(func() {
		ui.Icon(c, sourceAppIcon).FontSize(14).Shrink(0).TextColor(t.Accent)
		ui.Text(c, tx.AppFilter(name)).FontSize(11.5).FontWeight(550).TextColor(t.Text).SingleLine().Grow(1)
		ui.Icon(c, iconChevron).FontSize(10).Shrink(0).TextColor(t.TextMuted)
	})
	if a.sourceMovePending {
		sel.Highlight(a.sourceHighlight)
		a.sourceMovePending = false
	}
	width, height := c.Size()
	sel.Popup(func(panel ui.Element) {
		popupSurface(c, panel).Width(min(float32(360), width-44)).Padding(10).Radius(14).Margin(7, 0, 0, 0).Gap(8)
		ui.Text(c, tx.SourceApps()).FontSize(10.5).FontWeight(550).TextColor(t.TextMuted).Padding(2, 4)
		listHeight := min(float32((len(a.sources)+1)*40-4), max(float32(36), min(float32(320), height-180)))
		ui.Scroll(c.Key("source-options")).Height(listHeight).Gap(4).Children(func() {
			for _, source := range append([]string{""}, a.sources...) {
				label := source
				icon := sourceAppIcon
				if source == "" {
					label, icon = tx.All(), filterAllIcon
				}
				item := sel.Item(source).Height(36).Shrink(0).PaddingX(9).Gap(8).AlignItems(ui.Center).
					Radius(10).Cursor(ui.CursorPointer).Label(label).Description(label).Border(1, ui.Transparent)
				if item.Highlighted() {
					a.sourceHighlight = source
					item.Background(t.SurfaceHover).Border(1, t.Border.Alpha(0.7))
				}
				selected := a.source == source
				text := t.Text
				if selected {
					item.Gradient(t.Accent.Alpha(0.12), t.Accent.Alpha(0.04), 135).Border(1, t.Accent.Alpha(0.3))
					text = t.Accent
				}
				item.Children(func() {
					ui.Box(c).Size(24, 24).Shrink(0).Center().Radius(7).Background(t.Accent.Alpha(0.11)).Children(func() {
						ui.Icon(c, icon).FontSize(14).TextColor(t.Accent)
					})
					ui.Text(c, label).FontSize(12).FontWeight(550).TextColor(text).SingleLine().Grow(1)
					if selected {
						ui.Icon(c, filterCheckIcon).FontSize(12).Shrink(0).TextColor(t.Accent)
					}
				})
			}
		})
	})
}

// sourceFilterInput keeps type-ahead local to the selector. Only values and
// pending state survive the event; SelectParts are used in the next build.
func (a *app) sourceFilterInput(ev ui.InputEvent, all string) bool {
	if !a.sourceMenuOpen || ev.Kind != ui.InputKeyDown || ev.Mods&^ui.Shift != 0 {
		return false
	}
	var letter rune
	switch {
	case ev.Key >= ui.KeyA && ev.Key <= ui.KeyZ:
		letter = 'a' + rune(ev.Key-ui.KeyA)
	case ev.Key >= ui.Key0 && ev.Key <= ui.Key9:
		letter = '0' + rune(ev.Key-ui.Key0)
	default:
		return false
	}
	options := append([]string{""}, a.sources...)
	current := max(0, slices.Index(options, a.sourceHighlight))
	find := func(prefix string, start int) (string, bool) {
		for step := range len(options) {
			source := options[(start+step)%len(options)]
			label := source
			if source == "" {
				label = all
			}
			if strings.HasPrefix(strings.ToLower(label), prefix) {
				return source, true
			}
		}
		return "", false
	}
	prefix, start := string(letter), current+1
	if a.sourcePrefix != "" && a.sourcePrefix != prefix && ev.Time.Sub(a.sourceTypedAt) < 750*time.Millisecond {
		prefix, start = a.sourcePrefix+prefix, current
	}
	match, found := find(prefix, start)
	if !found {
		// A different initial starts a new search immediately rather than
		// making the user wait for the previous prefix to expire.
		prefix = string(letter)
		match, found = find(prefix, current+1)
	}
	a.sourcePrefix, a.sourceTypedAt = prefix, ev.Time
	if found {
		a.sourceHighlight, a.sourceMovePending = match, true
	} else {
		a.sourcePrefix = ""
	}
	return true
}

func (a *app) selectSource(source string) {
	a.source, a.chosen, a.scrollTo = source, 0, true
	a.cardMenuID, a.typeMenuOpen, a.groupMenuOpen = 0, false, false
	a.sourceMenuOpen = false
	a.reload()
	a.input.Focus()
}
