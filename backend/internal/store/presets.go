package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type CredentialPreset struct {
	ID             int
	Name           string
	EncryptedBlob  []byte
	Note           string
	CreatedAt      int64
}

func (d *DB) CreatePreset(p CredentialPreset) (CredentialPreset, error) {
	if p.CreatedAt == 0 {
		p.CreatedAt = time.Now().Unix()
	}
	res, err := d.sql.Exec(
		`INSERT INTO credential_presets (name, encrypted_blob, note, created_at)
		 VALUES (?, ?, ?, ?)`,
		p.Name, p.EncryptedBlob, p.Note, p.CreatedAt,
	)
	if err != nil {
		return CredentialPreset{}, fmt.Errorf("create preset: %w", err)
	}
	id, _ := res.LastInsertId()
	p.ID = int(id)
	return p, nil
}

func (d *DB) GetPreset(id int) (CredentialPreset, error) {
	row := d.sql.QueryRow(
		`SELECT id, name, encrypted_blob, note, created_at
		 FROM credential_presets WHERE id = ?`, id,
	)
	var p CredentialPreset
	err := row.Scan(&p.ID, &p.Name, &p.EncryptedBlob, &p.Note, &p.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return CredentialPreset{}, ErrNotFound
	}
	if err != nil {
		return CredentialPreset{}, fmt.Errorf("get preset: %w", err)
	}
	return p, nil
}

func (d *DB) ListPresets() ([]CredentialPreset, error) {
	rows, err := d.sql.Query(
		`SELECT id, name, encrypted_blob, note, created_at
		 FROM credential_presets ORDER BY id`,
	)
	if err != nil {
		return nil, fmt.Errorf("list presets: %w", err)
	}
	defer rows.Close()
	var out []CredentialPreset
	for rows.Next() {
		var p CredentialPreset
		if err := rows.Scan(&p.ID, &p.Name, &p.EncryptedBlob, &p.Note, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan preset: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (d *DB) DeletePreset(id int) error {
	_, err := d.sql.Exec(`DELETE FROM credential_presets WHERE id = ?`, id)
	return err
}
