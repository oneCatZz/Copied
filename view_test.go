package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
	"weak"

	"github.com/egoist/mygo/ui"
)

func stripTestApp() *app {
	a := &app{since: 2 * time.Second, shownAt: time.Now().Add(-2 * time.Second), shortcut: defaultShortcuts[0]}
	for i := 0; i < 200; i++ {
		a.clips = append(a.clips, &Clip{ID: int64(i + 1), Kind: KindText, Text: fmt.Sprintf("clip-%03d", i), Length: 8, UsedAt: time.Now()})
	}
	return a
}

func listTester(a *app) *ui.Tester {
	return ui.NewTester(func(c *ui.Context) {
		ui.Themed(c, lightTheme).Row().Fill().AlignItems(ui.Stretch).Children(func() { a.list(c) })
		a.scrollTo = false
	}, listWidth, 300)
}

func TestListDefersDistantRowsAndRevealsSelection(t *testing.T) {
	a := stripTestApp()
	tt := listTester(a)
	rows := 0
	for _, text := range tt.Texts() {
		if strings.HasPrefix(text, "clip-") {
			rows++
		}
	}
	if rows < 4 || rows > 20 || tt.HasText("clip-199") || a.listScroll.MaxY < 10000 {
		t.Fatalf("unexpected windowing: rows=%d, maxY=%v", rows, a.listScroll.MaxY)
	}
	a.listScroll.Y = 100 * (rowHeight + rowGap)
	tt.Frame()
	if !tt.HasText("clip-100") {
		t.Fatal("scrolling did not materialize distant row contents")
	}
	a.choose(199)
	tt.Frame()
	tt.Advance(time.Second)
	r, ok := tt.Find("clip-199")
	if !ok || r.Y < 0 || r.Y+r.H > 301 {
		t.Fatalf("selection was not revealed: %v, found=%v", r, ok)
	}
	saveUIArtifact(t, tt, "list-last-row")
	a.listScroll.Y = 0
	tt.Frame()
	if !tt.HasText("clip-000") {
		t.Fatal("returning to the start did not restore row contents")
	}
}

func TestListScrollsVertically(t *testing.T) {
	a := stripTestApp()
	tt := listTester(a)
	tt.Scroll(100, 100, 0, 120)
	tt.Advance(time.Second)
	if math.Abs(float64(a.listScroll.Y-120)) > 0.5 || a.listScroll.X != 0 {
		t.Fatalf("vertical gesture did not scroll the list: x=%v, y=%v", a.listScroll.X, a.listScroll.Y)
	}
	tt.Wheel(100, 100, 0, 120)
	tt.Advance(time.Second)
	if a.listScroll.Y <= 120 {
		t.Fatalf("wheel did not scroll the list: %v", a.listScroll.Y)
	}
}

func TestArrowKeysChooseAndPickRows(t *testing.T) {
	a := stripTestApp()
	a.key(0, ui.KeyDown)
	a.key(0, ui.KeyDown)
	a.key(0, ui.KeyUp)
	if a.chosen != 1 || len(a.picked) != 0 {
		t.Fatalf("Up/Down: chosen=%d, picked=%v", a.chosen, a.picked)
	}
	a.key(ui.Shift, ui.KeyDown)
	if a.chosen != 2 || len(a.picked) != 2 || a.picked[0] != 2 || a.picked[1] != 3 {
		t.Fatalf("Shift+Down: chosen=%d, picked=%v", a.chosen, a.picked)
	}
	a.key(0, ui.KeyUp)
	a.key(0, ui.KeyUp)
	a.key(0, ui.KeyUp)
	if a.chosen != 0 || len(a.picked) != 0 {
		t.Fatalf("Up past the start: chosen=%d, picked=%v", a.chosen, a.picked)
	}
	if a.key(0, ui.KeyLeft) || a.key(0, ui.KeyRight) {
		t.Fatal("Left and Right should stay with the search field")
	}
}

func TestThumbnailLoadingAndCancellation(t *testing.T) {
	s := testStore(t)
	data := trayIconPNG
	if err := s.Add(&Clip{Kind: KindImage, Data: data, Thumb: data, Hash: "test-image"}); err != nil {
		t.Fatal(err)
	}
	clips, err := s.List(FilterImage, "")
	if err != nil || len(clips) != 1 {
		t.Fatalf("image fixture: %v, records=%d", err, len(clips))
	}
	slots := make(chan struct{}, 2)
	bitmap, err := loadThumb(context.Background(), s, clips[0].ID, slots)
	if err != nil || bitmap == nil {
		t.Fatalf("thumbnail did not load: %v", err)
	}
	w, h := bitmap.Size()
	if w != 32 || h != 32 || len(slots) != 0 {
		t.Fatalf("thumbnail size or slot release: %dx%d, slots=%d", w, h, len(slots))
	}
	slots <- struct{}{}
	slots <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := loadThumb(ctx, nil, 0, slots)
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("thumbnail bypassed concurrency limit: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("queued thumbnail cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued thumbnail did not cancel")
	}
	a := &app{thumbs: map[int64]*thumb{1: {cancel: cancel}}}
	a.clearThumbs()
	if len(a.thumbs) != 0 {
		t.Fatal("hidden panel retained thumbnails")
	}
}

func TestPanelRendering(t *testing.T) {
	a := stripTestApp()
	tt := ui.NewTester(a.view, panelWidth, panelHeight)
	if !tt.HasText("Copied") || !tt.HasText("clip-000") || !tt.HasText("Paste") {
		t.Fatal("panel is missing its header, list or preview")
	}
	saveUIArtifact(t, tt, "panel-light")
	tt.SetDark(true)
	tt.Advance(time.Second)
	saveUIArtifact(t, tt, "panel-dark")
}

func TestClearHistoryBeforePanelCreation(t *testing.T) {
	s := testStore(t)
	if err := s.Add(&Clip{Kind: KindText, Text: "background clip", Hash: "background-clip"}); err != nil {
		t.Fatal(err)
	}
	a := &app{store: s}
	a.clearHistory()
	clips, err := s.List(FilterAll, "")
	if err != nil || len(clips) != 0 || a.panel != nil {
		t.Fatalf("background clear: error=%v, clips=%d, panel=%v", err, len(clips), a.panel)
	}
}

func TestPreviewKeepsImageAndCodeInBounds(t *testing.T) {
	pixels := image.NewRGBA(image.Rect(0, 0, 40, 40))
	for y := 0; y < 40; y++ {
		for x := 0; x < 40; x++ {
			pixels.SetRGBA(x, y, color.RGBA{R: 240, A: 255})
		}
	}
	var data bytes.Buffer
	if err := png.Encode(&data, pixels); err != nil {
		t.Fatal(err)
	}
	bitmap, err := ui.DecodeBitmap(data.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []Kind{KindImage, KindCode} {
		t.Run(string(kind), func(t *testing.T) {
			a := &app{thumbs: map[int64]*thumb{1: {bitmap: bitmap, cancel: func() {}}}}
			clip := &Clip{ID: 1, Kind: kind, Text: "func main() {\n\tfmt.Println(\"a long line that must stay inside the code preview\")\n}\n"}
			tt := ui.NewTester(func(c *ui.Context) {
				ui.Themed(c, lightTheme).Column().Fill().Background(ui.RGB(255, 255, 255)).Children(func() {
					a.previewContent(c, clip, a.previewOf(clip), kindColor(kind, false))
				})
			}, 240, 160)
			img := tt.Image()
			for _, point := range []image.Point{{10, 1}, {10, 158}, {5, 80}} {
				r, g, b, _ := img.At(point.X, point.Y).RGBA()
				if r != 65535 || g != 65535 || b != 65535 {
					t.Fatalf("preview escaped its padding at %v: %v", point, img.At(point.X, point.Y))
				}
			}
			if kind == KindImage {
				r, g, _, _ := img.At(120, 80).RGBA()
				if r < 50000 || g > 1000 {
					t.Fatal("image preview did not show the picture")
				}
			}
			saveUIArtifact(t, tt, "preview-"+string(kind))
		})
	}
}

func TestPreparePreviewFormatsCutsAndStripsFences(t *testing.T) {
	pad := strings.Repeat("x", 220)
	body := preparePreview(KindCode, `{"node":"result","label":[1,2],"pad":"`+pad+`"}`)
	if !strings.Contains(body.text, "\n  \"node\": \"result\",") || body.lines < 4 || body.truncated || body.code == nil {
		t.Fatalf("minified JSON was not laid out: %+v", body)
	}
	body = preparePreview(KindCode, `{"node":"a","pad":"`+pad+`"}`+"\n"+`{"node":"b","pad":"`+pad+`"}`)
	if !strings.Contains(body.text, "\n}\n{\n  \"node\": \"b\",") {
		t.Fatalf("JSON lines were not laid out: %q", body.text[:min(len(body.text), 120)])
	}
	laidOut := "{\n  \"node\": \"result\"\n}"
	if body = preparePreview(KindCode, laidOut); body.text != laidOut {
		t.Fatalf("laid out JSON changed: %q", body.text)
	}
	got := formatJSON(`{"a": NaN, "s": "x,{y}\"", "e": {}, "b": [1, 2`)
	want := "{\n  \"a\": NaN,\n  \"s\": \"x,{y}\\\"\",\n  \"e\": {},\n  \"b\": [\n    1,\n    2"
	if got != want {
		t.Fatalf("lenient JSON layout:\n%s\nwant:\n%s", got, want)
	}
	body = preparePreview(KindCode, "```go\nfunc main() {}\n```")
	if body.text != "func main() {}" {
		t.Fatalf("fence was not stripped: %q", body.text)
	}
	long := strings.Repeat("一", previewMaxRunes+10)
	body = preparePreview(KindText, long)
	if !body.truncated || utf8.RuneCountInString(body.text) != previewMaxRunes {
		t.Fatalf("long text was not cut: truncated=%v, runes=%d", body.truncated, utf8.RuneCountInString(body.text))
	}
	lines := strings.Repeat("x\n", previewMaxLines+5)
	body = preparePreview(KindText, lines)
	if !body.truncated || body.lines != previewMaxLines+5 || strings.Count(body.text, "\n") != previewMaxLines-1 {
		t.Fatalf("many lines were not cut: truncated=%v, lines=%d, kept=%d", body.truncated, body.lines, strings.Count(body.text, "\n")+1)
	}
	if got := hexRGB("#4f5dff"); got != "rgb(79, 93, 255)" {
		t.Fatalf("hexRGB: %q", got)
	}
	if got := hexRGB("#fff"); got != "rgb(255, 255, 255)" {
		t.Fatalf("short hexRGB: %q", got)
	}
}

func TestPreviewReadsWholeTextOfLongClip(t *testing.T) {
	s := testStore(t)
	text := strings.Repeat("abcdefghij", 200) + "END"
	if err := s.Add(&Clip{Kind: KindText, Text: text, Length: len(text), Hash: "long-text"}); err != nil {
		t.Fatal(err)
	}
	clips, err := s.List(FilterAll, "")
	if err != nil || len(clips) != 1 || len(clips[0].Text) != previewRunes {
		t.Fatalf("list fixture: %v, clips=%d", err, len(clips))
	}
	whole, err := s.Text(clips[0].ID)
	if err != nil || whole != text {
		t.Fatalf("Store.Text did not return the whole text: %v, len=%d", err, len(whole))
	}
	a := &app{store: s}
	if p := a.previewOf(&Clip{ID: 99, Kind: KindText, Text: "short"}); !p.loaded || p.text != "short" {
		t.Fatalf("a short clip should preview at once: %+v", p)
	}
}

func TestThumbnailLRUEvictsOldEntriesAndCancelsJobs(t *testing.T) {
	a := &app{thumbs: map[int64]*thumb{}}
	contexts := map[int64]context.Context{}
	for id := int64(1); id <= 30; id++ {
		ctx, cancel := context.WithCancel(context.Background())
		contexts[id] = ctx
		a.thumbs[id] = &thumb{cancel: cancel, used: uint64(id), pinned: id == 1 || id == 30}
	}
	t.Cleanup(a.clearThumbs)
	a.trimThumbs()
	if len(a.thumbs) != thumbCacheLimit || a.thumbs[1] == nil || a.thumbs[30] == nil {
		t.Fatalf("cache limit or pinned entries: count=%d", len(a.thumbs))
	}
	for id := int64(2); id <= 7; id++ {
		if a.thumbs[id] != nil || contexts[id].Err() != context.Canceled {
			t.Fatalf("old entry %d was not evicted and canceled", id)
		}
	}
	if contexts[1].Err() != nil || contexts[30].Err() != nil {
		t.Fatal("visible thumbnail job was canceled")
	}
}

func TestThumbnailPixelsReleaseAfterPresentationWithoutInput(t *testing.T) {
	bitmap, err := ui.DecodeBitmap(trayIconPNG)
	if err != nil {
		t.Fatal(err)
	}
	th := &thumb{bitmap: bitmap}
	builds := 0
	tt := ui.NewTester(func(c *ui.Context) {
		builds++
		image := ui.Image(c, bitmap).Size(64, 64)
		th.releasePixels(c, image)
		if builds == 1 && th.pixelsReleased {
			t.Fatal("pixels released before the first committed frame")
		}
	}, 80, 80)
	tt.Advance(50 * time.Millisecond)
	if !th.pixelsReleased || builds > 3 {
		t.Fatalf("settled image did not release promptly: released=%v, builds=%d", th.pixelsReleased, builds)
	}
	tt.Advance(time.Second)
	if tt.FramePending() {
		t.Fatal("pixel release keeps requesting idle frames")
	}
}

func TestPreviewPinsOnlyTheChosenImage(t *testing.T) {
	a := stripTestApp()
	a.thumbs = map[int64]*thumb{}
	for i, clip := range a.clips {
		clip.Kind, clip.Width, clip.Height = KindImage, 32, 32
		bitmap, err := ui.DecodeBitmap(trayIconPNG)
		if err != nil {
			t.Fatal(err)
		}
		a.thumbs[clip.ID] = &thumb{bitmap: bitmap, cancel: func() {}, used: uint64(i)}
	}
	a.chosen = 5
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Themed(c, lightTheme).Fill().Children(func() { a.content(c) })
	}, panelWidth, panelHeight)
	id := a.clips[5].ID
	chosen := a.thumbs[id]
	if len(a.thumbs) != thumbCacheLimit || chosen == nil || !chosen.pinned {
		t.Fatalf("preview cache: count=%d, chosen=%v", len(a.thumbs), chosen)
	}
	for other, th := range a.thumbs {
		if th.pinned && other != id {
			t.Fatalf("image %d stayed pinned outside the preview", other)
		}
	}
	tt.Advance(50 * time.Millisecond)
}

func TestPreviewDoesNotRetainEvictedBitmap(t *testing.T) {
	a := stripTestApp()
	a.clips = a.clips[:100]
	a.thumbs = map[int64]*thumb{}
	add := func(id int64) {
		bitmap, err := ui.DecodeBitmap(trayIconPNG)
		if err != nil {
			t.Fatal(err)
		}
		a.thumbs[id] = &thumb{bitmap: bitmap, cancel: func() {}, used: uint64(id)}
	}
	for _, clip := range a.clips {
		clip.Kind, clip.Width, clip.Height = KindImage, 32, 32
		add(clip.ID)
	}
	old := weak.Make(a.thumbs[1].bitmap)
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Themed(c, lightTheme).Fill().Children(func() { a.content(c) })
		a.scrollTo = false
	}, panelWidth, panelHeight)
	// Supply the next picture without spawning real window jobs in Tester.
	add(50)
	a.choose(49)
	tt.Frame()
	tt.Advance(time.Second)
	if a.thumbs[1] != nil {
		t.Fatal("the picture left behind was not evicted from the application cache")
	}
	runtime.GC()
	if old.Value() != nil {
		t.Fatal("the preview still retains the evicted bitmap")
	}
	runtime.KeepAlive(tt)
}
