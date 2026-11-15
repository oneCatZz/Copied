package main

import (
	"log"
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
	procGetAsyncKeyState           = user32.NewProc("GetAsyncKeyState")
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
	inputKeyboard    = 1
	keyEventFKeyUp   = 0x0002
	keyEventExtended = 0x0001
	vkControl        = 0x11
	vkV              = 0x56
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

// Use the physical left/right keys, rather than also releasing their
// aggregate VK_CONTROL/SHIFT/MENU aliases. Right Ctrl/Alt and Win keys
// require KEYEVENTF_EXTENDEDKEY when synthesized.
var pasteModifiers = [...]uint16{0xA0, 0xA1, 0xA2, 0xA3, 0xA4, 0xA5, 0x5B, 0x5C}

func pasteKey(vk uint16, up bool) keyboardInput {
	in := keyboardInput{typ: inputKeyboard, vk: vk}
	if vk == 0xA3 || vk == 0xA5 || vk == 0x5B || vk == 0x5C {
		in.flags |= keyEventExtended
	}
	if up {
		in.flags |= keyEventFKeyUp
	}
	return in
}

// SendInput inherits the keys already down. Temporarily release them in
// the same atomic input batch as Ctrl+V, then restore only the modifiers.
// Restoring V too would invoke the stack hotkey again. Unlike waiting for
// key-up, this also works while Ctrl+Alt is held for consecutive stack pastes.
func pasteInputs(down func(uint16) bool) []keyboardInput {
	var held []uint16
	var inputs []keyboardInput
	if down(vkV) {
		inputs = append(inputs, pasteKey(vkV, true))
	}
	for _, vk := range pasteModifiers {
		if down(vk) {
			held = append(held, vk)
			inputs = append(inputs, pasteKey(vk, true))
		}
	}
	inputs = append(inputs, pasteKey(vkControl, false), pasteKey(vkV, false), pasteKey(vkV, true), pasteKey(vkControl, true))
	for _, vk := range held {
		inputs = append(inputs, pasteKey(vk, false))
	}
	return inputs
}

// pasteInto gives the keyboard to the window the panel was shown over and
// presses Ctrl+V there.
func pasteInto(target uintptr) {
	if target != 0 {
		if alive, _, _ := procIsWindow.Call(target); alive == 0 {
			log.Print("paste: target window no longer exists")
			return
		}
		procSetForegroundWindow.Call(target)
	}
	time.Sleep(80 * time.Millisecond) // until the window has the keyboard
	if target != 0 && frontWindow() != target {
		log.Print("paste: target window did not receive focus")
		return
	}
	inputs := pasteInputs(func(vk uint16) bool {
		state, _, _ := procGetAsyncKeyState.Call(uintptr(vk))
		return state&0x8000 != 0
	})
	if n, _, err := procSendInput.Call(uintptr(len(inputs)), uintptr(unsafe.Pointer(&inputs[0])), unsafe.Sizeof(inputs[0])); n != uintptr(len(inputs)) {
		log.Printf("paste: SendInput sent %d/%d events: %v", n, len(inputs), err)
	}
}
