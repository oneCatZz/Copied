//go:build !windows && !darwin

package main

// clipboardSeq is unknown here: the watcher compares the clipboard's
// content instead.
func clipboardSeq() (seq int64, ok bool) { return 0, false }

func frontWindow() uintptr { return 0 }

// pasteInto does nothing here: the clip is on the clipboard, to paste by
// hand.
func pasteInto(uintptr) {}

