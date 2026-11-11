package main

import (
	"math"
	"time"

	"github.com/egoist/mygo/ui"
)

const (
	filterChipWidth  = 48
	filterChipStride = filterChipWidth + 2
	filterFlowTime   = 480 * time.Millisecond
)

// filterFlow is a value snapshot. Painting samples it without changing app
// state, so repaint-only frames and interrupted transitions use the same path.
type filterFlow struct {
	left, right float32
	target      float32
	started     time.Time
}

func (f filterFlow) edges(now time.Time) (left, right float32, moving bool) {
	if f.started.IsZero() || now.Sub(f.started) >= filterFlowTime {
		return f.target, f.target + filterChipWidth, false
	}
	t := max(0, float64(now.Sub(f.started))/float64(filterFlowTime))
	// A small overshoot moves the center; the body stretches then contracts.
	u := t - 1
	progress := float32(1 + 1.6*u*u*u + 0.6*u*u)
	start := (f.left + f.right) / 2
	end := f.target + filterChipWidth/2
	center := start + (end-start)*progress
	stretch := float32(math.Sin(math.Pi*t)) * min(float32(math.Abs(float64(end-start)))*0.24, 26)
	width := filterChipWidth + (f.right-f.left-filterChipWidth)*float32(math.Pow(1-t, 3)) + stretch
	return center - width/2, center + width/2, true
}

func (a *app) filters(c *ui.Context) {
	t := c.Theme()
	flow, still := a.filterFlow, c.Preferences().ReduceMotion
	now := c.Now()
	ui.RadioGroup(c.Key("filters"), func() {
		for i, label := range filterLabels {
			filter := Filter(i)
			chip := ui.RadioBase(c.Key(filter), &a.filter, filter).Size(filterChipWidth, 28).
				Center().Radius(14).Cursor(ui.CursorPointer).Label(label)
			keyboard := chip.FocusVisible()
			chip.OnChange(func() {
				a.setFilter(filter, now)
				if !keyboard {
					a.input.Focus()
				}
			})
			color := t.TextMuted
			if a.filter == filter {
				color = t.Accent
				if t.Dark {
					color = t.Text
				}
			} else if chip.Hovered() {
				chip.Background(t.SurfaceHover.Alpha(0.55))
				color = t.Text
			}
			chip.Children(func() {
				ui.Text(c, label).FontSize(12).FontWeight(550).TextColor(color)
			})
		}
	}).Row().Gap(2).Height(28).Label("剪贴板类型").Draw(func(p *ui.Painter, r ui.Rect) {
		left, right, moving := flow.edges(p.Now())
		if still {
			left, right, moving = flow.target, flow.target+filterChipWidth, false
		}
		if moving {
			p.AnimationFrame() // Repaint, without rebuilding the card list.
		}
		stretch := max(0, min((right-left-filterChipWidth)/26, 1))
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
	})
}
