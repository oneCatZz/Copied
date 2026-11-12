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
	"testing"
	"time"
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

func stripTester(a *app) *ui.Tester {
	return ui.NewTester(func(c *ui.Context) {
		ui.Themed(c, lightTheme).Fill().Children(func() { a.strip(c) })
		a.scrollTo = false
	}, 1000, 280)
}

func TestStripDefersDistantCardsAndRevealsSelection(t *testing.T) {
	a := stripTestApp()
	tt := stripTester(a)
	previews := 0
	for _, text := range tt.Texts() {
		if len(text) >= 5 && text[:5] == "clip-" {
			previews++
		}
	}
	if previews < 4 || previews > 12 || tt.HasText("clip-199") || a.stripScroll.MaxX < 45000 {
		t.Fatalf("unexpected windowing: previews=%d, maxX=%v", previews, a.stripScroll.MaxX)
	}
	a.stripScroll.X = 100 * (cardWidth + cardGap)
	tt.Frame()
	if !tt.HasText("clip-100") {
		t.Fatal("scrolling did not materialize distant card contents")
	}
	a.choose(199)
	tt.Frame()
	tt.Advance(time.Second)
	r, ok := tt.Find("clip-199")
	if !ok || r.X < 0 || r.X+r.W > 1001 {
		t.Fatalf("selection was not revealed: %v, found=%v", r, ok)
	}
	saveUIArtifact(t, tt, "strip-last-card")
	a.stripScroll.X = 0
	tt.Frame()
	if !tt.HasText("clip-000") {
		t.Fatal("returning to the start did not restore card contents")
	}
}

func TestStripWheelAccumulationReverseAndNativeGestures(t *testing.T) {
	a := stripTestApp()
	tt := stripTester(a)
	tt.Wheel(500, 100, 0, 120)
	tt.Wheel(500, 100, 0, 120)
	tt.Advance(time.Second)
	if math.Abs(float64(a.stripScroll.X-240)) > 0.5 {
		t.Fatalf("wheel notches did not accumulate: %v", a.stripScroll.X)
	}
	before := a.stripScroll.X
	tt.Scroll(500, 100, 180, 0)
	if math.Abs(float64(a.stripScroll.X-before-180)) > 0.5 {
		t.Fatalf("native horizontal gesture was changed: %v", a.stripScroll.X-before)
	}
	before = a.stripScroll.X
	tt.Scroll(500, 100, 0, 90)
	if a.stripScroll.X != before {
		t.Fatal("precise vertical gesture was remapped")
	}
	tt.HoldModifiers(ui.Shift)
	tt.Wheel(500, 100, 0, 120)
	tt.Advance(time.Second)
	tt.HoldModifiers(0)
	if a.stripScroll.X <= before {
		t.Fatal("Shift+wheel no longer scrolls sideways")
	}
	a.stripScroll.X = 0
	tt.Frame()
	tt.Wheel(500, 100, 0, 120)
	tt.Wheel(500, 100, 0, -120)
	tt.Advance(time.Second)
	if a.stripScroll.X > 0.5 {
		t.Fatalf("reverse wheel at start did not stop pending travel: %v", a.stripScroll.X)
	}
	a.stripScroll.X = a.stripScroll.MaxX
	tt.Frame()
	tt.Wheel(500, 100, 0, 120)
	tt.Wheel(500, 100, 0, -120)
	tt.Advance(time.Second)
	if a.stripScroll.X >= a.stripScroll.MaxX {
		t.Fatal("reverse wheel at end was swallowed")
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
	tt := ui.NewTester(a.view, 1200, panelHeight)
	if !tt.HasText("Copied") || !tt.HasText("clip-000") {
		t.Fatal("panel is missing its header or cards")
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

func TestPreviewKeepsRoundedImageAndBoundsCode(t *testing.T) {
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
					a.preview(c, 0, clip, kindColor(kind, false))
				})
			}, 240, 160)
			img := tt.Image()
			for _, point := range []image.Point{{13, 1}, {13, 158}, {5, 80}, {235, 80}} {
				r, g, b, _ := img.At(point.X, point.Y).RGBA()
				if r != 65535 || g != 65535 || b != 65535 {
					t.Fatalf("preview escaped its bounds at %v: %v", point, img.At(point.X, point.Y))
				}
			}
			if kind == KindImage {
				r, g, _, _ := img.At(120, 80).RGBA()
				if r < 50000 || g > 1000 {
					t.Fatal("image preview did not fill its surface")
				}
			}
			saveUIArtifact(t, tt, "preview-"+string(kind))
		})
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

func TestThumbnailCacheProtectsWideViewport(t *testing.T) {
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
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Themed(c, lightTheme).Fill().Children(func() { a.strip(c) })
	}, 9000, 280)
	if a.thumbLimit <= thumbCacheLimit || len(a.thumbs) > a.thumbLimit {
		t.Fatalf("wide viewport cache: count=%d, limit=%d", len(a.thumbs), a.thumbLimit)
	}
	for _, th := range a.thumbs {
		if !th.pinned {
			t.Fatal("wide viewport kept a distant image over an on-screen one")
		}
	}
	tt.Advance(50 * time.Millisecond)
}

func TestOffscreenCardDoesNotRetainEvictedBitmap(t *testing.T) {
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
	old := weak.Make(a.thumbs[2].bitmap)
	tt := stripTester(a)
	// Supply the next viewport without spawning real window jobs in Tester.
	for id := int64(81); id <= 90; id++ {
		if a.thumbs[id] == nil {
			add(id)
		}
	}
	a.stripScroll.X = 80 * (cardWidth + cardGap)
	tt.Frame()
	tt.Advance(time.Second)
	if a.thumbs[2] != nil {
		t.Fatal("old image was not evicted from the application cache")
	}
	runtime.GC()
	if old.Value() != nil {
		t.Fatal("offscreen card transition still retains the evicted bitmap")
	}
	runtime.KeepAlive(tt)
}
