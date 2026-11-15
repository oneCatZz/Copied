package main

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/sqlite"
)

const (
	listLimit    = 200  // clips the panel shows at most
	historyLimit = 1000 // clips kept, besides the pinned ones
	previewRunes = 600  // of a text, on its card
)

// Filter chooses the clips the panel lists.
type Filter int

const (
	FilterAll Filter = iota
	FilterText
	FilterCode
	FilterLink
	FilterColor
	FilterImage
	FilterFiles
	FilterPinned
)

// Store keeps the history in a SQLite database of the user's data
// directory. It is safe from any goroutine.
type Store struct{ db *sqlite.DB }

func openStore() (*Store, error) {
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		return nil, err
	}
	return openStoreAt(filepath.Join(dir, "history.db"))
}

func openStoreAt(path string) (*Store, error) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, path, sqlite.OpenOptions{})
	if err != nil {
		return nil, err
	}
	for _, sql := range []string{
		`CREATE TABLE IF NOT EXISTS clips (
			id INTEGER PRIMARY KEY,
			kind TEXT NOT NULL,
			text TEXT NOT NULL DEFAULT '',
			html TEXT NOT NULL DEFAULT '',
			data BLOB,
			thumb BLOB,
			width INTEGER NOT NULL DEFAULT 0,
			height INTEGER NOT NULL DEFAULT 0,
			length INTEGER NOT NULL DEFAULT 0,
			hash TEXT NOT NULL UNIQUE,
			pinned INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			used_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS clips_used_at ON clips (used_at DESC)`,
		`CREATE TABLE IF NOT EXISTS history_groups (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL COLLATE NOCASE UNIQUE
		)`,
		`CREATE TABLE IF NOT EXISTS clip_groups (
			clip_id INTEGER NOT NULL REFERENCES clips(id) ON DELETE CASCADE,
			group_id INTEGER NOT NULL REFERENCES history_groups(id) ON DELETE CASCADE,
			PRIMARY KEY (clip_id, group_id)
		)`,
		`CREATE INDEX IF NOT EXISTS clip_groups_group ON clip_groups (group_id, clip_id)`,
	} {
		if _, err := db.Execute(ctx, sql); err != nil {
			db.Close()
			return nil, err
		}
	}
	s := &Store{db}
	if err := s.migrateCodeClips(ctx); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.migrateGroups(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Existing text clips are classified once, without changing their content,
// IDs, hashes, saved status or order. The version advances atomically.
func (s *Store) migrateCodeClips(ctx context.Context) error {
	version, err := s.db.Query(ctx, `PRAGMA user_version`)
	if err != nil {
		return err
	}
	if len(version.Values) > 0 && integer(version.Values[0][0]) >= 1 {
		return nil
	}
	var statements []sqlite.Statement
	var lastID int64
	for {
		rows, err := s.db.Query(ctx, `SELECT id, substr(text, 1, ?) FROM clips
			WHERE kind = 'text' AND id > ? ORDER BY id LIMIT 64`, codeDetectLimit, lastID)
		if err != nil {
			return err
		}
		if len(rows.Values) == 0 {
			break
		}
		for _, row := range rows.Values {
			lastID = integer(row[0])
			if textKind(str(row[1])) == KindCode {
				statements = append(statements, sqlite.Statement{
					SQL: `UPDATE clips SET kind = 'code' WHERE id = ?`, Args: []any{lastID},
				})
			}
		}
	}
	statements = append(statements, sqlite.Statement{SQL: `PRAGMA user_version = 1`})
	_, err = s.db.Transaction(ctx, statements)
	return err
}

func (s *Store) Close() error { return s.db.Close() }

// Add records a clip, or moves the one with the same content to the front.
func (s *Store) Add(c *Clip) error {
	ctx := context.Background()
	now := time.Now().UnixMilli()
	_, err := s.db.Execute(ctx, `INSERT INTO clips (kind, text, html, data, thumb, width, height, length, hash, created_at, used_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (hash) DO UPDATE SET
			kind = excluded.kind,
			used_at = excluded.used_at,
			html = CASE WHEN excluded.html <> '' THEN excluded.html ELSE html END`,
		string(c.Kind), c.Text, c.HTML, c.Data, c.Thumb, c.Width, c.Height, c.Length, c.Hash, now, now)
	if err != nil {
		return err
	}
	_, err = s.db.Execute(ctx, `DELETE FROM clips WHERE pinned = 0
		AND NOT EXISTS (SELECT 1 FROM clip_groups WHERE clip_id = clips.id) AND id NOT IN (
		SELECT id FROM clips WHERE pinned = 0
		AND NOT EXISTS (SELECT 1 FROM clip_groups WHERE clip_id = clips.id)
		ORDER BY used_at DESC LIMIT ?)`, historyLimit)
	return err
}

// List returns the newest clips matching a filter and the text searched
// for, with a preview of each: the start of its text; Thumb reads its picture.
func (s *Store) List(filter Filter, query string, groupID ...int64) ([]*Clip, error) {
	where, args := []string{"1 = 1"}, []any{previewRunes}
	switch filter {
	case FilterText:
		where = append(where, "kind IN ('text', 'color')")
	case FilterCode:
		where = append(where, "kind = 'code'")
	case FilterLink:
		where = append(where, "kind = 'link'")
	case FilterColor:
		where = append(where, "kind = 'color'")
	case FilterImage:
		where = append(where, "kind = 'image'")
	case FilterFiles:
		where = append(where, "kind = 'files'")
	case FilterPinned:
		where = append(where, "pinned = 1")
	}
	if len(groupID) > 0 && groupID[0] != 0 {
		where = append(where, "EXISTS (SELECT 1 FROM clip_groups WHERE clip_id = clips.id AND group_id = ?)")
		args = append(args, groupID[0])
	}
	if q := strings.TrimSpace(query); q != "" {
		where = append(where, `text LIKE ? ESCAPE '\'`)
		q = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
		args = append(args, "%"+q+"%")
	}
	args = append(args, listLimit)
	rows, err := s.db.Query(context.Background(), `SELECT id, kind, substr(text, 1, ?), width, height, length, pinned, used_at,
		(SELECT group_concat(group_id) FROM clip_groups WHERE clip_id = clips.id)
		FROM clips WHERE `+strings.Join(where, " AND ")+` ORDER BY used_at DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	clips := make([]*Clip, 0, len(rows.Values))
	for _, v := range rows.Values {
		c := &Clip{
			ID:     integer(v[0]),
			Kind:   Kind(str(v[1])),
			Text:   str(v[2]),
			Width:  int(integer(v[3])),
			Height: int(integer(v[4])),
			Length: int(integer(v[5])),
			Pinned: integer(v[6]) != 0,
			UsedAt: time.UnixMilli(integer(v[7])),
		}
		for _, value := range strings.Split(str(v[8]), ",") {
			if id, err := strconv.ParseInt(value, 10, 64); err == nil {
				c.GroupIDs = append(c.GroupIDs, id)
			}
		}
		clips = append(clips, c)
	}
	return clips, nil
}

// Get returns a whole clip, to put on the clipboard. It is nil when the
// clip is gone.
func (s *Store) Get(id int64) (*Clip, error) {
	rows, err := s.db.Query(context.Background(), `SELECT kind, text, html, data FROM clips WHERE id = ?`, id)
	if err != nil || len(rows.Values) == 0 {
		return nil, err
	}
	v := rows.Values[0]
	c := &Clip{ID: id, Kind: Kind(str(v[0])), Text: str(v[1]), HTML: str(v[2])}
	c.Data, _ = v[3].([]byte)
	return c, nil
}

// Has reports whether a clip is still in the history.
func (s *Store) Has(id int64) bool {
	rows, err := s.db.Query(context.Background(), `SELECT 1 FROM clips WHERE id = ?`, id)
	return err == nil && len(rows.Values) > 0
}

// Thumb returns the small picture of an image clip, a PNG.
func (s *Store) Thumb(id int64) []byte {
	rows, err := s.db.Query(context.Background(), `SELECT thumb FROM clips WHERE id = ?`, id)
	if err != nil || len(rows.Values) == 0 {
		return nil
	}
	data, _ := rows.Values[0][0].([]byte)
	return data
}

// Touch moves a clip to the front, as it is used.
func (s *Store) Touch(id int64) error {
	_, err := s.db.Execute(context.Background(), `UPDATE clips SET used_at = ? WHERE id = ?`, time.Now().UnixMilli(), id)
	return err
}

func (s *Store) SetPinned(id int64, pinned bool) error {
	return s.SetGroupMember(id, defaultGroupID, pinned)
}

func (s *Store) Delete(id int64) error {
	_, err := s.db.Execute(context.Background(), `DELETE FROM clips WHERE id = ?`, id)
	return err
}

// Clear keeps every clip organized into a group, including legacy favorites.
func (s *Store) Clear() error {
	_, err := s.db.Execute(context.Background(), `DELETE FROM clips WHERE pinned = 0
		AND NOT EXISTS (SELECT 1 FROM clip_groups WHERE clip_id = clips.id)`)
	return err
}

func integer(v any) int64 {
	n, _ := v.(int64)
	return n
}

func str(v any) string {
	s, _ := v.(string)
	return s
}
