package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func cardMenuTester(a *app, dark bool) *ui.Tester {
	return ui.NewTesterWith(func(c *ui.Context) {
		t := lightTheme
		if c.Theme().Dark {
			t = darkTheme
		}
		var action func()
		ui.Themed(c, t).Fill().Children(func() {
			ui.Row(c).Children(func() {
				for i, clip := range a.clips {
					card := ui.Box(c.Key(clip.ID)).Size(140, 120).Background(t.Surface).
						Label(fmt.Sprintf("clip-%d", clip.ID))
					if chosen := a.cardMenu(c, card, i, clip); chosen != nil {
						action = chosen
					}
				}
			})
		})
		if action != nil {
			action()
		}
	}, 640, panelHeight, ui.TesterOptions{Dark: dark})
}

func TestCardMenuActionsTargetTheRightClickedClip(t *testing.T) {
	a := filterTestApp(t)
	a.filter = FilterText
	a.reload()
	first, second := a.clips[0].ID, a.clips[1].ID
	tt := cardMenuTester(a, false)
	tt.RightClickAt(40, 50)
	if !tt.HasText("粘贴为纯文本") || a.cardMenuID != first {
		t.Fatal("text context menu did not open")
	}
	tt.Key(0, ui.KeyEscape)
	if a.cardMenuID != 0 || tt.HasText("仅复制") {
		t.Fatal("Escape did not close the context menu")
	}
	tt.RightClickAt(180, 50)
	if a.cardMenuID != second || a.chosen != 1 {
		t.Fatal("context menu did not move to the other card")
	}
	if err := tt.Click("收藏"); err != nil {
		t.Fatal(err)
	}
	pinned, err := a.store.List(FilterPinned, "")
	if err != nil || len(pinned) != 1 || pinned[0].ID != second || a.cardMenuID != 0 {
		t.Fatalf("favorite target or dismissal: clips=%+v, error=%v, menu=%d", pinned, err, a.cardMenuID)
	}
	for _, clip := range a.clips {
		if clip.ID == first && clip.Pinned {
			t.Fatal("context menu changed the selected card instead of the right-clicked card")
		}
	}
	tt.RightClickAt(180, 50)
	if !tt.HasText("取消收藏") {
		t.Fatal("favorite context menu state was not refreshed")
	}
	tt.Key(0, ui.KeyEnd)
	tt.Key(0, ui.KeyEnter)
	clip, err := a.store.Get(second)
	if err != nil || clip != nil || len(a.clips) != 1 || a.cardMenuID != 0 {
		t.Fatalf("delete target or dismissal: clip=%+v, error=%v, menu=%d", clip, err, a.cardMenuID)
	}
	tt.Advance(time.Second)
}

func TestCardMenuAppearanceAndExplicitDismissal(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, kind := range []Kind{KindText, KindImage, KindFiles} {
			t.Run(fmt.Sprintf("dark-%v-kind-%v", dark, kind), func(t *testing.T) {
				a := &app{clips: []*Clip{{ID: 1, Kind: kind}}}
				tt := cardMenuTester(a, dark)
				tt.RightClickAt(40, 50)
				if !tt.HasText("仅复制") || tt.HasText("粘贴为纯文本") != (kind == KindText) {
					t.Fatal("context menu does not match the clip type")
				}
				saveUIArtifact(t, tt, fmt.Sprintf("card-menu-dark-%v-kind-%v", dark, kind))
				// Reusing the same renderer must observe the application's close.
				a.cardMenuID = 0
				tt.Frame()
				tt.Advance(time.Second)
				if tt.HasText("仅复制") {
					t.Fatal("application dismissal left the menu visible")
				}
				tt.RightClickAt(40, 50)
				tt.ClickAt(600, 300)
				if tt.HasText("仅复制") || a.cardMenuID != 0 {
					t.Fatal("outside click did not dismiss the reopened context menu")
				}
			})
		}
	}
}
