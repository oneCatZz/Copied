package main

import (
	"sync"
	"time"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

var (
	selGeneralPasteboard = objc.RegisterName("generalPasteboard")
	selChangeCount       = objc.RegisterName("changeCount")
)

// clipboardSeq returns NSPasteboard's change count, which changes with the
// clipboard's content.
func clipboardSeq() (seq int64, ok bool) {
	pasteboard := objc.ID(objc.GetClass("NSPasteboard")).Send(selGeneralPasteboard)
	if pasteboard == 0 {
		return 0, false
	}
	return int64(pasteboard.Send(selChangeCount)), true
}

// frontWindow is nothing on macOS: the panel takes the keyboard without
// activating the app, and the app the user was in has it back as the panel
// hides.
func frontWindow() uintptr { return 0 }

const (
	keyCodeV            = 9
	eventFlagMaskCmd    = 1 << 20
	eventTapHID         = 0
	coreGraphicsLibrary = "/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics"
	coreFoundationLib   = "/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation"
)

var (
	loadEvents            sync.Once
	eventsLoaded          bool
	cgEventCreateKeyboard func(source uintptr, key uint16, down bool) uintptr
	cgEventSetFlags       func(event uintptr, flags uint64)
	cgEventPost           func(tap uint32, event uintptr)
	cfRelease             func(ref uintptr)
)

// pasteInto presses Command+V in the app the user is in. macOS lets the app
// do so once the user allowed it in System Settings, Privacy & Security,
// Accessibility; until then the clip is on the clipboard, to paste by hand.
func pasteInto(uintptr) {
	loadEvents.Do(func() {
		cg, err := purego.Dlopen(coreGraphicsLibrary, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			return
		}
		cf, err := purego.Dlopen(coreFoundationLib, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			return
		}
		purego.RegisterLibFunc(&cgEventCreateKeyboard, cg, "CGEventCreateKeyboardEvent")
		purego.RegisterLibFunc(&cgEventSetFlags, cg, "CGEventSetFlags")
		purego.RegisterLibFunc(&cgEventPost, cg, "CGEventPost")
		purego.RegisterLibFunc(&cfRelease, cf, "CFRelease")
		eventsLoaded = true
	})
	if !eventsLoaded {
		return
	}
	time.Sleep(80 * time.Millisecond) // until the app has the keyboard back
	for _, down := range []bool{true, false} {
		event := cgEventCreateKeyboard(0, keyCodeV, down)
		if event == 0 {
			return
		}
		cgEventSetFlags(event, eventFlagMaskCmd)
		cgEventPost(eventTapHID, event)
		cfRelease(event)
	}
}
