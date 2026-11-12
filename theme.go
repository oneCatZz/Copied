package main

import (
	"math"
	"time"

	"github.com/egoist/mygo/ui"
)

// The panel has a look of its own, in light and in dark, which follows
// the system's appearance: neutral surfaces, an indigo accent, and a hue
// for each kind of clip.
var (
	lightTheme = makeTheme(ui.LightTheme(), themeColors{
		background: "#f4f5f8", surface: "#ffffff", hover: "#eef0f4", pressed: "#e4e6ec",
		border: "#e2e4ea", text: "#14161b", muted: "#6c7282",
		accent: "#4f5dff", accentHover: "#3f4cf0", accentPressed: "#343fd6",
	})
	darkTheme = makeTheme(ui.DarkTheme(), themeColors{
		background: "#0f1014", surface: "#1a1c22", hover: "#23262e", pressed: "#2b2f38",
		border: "#2b2e37", text: "#eceef3", muted: "#8b90a0",
		accent: "#7c87ff", accentHover: "#8f99ff", accentPressed: "#6a75f0",
	})
	lightEditTheme = editMenuTheme(lightTheme)
	darkEditTheme  = editMenuTheme(darkTheme)
)

// The text editor keeps MyGo's selection, clipboard and undo commands.
// Its drawn menu follows the same surface, type and hover colors as ours.
func editMenuTheme(t *ui.Theme) *ui.Theme {
	menu := *t
	menu.Background, menu.Accent, menu.AccentText = t.Surface, t.SurfaceHover, t.Text
	menu.FontSize, menu.Radius = 13, 6
	return &menu
}

type themeColors struct {
	background, surface, hover, pressed, border, text, muted string
	accent, accentHover, accentPressed                       string
}

func makeTheme(t *ui.Theme, c themeColors) *ui.Theme {
	t.Background, t.Surface = ui.Hex(c.background), ui.Hex(c.surface)
	t.SurfaceHover, t.SurfacePressed = ui.Hex(c.hover), ui.Hex(c.pressed)
	t.Border, t.Text, t.TextMuted = ui.Hex(c.border), ui.Hex(c.text), ui.Hex(c.muted)
	t.Accent, t.AccentHover, t.AccentPressed = ui.Hex(c.accent), ui.Hex(c.accentHover), ui.Hex(c.accentPressed)
	t.InactiveAccent = t.Accent // a panel is always the user's
	t.Selection, t.Focus = t.Accent.Alpha(0.3), t.Accent.Alpha(0.55)
	t.Radius = 8
	return t
}

// kindColor is the hue of a kind of clip: the same lightness and chroma
// for each, a little lighter on dark.
func kindColor(k Kind, dark bool) ui.Color {
	hue := float32(262) // text: blue
	switch k {
	case KindCode:
		hue = 145
	case KindLink:
		hue = 172
	case KindImage:
		hue = 312
	case KindFiles:
		hue = 68
	case KindColor:
		hue = 18
	}
	if dark {
		return ui.Oklch(0.76, 0.13, hue).SRGB()
	}
	return ui.Oklch(0.58, 0.16, hue).SRGB()
}

func svg(body string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">` + body + `</svg>`))
}

var (
	iconSearch   = svg(`<circle cx="11" cy="11" r="7"/><path d="m20 20-3.6-3.6"/>`)
	iconSettings = svg(`<path d="M4 7h10M18 7h2M4 17h2M10 17h10"/><circle cx="16" cy="7" r="2"/><circle cx="8" cy="17" r="2"/>`)
	iconText     = svg(`<path d="M5 6h14M5 12h14M5 18h9"/>`)
	iconCode     = svg(`<path d="m8 6-6 6 6 6m8-12 6 6-6 6m-3-15-2 18"/>`)
	iconLink     = svg(`<path d="M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1 1"/><path d="M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1-1"/>`)
	iconImage    = svg(`<rect x="3" y="4" width="18" height="16" rx="3"/><circle cx="9" cy="10" r="1.6"/><path d="m4 18 5-5 4 4 2.5-2.5L20 19"/>`)
	iconFile     = svg(`<path d="M7 3h7l5 5v11a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2z"/><path d="M14 3v5h5"/>`)
	iconColor    = svg(`<path d="M12 3s6 6.2 6 10.5a6 6 0 0 1-12 0C6 9.2 12 3 12 3z"/>`)
	iconLogo     = svg(`<path d="M9 5a2 2 0 0 1 2-2h7a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2"/><rect x="4" y="7" width="11" height="14" rx="2"/>`)
	iconPin      = svg(`<path d="m12 3 2.7 5.6 6.1.8-4.5 4.3 1.1 6.1L12 16.9l-5.4 2.9 1.1-6.1L3.2 9.4l6.1-.8z" fill="currentColor"/>`)
)

func kindIcon(k Kind) *ui.SVG {
	switch k {
	case KindCode:
		return iconCode
	case KindLink:
		return iconLink
	case KindImage:
		return iconImage
	case KindFiles:
		return iconFile
	case KindColor:
		return iconColor
	}
	return iconText
}

// Transitions of the panel's elements.
var (
	// Cards slide to their places as clips come, go and move to the
	// front, new ones growing in.
	cardMotion = ui.ElementTransition{
		Animation: &ui.Snappy,
		Enter:     &ui.Motion{Scale: 0.9, Y: 14},
		Exit:      &ui.Motion{Scale: 0.9},
	}
	// Hovers and selections fade.
	fadeMotion = ui.ElementTransition{Colors: true, Shadows: true, Duration: 140 * time.Millisecond}
)

// The panel's content rises as the panel shows, and its cards after it,
// one after the other.
const (
	riseTime    = 300 * time.Millisecond
	cardRise    = 380 * time.Millisecond
	cardStagger = 32 * time.Millisecond
	cardsRising = 12 // the cards after them are out of sight, or nearly
)

// rise is how far along something that takes d is, after since: from 0 to
// 1, fast then slow.
func rise(since, d time.Duration) float32 {
	if since <= 0 {
		return 0
	}
	if since >= d {
		return 1
	}
	x := 1 - float64(since)/float64(d)
	return float32(1 - math.Pow(x, 4))
}
