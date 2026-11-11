package main

import (
	"context"
	"fmt"
	"net/url"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

const (
	cardWidth = 240 // DIPs
	cardGap   = 14
)

// A placeholder must not inherit the full card's transition snapshot, which
// can retain the image even after its application cache entry is evicted.
type cardPlaceholder int64

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
	case mods == 0 && key == ui.KeyEscape:
		if a.query == "" {
			return false // left to the panel, which hides
		}
		a.query, a.chosen, a.scrollTo = "", 0, true
		a.reload()
	case mods == 0 && key == ui.KeyLeft:
		a.choose(a.chosen - 1)
	case mods == 0 && key == ui.KeyRight:
		a.choose(a.chosen + 1)
	case mods == 0 && key == ui.KeyEnter:
		a.paste(a.chosen, false)
	case mods == ui.Shift && key == ui.KeyEnter:
		a.paste(a.chosen, true)
	case mods == ui.Cmd && key == ui.KeyEnter:
		a.copyOnly(a.chosen)
	case mods == ui.Cmd && key == ui.KeyBackspace:
		a.remove(a.chosen)
	case mods == ui.Cmd && key == ui.KeyP:
		a.togglePin(a.chosen)
	case mods == ui.Cmd && key == ui.KeyComma:
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
	if !a.settingsOpen {
		type combo struct {
			mods ui.Modifiers
			key  ui.Key
		}
		combos := []combo{
			{0, ui.KeyLeft}, {0, ui.KeyRight}, {0, ui.KeyEnter}, {ui.Shift, ui.KeyEnter},
			{ui.Cmd, ui.KeyEnter}, {ui.Cmd, ui.KeyBackspace}, {ui.Cmd, ui.KeyP}, {ui.Cmd, ui.KeyComma},
		}
		if a.query != "" {
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
		a.strip(c)
		a.footer(c)
		a.settingsDialog(c)
	})
	a.scrollTo = false
}

// header has the search field, the filters, the number of clips and the
// settings button.
func (a *app) header(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Padding(18, 22, 6, 22).Gap(16).AlignItems(ui.Center).Children(func() {
		ui.Row(c).Gap(9).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(24, 24).Center().Radius(7).Gradient(t.Accent, kindColor(KindImage, t.Dark), 135).
				Shadow(0, 4, 10, -2, t.Accent.Alpha(0.5)).Children(func() {
				ui.Icon(c, iconLogo).FontSize(15).TextColor(ui.RGB(255, 255, 255))
			})
			ui.Text(c, "Copied").FontSize(15).FontWeight(650).LetterSpacing(-0.2)
		})

		field := ui.Row(c.Key("search")).Width(300).Height(34).PaddingX(11).Gap(8).AlignItems(ui.Center).
			Radius(10).Background(t.Surface)
		if a.input.Focused(c) {
			field.Border(1, t.Accent).Shadow(0, 0, 0, 3, t.Accent.Alpha(0.2))
		} else {
			field.Border(1, t.Border)
		}
		field.Transition(fadeMotion).Children(func() {
			ui.Icon(c, iconSearch).FontSize(14).TextColor(t.TextMuted)
			input := ui.TextInputBase(c.Key("input"), &a.query).Bind(&a.input).Grow(1).FontSize(13).
				Placeholder("搜索剪贴板历史").Label("搜索").
				OnTextKey(func(k ui.TextKey) bool { return a.key(k.Mods, k.Key) })
			if input.Changed() {
				a.chosen, a.scrollTo = 0, true
				a.reload()
			}
		})

		a.filters(c)

		ui.Spacer(c)
		if a.paused.Load() {
			ui.Row(c).Height(22).PaddingX(9).AlignItems(ui.Center).Radius(11).Background(t.Warning.Alpha(0.16)).Children(func() {
				ui.Text(c, "已暂停记录").FontSize(11).TextColor(t.Warning)
			})
		}
		ui.Textf(c, "%d 条", len(a.clips)).FontSize(11.5).TextColor(t.TextMuted)

		gear := ui.Row(c.Key("settings")).Size(30, 30).Center().Radius(9).Cursor(ui.CursorPointer).
			Label("快捷键设置").Tooltip("快捷键设置")
		color := t.TextMuted
		if gear.Hovered() {
			gear.Background(t.SurfaceHover)
			color = t.Text
		}
		if gear.Clicked() {
			a.settingsOpen = true
		}
		gear.Transition(fadeMotion).Children(func() {
			ui.Icon(c, iconSettings).FontSize(15).TextColor(color)
		})
	})
}

// strip is the row of cards, which scrolls sideways.
func (a *app) strip(c *ui.Context) {
	t := c.Theme()
	if len(a.clips) == 0 {
		ui.Column(c).Grow(1).Center().Gap(6).Children(func() {
			msg, hint := "还没有记录", "复制一段文字、一张图片或几个文件试试"
			if a.query != "" || a.filter != FilterAll {
				msg, hint = "没有匹配的记录", "换个关键词或筛选条件"
			}
			ui.Text(c, msg).FontSize(15).FontWeight(600)
			ui.Text(c, hint).FontSize(12).TextColor(t.TextMuted)
		})
		return
	}
	// What a card asks for changes the clips, so it waits for them all to
	// be built.
	var action func()
	width, _ := c.Size()
	const stride = cardWidth + cardGap
	const overscan = 2 * stride
	left, right := a.stripScroll.X-overscan, a.stripScroll.X+width+overscan
	chosenX := float32(a.chosen * stride)
	buildCard := func(i int) bool {
		x := float32(i * stride)
		near := x+cardWidth >= left && x <= right
		reveal := a.scrollTo && x+cardWidth >= chosenX-width-overscan && x <= chosenX+width+overscan
		return i == a.chosen || near || reveal
	}
	// Protect the entire current window before loading anything, so an LRU
	// insertion cannot evict another card that this frame is about to show.
	for _, th := range a.thumbs {
		th.pinned = false
	}
	required := 0
	for i, clip := range a.clips {
		if clip.Kind == KindImage && buildCard(i) {
			required++
			if th := a.thumbs[clip.ID]; th != nil {
				th.pinned = true
			}
		}
	}
	a.thumbLimit = max(thumbCacheLimit, required)
	a.trimThumbs()
	ui.ScrollHorizontal(c.Key("strip")).TrackScroll(&a.stripScroll).HandleInput(a.scrollStrip).
		Grow(1).Padding(18, 22, 20, 22).Gap(cardGap).AlignItems(ui.Stretch).Children(func() {
		for i, clip := range a.clips {
			if !buildCard(i) {
				// Keep geometry and identity, without text layout, shadows
				// or image work for distant cards.
				ui.Box(c.Key(cardPlaceholder(clip.ID))).Width(cardWidth).Shrink(0)
				continue
			}
			if act := a.card(c, i, clip); act != nil {
				action = act
			}
		}
	})
	if action != nil {
		action()
	}
}

// scrollStrip turns a vertical wheel sideways. Precise gestures and native
// horizontal input keep the platform's direction, momentum and axis handling.
func (a *app) scrollStrip(ev ui.InputEvent) bool {
	if ev.Kind != ui.InputScroll || ev.Precise || ev.DX != 0 || ev.DY == 0 || ev.Mods&ui.Shift != 0 {
		return false
	}
	s := &a.stripScroll
	from := s.X
	if s.Scrolling() && (ev.DY > 0) == (a.stripWheelTarget > s.X) {
		from = a.stripWheelTarget
	}
	target := max(0, min(from+ev.DY, s.MaxX))
	if target == s.X && !s.Scrolling() {
		return false
	}
	// Accumulate repeated notches, but reverse from the current position so
	// unfinished travel does not swallow a change of direction at an edge.
	a.stripWheelTarget = target
	s.ScrollTo(target, 0, ui.Animation{Duration: 150 * time.Millisecond, Ease: ui.EaseOut})
	return true
}

// card shows a clip, and returns what the user asked of it, if anything.
func (a *app) card(c *ui.Context, i int, clip *Clip) (action func()) {
	t := c.Theme()
	selected := i == a.chosen
	hue := kindColor(clip.Kind, t.Dark)
	shade := ui.RGB(0, 0, 0)
	depth := float32(0.16) // of the shadows, deeper on dark
	if t.Dark {
		depth = 0.5
	}

	// Previews clip their own contents inside the card's padding. Clipping
	// this animated rounded ancestor too creates redundant retained masks.
	card := ui.Column(c.Key(clip.ID)).Width(cardWidth).Shrink(0).Radius(16).
		Background(t.Surface).Cursor(ui.CursorPointer).Label(kindLabel(clip.Kind))
	lift := float32(0)
	switch {
	case selected:
		lift = 8
		card.Border(1, t.Accent).Shadow(0, 0, 0, 2, t.Accent.Alpha(0.9)).
			Shadow(0, 18, 36, -10, t.Accent.Alpha(0.45)).Shadow(0, 10, 24, -8, shade.Alpha(depth))
	case card.Hovered():
		lift = 3
		card.Border(1, t.TextMuted.Alpha(0.45)).Shadow(0, 10, 24, -8, shade.Alpha(depth))
	default:
		card.Border(1, t.Border).Shadow(0, 1, 2, 0, shade.Alpha(depth/4))
	}
	// The card rises to its lift, and from below as the panel shows.
	card.AnimatePose(ui.Pose{Y: -lift}, ui.Snappy)
	y := float32(0)
	if i < cardsRising {
		e := rise(a.since-riseTime/3-time.Duration(i)*cardStagger, cardRise)
		if e < 1 {
			y += (1 - e) * 36
			card.Opacity(e)
		}
	}
	card.Translate(0, y).Transition(cardMotion)

	if selected && a.scrollTo {
		card.ScrollIntoView()
	}
	if card.DoubleClicked() {
		action = func() { a.paste(i, false) }
	} else if card.Clicked() {
		a.chosen = i
		a.input.Focus()
	}
	card.PopoverMenu(func(m *ui.Menu) {
		if m.Item("粘贴").Chosen() {
			a.paste(i, false)
		}
		if clip.Kind != KindImage && clip.Kind != KindFiles && m.Item("粘贴为纯文本").Chosen() {
			a.paste(i, true)
		}
		if m.Item("仅复制").Chosen() {
			a.copyOnly(i)
		}
		m.Separator()
		if m.Item("收藏").Checked(clip.Pinned).Chosen() {
			a.togglePin(i)
		}
		if m.Item("删除").Danger().Chosen() {
			a.remove(i)
		}
	})

	card.Children(func() {
		ui.Row(c).Padding(12, 14, 10, 12).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(24, 24).Center().Radius(8).Background(hue.Alpha(0.16)).Children(func() {
				ui.Icon(c, kindIcon(clip.Kind)).FontSize(13).TextColor(hue)
			})
			ui.Text(c, kindLabel(clip.Kind)).FontSize(12).FontWeight(600)
			ui.Spacer(c)
			if clip.Pinned {
				ui.Icon(c, iconPin).FontSize(12).TextColor(kindColor(KindFiles, t.Dark))
			}
			ui.Text(c, ago(clip.UsedAt)).FontSize(11).TextColor(t.TextMuted)
		})

		a.preview(c, i, clip, hue)

		ui.Row(c).Padding(10, 14, 12, 14).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, clip.detail()).FontSize(11).TextColor(t.TextMuted).SingleLine().Grow(1)
			if i < len(digitKeys) {
				keycap(c, cmdCap()+" "+fmt.Sprint(i+1))
			}
		})
	})
	return action
}

// preview is the middle of a card: the start of a text, a link, a
// picture, a color, or the names of files.
func (a *app) preview(c *ui.Context, i int, clip *Clip, hue ui.Color) {
	t := c.Theme()
	switch clip.Kind {
	case KindImage:
		// Pictures are read as their cards come near the view.
		box := ui.Box(c).Grow(1).Basis(0).Margin(0, 12).Radius(10).Clip().Background(t.SurfaceHover)
		box.Children(func() {
			th := a.thumb(clip)
			if th.bitmap != nil {
				image := ui.Image(c, th.bitmap).Fill().Fit(ui.Cover)
				th.releasePixels(c, image)
			}
		})
	case KindColor:
		value := strings.TrimSpace(clip.Text)
		ui.Column(c).Grow(1).Basis(0).Padding(0, 12).Gap(8).Children(func() {
			ui.Box(c).Grow(1).Radius(10).Background(ui.Hex(value)).
				InsetShadow(0, 0, 0, 1, t.Text.Alpha(0.12))
			ui.Text(c, strings.ToUpper(value)).FontSize(12.5).FontWeight(600).LetterSpacing(0.4).TextAlign(ui.Center)
		})
	case KindLink:
		link := strings.TrimSpace(clip.Text)
		host := link
		if u, err := url.Parse(link); err == nil && u.Host != "" {
			host = u.Host
		}
		ui.Column(c).Grow(1).Basis(0).Padding(2, 14).Gap(5).Clip().Children(func() {
			ui.Text(c, host).FontSize(14).FontWeight(600).TextColor(hue).SingleLine()
			ui.Text(c, link).FontSize(11.5).LineHeight(1.5).MaxLines(5).TextColor(t.TextMuted)
		})
	case KindFiles:
		const shown = 5
		names := baseNames(clip.Files())
		ui.Column(c).Grow(1).Basis(0).Padding(2, 14).Gap(7).Clip().Children(func() {
			for _, name := range names[:min(len(names), shown)] {
				ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, iconFile).FontSize(13).TextColor(hue)
					ui.Text(c, name).FontSize(12.5).SingleLine().Grow(1)
				})
			}
			if more := len(names) - shown; more > 0 {
				ui.Textf(c, "还有 %d 个…", more).FontSize(11).TextColor(t.TextMuted)
			}
		})
	default:
		text := strings.ReplaceAll(strings.TrimSpace(clip.Text), "\t", "  ")
		ui.Column(c).Grow(1).Basis(0).Padding(2, 14).Clip().Children(func() {
			ui.Text(c, text).FontSize(12.5).LineHeight(1.55).MaxLines(7).TextColor(t.Text.Alpha(0.86))
		})
	}
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
	cmd := cmdCap()
	hints := []struct {
		keys  []string
		label string
	}{
		{[]string{"←", "→"}, "选择"},
		{[]string{"↵"}, "粘贴"},
		{[]string{"⇧", "↵"}, "纯文本粘贴"},
		{[]string{cmd, "↵"}, "仅复制"},
		{[]string{cmd, "P"}, "收藏"},
		{[]string{cmd, "⌫"}, "删除"},
		{[]string{"esc"}, "关闭"},
	}
	hint := func(keys []string, label string) {
		ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
			for _, key := range keys {
				keycap(c, key)
			}
			ui.Text(c, label).FontSize(11).TextColor(t.TextMuted).MarginStart(3)
		})
	}
	ui.Row(c).Padding(0, 22, 16, 22).Gap(18).AlignItems(ui.Center).Children(func() {
		for _, h := range hints {
			hint(h.keys, h.label)
		}
		ui.Spacer(c)
		if a.shortcut != "" {
			hint(keycaps(a.shortcut), "显示 / 隐藏")
		}
	})
}

// settingsDialog sets the shortcut that shows the panel: a click on the
// shortcut waits for the keys of another.
func (a *app) settingsDialog(c *ui.Context) {
	t := c.Theme()
	ui.Modal(c, &a.settingsOpen, func() {
		ui.Column(c).Width(440).Gap(16).Children(func() {
			ui.Column(c).Gap(4).Children(func() {
				ui.Text(c, "快捷键").FontSize(16).FontWeight(650)
				ui.Text(c, "点击右侧的快捷键，然后按下新的组合键；按 esc 取消。").FontSize(12).TextColor(t.TextMuted)
			})

			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).Gap(2).Children(func() {
					ui.Text(c, "显示 / 隐藏面板").FontSize(13).FontWeight(550)
					ui.Text(c, "在任何应用里呼出剪贴板历史").FontSize(11.5).TextColor(t.TextMuted)
				})
				a.recorder(c)
			})
			if a.recordError != "" {
				ui.Text(c, a.recordError).FontSize(12).TextColor(t.Danger)
			}

			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				if ui.Button(c, "恢复默认").Clicked() {
					if a.recording {
						a.stopRecording("")
					}
					a.resetShortcut()
				}
				ui.Spacer(c)
				if ui.PrimaryButton(c, "完成").Clicked() {
					a.settingsOpen = false
				}
			})
		})
	})
	if !a.settingsOpen && a.recording {
		a.stopRecording("") // dismissed while it waited
	}
}

// recorder shows the shortcut, or the keys held as it waits for a new one.
func (a *app) recorder(c *ui.Context) {
	t := c.Theme()
	box := ui.Row(c.Key("recorder")).Width(210).Height(40).PaddingX(12).Gap(5).
		Justify(ui.Center).AlignItems(ui.Center).Radius(11).Background(t.Background).
		Cursor(ui.CursorPointer).Focusable().FocusRing(false).Label("显示 / 隐藏面板的快捷键")
	if a.recording {
		box.Border(1, t.Accent).Shadow(0, 0, 0, 3, t.Accent.Alpha(0.22)).Focus().
			HandleInput(func(ev ui.InputEvent) bool {
				if ev.Kind != ui.InputKeyDown {
					return false
				}
				if ev.Mods == 0 && ev.Key == ui.KeyEscape {
					a.stopRecording("")
				} else if shortcut := accelerator(ev.Mods, ev.Key); shortcut != "" {
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
			a.startRecording()
		}
	}
	box.Transition(fadeMotion).Children(func() {
		switch held := keycaps(strings.Join(modifierNames(c.Modifiers()), "+")); {
		case a.recording && len(held) > 0:
			for _, key := range held {
				keycap(c, key)
			}
			ui.Text(c, "…").FontSize(12).TextColor(t.TextMuted)
		case a.recording:
			ui.Text(c, "请按下新的快捷键…").FontSize(12).TextColor(t.Accent)
		case a.shortcut == "":
			ui.Text(c, "未设置，点击录制").FontSize(12).TextColor(t.TextMuted)
		default:
			for _, key := range keycaps(a.shortcut) {
				keycap(c, key)
			}
		}
	})
}
