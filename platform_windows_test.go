package main

import (
	"os"
	"runtime"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

func TestWindowsPasteIsolatesShortcutModifiers(t *testing.T) {
	for name, keys := range map[string][]uint16{
		"plain":           nil,
		"stack-held":      {0xA2, 0xA4, vkV},
		"stack-right-alt": {0xA3, 0xA5, vkV},
		"batch-shift":     {0xA0},
		"batch-control":   {0xA2},
		"all-modifiers":   pasteModifiers[:],
	} {
		t.Run(name, func(t *testing.T) {
			state := map[uint16]bool{}
			for _, vk := range keys {
				state[vk] = true
			}
			inputs := pasteInputs(func(vk uint16) bool { return state[vk] })
			pastes := 0
			for _, in := range inputs {
				down := in.flags&keyEventFKeyUp == 0
				if down && in.vk == vkV {
					pastes++
					if !state[vkControl] {
						t.Fatal("V was sent without Ctrl")
					}
					for _, vk := range pasteModifiers {
						if state[vk] {
							t.Fatalf("held modifier %#x leaked into Ctrl+V", vk)
						}
					}
				}
				state[in.vk] = down
			}
			if pastes != 1 || state[vkV] || state[vkControl] {
				t.Fatalf("paste was repeated or left injected keys down: %d, %v", pastes, state)
			}
			for _, vk := range keys {
				if vk != vkV && !state[vk] {
					t.Fatalf("user modifier %#x was not restored", vk)
				}
			}
		})
	}
}

func TestWindowsPasteInputLayout(t *testing.T) {
	var in keyboardInput
	if unsafe.Sizeof(in) != 40 || unsafe.Offsetof(in.vk) != 8 || unsafe.Offsetof(in.extra) != 24 {
		t.Fatal("keyboard INPUT does not match the 64-bit Windows ABI")
	}
	for _, vk := range []uint16{0xA3, 0xA5, 0x5B, 0x5C} {
		if pasteKey(vk, false).flags&keyEventExtended == 0 || pasteKey(vk, true).flags != keyEventExtended|keyEventFKeyUp {
			t.Fatalf("extended key %#x is not encoded correctly", vk)
		}
	}
}

// An actual Win32 EDIT control reads the clipboard in response to the
// injected Ctrl+V. This exercises SendInput, focus and held keys together;
// the UI tester alone cannot check another window receiving a paste.
func TestWindowsPasteNative(t *testing.T) {
	if os.Getenv("COPIED_NATIVE_TEST") != "1" {
		t.Skip("set COPIED_NATIVE_TEST=1 in a Windows desktop session")
	}
	create := user32.NewProc("CreateWindowExW")
	destroy := user32.NewProc("DestroyWindow")
	peek := user32.NewProc("PeekMessageW")
	translate := user32.NewProc("TranslateMessage")
	dispatch := user32.NewProc("DispatchMessageW")
	sendMessage := user32.NewProc("SendMessageW")
	class, _ := syscall.UTF16PtrFromString("EDIT")
	ready := make(chan uintptr, 1)
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(done)
		hwnd, _, _ := create.Call(0, uintptr(unsafe.Pointer(class)), 0, 0x10CF1004, 100, 100, 500, 300, 0, 0, 0, 0)
		if hwnd == 0 {
			ready <- 0
			return
		}
		defer destroy.Call(hwnd)
		procSetForegroundWindow.Call(hwnd)
		user32.NewProc("SetFocus").Call(hwnd)
		ready <- hwnd
		var msg struct {
			hwnd           uintptr
			message        uint32
			wParam, lParam uintptr
			time           uint32
			x, y           int32
			private        uint32
		}
		for {
			select {
			case <-stop:
				return
			default:
			}
			for {
				n, _, _ := peek.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0, 1)
				if n == 0 {
					break
				}
				translate.Call(uintptr(unsafe.Pointer(&msg)))
				dispatch.Call(uintptr(unsafe.Pointer(&msg)))
			}
			time.Sleep(time.Millisecond)
		}
	}()
	hwnd := <-ready
	if hwnd == 0 {
		t.Fatal("could not create native paste receiver")
	}
	defer func() { close(stop); <-done }()
	send := func(t *testing.T, inputs []keyboardInput) {
		t.Helper()
		if n, _, err := procSendInput.Call(uintptr(len(inputs)), uintptr(unsafe.Pointer(&inputs[0])), unsafe.Sizeof(inputs[0])); n != uintptr(len(inputs)) {
			t.Fatalf("SendInput: %d/%d: %v", n, len(inputs), err)
		}
	}
	for _, tc := range []struct {
		name, text string
		keys       []uint16
	}{
		{"single", "single clip", nil},
		{"stack", "stack next clip", []uint16{0xA2, 0xA4, vkV}},
		{"batch", "first clip\r\nsecond clip", []uint16{0xA0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			empty, _ := syscall.UTF16PtrFromString("")
			sendMessage.Call(hwnd, 0x000C, 0, uintptr(unsafe.Pointer(empty))) // WM_SETTEXT
			setNativePasteText(t, hwnd, tc.text)
			var pressed, released []keyboardInput
			for _, vk := range tc.keys {
				pressed = append(pressed, pasteKey(vk, false))
				released = append(released, pasteKey(vk, true))
			}
			if len(pressed) > 0 {
				send(t, pressed)
				defer send(t, released)
			}
			pasteInto(hwnd) // keys stay down until after the paste is received
			var got string
			for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
				var buf [256]uint16
				user32.NewProc("GetWindowTextW").Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
				got = syscall.UTF16ToString(buf[:])
				if got == tc.text {
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatalf("native control received %q, want %q", got, tc.text)
		})
	}
}

func setNativePasteText(t *testing.T, hwnd uintptr, text string) {
	t.Helper()
	kernel := syscall.NewLazyDLL("kernel32.dll")
	u, err := syscall.UTF16FromString(text)
	if err != nil {
		t.Fatal(err)
	}
	opened := false
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		if ok, _, _ := user32.NewProc("OpenClipboard").Call(hwnd); ok != 0 {
			opened = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !opened {
		t.Fatal("clipboard stayed busy")
	}
	defer user32.NewProc("CloseClipboard").Call()
	user32.NewProc("EmptyClipboard").Call()
	h, _, _ := kernel.NewProc("GlobalAlloc").Call(2, uintptr(2*len(u)))
	if h == 0 {
		t.Fatal("GlobalAlloc failed")
	}
	p, _, _ := kernel.NewProc("GlobalLock").Call(h)
	if p == 0 {
		kernel.NewProc("GlobalFree").Call(h)
		t.Fatal("GlobalLock failed")
	}
	syscall.NewLazyDLL("ntdll.dll").NewProc("RtlMoveMemory").Call(p, uintptr(unsafe.Pointer(&u[0])), uintptr(2*len(u)))
	kernel.NewProc("GlobalUnlock").Call(h)
	if ok, _, _ := user32.NewProc("SetClipboardData").Call(13, h); ok == 0 {
		kernel.NewProc("GlobalFree").Call(h)
		t.Fatal("SetClipboardData failed")
	}
}
