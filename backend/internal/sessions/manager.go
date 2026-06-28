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

// EnvFactory resolves the credential/env slice for a username lazily, so the
// PTY always sees the live credential (re-decrypted at spawn time). Returning
// a function (rather than a precomputed slice) lets credential rotation take
// effect on the next Create without restarting the server.
type EnvFactory func(username string) []string

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
//  4. Builds the PTY via factory (opts.Env wrapped to call envFactory(username)
//     lazily; opts.Cwd and opts.Username set so gosu spawns as the right user
//     in the right directory).
//  5. Stores the PTY in the live map.
//
// It does NOT call p.Start() — the WS handler lazy-starts after subscribing
// OnData, matching the existing single-PTY behavior and letting the caller
// avoid missing the first emitted bytes.
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

	// --- build opts: env resolved lazily through the factory ---
	opts.Cwd = cwd
	opts.Username = username
	if env != nil {
		opts.Env = func() []string { return env(username) }
	}
	p := m.factory(opts)

	if m.sessions[username] == nil {
		m.sessions[username] = map[string]PTY{}
	}
	m.sessions[username][id] = p

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
