package main

import (
	"encoding/json"
	"testing"
	"time"

	"copied/locales"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"
)

func TestApplicationLanguageFallback(t *testing.T) {
	supported := i18n.Languages()
	if len(supported) != 2 || supported[0] != languageEnglish || supported[1] != languageChinese {
		t.Fatalf("unexpected application languages: %v", supported)
	}
	for _, test := range []struct{ preferred, want string }{
		{"zh-CN", languageChinese}, {"en-US", languageEnglish}, {"de-DE", languageEnglish},
	} {
		got := i18n.Match([]string{test.preferred}, supported)
		if got == "" {
			got = supported[0]
		}
		if got != test.want {
			t.Fatalf("system language %s resolved to %s, want %s", test.preferred, got, test.want)
		}
	}
}

func TestLanguagePreferencePreservesShortcut(t *testing.T) {
	for _, tag := range []string{"", languageChinese, languageEnglish} {
		before := Settings{Shortcut: "Alt+Shift+V", Language: tag}
		data, err := json.Marshal(before)
		if err != nil {
			t.Fatal(err)
		}
		var after Settings
		if err := json.Unmarshal(data, &after); err != nil || after != before {
			t.Fatalf("settings did not round-trip: %s, %v", data, err)
		}
	}
	if languageSetting("fr") != "" {
		t.Fatal("an unsupported saved language must follow the system")
	}
}

func TestLocalizedClipDetailsAndTimes(t *testing.T) {
	for _, test := range []struct {
		clip   Clip
		en, zh string
	}{
		{Clip{Kind: KindFiles, Length: 1}, "1 file", "1 个文件"},
		{Clip{Kind: KindFiles, Length: 2}, "2 files", "2 个文件"},
		{Clip{Kind: KindText, Length: 1}, "1 character", "1 个字符"},
		{Clip{Kind: KindText, Length: 8}, "8 characters", "8 个字符"},
	} {
		if got := test.clip.detail(languageEnglish); got != test.en {
			t.Errorf("English detail: %q, want %q", got, test.en)
		}
		if got := test.clip.detail(languageChinese); got != test.zh {
			t.Errorf("Chinese detail: %q, want %q", got, test.zh)
		}
	}
	if got := ago(time.Now().Add(-2*time.Minute), languageEnglish); got != "2 minutes ago" {
		t.Fatalf("English relative time: %s", got)
	}
	if got := locales.In("fr").Settings(); got != "Settings" {
		t.Fatalf("missing language did not fall back to English: %s", got)
	}
}

func TestBothLanguagesAcrossSurfaces(t *testing.T) {
	for _, tag := range []string{languageEnglish, languageChinese} {
		t.Run(tag, func(t *testing.T) {
			tx := locales.In(tag)
			a := stripTestApp()
			tt := ui.NewTester(a.view, 1200, panelHeight)
			tt.SetLocale(tag)
			search, found := tt.AccessByRole(ui.RoleTextField, tx.Search())
			if !found || search.Placeholder != tx.SearchHistory() || !tt.HasText(tx.TypeFilter(tx.All())) || !tt.HasText(tx.ClipCount(200)) {
				t.Fatal("panel did not use its window's language")
			}
			saveUIArtifact(t, tt, "panel-"+tag)
			if err := tt.Click(tx.ClipboardTypes()); err != nil {
				t.Fatal(err)
			}
			if !tt.HasText(tx.Images()) || !tt.HasText(tx.Code()) {
				t.Fatal("type dropdown did not use its window's language")
			}
			tt.Key(0, ui.KeyEscape)
			a.settingsOpen, a.recordError = true, "shortcutUnavailable"
			tt.Frame()
			if !tt.HasText(tx.Language()) || !tt.HasText(tx.ShortcutUnavailable()) || !tt.HasText(tx.Done()) {
				t.Fatal("settings did not use its window's language")
			}
			saveUIArtifact(t, tt, "settings-"+tag)
			tt.SetDark(true)
			saveUIArtifact(t, tt, "settings-dark-"+tag)
			tray := ui.NewTester(a.trayMenuView, trayMenuWidth, 320)
			tray.SetLocale(tag)
			if !tray.HasText(tx.OpenPanel()) || !tray.HasText(tx.QuitCopied()) || !tray.HasText(tx.ClipboardRecording()) {
				t.Fatal("tray did not use its window's language")
			}
			saveUIArtifact(t, tray, "tray-"+tag)
			menu := cardMenuTester(&app{clips: []*Clip{{ID: 1, Kind: KindText}}}, false)
			menu.SetLocale(tag)
			menu.RightClickAt(40, 50)
			if !menu.HasText(tx.PastePlain()) || !menu.HasText(tx.CopyOnly()) || !menu.HasText(tx.Delete()) {
				t.Fatal("card menu did not use its window's language")
			}
			saveUIArtifact(t, menu, "card-menu-"+tag)
		})
	}
}
