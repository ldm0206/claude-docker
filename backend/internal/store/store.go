package store

import (
	"database/sql"
	_ "embed"
	"fmt"

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
	return &DB{sql: sq}, nil
}

func (d *DB) Sqlite() *sql.DB { return d.sql }
func (d *DB) Close() error    { return d.sql.Close() }
