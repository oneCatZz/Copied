package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// Settings are what the user chose, kept in settings.json of the user's
// data directory.
type Settings struct {
	// Shortcut shows and hides the panel, as an accelerator. Empty, the
	// first of defaultShortcuts that is free does.
	Shortcut string `json:"shortcut,omitempty"`
}

func settingsPath() (string, error) {
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "settings.json"), nil
}

func loadSettings() Settings {
	var s Settings
	path, err := settingsPath()
	if err != nil {
		return s
	}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &s)
	}
	return s
}

func (s Settings) save() error {
	path, err := settingsPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// keyNames are the keys a shortcut can end with, as accelerators name them.
var keyNames = func() map[ui.Key]string {
	names := map[ui.Key]string{
		ui.KeySpace: "Space", ui.KeyEnter: "Enter", ui.KeyTab: "Tab",
		ui.KeyBackspace: "Backspace", ui.KeyDelete: "Delete", ui.KeyInsert: "Insert",
		ui.KeyUp: "Up", ui.KeyDown: "Down", ui.KeyLeft: "Left", ui.KeyRight: "Right",
		ui.KeyHome: "Home", ui.KeyEnd: "End", ui.KeyPageUp: "PageUp", ui.KeyPageDown: "PageDown",
		ui.KeyMinus: "-", ui.KeyEqual: "=", ui.KeyComma: ",", ui.KeyPeriod: ".",
		ui.KeySlash: "/", ui.KeySemicolon: ";", ui.KeyQuote: "'",
		ui.KeyBracketLeft: "[", ui.KeyBracketRight: "]", ui.KeyBackslash: `\`, ui.KeyBackquote: "`",
	}
	for i := 0; i < 26; i++ {
		names[ui.KeyA+ui.Key(i)] = string(rune('A' + i))
	}
	for i := 0; i < 10; i++ {
		names[ui.Key0+ui.Key(i)] = string(rune('0' + i))
	}
	for i := 0; i < 12; i++ {
		names[ui.KeyF1+ui.Key(i)] = "F" + string(rune('1'+i))
		if i >= 9 {
			names[ui.KeyF1+ui.Key(i)] = "F1" + string(rune('0'+i-9))
		}
	}
	return names
}()

// accelerator spells the keys of a key press as a global shortcut. It is
// empty for keys that make none: a modifier alone, or a key that types
// when pressed alone or with Shift.
func accelerator(mods ui.Modifiers, key ui.Key) string {
	name, ok := keyNames[key]
	if !ok {
		return ""
	}
	function := strings.HasPrefix(name, "F") && len(name) > 1
	if mods&(ui.Ctrl|ui.Alt|ui.Super) == 0 && !function {
		return ""
	}
	return strings.Join(append(modifierNames(mods), name), "+")
}

// modifierNames are the modifiers held, as accelerators name them.
func modifierNames(mods ui.Modifiers) []string {
	var parts []string
	if mods&ui.Cmd != 0 {
		parts = append(parts, "CmdOrCtrl")
	}
	if other := (ui.Ctrl | ui.Super) &^ ui.Cmd; mods&other != 0 {
		if other == ui.Ctrl {
			parts = append(parts, "Ctrl") // Control on macOS
		} else {
			parts = append(parts, "Super") // the Windows key
		}
	}
	if mods&ui.Alt != 0 {
		parts = append(parts, "Alt")
	}
	if mods&ui.Shift != 0 {
		parts = append(parts, "Shift")
	}
	return parts
}

// keycaps are the keys of an accelerator, as the platform writes them.
func keycaps(accelerator string) []string {
	if accelerator == "" {
		return nil
	}
	mac := runtime.GOOS == "darwin"
	var caps []string
	for _, part := range strings.Split(accelerator, "+") {
		switch part {
		case "CmdOrCtrl":
			part = "Ctrl"
			if mac {
				part = "⌘"
			}
		case "Ctrl":
			if mac {
				part = "⌃"
			}
		case "Super":
			part = "Win"
		case "Alt":
			if mac {
				part = "⌥"
			}
		case "Shift":
			if mac {
				part = "⇧"
			}
		case "":
			part = "+" // the key after the last +
		}
		caps = append(caps, part)
	}
	return caps
}
