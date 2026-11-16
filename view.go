package main

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"copied/locales"

	"github.com/egoist/mygo/ui"
)

const (
	listWidth = 360 // DIPs
	rowHeight = 54
	rowGap    = 4
)

// A placeholder must not inherit the full row's transition snapshot.
type cardPlaceholder int64

// Rows slide to their places as clips come, go and move to the front, new
// ones fading in.
var rowMotion = ui.ElementTransition{
	Animation: &ui.Snappy,
	Enter:     &ui.Motion{Y: 8, Opacity: 0},
	Exit:      &ui.Motion{Scale: 0.96, Opacity: 0},
}

var digitKeys = []ui.Key{ui.Key1, ui.Key2, ui.Key3, ui.Key4, ui.Key5, ui.Key6, ui.Key7, ui.Key8, ui.Key9}

// cmdCap is the Command key, as the platform writes it.
func cmdCap() string {
	if runtime.GOOS == "darwin" {
		return "⌘"
	}
	return "Ctrl"
}

// key does what a key of the panel does, and reports whether it took it.
func (a *app) key(mods ui.Modifiers, key ui.Key) bool {
	switch {
	case key == ui.KeyTab && (mods == 0 || mods == ui.Shift):
		if a.settingsOpen || a.groupDialogOpen || a.typeMenuOpen || a.groupMenuOpen || a.cardMenuID != 0 {
			return false
		}
		a.cycleGroup(mods == ui.Shift)
	case mods == 0 && key == ui.KeyEscape:
		if len(a.picked) > 0 {
			a.picked = nil
			return true
		}
		if a.query == "" {
			return false // left to the panel, which hides
		}
		a.query, a.chosen, a.scrollTo = "", 0, true
		a.reload()
	case mods == 0 && key == ui.KeyUp:
		a.picked = nil
		a.choose(a.chosen - 1)
	case mods == 0 && key == ui.KeyDown:
		a.picked = nil
		a.choose(a.chosen + 1)
	case mods == ui.Shift && key == ui.KeyUp:
		a.extendPick(-1)
	case mods == ui.Shift && key == ui.KeyDown:
		a.extendPick(1)
	case mods == 0 && key == ui.KeyEnter:
		a.paste(a.chosen, false)
	case mods == ui.Shift && key == ui.KeyEnter:
		a.paste(a.chosen, true)
	case mods == ui.Cmd && key == ui.KeyEnter:
		a.copyOnly(a.chosen)
	case mods == ui.Cmd && key == ui.KeyBackspace:
		a.remove(a.chosen)
	case mods == ui.Cmd && key == ui.KeyP:
		a.openClipGroups(a.chosen)
	case mods == ui.Cmd && key == ui.KeyS:
		a.toggleStack(a.chosen)
	case mods == ui.Cmd && key == ui.KeyComma:
		a.dismissGroupUI()
		a.settingsOpen = true
	default:
		if mods == ui.Cmd {
			for i, digit := range digitKeys {
				if key == digit {
					a.paste(i, false)
					return true
				}
			}
		}
		return false
	}
	return true
}

func (a *app) view(c *ui.Context) {
	// The system's theme says whether it is dark; the panel's own takes
	// its place below.
	t := lightTheme
	if c.Theme().Dark {
		t = darkTheme
	}
	glass := c.Vibrancy()
	if glass {
		c.Root().Background(ui.Transparent) // the material shows through
	} else {
		c.Root().Background(t.Background)
	}

	// The entrance draws frames until it is over.
	a.since = c.Now().Sub(a.shownAt)
	entranceEnd := riseTime
	if count := min(len(a.clips), cardsRising); count > 0 {
		entranceEnd = max(entranceEnd, riseTime/3+time.Duration(count-1)*cardStagger+cardRise)
	}
	if c.Preferences().ReduceMotion {
		a.since = entranceEnd
	}
	if a.since < entranceEnd {
		c.Invalidate()
	}

	// The keys the search field leaves, wherever the focus is.
	if !a.settingsOpen && !a.groupDialogOpen && !a.typeMenuOpen && !a.groupMenuOpen {
		type combo struct {
			mods ui.Modifiers
			key  ui.Key
		}
		combos := []combo{
			{0, ui.KeyTab}, {ui.Shift, ui.KeyTab},
			{0, ui.KeyUp}, {0, ui.KeyDown}, {ui.Shift, ui.KeyUp}, {ui.Shift, ui.KeyDown},
			{0, ui.KeyEnter}, {ui.Shift, ui.KeyEnter},
			{ui.Cmd, ui.KeyEnter}, {ui.Cmd, ui.KeyBackspace}, {ui.Cmd, ui.KeyP}, {ui.Cmd, ui.KeyS}, {ui.Cmd, ui.KeyComma},
		}
		if a.query != "" || len(a.picked) > 0 {
			combos = append(combos, combo{0, ui.KeyEscape})
		}
		for _, digit := range digitKeys {
			combos = append(combos, combo{ui.Cmd, digit})
		}
		for _, k := range combos {
			if c.Shortcut(k.mods, k.key) {
				a.key(k.mods, k.key)
			}
		}
	}

	body := ui.Themed(c, t).Fill()
	if glass {
		body.Background(t.Background.Alpha(0.66)) // a tint over the material
	}
	if p := rise(a.since, riseTime); p < 1 {
		body.Translate(0, (1-p)*24).Opacity(p)
	}
	body.Children(func() {
		a.header(c)
		a.content(c)
		a.footer(c)
		a.settingsDialog(c)
		a.groupDialog(c)
	})
	a.scrollTo = false
}

// header has the search field, the filters, the number of clips and the
// settings button.
func (a *app) header(c *ui.Context) {
	t := c.Theme()
	tx := locales.In(c.Locale())
	width, _ := c.Size()
	brandWidth := float32(98)
	if width < 960 {
		brandWidth = 24
	}
	searchWidth := min(float32(300), max(float32(160), width*0.27))
	if width < 600 {
		searchWidth = 120
	}
	groupWidth := max(float32(100), width-44-brandWidth-searchWidth-118-30-5*12)
	if width >= 1100 {
		groupWidth -= 62
	}
	ui.Row(c).Padding(18, 22, 6, 22).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Row(c).Width(brandWidth).Shrink(0).Gap(9).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(24, 24).Center().Radius(7).Gradient(t.Accent, kindColor(KindImage, t.Dark), 135).
				Shadow(0, 4, 10, -2, t.Accent.Alpha(0.5)).Children(func() {
				ui.Icon(c, iconLogo).FontSize(15).TextColor(ui.RGB(255, 255, 255))
			})
			if brandWidth > 24 {
				ui.Text(c, "Copied").FontSize(15).FontWeight(650).LetterSpacing(-0.2)
			}
		})

		editTheme := lightEditTheme
		if t.Dark {
			editTheme = darkEditTheme
		}
		field := ui.Themed(c.Key("search"), editTheme).Row().Width(searchWidth).Shrink(0).Height(34).PaddingX(11).Gap(8).AlignItems(ui.Center).
			Radius(10).Background(t.Surface)
		if a.input.Focused(c) {
			field.Border(1, t.Accent).Shadow(0, 0, 0, 3, t.Accent.Alpha(0.2))
		} else {
			field.Border(1, t.Border)
		}
		field.Transition(fadeMotion).Children(func() {
			ui.Icon(c, iconSearch).FontSize(14).TextColor(t.TextMuted)
			input := ui.TextInputBase(c.Key(searchInputKey(a.searchSession)), &a.query).Bind(&a.input).Grow(1).FontSize(13).
				CaretColor(t.Accent).
				Placeholder(tx.SearchHistory()).Label(tx.Search()).
				OnTextKey(func(k ui.TextKey) bool {
					return !a.groupDialogOpen && a.key(k.Mods, k.Key)
				})
			input.PopoverMenu(func(m *ui.Menu) { m.EditItems() })
			if input.Changed() {
				a.chosen, a.scrollTo = 0, true
				a.reload()
			}
		})

		a.groupNavigation(c, groupWidth)
		a.filters(c)
		if width >= 1100 {
			ui.Row(c).Width(50).Gap(5).AlignItems(ui.Center).Children(func() {
				if a.paused.Load() {
					ui.Box(c).Size(6, 6).Radius(3).Background(t.Warning).Description(tx.RecordingPaused())
				}
				ui.Text(c, tx.ClipCount(len(a.clips))).FontSize(11.5).SingleLine().TextColor(t.TextMuted)
			})
		}

		gear := ui.Row(c.Key(settingsTipKey(a.searchSession))).Size(30, 30).Shrink(0).Center().Radius(9).Cursor(ui.CursorPointer).
			Label(tx.Settings()).Description(tx.Settings())
		ui.TooltipBase(c, gear, func(tip ui.Element) {
			popupSurface(c, tip).Padding(6, 10).Radius(6).Margin(0, 0, 6, 0).
				Children(func() { ui.Text(c, tx.Settings()).FontSize(12) })
		})
		color := t.TextMuted
		if gear.Hovered() {
			gear.Background(t.SurfaceHover)
			color = t.Text
		}
		if gear.Clicked() {
			a.dismissGroupUI()
			a.settingsOpen = true
		}
		gear.Transition(fadeMotion).Children(func() {
			ui.Icon(c, iconSettings).FontSize(15).TextColor(color)
		})
	})
}

// content is the list of clips beside the preview of the chosen one.
func (a *app) content(c *ui.Context) {
	t := c.Theme()
	tx := locales.In(c.Locale())
	if len(a.clips) == 0 {
		ui.Column(c).Grow(1).Center().Gap(6).Children(func() {
			msg, hint := tx.EmptyHistory(), tx.EmptyHistoryHint()
			if a.query != "" || a.filter != FilterAll || a.groupID != 0 {
				msg, hint = tx.NoMatches(), tx.NoMatchesHint()
			}
			ui.Text(c, msg).FontSize(15).FontWeight(600)
			ui.Text(c, hint).FontSize(12).TextColor(t.TextMuted)
		})
		return
	}
	// What a row or the preview asks for changes the clips, so it waits for
	// them all to be built.
	var action func()
	ui.Row(c).Grow(1).Basis(0).MinHeight(0).Padding(12, 22, 14, 22).Gap(14).AlignItems(ui.Stretch).Children(func() {
		if act := a.list(c); act != nil {
			action = act
		}
		if act := a.detail(c); act != nil {
			action = act
		}
	})
	if action != nil {
		action()
	}
}

// list is the column of clips, which scrolls up and down.
func (a *app) list(c *ui.Context) (action func()) {
	_, height := c.Size()
	const stride = rowHeight + rowGap
	const overscan = 4 * stride
	top, bottom := a.listScroll.Y-overscan, a.listScroll.Y+height+overscan
	chosenY := float32(a.chosen * stride)
	buildRow := func(i int) bool {
		y := float32(i * stride)
		near := y+rowHeight >= top && y <= bottom
		reveal := a.scrollTo && y+rowHeight >= chosenY-height-overscan && y <= chosenY+height+overscan
		return i == a.chosen || near || reveal
	}
	ui.Scroll(c.Key("list")).TrackScroll(&a.listScroll).Width(listWidth).Shrink(0).Gap(rowGap).Children(func() {
		for i, clip := range a.clips {
			if !buildRow(i) {
				if a.cardMenuID == clip.ID {
					a.cardMenuID = 0
				}
				// Keep geometry and identity, without text layout for
				// distant rows.
				ui.Box(c.Key(cardPlaceholder(clip.ID))).Height(rowHeight).Shrink(0)
				continue
			}
			if act := a.row(c, i, clip); act != nil {
				action = act
			}
		}
	})
	return action
}

// row shows a clip in the list, and returns what the user asked of it, if
// anything.
func (a *app) row(c *ui.Context, i int, clip *Clip) (action func()) {
	t, tx := c.Theme(), locales.In(c.Locale())
	selected := i == a.chosen
	picked := a.pickOrder(clip.ID) > 0
	hue := kindColor(clip.Kind, t.Dark)

	row := ui.Row(c.Key(clip.ID)).Height(rowHeight).Shrink(0).PaddingX(10).Gap(10).AlignItems(ui.Center).
		Radius(10).Cursor(ui.CursorPointer).Label(kindLabel(clip.Kind, c.Locale()))
	switch {
	case selected:
		row.Background(t.Accent.Alpha(0.13)).Border(1, t.Accent.Alpha(0.55))
	case picked:
		row.Background(t.Accent.Alpha(0.07)).Border(1, t.Accent.Alpha(0.3))
	case row.Hovered():
		row.Background(t.SurfaceHover).Border(1, ui.Transparent)
	default:
		row.Border(1, ui.Transparent)
	}
	// The rows rise one after the other as the panel shows.
	if i < cardsRising {
		e := rise(a.since-riseTime/3-time.Duration(i)*cardStagger, cardRise)
		if e < 1 {
			row.Translate(0, (1-e)*16).Opacity(e)
		}
	}
	row.Transition(rowMotion)

	if selected && a.scrollTo {
		row.ScrollIntoView()
	}
	if row.DoubleClicked() {
		action = func() { a.paste(i, false) }
	} else if row.Clicked() {
		a.clickCard(i, row.ClickModifiers())
		a.input.Focus()
	}
	if menuAction := a.cardMenu(c, row, i, clip); menuAction != nil {
		action = menuAction
	}

	row.Children(func() {
		switch {
		case picked:
			ui.Box(c).Size(30, 30).Shrink(0).Center().Radius(8).Background(t.Accent).Description(tx.PickedCount(len(a.picked))).Children(func() {
				ui.Icon(c, filterCheckIcon).FontSize(14).TextColor(ui.RGB(255, 255, 255))
			})
		case clip.Kind == KindColor:
			ui.Box(c).Size(30, 30).Shrink(0).Radius(8).Background(ui.Hex(strings.TrimSpace(clip.Text))).
				InsetShadow(0, 0, 0, 1, t.Text.Alpha(0.12))
		default:
			ui.Box(c).Size(30, 30).Shrink(0).Center().Radius(8).Background(hue.Alpha(0.16)).Children(func() {
				ui.Icon(c, kindIcon(clip.Kind)).FontSize(14).TextColor(hue)
			})
		}
		ui.Column(c).Grow(1).Basis(0).MinWidth(0).Gap(3).Children(func() {
			ui.Text(c, rowTitle(clip, c.Locale())).FontSize(12.5).FontWeight(500).SingleLine()
			ui.Text(c, ago(clip.UsedAt, c.Locale())+" · "+clip.detail(c.Locale())).FontSize(11).TextColor(t.TextMuted).SingleLine()
		})
		if len(clip.GroupIDs) > 0 || clip.Pinned {
			ui.Icon(c, iconFolder).FontSize(12).Shrink(0).TextColor(kindColor(KindFiles, t.Dark)).Description(tx.Groups())
		}
		if n := a.stackOrder(clip.ID); n > 0 {
			ui.Row(c).Height(18).MinWidth(18).Shrink(0).PaddingX(5).Justify(ui.Center).AlignItems(ui.Center).Radius(9).
				Background(t.Accent).Description(tx.StackCount(n)).Children(func() {
				ui.Text(c, fmt.Sprint(n)).FontSize(10.5).FontWeight(650).TextColor(ui.RGB(255, 255, 255))
			})
		}
		if i < len(digitKeys) {
			keycap(c, cmdCap()+" "+fmt.Sprint(i+1))
		}
	})
	return action
}

// rowTitle is the line a row shows of its clip: the start of its text, on
// one line, the names of its files, or what kind of picture it is.
func rowTitle(clip *Clip, locale string) string {
	switch clip.Kind {
	case KindImage:
		return kindLabel(clip.Kind, locale)
	case KindFiles:
		return strings.Join(baseNames(clip.Files()), ", ")
	}
	s := clip.Text
	if len(s) > 400 {
		end := 400
		for end > 0 && !utf8.RuneStart(s[end]) {
			end--
		}
		s = s[:end]
	}
	return strings.Join(strings.Fields(s), " ")
}

const thumbCacheLimit = 24

// thumb is the picture of an image clip, on the GPU alone once it showed.
type thumb struct {
	cancel context.CancelFunc

	bitmap           *ui.Bitmap // nil where the picture does not decode
	used             uint64
	pinned           bool
	pixelsReleased   bool
	releaseScheduled bool
}

// thumb queues a picture once it is near the view. Only the UI thread
// publishes or paints bitmaps; workers own them until Update hands them over.
func (a *app) thumb(clip *Clip) *thumb {
	th, ok := a.thumbs[clip.ID]
	if !ok {
		ctx, cancel := context.WithCancel(context.Background())
		th = &thumb{cancel: cancel}
		a.thumbs[clip.ID] = th
		id, store, panel, slots := clip.ID, a.store, a.panel, a.thumbSlots
		go func() {
			defer cancel()
			bitmap, err := loadThumb(ctx, store, id, slots)
			if err != nil {
				return
			}
			panel.Update(func() {
				if a.thumbs[id] == th {
					th.bitmap = bitmap
				}
			})
		}()
	}
	a.thumbClock++
	th.used, th.pinned = a.thumbClock, true
	a.trimThumbs()
	return th
}

// InView refers to a committed frame, so the image was already painted.
// One timed build handles a settled image even when later frames only paint.
func (th *thumb) releasePixels(c *ui.Context, image ui.Element) {
	if th.pixelsReleased {
		return
	}
	if image.InView() {
		th.bitmap.ReleasePixels()
		th.pixelsReleased = true
	} else if !th.releaseScheduled {
		th.releaseScheduled = true
		c.After(32 * time.Millisecond)
	}
}

func (a *app) trimThumbs() {
	for len(a.thumbs) > max(thumbCacheLimit, a.thumbLimit) {
		var oldest *thumb
		var oldestID int64
		for id, th := range a.thumbs {
			if !th.pinned && (oldest == nil || th.used < oldest.used) {
				oldest, oldestID = th, id
			}
		}
		if oldest == nil {
			return
		}
		oldest.cancel()
		delete(a.thumbs, oldestID)
	}
}

func loadThumb(ctx context.Context, store *Store, id int64, slots chan struct{}) (*ui.Bitmap, error) {
	select {
	case slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-slots }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data := store.Thumb(id)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bitmap, err := ui.DecodeBitmap(data)
	if err == nil {
		err = ctx.Err()
	}
	return bitmap, err
}

func (a *app) clearThumbs() {
	for _, th := range a.thumbs {
		th.cancel()
	}
	a.thumbs = map[int64]*thumb{}
	a.thumbClock = 0
}

// keycap draws a key, as on a keyboard.
func keycap(c *ui.Context, key string) {
	t := c.Theme()
	ui.Row(c).Height(19).MinWidth(19).PaddingX(5).Justify(ui.Center).AlignItems(ui.Center).Radius(5).
		Background(t.SurfaceHover).Border(1, t.Border).Children(func() {
		ui.Text(c, key).FontSize(10.5).FontWeight(550).TextColor(t.TextMuted)
	})
}

// footer lists the keys of the panel.
func (a *app) footer(c *ui.Context) {
	t := c.Theme()
	tx := locales.In(c.Locale())
	width, _ := c.Size()
	cmd := cmdCap()
	hints := []struct {
		keys  []string
		label string
	}{
		{[]string{"↑", "↓"}, tx.Select()},
		{[]string{"↵"}, tx.Paste()},
		{[]string{"⇧", "↵"}, tx.PastePlainHint()},
		{[]string{cmd, "↵"}, tx.CopyOnly()},
		{[]string{cmd, "P"}, tx.AddToGroups()},
		{[]string{cmd, "⌫"}, tx.Delete()},
		{[]string{"esc"}, tx.Close()},
	}
	hint := func(keys []string, label string) {
		ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
			for _, key := range keys {
				keycap(c, key)
			}
			ui.Text(c, label).FontSize(11).TextColor(t.TextMuted).MarginStart(3).SingleLine()
		})
	}
	ui.Row(c).Padding(0, 22, 16, 22).Gap(18).AlignItems(ui.Center).Children(func() {
		if len(a.picked) > 1 {
			a.batchHints(c, hint)
		}
		for i, h := range hints {
			if len(a.picked) > 1 {
				break
			}
			if width < 900 && (i == 2 || i == 3 || i == 5) {
				continue
			}
			hint(h.keys, h.label)
			if i == 4 && width >= 1100 && len(a.stack) == 0 {
				hint([]string{cmd, "S"}, tx.AddToStack())
			}
			if i == 4 && width >= 1300 {
				hint([]string{cmd, tx.Click()}, tx.MultiSelect())
			}
		}
		ui.Spacer(c)
		if len(a.stack) > 0 {
			a.stackStatus(c, width)
		}
		if a.shortcut != "" && width >= 1100 && (len(a.stack) == 0 || width >= 1400) {
			hint(keycaps(a.shortcut), tx.ShowHide())
		}
	})
}

// settingsDialog edits the panel and stack shortcuts and the language.
func (a *app) settingsDialog(c *ui.Context) {
	t := c.Theme()
	tx := locales.In(c.Locale())
	ui.DialogBase(c, &a.settingsOpen, func(backdrop, panel ui.Element) {
		width, height := c.Size()
		backdrop.Background(ui.RGBA(0, 0, 0, 0.4))
		popupSurface(c, panel).Padding(16).Radius(12).MaxWidth(width - 32).MaxHeight(height - 24)
		ui.Column(c).Width(560).MaxWidth(max(0, width-64)).Gap(12).Children(func() {
			ui.Column(c).Gap(4).Children(func() {
				ui.Text(c, tx.Settings()).FontSize(16).FontWeight(650)
				ui.Text(c, tx.ShortcutInstructions()).FontSize(12).TextColor(t.TextMuted)
			})

			for _, stack := range []bool{false, true} {
				title, hint, resetLabel := tx.ShowHidePanel(), tx.ShowHidePanelHint(), tx.ResetPanelShortcut()
				if stack {
					title = "Stack · " + tx.PasteNext()
					hint = tx.StackShortcutHint()
					resetLabel = tx.ResetStackShortcut()
				}
				ui.Row(c.Key(stack)).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Column(c).Grow(1).Basis(0).Gap(2).Children(func() {
						ui.Text(c, title).FontSize(13).FontWeight(550)
						ui.Text(c, hint).FontSize(11.5).TextColor(t.TextMuted)
					})
					a.recorder(c, stack)
					dialogButton(c, "reset-shortcut", tx.ResetDefault(), false).Label(resetLabel).OnClick(func() {
						if stack {
							a.resetStackShortcut()
						} else {
							a.resetShortcut()
						}
					})
				})
			}
			if a.recordError != "" {
				ui.Text(c, locales.Catalog.Format(c.Locale(), a.recordError, nil)).FontSize(12).TextColor(t.Danger)
			}
			a.languageOptions(c)

			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Spacer(c)
				if dialogButton(c, "finish-settings", tx.Done(), true).Clicked() {
					a.settingsOpen = false
				}
			})
		})
	})
	if !a.settingsOpen {
		if a.recording {
			a.stopRecording("") // dismissed while it waited
		}
		a.recordError = ""
	}
}

// recorder shows the shortcut, or the keys held as it waits for a new one.
func (a *app) recorder(c *ui.Context, stack bool) {
	t := c.Theme()
	tx := locales.In(c.Locale())
	label, shortcut := tx.PanelShortcut(), a.shortcut
	if stack {
		label, shortcut = tx.StackShortcut(), a.stackShortcut()
	}
	recording := a.recording && a.recordingStack == stack
	box := ui.Row(c.Key("recorder")).Width(210).Height(40).PaddingX(12).Gap(5).
		Justify(ui.Center).AlignItems(ui.Center).Radius(11).Background(t.Background).
		Cursor(ui.CursorPointer).Focusable().FocusRing(false).Label(label)
	if recording {
		box.Border(1, t.Accent).Shadow(0, 0, 0, 3, t.Accent.Alpha(0.22)).Focus().
			HandleInput(func(ev ui.InputEvent) bool {
				if ev.Kind != ui.InputKeyDown {
					return false
				}
				key := ev.Key
				// Carbon registers Mac shortcuts by physical key, not input source.
				if runtime.GOOS == "darwin" && ev.PhysicalKey != ui.KeyUnknown {
					key = ev.PhysicalKey
				}
				if ev.Mods == 0 && ev.Key == ui.KeyEscape {
					a.stopRecording("")
				} else if shortcut := accelerator(ev.Mods, key); shortcut != "" {
					a.stopRecording(shortcut)
				}
				return true
			})
	} else {
		box.Border(1, t.Border)
		if box.Hovered() {
			box.Border(1, t.TextMuted.Alpha(0.6))
		}
		if box.Clicked() {
			a.startRecording(stack)
		}
	}
	box.Transition(fadeMotion).Children(func() {
		switch held := keycaps(strings.Join(modifierNames(c.Modifiers()), "+")); {
		case recording && len(held) > 0:
			for _, key := range held {
				keycap(c, key)
			}
			ui.Text(c, "…").FontSize(12).TextColor(t.TextMuted)
		case recording:
			ui.Text(c, tx.PressShortcut()).FontSize(12).TextColor(t.Accent)
		case shortcut == "":
			ui.Text(c, tx.ShortcutUnset()).FontSize(12).TextColor(t.TextMuted)
		default:
			for _, key := range keycaps(shortcut) {
				keycap(c, key)
			}
		}
	})
}
