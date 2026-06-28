// Package sessions owns the live, per-user, multi-session PTY pool. It is the
// runtime counterpart to store.Session (which is just the persisted metadata
// row): each Manager entry holds a running PTY plus its bookkeeping.
//
// The PTY dependency is an interface (sessions.PTY) so unit tests can inject a
// fake instead of the real *pty.Manager (which needs a Linux PTY and gosu).
// Production wires the factory `func(o pty.Options) PTY { return pty.New(o) }`.
package sessions

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ldm0206/claude-docker/backend/internal/pty"
	"github.com/ldm0206/claude-docker/backend/internal/store"
)

// ErrSessionCapReached is returned by Create when the user already has
// `EffectiveMaxSessions` alive sessions and the cap is non-zero.
var ErrSessionCapReached = errors.New("session cap reached")

// ErrNotFound is returned by Kill when the session does not exist in the live
// map (it may still exist as a dead row in the DB). It mirrors store.ErrNotFound
// but is a distinct sentinel so callers can distinguish "never existed" from
// "existed but already exited" if needed.
var ErrNotFound = errors.New("session not found")

// PTY is the seam between sessions.Manager and the actual terminal backend.
// The real implementation is *pty.Manager (see backend/internal/pty/manager.go);
// tests inject a fake. Method set matches *pty.Manager exactly:
//
//	Start() error
//	Stop()
//	Write(b []byte) error
//	Resize(cols, rows uint16) error
//	OnData(cb func([]byte)) func()
//	OnExit(cb func(int)) func()
//	Alive() bool
type PTY interface {
	Start() error
	Stop()
	Write(b []byte) error
	Resize(cols, rows uint16) error
	OnData(cb func([]byte)) func()
	OnExit(cb func(int)) func()
	Alive() bool
}

// Compile-time guarantee that *pty.Manager satisfies PTY. If the real type's
// method set drifts from the interface, this fails to compile (catches a
// production-wiring break early — the factory in main.go relies on this).
var _ PTY = (*pty.Manager)(nil)

// PTYFactory builds a PTY from options. Production uses
// `func(o pty.Options) PTY { return pty.New(o) }`; tests pass a fake.
type PTYFactory func(opts pty.Options) PTY

// EnvFactory resolves the credential/env slice for a (username, sessionID)
// lazily, so the PTY always sees the live credential AND the live per-session
// state (e.g. the Plan 5 capture flag, which routes the PTY through the MITM
// proxy when on). Returning a function (rather than a precomputed slice) lets
// BOTH credential rotation AND capture toggling take effect on the next Start
// (including a Manager.Restart) without restarting the server.
//
// The sessionID arg was added in P5-T3 so the factory can look up per-session
// state (capture.IsEnabled(sessionID)); the username is kept for the
// credential lookup that pre-dated Plan 5.
type EnvFactory func(username, sessionID string) []string

// Manager owns a map of live PTYs keyed by username → sessionID, guarded by
// a single mutex. The mutex covers both the map and the cap-check+INSERT
// sequence in Create so concurrent Creates cannot slip past the cap.
type Manager struct {
	db      *store.DB
	factory PTYFactory

	mu       sync.Mutex
	sessions map[string]map[string]PTY
}

// NewManager constructs a Manager. The db is used for session-row persistence
// and cap enforcement; the factory builds each PTY.
func NewManager(db *store.DB, factory PTYFactory) *Manager {
	if factory == nil {
		panic("sessions: NewManager factory must not be nil")
	}
	return &Manager{
		db:       db,
		factory:  factory,
		sessions: map[string]map[string]PTY{},
	}
}

// Create spawns a new session for the user. It:
//  1. Enforces the per-user alive-session cap (EffectiveMaxSessions); cap==0
//     means unlimited.
//  2. Generates a 16-byte crypto/rand id (base64 RawURL — no padding, URL-safe).
//  3. INSERTs a sessions row (alive=1).
//  4. Builds the PTY via factory (opts.Env wrapped to call envFactory(username, id)
//     lazily; opts.Cwd and opts.Username set so gosu spawns as the right user
//     in the right directory).
//  5. Stores the PTY in the live map.
//  6. Registers an OnExit reaper that flips the DB row to alive=0 and removes
//     the map entry when the PTY exits naturally (so the cap doesn't drift).
//
// It does NOT call p.Start() — the WS handler lazy-starts after subscribing
// OnData, matching the existing single-PTY behavior and letting the caller
// avoid missing the first emitted bytes. The reaper's OnExit is independent of
// the WS handler's own OnExit registration (PTY.OnExit allows multiple cbs).
func (m *Manager) Create(username string, userID int, cwd string, env EnvFactory, opts pty.Options) (string, PTY, error) {
	// --- cap check + INSERT under the lock so concurrent Creates serialize ---
	m.mu.Lock()
	defer m.mu.Unlock()

	cap, err := m.db.EffectiveMaxSessions(userID)
	if err != nil {
		return "", nil, fmt.Errorf("session cap lookup: %w", err)
	}
	if cap > 0 {
		alive, err := m.db.CountAliveSessionsForUser(userID)
		if err != nil {
			return "", nil, fmt.Errorf("count alive sessions: %w", err)
		}
		if alive >= cap {
			return "", nil, ErrSessionCapReached
		}
	}

	// --- id: crypto-random 16 bytes, base64 RawURL (no Math.random/uuid lib) ---
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", nil, fmt.Errorf("generate session id: %w", err)
	}
	id := base64.RawURLEncoding.EncodeToString(buf[:])

	now := time.Now().Unix()
	if err := m.db.CreateSession(store.Session{
		ID:         id,
		UserID:     userID,
		Name:       opts.Username, // tentative name; UI can rename later
		StartedAt:  now,
		LastSeenAt: now,
		Alive:      true,
	}); err != nil {
		return "", nil, fmt.Errorf("persist session: %w", err)
	}

	// --- build opts: env resolved lazily through the factory (closes over the
	// freshly-minted id, so the factory sees the right sessionID for per-session
	// state like the Plan 5 capture flag) ---
	opts.Cwd = cwd
	opts.Username = username
	if env != nil {
		opts.Env = func() []string { return env(username, id) }
	}
	p := m.factory(opts)

	if m.sessions[username] == nil {
		m.sessions[username] = map[string]PTY{}
	}
	m.sessions[username][id] = p

	// Reap natural exits: when the PTY's process quits on its own (user typed
	// `exit`, claude crashed, …), flip the DB row to alive=0 and drop the live
	// map entry so the session stops counting toward the user's cap. This is
	// best-effort and idempotent alongside Kill/KillAll (both call
	// MarkSessionExited and delete the same map key; whichever runs second is
	// a harmless no-op). The DB write is done OUTSIDE the lock to mirror Kill
	// and avoid holding m.mu across I/O.
	p.OnExit(func(_ int) {
		m.mu.Lock()
		if userSessions, ok := m.sessions[username]; ok {
			delete(userSessions, id)
			if len(userSessions) == 0 {
				delete(m.sessions, username)
			}
		}
		m.mu.Unlock()
		_ = m.db.MarkSessionExited(id)
	})

	return id, p, nil
}

// Get returns the live PTY for (username, sessionID). The username scoping
// prevents one user from touching another's PTY. Returns (nil, false) if absent.
func (m *Manager) Get(username, sessionID string) (PTY, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.sessions[username][sessionID]
	if !ok {
		return nil, false
	}
	return p, true
}

// List returns the persisted session rows for the user (alive AND dead). It
// delegates to the store and takes userID (not username) because the store is
// keyed by the integer user id; the caller (API/WS handler) already has it.
func (m *Manager) List(userID int) ([]store.Session, error) {
	return m.db.ListSessionsForUser(userID)
}

// Kill stops the live PTY, marks the DB row exited (alive=0), and removes the
// entry from the live map. Safe to call multiple times — the second call
// returns ErrNotFound (the map entry is gone).
func (m *Manager) Kill(username, sessionID string) error {
	m.mu.Lock()
	userSessions, ok := m.sessions[username]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}
	p, ok := userSessions[sessionID]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}
	delete(userSessions, sessionID)
	if len(userSessions) == 0 {
		delete(m.sessions, username)
	}
	m.mu.Unlock()

	if p != nil {
		p.Stop()
	}
	_ = m.db.MarkSessionExited(sessionID)
	return nil
}

// Restart stops the live PTY for (username, sessionID) and starts a fresh one
// in place, keeping the SAME session id and DB row. It exists for Plan 5's
// capture toggle: the PTY env factory is lazy (the real *pty.Manager calls
// opts.Env() on every Start), so Stop+Start re-reads per-session state like
// the capture flag and routes the new process through (or away from) the MITM
// proxy without creating a new session id or DB row.
//
// If the session is not in the live map Restart returns ErrNotFound and is a
// no-op (the caller — the admin capture API — turns this into a 404). A nil
// PTY entry is also ErrNotFound. Restart does NOT touch the DB row: the row
// stays alive=1 across the restart (the session did not exit; it was re-spawned).
//
// The PTY's OnExit reapers (registered by Create) survive the restart — they
// were attached to the *pty.Manager instance, which Stop+Start reuses — so a
// natural exit of the restarted process is still reaped normally.
func (m *Manager) Restart(username, sessionID string) error {
	m.mu.Lock()
	userSessions, ok := m.sessions[username]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}
	p, ok := userSessions[sessionID]
	if !ok || p == nil {
		m.mu.Unlock()
		return ErrNotFound
	}
	m.mu.Unlock()

	p.Stop()
	if err := p.Start(); err != nil {
		return fmt.Errorf("restart session %s: %w", sessionID, err)
	}
	return nil
}

// KillAll stops every live PTY for the user and marks their DB rows exited.
// Used on user suspend/delete to reclaim resources. Sessions belonging to
// other users are untouched.
func (m *Manager) KillAll(username string) error {
	m.mu.Lock()
	userSessions := m.sessions[username]
	if userSessions == nil {
		m.mu.Unlock()
		return nil
	}
	// Snapshot ids then drop the whole user bucket under the lock.
	ids := make([]string, 0, len(userSessions))
	for id, p := range userSessions {
		ids = append(ids, id)
		if p != nil {
			p.Stop()
		}
	}
	delete(m.sessions, username)
	m.mu.Unlock()

	// DB updates outside the lock — best-effort, never fail KillAll mid-way.
	for _, id := range ids {
		_ = m.db.MarkSessionExited(id)
	}
	return nil
}
