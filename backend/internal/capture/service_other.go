//go:build !linux

package capture

// MITMRunner is a stub on non-Linux platforms. The real go-mitmproxy-backed
// runner lives in service_linux.go and is Linux-runtime only (container CA,
// network). On Windows we build this no-op stub so `go build ./...` stays
// green; capture functionality is unavailable.
//
// To use the real runner, build with GOOS=linux (the container build).

// MITMRunner is the production ProxyRunner backed by go-mitmproxy. On non-Linux
// platforms it is a non-functional stub; calling Start returns errNotImplemented.
type MITMRunner struct{}

// NewMITMRunner returns a stub runner on non-Linux platforms. The arguments
// mirror the Linux constructor signature (caRootPath, in-memory CA, store, db
// seam, masterKey) but are ignored on non-Linux, where the real go-mitmproxy
// runner cannot run.
func NewMITMRunner(_ string, _ any, _ *Store, _ any, _ []byte) *MITMRunner {
	return &MITMRunner{}
}

// Start always fails on non-Linux platforms.
func (r *MITMRunner) Start(_ string) error { return errNotImplemented }

// Stop is a no-op.
func (r *MITMRunner) Stop() error { return nil }

// Running always returns false on non-Linux platforms.
func (r *MITMRunner) Running() bool { return false }

// compile-time interface check.
var _ ProxyRunner = (*MITMRunner)(nil)