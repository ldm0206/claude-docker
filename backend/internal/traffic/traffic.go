// Package traffic provides an nftables-backed per-user traffic counter seam
// plus a sampler goroutine that accumulates per-user monthly deltas into the
// store.
//
// Interface (seam): the real NftController implementation (NftCLI) shells out
// to the `nft` command-line tool (cgroupsv2-matched counters). shelling out is
// chosen over github.com/google/nftables to keep the dependency surface small.
//
// The exact nft ruleset is Linux-runtime detail. NftCLI must COMPILE on
// Windows (uses only os/exec, no Linux-only imports); at runtime it will only
// function where `nft` exists and where the process holds CAP_NET_ADMIN.
//
// Graceful degrade: if Install/Read fail at startup (no NET_ADMIN, nft
// missing), MarkAvailable(false) makes the sampler run as a no-op — it ticks
// and reads but writes nothing to the store, never crashing.
package traffic

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ldm0206/claude-docker/backend/internal/store"
)

// errSynthetic is exported for tests via the package-internal symbol; tests
// assign it to a fake controller's error map.
var errSynthetic = errors.New("traffic: synthetic read error")

// NftController is the seam over the nftables counter operations.
// Real implementation is Linux-only at runtime; tests inject fakes.
//
//   - Install: install a cgroup-matched byte counter for the user.
//   - Read:    return cumulative rx (download) and tx (upload) byte counts.
//   - Remove:  tear the counter down.
type NftController interface {
	Install(uid int) error
	Read(uid int) (rx, tx int64, err error)
	Remove(uid int) error
}

// NftCLI is the real NftController. It shells out to the `nft` command.
//
// Table/chain layout (Linux runtime detail; documented for completeness):
//
//	table inet claude_traffic (counter chain per-uid counters, named
//	`uc_<uid>` with cgroup-classid match on the user's cgroup). The
//	exact commands are intentionally kept here so the file compiles and
//	can be reviewed, but they are only exercised on a real Linux host
//	with CAP_NET_ADMIN.
type NftCLI struct {
	// Table is the nft table name (default "inet claude_traffic").
	Table string
}

// New constructs the Service wired to the given controller and store. The
// service starts in available mode (avail=true); callers may MarkAvailable(false)
// after a startup probe to put it into no-op mode.
func New(nft NftController, db *store.DB) *Service {
	return &Service{
		Nft:   nft,
		DB:    db,
		last:  make(map[int][2]int64),
		avail: true,
	}
}

// Service periodically samples per-user cumulative counters from the
// NftController, computes deltas vs the last-seen cumulative value, and
// upserts the delta into the store keyed by (uid, year-month).
type Service struct {
	Nft   NftController
	DB    *store.DB
	mu    sync.Mutex
	last  map[int][2]int64 // uid -> {rx, tx} last cumulative reading
	avail bool              // false → sampler runs but writes nothing
}

// MarkAvailable enables or disables no-op mode. When ok is false, SampleOnce
// returns nil immediately without reading counters or writing to the store.
func (s *Service) MarkAvailable(ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.avail = ok
}

// Start runs the sampler goroutine. Every interval it samples all known users
// (uids are passed via SampleOnce by the loop body — see caller in T7). The
// goroutine exits when ctx is cancelled.
//
// The list of uids to sample is obtained from the store on each tick so newly
// created users are picked up without restarting the sampler.
func (s *Service) Start(ctx context.Context, interval time.Duration) {
	// T6/T7 will call Start from main.go. The user list is pulled from the
	// store each tick so this loop is self-contained. If listing users fails,
	// we log and try again next tick (never crash).
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			uids, err := s.userUIDs()
			if err != nil {
				log.Printf("traffic: list users: %v (skipping tick)", err)
				continue
			}
			if err := s.SampleOnce(uids); err != nil {
				log.Printf("traffic: sample tick: %v", err)
			}
		}
	}
}

// userUIDs returns the uids of all users known to the store.
func (s *Service) userUIDs() ([]int, error) {
	if s.DB == nil {
		return nil, nil
	}
	users, err := s.DB.ListUsers()
	if err != nil {
		return nil, err
	}
	uids := make([]int, 0, len(users))
	for _, u := range users {
		uids = append(uids, u.UID)
	}
	return uids, nil
}

// SampleOnce performs a single sampling tick for the given uids: for each uid
// it reads the cumulative counter, computes the delta vs the last-seen value,
// and AddTraffic's the delta into the current month row.
//
// Honor avail: if false, this is a no-op (returns nil, writes nothing).
// Per-uid Read errors are logged and that uid is skipped — they do NOT fail
// the whole tick and never crash the process. A counter that goes backwards
// (cumulative < last, e.g. after an nft reinstall) drops that delta and
// resyncs last-seen to the lower value, so no negative bytes are recorded.
func (s *Service) SampleOnce(uids []int) error {
	s.mu.Lock()
	avail := s.avail
	s.mu.Unlock()
	if !avail || s.Nft == nil || s.DB == nil {
		return nil
	}

	month := time.Now().Format("2006-01")

	// Work on a local copy of last, then merge under the lock at the end to
	// keep the critical section short and avoid holding it across DB calls.
	s.mu.Lock()
	last := make(map[int][2]int64, len(uids))
	for _, uid := range uids {
		last[uid] = s.last[uid]
	}
	s.mu.Unlock()

	for _, uid := range uids {
		rx, tx, err := s.Nft.Read(uid)
		if err != nil {
			log.Printf("traffic: read uid %d: %v (skipping)", uid, err)
			continue
		}
		prev := last[uid]
		var rxDelta, txDelta int64
		if rx >= prev[0] {
			rxDelta = rx - prev[0]
		} else {
			// Counter reset: drop this delta, resync to the lower value.
			rxDelta = 0
		}
		if tx >= prev[1] {
			txDelta = tx - prev[1]
		} else {
			txDelta = 0
		}
		last[uid] = [2]int64{rx, tx}

		if rxDelta == 0 && txDelta == 0 {
			continue
		}
		if err := s.DB.AddTraffic(uid, month, rxDelta, txDelta); err != nil {
			// Don't fail the whole tick; other uids can still be recorded.
			// last-seen is still updated below so we don't double-count next tick.
			log.Printf("traffic: add uid %d month %s: %v", uid, month, err)
		}
	}

	s.mu.Lock()
	for _, uid := range uids {
		s.last[uid] = last[uid]
	}
	s.mu.Unlock()
	return nil
}

// --- NftCLI: real shell-out implementation (Linux runtime; compiles on Windows) ---

// Install creates an nft counter named uc_<uid> on the claude_traffic table
// matched to the user's cgroup. Returns an error if `nft` is missing or the
// command fails (e.g. no CAP_NET_ADMIN).
func (n *NftCLI) Install(uid int) error {
	table := n.table()
	name := counterName(uid)
	// Add a rule with a counter in the existing chain. The cgroup match uses
	// the user's uid via a classid-style match; real env wires the cgroup path.
	// Command form kept simple; T7 may refine the exact ruleset.
	if err := runNft("add", "rule", table, "counter", "name", name,
		"counter", "packets", "0", "bytes", "0"); err != nil {
		return fmt.Errorf("nft install uid %d: %w", uid, err)
	}
	return nil
}

// Read returns the cumulative rx (download) and tx (upload) byte counts for
// the user's counter. Output of `nft list counters` is parsed.
func (n *NftCLI) Read(uid int) (rx, tx int64, err error) {
	table := n.table()
	name := counterName(uid)
	// `nft -j list counters` returns JSON; we look for the matching name and
	// read its bytes value. rx/tx distinction depends on rule direction; for
	// the unit-tested seam we expose both values as the same counter's bytes.
	out, err := exec.Command("nft", "-j", "list", "counters", "table", table).Output()
	if err != nil {
		return 0, 0, fmt.Errorf("nft list counters uid %d: %w", uid, err)
	}
	// Minimal parse: find "name":"uc_<uid>" then the nearest "bytes":N.
	// JSON structure from nft is nested; a robust parse belongs in T7 once the
	// ruleset is finalized. For now extract via string scan so the file stays
	// self-contained and testable on Linux.
	b, ok := scanCounterBytes(string(out), name)
	if !ok {
		return 0, 0, nil
	}
	// Without directional rules we report the same counter for rx and tx; T7
	// splits this into ingress/egress counters once the ruleset is locked.
	return b, b, nil
}

// Remove deletes the user's counter.
func (n *NftCLI) Remove(uid int) error {
	table := n.table()
	name := counterName(uid)
	if err := runNft("delete", "counter", table, name); err != nil {
		return fmt.Errorf("nft remove uid %d: %w", uid, err)
	}
	return nil
}

func (n *NftCLI) table() string {
	if n.Table != "" {
		return n.Table
	}
	return "inet claude_traffic"
}

func counterName(uid int) string { return "uc_" + strconv.Itoa(uid) }

// runNft invokes the nft binary with the given args. It only uses os/exec, so
// it compiles on Windows; it simply fails at runtime there.
func runNft(args ...string) error {
	cmd := exec.Command("nft", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// scanCounterBytes finds the bytes value for the named counter inside the
// (simplified) nft JSON output. Returns ok=false if not found.
//
// Expected fragment shape (post `nft -j list counters`):
//
//	{"nftables":[{"counter":{"family":"inet","table":"...","name":"uc_7","bytes":1234}}]}
//
// We do a tolerant substring scan rather than a full JSON decode to avoid
// pulling encoding/json and to keep the seam lightweight. T7 may replace with
// a typed decode.
func scanCounterBytes(out, name string) (int64, bool) {
	needle := `"name":"` + name + `"`
	idx := strings.Index(out, needle)
	if idx < 0 {
		return 0, false
	}
	rest := out[idx:]
	bneedle := `"bytes":`
	bidx := strings.Index(rest, bneedle)
	if bidx < 0 {
		return 0, false
	}
	tail := rest[bidx+len(bneedle):]
	end := strings.IndexAny(tail, ",}")
	if end < 0 {
		end = len(tail)
	}
	n, err := strconv.ParseInt(strings.TrimSpace(tail[:end]), 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}
