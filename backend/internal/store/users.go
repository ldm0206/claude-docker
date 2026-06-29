package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type User struct {
	ID                 int
	UID                int
	Username           string
	PasswordHash       string
	Role               string
	MustChangePassword bool
	RoleTemplateID     sql.NullInt64
	CredentialPresetID sql.NullInt64
	Suspended          bool
	DiskQuotaBytes     sql.NullInt64
	MaxSessions        sql.NullInt64
	CreatedAt          int64
	LastLoginAt        sql.NullInt64
	LastLoginIP        string
}

var ErrNotFound = errors.New("not found")

func (d *DB) AllocateUID() (int, error) {
	var maxUID sql.NullInt64
	if err := d.sql.QueryRow("SELECT MAX(uid) FROM users").Scan(&maxUID); err != nil {
		return 0, fmt.Errorf("allocate uid: %w", err)
	}
	if !maxUID.Valid {
		return 2000, nil
	}
	return int(maxUID.Int64) + 1, nil
}

func (d *DB) CreateUser(u User) (User, error) {
	if u.CreatedAt == 0 {
		u.CreatedAt = time.Now().Unix()
	}
	res, err := d.sql.Exec(
		`INSERT INTO users (uid, username, password_hash, role, must_change_password, suspended, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		u.UID, u.Username, u.PasswordHash, u.Role, btoi(u.MustChangePassword), btoi(u.Suspended), u.CreatedAt)
	if err != nil {
		return User{}, fmt.Errorf("create user: %w", err)
	}
	id, _ := res.LastInsertId()
	u.ID = int(id)
	return u, nil
}

func (d *DB) GetUserByUsername(name string) (User, error) {
	row := d.sql.QueryRow(`SELECT id, uid, username, password_hash, role, must_change_password, suspended, role_template_id, credential_preset_id, disk_quota_bytes, max_sessions, created_at, last_login_at, COALESCE(last_login_ip, '') AS last_login_ip FROM users WHERE username = ?`, name)
	return scanUser(row)
}

func (d *DB) GetUserByID(id int) (User, error) {
	row := d.sql.QueryRow(`SELECT id, uid, username, password_hash, role, must_change_password, suspended, role_template_id, credential_preset_id, disk_quota_bytes, max_sessions, created_at, last_login_at, COALESCE(last_login_ip, '') AS last_login_ip FROM users WHERE id = ?`, id)
	return scanUser(row)
}

func (d *DB) SetPassword(id int, hash string) error {
	_, err := d.sql.Exec(`UPDATE users SET password_hash = ?, must_change_password = 0 WHERE id = ?`, hash, id)
	return err
}

func (d *DB) ListUsers() ([]User, error) {
	rows, err := d.sql.Query(`SELECT id, uid, username, password_hash, role, must_change_password, suspended, role_template_id, credential_preset_id, disk_quota_bytes, max_sessions, created_at, last_login_at, COALESCE(last_login_ip, '') AS last_login_ip FROM users ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	var users []User
	for rows.Next() {
		var u User
		var mcp, sus int
		if err := rows.Scan(&u.ID, &u.UID, &u.Username, &u.PasswordHash, &u.Role, &mcp, &sus, &u.RoleTemplateID, &u.CredentialPresetID, &u.DiskQuotaBytes, &u.MaxSessions, &u.CreatedAt, &u.LastLoginAt, &u.LastLoginIP); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		u.MustChangePassword = mcp == 1
		u.Suspended = sus == 1
		users = append(users, u)
	}
	return users, rows.Err()
}

func (d *DB) SetSuspended(id int, suspended bool) error {
	_, err := d.sql.Exec(`UPDATE users SET suspended = ? WHERE id = ?`, btoi(suspended), id)
	return err
}

func (d *DB) TouchLogin(id int, ts int64, ip string) error {
	_, err := d.sql.Exec(`UPDATE users SET last_login_at = ?, last_login_ip = ? WHERE id = ?`, ts, ip, id)
	return err
}

func (d *DB) BindCredential(userID, presetID int) error {
	_, err := d.sql.Exec(`UPDATE users SET credential_preset_id = ? WHERE id = ?`, presetID, userID)
	return err
}

func (d *DB) BindTemplate(userID, templateID int) error {
	_, err := d.sql.Exec(`UPDATE users SET role_template_id = ? WHERE id = ?`, templateID, userID)
	return err
}

func (d *DB) SetUserMaxSessions(userID int, max int) error {
	_, err := d.sql.Exec(`UPDATE users SET max_sessions = ? WHERE id = ?`, max, userID)
	return err
}

func (d *DB) SetUserDiskQuota(userID int, quota int64) error {
	_, err := d.sql.Exec(`UPDATE users SET disk_quota_bytes = ? WHERE id = ?`, quota, userID)
	return err
}

func (d *DB) EffectiveMaxSessions(userID int) (int, error) {
	var maxSessions sql.NullInt64
	err := d.sql.QueryRow(`SELECT max_sessions FROM users WHERE id = ?`, userID).Scan(&maxSessions)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("effective max sessions: %w", err)
	}
	if maxSessions.Valid {
		return int(maxSessions.Int64), nil
	}
	// No user override — check bound template
	var tmplMax sql.NullInt64
	err = d.sql.QueryRow(
		`SELECT rt.max_sessions FROM users u
		 JOIN role_templates rt ON rt.id = u.role_template_id
		 WHERE u.id = ?`, userID,
	).Scan(&tmplMax)
	if err == nil && tmplMax.Valid {
		return int(tmplMax.Int64), nil
	}
	// Default
	return 3, nil
}

func (d *DB) EffectiveDiskQuota(userID int) (int64, error) {
	var diskQuota sql.NullInt64
	err := d.sql.QueryRow(`SELECT disk_quota_bytes FROM users WHERE id = ?`, userID).Scan(&diskQuota)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("effective disk quota: %w", err)
	}
	if diskQuota.Valid {
		return diskQuota.Int64, nil
	}
	// No user override — check bound template
	var tmplQuota sql.NullInt64
	err = d.sql.QueryRow(
		`SELECT rt.disk_quota_bytes FROM users u
		 JOIN role_templates rt ON rt.id = u.role_template_id
		 WHERE u.id = ?`, userID,
	).Scan(&tmplQuota)
	if err == nil && tmplQuota.Valid {
		return tmplQuota.Int64, nil
	}
	// Default
	return 0, nil
}

func scanUser(row *sql.Row) (User, error) {
	var u User
	var mcp, sus int
	err := row.Scan(&u.ID, &u.UID, &u.Username, &u.PasswordHash, &u.Role, &mcp, &sus, &u.RoleTemplateID, &u.CredentialPresetID, &u.DiskQuotaBytes, &u.MaxSessions, &u.CreatedAt, &u.LastLoginAt, &u.LastLoginIP)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	u.MustChangePassword = mcp == 1
	u.Suspended = sus == 1
	return u, nil
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}
