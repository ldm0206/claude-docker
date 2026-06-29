package store

import (
	"database/sql"
	_ "embed"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string
type DB struct {
	sql *sql.DB
}

func Open(path string) (*DB, error) {
	sq, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}
	if _, err := sq.Exec(schemaSQL); err != nil {
		sq.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}

	for _, alter := range []string{
		`ALTER TABLE users ADD COLUMN last_login_ip TEXT`,
		`ALTER TABLE sessions ADD COLUMN client_ip TEXT`,
	} {
		if _, err := sq.Exec(alter); err != nil {
			if !strings.Contains(err.Error(), "duplicate column") {
				sq.Close()
				return nil, fmt.Errorf("migrate %q: %w", alter, err)
			}
		}
	}
	return &DB{sql: sq}, nil
}

func (d *DB) Sqlite() *sql.DB { return d.sql }
func (d *DB) Close() error    { return d.sql.Close() }
