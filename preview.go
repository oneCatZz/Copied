package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
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
	id        int64
	gen       uint64 // advances as the preview changes clips, to reject stale reads
	loaded    bool
	original  *thumb // the whole picture of an image clip, once it decoded
	viewing   bool   // the picture shows over the whole panel (imageViewer)
	view      viewer // where it shows there
	linkPress linkPress
	previewBody

	editing     bool      // the preview edits the clip's text (previewEditor)
	editSession uint64    // a fresh editor for each time it starts
	draft       string    // the text as edited
	editSaved   string    // the clip's text, as last saved
	editTyped   string    // the draft as last seen, to tell when typing pauses
	editTypedAt time.Time // when it last changed
	editBlocked string    // a draft that only a merge with another clip could save
	editFailed  bool      // saving the draft failed
}

// editSaveDelay is how long typing pauses before the draft saves itself.
const editSaveDelay = 600 * time.Millisecond

// editDirty reports whether the draft differs from the text saved. An
// empty draft is never saved: deleting is for the Delete button.
func (p *clipPreview) editDirty() bool {
	return p.editing && strings.TrimSpace(p.draft) != "" && p.draft != p.editSaved &&
		p.draft != strings.ReplaceAll(p.editSaved, "\r\n", "\n")
}

type previewBody struct {
	text      string
	lines     int  // of the whole text, before it was cut short
	truncated bool // the text was cut short for the preview
	code      *codePreview
	links     []previewLink
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
	if p.editing && a.store != nil {
		// Choosing another clip keeps what was typed in this one.
		chosen := clip.ID
		merged := a.flushEdit(true)
		a.reloadAfterEdit = func() { a.reloadChoosing(chosen, merged) }
	}
	p.reset()
	p.id = clip.ID
	if clip.Kind == KindImage {
		p.loaded = true
		store, id := a.store, clip.ID
		a.loadOriginal(p, func() []byte { return store.Image(id) })
		return p
	}
	if path := clip.imageFile(); path != "" {
		a.loadOriginal(p, func() []byte { return readImageFile(path) })
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
		// JSON is laid out afresh, whether it came minified, as JSON lines
		// or indented in a way of its own.
		if language == "JSON" {
			text = formatJSON(strings.TrimSpace(text))
		}
	} else {
		text = strings.TrimSpace(text)
	}
	text = strings.ReplaceAll(text, "\t", "    ")
	body := previewBody{lines: strings.Count(text, "\n") + 1}
	body.text, body.truncated = cutPreview(text)
	if kind == KindText || kind == KindCode {
		croppedMidToken := false
		if body.truncated {
			next, _ := utf8.DecodeRuneInString(text[len(body.text):])
			croppedMidToken = !webLinkDelimiter(next)
		}
		body.links = linksInPreview(body.text, croppedMidToken)
	}
	if kind == KindCode {
		body.code = highlightCode(language, body.text, previewMaxLines)
	}
	return body
}

// imageFileLimit is the largest picture file the preview reads.
const imageFileLimit = 64 << 20

// readImageFile reads a picture file for the preview, or returns nil when
// it is gone, unreadable or too large.
func readImageFile(path string) []byte {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > imageFileLimit {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return data
}

// loadOriginal decodes the whole picture of an image clip, or of a picture
// file, off the UI thread; the preview shows an image clip's thumbnail until
// then. Without a window to publish to, it stays with the thumbnail.
func (a *app) loadOriginal(p *clipPreview, read func() []byte) {
	if a.store == nil || a.panel == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	th := &thumb{cancel: cancel}
	p.original = th
	panel := a.panel
	go func() {
		defer cancel()
		data := read()
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

// formatJSON lays JSON out one value per line. It does not validate it:
// a text cut short, NaN or several values in turn are laid out as far as
// they go. A text with comments keeps its own layout, since a comment
// runs to the end of its line.
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
		case ch == '/':
			return s
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

// editable reports whether the preview can edit the text of a clip of a
// kind: the clips of text, not of pictures or files.
func editable(kind Kind) bool {
	return kind == KindText || kind == KindCode || kind == KindLink || kind == KindColor
}

// editing reports whether the preview edits the chosen clip.
func (a *app) editing() bool {
	p := &a.preview
	return p.editing && a.chosen >= 0 && a.chosen < len(a.clips) && a.clips[a.chosen].ID == p.id
}

// startEdit lets the preview edit the whole text of a clip.
func (a *app) startEdit(clip *Clip) {
	text := clip.Text
	if a.store != nil {
		whole, err := a.store.Text(clip.ID)
		if err != nil {
			log.Print("reading clip: ", err)
			return
		}
		text = whole
	}
	p := a.previewOf(clip)
	p.editing, p.draft, p.editSaved, p.editTyped = true, text, text, text
	p.editBlocked, p.editFailed = "", false
	p.editSession++
}

// flushEdit saves the draft as the clip's text, when it changed, and
// shows it in the list at once. Saving as typing pauses leaves alone
// another clip holding the same text; saving as the editing ends (merge)
// has that clip give way, and reports that the history needs reloading.
func (a *app) flushEdit(merge bool) (merged bool) {
	p := &a.preview
	if a.store == nil || !p.editDirty() || !merge && p.draft == p.editBlocked {
		return false
	}
	saved, err := a.store.UpdateText(p.id, p.draft, merge)
	if err != nil {
		log.Print("saving clip: ", err)
		p.editFailed = true
		return false
	}
	if !saved {
		p.editBlocked = p.draft
		return false
	}
	p.editSaved, p.editBlocked, p.editFailed = p.draft, "", false
	for _, clip := range a.clips {
		if clip.ID == p.id {
			clip.Kind, clip.Length = textKind(p.draft), utf8.RuneCountInString(p.draft)
			clip.Text = p.draft
			if runes := []rune(p.draft); len(runes) > previewRunes {
				clip.Text = string(runes[:previewRunes]) // as Store.List cuts it
			}
		}
	}
	return merge
}

// finishEdit saves the draft and leaves the editor. Should saving fail,
// the editor stays, with the draft.
func (a *app) finishEdit() {
	p := &a.preview
	merged := a.flushEdit(true)
	if p.editFailed {
		return
	}
	id := p.id
	a.preview.reset()
	a.reloadChoosing(id, merged)
	a.input.Focus()
}

// reloadChoosing lists the clips again with the clip with an ID still
// chosen, after an edit saved; merged drops from the stack the clip that
// gave way to the edited one.
func (a *app) reloadChoosing(id int64, merged bool) {
	if merged {
		a.pruneStack()
	}
	a.reload()
	if i := slices.IndexFunc(a.clips, func(c *Clip) bool { return c.ID == id }); i >= 0 {
		a.chosen = i
	}
}

// autosaveEdit saves the draft once typing paused for editSaveDelay.
func (a *app) autosaveEdit(c *ui.Context) {
	p := &a.preview
	now := c.Now()
	if p.draft != p.editTyped {
		p.editTyped, p.editTypedAt = p.draft, now
	}
	if !p.editDirty() || p.draft == p.editBlocked {
		return
	}
	if wait := editSaveDelay - now.Sub(p.editTypedAt); wait > 0 {
		c.After(wait)
		return
	}
	a.flushEdit(false)
	c.Invalidate() // for the list to show it
}

// editKey gives each editing of a clip its own editor.
type editKey struct {
	id      int64
	session uint64
}

// previewButton is a small button of the preview's header, highlighted
// while active.
func previewButton(c *ui.Context, key, label string, active bool) ui.Element {
	t := c.Theme()
	button := ui.ButtonBase(c.Key(key)).Height(24).PaddingX(8).Radius(6).
		Cursor(ui.CursorPointer).Label(label).Border(1, t.Border)
	ink := t.TextMuted
	switch {
	case active:
		button.Background(t.Accent.Alpha(0.12)).Border(1, t.Accent.Alpha(0.4))
		ink = t.Accent
	case button.Hovered():
		button.Background(t.SurfaceHover)
		ink = t.Text
	}
	return button.Transition(fadeMotion).Children(func() {
		ui.Text(c, label).FontSize(11).TextColor(ink).SingleLine()
	})
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
	if a.reloadAfterEdit != nil {
		c.Invalidate() // the list, as the edit left it, in the next pass
	}
	hue := kindColor(clip.Kind, t.Dark)
	textual := clip.Kind == KindText || clip.Kind == KindCode
	editing := a.editing()
	batch := a.batchIDs(i) != nil

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
			if clip.Kind == KindCode && !editing {
				if previewButton(c, "wrap-lines", tx.WrapLines(), a.codeWrap).Clicked() {
					a.codeWrap = !a.codeWrap
					a.input.Focus()
				}
			}
			// The whole text, once read, and when the preview shows it all.
			if editable(clip.Kind) && !batch && p.loaded && !p.truncated {
				if previewButton(c, "edit-clip", tx.Edit(), editing).Clicked() {
					if editing {
						action = a.finishEdit
					} else {
						action = func() { a.startEdit(clip) }
					}
				}
			}
			when := ago(clip.UsedAt, c.Locale())
			if clip.Source != "" {
				when = clip.Source + " · " + when
			}
			ui.Text(c, when).FontSize(11).TextColor(t.TextMuted).SingleLine()
		})
		ui.Box(c).Height(1).Shrink(0).Background(t.Border)

		if editing {
			a.previewEditor(c, clip, p)
			a.autosaveEdit(c)
			if p.editFailed {
				ui.Text(c, tx.EditFailed()).FontSize(11).TextColor(t.Danger).Padding(0, 16, 6, 16).SingleLine()
			}
		} else {
			a.previewContent(c, clip, p, hue)
		}

		if p.truncated && !editing {
			ui.Text(c, tx.PreviewTruncated()).FontSize(11).TextColor(t.TextMuted).Padding(6, 16).SingleLine()
		}
		ui.Box(c).Height(1).Shrink(0).Background(t.Border)
		ui.Row(c).Padding(10, 12).Gap(8).AlignItems(ui.Center).Children(func() {
			if editing {
				if dialogButton(c, "finish-edit", tx.Done(), true).Clicked() {
					action = a.finishEdit
				}
				ui.Text(c, tx.AutoSave()).FontSize(11.5).TextColor(t.TextMuted).SingleLine()
				return
			}
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
			deleteLabel := tx.Delete()
			if batch {
				deleteLabel = tx.DeleteAll()
			}
			if dialogButton(c, "detail-delete", deleteLabel, false).Clicked() {
				action = func() {
					a.remove(i)
					a.input.Focus()
				}
			}
		})
	})
	return action
}

// previewEditor edits the whole text of the chosen clip, in place of its
// preview.
func (a *app) previewEditor(c *ui.Context, clip *Clip, p *clipPreview) {
	t, tx := c.Theme(), locales.In(c.Locale())
	editTheme := lightEditTheme
	if t.Dark {
		editTheme = darkEditTheme
	}
	ui.Column(c).Grow(1).Basis(0).MinHeight(0).Padding(10, 12).Children(func() {
		ui.Themed(c.Key(editKey{clip.ID, p.editSession}), editTheme).Column().Grow(1).Basis(0).MinHeight(0).Children(func() {
			editor := ui.TextAreaBase(c.Key("editor"), &p.draft).AutoFocus().Grow(1).Basis(0).MinHeight(0).
				Padding(10, 12).Radius(8).Background(t.Background).Border(1, t.Accent).Shadow(0, 0, 0, 3, t.Accent.Alpha(0.2)).
				CaretColor(t.Accent).TextColor(t.Text).FontSize(13).LineHeight(1.6).Label(tx.Edit())
			if clip.Kind == KindCode {
				editor.Font("monospace").FontSize(12)
			}
			editor.PopoverMenu(func(m *ui.Menu) { m.EditItems() })
		})
	})
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
			// until it decoded. A click shows it over the whole panel.
			if original := p.original; original != nil && original.bitmap != nil {
				image := ui.Image(c.Key("preview-image"), original.bitmap).Fill().Fit(ui.ScaleDown).Radius(8).
					Cursor(ui.CursorZoomIn)
				if image.Clicked() {
					p.viewing = true
				}
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
			openable := p.loaded && !p.truncated && parseWebLink(link) != nil
			for i, label := range []string{host, link} {
				el := ui.RichText(c.Key(i)).Selectable()
				if i == 0 {
					el.FontSize(15).FontWeight(600).TextColor(hue)
				} else {
					el.FontSize(12.5).LineHeight(1.6).TextColor(t.TextMuted)
				}
				el.Children(func() {
					part := ui.Text(c, label)
					if openable {
						p.previewWebLink(c, part, label, link)
					}
				})
			}
		})
	case KindFiles:
		original := p.original
		picture := original != nil && original.bitmap != nil
		if picture {
			// A picture file shows as a picture, with its path below.
			ui.Box(c).Grow(1).Basis(0).Padding(14, 14, 0, 14).Children(func() {
				image := ui.Image(c.Key("preview-image"), original.bitmap).Fill().Fit(ui.ScaleDown).Radius(8).
					Cursor(ui.CursorZoomIn)
				if image.Clicked() {
					p.viewing = true
				}
				original.releasePixels(c, image)
			})
		}
		files := ui.Scroll(c.Key(previewKey{id: clip.ID}))
		if !picture {
			files.Grow(1).Basis(0)
		}
		files.Padding(12, 16).Gap(10).Children(func() {
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
				spans := p.code.styledSpans(t)
				if len(p.links) > 0 {
					code = ui.RichText(c).TextColor(t.Text.Alpha(0.86))
					p.linkedPreviewText(c, code, spans)
				} else {
					code = ui.RichText(c, spans...)
				}
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
			var body ui.Element
			if p.loaded && len(p.links) > 0 {
				body = ui.RichText(c)
				p.linkedPreviewText(c, body, []ui.Span{{Text: text}})
			} else {
				body = ui.Text(c, text)
			}
			body.FontSize(13).LineHeight(1.65).TextColor(t.Text.Alpha(0.9)).Selectable()
		})
	}
}

// Zoom of the picture shown over the whole panel.
const (
	viewerMaxScale = 8   // the largest, eight times its own size
	viewerStep     = 1.2 // a notch of a mouse wheel
)

// viewer is where the picture shown over the whole panel is, as the
// pointer zooms and moves it.
type viewer struct {
	scale        float32 // of the picture's own size; 0 fits it in the stage
	x, y         float32 // its middle, from the middle of the stage
	pressed      bool
	moved        bool // the press drags the picture, rather than clicks
	downX, downY float32
	lastX, lastY float32
	shown        bool // in the last frame
}

// imageViewer shows the chosen picture over the whole panel while the
// preview is viewing it, fitted in at first. A mouse wheel, a pinch of a
// touchpad or Cmd/Ctrl with scrolling zooms it at the pointer; dragging it,
// or scrolling a touchpad, moves it. A double click fits it in again, and
// a click beside it, Space or Escape closes it.
func (a *app) imageViewer(c *ui.Context) {
	p := &a.preview
	v := &p.view
	tx := locales.In(c.Locale())
	ui.DialogBase(c.Key("image-viewer"), &p.viewing, func(backdrop, panel ui.Element) {
		width, height := c.Size()
		backdrop.Background(ui.RGBA(0, 0, 0, 0.78))
		panel.Column().Gap(8).AlignItems(ui.Center)
		stageW, stageH := max(1, width-32), max(1, height-32-26)

		original := p.original
		ready := original != nil && original.bitmap != nil
		var iw, ih, fit float32
		if ready {
			w, h := original.bitmap.Size()
			iw, ih = float32(max(w, 1)), float32(max(h, 1))
			fit = min(stageW/iw, stageH/ih, 1)
		}
		// clamp keeps the picture inside the stage where it fits, and the
		// stage covered where it does not.
		clamp := func() {
			lx, ly := absf(iw*v.scale-stageW)/2, absf(ih*v.scale-stageH)/2
			v.x, v.y = min(max(v.x, -lx), lx), min(max(v.y, -ly), ly)
		}
		if ready {
			if v.scale < fit {
				v.scale, v.x, v.y = fit, 0, 0
			}
			clamp()
		}
		// zoom scales the picture by k, keeping where it is under px, py.
		zoom := func(k, px, py float32) {
			if !ready || k <= 0 {
				return
			}
			s := min(max(v.scale*k, fit), viewerMaxScale)
			k = s / v.scale
			fx, fy := px-stageW/2, py-stageH/2
			v.x, v.y = fx-(fx-v.x)*k, fy-(fy-v.y)*k
			v.scale = s
			clamp()
		}

		stage := ui.Box(c.Key("viewer-stage")).Size(stageW, stageH).Clip().Cursor(ui.CursorGrab).
			Focusable().FocusRing(false).Focus()
		if v.pressed && v.moved {
			stage.Cursor(ui.CursorGrabbing)
		}
		stage.HandleInput(func(ev ui.InputEvent) bool {
			switch ev.Kind {
			case ui.InputKeyDown:
				if ev.Mods == 0 && (ev.Key == ui.KeySpace || ev.Key == ui.KeyEscape) {
					p.viewing = false
					return true
				}
				return false
			case ui.InputZoom:
				zoom(ev.Scale, ev.X, ev.Y)
				return true
			case ui.InputScroll:
				switch {
				case ev.Precise && ev.Mods&ui.Cmd == 0:
					// A touchpad's two fingers move it, as in Preview.
					v.x -= ev.DX
					v.y -= ev.DY
					clamp()
				case ev.DY == 0:
				case ev.Precise:
					zoom(float32(math.Exp(float64(-ev.DY)/300)), ev.X, ev.Y)
				case ev.DY < 0:
					zoom(viewerStep, ev.X, ev.Y)
				default:
					zoom(1/viewerStep, ev.X, ev.Y)
				}
				return true
			case ui.InputPointerDown:
				if ev.Button != 0 {
					return false
				}
				if ev.Clicks == 2 {
					v.scale, v.pressed = 0, false // fitted in again in the next frame
					return true
				}
				v.pressed, v.moved = true, false
				v.downX, v.downY, v.lastX, v.lastY = ev.X, ev.Y, ev.X, ev.Y
				return true
			case ui.InputPointerMove:
				if !v.pressed {
					return false
				}
				dx, dy := ev.X-v.lastX, ev.Y-v.lastY
				v.lastX, v.lastY = ev.X, ev.Y
				if !v.moved && absf(ev.X-v.downX)+absf(ev.Y-v.downY) > 4 {
					v.moved = true
					dx, dy = ev.X-v.downX, ev.Y-v.downY
				}
				if v.moved && ready {
					v.x += dx
					v.y += dy
					clamp()
				}
				return true
			case ui.InputPointerUp:
				if !v.pressed {
					return true
				}
				v.pressed = false
				if !v.moved && !ev.Canceled {
					// A click beside the picture closes it.
					sw, sh := iw*v.scale, ih*v.scale
					left, top := stageW/2+v.x-sw/2, stageH/2+v.y-sh/2
					if !ready || ev.X < left || ev.Y < top || ev.X > left+sw || ev.Y > top+sh {
						p.viewing = false
					}
				}
				return true
			}
			return false
		})
		stage.Children(func() {
			if !ready {
				return
			}
			sw, sh := iw*v.scale, ih*v.scale
			image := ui.Image(c.Key("viewer-image"), original.bitmap).Absolute().
				Left(stageW/2+v.x-sw/2).Top(stageH/2+v.y-sh/2).Size(sw, sh)
			original.releasePixels(c, image)
		})

		hint := tx.ImageViewerHint()
		if ready {
			hint = fmt.Sprintf("%d%% · %s", int(v.scale*100+0.5), hint)
		}
		ui.Text(c, hint).FontSize(11.5).TextColor(ui.RGBA(255, 255, 255, 0.78)).SingleLine()
	})
	if p.viewing && !v.shown {
		activateForViewer() // for pinches to come here
	}
	if !p.viewing && v.shown {
		p.view = viewer{}
		a.input.Focus()
	}
	v.shown = p.viewing
}

func absf(x float32) float32 {
	if x < 0 {
		return -x
	}
	return x
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
