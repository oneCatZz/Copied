//go:build !windows && !darwin

package main

import "github.com/egoist/mygo"

// clipboardSeq is unknown here: the watcher compares the clipboard's
// content instead.
func clipboardSeq() (seq int64, ok bool) { return 0, false }

func frontWindow() uintptr { return 0 }

func writeImage(data []byte) error { return mygo.Clipboard.WriteImage(data) }

// terminalWindow is never known here: a picture is pasted as one.
func terminalWindow(uintptr) bool { return false }

func writeImageText(data []byte, _ string) error { return writeImage(data) }

// pasteInto does nothing here: the clip is on the clipboard, to paste by
// hand.
func pasteInto(uintptr) {}
