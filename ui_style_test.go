package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func searchTester(t *testing.T, dark bool) (*app, *ui.Tester) {
	t.Helper()
	a := &app{store: testStore(t), thumbs: map[int64]*thumb{}, shortcut: defaultShortcuts[0]}
	tt := ui.NewTesterWith(func(c *ui.Context) {
		theme := lightTheme
		if c.Theme().Dark {
			theme = darkTheme
		}
		ui.Themed(c, theme).Fill().Children(func() {
			a.header(c)
			ui.Spacer(c)
			a.settingsDialog(c)
		})
	}, 1200, panelHeight, ui.TesterOptions{Dark: dark})
	tt.SetLocale(languageChinese)
	return a, tt
}

func openSearchMenu(t *testing.T, tt *ui.Tester) {
	t.Helper()
	r, ok := tt.Find("搜索")
	if !ok {
		t.Fatal("search input not found")
	}
	// Inside the selected text, so right-clicking preserves the selection.
	tt.RightClickAt(r.X+3, r.Y+r.H/2)
	if !tt.HasText("复制") || len(tt.Menu()) == 0 {
		t.Fatal("search editing menu was not drawn in the window")
	}
}

func TestSearchMenuPreservesEditingCommands(t *testing.T) {
	a, tt := searchTester(t, false)
	if err := tt.Click("搜索"); err != nil {
		t.Fatal(err)
	}
	const original = "hello 世界"
	tt.Type(original)
	tt.Key(ui.Cmd, ui.KeyA)
	openSearchMenu(t, tt)
	saveUIArtifact(t, tt, "search-menu-light")
	if err := tt.ChooseMenuItem("复制"); err != nil {
		t.Fatal(err)
	}
	if tt.Clipboard() != original || a.query != original {
		t.Fatal("copy changed the query or lost its selection")
	}
	openSearchMenu(t, tt)
	if err := tt.ChooseMenuItem("剪切"); err != nil {
		t.Fatal(err)
	}
	if a.query != "" || tt.Clipboard() != original {
		t.Fatal("cut did not use the input's selected text")
	}
	// macOS's editing menu omits Undo; the shortcut is supported on both
	// platforms and still verifies that a menu cut preserves undo history.
	tt.Key(ui.Cmd, ui.KeyZ)
	if a.query != original {
		t.Fatal("editing through the menu lost the undo history")
	}
	tt.SetClipboard("替换内容")
	tt.Key(ui.Cmd, ui.KeyA)
	openSearchMenu(t, tt)
	if err := tt.ChooseMenuItem("粘贴"); err != nil {
		t.Fatal(err)
	}
	if a.query != "替换内容" {
		t.Fatal("paste did not replace the selection")
	}
	tt.Key(ui.Cmd, ui.KeyZ)
	if a.query != original {
		t.Fatal("keyboard undo after menu paste did not restore the query")
	}
}

func TestSearchSessionDismissesEditingMenu(t *testing.T) {
	a, tt := searchTester(t, true)
	openSearchMenu(t, tt)
	saveUIArtifact(t, tt, "search-menu-dark")
	a.searchSession++
	a.query = ""
	a.input.Focus()
	tt.Frame()
	tt.Advance(time.Second)
	if len(tt.Menu()) != 0 || tt.HasText("复制") || !tt.Focused("搜索") {
		t.Fatal("new search session retained its editing menu or lost focus")
	}
	openSearchMenu(t, tt)
	tt.Key(0, ui.KeyEscape)
	if len(tt.Menu()) != 0 {
		t.Fatal("Escape did not dismiss the reopened search menu")
	}
}

func TestSettingsSurfaceDismissalAndErrors(t *testing.T) {
	for _, dark := range []bool{false, true} {
		a, tt := searchTester(t, dark)
		a.settingsOpen, a.recordError = true, "shortcutUnavailable"
		tt.Frame()
		name := "settings-light"
		if dark {
			name = "settings-dark"
		}
		saveUIArtifact(t, tt, name)
		if err := tt.Click("完成"); err != nil {
			t.Fatal(err)
		}
		if a.settingsOpen || a.recordError != "" || tt.HasText("恢复默认") {
			t.Fatal("finishing settings retained the dialog or its error")
		}
		a.settingsOpen = true
		tt.Frame()
		tt.Key(0, ui.KeyEscape)
		if a.settingsOpen || tt.HasText("恢复默认") {
			t.Fatal("Escape did not dismiss settings")
		}
	}
}

func TestSettingsTooltipDismissesAfterPointerLeaves(t *testing.T) {
	a, tt := searchTester(t, false)
	r, ok := tt.Find("设置")
	if !ok {
		t.Fatal("settings control not found")
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Advance(700 * time.Millisecond)
	if _, shown := tt.AccessByRole(ui.RoleTooltip, ""); !shown {
		t.Fatal("settings tooltip did not appear")
	}
	saveUIArtifact(t, tt, "settings-tooltip")
	a.searchSession++
	tt.Move(20, panelHeight-20)
	tt.Frame()
	tt.Advance(100 * time.Millisecond)
	if _, shown := tt.AccessByRole(ui.RoleTooltip, ""); shown {
		t.Fatal("settings tooltip remained after leaving the control")
	}
}
