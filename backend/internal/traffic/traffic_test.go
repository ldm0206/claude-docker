package traffic

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ldm0206/claude-docker/backend/internal/store"
)

// fakeNft is a controllable in-memory NftController for tests.
type fakeNft struct {
	mu  sync.Mutex
	cur map[int][2]int64 // uid -> {rx, tx} cumulative
	err map[int]error    // uid -> forced error on Read (nil = ok)
}

func newFakeNft() *fakeNft {
	return &fakeNft{cur: map[int][2]int64{}, err: map[int]error{}}
}

func (f *fakeNft) Install(uid int) error        { return nil }
func (f *fakeNft) Remove(uid int) error         { return nil }
func (f *fakeNft) Read(uid int) (int64, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e, ok := f.err[uid]; ok && e != nil {
		return 0, 0, e
	}
	v := f.cur[uid]
	return v[0], v[1], nil
}

func (f *fakeNft) set(uid int, rx, tx int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cur[uid] = [2]int64{rx, tx}
}
func (f *fakeNft) setErr(uid int, e error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err[uid] = e
}

func openDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func currentMonth() string { return time.Now().Format("2006-01") }

// --- Tests ---

// TestSampleOnceAccumulatesDeltas verifies that consecutive SampleOnce calls
// compute deltas vs the last-seen cumulative counter and accumulate into the
// store via AddTraffic.
func TestSampleOnceAccumulatesDeltas(t *testing.T) {
	nft := newFakeNft()
	db := openDB(t)
	svc := New(nft, db)

	// First tick: cumulative rx=100, tx=50 → delta 100,50
	nft.set(7, 100, 50)
	if err := svc.SampleOnce([]int{7}); err != nil {
		t.Fatalf("SampleOnce #1: %v", err)
	}
	rx, tx, err := db.GetTraffic(7, currentMonth())
	if err != nil {
		t.Fatalf("GetTraffic after #1: %v", err)
	}
	if rx != 100 || tx != 50 {
		t.Fatalf("after #1 expected 100/50, got %d/%d", rx, tx)
	}

	// Second tick: cumulative rx=150, tx=80 → delta 50,30 (accumulates to 150/80)
	nft.set(7, 150, 80)
	if err := svc.SampleOnce([]int{7}); err != nil {
		t.Fatalf("SampleOnce #2: %v", err)
	}
	rx, tx, err = db.GetTraffic(7, currentMonth())
	if err != nil {
		t.Fatalf("GetTraffic after #2: %v", err)
	}
	if rx != 150 || tx != 80 {
		t.Fatalf("after #2 expected 150/80, got %d/%d", rx, tx)
	}
}

// TestSampleOnceReadErrorSkipsUID verifies a uid whose Read errors is skipped
// (no AddTraffic, no crash).
func TestSampleOnceReadErrorSkipsUID(t *testing.T) {
	nft := newFakeNft()
	db := openDB(t)
	svc := New(nft, db)

	// Seed an ok uid alongside the erroring one.
	nft.set(11, 100, 100)
	nft.setErr(22, errSynthetic)
	nft.set(22, 999, 999) // even if cur is set, Read errors

	if err := svc.SampleOnce([]int{11, 22}); err != nil {
		t.Fatalf("SampleOnce returned error despite per-uid error: %v", err)
	}

	rx, tx, err := db.GetTraffic(11, currentMonth())
	if err != nil || rx != 100 || tx != 100 {
		t.Fatalf("uid 11 expected 100/100, got %d/%d (err=%v)", rx, tx, err)
	}
	// uid 22 must have no row written
	rx, tx, err = db.GetTraffic(22, currentMonth())
	if err != nil || rx != 0 || tx != 0 {
		t.Fatalf("uid 22 should be skipped, got %d/%d (err=%v)", rx, tx, err)
	}
}

// TestSampleOnceUnavailableWritesNothing verifies MarkAvailable(false) makes
// SampleOnce a no-op (writes nothing).
func TestSampleOnceUnavailableWritesNothing(t *testing.T) {
	nft := newFakeNft()
	db := openDB(t)
	svc := New(nft, db)
	svc.MarkAvailable(false)

	nft.set(33, 500, 250)
	if err := svc.SampleOnce([]int{33}); err != nil {
		t.Fatalf("SampleOnce when unavailable: %v", err)
	}
	rx, tx, _ := db.GetTraffic(33, currentMonth())
	if rx != 0 || tx != 0 {
		t.Fatalf("unavailable mode wrote traffic: %d/%d", rx, tx)
	}
}

// TestSampleOnceMultipleUsers verifies several uids are handled in one tick.
func TestSampleOnceMultipleUsers(t *testing.T) {
	nft := newFakeNft()
	db := openDB(t)
	svc := New(nft, db)

	nft.set(101, 10, 1)
	nft.set(102, 20, 2)
	nft.set(103, 30, 3)

	if err := svc.SampleOnce([]int{101, 102, 103}); err != nil {
		t.Fatalf("SampleOnce multi: %v", err)
	}
	for uid, want := range map[int][2]int64{101: {10, 1}, 102: {20, 2}, 103: {30, 3}} {
		rx, tx, err := db.GetTraffic(uid, currentMonth())
		if err != nil || rx != want[0] || tx != want[1] {
			t.Fatalf("uid %d expected %d/%d, got %d/%d (err=%v)", uid, want[0], want[1], rx, tx, err)
		}
	}
}

// TestSampleOnceCounterResetDropsDelta verifies that if the cumulative counter
// goes backwards (counter reset / nft reinstalled), we do not emit a negative
// delta: we skip and resync last-seen to the lower value.
func TestSampleOnceCounterResetDropsDelta(t *testing.T) {
	nft := newFakeNft()
	db := openDB(t)
	svc := New(nft, db)

	// Tick 1: cumulative 1000/1000 → +1000/1000
	nft.set(42, 1000, 1000)
	svc.SampleOnce([]int{42})

	// Counter reset (nft reinstalled, starts from small numbers again)
	nft.set(42, 5, 5)
	svc.SampleOnce([]int{42})

	// Expect only the first tick's bytes; reset dropped, no negative.
	rx, tx, _ := db.GetTraffic(42, currentMonth())
	if rx != 1000 || tx != 1000 {
		t.Fatalf("after reset expected 1000/1000, got %d/%d", rx, tx)
	}

	// Next real increment is +20/+20 → total 1020/1020
	nft.set(42, 25, 25)
	svc.SampleOnce([]int{42})
	rx, tx, _ = db.GetTraffic(42, currentMonth())
	if rx != 1020 || tx != 1020 {
		t.Fatalf("after post-reset delta expected 1020/1020, got %d/%d", rx, tx)
	}
}

// TestStartExitsOnContextCancel verifies the Start sampler goroutine exits when
// ctx is cancelled.
func TestStartExitsOnContextCancel(t *testing.T) {
	nft := newFakeNft()
	db := openDB(t)
	svc := New(nft, db)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		svc.Start(ctx, 10*time.Millisecond)
		close(done)
	}()
	// Let it tick once or twice.
	time.Sleep(35 * time.Millisecond)
	cancel()
	select {
	case <-done:
		// ok
	case <-time.After(2 * time.Second):
		t.Fatalf("Start did not exit after ctx cancel")
	}
}

// TestNftCLICompiles is a compile-time check that the shell-out impl satisfies
// the interface and builds on Windows (real exec only).
func TestNftCLICompiles(t *testing.T) {
	var _ NftController = (*NftCLI)(nil)
	// Instantiating is safe; calling Install would shell out, which we avoid here.
	_ = New(&NftCLI{}, nil)
}

// NOTE: a month-boundary test (year-month rollover mid-accumulation) is NOT
// covered here because it would require mocking time.Now(). The Service uses
// time.Now().Format("2006-01") on each SampleOnce tick, so a real rollover
// simply starts accumulating into a new month row automatically — no special
// handling and no carryover bug. If deterministic coverage is later needed,
// inject a clock func into Service; deferred.
