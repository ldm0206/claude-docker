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
	row := d.sql.QueryRow(`SELECT id, uid, username, password_hash, role, must_change_password, suspended FROM users WHERE username = ?`, name)
	return scanUser(row)
}

func (d *DB) GetUserByID(id int) (User, error) {
	row := d.sql.QueryRow(`SELECT id, uid, username, password_hash, role, must_change_password, suspended FROM users WHERE id = ?`, id)
	return scanUser(row)
}

func (d *DB) SetPassword(id int, hash string) error {
	_, err := d.sql.Exec(`UPDATE users SET password_hash = ?, must_change_password = 0 WHERE id = ?`, hash, id)
	return err
}

func (d *DB) ListUsers() ([]User, error) {
	rows, err := d.sql.Query(`SELECT id, uid, username, password_hash, role, must_change_password, suspended FROM users ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	var users []User
	for rows.Next() {
		var u User
		var mcp, sus int
		if err := rows.Scan(&u.ID, &u.UID, &u.Username, &u.PasswordHash, &u.Role, &mcp, &sus); err != nil {
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

func (d *DB) TouchLogin(id int, ts int64) error {
	_, err := d.sql.Exec(`UPDATE users SET last_login_at = ? WHERE id = ?`, ts, id)
	return err
}

func scanUser(row *sql.Row) (User, error) {
	var u User
	var mcp, sus int
	err := row.Scan(&u.ID, &u.UID, &u.Username, &u.PasswordHash, &u.Role, &mcp, &sus)
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
