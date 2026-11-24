package main

import (
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func editTestApp(t *testing.T, texts ...string) *app {
	t.Helper()
	s := testStore(t)
	for _, text := range texts {
		if err := s.Add(&Clip{Kind: textKind(text), Text: text, Length: len([]rune(text)), Hash: hashOf(KindText, []byte(text))}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond) // a newer used_at for each
	}
	a := &app{store: s, thumbs: map[int64]*thumb{}, since: 2 * time.Second, shownAt: time.Now().Add(-2 * time.Second)}
	a.reload()
	return a
}

func TestStoreUpdateTextKeepsPlaceAndMergesDuplicates(t *testing.T) {
	a := editTestApp(t, "first", "second")
	s := a.store
	first, second := a.clips[1].ID, a.clips[0].ID
	if err := s.SetGroupMember(second, defaultGroupID, true); err != nil {
		t.Fatal(err)
	}
	if saved, err := s.UpdateText(first, "https://example.com/edited", false); !saved || err != nil {
		t.Fatal(saved, err)
	}
	clip, err := s.Get(first)
	if err != nil || clip == nil || clip.Text != "https://example.com/edited" || clip.Kind != KindLink {
		t.Fatalf("edited clip: %+v, %v", clip, err)
	}
	// Without merging, the text of another clip is not saved.
	if saved, err := s.UpdateText(first, "second", false); saved || err != nil {
		t.Fatalf("a duplicate saved without merging: %v, %v", saved, err)
	}
	if clips, _ := s.List(FilterAll, ""); len(clips) != 2 {
		t.Fatalf("a clip went away without merging: %d", len(clips))
	}
	// Merging, the other clip gives way, its groups joining the edited one.
	if saved, err := s.UpdateText(first, "second", true); !saved || err != nil {
		t.Fatal(saved, err)
	}
	clips, err := s.List(FilterAll, "")
	if err != nil || len(clips) != 1 || clips[0].ID != first || clips[0].Text != "second" || clips[0].Kind != KindText {
		t.Fatalf("merged clips: %+v, %v", clips, err)
	}
	if ids, _ := s.ClipGroupIDs(first); !slices.Contains(ids, defaultGroupID) || !clips[0].Pinned {
		t.Fatalf("groups of the clip that gave way were lost: %v, pinned=%v", ids, clips[0].Pinned)
	}
}

func storedText(t *testing.T, a *app, id int64) string {
	t.Helper()
	clip, err := a.store.Get(id)
	if err != nil || clip == nil {
		t.Fatalf("clip %d: %+v, %v", id, clip, err)
	}
	return clip.Text
}

func TestPreviewEditSavesAutomatically(t *testing.T) {
	a := editTestApp(t, "other", "hello")
	tt := ui.NewTester(a.view, panelWidth, panelHeight)
	id, other := a.clips[0].ID, a.clips[1].ID
	if err := tt.Click("Edit"); err != nil {
		t.Fatal(err)
	}
	if !a.editing() || a.preview.draft != "hello" || !tt.HasText("Changes save automatically") {
		t.Fatalf("editing did not start: editing=%v, draft=%q", a.editing(), a.preview.draft)
	}
	// Keys go to the editor rather than to the list or the panel.
	tt.Key(0, ui.KeyEnd)
	tt.Type(" world")
	tt.Key(0, ui.KeyEnter)
	tt.Type("again")
	if a.preview.draft != "hello world\nagain" || !a.editing() {
		t.Fatalf("typing did not reach the editor: %q", a.preview.draft)
	}
	saveUIArtifact(t, tt, "preview-editing")
	if storedText(t, a, id) != "hello" {
		t.Fatal("saved before typing paused")
	}
	tt.Advance(time.Second)
	if got := storedText(t, a, id); got != "hello world\nagain" || !a.editing() {
		t.Fatalf("a pause in typing did not save: %q, editing=%v", got, a.editing())
	}
	if !tt.HasText("hello world again") {
		t.Fatal("the list did not show the saved text")
	}

	// A text passing by that another clip holds does not take it away.
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("other")
	tt.Advance(time.Second)
	if storedText(t, a, id) != "hello world\nagain" || !a.store.Has(other) {
		t.Fatal("typing the text of another clip merged them before the editing ended")
	}
	tt.Type(" edited")
	tt.Advance(time.Second)

	// Escape leaves the editor, keeping what was typed, and the panel shows.
	tt.Type("!")
	tt.Key(0, ui.KeyEscape)
	if a.editing() || storedText(t, a, id) != "other edited!" {
		t.Fatalf("Escape: editing=%v, text=%q", a.editing(), storedText(t, a, id))
	}
	tt.Frame()
	saveUIArtifact(t, tt, "preview-saved")

	// Choosing another clip keeps what was typed in this one.
	if err := tt.Click("Edit"); err != nil {
		t.Fatal(err)
	}
	tt.Type(" more")
	a.choose(1)
	tt.Frame()
	tt.Frame()
	if a.editing() || storedText(t, a, id) != "other edited! more" {
		t.Fatalf("switching clips: editing=%v, text=%q", a.editing(), storedText(t, a, id))
	}
	if !tt.HasText("other edited! more") {
		t.Fatal("the list did not show the text saved on switching")
	}
}

func TestPreviewOffersEditingOnlyForText(t *testing.T) {
	a := filterTestApp(t)
	tt := ui.NewTester(a.view, panelWidth, panelHeight)
	for i, clip := range a.clips {
		a.choose(i)
		tt.Frame()
		if got := tt.HasText("Edit"); got != editable(clip.Kind) {
			t.Fatalf("%s clip: Edit shown=%v", clip.Kind, got)
		}
	}
	a.picked = []int64{a.clips[0].ID, a.clips[1].ID}
	a.chosen = 0
	tt.Frame()
	if tt.HasText("Edit") || !tt.HasText("Delete all") {
		t.Fatal("several clips picked should offer deleting them, not editing")
	}
}

func TestBatchDeleteAsksFirst(t *testing.T) {
	a := editTestApp(t, "one", "two", "three")
	tt := ui.NewTester(a.view, panelWidth, panelHeight)
	keep := a.clips[2].ID
	a.clickCard(0, 0)
	a.clickCard(1, ui.Shift)
	tt.Frame()
	if len(a.picked) != 2 {
		t.Fatalf("picked: %v", a.picked)
	}
	tt.Key(ui.Cmd, ui.KeyBackspace)
	if !a.deleteOpen || !tt.HasText("Delete 2 items?") {
		t.Fatal("deleting several clips did not ask first")
	}
	saveUIArtifact(t, tt, "delete-clips")
	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	if a.deleteOpen || len(a.clips) != 3 || len(a.picked) != 2 {
		t.Fatalf("Cancel deleted: clips=%d, picked=%v", len(a.clips), a.picked)
	}
	if err := tt.Click("Delete all"); err != nil {
		t.Fatal(err)
	}
	if !a.deleteOpen {
		t.Fatal("the Delete all button did not ask first")
	}
	if err := tt.Click("Delete"); err != nil {
		t.Fatal(err)
	}
	if a.deleteOpen || len(a.clips) != 1 || a.clips[0].ID != keep || len(a.picked) != 0 {
		t.Fatalf("batch delete: open=%v, clips=%d, picked=%v", a.deleteOpen, len(a.clips), a.picked)
	}
	tt.Advance(time.Second)
}

func TestPreviewEditEmptiedDeletesOnLeaving(t *testing.T) {
	a := editTestApp(t, "keep", "switch", "remove")
	tt := ui.NewTester(a.view, panelWidth, panelHeight)
	id, switched, keep := a.clips[0].ID, a.clips[1].ID, a.clips[2].ID
	a.stack = []int64{id}
	if err := tt.Click("Edit"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(0, ui.KeyBackspace)
	tt.Advance(time.Second)
	// Emptied while typing, it stays until the editing ends.
	if !a.editing() || storedText(t, a, id) != "remove" || !tt.HasText("Leaving it empty deletes this item") {
		t.Fatalf("emptying while typing: editing=%v, draft=%q", a.editing(), a.preview.draft)
	}
	tt.Key(0, ui.KeyEscape)
	if a.editing() || a.store.Has(id) || len(a.clips) != 2 || len(a.stack) != 0 {
		t.Fatalf("leaving it empty: editing=%v, has=%v, clips=%d, stack=%v", a.editing(), a.store.Has(id), len(a.clips), a.stack)
	}

	// Choosing another clip after emptying one deletes it too.
	a.choose(0)
	tt.Frame()
	if err := tt.Click("Edit"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(0, ui.KeyBackspace)
	a.choose(1)
	tt.Frame()
	tt.Frame()
	if a.store.Has(switched) || len(a.clips) != 1 || a.clips[0].ID != keep || a.chosen != 0 {
		t.Fatalf("an emptied clip left for another: has=%v, clips=%d", a.store.Has(switched), len(a.clips))
	}
}
