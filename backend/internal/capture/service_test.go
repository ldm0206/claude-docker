package capture

import (
	"strings"
	"sync"
	"testing"
)

// fakeRunner is a test double for ProxyRunner. It records Start/Stop calls and
// can be configured to fail Start.
type fakeRunner struct {
	mu          sync.Mutex
	startCalls  int
	stopCalls   int
	startAddr   string
	running     bool
	startErr    error
}

func (f *fakeRunner) Start(addr string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.startCalls++
	f.startAddr = addr
	if f.startErr != nil {
		return f.startErr
	}
	f.running = true
	return nil
}

func (f *fakeRunner) Stop() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopCalls++
	f.running = false
	return nil
}

func (f *fakeRunner) Running() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running
}

func (f *fakeRunner) starts() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.startCalls
}

func (f *fakeRunner) stops() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stopCalls
}

func (f *fakeRunner) addr() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.startAddr
}

func newService(t *testing.T, fr *fakeRunner) *Service {
	t.Helper()
	return NewService(fr, NewStore(), nil, 8888)
}

func TestEnable_StartsRunnerOnFirstEnable(t *testing.T) {
	fr := &fakeRunner{}
	s := newService(t, fr)

	if err := s.Enable("sess-1", 7); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if !s.IsEnabled("sess-1") {
		t.Fatal("IsEnabled(sess-1) = false, want true")
	}
	if fr.starts() != 1 {
		t.Fatalf("start calls = %d, want 1", fr.starts())
	}
	if !fr.Running() {
		t.Fatal("runner.Running() = false, want true")
	}
	if fr.addr() != "127.0.0.1:8888" {
		t.Fatalf("start addr = %q, want 127.0.0.1:8888", fr.addr())
	}
}

func TestEnable_SecondEnableDoesNotRestart(t *testing.T) {
	fr := &fakeRunner{}
	s := newService(t, fr)

	if err := s.Enable("sess-1", 7); err != nil {
		t.Fatalf("Enable 1: %v", err)
	}
	if err := s.Enable("sess-2", 8); err != nil {
		t.Fatalf("Enable 2: %v", err)
	}
	if fr.starts() != 1 {
		t.Fatalf("start calls = %d, want 1", fr.starts())
	}
	if !s.IsEnabled("sess-1") || !s.IsEnabled("sess-2") {
		t.Fatal("both sessions should be enabled")
	}
}

func TestEnable_StartFailureClearsFlag(t *testing.T) {
	fr := &fakeRunner{startErr: errBoom}
	s := newService(t, fr)

	err := s.Enable("sess-1", 7)
	if err == nil {
		t.Fatal("Enable succeeded, want error")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want '*boom*'", err)
	}
	if s.IsEnabled("sess-1") {
		t.Fatal("IsEnabled should be false after Start failure")
	}
	if fr.Running() {
		t.Fatal("runner.Running() should be false after Start failure")
	}
	// A retry after fixing the failure should work and only count as one Start.
	fr.startErr = nil
	if err := s.Enable("sess-1", 7); err != nil {
		t.Fatalf("Enable retry: %v", err)
	}
	if !s.IsEnabled("sess-1") {
		t.Fatal("IsEnabled should be true after retry")
	}
	if fr.starts() != 2 {
		t.Fatalf("start calls = %d, want 2 (attempt+retry)", fr.starts())
	}
}

func TestDisable_StopsRunnerWhenNoFlagsRemain(t *testing.T) {
	fr := &fakeRunner{}
	s := newService(t, fr)

	if err := s.Enable("sess-1", 7); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	s.Disable("sess-1")
	if s.IsEnabled("sess-1") {
		t.Fatal("IsEnabled should be false after Disable")
	}
	if fr.stops() != 1 {
		t.Fatalf("stop calls = %d, want 1", fr.stops())
	}
	if fr.Running() {
		t.Fatal("runner should be stopped")
	}
}

func TestDisable_DoesNotStopWhenAnotherSessionStillEnabled(t *testing.T) {
	fr := &fakeRunner{}
	s := newService(t, fr)

	if err := s.Enable("sess-1", 7); err != nil {
		t.Fatalf("Enable 1: %v", err)
	}
	if err := s.Enable("sess-2", 8); err != nil {
		t.Fatalf("Enable 2: %v", err)
	}
	s.Disable("sess-1")
	if s.IsEnabled("sess-1") {
		t.Fatal("sess-1 should be disabled")
	}
	if !s.IsEnabled("sess-2") {
		t.Fatal("sess-2 should still be enabled")
	}
	if fr.stops() != 0 {
		t.Fatalf("stop calls = %d, want 0 (other session still enabled)", fr.stops())
	}
	// Now disabling the last one stops the runner.
	s.Disable("sess-2")
	if fr.stops() != 1 {
		t.Fatalf("stop calls = %d, want 1 after last disable", fr.stops())
	}
}

func TestProxyURL(t *testing.T) {
	fr := &fakeRunner{}
	s := NewService(fr, NewStore(), nil, 9090)
	if got, want := s.ProxyURL(), "http://127.0.0.1:9090"; got != want {
		t.Fatalf("ProxyURL = %q, want %q", got, want)
	}
}

func TestProxyURL_DefaultPort(t *testing.T) {
	fr := &fakeRunner{}
	s := newService(t, fr)
	if got, want := s.ProxyURL(), "http://127.0.0.1:8888"; got != want {
		t.Fatalf("ProxyURL = %q, want %q", got, want)
	}
}

func TestDisable_DisabledWhenNotEnabledNoOp(t *testing.T) {
	fr := &fakeRunner{}
	s := newService(t, fr)
	// Disabling a session that was never enabled should not panic and should not start the runner.
	s.Disable("never-enabled")
	if fr.stops() != 0 {
		t.Fatalf("stop calls = %d, want 0", fr.stops())
	}
	if fr.starts() != 0 {
		t.Fatalf("start calls = %d, want 0", fr.starts())
	}
}

func TestStore(t *testing.T) {
	fr := &fakeRunner{}
	st := NewStore()
	s := NewService(fr, st, nil, 8888)
	if s.Store() != st {
		t.Fatal("Store() does not return the injected store")
	}
}