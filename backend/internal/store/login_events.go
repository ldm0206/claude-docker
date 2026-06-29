package store

import "fmt"

// LoginEvent is one row of the login audit stream (every /auth attempt).
type LoginEvent struct {
	ID        int
	UserID    int
	Username  string
	IP        string
	UserAgent string
	Success   bool
	At        int64
}

// CreateLoginEvent appends one audit row. user_id is 0 when the username does
// not exist (failed login for unknown user) — username is always recorded.
func (d *DB) CreateLoginEvent(e LoginEvent) error {
	_, err := d.sql.Exec(
		`INSERT INTO login_events (user_id, username, ip, user_agent, success, at)
			 VALUES (?, ?, ?, ?, ?, ?)`,
		e.UserID, e.Username, e.IP, e.UserAgent, btoi(e.Success), e.At,
	)
	if err != nil {
		return fmt.Errorf("create login event: %w", err)
	}
	return nil
}

// ListLoginEvents returns the most recent `limit` events, newest-first. A
// non-positive limit defaults to 100; the limit is capped at 500.
func (d *DB) ListLoginEvents(limit int) ([]LoginEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	rows, err := d.sql.Query(
		`SELECT id, user_id, username, ip, user_agent, success, at FROM login_events
		 ORDER BY at DESC, id DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list login events: %w", err)
	}
	defer rows.Close()

	var out []LoginEvent
	for rows.Next() {
		var e LoginEvent
		var success int
		if err := rows.Scan(&e.ID, &e.UserID, &e.Username, &e.IP, &e.UserAgent, &success, &e.At); err != nil {
			return nil, fmt.Errorf("scan login event: %w", err)
		}
		e.Success = success == 1
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list login events: %w", err)
	}
	return out, nil
}
