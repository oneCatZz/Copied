package main

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"copied/locales"

	"github.com/egoist/mygo/plugins/sqlite"
)

const defaultGroupID int64 = 1

type Group struct {
	ID   int64
	Name string
}

func (g Group) label(locale string) string {
	if g.ID == 0 {
		return locales.In(locale).All()
	}
	if g.ID == defaultGroupID && g.Name == "" {
		return locales.In(locale).Favorites()
	}
	return g.Name
}

func (s *Store) migrateGroups(ctx context.Context) error {
	version, err := s.db.Query(ctx, `PRAGMA user_version`)
	if err != nil {
		return err
	}
	if len(version.Values) > 0 && integer(version.Values[0][0]) >= 2 {
		return nil
	}
	_, err = s.db.Transaction(ctx, []sqlite.Statement{
		{SQL: `INSERT OR IGNORE INTO history_groups (id, name) VALUES (1, '')`},
		{SQL: `INSERT OR IGNORE INTO clip_groups (clip_id, group_id) SELECT id, 1 FROM clips WHERE pinned = 1`},
		{SQL: `PRAGMA user_version = 2`},
	})
	return err
}

func (s *Store) Groups() ([]Group, error) {
	rows, err := s.db.Query(context.Background(), `SELECT id, name FROM history_groups ORDER BY id`)
	if err != nil {
		return nil, err
	}
	groups := make([]Group, 0, len(rows.Values))
	for _, row := range rows.Values {
		groups = append(groups, Group{ID: integer(row[0]), Name: str(row[1])})
	}
	return groups, nil
}

func validGroupName(name string) bool {
	return name != "" && utf8.RuneCountInString(name) <= 20 && !strings.ContainsAny(name, "\r\n\t")
}

func (s *Store) SaveGroup(id int64, name string) (int64, error) {
	name = strings.TrimSpace(name)
	if !validGroupName(name) {
		return 0, errors.New("invalid group name")
	}
	if id == 0 {
		result, err := s.db.Execute(context.Background(), `INSERT INTO history_groups (name) VALUES (?)`, name)
		return result.LastInsertID, err
	}
	_, err := s.db.Execute(context.Background(), `UPDATE history_groups SET name = ? WHERE id = ?`, name, id)
	return id, err
}

func (s *Store) DeleteGroup(id int64) error {
	if id <= defaultGroupID {
		return errors.New("cannot delete the default group")
	}
	_, err := s.db.Execute(context.Background(), `DELETE FROM history_groups WHERE id = ?`, id)
	return err
}

func (s *Store) SetGroupMember(clipID, groupID int64, member bool) error {
	statement := sqlite.Statement{
		SQL: `DELETE FROM clip_groups WHERE clip_id = ? AND group_id = ?`, Args: []any{clipID, groupID},
	}
	if member {
		statement.SQL = `INSERT OR IGNORE INTO clip_groups (clip_id, group_id)
			SELECT id, ? FROM clips WHERE id = ?`
		statement.Args = []any{groupID, clipID}
	}
	statements := []sqlite.Statement{statement}
	if groupID == defaultGroupID {
		flag := 0
		if member {
			flag = 1
		}
		statements = append(statements, sqlite.Statement{
			SQL: `UPDATE clips SET pinned = ? WHERE id = ?`, Args: []any{flag, clipID},
		})
	}
	_, err := s.db.Transaction(context.Background(), statements)
	return err
}

func (s *Store) ClipGroupIDs(clipID int64) ([]int64, error) {
	rows, err := s.db.Query(context.Background(), `SELECT group_id FROM clip_groups WHERE clip_id = ? ORDER BY group_id`, clipID)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(rows.Values))
	for _, row := range rows.Values {
		ids = append(ids, integer(row[0]))
	}
	return ids, nil
}
