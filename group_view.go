package main

import (
	"log"
	"slices"
	"strings"

	"copied/locales"

	"github.com/egoist/mygo/ui"
)

type headerMenuKey struct {
	session uint64
	name    string
}

func (a *app) hasGroup(id int64) bool {
	return slices.ContainsFunc(a.groups, func(g Group) bool { return g.ID == id })
}

func (a *app) currentGroup() Group {
	for _, group := range a.groups {
		if group.ID == a.groupID {
			return group
		}
	}
	return Group{}
}

func selectTrigger(c *ui.Context, button ui.Element, label string, icon *ui.SVG) ui.Element {
	t := c.Theme()
	button.Row().Height(34).Shrink(0).PaddingX(10).Gap(6).AlignItems(ui.Center).
		Radius(9).Border(1, t.Border).Background(t.Surface).TextColor(t.TextMuted).
		Cursor(ui.CursorPointer).Label(label)
	if button.Hovered() || button.FocusVisible() {
		button.Background(t.SurfaceHover).Border(1, t.TextMuted.Alpha(0.4))
	}
	button.Children(func() {
		ui.Text(c, label).FontSize(12).SingleLine().Grow(1)
		ui.Icon(c, icon).FontSize(13).Shrink(0)
	})
	return button
}

func headerIconButton(c *ui.Context, key headerMenuKey, icon *ui.SVG, label string) ui.Element {
	t := c.Theme()
	button := ui.ButtonBase(c.Key(key)).Size(28, 28).Shrink(0).Center().Radius(8).
		Cursor(ui.CursorPointer).Label(label).Description(label)
	if button.Hovered() || button.FocusVisible() {
		button.Background(t.Surface).TextColor(t.Accent)
	} else {
		button.TextColor(t.TextMuted)
	}
	button.Children(func() { ui.Icon(c, icon).FontSize(15) })
	return button
}

func (a *app) groupNavigation(c *ui.Context, width float32) {
	const compactWidth = 240
	t, tx := c.Theme(), locales.In(c.Locale())
	locale := c.Locale()
	ui.Row(c.Key("group-navigation")).Width(width).Grow(1).Height(34).Gap(4).AlignItems(ui.Center).Children(func() {
		// Small displays use one scope picker; its label always names the
		// current group. Larger displays reserve space for both controls.
		if width < compactWidth {
			button := ui.ButtonBase(c.Key(headerMenuKey{a.searchSession, "scope"}))
			selectTrigger(c, button, a.currentGroup().label(locale), iconChevron).Grow(1)
			a.groupMenu(button, locale)
		} else {
			const groupWidth = float32(80)
			capacity := min(4, max(0, int((width-64-groupWidth-8)/(groupWidth+2))))
			visible := append([]Group(nil), a.groups[:min(capacity, len(a.groups))]...)
			if a.groupID != 0 && !slices.ContainsFunc(visible, func(g Group) bool { return g.ID == a.groupID }) && len(visible) > 0 {
				visible[len(visible)-1] = a.currentGroup()
			}
			items := append([]Group{{}}, visible...)
			target := float32(0)
			for i, group := range items {
				if group.ID == a.groupID {
					target = float32(i) * (groupWidth + 2)
				}
			}
			if a.filterFlow.width() != groupWidth {
				a.filterFlow = filterFlow{target: target, targetWidth: groupWidth}
			} else if a.filterFlow.target != target {
				left, right, _ := a.filterFlow.edges(c.Now())
				a.filterFlow = filterFlow{left: left, right: right, target: target, targetWidth: groupWidth, started: c.Now()}
			}
			trackWidth := float32(len(items))*(groupWidth+2) - 2
			ui.RadioGroup(c.Key("groups"), func() {
				for _, group := range items {
					label := group.label(locale)
					chip := ui.RadioBase(c.Key(group.ID), &a.groupID, group.ID).Size(groupWidth, 28).
						Center().PaddingX(9).Radius(14).Cursor(ui.CursorPointer).Label(label).Description(label).
						OnChange(func() { a.selectGroup(group.ID) })
					ink := t.TextMuted
					if a.groupID == group.ID {
						ink = t.Accent
					} else if chip.Hovered() {
						chip.Background(t.Surface.Alpha(0.6))
					}
					chip.Children(func() {
						ui.Text(c, label).FontSize(12).FontWeight(550).TextColor(ink).SingleLine()
					})
				}
			}).Row().Width(trackWidth).Shrink(0).Height(28).Gap(2).Radius(14).Background(t.SurfaceHover).
				Clip().Label(tx.Groups()).Draw(drawFilterFlow(t, a.filterFlow, c.Preferences().ReduceMotion))
			a.groupMenu(headerIconButton(c, headerMenuKey{a.searchSession, "more-groups"}, iconMore, tx.MoreGroups()), locale)
		}
		headerIconButton(c, headerMenuKey{a.searchSession, "new-group"}, iconPlus, tx.NewGroup()).
			OnClick(func() { a.editGroup("create", Group{}, 0, locale) })
		if width >= compactWidth {
			ui.Spacer(c)
		}
	})
}

func (a *app) groupMenu(button ui.Element, locale string) {
	tx := locales.In(locale)
	button.PopoverMenuButton(func(m *ui.Menu) {
		if m.Item(tx.All()).Radio(a.groupID == 0).Chosen() {
			a.selectGroup(0)
		}
		for _, group := range a.groups {
			if m.Item(group.label(locale)).Radio(a.groupID == group.ID).Chosen() {
				a.selectGroup(group.ID)
			}
		}
		m.Separator()
		if m.Item(tx.NewGroup()).Chosen() {
			a.editGroup("create", Group{}, 0, locale)
		}
		if m.Item(tx.RenameGroup()).Disabled(a.groupID == 0).Chosen() {
			a.editGroup("rename", a.currentGroup(), 0, locale)
		}
		if m.Item(tx.DeleteGroup()).Disabled(a.groupID <= defaultGroupID).Chosen() {
			a.editGroup("delete", a.currentGroup(), 0, locale)
		}
	}, ui.PopoverMenuButtonOptions{Open: &a.groupMenuOpen})
}

func (a *app) selectGroup(id int64) {
	a.groupID, a.chosen, a.scrollTo = id, 0, true
	a.cardMenuID, a.typeMenuOpen, a.groupMenuOpen = 0, false, false
	a.reload()
	a.input.Focus()
}

func (a *app) cycleGroup(backward bool) {
	index := 0
	for i, group := range a.groups {
		if group.ID == a.groupID {
			index = i + 1
			break
		}
	}
	step := 1
	if backward {
		step = -1
	}
	count := len(a.groups) + 1 // Include All before the stored groups.
	index = (index + step + count) % count
	id := int64(0)
	if index > 0 {
		id = a.groups[index-1].ID
	}
	a.selectGroup(id)
}

func (a *app) dismissGroupUI() {
	a.typeMenuOpen, a.groupMenuOpen, a.groupDialogOpen = false, false, false
	a.typeMovePending = false
	a.groupMode, a.groupName, a.groupError = "", "", ""
	a.groupEditID, a.groupClipID = 0, 0
	a.groupMembers = nil
}

func (a *app) editGroup(mode string, group Group, clipID int64, locale string) {
	a.cardMenuID, a.typeMenuOpen, a.groupMenuOpen = 0, false, false
	a.groupSession++
	a.groupMode, a.groupEditID, a.groupClipID = mode, group.ID, clipID
	a.groupName, a.groupError = "", ""
	if mode != "create" {
		a.groupName = group.label(locale)
	}
	a.groupDialogOpen = true
}

func (a *app) openClipGroups(i int) {
	if i < 0 || i >= len(a.clips) {
		return
	}
	a.openGroupAssignment(a.clips[i].ID)
}

func (a *app) openGroupAssignment(clipID int64) {
	ids, err := a.store.ClipGroupIDs(clipID)
	if err != nil {
		log.Print("listing clip groups: ", err)
		return
	}
	a.editGroup("assign", Group{}, clipID, "")
	a.groupMembers = make(map[int64]*bool, len(a.groups))
	for _, group := range a.groups {
		member := slices.Contains(ids, group.ID)
		a.groupMembers[group.ID] = &member
	}
}

func (a *app) saveGroup(locale string) {
	name := strings.TrimSpace(a.groupName)
	if !validGroupName(name) {
		a.groupError = "groupNameInvalid"
		return
	}
	if strings.EqualFold(name, locales.In(locale).All()) {
		a.groupError = "groupNameExists"
		return
	}
	for _, group := range a.groups {
		if group.ID != a.groupEditID && strings.EqualFold(name, group.label(locale)) {
			a.groupError = "groupNameExists"
			return
		}
	}
	id, err := a.store.SaveGroup(a.groupEditID, name)
	if err != nil {
		log.Print("saving group: ", err)
		a.groupError = "groupFailed"
		return
	}
	clipID := a.groupClipID
	if clipID > 0 {
		err = a.store.SetGroupMember(clipID, id, true)
	}
	a.reload()
	if clipID > 0 {
		a.openGroupAssignment(clipID)
		if err != nil {
			log.Print("adding clip to new group: ", err)
			a.groupError = "groupFailed"
		}
	} else {
		a.dismissGroupUI()
		a.selectGroup(id)
	}
}

func (a *app) deleteGroup() {
	if err := a.store.DeleteGroup(a.groupEditID); err != nil {
		log.Print("deleting group: ", err)
		a.groupError = "groupFailed"
		return
	}
	a.dismissGroupUI()
	a.selectGroup(0)
}

func (a *app) groupDialog(c *ui.Context) {
	t, tx := c.Theme(), locales.In(c.Locale())
	locale := c.Locale()
	ui.DialogBase(c.Key(headerMenuKey{a.groupSession, "group-dialog"}), &a.groupDialogOpen, func(backdrop, panel ui.Element) {
		width, height := c.Size()
		backdrop.Background(ui.RGBA(0, 0, 0, 0.4))
		popupSurface(c, panel).Column().Padding(18).Radius(12).Width(360).MaxWidth(width - 32).MaxHeight(height - 24).Gap(12)
		title, hint := tx.NewGroup(), tx.GroupNameHint()
		switch a.groupMode {
		case "rename":
			title = tx.RenameGroup()
		case "delete":
			title, hint = tx.DeleteGroup(), tx.DeleteGroupHint()
		case "assign":
			title, hint = tx.AddToGroups(), tx.GroupMembershipHint()
		}
		ui.Text(c, title).FontSize(16).FontWeight(650)
		ui.Text(c, hint).FontSize(12).TextColor(t.TextMuted)
		switch a.groupMode {
		case "create", "rename":
			input := ui.TextInputBase(c.Key(headerMenuKey{a.groupSession, "group-name"}), &a.groupName).
				AutoFocus().Height(36).PaddingX(10).Radius(8).Border(1, t.Border).
				Background(t.Background).FontSize(13).MaxLength(20).Label(tx.GroupName()).Placeholder(tx.GroupName()).
				OnSubmit(func() { a.saveGroup(locale) })
			if input.Focused() {
				input.Border(1, t.Accent)
			}
			input.PopoverMenu(func(m *ui.Menu) { m.EditItems() })
		case "delete":
			ui.Text(c, a.groupName).FontSize(13).FontWeight(600).SingleLine()
		case "assign":
			ui.Scroll(c.Key("group-members")).Height(132).Children(func() {
				for _, group := range a.groups {
					member := a.groupMembers[group.ID]
					if member == nil {
						continue
					}
					row := ui.CheckboxBase(c.Key(group.ID), member).OnChange(func() {
						if err := a.store.SetGroupMember(a.groupClipID, group.ID, *member); err != nil {
							*member = !*member
							log.Print("changing clip group: ", err)
							a.groupError = "groupFailed"
							return
						}
						a.groupError = ""
						a.reload()
					})
					detail := ""
					if *member {
						detail = "✓"
					}
					menuActionRow(c, row, iconFolder, group.label(locale), detail, t.TextMuted)
				}
			})
		}
		if a.groupError != "" {
			ui.Text(c, locales.Catalog.Format(locale, a.groupError, nil)).FontSize(12).TextColor(t.Danger)
		}
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			if a.groupMode == "assign" {
				if dialogButton(c, "create-from-clip", tx.NewGroup(), false).Clicked() {
					a.editGroup("create", Group{}, a.groupClipID, locale)
				}
			} else if dialogButton(c, "cancel-group", tx.Cancel(), false).Clicked() {
				a.dismissGroupUI()
			}
			ui.Spacer(c)
			label := tx.Save()
			if a.groupMode == "assign" {
				label = tx.Done()
			} else if a.groupMode == "delete" {
				label = tx.Delete()
			}
			button := dialogButton(c, "finish-group", label, true)
			if a.groupMode == "delete" {
				button.Background(t.Danger)
			}
			if button.Clicked() {
				switch a.groupMode {
				case "assign":
					a.dismissGroupUI()
				case "delete":
					a.deleteGroup()
				default:
					a.saveGroup(locale)
				}
			}
		})
	})
	if !a.groupDialogOpen {
		a.groupMembers = nil
		a.groupError = ""
	}
}
