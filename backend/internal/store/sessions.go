package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// Session is the persisted metadata row for one terminal session. It is distinct
// from the live PTY process (owned by sessions.Manager) — the DB does not auto-generate it.
type Session struct {
	ID         string
	UserID     int
	Name       string
	StartedAt  int64
	LastSeenAt int64
	Alive      bool
	ClientIP   string
}

// CreateSession inserts a session row. The caller generates the id (crypto/rand
// in sessions.Manager) — the DB does not auto-generate it.
func (d *DB) CreateSession(s Session) error {
	_, err := d.sql.Exec(
		`INSERT INTO sessions (id, user_id, name, started_at, last_seen_at, alive, client_ip)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.UserID, s.Name, s.StartedAt, s.LastSeenAt, btoi(s.Alive), s.ClientIP,
	)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// GetSession returns one session row by id. Returns ErrNotFound if absent.
func (d *DB) GetSession(id string) (Session, error) {
	row := d.sql.QueryRow(
		`SELECT id, user_id, name, started_at, last_seen_at, alive, COALESCE(client_ip, '') AS client_ip FROM sessions WHERE id = ?`,
		id,
	)
	var s Session
	var alive int
	err := row.Scan(&s.ID, &s.UserID, &s.Name, &s.StartedAt, &s.LastSeenAt, &alive, &s.ClientIP)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("get session: %w", err)
	}
	s.Alive = alive == 1
	return s, nil
}

// ListSessionsForUser returns every session row (alive or dead) for the given
// user, ordered oldest-first. Used to populate the session list UI.
func (d *DB) ListSessionsForUser(userID int) ([]Session, error) {
	rows, err := d.sql.Query(
		`SELECT id, user_id, name, started_at, last_seen_at, alive, COALESCE(client_ip, '') AS client_ip FROM sessions
			 WHERE user_id = ? ORDER BY started_at ASC, id ASC`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var s Session
		var alive int
		if err := rows.Scan(&s.ID, &s.UserID, &s.Name, &s.StartedAt, &s.LastSeenAt, &alive, &s.ClientIP); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		s.Alive = alive == 1
		out = append(out, s)
	}
	return out, rows.Err()
}

// TouchSession updates last_seen_at — called on every WS frame to keep the
// session's liveness signal fresh.
func (d *DB) TouchSession(id string, ts int64) error {
	_, err := d.sql.Exec(`UPDATE sessions SET last_seen_at = ? WHERE id = ?`, ts, id)
	return err
}

// MarkSessionExited sets alive=0. The row is retained for history; the live
// PTY is stopped separately by sessions.Manager.Kill.
func (d *DB) MarkSessionExited(id string) error {
	_, err := d.sql.Exec(`UPDATE sessions SET alive = 0 WHERE id = ?`, id)
	return err
}

// MarkSessionAlive flips a session row back to alive=1 and refreshes
// last_seen_at. Used by sessions.Manager.Revive when a DB-persisted session is
// reattached after the live PTY was lost to a server restart: the row already
// exists (so no INSERT), it just needs to look alive again and reflect the
// reconnect time.
func (d *DB) MarkSessionAlive(id string, ts int64) error {
	_, err := d.sql.Exec(`UPDATE sessions SET alive = 1, last_seen_at = ? WHERE id = ?`, ts, id)
	return err
}

// UpdateSessionName changes the name column of a session row. Used by the
// session-create API to override the default name (opts.Username) with a
// user-supplied one.
func (d *DB) UpdateSessionName(id, name string) error {
	_, err := d.sql.Exec(`UPDATE sessions SET name = ? WHERE id = ?`, name, id)
	return err
}

// DeleteSession hard-deletes the metadata row. Used when a session is fully
// removed from the user's list.
func (d *DB) DeleteSession(id string) error {
	_, err := d.sql.Exec(`DELETE FROM sessions WHERE id = ?`, id)
	return err
}

// CountAliveSessionsForUser returns the number of alive=1 sessions for the user.
// Used by sessions.Manager to enforce the per-user session cap.
func (d *DB) CountAliveSessionsForUser(userID int) (int, error) {
	var n int
	err := d.sql.QueryRow(
		`SELECT count(*) FROM sessions WHERE user_id = ? AND alive = 1`,
		userID,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count alive sessions: %w", err)
	}
	return n, nil
}
