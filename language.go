package main

import (
	"copied/locales"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const (
	languageEnglish = "en"
	languageChinese = "zh-Hans"
)

func languageSetting(tag string) string {
	switch tag {
	case languageEnglish, languageChinese:
		return tag
	default:
		return "" // Empty or an unsupported setting follows the system.
	}
}

func (a *app) configureLanguage() {
	a.settings.Language = languageSetting(a.settings.Language)
	mygo.App.SetLocale(a.settings.Language)
	mygo.App.OnLocaleChanged(func(string) { a.updateTrayMenu() })
}

func (a *app) changeLanguage(tag string) {
	a.settings.Language = languageSetting(tag)
	mygo.App.SetLocale(a.settings.Language)
	a.saveSettings()
}

func (a *app) languageOptions(c *ui.Context) {
	t, tx := c.Theme(), locales.In(c.Locale())
	ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Text(c, tx.Language()).FontSize(13).FontWeight(550).Grow(1)
		ui.RadioGroup(c.Key("language"), func() {
			for _, option := range []struct{ tag, label string }{
				{"", tx.FollowSystem()}, {languageChinese, "简体中文"}, {languageEnglish, "English"},
			} {
				tag := option.tag
				button := ui.RadioBase(c.Key(tag), &a.settings.Language, tag).Height(34).PaddingX(10).
					Center().Radius(6).Cursor(ui.CursorPointer).Label(option.label).
					OnChange(func() { a.changeLanguage(tag) })
				fill, ink, border := t.Surface, t.Text, t.Border
				if a.settings.Language == tag {
					fill, ink, border = t.Accent.Alpha(0.12), t.Accent, t.Accent
				} else if button.Hovered() {
					fill = t.SurfaceHover
				}
				button.Background(fill).Border(1, border).TextColor(ink).Children(func() {
					ui.Text(c, option.label).FontSize(12).SingleLine()
				})
			}
		}).Row().Gap(4).Label(tx.Language())
	})
}

func filterLabelsFor(locale string) [8]string {
	tx := locales.In(locale)
	return [8]string{tx.All(), tx.Text(), tx.Code(), tx.Links(), tx.Colors(), tx.Images(), tx.Files(), tx.Favorites()}
}
