package main

import (
	"math"
	"time"

	"copied/locales"

	"github.com/egoist/mygo/ui"
)

const (
	filterChipWidth  = 48
	filterChipStride = filterChipWidth + 2
	filterFlowTime   = 480 * time.Millisecond
)

var (
	filterAllIcon = svg(`<rect x="3" y="3" width="7" height="7" rx="2"/><rect x="14" y="3" width="7" height="7" rx="2"/><rect x="3" y="14" width="7" height="7" rx="2"/><rect x="14" y="14" width="7" height="7" rx="2"/>`)
	filterCheckIcon = svg(`<path d="m5 12 4 4L19 6"/>`)
)

// filterFlow is a value snapshot. Painting samples it without changing app
// state, so repaint-only frames and interrupted transitions use the same path.
type filterFlow struct {
	left, right float32
	target      float32
	targetWidth float32
	started     time.Time
}

func (f filterFlow) width() float32 {
	if f.targetWidth > 0 {
		return f.targetWidth
	}
	return filterChipWidth
}

func (f filterFlow) edges(now time.Time) (left, right float32, moving bool) {
	if f.started.IsZero() || now.Sub(f.started) >= filterFlowTime {
		return f.target, f.target + f.width(), false
	}
	t := max(0, float64(now.Sub(f.started))/float64(filterFlowTime))
	// A small overshoot moves the center; the body stretches then contracts.
	u := t - 1
	progress := float32(1 + 1.6*u*u*u + 0.6*u*u)
	start := (f.left + f.right) / 2
	end := f.target + f.width()/2
	center := start + (end-start)*progress
	stretch := float32(math.Sin(math.Pi*t)) * min(float32(math.Abs(float64(end-start)))*0.24, 26)
	width := f.width() + (f.right-f.left-f.width())*float32(math.Pow(1-t, 3)) + stretch
	return center - width/2, center + width/2, true
}

func (a *app) filters(c *ui.Context) {
	t, tx := c.Theme(), locales.In(c.Locale())
	now := c.Now()
	labels := filterLabelsFor(c.Locale())
	sel := ui.SelectBaseOpen(c.Key(headerMenuKey{a.searchSession, "type"}), &a.filter, &a.typeMenuOpen)
	trigger := sel.Trigger.Row().Width(118).Height(34).Shrink(0).PaddingX(8).Gap(5).
		AlignItems(ui.Center).Radius(10).Border(1, t.Border).Background(t.Surface).
		Cursor(ui.CursorPointer).Label(tx.ClipboardTypes()).Value(labels[a.filter]).
		HandleInput(a.typeGridInput).ObserveInput(a.typeGridObserve).
		OnChange(func() { a.setFilter(a.filter, now) })
	if a.typeMenuOpen || trigger.Hovered() || trigger.FocusVisible() {
		trigger.Border(1, t.Accent.Alpha(0.4)).Background(t.Accent.Alpha(0.05))
	}
	if !c.Preferences().ReduceMotion {
		trigger.Transition(fadeMotion)
	}
	trigger.Children(func() {
		ui.Icon(c, filterIcon(a.filter)).FontSize(14).Shrink(0).TextColor(filterColor(a.filter, t))
		ui.Text(c, tx.TypeFilter(labels[a.filter])).FontSize(11.5).FontWeight(550).TextColor(t.Text).SingleLine().Grow(1)
		ui.Icon(c, iconChevron).FontSize(10).Shrink(0).TextColor(t.TextMuted)
	})
	if a.typeMovePending {
		sel.Highlight(a.typeHighlight)
		a.typeMovePending = false
	}
	sel.Popup(func(panel ui.Element) {
		panel.Key(headerMenuKey{a.searchSession, "type-popup"})
		popupSurface(c, panel).Width(272).Padding(10).Radius(14).Margin(7, 0, 0, 0).Gap(8)
		if !c.Preferences().ReduceMotion {
			panel.TransformOrigin(0, 0).Transition(dropdownMotion)
		}
		ui.Text(c, tx.ClipboardTypes()).FontSize(10.5).FontWeight(550).TextColor(t.TextMuted).Padding(2, 4)
		ui.Grid(c).Columns(2).Gap(6).Children(func() {
			for i, label := range labels[:int(FilterPinned)] {
				filter := Filter(i)
				ink := filterColor(filter, t)
				item := sel.Item(filter).Height(48).PaddingX(9).Gap(8).AlignItems(ui.Center).
					Radius(10).Cursor(ui.CursorPointer).Label(label).Border(1, ui.Transparent)
				if item.Highlighted() {
					a.typeHighlight = filter
					item.Background(t.SurfaceHover).Border(1, t.Border.Alpha(0.7))
				}
				selected := a.filter == filter
				text := t.Text
				if selected {
					item.Gradient(t.Accent.Alpha(0.12), t.Accent.Alpha(0.04), 135).Border(1, t.Accent.Alpha(0.3))
					text = t.Accent
				}
				item.Children(func() {
					ui.Box(c).Size(28, 28).Shrink(0).Center().Radius(8).Background(ink.Alpha(0.11)).Children(func() {
						ui.Icon(c, filterIcon(filter)).FontSize(15).TextColor(ink)
					})
					ui.Text(c, label).FontSize(12).FontWeight(550).TextColor(text).SingleLine().Grow(1)
					if selected {
						ui.Icon(c, filterCheckIcon).Absolute().Top(4).Right(4).Size(10, 10).TextColor(t.Accent)
					}
				})
			}
		})
	})
}

// The select takes Up and Down as its own shortcuts, moving one option at a
// time, before HandleInput sees them. Observing them first lets the grid
// move a whole row; the Highlight this queues overrides the select's step.
func (a *app) typeGridObserve(ev ui.InputEvent) {
	if ev.Key == ui.KeyUp || ev.Key == ui.KeyDown {
		a.typeGridInput(ev)
	}
}

// The popup is a two-column list. Keep spatial arrow navigation without
// retaining its build-scoped SelectParts in an input callback.
func (a *app) typeGridInput(ev ui.InputEvent) bool {
	if !a.typeMenuOpen || ev.Kind != ui.InputKeyDown || ev.Mods != 0 {
		return false
	}
	next := int(a.typeHighlight)
	switch ev.Key {
	case ui.KeyLeft:
		if next%2 == 1 {
			next--
		}
	case ui.KeyRight:
		if next%2 == 0 {
			next++
		}
	case ui.KeyUp:
		if next >= 2 {
			next -= 2
		}
	case ui.KeyDown:
		if next+2 < int(FilterPinned) {
			next += 2
		}
	default:
		return false
	}
	a.typeHighlight, a.typeMovePending = Filter(next), true
	return true
}

func filterColor(filter Filter, t *ui.Theme) ui.Color {
	switch filter {
	case FilterText:
		return kindColor(KindText, t.Dark)
	case FilterCode:
		return kindColor(KindCode, t.Dark)
	case FilterLink:
		return kindColor(KindLink, t.Dark)
	case FilterColor:
		return kindColor(KindColor, t.Dark)
	case FilterImage:
		return kindColor(KindImage, t.Dark)
	case FilterFiles:
		return kindColor(KindFiles, t.Dark)
	}
	return t.Accent
}

func filterIcon(filter Filter) *ui.SVG {
	switch filter {
	case FilterText:
		return iconText
	case FilterCode:
		return iconCode
	case FilterLink:
		return iconLink
	case FilterColor:
		return iconColor
	case FilterImage:
		return iconImage
	case FilterFiles:
		return iconFile
	}
	return filterAllIcon
}

func drawFilterFlow(t *ui.Theme, flow filterFlow, still bool) func(*ui.Painter, ui.Rect) {
	return func(p *ui.Painter, r ui.Rect) {
		left, right, moving := flow.edges(p.Now())
		if still {
			left, right, moving = flow.target, flow.target+flow.width(), false
		}
		if moving {
			p.AnimationFrame() // Repaint, without rebuilding the card list.
		}
		stretch := max(0, min((right-left-flow.width())/26, 1))
		x, width := r.X+left, right-left
		height := r.H - 2*stretch
		y, radius := r.Y+(r.H-height)/2, height/2
		body := ui.Rect{X: x, Y: y, W: width, H: height}
		// Rounded fills use GPU primitives instead of caching a new path mask
		// for each animated shape. The liquid stretch and highlight stay.
		p.FillGradient(body, ui.LinearGradient{
			From: t.Accent.Alpha(0.14), To: t.Accent.Alpha(0.32), Angle: 160,
		}, radius)
		white := ui.RGB(255, 255, 255)
		p.Stroke(body, t.Accent.Alpha(0.25), radius, 0.8)
		p.FillGradient(ui.Rect{X: x + 3, Y: y + 1, W: width - 6, H: height * 0.45},
			ui.LinearGradient{From: white.Alpha(0.3), To: white.Alpha(0), Angle: 180}, height*0.225)
	}
}
