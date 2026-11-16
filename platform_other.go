//go:build !windows && !darwin

package main

import "github.com/egoist/mygo"

// clipboardSeq is unknown here: the watcher compares the clipboard's
// content instead.
func clipboardSeq() (seq int64, ok bool) { return 0, false }

func frontWindow() uintptr { return 0 }

func writeImage(data []byte) error { return mygo.Clipboard.WriteImage(data) }

// pasteInto does nothing here: the clip is on the clipboard, to paste by
// hand.
func pasteInto(uintptr) {}
