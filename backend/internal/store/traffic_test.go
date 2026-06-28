package store

import (
	"path/filepath"
	"testing"
)

func TestAddTrafficAccumulates(t *testing.T) {
	d := t.TempDir()
	db, err := Open(filepath.Join(d, "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	if err := db.AddTraffic(1, "2026-06", 100, 50); err != nil {
		t.Fatalf("first AddTraffic: %v", err)
	}
	if err := db.AddTraffic(1, "2026-06", 10, 5); err != nil {
		t.Fatalf("second AddTraffic: %v", err)
	}

	rx, tx, err := db.GetTraffic(1, "2026-06")
	if err != nil {
		t.Fatalf("GetTraffic: %v", err)
	}
	if rx != 110 || tx != 55 {
		t.Fatalf("expected 110/55, got %d/%d", rx, tx)
	}
}

func TestAddTrafficCreatesRowOnFresh(t *testing.T) {
	d := t.TempDir()
	db, err := Open(filepath.Join(d, "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	if err := db.AddTraffic(1, "2026-07", 200, 100); err != nil {
		t.Fatalf("AddTraffic: %v", err)
	}

	rx, tx, err := db.GetTraffic(1, "2026-07")
	if err != nil {
		t.Fatalf("GetTraffic: %v", err)
	}
	if rx != 200 || tx != 100 {
		t.Fatalf("expected 200/100, got %d/%d", rx, tx)
	}
}

func TestGetTrafficMissingRow(t *testing.T) {
	d := t.TempDir()
	db, err := Open(filepath.Join(d, "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	rx, tx, err := db.GetTraffic(99, "2099-12")
	if err != nil {
		t.Fatalf("GetTraffic on missing row should not error: %v", err)
	}
	if rx != 0 || tx != 0 {
		t.Fatalf("expected 0/0 for missing row, got %d/%d", rx, tx)
	}
}

func TestResetTraffic(t *testing.T) {
	d := t.TempDir()
	db, err := Open(filepath.Join(d, "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	if err := db.AddTraffic(1, "2026-06", 500, 300); err != nil {
		t.Fatalf("AddTraffic: %v", err)
	}

	if err := db.ResetTraffic(1, "2026-06"); err != nil {
		t.Fatalf("ResetTraffic: %v", err)
	}

	rx, tx, err := db.GetTraffic(1, "2026-06")
	if err != nil {
		t.Fatalf("GetTraffic after reset: %v", err)
	}
	if rx != 0 || tx != 0 {
		t.Fatalf("expected 0/0 after reset, got %d/%d", rx, tx)
	}
}

func TestResetTrafficMissingRow(t *testing.T) {
	d := t.TempDir()
	db, err := Open(filepath.Join(d, "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	// Reset on a row that doesn't exist should be a no-op, no error
	if err := db.ResetTraffic(99, "2099-12"); err != nil {
		t.Fatalf("ResetTraffic on missing row should not error: %v", err)
	}
}

func TestSumTrafficForUser(t *testing.T) {
	d := t.TempDir()
	db, err := Open(filepath.Join(d, "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	if err := db.AddTraffic(1, "2026-05", 100, 50); err != nil {
		t.Fatalf("AddTraffic May: %v", err)
	}
	if err := db.AddTraffic(1, "2026-06", 200, 100); err != nil {
		t.Fatalf("AddTraffic June: %v", err)
	}

	rx, tx, err := db.SumTrafficForUser(1)
	if err != nil {
		t.Fatalf("SumTrafficForUser: %v", err)
	}
	if rx != 300 || tx != 150 {
		t.Fatalf("expected 300/150, got %d/%d", rx, tx)
	}
}

func TestListTrafficForUser(t *testing.T) {
	d := t.TempDir()
	db, err := Open(filepath.Join(d, "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	if err := db.AddTraffic(1, "2026-05", 100, 50); err != nil {
		t.Fatalf("AddTraffic May: %v", err)
	}
	if err := db.AddTraffic(1, "2026-06", 200, 100); err != nil {
		t.Fatalf("AddTraffic June: %v", err)
	}

	rows, err := db.ListTrafficForUser(1)
	if err != nil {
		t.Fatalf("ListTrafficForUser: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}

	// Build a map for order-independent check
	m := map[string]TrafficRow{}
	for _, r := range rows {
		m[r.YearMonth] = r
	}
	if r, ok := m["2026-05"]; !ok || r.RxBytes != 100 || r.TxBytes != 50 {
		t.Fatalf("May row wrong: %+v", r)
	}
	if r, ok := m["2026-06"]; !ok || r.RxBytes != 200 || r.TxBytes != 100 {
		t.Fatalf("June row wrong: %+v", r)
	}
}
