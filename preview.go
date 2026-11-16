package main

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"copied/locales"

	"github.com/egoist/mygo/ui"
)

// The preview shows this much of a clip at most. Pasting keeps it whole.
const (
	previewMaxRunes = 40_000
	previewMaxLines = 2_000
)

// previewKey gives the scrolling content of a preview its own identity for
// each clip and wrapping, so another clip starts at its top.
type previewKey struct {
	id   int64
	wrap bool
}

// clipPreview is the whole text of the chosen clip, as far as the preview
// shows it. It is read off the UI thread when the list holds only its start.
type clipPreview struct {
	id       int64
	gen      uint64 // advances as the preview changes clips, to reject stale reads
	loaded   bool
	original *thumb // the whole picture of an image clip, once it decoded
	previewBody
}

type previewBody struct {
	text      string
	lines     int  // of the whole text, before it was cut short
	truncated bool // the text was cut short for the preview
	code      *codePreview
}

// reset forgets the preview, and any read on its way.
func (p *clipPreview) reset() {
	if p.original != nil {
		p.original.cancel()
	}
	*p = clipPreview{gen: p.gen + 1}
}

// previewOf returns the preview of a clip, and starts reading the clip's
// whole text when the list holds only its start.
func (a *app) previewOf(clip *Clip) *clipPreview {
	p := &a.preview
	if p.id == clip.ID && p.gen != 0 {
		return p
	}
	p.reset()
	p.id = clip.ID
	if clip.Kind == KindImage {
		p.loaded = true
		a.loadOriginal(p, clip.ID)
		return p
	}
	if utf8.RuneCountInString(clip.Text) < previewRunes {
		// The list's text is the whole text: Store.List cuts it at previewRunes.
		p.previewBody, p.loaded = preparePreview(clip.Kind, clip.Text), true
		return p
	}
	id, kind, gen, store, panel := clip.ID, clip.Kind, p.gen, a.store, a.panel
	go func() {
		text, err := store.Text(id)
		if err != nil {
			log.Print("reading clip: ", err)
			return
		}
		body := preparePreview(kind, text)
		panel.Update(func() {
			if a.preview.gen == gen && a.preview.id == id {
				a.preview.previewBody, a.preview.loaded = body, true
			}
		})
	}()
	return p
}

// preparePreview cuts a text short for the preview, lays out a minified
// JSON and highlights code. It may take a while for a large clip.
func preparePreview(kind Kind, text string) previewBody {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	var language string
	if kind == KindCode {
		language, _, _ = detectCode(text)
		text = stripFence(strings.Trim(text, "\n"))
		// Minified JSON, or JSON lines as a stream sends them, is laid out;
		// JSON laid out already keeps its own lines.
		if language == "JSON" && hasLongLine(text[:min(len(text), codeDetectLimit)], 200) {
			text = formatJSON(strings.TrimSpace(text))
		}
	} else {
		text = strings.TrimSpace(text)
	}
	text = strings.ReplaceAll(text, "\t", "    ")
	body := previewBody{lines: strings.Count(text, "\n") + 1}
	body.text, body.truncated = cutPreview(text)
	if kind == KindCode {
		body.code = highlightCode(language, body.text, previewMaxLines)
	}
	return body
}

// loadOriginal decodes the whole picture of an image clip off the UI
// thread; the preview shows its thumbnail until then. Without a window to
// publish to, it stays with the thumbnail.
func (a *app) loadOriginal(p *clipPreview, id int64) {
	if a.store == nil || a.panel == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	th := &thumb{cancel: cancel}
	p.original = th
	store, panel := a.store, a.panel
	go func() {
		defer cancel()
		data := store.Image(id)
		if ctx.Err() != nil || len(data) == 0 {
			return
		}
		bitmap, err := ui.DecodeBitmap(data)
		if err != nil || ctx.Err() != nil {
			return
		}
		panel.Update(func() {
			if a.preview.original == th {
				th.bitmap = bitmap
			}
		})
	}()
}

// hasLongLine reports whether a line of s is longer than n bytes.
func hasLongLine(s string, n int) bool {
	for len(s) > n {
		end := strings.IndexByte(s, '\n')
		if end < 0 || end > n {
			return true
		}
		s = s[end+1:]
	}
	return false
}

// formatJSON lays JSON out one value per line. It does not validate it:
// a text cut short, NaN or several values in turn are laid out as far as
// they go.
func formatJSON(s string) string {
	var b strings.Builder
	b.Grow(len(s) + len(s)/4)
	depth := 0
	newline := func() {
		b.WriteByte('\n')
		for range depth {
			b.WriteString("  ")
		}
	}
	space := func(ch byte) bool { return ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n' }
	inString, escaped := false, false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if inString {
			b.WriteByte(ch)
			switch {
			case escaped:
				escaped = false
			case ch == '\\':
				escaped = true
			case ch == '"':
				inString = false
			}
			continue
		}
		switch {
		case space(ch):
			// The layout takes the place of the spaces between values.
		case ch == '{' || ch == '[':
			if depth == 0 && b.Len() > 0 {
				newline() // another value after the first
			}
			b.WriteByte(ch)
			j := i + 1
			for j < len(s) && space(s[j]) {
				j++
			}
			if j < len(s) && (s[j] == '}' || s[j] == ']') {
				b.WriteByte(s[j]) // empty, on one line
				i = j
				continue
			}
			depth++
			newline()
		case ch == '}' || ch == ']':
			depth = max(0, depth-1)
			newline()
			b.WriteByte(ch)
		case ch == ',':
			b.WriteByte(ch)
			newline()
		case ch == ':':
			b.WriteString(": ")
		case ch == '"':
			inString = true
			b.WriteByte(ch)
		default:
			b.WriteByte(ch)
		}
	}
	return b.String()
}

// stripFence leaves out the fences around a Markdown code block.
func stripFence(s string) string {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "```") && !strings.HasPrefix(t, "~~~") {
		return s
	}
	marker := t[:3]
	_, rest, found := strings.Cut(t, "\n")
	if !found {
		return s
	}
	rest = strings.TrimRight(rest, "\n")
	if end := strings.LastIndex(rest, "\n"); end >= 0 && strings.TrimSpace(rest[end+1:]) == marker {
		rest = rest[:end]
	} else if strings.TrimSpace(rest) == marker {
		return ""
	}
	return rest
}

// cutPreview keeps at most previewMaxLines lines and previewMaxRunes
// characters of a text, and reports whether it left any out.
func cutPreview(text string) (string, bool) {
	lines, runes := 0, 0
	for i, r := range text {
		if runes == previewMaxRunes {
			return text[:i], true
		}
		runes++
		if r == '\n' {
			if lines++; lines == previewMaxLines {
				return text[:i], i+1 < len(text)
			}
		}
	}
	return text, false
}

// detail shows the chosen clip whole, as far as it can, with what to do
// with it. It returns what the user asked, if anything.
func (a *app) detail(c *ui.Context) (action func()) {
	if a.chosen < 0 || a.chosen >= len(a.clips) {
		return nil
	}
	t, tx := c.Theme(), locales.In(c.Locale())
	i, clip := a.chosen, a.clips[a.chosen]
	p := a.previewOf(clip)
	hue := kindColor(clip.Kind, t.Dark)
	textual := clip.Kind == KindText || clip.Kind == KindCode

	pane := ui.Column(c.Key("detail")).Grow(1).Basis(0).MinWidth(0)
	paneSurface(c, pane).Children(func() {
		ui.Row(c).Padding(12, 14, 10, 14).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(24, 24).Shrink(0).Center().Radius(8).Background(hue.Alpha(0.16)).Children(func() {
				ui.Icon(c, kindIcon(clip.Kind)).FontSize(13).TextColor(hue)
			})
			ui.Text(c, kindLabel(clip.Kind, c.Locale())).FontSize(12.5).FontWeight(600).SingleLine()
			meta := clip.detail(c.Locale())
			if textual && p.loaded {
				meta += " · " + tx.LineCount(p.lines)
			}
			ui.Text(c, meta).FontSize(11.5).TextColor(t.TextMuted).SingleLine().Grow(1)
			if clip.Kind == KindCode {
				wrap := ui.ButtonBase(c.Key("wrap-lines")).Height(24).PaddingX(8).Radius(6).
					Cursor(ui.CursorPointer).Label(tx.WrapLines()).Border(1, t.Border)
				ink := t.TextMuted
				switch {
				case a.codeWrap:
					wrap.Background(t.Accent.Alpha(0.12)).Border(1, t.Accent.Alpha(0.4))
					ink = t.Accent
				case wrap.Hovered():
					wrap.Background(t.SurfaceHover)
					ink = t.Text
				}
				if wrap.Clicked() {
					a.codeWrap = !a.codeWrap
					a.input.Focus()
				}
				wrap.Transition(fadeMotion).Children(func() {
					ui.Text(c, tx.WrapLines()).FontSize(11).TextColor(ink).SingleLine()
				})
			}
			ui.Text(c, ago(clip.UsedAt, c.Locale())).FontSize(11).TextColor(t.TextMuted).SingleLine()
		})
		ui.Box(c).Height(1).Shrink(0).Background(t.Border)

		a.previewContent(c, clip, p, hue)

		if p.truncated {
			ui.Text(c, tx.PreviewTruncated()).FontSize(11).TextColor(t.TextMuted).Padding(6, 16).SingleLine()
		}
		ui.Box(c).Height(1).Shrink(0).Background(t.Border)
		ui.Row(c).Padding(10, 12).Gap(8).AlignItems(ui.Center).Children(func() {
			batch := a.batchIDs(i) != nil
			pasteLabel, copyLabel := tx.Paste(), tx.CopyOnly()
			if batch {
				pasteLabel, copyLabel = tx.PasteAll(), tx.CopyAll()
			}
			if dialogButton(c, "detail-paste", pasteLabel, true).Clicked() {
				action = func() { a.paste(i, false) }
			}
			if !batch && clip.Kind != KindImage && clip.Kind != KindFiles {
				if dialogButton(c, "detail-paste-plain", tx.PastePlain(), false).Clicked() {
					action = func() { a.paste(i, true) }
				}
			}
			if dialogButton(c, "detail-copy", copyLabel, false).Clicked() {
				action = func() { a.copyOnly(i) }
			}
			ui.Spacer(c)
			if dialogButton(c, "detail-groups", tx.AddToGroups(), false).Clicked() {
				action = func() { a.openClipGroups(i) }
			}
			if dialogButton(c, "detail-delete", tx.Delete(), false).Clicked() {
				action = func() {
					a.remove(i)
					a.input.Focus()
				}
			}
		})
	})
	return action
}

// previewContent is the middle of the preview: the whole text, code, link
// or list of files, scrolling, or a picture or a color.
func (a *app) previewContent(c *ui.Context, clip *Clip, p *clipPreview, hue ui.Color) {
	t := c.Theme()
	text := p.text
	if !p.loaded {
		text = strings.TrimSpace(clip.Text) // the start, until the rest is read
	}
	switch clip.Kind {
	case KindImage:
		// Only the chosen picture stays; the others may leave the cache.
		for _, th := range a.thumbs {
			th.pinned = false
		}
		a.thumbLimit = thumbCacheLimit
		ui.Box(c).Grow(1).Basis(0).Padding(14).Children(func() {
			// The whole picture at its own size where it fits, its thumbnail
			// until it decoded.
			if original := p.original; original != nil && original.bitmap != nil {
				image := ui.Image(c, original.bitmap).Fill().Fit(ui.ScaleDown).Radius(8)
				original.releasePixels(c, image)
				return
			}
			th := a.thumb(clip)
			if th.bitmap != nil {
				image := ui.Image(c, th.bitmap).Fill().Fit(ui.Contain).Radius(8)
				th.releasePixels(c, image)
			}
		})
	case KindColor:
		value := strings.TrimSpace(clip.Text)
		ui.Column(c).Grow(1).Basis(0).Padding(16).Gap(12).Children(func() {
			ui.Box(c).Grow(1).Radius(12).Background(ui.Hex(value)).InsetShadow(0, 0, 0, 1, t.Text.Alpha(0.12))
			ui.Row(c).Gap(16).Justify(ui.Center).AlignItems(ui.Center).Selectable().Children(func() {
				ui.Text(c, strings.ToUpper(value)).FontSize(15).FontWeight(600).LetterSpacing(0.4)
				if rgb := hexRGB(value); rgb != "" {
					ui.Text(c, rgb).FontSize(13).TextColor(t.TextMuted)
				}
			})
		})
	case KindLink:
		link := text
		host := link
		if u, err := url.Parse(link); err == nil && u.Host != "" {
			host = u.Host
		}
		ui.Scroll(c.Key(previewKey{id: clip.ID})).Grow(1).Basis(0).Padding(14, 16).Gap(8).Children(func() {
			ui.Text(c, host).FontSize(15).FontWeight(600).TextColor(hue).Selectable()
			ui.Text(c, link).FontSize(12.5).LineHeight(1.6).TextColor(t.TextMuted).Selectable()
		})
	case KindFiles:
		ui.Scroll(c.Key(previewKey{id: clip.ID})).Grow(1).Basis(0).Padding(12, 16).Gap(10).Children(func() {
			for n, path := range strings.Split(text, "\n") {
				if path = strings.TrimSpace(path); path == "" {
					continue
				}
				ui.Row(c.Key(n)).Gap(9).AlignItems(ui.Start).Children(func() {
					ui.Icon(c, iconFile).FontSize(14).Shrink(0).TextColor(hue).Margin(2, 0, 0, 0)
					ui.Column(c).Grow(1).Basis(0).MinWidth(0).Gap(2).Selectable().Children(func() {
						ui.Text(c, baseNames([]string{path})[0]).FontSize(12.5).FontWeight(550)
						ui.Text(c, path).FontSize(11.5).TextColor(t.TextMuted)
					})
				})
			}
		})
	case KindCode:
		wrap := a.codeWrap
		var scroll ui.Element
		if wrap {
			scroll = ui.Scroll(c.Key(previewKey{clip.ID, true}))
		} else {
			scroll = ui.ScrollBoth(c.Key(previewKey{clip.ID, false}))
		}
		scroll.Grow(1).Basis(0).Padding(12, 16).Children(func() {
			var code ui.Element
			if p.loaded && p.code != nil {
				code = ui.RichText(c, p.code.styledSpans(t)...)
			} else {
				code = ui.Text(c, text).TextColor(t.Text.Alpha(0.86))
			}
			code.Font("monospace").FontSize(12).LineHeight(1.6).Selectable()
			if !wrap {
				code.NoWrap()
			}
		})
	default:
		ui.Scroll(c.Key(previewKey{id: clip.ID})).Grow(1).Basis(0).Padding(14, 18).Children(func() {
			ui.Text(c, text).FontSize(13).LineHeight(1.65).TextColor(t.Text.Alpha(0.9)).Selectable()
		})
	}
}

// hexRGB writes a color of the form #rgb, #rrggbb or #rrggbbaa as rgb()
// or rgba(), or returns "" for anything else.
func hexRGB(s string) string {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) == 3 {
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	}
	if len(s) != 6 && len(s) != 8 {
		return ""
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return ""
	}
	if len(s) == 8 {
		return fmt.Sprintf("rgba(%d, %d, %d, %.2g)", v>>24, v>>16&0xff, v>>8&0xff, float64(v&0xff)/255)
	}
	return fmt.Sprintf("rgb(%d, %d, %d)", v>>16, v>>8&0xff, v&0xff)
}
