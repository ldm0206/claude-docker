package store

import "fmt"

// TrafficRow represents one row in the traffic table.
type TrafficRow struct {
	UserID    int
	YearMonth string
	RxBytes   int64
	TxBytes   int64
}

// AddTraffic upserts a delta into the traffic table for the given user and month.
func (d *DB) AddTraffic(userID int, yearMonth string, rxDelta, txDelta int64) error {
	_, err := d.sql.Exec(
		`INSERT INTO traffic(user_id, year_month, rx_bytes, tx_bytes) VALUES(?, ?, ?, ?)
		 ON CONFLICT(user_id, year_month) DO UPDATE SET rx_bytes=rx_bytes+excluded.rx_bytes, tx_bytes=tx_bytes+excluded.tx_bytes`,
		userID, yearMonth, rxDelta, txDelta,
	)
	if err != nil {
		return fmt.Errorf("add traffic: %w", err)
	}
	return nil
}

// GetTraffic returns the rx and tx bytes for a user/month. Returns 0,0,nil if the row doesn't exist.
func (d *DB) GetTraffic(userID int, yearMonth string) (rx, tx int64, err error) {
	err = d.sql.QueryRow(
		`SELECT rx_bytes, tx_bytes FROM traffic WHERE user_id = ? AND year_month = ?`,
		userID, yearMonth,
	).Scan(&rx, &tx)
	if err != nil {
		// Row not found is not an error — return zeros.
		return 0, 0, nil
	}
	return rx, tx, nil
}

// ListTrafficForUser returns all traffic rows for a user, ordered by year_month.
func (d *DB) ListTrafficForUser(userID int) ([]TrafficRow, error) {
	rows, err := d.sql.Query(
		`SELECT user_id, year_month, rx_bytes, tx_bytes FROM traffic WHERE user_id = ? ORDER BY year_month`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("list traffic: %w", err)
	}
	defer rows.Close()

	var result []TrafficRow
	for rows.Next() {
		var r TrafficRow
		if err := rows.Scan(&r.UserID, &r.YearMonth, &r.RxBytes, &r.TxBytes); err != nil {
			return nil, fmt.Errorf("scan traffic row: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// ResetTraffic sets rx_bytes=0 and tx_bytes=0 for the given user/month.
// No-op if the row doesn't exist.
func (d *DB) ResetTraffic(userID int, yearMonth string) error {
	_, err := d.sql.Exec(
		`UPDATE traffic SET rx_bytes = 0, tx_bytes = 0 WHERE user_id = ? AND year_month = ?`,
		userID, yearMonth,
	)
	if err != nil {
		return fmt.Errorf("reset traffic: %w", err)
	}
	return nil
}

// SumTrafficForUser returns the total rx and tx bytes across all months for a user.
func (d *DB) SumTrafficForUser(userID int) (rx, tx int64, err error) {
	err = d.sql.QueryRow(
		`SELECT COALESCE(SUM(rx_bytes),0), COALESCE(SUM(tx_bytes),0) FROM traffic WHERE user_id = ?`,
		userID,
	).Scan(&rx, &tx)
	if err != nil {
		return 0, 0, fmt.Errorf("sum traffic: %w", err)
	}
	return rx, tx, nil
}

// SumTrafficByMonth returns the total rx and tx bytes across ALL users for the
// given year-month. Used by the admin traffic-aggregate endpoint. Rows for
// other months are excluded; a month with no rows returns 0,0,nil.
func (d *DB) SumTrafficByMonth(yearMonth string) (rx, tx int64, err error) {
	err = d.sql.QueryRow(
		`SELECT COALESCE(SUM(rx_bytes),0), COALESCE(SUM(tx_bytes),0) FROM traffic WHERE year_month = ?`,
		yearMonth,
	).Scan(&rx, &tx)
	if err != nil {
		return 0, 0, fmt.Errorf("sum traffic by month: %w", err)
	}
	return rx, tx, nil
}
