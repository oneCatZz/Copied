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
	if err := s.UpdateText(first, "https://example.com/edited"); err != nil {
		t.Fatal(err)
	}
	clip, err := s.Get(first)
	if err != nil || clip == nil || clip.Text != "https://example.com/edited" || clip.Kind != KindLink {
		t.Fatalf("edited clip: %+v, %v", clip, err)
	}
	// Editing it to the text of another clip merges them, groups included.
	if err := s.UpdateText(first, "second"); err != nil {
		t.Fatal(err)
	}
	clips, err := s.List(FilterAll, "")
	if err != nil || len(clips) != 1 || clips[0].ID != first || clips[0].Text != "second" || clips[0].Kind != KindText {
		t.Fatalf("merged clips: %+v, %v", clips, err)
	}
	if ids, _ := s.ClipGroupIDs(first); !slices.Contains(ids, defaultGroupID) || !clips[0].Pinned {
		t.Fatalf("groups of the clip that gave way were lost: %v, pinned=%v", ids, clips[0].Pinned)
	}
}

func TestPreviewEditsTextAndSaves(t *testing.T) {
	a := editTestApp(t, "hello")
	tt := ui.NewTester(a.view, panelWidth, panelHeight)
	if err := tt.Click("Edit"); err != nil {
		t.Fatal(err)
	}
	if !a.editing() || a.preview.draft != "hello" || !tt.HasText("Save") {
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
	tt.Key(ui.Cmd, ui.KeyS)
	if a.editing() {
		t.Fatal("Cmd+S did not leave the editor")
	}
	clip, err := a.store.Get(a.clips[0].ID)
	if err != nil || clip == nil || clip.Text != "hello world\nagain" {
		t.Fatalf("the edit was not saved: %+v, %v", clip, err)
	}
	tt.Frame()
	if !tt.HasText("hello world") {
		t.Fatal("the preview did not show the saved text")
	}
	saveUIArtifact(t, tt, "preview-saved")

	// Escape forgets the draft without hiding the panel.
	if err := tt.Click("Edit"); err != nil {
		t.Fatal(err)
	}
	tt.Type("discarded")
	tt.Key(0, ui.KeyEscape)
	if a.editing() {
		t.Fatal("Escape did not leave the editor")
	}
	if clip, _ := a.store.Get(a.clips[0].ID); clip.Text != "hello world\nagain" {
		t.Fatalf("a cancelled edit was saved: %q", clip.Text)
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
