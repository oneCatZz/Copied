package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/egoist/mygo"
	_ "github.com/egoist/mygo/plugins/automation"
	"github.com/egoist/mygo/ui"
)

func TestMain(m *testing.M) {
	if os.Getenv("COPIED_NATIVE_HELPER") == "1" {
		runNativeTestApp()
		return
	}
	os.Exit(m.Run())
}

// The native fixture has its own temporary database, no clipboard watcher,
// global shortcuts, tray or login registration, and no single-instance lock.
func runNativeTestApp() {
	mygo.App.SetName("Copied UI Test")
	mygo.App.SetPath(mygo.PathUserData, filepath.Dir(os.Getenv("COPIED_NATIVE_DB")))
	mygo.App.SetLocale(languageChinese)
	mygo.App.OnWindowAllClosed(func() {})
	mygo.App.WhenReady(func() {
		if expected := os.Getenv("COPIED_NATIVE_EXPECT_LANGUAGE"); expected != "" {
			a := &app{settings: loadSettings()}
			a.configureLanguage()
			if a.settings.Language != expected || mygo.App.Locale() != expected {
				panic("saved application language was not restored on startup")
			}
			mygo.App.Quit()
			return
		}
		s, err := openStoreAt(os.Getenv("COPIED_NATIVE_DB"))
		if err != nil {
			panic(err)
		}
		records := 90
		memoryRun := os.Getenv("COPIED_NATIVE_MEMORY") == "1"
		switchRun := os.Getenv("COPIED_NATIVE_SWITCH") == "1"
		profileRun := memoryRun || switchRun
		if memoryRun {
			records = 360
		}
		for i := 0; i < records; i++ {
			clip := &Clip{Kind: KindText, Text: fmt.Sprintf("Native test %03d", i), Hash: fmt.Sprint(i), Length: 15}
			switch i % 3 {
			case 1:
				clip.Kind, clip.Text = KindLink, fmt.Sprintf("https://example.com/%d", i)
			case 2:
				clip.Kind, clip.Text = KindImage, ""
				clip.Data, clip.Thumb, clip.Width, clip.Height = trayIconPNG, trayIconPNG, 32, 32
				if memoryRun {
					clip.Data = memoryTestPNG(i)
					clip.Thumb, clip.Width, clip.Height = clip.Data, 520, 320
				}
			}
			if err := s.Add(clip); err != nil {
				panic(err)
			}
		}
		a := &app{store: s, thumbs: map[int64]*thumb{}, thumbSlots: make(chan struct{}, 2), shortcut: defaultShortcuts[0], shownAt: time.Now()}
		a.settings.Language = languageChinese
		a.configureLanguage()
		mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) {
			a.quitting = true
			a.cancelPanelRelease()
		})
		var samples []memorySample
		sample := func(phase string) {
			debug.FreeOSMemory()
			var heap runtime.MemStats
			runtime.ReadMemStats(&heap)
			if dir := os.Getenv("COPIED_NATIVE_HEAP_PROFILES"); dir != "" {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					panic(err)
				}
				f, err := os.Create(filepath.Join(dir, phase+".pprof"))
				if err != nil {
					panic(err)
				}
				if err := pprof.WriteHeapProfile(f); err != nil {
					panic(err)
				}
				if err := f.Close(); err != nil {
					panic(err)
				}
			}
			var counters processMemoryCounters
			counters.Size = uint32(unsafe.Sizeof(counters))
			ok, _, err := syscall.NewLazyDLL("psapi.dll").NewProc("GetProcessMemoryInfo").Call(^uintptr(0), uintptr(unsafe.Pointer(&counters)), uintptr(counters.Size))
			if ok == 0 {
				panic(err)
			}
			samples = append(samples, memorySample{
				Phase: phase, CacheEntries: len(a.thumbs), HeapMiB: float64(heap.HeapAlloc) / (1 << 20),
				WorkingSetMiB: float64(counters.WorkingSet) / (1 << 20), PrivateMiB: float64(counters.PrivateUsage) / (1 << 20),
			})
			data, err := json.MarshalIndent(samples, "", "  ")
			if err != nil {
				panic(err)
			}
			if err := os.WriteFile(os.Getenv("COPIED_NATIVE_MEMORY_REPORT"), data, 0o644); err != nil {
				panic(err)
			}
		}
		a.reload()
		a.panel = mygo.NewWindow(mygo.WindowOptions{
			Title: "Copied UI Test", Width: 1200, Height: panelHeight,
			Hidden: true, Panel: true, Frameless: true, Transparent: true,
			Vibrancy: mygo.VibrancyGlass, CornerRadius: 18, Content: ui.View(func(c *ui.Context) {
				if !profileRun {
					// Arm a global-style toggle before opening a modal, whose
					// focus scope intentionally blocks local window shortcuts.
					c.OnWindowShortcut(0, ui.KeyF7, func() {
						time.AfterFunc(time.Second, func() {
							mygo.RunOnMain(a.toggle)
							time.AfterFunc(100*time.Millisecond, func() { mygo.RunOnMain(a.toggle) })
						})
					})
					c.OnWindowShortcut(0, ui.KeyF8, func() {
						a.toggle()
						time.AfterFunc(100*time.Millisecond, func() { mygo.RunOnMain(a.toggle) })
					})
				}
				if profileRun {
					c.OnWindowShortcut(0, ui.KeyF9, func() {
						phase := "initial"
						if len(samples) > 0 {
							phase = "browsed"
						}
						if switchRun {
							phase = fmt.Sprintf("switch-round-%d", len(samples))
						}
						sample(phase)
					})
					c.OnWindowShortcut(0, ui.KeyF10, func() {
						a.panel.Hide()
						time.AfterFunc(200*time.Millisecond, func() {
							mygo.RunOnMain(func() {
								sample("hidden")
								if !switchRun {
									mygo.App.Quit()
								}
							})
						})
						if switchRun {
							time.AfterFunc(3*time.Second, func() {
								mygo.RunOnMain(func() {
									sample("hidden-settled")
									if !a.panel.IsDestroyed() {
										panic("hidden panel renderer was not destroyed")
									}
									old := a.panel
									a.show()
									if a.panel == old || !a.panel.IsVisible() {
										panic("panel was not recreated")
									}
									a.panel.Hide()
									a.show() // Cancel destruction when reopened during the grace period.
									time.AfterFunc(panelIdleDelay+300*time.Millisecond, func() {
										mygo.RunOnMain(func() {
											if a.panel.IsDestroyed() || !a.panel.IsVisible() {
												panic("stale hide timer destroyed the reopened panel")
											}
											sample("reopened")
											mygo.App.Quit()
										})
									})
								})
							})
						}
					})
				}
				a.view(c)
			}),
		})
		a.panel.OnHide(func() {
			a.panelHidden(a.panel)
		})
		if !switchRun {
			a.panel.OnClosed(mygo.App.Quit)
		}
		mygo.App.OnWillQuit(func(*mygo.QuitEvent) {
			a.clearThumbs()
			s.Close()
		})
		a.panel.SetPosition(70, 90)
		a.input.Focus()
		a.panel.Show()
	})
	if err := mygo.App.Run(); err != nil {
		panic(err)
	}
}

type processMemoryCounters struct {
	Size, PageFaults                                                     uint32
	PeakWorkingSet, WorkingSet, PeakPagedPool, PagedPool                 uintptr
	PeakNonPagedPool, NonPagedPool, Pagefile, PeakPagefile, PrivateUsage uintptr
}

type memorySample struct {
	Phase                              string
	CacheEntries                       int
	HeapMiB, WorkingSetMiB, PrivateMiB float64
}

func memoryTestPNG(seed int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 520, 320))
	random := uint32(seed + 1)
	for i := 0; i < len(img.Pix); i += 4 {
		random ^= random << 13
		random ^= random >> 17
		random ^= random << 5
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = byte(random), byte(random>>8), byte(random>>16), 255
	}
	var data bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := encoder.Encode(&data, img); err != nil {
		panic(err)
	}
	return data.Bytes()
}

func TestNativePanelInteractions(t *testing.T) {
	if os.Getenv("COPIED_NATIVE_TEST") != "1" {
		t.Skip("set COPIED_NATIVE_TEST=1 to open the isolated native test window")
	}
	if (os.Getenv("COPIED_NATIVE_MEMORY") == "1" || os.Getenv("COPIED_NATIVE_SWITCH") == "1") && os.Getenv("COPIED_NATIVE_MEMORY_REPORT") == "" {
		t.Fatal("set COPIED_NATIVE_MEMORY_REPORT to the memory report path")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	socket := filepath.Join(dir, "ui.sock")
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), "COPIED_NATIVE_HELPER=1", "MYGO_AUTOMATION="+socket, "COPIED_NATIVE_DB="+filepath.Join(dir, "history.db"))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	var logs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &logs, &logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()
	t.Cleanup(func() {
		cmd.Process.Kill()
		<-done
		if logs.Len() > 0 {
			t.Log(logs.String())
		}
	})
	var conn net.Conn
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
		conn, err = net.DialTimeout("unix", socket, 200*time.Millisecond)
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("native automation did not start: %v", err)
	}
	defer conn.Close()
	reader, sequence := bufio.NewReader(conn), 0
	call := func(method string, params any, result any) {
		t.Helper()
		sequence++
		conn.SetDeadline(time.Now().Add(10 * time.Second))
		request, err := json.Marshal(map[string]any{"id": sequence, "method": method, "params": params})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Write(append(request, '\n')); err != nil {
			t.Fatal(err)
		}
		line, err := reader.ReadBytes('\n')
		if err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		var response struct {
			Result json.RawMessage
			Error  *struct{ Code, Message string }
		}
		if err := json.Unmarshal(line, &response); err != nil {
			t.Fatal(err)
		}
		if response.Error != nil {
			t.Fatalf("%s: %s: %s", method, response.Error.Code, response.Error.Message)
		}
		if result != nil {
			if err := json.Unmarshal(response.Result, result); err != nil {
				t.Fatal(err)
			}
		}
	}
	// The automation listener starts before WhenReady finishes seeding the
	// temporary database and creating the window.
	ready := false
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		var result struct {
			Windows []struct{ Visible bool }
		}
		call("windows", nil, &result)
		if len(result.Windows) > 0 && result.Windows[0].Visible {
			ready = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !ready {
		t.Fatal("native test window did not become visible")
	}
	idle := func() { call("idle", map[string]any{"timeout": 5000}, nil) }
	type testNode struct {
		ID, Label, Role string
		States          []string
	}
	readTree := func() []testNode {
		var tree struct{ Nodes []testNode }
		call("tree", nil, &tree)
		return tree.Nodes
	}
	press := func(label string) {
		t.Helper()
		for _, node := range readTree() {
			if node.Role == "radio" && node.Label == label {
				call("act", map[string]any{"id": node.ID, "action": "press"}, nil)
				return
			}
		}
		t.Fatalf("native filter %q was not found", label)
	}
	capture := func(name string) []byte {
		t.Helper()
		var result struct {
			PNG           string
			Width, Height int
		}
		call("capture", nil, &result)
		data, err := base64.StdEncoding.DecodeString(result.PNG)
		if err != nil || len(data) == 0 || result.Width < 1000 {
			t.Fatalf("invalid native capture: %v, size=%dx%d", err, result.Width, result.Height)
		}
		if dir := os.Getenv("COPIED_TEST_ARTIFACTS"); dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, name+".png"), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return data
	}
	idle()
	for _, label := range []string{"文本", "图片", "全部", "链接", "文本", "图片"} {
		press(label)
	}
	idle()
	checked, count := false, false
	for _, node := range readTree() {
		expectedCount := "30 条"
		if os.Getenv("COPIED_NATIVE_MEMORY") == "1" {
			expectedCount = "120 条"
		}
		if node.Label == expectedCount {
			count = true
		}
		if node.Role == "radio" && node.Label == "图片" {
			for _, state := range node.States {
				checked = checked || state == "checked"
			}
		}
	}
	if !checked || !count {
		t.Fatalf("native image filter state: checked=%v, count=%v", checked, count)
	}
	before := capture("native-images")
	call("input", map[string]any{"events": []map[string]any{{"type": "wheel", "x": 600, "y": 180, "dx": 0, "dy": 240}}}, nil)
	idle()
	after := capture("native-images-scrolled")
	if bytes.Equal(before, after) {
		t.Fatal("native wheel did not change the rendered panel")
	}
	if os.Getenv("COPIED_NATIVE_MEMORY") == "1" || os.Getenv("COPIED_NATIVE_SWITCH") == "1" {
		key := func(key string) {
			call("input", map[string]any{"events": []map[string]any{{"type": "key", "key": key}}}, nil)
		}
		key("F9")
		if os.Getenv("COPIED_NATIVE_SWITCH") == "1" {
			for round := 0; round < 3; round++ {
				for cycle := 0; cycle < 5; cycle++ {
					for _, label := range filterLabelsFor(languageChinese) {
						press(label)
						idle()
					}
				}
				key("F9")
			}
		} else {
			for i := 0; i < 32; i++ {
				call("input", map[string]any{"events": []map[string]any{{"type": "wheel", "x": 600, "y": 180, "dx": 1000, "dy": 0, "precise": true}}}, nil)
				idle()
			}
			key("F9")
		}
		key("F10")
		select {
		case <-done:
		case <-time.After(9 * time.Second):
			t.Fatal("memory fixture did not exit")
		}
		if !cmd.ProcessState.Success() {
			t.Fatalf("native memory/lifecycle fixture failed: %s", logs.String())
		}
		return
	}
	call("input", map[string]any{"events": []map[string]any{{"type": "click", "button": "right", "x": 120, "y": 180}}}, nil)
	idle()
	menuVisible := func() bool {
		for _, node := range readTree() {
			if node.Label == "仅复制" && node.Role == "menuItem" {
				return true
			}
		}
		return false
	}
	if !menuVisible() {
		t.Fatal("native context menu did not open")
	}
	capture("native-card-menu")
	call("input", map[string]any{"events": []map[string]any{{"type": "key", "key": "Escape"}}}, nil)
	idle()
	if menuVisible() {
		t.Fatal("Escape did not dismiss the native context menu")
	}
	call("input", map[string]any{"events": []map[string]any{{"type": "click", "button": "right", "x": 120, "y": 180}}}, nil)
	idle()
	if !menuVisible() {
		t.Fatal("native context menu did not reopen after Escape")
	}
	// Exercise the same toggle path as the global shortcut, twice inside
	// the renderer's two-second grace period, without global registrations.
	call("input", map[string]any{"events": []map[string]any{{"type": "key", "key": "F8"}}}, nil)
	time.Sleep(200 * time.Millisecond)
	idle()
	if menuVisible() {
		t.Fatal("native context menu survived hiding and reopening the panel")
	}
	capture("native-reopened-without-menu")
	call("input", map[string]any{"events": []map[string]any{
		{"type": "click", "x": 200, "y": 34},
		{"type": "text", "text": "search menu regression"},
		{"type": "click", "button": "right", "x": 200, "y": 34},
	}}, nil)
	idle()
	hasRole := func(role string) bool {
		for _, node := range readTree() {
			if node.Role == role {
				return true
			}
		}
		return false
	}
	if !hasRole("menu") {
		t.Fatal("native search editing menu was not drawn in the panel")
	}
	chineseItems := map[string]bool{"撤销": false, "剪切": false, "复制": false, "粘贴": false, "删除": false, "全选": false}
	for _, node := range readTree() {
		if node.Role == "menuItem" {
			if _, expected := chineseItems[node.Label]; expected {
				chineseItems[node.Label] = true
			}
		}
	}
	for label, found := range chineseItems {
		if !found {
			t.Fatalf("native search menu is missing the Chinese label %q", label)
		}
	}
	capture("native-search-menu")
	call("input", map[string]any{"events": []map[string]any{{"type": "key", "key": "F8"}}}, nil)
	time.Sleep(200 * time.Millisecond)
	idle()
	if hasRole("menu") {
		t.Fatal("native search menu survived hiding and reopening the panel")
	}
	call("input", map[string]any{"events": []map[string]any{
		{"type": "key", "key": "F7"},
		{"type": "key", "key": "CmdOrCtrl+,"},
	}}, nil)
	idle()
	if !hasRole("dialog") {
		t.Fatal("native settings dialog did not open")
	}
	capture("native-settings")
	time.Sleep(1200 * time.Millisecond)
	idle()
	if hasRole("dialog") || hasRole("menu") {
		t.Fatal("native settings or menu survived hiding and reopening the panel")
	}
	call("input", map[string]any{"events": []map[string]any{{"type": "key", "key": "CmdOrCtrl+,"}}}, nil)
	idle()
	hasLabel := func(label string) bool {
		for _, node := range readTree() {
			if node.Label == label {
				return true
			}
		}
		return false
	}
	wantPreference := func(language string) {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(dir, "settings.json"))
		var saved Settings
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &saved); err != nil || saved.Language != language {
			t.Fatalf("language preference was not saved: %s, error=%v", data, err)
		}
	}
	wantRestoredPreference := func(language string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		probe := exec.CommandContext(ctx, exe)
		probe.Env = append(os.Environ(), "COPIED_NATIVE_HELPER=1", "MYGO_AUTOMATION=",
			"COPIED_NATIVE_DB="+filepath.Join(dir, "history.db"), "COPIED_NATIVE_EXPECT_LANGUAGE="+language)
		probe.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		if output, err := probe.CombinedOutput(); err != nil {
			t.Fatalf("language was not restored by a fresh process: %v\n%s", err, output)
		}
	}
	press("English")
	idle()
	if !hasLabel("Done") || !hasLabel("Follow system") {
		t.Fatal("native app did not switch all surfaces to English")
	}
	wantPreference(languageEnglish)
	wantRestoredPreference(languageEnglish)
	capture("native-settings-english")
	for _, node := range readTree() {
		if node.Label == "Done" && node.Role == "button" {
			call("act", map[string]any{"id": node.ID, "action": "press"}, nil)
			break
		}
	}
	idle()
	if !hasLabel("Images") || !hasLabel("Search") {
		t.Fatal("native panel did not follow the English settings language")
	}
	call("input", map[string]any{"events": []map[string]any{{"type": "click", "button": "right", "x": 200, "y": 34}}}, nil)
	idle()
	if !hasLabel("Copy") || !hasLabel("Paste") || hasLabel("复制") {
		t.Fatal("native editing menu did not follow the English application language")
	}
	capture("native-search-menu-english")
	call("input", map[string]any{"events": []map[string]any{
		{"type": "key", "key": "Escape"}, {"type": "key", "key": "CmdOrCtrl+,"},
	}}, nil)
	idle()
	press("简体中文")
	idle()
	if !hasLabel("完成") || !hasLabel("跟随系统") {
		t.Fatal("native app did not switch all surfaces back to Chinese")
	}
	wantPreference(languageChinese)
	wantRestoredPreference(languageChinese)
	press("跟随系统")
	idle()
	wantPreference("")
	call("input", map[string]any{"events": []map[string]any{{"type": "key", "key": "Escape"}}}, nil)
	idle()
	call("input", map[string]any{"events": []map[string]any{{"type": "key", "key": "Escape"}}}, nil)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("native panel did not close on Escape")
	}
}
