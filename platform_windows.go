package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/draw"
	"image/png"
	"log"
	"strings"
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
	procOpenClipboard              = user32.NewProc("OpenClipboard")
	procCloseClipboard             = user32.NewProc("CloseClipboard")
	procEmptyClipboard             = user32.NewProc("EmptyClipboard")
	procSetClipboardData           = user32.NewProc("SetClipboardData")
	procRegisterClipboardFormatW   = user32.NewProc("RegisterClipboardFormatW")
	procMsgWaitForMultipleObjects  = user32.NewProc("MsgWaitForMultipleObjectsEx")
	procPeekMessageW               = user32.NewProc("PeekMessageW")
	procGetClassNameW              = user32.NewProc("GetClassNameW")
	procGetWindowThreadProcessId   = user32.NewProc("GetWindowThreadProcessId")
	kernel32                       = syscall.NewLazyDLL("kernel32.dll")
	procGlobalAlloc                = kernel32.NewProc("GlobalAlloc")
	procGlobalLock                 = kernel32.NewProc("GlobalLock")
	procGlobalUnlock               = kernel32.NewProc("GlobalUnlock")
	procGlobalFree                 = kernel32.NewProc("GlobalFree")
	procRtlMoveMemory              = kernel32.NewProc("RtlMoveMemory")
)

const (
	cfUnicodeText   = 13
	cfDIB           = 8
	gmemMoveable    = 0x0002
	qsSendMessage   = 0x0040
	pmNoRemove      = 0x0000
	pmQSSendMessage = qsSendMessage << 16
)

// writeImage puts a PNG on the clipboard as a bitmap (CF_DIB) and as PNG,
// at once, as screenshot tools do. MyGo offers a picture through OLE,
// rendered on demand, and some readers got nothing of it: Windows Forms,
// by which Claude Code reads one, among them.
func writeImage(data []byte) error { return writeImageText(data, "") }

// writeImageText puts a PNG on the clipboard as writeImage does, and text
// along with it unless it is empty.
func writeImageText(data []byte, text string) error {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return err
	}
	dib := imageDIB(img)
	name, _ := syscall.UTF16PtrFromString("PNG")
	pngFormat, _, _ := procRegisterClipboardFormatW.Call(uintptr(unsafe.Pointer(name)))
	if !openClipboard() {
		return errors.New("the clipboard is busy")
	}
	defer procCloseClipboard.Call()
	procEmptyClipboard.Call()
	if !setClipboardData(cfDIB, dib) {
		return errors.New("cannot put the picture on the clipboard")
	}
	if pngFormat != 0 {
		setClipboardData(pngFormat, data)
	}
	if text != "" {
		units, err := syscall.UTF16FromString(text)
		if err != nil {
			return err
		}
		b := make([]byte, 2*len(units))
		for i, u := range units {
			binary.LittleEndian.PutUint16(b[2*i:], u)
		}
		if !setClipboardData(cfUnicodeText, b) {
			return errors.New("cannot put the text on the clipboard")
		}
	}
	return nil
}

// terminalWindow reports whether a window is a terminal's: Windows
// Terminal's, the console's, mintty's (Git Bash), or any app's hosting a
// pseudo console, in which terminal apps run their shells.
func terminalWindow(h uintptr) bool {
	if h == 0 {
		return false
	}
	var name [64]uint16
	n, _, _ := procGetClassNameW.Call(h, uintptr(unsafe.Pointer(&name[0])), uintptr(len(name)))
	switch syscall.UTF16ToString(name[:n]) {
	case "CASCADIA_HOSTING_WINDOW_CLASS", "ConsoleWindowClass", "mintty":
		return true
	}
	var pid uint32
	procGetWindowThreadProcessId.Call(h, uintptr(unsafe.Pointer(&pid)))
	return pid != 0 && hostsConsole(pid)
}

// hostsConsole reports whether a process runs a console host (OpenConsole
// or conhost), itself or through a process of the same program, as an app
// of several processes does.
func hostsConsole(pid uint32) bool {
	snapshot, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(snapshot)
	type process struct {
		parent uint32
		exe    string
	}
	processes := map[uint32]process{}
	var e syscall.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = syscall.Process32First(snapshot, &e); err == nil; err = syscall.Process32Next(snapshot, &e) {
		processes[e.ProcessID] = process{e.ParentProcessID, strings.ToLower(syscall.UTF16ToString(e.ExeFile[:]))}
	}
	app := processes[pid].exe
	if app == "" {
		return false
	}
	for _, p := range processes {
		if p.exe != "openconsole.exe" && p.exe != "conhost.exe" {
			continue
		}
		if p.parent == pid {
			return true
		}
		if host, ok := processes[p.parent]; ok && host.parent == pid && host.exe == app {
			return true
		}
	}
	return false
}

// openClipboard opens the clipboard, waiting at most a second while
// another program has it open, as Windows' clipboard history does right
// after every change. That program may wait for this one to render what it
// offered before, so the wait handles the messages sent to it.
func openClipboard() bool {
	deadline := time.Now().Add(time.Second)
	for {
		if ok, _, _ := procOpenClipboard.Call(0); ok != 0 {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		procMsgWaitForMultipleObjects.Call(0, 0, 10, qsSendMessage, 0)
		var m [48]byte // MSG
		procPeekMessageW.Call(uintptr(unsafe.Pointer(&m[0])), 0, 0, 0, pmNoRemove|pmQSSendMessage)
	}
}

// setClipboardData puts data in a format on the open, emptied clipboard,
// which then owns its memory.
func setClipboardData(format uintptr, data []byte) bool {
	h, _, _ := procGlobalAlloc.Call(gmemMoveable, uintptr(len(data)))
	if h == 0 {
		return false
	}
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		procGlobalFree.Call(h)
		return false
	}
	procRtlMoveMemory.Call(p, uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)))
	procGlobalUnlock.Call(h)
	if r, _, _ := procSetClipboardData.Call(format, h); r == 0 {
		procGlobalFree.Call(h)
		return false
	}
	return true
}

// imageDIB lays a picture out as a bottom-up, 32-bit device independent
// bitmap, its alpha in the fourth byte.
func imageDIB(img image.Image) []byte {
	b := img.Bounds()
	nrgba, ok := img.(*image.NRGBA)
	if !ok || nrgba.Rect.Min != (image.Point{}) {
		nrgba = image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
		draw.Draw(nrgba, nrgba.Rect, img, b.Min, draw.Src)
	}
	w, h := nrgba.Rect.Dx(), nrgba.Rect.Dy()
	out := make([]byte, 40+4*w*h)
	le := binary.LittleEndian
	le.PutUint32(out, 40)
	le.PutUint32(out[4:], uint32(w))
	le.PutUint32(out[8:], uint32(h))
	le.PutUint16(out[12:], 1)
	le.PutUint16(out[14:], 32)
	le.PutUint32(out[20:], uint32(4*w*h))
	i := 40
	for y := h - 1; y >= 0; y-- {
		row := nrgba.Pix[y*nrgba.Stride : y*nrgba.Stride+4*w]
		for x := 0; x < len(row); x += 4 {
			out[i], out[i+1], out[i+2], out[i+3] = row[x+2], row[x+1], row[x], row[x+3]
			i += 4
		}
	}
	return out
}

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
