//go:build linux

// Package capture — real MITM proxy runner (Linux runtime only).
//
// This file is excluded from non-Linux builds so that the go-mitmproxy
// dependency (which targets the container's network + CA environment) does
// not need to compile on the Windows host. The Windows stub lives in
// service_other.go and returns errNotImplemented. The container build
// (GOOS=linux) compiles this real runner; main.go (T5) wires it up.

package capture

import (
	"fmt"
	"net"
	"strconv"
	"sync"

	"github.com/lqqyt2423/go-mitmproxy/cert"
	"github.com/lqqyt2423/go-mitmproxy/proxy"
)

// MITMRunner is the production ProxyRunner backed by go-mitmproxy. It exposes
// a Start/Stop/Running lifecycle over a self-signed MITM CA and an addon that
// captures each request/response pair into the Store (after redaction).
//
// Linux-runtime only: it relies on the container's network stack and a CA
// that must be trusted by the captured client (configured in T5).
type MITMRunner struct {
	caRootPath string           // optional: directory of CA cert/key files
	ca         cert.CA          // optional: in-memory CA (mutually exclusive with caRootPath)
	store      *Store           // where captured records are appended
	db         PackageManager   // DB seam for session/user resolution + cred decrypt
	masterKey  []byte            // key for decrypting user credential presets

	mu      sync.Mutex
	proxy   *proxy.Proxy
	stopped bool
}

// PackageManager is the subset of *store.DB used by the capture hook. It is
// declared as a local interface so this Linux-only file does not hard-import
// the DB types (keeping the Windows stub trivial). T5 wires the concrete
// *store.DB here, which satisfies this interface structurally.
type PackageManager interface {
	// The concrete *store.DB provides GetUserByID, GetPreset, etc.
	// The hook (implemented in T5) will resolve the producing session's user,
	// decrypt that user's credential preset, redact, and store.Add.
}

// NewMITMRunner constructs a runner. Pass caRootPath to point go-mitmproxy at a
// directory of CA cert/key files; pass an empty string + a non-nil ca to use
// an in-memory CA; or both empty to let go-mitmproxy generate an ephemeral CA.
func NewMITMRunner(caRootPath string, ca cert.CA, st *Store, db PackageManager, masterKey []byte) *MITMRunner {
	return &MITMRunner{
		caRootPath: caRootPath,
		ca:         ca,
		store:      st,
		db:         db,
		masterKey:  masterKey,
	}
}

// Start launches the proxy on addr (host:port). It returns an error if the
// proxy fails to construct. Start blocks in a goroutine; errors that occur
// after a successful start are surfaced via the caller's next Running() check
// (the proxy stops itself on failure). Calling Start while Running is a no-op.
func (r *MITMRunner) Start(addr string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.proxy != nil {
		return nil
	}

	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("mitm start: parse addr: %w", err)
	}
	if _, err := strconv.Atoi(portStr); err != nil {
		return fmt.Errorf("mitm start: parse port: %w", err)
	}
	_ = host

	opts := &proxy.Options{
		Addr:              addr,
		StreamLargeBodies: 1024 * 1024 * 5,
		CaRootPath:        r.caRootPath,
	}
	if r.ca != nil {
		opts.NewCaFunc = func() (cert.CA, error) { return r.ca, nil }
	}

	p, err := proxy.NewProxy(opts)
	if err != nil {
		return fmt.Errorf("mitm proxy: %w", err)
	}
	p.AddAddon(&captureAddon{runner: r})
	r.proxy = p
	r.stopped = false

	// p.Start() blocks until Close()/Shutdown(); run it in a goroutine. If it
	// returns a non-nil error the proxy is unusable; clear it (only if still
	// ours — Stop may have already swapped it) so a subsequent Enable can
	// reattempt.
	go func() {
		if err := p.Start(); err != nil {
			r.mu.Lock()
			if r.proxy == p {
				r.proxy = nil
			}
			r.mu.Unlock()
		}
	}()
	return nil
}

// Stop terminates the proxy and releases the listening port.
func (r *MITMRunner) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.proxy == nil {
		return nil
	}
	r.stopped = true
	err := r.proxy.Close()
	r.proxy = nil
	return err
}

// Running reports whether the proxy is currently active.
func (r *MITMRunner) Running() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.proxy != nil && !r.stopped
}

// captureAddon is the go-mitmproxy addon that captures each request/response
// pair into the Store after redacting against the producing session's user
// credentials. It embeds proxy.BaseAddon for no-op defaults on hooks we don't
// override.
type captureAddon struct {
	proxy.BaseAddon
	runner *MITMRunner
}

// Response is called after the full HTTP response has been read. It captures
// the req/resp pair into the store.
//
// TODO(T5): resolve the sessionID from the connection context, fetch the
// owning user, decrypt that user's credential preset via masterKey, redact
// headers + body with Redact/RedactHeaders, and r.store.Add(Record{...}).
func (a *captureAddon) Response(f *proxy.Flow) {
	_ = f
	// Captures are produced here in T5; lifecycle (this file) is tested via
	// the fake ProxyRunner in service_test.go.
}

// compile-time interface check.
var _ ProxyRunner = (*MITMRunner)(nil)