package main

import (
	"log"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type shortcutRegistry interface {
	Register(string, func()) error
	Unregister(string)
	IsRegistered(string) bool
}

func (a *app) globalShortcuts() shortcutRegistry {
	if a.shortcuts != nil {
		return a.shortcuts
	}
	return mygo.GlobalShortcut
}

// resumeShortcuts restores both bindings after either recorder releases
// them. The stack binding still exists only while the stack has items.
func (a *app) resumeShortcuts() {
	if a.recording {
		return
	}
	keys := a.globalShortcuts()
	if a.shortcut != "" && !keys.IsRegistered(a.shortcut) {
		if err := keys.Register(a.shortcut, a.toggle); err != nil {
			log.Printf("shortcut %s: %v", a.shortcut, err)
			a.recordError = "shortcutUnavailable"
		}
	}
	a.syncStackShortcut()
}

func (a *app) shortcutConflict(shortcut string, stack bool) string {
	other := a.stackShortcut()
	if stack {
		other = a.shortcut
		// The synthetic paste must not trigger the stack hotkey itself.
		if sameShortcut(shortcut, "CmdOrCtrl+V") {
			return "stackShortcutReserved"
		}
	}
	if other != "" && sameShortcut(shortcut, other) {
		return "shortcutConflict"
	}
	if sameShortcut(shortcut, a.sourceShortcut()) {
		return "sourceShortcutConflict"
	}
	return ""
}

// changeShortcut validates and acquires the new binding before releasing
// the old one. Probe even with an empty stack, then release it immediately.
// On failure both the saved choice and the old registration stay intact.
func (a *app) changeShortcut(shortcut string, stack bool) bool {
	if conflict := a.shortcutConflict(shortcut, stack); conflict != "" {
		a.recordError = conflict
		return false
	}
	keys := a.globalShortcuts()
	old, callback := a.shortcut, a.toggle
	if stack {
		old, callback = a.stackKeys, a.pasteNext
	}
	if old == "" || !sameShortcut(shortcut, old) || !keys.IsRegistered(old) {
		if err := keys.Register(shortcut, callback); err != nil {
			log.Printf("shortcut %s: %v", shortcut, err)
			a.recordError = "shortcutUnavailable"
			return false
		}
	}
	if old != "" && !sameShortcut(old, shortcut) {
		keys.Unregister(old)
	}
	if stack {
		a.settings.StackShortcut = shortcut
		a.stackKeys = shortcut
		if len(a.stack) == 0 || a.recording {
			keys.Unregister(shortcut)
			a.stackKeys = ""
		}
	} else {
		a.shortcut, a.settings.Shortcut = shortcut, shortcut
	}
	a.recordError = ""
	return true
}

func (a *app) resetStackShortcut() {
	if a.recording {
		a.stopRecording("")
	}
	if a.changeShortcut(defaultStackShortcut, true) {
		a.settings.StackShortcut = ""
		a.saveSettings()
	}
}

const defaultSourceShortcut = "CmdOrCtrl+Shift+A"

func (a *app) sourceShortcut() string {
	if shortcut := a.settings.SourceShortcut; shortcut != "" {
		if _, _, valid := shortcutKeys(shortcut); valid {
			return shortcut
		}
	}
	return defaultSourceShortcut
}

func (a *app) isSourceShortcut(mods ui.Modifiers, key ui.Key) bool {
	return sameShortcut(accelerator(mods, key), a.sourceShortcut())
}

// App selection is window-local; recording it must not register a global key.
func (a *app) changeSourceShortcut(shortcut string) bool {
	if _, _, valid := shortcutKeys(shortcut); !valid {
		a.recordError = "sourceShortcutReserved"
		return false
	}
	panel := a.shortcut
	if panel == "" {
		panel = a.settings.Shortcut
	}
	if panel == "" {
		panel = defaultShortcuts[0]
	}
	if sameShortcut(shortcut, panel) || sameShortcut(shortcut, a.stackShortcut()) {
		a.recordError = "sourceShortcutConflict"
		return false
	}
	reserved := []string{
		"CmdOrCtrl+A", "CmdOrCtrl+C", "CmdOrCtrl+X", "CmdOrCtrl+V", "CmdOrCtrl+Z", "CmdOrCtrl+Shift+Z", "CmdOrCtrl+Y",
		"CmdOrCtrl+Enter", "CmdOrCtrl+Backspace", "CmdOrCtrl+P", "CmdOrCtrl+S", "CmdOrCtrl+,", "CmdOrCtrl+W", "Alt+F4",
	}
	for _, key := range digitKeys {
		reserved = append(reserved, accelerator(ui.Cmd, key))
	}
	for _, command := range reserved {
		if sameShortcut(shortcut, command) {
			a.recordError = "sourceShortcutReserved"
			return false
		}
	}
	a.settings.SourceShortcut, a.recordError = shortcut, ""
	return true
}

func (a *app) resetSourceShortcut() {
	if a.recording {
		a.stopRecording("")
	}
	if a.changeSourceShortcut(defaultSourceShortcut) {
		a.settings.SourceShortcut = ""
		a.saveSettings()
	}
}
