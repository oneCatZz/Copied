package main

import (
	"syscall"
	"time"
	"unsafe"
)

var (
	user32                         = syscall.NewLazyDLL("user32.dll")
	procGetClipboardSequenceNumber = user32.NewProc("GetClipboardSequenceNumber")
	procGetForegroundWindow        = user32.NewProc("GetForegroundWindow")
	procSetForegroundWindow        = user32.NewProc("SetForegroundWindow")
	procIsWindow                   = user32.NewProc("IsWindow")
	procSendInput                  = user32.NewProc("SendInput")
)

// clipboardSeq returns a number that changes with the clipboard's content.
func clipboardSeq() (seq int64, ok bool) {
	n, _, _ := procGetClipboardSequenceNumber.Call()
	return int64(n), true
}

// frontWindow returns the window the user is in, to give the keyboard back
// to it before pasting.
func frontWindow() uintptr {
	h, _, _ := procGetForegroundWindow.Call()
	return h
}

const (
	inputKeyboard  = 1
	keyEventFKeyUp = 0x0002
	vkControl      = 0x11
	vkV            = 0x56
)

// keyboardInput is INPUT with its KEYBDINPUT, as laid out on 64-bit Windows.
type keyboardInput struct {
	typ   uint32
	_     uint32
	vk    uint16
	scan  uint16
	flags uint32
	time  uint32
	_     uint32
	extra uintptr
	_     [8]byte
}

// pasteInto gives the keyboard to the window the panel was shown over and
// presses Ctrl+V there.
func pasteInto(target uintptr) {
	if target != 0 {
		if alive, _, _ := procIsWindow.Call(target); alive != 0 {
			procSetForegroundWindow.Call(target)
		}
	}
	time.Sleep(80 * time.Millisecond) // until the window has the keyboard
	inputs := []keyboardInput{
		{typ: inputKeyboard, vk: vkControl},
		{typ: inputKeyboard, vk: vkV},
		{typ: inputKeyboard, vk: vkV, flags: keyEventFKeyUp},
		{typ: inputKeyboard, vk: vkControl, flags: keyEventFKeyUp},
	}
	procSendInput.Call(uintptr(len(inputs)), uintptr(unsafe.Pointer(&inputs[0])), unsafe.Sizeof(inputs[0]))
}
