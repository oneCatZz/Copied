package main

import (
	"os"
	"sync"
	"time"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"github.com/egoist/mygo"
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

// frontWindow uses the active app's process on macOS. The panel takes the
// keyboard without activating the app.
func frontWindow() uintptr { return uintptr(frontmostPID()) }

var (
	selLocalizedName   = objc.RegisterName("localizedName")
	selUTF8String      = objc.RegisterName("UTF8String")
	selSourceBundleURL = objc.RegisterName("bundleURL")
	selSourcePath      = objc.RegisterName("path")
)

// clipboardSource names the app in front, as the user copies in it as a
// rule: macOS does not record which app wrote the clipboard.
func clipboardSource() (string, string) {
	var name, path string
	mygo.RunOnMain(func() {
		app := objc.ID(objc.GetClass("NSWorkspace")).Send(selSharedWorkspace).Send(selFrontmostApp)
		if app == 0 || int32(app.Send(selProcessIdentifier)) == int32(os.Getpid()) {
			return
		}
		if s := app.Send(selLocalizedName); s != 0 {
			name = objc.Send[string](s, selUTF8String)
		}
		if url := app.Send(selSourceBundleURL); url != 0 {
			if s := url.Send(selSourcePath); s != 0 {
				path = objc.Send[string](s, selUTF8String)
			}
		}
	})
	return name, path
}

func writeImage(data []byte) error { return mygo.Clipboard.WriteImage(data) }

// terminalWindow is never known on macOS: a picture is pasted as one.
func terminalWindow(uintptr) bool { return false }

func writeImageText(data []byte, _ string) error { return writeImage(data) }

const (
	keyCodeV            = 9
	eventFlagMaskCmd    = 1 << 20
	eventTapSession     = 1                     // kCGSessionEventTap
	eventStatePrivate   = -1                    // kCGEventSourceStatePrivate
	eventStateHID       = 1                     // kCGEventSourceStateHIDSystemState
	eventFlagsHeld      = 1<<17 | 1<<18 | 1<<19 // Shift, Control, Option
	coreGraphicsLibrary = "/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics"
	coreFoundationLib   = "/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation"
)

var (
	loadEvents            sync.Once
	eventsLoaded          bool
	cgEventSourceCreate   func(state int32) uintptr
	cgEventCreateKeyboard func(source uintptr, key uint16, down bool) uintptr
	cgEventSetFlags       func(event uintptr, flags uint64)
	cgEventPost           func(tap uint32, event uintptr)
	cfRelease             func(ref uintptr)
	cgEventSourceFlags    func(state int32) uint64
	cgEventPostToPid      func(pid int32, event uintptr)
)

var (
	selSharedWorkspace   = objc.RegisterName("sharedWorkspace")
	selFrontmostApp      = objc.RegisterName("frontmostApplication")
	selProcessIdentifier = objc.RegisterName("processIdentifier")
)

// pasteInto presses Command+V in the app the user is in. macOS lets the app
// do so once the user allowed it in System Settings, Privacy & Security,
// Accessibility; until then the clip is on the clipboard, to paste by hand.
//
// The keys come from a private event source. Posted to the session, they
// take on the modifiers the user holds: Option still held from the stack's
// shortcut would make Command+Option+V. While one is held, they go to the
// frontmost app itself instead.
func pasteInto(target uintptr) {
	loadEvents.Do(func() {
		cg, err := purego.Dlopen(coreGraphicsLibrary, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			return
		}
		cf, err := purego.Dlopen(coreFoundationLib, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			return
		}
		purego.RegisterLibFunc(&cgEventSourceCreate, cg, "CGEventSourceCreate")
		purego.RegisterLibFunc(&cgEventCreateKeyboard, cg, "CGEventCreateKeyboardEvent")
		purego.RegisterLibFunc(&cgEventSetFlags, cg, "CGEventSetFlags")
		purego.RegisterLibFunc(&cgEventPost, cg, "CGEventPost")
		purego.RegisterLibFunc(&cfRelease, cf, "CFRelease")
		purego.RegisterLibFunc(&cgEventSourceFlags, cg, "CGEventSourceFlagsState")
		purego.RegisterLibFunc(&cgEventPostToPid, cg, "CGEventPostToPid")
		eventsLoaded = true
	})
	if !eventsLoaded {
		return
	}
	time.Sleep(80 * time.Millisecond) // until the app has the keyboard back
	pid := frontmostPID()
	if target != 0 && uintptr(pid) != target {
		return
	}
	source := cgEventSourceCreate(eventStatePrivate)
	if source == 0 {
		return
	}
	defer cfRelease(source)
	held := cgEventSourceFlags(eventStateHID)
	for _, down := range []bool{true, false} {
		event := cgEventCreateKeyboard(source, keyCodeV, down)
		if event == 0 {
			return
		}
		cgEventSetFlags(event, eventFlagMaskCmd)
		if pid > 0 && held&eventFlagsHeld != 0 {
			cgEventPostToPid(pid, event)
		} else {
			cgEventPost(eventTapSession, event)
		}
		cfRelease(event)
	}
}

var (
	selRunningAppWithPID = objc.RegisterName("runningApplicationWithProcessIdentifier:")
	selActivateWithOpts  = objc.RegisterName("activateWithOptions:")
)

// viewerFront is the app the user was in before the picture viewer brought
// Copied to the front, or 0.
var viewerFront int32

// activateForViewer brings Copied to the front while the panel shows a
// picture large: macOS gives the pinches of a touchpad only to the app in
// front, and the panel never activates it of its own.
func activateForViewer() {
	if viewerFront != 0 {
		return
	}
	// NSApp's isActive is true already while the panel has the keyboard:
	// the app in front is what the window server says.
	pid := frontmostPID()
	if pid == 0 || pid == int32(os.Getpid()) {
		return
	}
	viewerFront = pid
	mygo.App.Focus()
}

// restoreFront gives the app the user was in the front back as the panel
// hides, unless the user went to another app meanwhile.
func restoreFront() {
	pid := viewerFront
	viewerFront = 0
	if pid == 0 || frontmostPID() != int32(os.Getpid()) {
		return
	}
	mygo.RunOnMain(func() {
		app := objc.ID(objc.GetClass("NSRunningApplication")).Send(selRunningAppWithPID, pid)
		if app != 0 {
			app.Send(selActivateWithOpts, uint(1<<1)) // NSApplicationActivateIgnoringOtherApps
		}
	})
}

// frontmostPID returns the process of the app the user is in, or 0.
func frontmostPID() int32 {
	var pid int32
	mygo.RunOnMain(func() {
		app := objc.ID(objc.GetClass("NSWorkspace")).Send(selSharedWorkspace).Send(selFrontmostApp)
		if app != 0 {
			pid = int32(app.Send(selProcessIdentifier))
		}
	})
	return pid
}
