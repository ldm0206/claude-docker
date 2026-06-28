package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type RoleTemplate struct {
	ID              int
	Name            string
	DiskQuotaBytes  int64
	CPUQuota        string
	MemoryMaxBytes  int64
	MaxSessions     int
	Permissions     string
	CreatedAt       int64
}

func (d *DB) CreateTemplate(t RoleTemplate) (RoleTemplate, error) {
	if t.CreatedAt == 0 {
		t.CreatedAt = time.Now().Unix()
	}
	res, err := d.sql.Exec(
		`INSERT INTO role_templates (name, disk_quota_bytes, cpu_quota, memory_max_bytes, max_sessions, permissions, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		t.Name, t.DiskQuotaBytes, t.CPUQuota, t.MemoryMaxBytes, t.MaxSessions, t.Permissions, t.CreatedAt,
	)
	if err != nil {
		return RoleTemplate{}, fmt.Errorf("create template: %w", err)
	}
	id, _ := res.LastInsertId()
	t.ID = int(id)
	return t, nil
}

func (d *DB) GetTemplate(id int) (RoleTemplate, error) {
	row := d.sql.QueryRow(
		`SELECT id, name, disk_quota_bytes, cpu_quota, memory_max_bytes, max_sessions, permissions, created_at
		 FROM role_templates WHERE id = ?`, id,
	)
	var t RoleTemplate
	err := row.Scan(&t.ID, &t.Name, &t.DiskQuotaBytes, &t.CPUQuota, &t.MemoryMaxBytes, &t.MaxSessions, &t.Permissions, &t.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return RoleTemplate{}, ErrNotFound
	}
	if err != nil {
		return RoleTemplate{}, fmt.Errorf("get template: %w", err)
	}
	return t, nil
}

func (d *DB) ListTemplates() ([]RoleTemplate, error) {
	rows, err := d.sql.Query(
		`SELECT id, name, disk_quota_bytes, cpu_quota, memory_max_bytes, max_sessions, permissions, created_at
		 FROM role_templates ORDER BY id`,
	)
	if err != nil {
		return nil, fmt.Errorf("list templates: %w", err)
	}
	defer rows.Close()
	var out []RoleTemplate
	for rows.Next() {
		var t RoleTemplate
		if err := rows.Scan(&t.ID, &t.Name, &t.DiskQuotaBytes, &t.CPUQuota, &t.MemoryMaxBytes, &t.MaxSessions, &t.Permissions, &t.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan template: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (d *DB) DeleteTemplate(id int) error {
	_, err := d.sql.Exec(`DELETE FROM role_templates WHERE id = ?`, id)
	return err
}
