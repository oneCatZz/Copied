package main

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/sqlite"
	"github.com/egoist/mygo/ui"
)

func groupTestApp(t *testing.T) *app {
	t.Helper()
	a := &app{store: testStore(t), thumbs: map[int64]*thumb{}, shownAt: time.Now().Add(-time.Second)}
	for _, clip := range []*Clip{
		{Kind: KindText, Text: "meeting notes", Hash: "notes", Length: 13},
		{Kind: KindCode, Text: "const answer = 42;", Hash: "code", Length: 18},
		{Kind: KindLink, Text: "https://example.com", Hash: "link", Length: 19},
	} {
		if err := a.store.Add(clip); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"工作", "灵感"} {
		if _, err := a.store.SaveGroup(0, name); err != nil {
			t.Fatal(err)
		}
	}
	a.reload()
	for _, clip := range a.clips {
		if clip.Kind == KindCode || clip.Kind == KindText {
			if err := a.store.SetGroupMember(clip.ID, a.groups[1].ID, true); err != nil {
				t.Fatal(err)
			}
		}
		if clip.Kind == KindCode {
			if err := a.store.SetGroupMember(clip.ID, a.groups[2].ID, true); err != nil {
				t.Fatal(err)
			}
		}
	}
	a.reload()
	return a
}

func TestGroupsCombineWithTypeAndSearch(t *testing.T) {
	a := groupTestApp(t)
	work, ideas := a.groups[1].ID, a.groups[2].ID
	clips, err := a.store.List(FilterAll, "", work)
	if err != nil || len(clips) != 2 {
		t.Fatalf("work group: clips=%v, error=%v", clips, err)
	}
	clips, err = a.store.List(FilterCode, "answer", work)
	if err != nil || len(clips) != 1 || clips[0].Kind != KindCode || !slices.Contains(clips[0].GroupIDs, ideas) {
		t.Fatalf("combined group/type/search: clips=%v, error=%v", clips, err)
	}
	id := clips[0].ID
	if err := a.store.SetGroupMember(id, work, false); err != nil {
		t.Fatal(err)
	}
	clips, err = a.store.List(FilterAll, "answer", ideas)
	if err != nil || len(clips) != 1 || slices.Contains(clips[0].GroupIDs, work) {
		t.Fatalf("removing one membership affected another: clips=%v, error=%v", clips, err)
	}
	if _, err := a.store.SaveGroup(ideas, "代码灵感"); err != nil {
		t.Fatal(err)
	}
	if err := a.store.DeleteGroup(ideas); err != nil {
		t.Fatal(err)
	}
	if clip, err := a.store.Get(id); err != nil || clip == nil || clip.Text != "const answer = 42;" {
		t.Fatalf("deleting a group removed its clip: clip=%v, error=%v", clip, err)
	}
	if ids, err := a.store.ClipGroupIDs(id); err != nil || len(ids) != 0 {
		t.Fatalf("deleted group left membership behind: ids=%v, error=%v", ids, err)
	}
}

func TestGroupedClipsSurvivePruningAndClearing(t *testing.T) {
	s := testStore(t)
	groupID, err := s.SaveGroup(0, "保留")
	if err != nil {
		t.Fatal(err)
	}
	var statements []sqlite.Statement
	for i := 1; i <= historyLimit+5; i++ {
		statements = append(statements, sqlite.Statement{
			SQL: `INSERT INTO clips (id, kind, text, hash, created_at, used_at) VALUES (?, 'text', ?, ?, ?, ?)`,
			Args: []any{i, fmt.Sprint(i), fmt.Sprint(i), i, i},
		})
	}
	if _, err := s.db.Transaction(context.Background(), statements); err != nil {
		t.Fatal(err)
	}
	if err := s.SetGroupMember(1, groupID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(&Clip{Kind: KindText, Text: "newest", Hash: "newest"}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.db.Query(context.Background(), `SELECT count(*) FROM clips`)
	if err != nil || integer(rows.Values[0][0]) != historyLimit+1 {
		t.Fatalf("history limit did not exclude grouped clips: rows=%v, error=%v", rows, err)
	}
	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	clips, err := s.List(FilterAll, "")
	if err != nil || len(clips) != 1 || clips[0].ID != 1 {
		t.Fatalf("clear did not keep the oldest grouped clip: clips=%v, error=%v", clips, err)
	}
}

func TestLegacyFavoritesMigrateAndGroupsPersistAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	s, err := openStoreAt(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if s != nil {
			s.Close()
		}
	})
	if err := s.Add(&Clip{Kind: KindText, Text: "saved content", Hash: "legacy"}); err != nil {
		t.Fatal(err)
	}
	// Restore the relevant shape of the previous release's database.
	if _, err := s.db.Transaction(context.Background(), []sqlite.Statement{
		{SQL: `UPDATE clips SET pinned = 1`},
		{SQL: `DROP TABLE clip_groups`},
		{SQL: `DROP TABLE history_groups`},
		{SQL: `PRAGMA user_version = 1`},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = openStoreAt(path)
	if err != nil {
		t.Fatal(err)
	}
	clips, err := s.List(FilterAll, "", defaultGroupID)
	if err != nil || len(clips) != 1 || clips[0].Text != "saved content" || !clips[0].Pinned {
		t.Fatalf("legacy favorite was not migrated: clips=%v, error=%v", clips, err)
	}
	if _, err := s.SaveGroup(defaultGroupID, "常用"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = openStoreAt(path)
	if err != nil {
		t.Fatal(err)
	}
	groups, err := s.Groups()
	if err != nil || len(groups) != 1 || groups[0].Name != "常用" {
		t.Fatalf("group name did not persist, or migration duplicated it: groups=%v, error=%v", groups, err)
	}
	if err := s.DeleteGroup(defaultGroupID); err == nil {
		t.Fatal("default group was deleted")
	}
}

func TestGroupFlowInterruptsWithoutRebuilding(t *testing.T) {
	a := groupTestApp(t)
	builds := 0
	tt := ui.NewTester(func(c *ui.Context) {
		builds++
		ui.Themed(c, lightTheme).Fill().Padding(8).Children(func() { a.groupNavigation(c, 400) })
	}, 420, panelHeight)
	tt.SetLocale(languageChinese)
	if err := tt.Click("工作"); err != nil {
		t.Fatal(err)
	}
	if len(a.clips) != 2 {
		t.Fatal("group switch did not filter clips")
	}
	before := builds
	tt.Advance(120 * time.Millisecond)
	if builds != before {
		t.Fatalf("paint-only group animation rebuilt the view %d times", builds-before)
	}
	saveUIArtifact(t, tt, "groups-flow-moving")
	left, right, _ := a.filterFlow.edges(tt.Now())
	if err := tt.Click("灵感"); err != nil {
		t.Fatal(err)
	}
	nextLeft, nextRight, _ := a.filterFlow.edges(tt.Now())
	if math.Abs(float64(left-nextLeft)) > 0.01 || math.Abs(float64(right-nextRight)) > 0.01 || len(a.clips) != 1 {
		t.Fatal("interrupted group animation jumped or selected the wrong group")
	}
	tt.Advance(time.Second)
	if tt.FramePending() {
		t.Fatal("group animation did not settle")
	}
}

func TestGroupHeaderResponsiveLayoutAndOverflow(t *testing.T) {
	for _, width := range []int{560, 640, 780, 1200} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("width-%d-dark-%v", width, dark), func(t *testing.T) {
				a := groupTestApp(t)
				for i := 0; i < 10; i++ {
					id, err := a.store.SaveGroup(0, fmt.Sprintf("Project-%02d-long", i))
					if err != nil {
						t.Fatal(err)
					}
					a.groupID = id
				}
				a.reload()
				tt := ui.NewTesterWith(a.view, width, panelHeight, ui.TesterOptions{Dark: dark, Preferences: ui.Preferences{ReduceMotion: true}})
				tt.SetLocale(languageChinese)
				for _, label := range []string{"搜索", "剪贴板类型", "设置", "新建分组", "Project-09-long"} {
					r, found := tt.Find(label)
					if !found || r.X < 0 || r.X+r.W > float32(width)+1 || r.W <= 0 {
						t.Fatalf("header item %q does not fit: bounds=%v, found=%v", label, r, found)
					}
				}
				for _, label := range []string{"选择", "添加到分组", "关闭"} {
					r, found := tt.Find(label)
					if !found || r.H > 20 || r.X+r.W > float32(width)+1 {
						t.Fatalf("footer hint %q wrapped or overflowed: bounds=%v, found=%v", label, r, found)
					}
				}
				if tt.FramePending() {
					t.Fatal("reduced motion still schedules group animation")
				}
				saveUIArtifact(t, tt, fmt.Sprintf("groups-header-%d-dark-%v", width, dark))
			})
		}
	}
}

func TestGroupDialogsCreateRenameAssignAndDismiss(t *testing.T) {
	a := groupTestApp(t)
	tt := ui.NewTester(a.view, 1200, panelHeight)
	tt.SetLocale(languageChinese)
	if err := tt.Click("新建分组"); err != nil {
		t.Fatal(err)
	}
	if !tt.Focused("分组名称") {
		t.Fatal("new group input did not receive focus")
	}
	tt.Type("新项目")
	if err := tt.Click("保存"); err != nil {
		t.Fatal(err)
	}
	if a.groupDialogOpen || a.currentGroup().Name != "新项目" {
		t.Fatal("creating a group did not persist and select it")
	}
	if err := tt.Click("更多分组与操作"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("重命名分组"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("新版项目")
	tt.Key(0, ui.KeyEnter)
	if a.groupDialogOpen || a.currentGroup().Name != "新版项目" {
		t.Fatal("renaming a group through Enter did not persist")
	}
	a.selectGroup(0)
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyP)
	if !a.groupDialogOpen || a.groupClipID == 0 {
		t.Fatal("group shortcut did not open assignment")
	}
	clipID := a.groupClipID
	checkbox, found := tt.AccessByRole(ui.RoleCheckBox, "新版项目")
	if !found {
		t.Fatal("group assignment checkbox was not accessible")
	}
	tt.ClickAt(checkbox.Bounds.X+checkbox.Bounds.W/2, checkbox.Bounds.Y+checkbox.Bounds.H/2)
	ids, err := a.store.ClipGroupIDs(clipID)
	if err != nil || !slices.Contains(ids, a.groups[len(a.groups)-1].ID) {
		t.Fatalf("assignment was not persisted: ids=%v, error=%v", ids, err)
	}
	saveUIArtifact(t, tt, "groups-assignment")
	a.dismissGroupUI()
	a.searchSession++
	tt.Frame()
	tt.Advance(time.Second)
	if _, found := tt.AccessByRole(ui.RoleDialog, ""); found {
		t.Fatal("group dialog survived application dismissal")
	}
}

func TestTypeKeyboardSelectionDoesNotPasteOrOpenGroupDialog(t *testing.T) {
	a := groupTestApp(t)
	a.filter = FilterImage
	tt := ui.NewTester(a.view, 1200, panelHeight)
	tt.SetLocale(languageChinese)
	if err := tt.Click("剪贴板类型"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyHome)
	tt.Key(0, ui.KeyEnter)
	if a.typeMenuOpen || a.filter != FilterAll || a.groupDialogOpen {
		t.Fatal("keyboard type selection did not consume Enter")
	}
}

func TestGroupAssignmentCanCreateGroupAndRemoveCurrentMembership(t *testing.T) {
	a := groupTestApp(t)
	a.selectGroup(a.groups[1].ID)
	tt := ui.NewTester(a.view, 1200, panelHeight)
	tt.SetLocale(languageChinese)
	tt.Key(ui.Cmd, ui.KeyP)
	clipID := a.groupClipID
	checkbox, found := tt.AccessByRole(ui.RoleCheckBox, "工作")
	if !found || !checkbox.Checked {
		t.Fatal("current group membership was not shown")
	}
	tt.ClickAt(checkbox.Bounds.X+checkbox.Bounds.W/2, checkbox.Bounds.Y+checkbox.Bounds.H/2)
	if !a.groupDialogOpen || len(a.clips) != 1 {
		t.Fatal("removing the current membership dismissed assignment or kept a stale card")
	}
	create, found := tt.AccessByRole(ui.RoleButton, "新建分组")
	if !found {
		t.Fatal("new group action was not available inside assignment")
	}
	tt.ClickAt(create.Bounds.X+create.Bounds.W/2, create.Bounds.Y+create.Bounds.H/2)
	tt.Type("从记录创建")
	tt.Key(0, ui.KeyEnter)
	if a.groupMode != "assign" || a.groupClipID != clipID || !a.groupDialogOpen {
		t.Fatal("creating from assignment did not return to the same clip")
	}
	ids, err := a.store.ClipGroupIDs(clipID)
	last := a.groups[len(a.groups)-1]
	if err != nil || last.Name != "从记录创建" || !slices.Contains(ids, last.ID) {
		t.Fatalf("new group did not retain the original clip: group=%v, ids=%v, error=%v", last, ids, err)
	}
}

func TestGroupMenusAndEditorClearAcrossSessions(t *testing.T) {
	a := groupTestApp(t)
	tt := ui.NewTester(a.view, 1200, panelHeight)
	tt.SetLocale(languageChinese)
	if err := tt.Click("更多分组与操作"); err != nil {
		t.Fatal(err)
	}
	if len(tt.Menu()) == 0 {
		t.Fatal("group menu did not open")
	}
	a.dismissGroupUI()
	a.searchSession++
	tt.Frame()
	if len(tt.Menu()) != 0 {
		t.Fatal("group menu survived a new panel session")
	}
	if err := tt.Click("新建分组"); err != nil {
		t.Fatal(err)
	}
	tt.Type("draft")
	r, found := tt.Find("分组名称")
	if !found {
		t.Fatal("group name input was not found")
	}
	tt.RightClickAt(r.X+5, r.Y+r.H/2)
	if len(tt.Menu()) == 0 {
		t.Fatal("group name editing menu did not open")
	}
	a.dismissGroupUI()
	a.searchSession++
	tt.Frame()
	tt.Advance(time.Second)
	if len(tt.Menu()) != 0 || a.groupName != "" {
		t.Fatal("group editor menu or draft survived hiding the panel")
	}
}
