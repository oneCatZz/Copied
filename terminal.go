package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// imageFileAge is how long a picture saved for a terminal is kept.
const imageFileAge = 24 * time.Hour

// copyForTerminal puts a picture chosen to paste into a terminal on the
// clipboard together with the path of a file holding it. A terminal pastes
// only text, and Claude Code and Codex take the path of a picture pasted
// into them for the picture. It reports false for anything else, to be
// copied as usual.
func (a *app) copyForTerminal(i int, target uintptr) bool {
	if i < 0 || i >= len(a.clips) || a.clips[i].Kind != KindImage || a.batchIDs(i) != nil || !terminalWindow(target) {
		return false
	}
	id := a.clips[i].ID
	clip, err := a.store.Get(id)
	if err != nil || clip == nil {
		log.Print("reading clip: ", err)
		return false
	}
	path, err := saveImageFile(id, clip.Data)
	if err != nil {
		log.Print("saving a picture for a terminal: ", err)
		return false
	}
	if strings.ContainsRune(path, ' ') {
		path = `"` + path + `"`
	}
	a.ownUntil.Store(time.Now().Add(ownWrite).UnixMilli())
	if err := writeImageText(clip.Data, path); err != nil {
		log.Print("writing the clipboard: ", err)
		return false
	}
	if err := a.store.Touch(id); err != nil {
		log.Print(err)
	}
	return true
}

// saveImageFile writes a picture to the temporary directory, where it is
// kept a day, and returns its path.
func saveImageFile(id int64, data []byte) (string, error) {
	dir := filepath.Join(os.TempDir(), "Copied")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > imageFileAge {
				os.Remove(filepath.Join(dir, e.Name()))
			}
		}
	}
	path := filepath.Join(dir, fmt.Sprintf("clip-%d.png", id))
	return path, os.WriteFile(path, data, 0o600)
}
