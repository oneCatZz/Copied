package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

var (
	procGetClipboardOwner          = user32.NewProc("GetClipboardOwner")
	procQueryFullProcessImageNameW = kernel32.NewProc("QueryFullProcessImageNameW")
	versionDLL                     = syscall.NewLazyDLL("version.dll")
	procGetFileVersionInfoSizeW    = versionDLL.NewProc("GetFileVersionInfoSizeW")
	procGetFileVersionInfoW        = versionDLL.NewProc("GetFileVersionInfoW")
	procVerQueryValueW             = versionDLL.NewProc("VerQueryValueW")

	appNamesMu sync.Mutex
	appNames   = map[string]string{} // by the path of a program
)

const processQueryLimitedInformation = 0x1000

// clipboardSource names the app that put what is on the clipboard there:
// the one owning the clipboard, or else the one in front.
func clipboardSource() (string, string) {
	h, _, _ := procGetClipboardOwner.Call()
	if h == 0 {
		h = frontWindow()
	}
	if h == 0 {
		return "", ""
	}
	var pid uint32
	procGetWindowThreadProcessId.Call(h, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 || pid == uint32(os.Getpid()) {
		return "", ""
	}
	path := processPath(pid)
	if path == "" {
		return "", ""
	}
	appNamesMu.Lock()
	defer appNamesMu.Unlock()
	name, ok := appNames[path]
	if !ok {
		name = fileDescription(path)
		if name == "" {
			name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		}
		appNames[path] = name
	}
	return name, path
}

// processPath returns the path of the program a process runs.
func processPath(pid uint32) string {
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, pid)
	if err != nil {
		return ""
	}
	defer syscall.CloseHandle(h)
	buf := make([]uint16, 1024)
	n := uint32(len(buf))
	if r, _, _ := procQueryFullProcessImageNameW.Call(uintptr(h), 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n))); r == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:n])
}

// fileDescription returns the name a program gives itself in its version
// information, such as "Google Chrome" for chrome.exe.
func fileDescription(path string) string {
	file, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return ""
	}
	size, _, _ := procGetFileVersionInfoSizeW.Call(uintptr(unsafe.Pointer(file)), 0)
	if size == 0 {
		return ""
	}
	info := make([]byte, size)
	if r, _, _ := procGetFileVersionInfoW.Call(uintptr(unsafe.Pointer(file)), 0, size, uintptr(unsafe.Pointer(&info[0]))); r == 0 {
		return ""
	}
	// query returns the bytes of a value, which lie within info.
	query := func(name string) []byte {
		sub, err := syscall.UTF16PtrFromString(name)
		if err != nil {
			return nil
		}
		var at uintptr
		var n uint32
		if r, _, _ := procVerQueryValueW.Call(uintptr(unsafe.Pointer(&info[0])), uintptr(unsafe.Pointer(sub)), uintptr(unsafe.Pointer(&at)), uintptr(unsafe.Pointer(&n))); r == 0 || at == 0 {
			return nil
		}
		start := at - uintptr(unsafe.Pointer(&info[0]))
		if start >= uintptr(len(info)) {
			return nil
		}
		return info[start:]
	}
	languages := []string{"040904b0", "040904e4", "000004b0"}
	if t := query(`\VarFileInfo\Translation`); len(t) >= 4 {
		le := binary.LittleEndian
		languages = append([]string{fmt.Sprintf("%04x%04x", le.Uint16(t), le.Uint16(t[2:]))}, languages...)
	}
	for _, language := range languages {
		value := query(`\StringFileInfo\` + language + `\FileDescription`)
		units := make([]uint16, 0, 32)
		for i := 0; i+1 < len(value) && len(units) < 256; i += 2 {
			u := binary.LittleEndian.Uint16(value[i:])
			if u == 0 {
				break
			}
			units = append(units, u)
		}
		if name := strings.TrimSpace(syscall.UTF16ToString(units)); name != "" {
			return name
		}
	}
	return ""
}
