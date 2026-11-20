package main

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestPreviewLinkDetection(t *testing.T) {
	text := "链接：https://example.com/a(b)。Next (HTTP://localhost:3000/path?q=a#z), " +
		"<https://[::1]/test>; https://good.test/?next=https://else.test. " +
		"file:///tmp/a javascript:alert(1) nothttps://example.com https://:80 https://bad.test/%ZZ"
	var got []string
	for _, link := range linksInPreview(text, false) {
		got = append(got, text[link.start:link.end])
	}
	want := []string{
		"https://example.com/a(b)", "HTTP://localhost:3000/path?q=a#z",
		"https://[::1]/test", "https://good.test/?next=https://else.test",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("links = %q, want %q", got, want)
	}
	head := "https://example.com/a "
	body := preparePreview(KindText, head+strings.Repeat(" ", previewMaxRunes-len(head)-18)+"https://long.test/unfinished")
	if !body.truncated || len(body.links) != 1 || body.links[0].start != 0 {
		t.Fatalf("truncated link should not be openable: %+v", body.links)
	}
	for _, text := range []string{"file:///tmp/a", "javascript:alert(1)", "https://:80", `https://example.com/a\b`} {
		if parseWebLink(text) != nil || textKind(text) == KindLink {
			t.Errorf("non-web URL treated as openable: %q", text)
		}
	}
}

func TestPreviewOpensOnlyModifiedLinkClick(t *testing.T) {
	const first, second = "https://example.com/first", "https://example.org/second"
	for _, tc := range []struct {
		name   string
		kind   Kind
		text   string
		target string
		want   string
	}{
		{"text", KindText, "Read " + first + " and " + second + " for details.", second, second},
		{"code", KindCode, "package main\n// Read " + first + " and " + second + " for details.", first, first},
		{"URL", KindLink, first, first, first},
		{"host", KindLink, first, "example.com", first},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clip := &Clip{ID: 1, Kind: tc.kind, Text: tc.text}
			a := &app{}
			p := &clipPreview{id: clip.ID, loaded: true, previewBody: preparePreview(clip.Kind, clip.Text)}
			tt := ui.NewTester(func(c *ui.Context) {
				ui.Column(c).Fill().Children(func() { a.previewContent(c, clip, p, c.Theme().Accent) })
			}, 700, 220)
			r, ok := tt.Find(tc.target)
			if !ok {
				t.Fatalf("missing link %q", tc.target)
			}
			// A standalone URL has the same label as its (wider) paragraph.
			// Click near the start of the text, not the center of that box.
			x, y := r.X+5, r.Y+r.H/2
			click := func(mods ui.Modifiers) {
				tt.ClickAtWith(mods, x, y)
				tt.Advance(time.Second)
			}
			click(0)
			if tc.kind != KindLink {
				tt.Command("selectAll")
				tt.Command("copy")
				if got := tt.Clipboard(); got != tc.text {
					t.Fatalf("copied %q, want %q", got, tc.text)
				}
			}
			tt.RightClickAt(x, y)
			tt.Key(0, ui.KeyEscape)
			tt.Advance(time.Second)
			click(ui.Shift)
			if tc.kind == KindText {
				r, ok := tt.Find(tc.text)
				if !ok {
					t.Fatal("missing text paragraph")
				}
				tt.ClickAtWith(ui.Cmd, r.X+5, r.Y+r.H/2)
				tt.Advance(time.Second)
			}
			if got := tt.OpenedURLs(); len(got) != 0 {
				t.Fatalf("unexpected URLs opened: %q", got)
			}
			click(ui.Cmd)
			if got := tt.OpenedURLs(); !slices.Equal(got, []string{tc.want}) {
				t.Fatalf("opened %q, want %q", got, tc.want)
			}
			tt.Frame()
			if got := tt.OpenedURLs(); len(got) != 1 {
				t.Fatalf("rebuilt frame opened the link again: %q", got)
			}
		})
	}
}
