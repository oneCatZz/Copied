package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"copied/locales"

	"github.com/egoist/mygo/ui"
)

type fakeShortcuts struct {
	bindings map[string]func()
	busy     map[string]bool
}

func newFakeShortcuts() *fakeShortcuts {
	return &fakeShortcuts{bindings: map[string]func(){}, busy: map[string]bool{}}
}

func (f *fakeShortcuts) Register(key string, fn func()) error {
	k := shortcutSignature(key)
	if f.bindings[k] != nil || f.busy[k] {
		return errors.New("shortcut occupied")
	}
	f.bindings[k] = fn
	return nil
}

func (f *fakeShortcuts) Unregister(key string) { delete(f.bindings, shortcutSignature(key)) }
func (f *fakeShortcuts) IsRegistered(key string) bool {
	return f.bindings[shortcutSignature(key)] != nil
}

func shortcutTestApp() (*app, *fakeShortcuts) {
	keys := newFakeShortcuts()
	a := &app{shortcuts: keys, shortcut: defaultShortcuts[0]}
	a.resumeShortcuts()
	return a, keys
}

func TestStackShortcutRebindsAndRecordsWithoutStealingPanelKeys(t *testing.T) {
	a, keys := shortcutTestApp()
	if keys.IsRegistered(a.stackShortcut()) {
		t.Fatal("empty stack registered a hotkey")
	}
	a.stack = []int64{1}
	a.syncStackShortcut()
	custom := "CmdOrCtrl+Alt+X"
	if !a.changeShortcut(custom, true) || !keys.IsRegistered(custom) || keys.IsRegistered(defaultStackShortcut) || !keys.IsRegistered(a.shortcut) {
		t.Fatal("changing a live stack did not replace just its binding")
	}
	for _, stack := range []bool{true, false, true} {
		a.startRecording(stack)
		if !a.recording || a.recordingStack != stack || keys.IsRegistered(custom) || keys.IsRegistered(a.shortcut) {
			t.Fatal("recorder did not suspend both hotkeys")
		}
		a.stopRecording("")
		if a.recording || !keys.IsRegistered(custom) || !keys.IsRegistered(a.shortcut) {
			t.Fatal("cancelling did not restore both hotkeys")
		}
	}
	a.clearStack()
	if keys.IsRegistered(custom) || !keys.IsRegistered(a.shortcut) {
		t.Fatal("clearing the stack released the panel key or retained the stack key")
	}
	if !a.changeShortcut("CmdOrCtrl+Alt+Z", true) || keys.IsRegistered(a.stackShortcut()) {
		t.Fatal("editing an empty stack must probe and release the new hotkey")
	}
}

func TestStackShortcutConflictsPreserveCurrentBindings(t *testing.T) {
	a, keys := shortcutTestApp()
	a.stack = []int64{1}
	a.syncStackShortcut()
	before := a.settings
	busy := "CmdOrCtrl+Alt+X"
	keys.busy[shortcutSignature(busy)] = true
	for _, tc := range []struct {
		shortcut string
		stack    bool
		errorKey string
	}{
		{a.shortcut, true, "shortcutConflict"},
		{defaultStackShortcut, false, "shortcutConflict"},
		{"CmdOrCtrl+V", true, "stackShortcutReserved"},
		{busy, true, "shortcutUnavailable"},
	} {
		if a.changeShortcut(tc.shortcut, tc.stack) || a.recordError != tc.errorKey || a.settings != before || !keys.IsRegistered(a.shortcut) || !keys.IsRegistered(defaultStackShortcut) {
			t.Fatalf("failed shortcut change corrupted configuration: %+v, %q", tc, a.recordError)
		}
	}
	a.clearStack()
	if a.changeShortcut(a.shortcut, true) || a.recordError != "shortcutConflict" {
		t.Fatal("panel conflict was allowed with an empty stack")
	}
	if a.changeShortcut(busy, true) || a.settings != before {
		t.Fatal("occupied shortcut was saved with an empty stack")
	}
}

func TestPanelStartupAvoidsConfiguredStackShortcut(t *testing.T) {
	a, keys := shortcutTestApp()
	keys.Unregister(a.shortcut)
	a.settings.StackShortcut = defaultShortcuts[0]
	a.registerShortcut()
	if a.shortcut != defaultShortcuts[1] || !keys.IsRegistered(a.shortcut) || keys.IsRegistered(a.stackShortcut()) {
		t.Fatal("startup claimed the stack's configured key for the panel")
	}
}

func TestShortcutSettingsPersistAndMigrate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"shortcut":"Alt+Shift+V","language":"zh-Hans"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	legacy := readSettings(path)
	a := &app{settings: legacy}
	if a.stackShortcut() != defaultStackShortcut || legacy.StackShortcut != "" {
		t.Fatal("old settings did not retain the default stack shortcut")
	}
	legacy.StackShortcut = "CmdOrCtrl+Alt+X"
	if err := legacy.saveAt(path); err != nil {
		t.Fatal(err)
	}
	if got := readSettings(path); got != legacy {
		t.Fatalf("saved shortcuts and language did not survive reload: %+v", got)
	}
}

func TestShortcutAliasesMatchNativeModifiers(t *testing.T) {
	command := "Control"
	if runtime.GOOS == "darwin" {
		command = "Command"
	}
	if !sameShortcut("CmdOrCtrl+Alt+V", " Option + "+command+" + v ") || sameShortcut("CmdOrCtrl+V", "CmdOrCtrl+Shift+V") || !sameShortcut("Ctrl++", "Control+Plus") {
		t.Fatal("shortcut comparison lost native aliases or extra modifiers")
	}
}

func TestStackShortcutSettingsLayout(t *testing.T) {
	for _, width := range []int{560, 1200} {
		for _, tag := range []string{languageEnglish, languageChinese} {
			for _, dark := range []bool{false, true} {
				t.Run(fmt.Sprintf("%d-%s-dark-%v", width, tag, dark), func(t *testing.T) {
					a, _ := shortcutTestApp()
					a.settingsOpen, a.recordError = true, "stackShortcutReserved"
					a.settings.StackShortcut = "CmdOrCtrl+Alt+X"
					tt := ui.NewTester(a.settingsDialog, width, panelHeight)
					tt.SetLocale(tag)
					tt.SetDark(dark)
					saveUIArtifact(t, tt, fmt.Sprintf("stack-settings-%d-%s-dark-%v", width, tag, dark))
					tx := locales.In(tag)
					for _, label := range []string{tx.PanelShortcut(), locales.Catalog.Format(tag, "stackShortcut", nil), locales.Catalog.Format(tag, "resetStackShortcut", nil), tx.Done()} {
						r, ok := tt.Find(label)
						if !ok || r.W <= 0 || r.X < 0 || r.Y < 0 || r.X+r.W > float32(width)+1 || r.Y+r.H > panelHeight {
							t.Fatalf("settings control %q is missing or clipped: %v", label, r)
						}
					}
					if !tt.HasText("X") || !tt.HasText(locales.Catalog.Format(tag, "stackShortcutReserved", nil)) {
						t.Fatal("custom shortcut or localized error was not displayed")
					}
				})
			}
		}
	}
}

func TestStackShortcutRecorderCancelAndConflict(t *testing.T) {
	a, keys := shortcutTestApp()
	a.stack = []int64{1}
	a.syncStackShortcut()
	a.settingsOpen = true
	tt := ui.NewTester(a.settingsDialog, 1200, panelHeight)
	tt.SetLocale(languageChinese)
	if err := tt.Click("Stack 粘贴快捷键"); err != nil {
		t.Fatal(err)
	}
	if !a.recording || !a.recordingStack {
		t.Fatal("clicking the stack recorder edited the panel shortcut")
	}
	tt.Key(ui.Cmd, ui.KeyV)
	if a.recording || a.recordError != "stackShortcutReserved" || !keys.IsRegistered(defaultStackShortcut) || !keys.IsRegistered(a.shortcut) {
		t.Fatal("reserved paste key did not show an error and restore both bindings")
	}
	if err := tt.Click("Stack 粘贴快捷键"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyEscape)
	if a.recording || !a.settingsOpen || a.settings.StackShortcut != "" || !keys.IsRegistered(defaultStackShortcut) {
		t.Fatal("Escape changed the saved shortcut, closed settings or lost the binding")
	}
	tt.Frame()
	if err := tt.Click("完成"); err != nil {
		t.Fatal(err)
	}
	a.settingsOpen = true
	tt.Frame()
	if !tt.HasText("Stack 粘贴快捷键") {
		t.Fatal("reopening settings lost the stack recorder")
	}
}
