package main

import (
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"copied/locales"

	"github.com/egoist/mygo/ui"
)

// A clip is not a Markdown document. Recognize web URLs without changing
// the surrounding text or treating other schemes as browser targets.
var webLinkStart = regexp.MustCompile(`(?i)https?://`)

type previewLink struct{ start, end int } // byte offsets in the displayed text

func parseWebLink(text string) *url.URL {
	if !ui.SafeToOpen(text) || strings.ContainsFunc(text, unicode.IsSpace) {
		return nil
	}
	u, err := url.Parse(text)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil
	}
	return u
}

func webLinkDelimiter(r rune) bool {
	return unicode.IsSpace(r) || unicode.IsControl(r) ||
		strings.ContainsRune("\"'<>`“”‘’「」『』《》【】。，、；：！？（）", r)
}

func linksInPreview(text string, croppedMidToken bool) []previewLink {
	var links []previewLink
	consumed := 0
	for _, match := range webLinkStart.FindAllStringIndex(text, -1) {
		start, end := match[0], match[1]
		if start < consumed {
			continue // for example, a second URL in a query parameter
		}
		if start > 0 {
			r, _ := utf8.DecodeLastRuneInString(text[:start])
			if r < utf8.RuneSelf && (unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("_+.-", r)) {
				continue
			}
		}
		for end < len(text) {
			r, size := utf8.DecodeRuneInString(text[end:])
			if webLinkDelimiter(r) || r == utf8.RuneError && size == 1 {
				break
			}
			end += size
		}
		consumed = end
		if end == len(text) && croppedMidToken {
			continue // never open a destination cut short by the preview
		}
		end = start + len(trimWebLinkPunctuation(text[start:end]))
		if parseWebLink(text[start:end]) != nil {
			links = append(links, previewLink{start, end})
		}
	}
	return links
}

func trimWebLinkPunctuation(text string) string {
	var balance [3]int
	for _, r := range text {
		if i := strings.IndexRune("([{", r); i >= 0 {
			balance[i]++
		} else if i := strings.IndexRune(")]}", r); i >= 0 {
			balance[i]--
		}
	}
	for len(text) > 0 {
		last := text[len(text)-1]
		if i := strings.IndexByte(")]}", last); i >= 0 && balance[i] < 0 {
			balance[i]++
		} else if !strings.ContainsRune(".,;:!?", rune(last)) {
			break
		}
		text = text[:len(text)-1]
	}
	return text
}

type linkPress struct {
	active bool
	x, y   float32
}

// previewWebLink handles only the modified primary click. Ordinary presses
// still go to the paragraph's text selection. HandleInput takes the
// modified press before selection can redirect its release to the paragraph.
func (p *clipPreview) previewWebLink(c *ui.Context, el ui.Element, label, target string) ui.Element {
	tip := locales.In(c.Locale()).OpenBrowserLink(cmdCap())
	services := c.Services()
	return el.Label(label).TextColor(c.Theme().Accent).Underline().Cursor(ui.CursorPointer).Tooltip(tip).
		HandleInput(func(ev ui.InputEvent) bool {
			press := &p.linkPress
			switch ev.Kind {
			case ui.InputPointerDown:
				if ev.Button != 0 || ev.Mods != ui.Cmd || ev.Clicks != 1 {
					return false
				}
				*press = linkPress{active: true, x: ev.X, y: ev.Y}
				return true
			case ui.InputPointerMove, ui.InputPointerUp:
				if !press.active || ev.Button != 0 {
					return false
				}
				dx, dy := ev.X-press.x, ev.Y-press.y
				if ev.Canceled || dx*dx+dy*dy > 16 {
					press.active = false
				}
				if ev.Kind == ui.InputPointerUp && press.active {
					press.active = false
					services.OpenURL(target)
				}
				return true
			}
			return false
		})
}

// linkedPreviewText keeps the entire paragraph selectable and wrapping
// normally, while giving each URL its own hit region. Keep syntax colors
// in the code around URLs, splitting spans only where a URL begins or ends.
func (p *clipPreview) linkedPreviewText(c *ui.Context, el ui.Element, spans []ui.Span) {
	el.Children(func() {
		bytePos, spanPos := 0, 0
		take := func(end int, keep bool) []ui.Span {
			var out []ui.Span
			for bytePos < end && len(spans) > 0 {
				span := spans[0]
				n := min(end-bytePos, len(span.Text)-spanPos)
				if n > 0 {
					if keep {
						span.Text = span.Text[spanPos : spanPos+n]
						out = append(out, span)
					}
					bytePos += n
					spanPos += n
				}
				if spanPos == len(spans[0].Text) {
					spans, spanPos = spans[1:], 0
				}
			}
			return out
		}
		for _, link := range p.links {
			if bytePos < link.start {
				ui.RichText(c, take(link.start, true)...)
			}
			target := p.text[link.start:link.end]
			take(link.end, false)
			p.previewWebLink(c, ui.Text(c.Key(link.start), target), target, target)
		}
		if bytePos < len(p.text) {
			ui.RichText(c, take(len(p.text), true)...)
		}
	})
}
