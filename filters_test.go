package main

import (
	"fmt"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"copied/locales"

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

func TestTypeDropdownFiltersAndDismisses(t *testing.T) {
	a := filterTestApp(t)
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Themed(c, lightTheme).Fill().Padding(8).Children(func() { a.filters(c) })
	}, 320, panelHeight)
	tt.SetLocale(languageChinese)
	if err := tt.Click("剪贴板类型"); err != nil {
		t.Fatal(err)
	}
	saveUIArtifact(t, tt, "type-dropdown")
	if err := tt.Click("文本"); err != nil {
		t.Fatal(err)
	}
	if a.filter != FilterText || len(a.clips) != 2 {
		t.Fatalf("text filter: filter=%v, records=%d", a.filter, len(a.clips))
	}
	if a.typeMenuOpen || tt.HasText("图片") || !tt.HasText("类型：文本") {
		t.Fatal("type dropdown stayed open or lost its selected label")
	}
	if err := tt.Click("剪贴板类型"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("图片"); err != nil {
		t.Fatal(err)
	}
	if a.filter != FilterImage || len(a.clips) != 1 || a.clips[0].Kind != KindImage {
		t.Fatal("image filter did not select its records")
	}
	tt.Advance(time.Second)
	if tt.FramePending() {
		t.Fatal("settled type picker keeps requesting frames")
	}
	saveUIArtifact(t, tt, "filters-settled")
}

func TestTypeDropdownDarkAppearanceAndApplicationDismissal(t *testing.T) {
	a := filterTestApp(t)
	tt := ui.NewTesterWith(func(c *ui.Context) {
		ui.Themed(c, darkTheme).Fill().Padding(8).Children(func() { a.filters(c) })
	}, 320, panelHeight, ui.TesterOptions{Dark: true, Preferences: ui.Preferences{ReduceMotion: true}})
	tt.SetLocale(languageChinese)
	if err := tt.Click("剪贴板类型"); err != nil {
		t.Fatal(err)
	}
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
	if err := tt.Click("剪贴板类型"); err != nil {
		t.Fatal(err)
	}
	a.dismissGroupUI()
	a.searchSession++
	tt.Frame()
	tt.Advance(time.Second)
	if a.typeMenuOpen || tt.HasText("图片") {
		t.Fatal("type dropdown survived application dismissal")
	}
}

func TestTypePanelGridNavigationAndThemes(t *testing.T) {
	for _, locale := range []string{languageChinese, languageEnglish} {
		for _, dark := range []bool{false, true} {
			name := locale + "-light"
			if dark {
				name = locale + "-dark"
			}
			t.Run(name, func(t *testing.T) {
				a := filterTestApp(t)
				tt := ui.NewTesterWith(func(c *ui.Context) {
					theme := lightTheme
					if c.Theme().Dark {
						theme = darkTheme
					}
					ui.Themed(c, theme).Fill().Padding(16).Children(func() { a.filters(c) })
				}, 340, 280, ui.TesterOptions{Dark: dark})
				tt.SetLocale(locale)
				tx := locales.In(locale)
				if err := tt.Click(tx.ClipboardTypes()); err != nil {
					t.Fatal(err)
				}
				all, _ := tt.AccessByRole(ui.RoleOption, tx.All())
				text, found := tt.AccessByRole(ui.RoleOption, tx.Text())
				if !found || text.Bounds.X <= all.Bounds.X || math.Abs(float64(text.Bounds.Y-all.Bounds.Y)) > 1 {
					t.Fatal("type options did not form two columns")
				}
				saveUIArtifact(t, tt, "type-panel-"+name)
				tt.Key(0, ui.KeyHome)
				tt.Key(0, ui.KeyRight)
				tt.Key(0, ui.KeyDown)
				tt.Key(0, ui.KeyLeft)
				tt.Key(0, ui.KeyEnter)
				if a.filter != FilterCode || a.typeMenuOpen {
					t.Fatalf("spatial navigation did not choose code: filter=%v, open=%v", a.filter, a.typeMenuOpen)
				}
			})
		}
	}
}

func TestTypePanelFitsWindowEdge(t *testing.T) {
	for _, width := range []int{560, 780, 1200} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			a := groupTestApp(t)
			tt := ui.NewTester(a.view, width, panelHeight)
			tt.SetLocale(languageChinese)
			if err := tt.Click("剪贴板类型"); err != nil {
				t.Fatal(err)
			}
			labels := filterLabelsFor(languageChinese)
			for _, label := range labels[:int(FilterPinned)] {
				option, found := tt.AccessByRole(ui.RoleOption, label)
				r := option.Bounds
				if !found || r.W < 100 || r.X < 0 || r.Y < 0 || r.X+r.W > float32(width)+1 || r.Y+r.H > panelHeight {
					t.Fatalf("type option %q is clipped at the window edge: %v, found=%v", label, r, found)
				}
			}
			saveUIArtifact(t, tt, fmt.Sprintf("type-panel-full-%d", width))
		})
	}
}
