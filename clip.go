package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/gif"  // picture files
	_ "image/jpeg" // picture files
	"image/png"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"copied/locales"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	_ "golang.org/x/image/bmp" // picture files
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // picture files
)

// Kind is what a clip holds.
type Kind string

const (
	KindText  Kind = "text"
	KindCode  Kind = "code"
	KindLink  Kind = "link"
	KindColor Kind = "color"
	KindImage Kind = "image"
	KindFiles Kind = "files"
)

// Clip is one entry of the clipboard history. The list keeps a preview of
// it: Text is cut short and Data, the full PNG of an image, stays in the
// database until the clip is pasted.
type Clip struct {
	ID     int64
	Kind   Kind
	Text   string // the text, or the paths of files, one per line
	HTML   string
	Data   []byte // the PNG of an image
	Thumb  []byte // a small PNG of it, for the card
	Width  int    // of an image, in pixels
	Height int
	Length int // of the whole text, in characters
	Hash   string
	Pinned bool
	UsedAt time.Time
	Source string // the name of the app it was copied from, when known

	GroupIDs []int64
	code     *codePreview // bounded, lazily highlighted preview; never persisted
}

// Files returns the paths of a clip of files.
func (c *Clip) Files() []string {
	if c.Kind != KindFiles || c.Text == "" {
		return nil
	}
	return strings.Split(c.Text, "\n")
}

// imageFile returns the path of the one picture a clip of files holds, for
// the preview to show it, or "" for anything else.
func (c *Clip) imageFile() string {
	files := c.Files()
	if len(files) != 1 {
		return ""
	}
	if !isImagePath(files[0]) {
		return ""
	}
	return files[0]
}

// isImagePath reports whether a file is a picture by its extension.
func isImagePath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".bmp", ".webp":
		return true
	}
	return false
}

const thumbSize = 520 // pixels, the longer side of a card's picture

var colorPattern = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$`)

// Formats by which password managers mark what should not be recorded.
var concealedFormats = []string{
	"org.nspasteboard.ConcealedType",
	"org.nspasteboard.TransientType",
	"com.agilebits.onepassword",
	"ExcludeClipboardContentFromMonitorProcessing",
	"Clipboard Viewer Ignore",
}

// readClipboard makes a clip of what is on the clipboard, or returns nil
// when there is nothing to keep. It reads files first, then text, then a
// picture: a spreadsheet's cells come with a picture of them, but are text.
func readClipboard() *Clip {
	for _, f := range mygo.Clipboard.AvailableFormats() {
		for _, concealed := range concealedFormats {
			if strings.EqualFold(f, concealed) {
				return nil
			}
		}
	}
	formats := mygo.Clipboard.Formats()
	if slices.Contains(formats, transfer.FileList) || slices.Contains(formats, transfer.URIList) {
		if files, err := mygo.Clipboard.ReadFiles(); err == nil && len(files) > 0 {
			// One picture, as WeChat copies it, is kept as the picture: the
			// file may go before it is pasted.
			if len(files) == 1 && isImagePath(files[0]) {
				if c := imageFileClip(files[0]); c != nil {
					return c
				}
			}
			text := strings.Join(files, "\n")
			return &Clip{Kind: KindFiles, Text: text, Length: len(files), Hash: hashOf(KindFiles, []byte(text))}
		}
	}
	if text := mygo.Clipboard.ReadText(); strings.TrimSpace(text) != "" {
		c := &Clip{Kind: textKind(text), Text: text, Length: len([]rune(text))}
		if slices.Contains(formats, transfer.HTML) {
			c.HTML = mygo.Clipboard.ReadHTML()
		}
		// Links and colors are the same clip however they were copied.
		c.Hash = hashOf(KindText, []byte(text))
		return c
	}
	if slices.Contains(formats, transfer.PNG) {
		if data := mygo.Clipboard.ReadImage(); len(data) > 0 {
			return imageClip(data)
		}
	}
	return nil
}

func textKind(text string) Kind {
	s := strings.TrimSpace(text)
	if colorPattern.MatchString(s) {
		return KindColor
	}
	if !strings.ContainsAny(s, " \t\r\n") {
		if u, err := url.Parse(s); err == nil && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https") {
			return KindLink
		}
	}
	if _, _, ok := detectCode(text); ok {
		return KindCode
	}
	return KindText
}

func imageClip(data []byte) *Clip {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	return imageClipOf(img, data)
}

// lastImageFile is the clip the picture file last on the clipboard made,
// so that watching the clipboard does not decode it again.
var lastImageFile struct {
	path    string
	size    int64
	modTime time.Time
	clip    *Clip
}

// imageFileClip makes a clip of a picture file, as a PNG, or returns nil
// when it does not read or decode.
func imageFileClip(path string) *Clip {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > imageFileLimit {
		return nil
	}
	last := &lastImageFile
	if last.clip != nil && last.path == path && last.size == info.Size() && last.modTime.Equal(info.ModTime()) {
		c := *last.clip
		return &c
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	if format != "png" {
		var buf bytes.Buffer
		if png.Encode(&buf, img) != nil {
			return nil
		}
		data = buf.Bytes()
	}
	c := imageClipOf(img, data)
	last.path, last.size, last.modTime, last.clip = path, info.Size(), info.ModTime(), c
	copied := *c
	return &copied
}

// imageClipOf makes a clip of a decoded picture and its PNG.
func imageClipOf(img image.Image, data []byte) *Clip {
	b := img.Bounds()
	c := &Clip{Kind: KindImage, Data: data, Width: b.Dx(), Height: b.Dy(), Hash: hashOf(KindImage, data)}
	c.Thumb = data
	if longer := max(b.Dx(), b.Dy()); longer > thumbSize {
		w, h := b.Dx()*thumbSize/longer, b.Dy()*thumbSize/longer
		small := image.NewRGBA(image.Rect(0, 0, max(w, 1), max(h, 1)))
		draw.ApproxBiLinear.Scale(small, small.Bounds(), img, b, draw.Src, nil)
		var buf bytes.Buffer
		if png.Encode(&buf, small) == nil {
			c.Thumb = buf.Bytes()
		}
	}
	return c
}

func hashOf(kind Kind, data []byte) string {
	h := sha256.New()
	h.Write([]byte(kind))
	h.Write([]byte{0})
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

// writeClipboard puts a whole clip (see Store.Get) on the clipboard. Plain
// leaves out the formatting of text.
func writeClipboard(c *Clip, plain bool) error {
	switch c.Kind {
	case KindImage:
		return writeImage(c.Data)
	case KindFiles:
		return mygo.Clipboard.WriteFiles(c.Files()...)
	}
	reps := []transfer.Representation{transfer.Bytes(transfer.Text, []byte(c.Text))}
	if c.HTML != "" && !plain {
		reps = append(reps, transfer.Bytes(transfer.HTML, []byte(c.HTML)))
	}
	return mygo.Clipboard.Write(transfer.New(transfer.NewItem(reps...)))
}

// detail is the line a card's footer shows.
func (c *Clip) detail(locale string) string {
	tx := locales.In(locale)
	switch c.Kind {
	case KindImage:
		return fmt.Sprintf("%d × %d", c.Width, c.Height)
	case KindFiles:
		return tx.FileCount(c.Length)
	case KindLink:
		if u, err := url.Parse(strings.TrimSpace(c.Text)); err == nil {
			return u.Host
		}
	case KindColor:
		return tx.Color()
	case KindCode:
		if language := c.codePreview().language; language != "" {
			return language + " · " + tx.CharacterCount(c.Length)
		}
	}
	return tx.CharacterCount(c.Length)
}

func kindLabel(k Kind, locale string) string {
	tx := locales.In(locale)
	switch k {
	case KindCode:
		return tx.Code()
	case KindLink:
		return tx.Link()
	case KindColor:
		return tx.Color()
	case KindImage:
		return tx.Image()
	case KindFiles:
		return tx.Files()
	}
	return tx.Text()
}

// ago says how long ago t was, shortly.
func ago(t time.Time, locale string) string {
	tx := locales.In(locale)
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return tx.JustNow()
	case d < time.Hour:
		return tx.MinutesAgo(int(d.Minutes()))
	case d < 24*time.Hour:
		return tx.HoursAgo(int(d.Hours()))
	case d < 48*time.Hour:
		return tx.Yesterday()
	case d < 30*24*time.Hour:
		return tx.DaysAgo(int(d.Hours() / 24))
	}
	return t.Format("2006-01-02")
}

func baseNames(paths []string) []string {
	names := make([]string, len(paths))
	for i, p := range paths {
		names[i] = filepath.Base(p)
	}
	return names
}
