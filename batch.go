package main

import (
	"log"
	"slices"
	"strings"
	"time"

	"copied/locales"

	"github.com/egoist/mygo/ui"
)

// pickOrder is a clip's place among the clips picked together, from 1, or
// 0 when it is not picked.
func (a *app) pickOrder(id int64) int {
	return slices.Index(a.picked, id) + 1
}

// batchIDs returns the clips an action on card i takes together: the clips
// picked, when there are several and the card is one of them.
func (a *app) batchIDs(i int) []int64 {
	if len(a.picked) < 2 || i < 0 || i >= len(a.clips) || a.pickOrder(a.clips[i].ID) == 0 {
		return nil
	}
	return a.picked
}

// clickCard chooses card i as clicked: Cmd picks it or lets it go, Shift
// picks the cards from the chosen one to it, and a plain click picks none.
func (a *app) clickCard(i int, mods ui.Modifiers) {
	if i < 0 || i >= len(a.clips) {
		return
	}
	if mods != ui.Shift {
		a.pickRange = nil
	}
	switch mods {
	case ui.Cmd:
		if len(a.picked) == 0 && i != a.chosen {
			a.pick(a.chosen) // the card chosen before starts the batch
		}
		id := a.clips[i].ID
		if n := slices.Index(a.picked, id); n >= 0 {
			a.picked = slices.Delete(a.picked, n, n+1)
			if len(a.picked) > 0 {
				// Enter acts on the batch, so the chosen card stays in it.
				i = slices.IndexFunc(a.clips, func(c *Clip) bool { return c.ID == a.picked[len(a.picked)-1] })
			}
		} else {
			a.picked = append(a.picked, id)
		}
	case ui.Shift:
		a.pickThrough(a.chosen, i)
	default:
		a.picked = nil
	}
	a.chosen = max(0, i)
}

type pickRange struct {
	anchor int
	tip    int
	base   []int64 // the picks made before starting this range
}

// extendPick moves the end of the range, leaving its starting card in place.
func (a *app) extendPick(step int) {
	from := a.chosen
	a.choose(from + step)
	if a.chosen != from {
		a.pickThrough(from, a.chosen)
	}
}

func (a *app) pickThrough(from, to int) {
	r := a.pickRange
	if r == nil || r.tip != from || len(a.picked) == 0 {
		r = &pickRange{anchor: from, base: slices.Clone(a.picked)}
		a.pickRange = r
	}
	r.tip = to
	a.picked = append(a.picked[:0], r.base...)
	step := 1
	if to < r.anchor {
		step = -1
	}
	for i := r.anchor; i != to; i += step {
		a.pick(i)
	}
	a.pick(to)
	if len(a.picked) == 1 {
		a.picked, a.pickRange = nil, nil
	}
}

// pick adds card i to the batch, unless it is there already.
func (a *app) pick(i int) {
	if i >= 0 && i < len(a.clips) && a.pickOrder(a.clips[i].ID) == 0 {
		a.picked = append(a.picked, a.clips[i].ID)
	}
}

// batchPasteWrites keeps text together and sends native clipboard data for
// attachments in selection order. Each image needs its own paste event.
func (a *app) batchPasteWrites(ids []int64, target uintptr, plain bool) []func() bool {
	kinds := make(map[int64]Kind, len(a.clips))
	for _, clip := range a.clips {
		kinds[clip.ID] = clip.Kind
	}
	var texts, files []int64
	writes := make([]func() bool, 0, len(ids))
	writeText := func() bool { return a.copyBatch(texts, plain) }
	flushFiles := func() {
		if len(files) > 0 {
			group := files
			writes = append(writes, func() bool { return a.copyBatch(group, plain) })
			files = nil
		}
	}
	for _, id := range ids {
		switch kinds[id] {
		case KindImage:
			flushFiles()
			writes = append(writes, func() bool { return a.copyForPaste(id, KindImage, target, plain) })
		case KindFiles:
			files = append(files, id)
		default:
			texts = append(texts, id)
		}
	}
	flushFiles()
	if len(texts) > 0 {
		return slices.Insert(writes, 0, writeText)
	}
	return writes
}

// copyBatch puts the clips with IDs on the clipboard as one, in turn, and
// moves them to the front of the history.
func (a *app) copyBatch(ids []int64, plain bool) bool {
	clips := make([]*Clip, 0, len(ids))
	for _, id := range ids {
		clip, err := a.store.Get(id)
		if err != nil {
			log.Print("reading clip: ", err)
		}
		if clip != nil {
			clips = append(clips, clip)
		}
	}
	merged := mergeClips(clips)
	if merged == nil {
		return false
	}
	a.ownUntil.Store(time.Now().Add(ownWrite).UnixMilli())
	if err := writeClipboard(merged, plain); err != nil {
		log.Print("writing the clipboard: ", err)
		return false
	}
	for _, id := range slices.Backward(ids) {
		if err := a.store.Touch(id); err != nil {
			log.Print(err)
		}
	}
	return true
}

// mergeClips makes one clip of several, to copy them at once: the paths
// of files when all of them are files, or else their texts one per line,
// the paths of files among them. Pictures cannot join a text and are left
// out; it returns nil when nothing remains.
func mergeClips(clips []*Clip) *Clip {
	var texts, files []string
	allFiles := true
	for _, c := range clips {
		switch c.Kind {
		case KindImage:
			allFiles = false
			continue
		case KindFiles:
			for _, f := range c.Files() {
				if !slices.Contains(files, f) {
					files = append(files, f)
				}
			}
		default:
			allFiles = false
		}
		if text := strings.TrimRight(c.Text, "\r\n"); text != "" {
			texts = append(texts, text)
		}
	}
	if allFiles && len(files) > 0 {
		text := strings.Join(files, "\n")
		return &Clip{Kind: KindFiles, Text: text, Length: len(files)}
	}
	if len(texts) == 0 {
		return nil
	}
	text := strings.Join(texts, "\n")
	return &Clip{Kind: KindText, Text: text, Length: len([]rune(text))}
}

// batchHints replace the footer's hints while several clips are picked.
func (a *app) batchHints(c *ui.Context, hint func(keys []string, label string)) {
	t, tx := c.Theme(), locales.In(c.Locale())
	cmd := cmdCap()
	ui.Row(c).Height(24).PaddingX(9).Gap(6).AlignItems(ui.Center).Radius(12).
		Background(t.Accent.Alpha(0.1)).Children(func() {
		ui.Icon(c, filterCheckIcon).FontSize(12).TextColor(t.Accent)
		ui.Text(c, tx.PickedCount(len(a.picked))).FontSize(11).FontWeight(600).TextColor(t.Accent).SingleLine()
	})
	hint([]string{"↵"}, tx.PasteAll())
	hint([]string{cmd, "↵"}, tx.CopyAll())
	hint([]string{cmd, "S"}, tx.AddToStack())
	hint([]string{"esc"}, tx.ClearSelection())
}
