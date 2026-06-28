package capture

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"

	"github.com/ldm0206/claude-docker/backend/internal/store"
)

// errBoom is a sentinel error used in unit tests to simulate a runner Start failure.
var errBoom = errors.New("boom")

// errNotImplemented is returned by the MITMRunner stub on non-Linux platforms.
var errNotImplemented = errors.New("mitm runner not available on this platform")

// ProxyRunner is the seam over the MITM proxy lifecycle. The real implementation
// (MITMRunner) wraps github.com/lqqyt2423/go-mitmproxy; tests inject a fake.
// The real runner is Linux-runtime only (see service_linux.go), but compiles on
// all platforms.
type ProxyRunner interface {
	Start(addr string) error
	Stop() error
	Running() bool
}

// Service manages the lazy lifecycle of the capture proxy and a per-session
// capture flag. The first Enable starts the runner; the last Disable stops it.
// Start failures roll back the flag so the API can return a 500-equivalent
// error and the session env is not rerouted.
type Service struct {
	runner    ProxyRunner
	store     *Store
	masterKey []byte
	db        *store.DB

	mu   sync.Mutex
	flag map[string]bool // sessionID -> capture-on
	port int             // CLAUDE_DEBUG_PROXY_PORT (default 8888)
}

// NewService returns a Service. port is the port the proxy listens on; pass 0
// to use 8888.
func NewService(runner ProxyRunner, st *Store, db *store.DB, masterKey []byte, port int) *Service {
	if port == 0 {
		port = 8888
	}
	return &Service{
		runner:    runner,
		store:     st,
		masterKey: masterKey,
		db:        db,
		flag:      make(map[string]bool),
		port:      port,
	}
}

// Enable marks the session as capture-on and, on the first enable, lazily
// starts the proxy runner. If Start fails the flag is cleared and the error
// is returned, so callers do not reroute the session env to a dead proxy.
func (s *Service) Enable(sessionID string, userID int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.runner.Running() {
		if err := s.runner.Start(s.proxyAddr()); err != nil {
			// Defensive: ensure the flag is clear even if it was somehow set.
			s.flag[sessionID] = false
			return fmt.Errorf("start capture proxy: %w", err)
		}
	}
	s.flag[sessionID] = true
	return nil
}

// Disable unmarks the session. If no flags remain capture-on, the runner is
// stopped (releasing the listening port). Calling Disable on a session that
// was never enabled is a no-op; in particular it does not Stop a runner that
// was never started (no flags were ever true, so the runner isn't running).
func (s *Service) Disable(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	wasEnabled := s.flag[sessionID]
	s.flag[sessionID] = false
	// Only stop if we just cleared the last enabled flag AND the runner is
	// actually running. This avoids stopping a never-started runner when
	// Disable is called on an unknown session.
	if wasEnabled && noFlagsRemain(s.flag) && s.runner.Running() {
		s.runner.Stop()
	}
}

// IsEnabled reports whether capture is on for the given session.
func (s *Service) IsEnabled(sessionID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.flag[sessionID]
}

// ProxyURL returns the proxy URL injected into the PTY env when capture is on,
// e.g. "http://127.0.0.1:8888".
func (s *Service) ProxyURL() string {
	return "http://127.0.0.1:" + strconv.Itoa(s.port)
}

// Store returns the underlying capture store, for the API/WS layer to list and
// subscribe to records.
func (s *Service) Store() *Store {
	return s.store
}

// proxyAddr is the address passed to runner.Start.
func (s *Service) proxyAddr() string {
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(s.port))
}

// noFlagsRemain reports whether every flag value is false.
func noFlagsRemain(flag map[string]bool) bool {
	for _, on := range flag {
		if on {
			return false
		}
	}
	return true
}