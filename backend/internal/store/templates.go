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

// UpdateTemplate patches an existing role_template. Every field is optional: a
// nil pointer leaves the column unchanged, a non-nil pointer updates it. This
// matches the PATCH semantics used by the admin handler (partial update).
func (d *DB) UpdateTemplate(id int, patch TemplatePatch) error {
	existing, err := d.GetTemplate(id)
	if err != nil {
		return err
	}
	if patch.Name != nil {
		existing.Name = *patch.Name
	}
	if patch.DiskQuotaBytes != nil {
		existing.DiskQuotaBytes = *patch.DiskQuotaBytes
	}
	if patch.CPUQuota != nil {
		existing.CPUQuota = *patch.CPUQuota
	}
	if patch.MemoryMaxBytes != nil {
		existing.MemoryMaxBytes = *patch.MemoryMaxBytes
	}
	if patch.MaxSessions != nil {
		existing.MaxSessions = *patch.MaxSessions
	}
	if patch.Permissions != nil {
		existing.Permissions = *patch.Permissions
	}
	_, err = d.sql.Exec(
		`UPDATE role_templates
		 SET name = ?, disk_quota_bytes = ?, cpu_quota = ?, memory_max_bytes = ?, max_sessions = ?, permissions = ?
		 WHERE id = ?`,
		existing.Name, existing.DiskQuotaBytes, existing.CPUQuota, existing.MemoryMaxBytes, existing.MaxSessions, existing.Permissions, id,
	)
	return err
}

// TemplatePatch is the partial-update payload for UpdateTemplate.
type TemplatePatch struct {
	Name            *string
	DiskQuotaBytes  *int64
	CPUQuota        *string
	MemoryMaxBytes  *int64
	MaxSessions     *int
	Permissions     *string
}
