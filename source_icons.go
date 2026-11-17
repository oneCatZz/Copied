package main

import (
	"context"
	"strings"
	"sync"
	"unicode"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type sourceIcon struct {
	bitmap *ui.Bitmap
	cancel context.CancelFunc
}

// Source paths survive restarts; cached PNGs also survive app updates or removal.
func (s *Store) saveSourcePath(name, path string) error {
	if name == "" || path == "" {
		return nil
	}
	_, err := s.db.Execute(context.Background(), `INSERT INTO source_apps (name, path) VALUES (?, ?)
		ON CONFLICT (name) DO UPDATE SET path = excluded.path, icon = NULL
		WHERE source_apps.path <> excluded.path`, name, path)
	return err
}

func (s *Store) sourceIconData(name string) (string, []byte) {
	rows, err := s.db.Query(context.Background(), `SELECT path, icon FROM source_apps WHERE name = ?`, name)
	if err != nil || len(rows.Values) == 0 {
		return "", nil
	}
	data, _ := rows.Values[0][1].([]byte)
	return str(rows.Values[0][0]), data
}

func (s *Store) saveSourceIcon(name, path string, data []byte) {
	_, _ = s.db.Execute(context.Background(), `INSERT INTO source_apps (name, path, icon) VALUES (?, ?, ?)
		ON CONFLICT (name) DO UPDATE SET icon = excluded.icon
		WHERE source_apps.path = excluded.path`, name, path, data)
}

var sourceCatalog struct {
	sync.Once
	apps []mygo.InstalledApp
}

// Old history only has display names. Match installed apps conservatively,
// leaving an initial when a name cannot identify an application reliably.
func installedSourcePath(name string) string {
	sourceCatalog.Do(func() { sourceCatalog.apps, _ = mygo.Shell.InstalledApps() })
	normalize := func(name string) string {
		name = strings.ToLower(strings.TrimSpace(name))
		name = strings.TrimSuffix(name, " (gui launcher)")
		return strings.TrimSuffix(name, " screenshot utility")
	}
	var path string
	for _, app := range sourceCatalog.apps {
		if normalize(app.Name) == normalize(name) {
			if path != "" && path != app.Path {
				return "" // Several installations share this name.
			}
			path = app.Path
		}
	}
	return path
}

func (a *app) sourceIconBitmap(name string) *ui.Bitmap {
	if name == "" || a.store == nil || a.panel == nil {
		return nil
	}
	if icon, ok := a.sourceIcons[name]; ok {
		return icon.bitmap
	}
	if a.sourceIcons == nil {
		a.sourceIcons = make(map[string]*sourceIcon)
	}
	if a.sourceIconSlots == nil {
		a.sourceIconSlots = make(chan struct{}, 2)
	}
	ctx, cancel := context.WithCancel(context.Background())
	icon := &sourceIcon{cancel: cancel}
	a.sourceIcons[name] = icon
	store, panel, slots := a.store, a.panel, a.sourceIconSlots
	go func() {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			return
		}
		defer func() { <-slots }()
		if ctx.Err() != nil {
			return
		}
		path, data := store.sourceIconData(name)
		if len(data) == 0 {
			if path == "" {
				path = installedSourcePath(name)
			}
			if path == "" || ctx.Err() != nil {
				return
			}
			var err error
			data, err = mygo.Shell.FileIcon(path, 48)
			if err != nil || len(data) == 0 || ctx.Err() != nil {
				return
			}
			store.saveSourceIcon(name, path, data)
		}
		bitmap, err := ui.DecodeBitmap(data)
		if err != nil || ctx.Err() != nil {
			return
		}
		panel.Update(func() {
			if a.panel == panel && a.sourceIcons[name] == icon && ctx.Err() == nil {
				icon.bitmap = bitmap
			}
		})
	}()
	return nil
}

func (a *app) sourceIconView(c *ui.Context, name string, size float32, t *ui.Theme) {
	if bitmap := a.sourceIconBitmap(name); bitmap != nil {
		ui.Image(c, bitmap).Size(size, size).Shrink(0).Fit(ui.Contain)
		return
	}
	ui.Box(c).Size(size, size).Shrink(0).Center().Radius(5).Background(t.Accent.Alpha(0.1)).Children(func() {
		if name == "" {
			ui.Icon(c, filterAllIcon).FontSize(size * 0.65).TextColor(t.Accent)
			return
		}
		for _, initial := range name {
			ui.Text(c, string(unicode.ToUpper(initial))).FontSize(size * 0.65).FontWeight(600).TextColor(t.Accent)
			break
		}
	})
}

func (a *app) clearSourceIcons() {
	for _, icon := range a.sourceIcons {
		icon.cancel()
	}
	a.sourceIcons = nil
}
