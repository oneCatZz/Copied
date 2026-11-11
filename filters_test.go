package main

import (
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := openStoreAt(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}

func saveUIArtifact(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	dir := os.Getenv("COPIED_TEST_ARTIFACTS")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, tt.Image()); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func filterTestApp(t *testing.T) *app {
	t.Helper()
	s := testStore(t)
	for _, clip := range []*Clip{
		{Kind: KindText, Text: "hello", Hash: "text"},
		{Kind: KindColor, Text: "#4f5dff", Hash: "color"},
		{Kind: KindLink, Text: "https://example.com", Hash: "link"},
		{Kind: KindImage, Hash: "image"},
	} {
		if err := s.Add(clip); err != nil {
			t.Fatal(err)
		}
	}
	a := &app{store: s, thumbs: map[int64]*thumb{}}
	a.reload()
	return a
}

func TestFilterFlowGeometry(t *testing.T) {
	now := time.Now()
	for _, positions := range [][2]float32{{0, 250}, {250, 0}, {50, 100}, {100, 50}} {
		f := filterFlow{left: positions[0], right: positions[0] + filterChipWidth, target: positions[1], started: now}
		for elapsed := time.Duration(0); elapsed <= filterFlowTime; elapsed += time.Millisecond {
			left, right, _ := f.edges(now.Add(elapsed))
			if math.IsNaN(float64(left)) || math.IsNaN(float64(right)) || right-left < filterChipWidth-0.01 || right-left > 80 {
				t.Fatalf("invalid capsule at %v: %v..%v", elapsed, left, right)
			}
		}
		left, right, moving := f.edges(now.Add(filterFlowTime))
		if moving || left != f.target || right != f.target+filterChipWidth {
			t.Fatalf("capsule did not settle: %v..%v, moving=%v", left, right, moving)
		}
	}
}

func TestFiltersSelectAndInterruptWithoutRebuilding(t *testing.T) {
	a := filterTestApp(t)
	builds := 0
	tt := ui.NewTester(func(c *ui.Context) {
		builds++
		ui.Themed(c, lightTheme).Fill().Padding(8).Children(func() { a.filters(c) })
	}, 320, 44)
	if err := tt.Click("文本"); err != nil {
		t.Fatal(err)
	}
	if a.filter != FilterText || len(a.clips) != 2 {
		t.Fatalf("text filter: filter=%v, records=%d", a.filter, len(a.clips))
	}
	before := builds
	tt.Advance(120 * time.Millisecond)
	if builds != before {
		t.Fatalf("paint-only animation rebuilt the view %d times", builds-before)
	}
	saveUIArtifact(t, tt, "filters-moving")
	left, right, _ := a.filterFlow.edges(tt.Now())
	if err := tt.Click("图片"); err != nil {
		t.Fatal(err)
	}
	newLeft, newRight, _ := a.filterFlow.edges(tt.Now())
	if math.Abs(float64(newLeft-left)) > 0.01 || math.Abs(float64(newRight-right)) > 0.01 {
		t.Fatalf("interrupted capsule jumped: %v..%v -> %v..%v", left, right, newLeft, newRight)
	}
	if a.filter != FilterImage || len(a.clips) != 1 || a.clips[0].Kind != KindImage {
		t.Fatal("image filter did not select its records")
	}
	tt.Advance(time.Second)
	if tt.FramePending() {
		t.Fatal("filter animation keeps requesting frames after settling")
	}
	saveUIArtifact(t, tt, "filters-settled")
}

func TestFiltersReducedMotionAndDarkAppearance(t *testing.T) {
	a := filterTestApp(t)
	tt := ui.NewTesterWith(func(c *ui.Context) {
		ui.Themed(c, darkTheme).Fill().Padding(8).Children(func() { a.filters(c) })
	}, 320, 44, ui.TesterOptions{Dark: true, Preferences: ui.Preferences{ReduceMotion: true}})
	if err := tt.Click("链接"); err != nil {
		t.Fatal(err)
	}
	if a.filter != FilterLink || len(a.clips) != 1 {
		t.Fatal("link filter did not select its records")
	}
	if tt.FramePending() {
		t.Fatal("reduced motion still schedules filter animation")
	}
	saveUIArtifact(t, tt, "filters-dark-reduced-motion")
}
