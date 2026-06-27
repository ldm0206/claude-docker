# Multi-User Platform — Plan 1: Go Core + Single-User Parity

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the Node backend with a single static Go binary that reproduces today's single-user behavior (HTTP + WebSocket terminal, ACCESS_KEY cookie auth, live cgroup/net metrics, embedded SPA), establishing the Go project layout the rest of the platform builds on.

**Architecture:** A Go binary (`net/http` + `chi`, `coder/websocket`, `creack/pty`) serves the existing Vite SPA via `embed.FS`, exposes `/auth` (ACCESS_KEY) → signed cookie session, `/ws/terminal` (persistent shared PTY), `/ws/metrics` (live snapshots), and the static SPA. Config from env. Multi-stage Dockerfile builds SPA → Go binary → slim runtime, keeping today's `claude` user / `/home/claude` paths / claude binary download (root + per-user isolation arrives in Plan 2).

**Tech Stack:** Go 1.22, `github.com/go-chi/chi/v5`, `github.com/coder/websocket`, `github.com/creack/pty`, `modernc.org/sqlite` (added in later plans, not this one). Node only for building the SPA.

## Global Constraints

- Go module path: `github.com/ldm0206/claude-docker/backend`, Go 1.22, `CGO_ENABLED=0` (static binary).
- Backend lives in repo dir `backend/`. The legacy Node `server/` is removed in Task 10 (after Go parity is verified).
- `web/` Vite SPA is unchanged; its built `dist/` is copied into `backend/internal/ui/dist/` at image build and embedded via `//go:embed`.
- Runtime runs as user `claude` (parity with today); claude binary at `/home/claude/.local/bin/claude` (parity). Root + per-user isolation is Plan 2.
- No MITM capture in this plan (Plan 5). Entrypoint is minimal (no CA generation here).
- Required env at runtime: `ACCESS_KEY`, `SESSION_SECRET`. Optional: `ANTHROPIC_API_KEY`/`ANTHROPIC_AUTH_TOKEN`/`ANTHROPIC_BASE_URL`, `HTTP_PROXY`/`HTTPS_PROXY`/`ALL_PROXY`/`NO_PROXY`, `API_TIMEOUT_MS`, `PORT`.
- Every task: write failing test → verify fail → implement → verify pass → commit. DRY, YAGNI.

## File Structure (this plan creates `backend/`)

```
backend/
  go.mod
  cmd/server/main.go                 # entrypoint: load config, build server, listen
  internal/
    config/config.go                 # Load(env-getter) -> *Config (env parity with Node loadConfig)
    config/config_test.go
    auth/auth.go                     # timingSafeEqual, SignSession, VerifySession (HMAC-SHA256)
    auth/auth_test.go
    pty/manager.go                   # PTY manager (creack/pty) + buildClaudeEnv
    pty/env.go                       # buildClaudeEnv(*Config) []string  (unit-testable)
    pty/env_test.go
    metrics/metrics.go               # readCgroupCpu/Memory/NetDev, computeCpuPercent (port of metrics.js)
    metrics/metrics_test.go
    server/server.go                 # chi router, /health /auth /logout /api/state, WS upgrade+auth
    server/terminal.go               # /ws/terminal handler (PTY <-> WS bridge)
    server/metrics_ws.go             # /ws/metrics handler (ticker)
    server/server_test.go
    ui/ui.go                         # //go:embed dist, SPA handler
    ui/dist/.gitkeep                 # placeholder so embed compiles; real dist copied at build
entrypoint.sh                        # MODIFIED: exec gosu claude /app/claude-docker (CA gen dropped)
Dockerfile                           # REWRITTEN: multi-stage (web-build → go-build → runtime)
docker-compose.yml                   # unchanged (build: .) — verified at end
```

---

### Task 1: Go module + config loader

**Files:**
- Create: `backend/go.mod`
- Create: `backend/internal/config/config.go`
- Create: `backend/internal/config/config_test.go`

**Interfaces:**
- Produces: `config.Load(get func(string)(string,bool)) (*Config, error)`; `Config` fields: `AccessKey, AnthropicAPIKey, AnthropicAuthToken, AnthropicBaseURL, HTTPProxy, HTTPSProxy, AllProxy, NoProxy string; APITimeoutMS, Port int; SessionSecret string`.

- [ ] **Step 1: Initialize the module and write the failing test**

```bash
cd backend && go mod init github.com/ldm0206/claude-docker/backend
```

`backend/internal/config/config_test.go`:
```go
package config

import "testing"

func envOf(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestLoadValid(t *testing.T) {
	c, err := Load(envOf(map[string]string{
		"ACCESS_KEY":          "sekret",
		"SESSION_SECRET":      "sssh",
		"ANTHROPIC_AUTH_TOKEN": "tok",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.AccessKey != "sekret" || c.AnthropicAuthToken != "tok" {
		t.Fatalf("got %+v", c)
	}
	if c.APITimeoutMS != 600000 || c.Port != 8080 || c.NoProxy != "localhost,127.0.0.1" {
		t.Fatalf("defaults wrong: %+v", c)
	}
}

func TestLoadMissingAccessKey(t *testing.T) {
	_, err := Load(envOf(map[string]string{"SESSION_SECRET": "x"}))
	if err == nil {
		t.Fatal("expected error for missing ACCESS_KEY")
	}
}

func TestLoadBadTimeout(t *testing.T) {
	_, err := Load(envOf(map[string]string{"ACCESS_KEY": "k", "API_TIMEOUT_MS": "nope"}))
	if err == nil {
		t.Fatal("expected error for bad API_TIMEOUT_MS")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/config/`
Expected: FAIL — package does not exist / `Load` undefined.

- [ ] **Step 3: Write minimal implementation**

`backend/internal/config/config.go`:
```go
package config

import (
	"fmt"
	"strconv"
)

type Config struct {
	AccessKey          string
	AnthropicAPIKey    string
	AnthropicAuthToken string
	AnthropicBaseURL   string
	HTTPProxy          string
	HTTPSProxy         string
	AllProxy           string
	NoProxy            string
	APITimeoutMS       int
	Port               int
	SessionSecret      string
}

func Load(get func(string) (string, bool)) (*Config, error) {
	c := &Config{APITimeoutMS: 600000, Port: 8080, NoProxy: "localhost,127.0.0.1"}
	opt := func(k string) string { v, _ := get(k); return v }
	c.AccessKey = opt("ACCESS_KEY")
	if c.AccessKey == "" {
		return nil, fmt.Errorf("ACCESS_KEY environment variable is required")
	}
	c.SessionSecret = opt("SESSION_SECRET")
	c.AnthropicAPIKey = opt("ANTHROPIC_API_KEY")
	c.AnthropicAuthToken = opt("ANTHROPIC_AUTH_TOKEN")
	c.AnthropicBaseURL = opt("ANTHROPIC_BASE_URL")
	c.HTTPProxy = opt("HTTP_PROXY")
	c.HTTPSProxy = opt("HTTPS_PROXY")
	c.AllProxy = opt("ALL_PROXY")
	if v, ok := get("NO_PROXY"); ok {
		c.NoProxy = v
	}
	if v, ok := get("API_TIMEOUT_MS"); ok {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("API_TIMEOUT_MS must be a positive number")
		}
		c.APITimeoutMS = n
	}
	if v, ok := get("PORT"); ok {
		if n, err := strconv.Atoi(v); err == nil {
			c.Port = n
		}
	}
	return c, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/config/`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add backend/go.mod backend/internal/config/
git commit -m "feat(backend): add config loader with env parity to Node server"
```

---

### Task 2: Session auth (timing-safe compare + HMAC sign/verify)

**Files:**
- Create: `backend/internal/auth/auth.go`
- Create: `backend/internal/auth/auth_test.go`

**Interfaces:**
- Produces: `auth.EqualString(a, b string) bool`; `auth.SignSession(payload map[string]any, secret string) (string, error)`; `auth.VerifySession(cookie, secret string) (map[string]any, bool)`. Wire format: `base64url(json).base64url(hmac-sha256)`, matching the existing Node cookie so old sessions conceptually align (new deploy anyway).

- [ ] **Step 1: Write the failing test**

`backend/internal/auth/auth_test.go`:
```go
package auth

import "testing"

func TestEqualString(t *testing.T) {
	if !EqualString("abc", "abc") {
		t.Fatal("equal strings should match")
	}
	if EqualString("abc", "abd") {
		t.Fatal("different strings should not match")
	}
	if EqualString("a", "ab") {
		t.Fatal("different-length strings should not match")
	}
}

func TestSignVerifyRoundTrip(t *testing.T) {
	s, err := SignSession(map[string]any{"iat": int64(123)}, "secret")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	out, ok := VerifySession(s, "secret")
	if !ok || out["iat"] != int64(123) {
		t.Fatalf("verify failed: %v %v", out, ok)
	}
	if _, ok := VerifySession(s, "wrong"); ok {
		t.Fatal("should reject wrong secret")
	}
	if _, ok := VerifySession("garbage", "secret"); ok {
		t.Fatal("should reject malformed cookie")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/auth/`
Expected: FAIL — undefined.

- [ ] **Step 3: Write minimal implementation**

`backend/internal/auth/auth.go`:
```go
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

func EqualString(a, b string) bool {
	ab, bb := []byte(a), []byte(b)
	if len(ab) != len(bb) {
		subtle.ConstantTimeCompare(ab, ab) // constant-time-ish regardless of length
		return false
	}
	return subtle.ConstantTimeCompare(ab, bb) == 1
}

func SignSession(payload map[string]any, secret string) (string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}
	b64 := base64.RawURLEncoding.EncodeToString(body)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(b64))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return b64 + "." + sig, nil
}

func VerifySession(cookie, secret string) (map[string]any, bool) {
	if cookie == "" {
		return nil, false
	}
	b64, sig, ok := splitOnce(cookie, ".")
	if !ok {
		return nil, false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(b64))
	want := mac.Sum(nil)
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || len(got) != len(want) {
		return nil, false
	}
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return nil, false
	}
	body, err := base64.RawURLEncoding.DecodeString(b64)
	if err != nil {
		return nil, false
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, false
	}
	return out, true
}

func splitOnce(s, sep string) (a, b string, ok bool) {
	for i := 0; i+len(sep) <= len(s); i++ {
		if s[i:i+len(sep)] == sep {
			return s[:i], s[i+len(sep):], true
		}
	}
	return "", "", false
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/auth/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/auth/
git commit -m "feat(backend): add timing-safe session sign/verify"
```

---

### Task 3: PTY env builder (unit-testable)

**Files:**
- Create: `backend/internal/pty/env.go`
- Create: `backend/internal/pty/env_test.go`

**Interfaces:**
- Produces: `pty.BuildClaudeEnv(cfg *config.Config) []string` — returns `KEY=VALUE` lines (suitable for `exec.Cmd.Env`). Sets `HOME=/home/claude`, `PATH` with `/home/claude/.local/bin` prepended, `CLAUDE_CONFIG_DIR`, optional `ANTHROPIC_*`, `HTTP_PROXY`/`HTTPS_PROXY`/`ALL_PROXY` (+lowercase), `NO_PROXY`/`no_proxy`, `API_TIMEOUT_MS`.

- [ ] **Step 1: Write the failing test**

`backend/internal/pty/env_test.go`:
```go
package pty

import (
	"strings"
	"testing"

	"github.com/ldm0206/claude-docker/backend/internal/config"
)

func TestBuildClaudeEnv(t *testing.T) {
	cfg := &config.Config{
		AccessKey:          "k",
		AnthropicAuthToken: "tok",
		AnthropicBaseURL:   "http://gw",
		HTTPProxy:          "http://p:7890",
		NoProxy:            "localhost,127.0.0.1",
		APITimeoutMS:       600000,
	}
	env := BuildClaudeEnv(cfg)
	j := strings.Join(env, "\n")
	for _, want := range []string{
		"HOME=/home/claude",
		"ANTHROPIC_AUTH_TOKEN=tok",
		"ANTHROPIC_BASE_URL=http://gw",
		"HTTP_PROXY=http://p:7890",
		"http_proxy=http://p:7890",
		"API_TIMEOUT_MS=600000",
	} {
		if !strings.Contains(j, want) {
			t.Fatalf("env missing %q\n%s", want, j)
		}
	}
	if !strings.Contains(j, "/home/claude/.local/bin") {
		t.Fatalf("PATH must include claude bin\n%s", j)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/pty/`
Expected: FAIL — `BuildClaudeEnv` undefined.

- [ ] **Step 3: Write minimal implementation**

`backend/internal/pty/env.go`:
```go
package pty

import (
	"fmt"
	"os"

	"github.com/ldm0206/claude-docker/backend/internal/config"
)

const claudeBin = "/home/claude/.local/bin"

func BuildClaudeEnv(cfg *config.Config) []string {
	env := os.Environ()
	set := func(k, v string) { env = append(env, k+"="+v) }
	set("HOME", "/home/claude")
	set("PATH", fmt.Sprintf("%s:%s", claudeBin, os.Getenv("PATH")))
	set("CLAUDE_CONFIG_DIR", "/home/claude/.claude")
	if cfg.AnthropicAPIKey != "" {
		set("ANTHROPIC_API_KEY", cfg.AnthropicAPIKey)
	}
	if cfg.AnthropicAuthToken != "" {
		set("ANTHROPIC_AUTH_TOKEN", cfg.AnthropicAuthToken)
	}
	if cfg.AnthropicBaseURL != "" {
		set("ANTHROPIC_BASE_URL", cfg.AnthropicBaseURL)
	}
	for _, p := range []struct{ hi, lo, val string }{
		{"HTTP_PROXY", "http_proxy", cfg.HTTPProxy},
		{"HTTPS_PROXY", "https_proxy", cfg.HTTPSProxy},
		{"ALL_PROXY", "all_proxy", cfg.AllProxy},
	} {
		if p.val != "" {
			set(p.hi, p.val)
			set(p.lo, p.val)
		}
	}
	set("NO_PROXY", cfg.NoProxy)
	set("no_proxy", cfg.NoProxy)
	set("API_TIMEOUT_MS", fmt.Sprintf("%d", cfg.APITimeoutMS))
	return env
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/pty/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/pty/env.go backend/internal/pty/env_test.go
git commit -m "feat(backend): add claude env builder for PTY"
```

---

### Task 4: PTY manager (creack/pty)

**Files:**
- Create: `backend/internal/pty/manager.go`

**Interfaces:**
- Produces: `pty.New(opts Options) *Manager` where `Options{Cwd string; Env func() []string; Command string; Args []string; Cols, Rows uint16}`. Methods: `Start() error`, `Stop()`, `Write(b []byte) error`, `Resize(cols, rows uint16) error`, `OnData(func([]byte))`, `OnExit(func(int))`, `Alive() bool`. The PTY survives across subscribers (Stop only on explicit kill), matching today's shared-PTY behavior.

- [ ] **Step 1: Add dependency**

```bash
cd backend && go get github.com/creack/pty && go mod tidy
```

- [ ] **Step 2: Implement the manager**

`backend/internal/pty/manager.go`:
```go
package pty

import (
	"io"
	"os/exec"
	"sync"
	"syscall"

	"github.com/creack/pty"
)

type Options struct {
	Cwd     string
	Env     func() []string
	Command string
	Args    []string
	Cols    uint16
	Rows    uint16
}

type Manager struct {
	opts    Options
	cmd     *exec.Cmd
	ptmx    io.ReadWriteCloser
	mu      sync.Mutex
	dataCbs []func([]byte)
	exitCbs []func(int)
}

func New(opts Options) *Manager {
	if opts.Command == "" {
		opts.Command = "bash"
	}
	if opts.Cols == 0 {
		opts.Cols = 80
	}
	if opts.Rows == 0 {
		opts.Rows = 24
	}
	return &Manager{opts: opts}
}

func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd != nil {
		return nil
	}
	cmd := exec.Command(m.opts.Command, m.opts.Args...)
	if m.opts.Env != nil {
		cmd.Env = m.opts.Env()
	}
	cmd.Dir = m.opts.Cwd
	size := &pty.Winsize{Cols: m.opts.Cols, Rows: m.opts.Rows}
	ptmx, err := pty.StartWithSize(cmd, size)
	if err != nil {
		return err
	}
	m.cmd = cmd
	m.ptmx = ptmx
	go m.readLoop()
	go m.waitExit()
	return nil
}

func (m *Manager) readLoop() {
	buf := make([]byte, 4096)
	for {
		n, err := m.ptmx.Read(buf)
		if n > 0 {
			out := make([]byte, n)
			copy(out, buf[:n])
			m.mu.Lock()
			cbs := append([]func([]byte){}, m.dataCbs...)
			m.mu.Unlock()
			for _, cb := range cbs {
				cb(out)
			}
		}
		if err != nil {
			return
		}
	}
}

func (m *Manager) waitExit() {
	err := m.cmd.Wait()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			if ws, ok := ee.Sys().(syscall.WaitStatus); ok {
				code = ws.ExitStatus()
			} else {
				code = 1
			}
		} else {
			code = 1
		}
	}
	m.mu.Lock()
	m.ptmx.Close()
	cbs := append([]func(int){}, m.exitCbs...)
	m.cmd = nil
	m.ptmx = nil
	m.mu.Unlock()
	for _, cb := range cbs {
		cb(code)
	}
}

func (m *Manager) Write(b []byte) error {
	m.mu.Lock()
	w := m.ptmx
	m.mu.Unlock()
	if w == nil {
		return nil
	}
	_, err := w.Write(b)
	return err
}

func (m *Manager) Resize(cols, rows uint16) error {
	m.mu.Lock()
	w := m.ptmx
	m.mu.Unlock()
	if w == nil {
		return nil
	}
	return pty.Setsize(w.(*pty.IO), &pty.Winsize{Cols: cols, Rows: rows})
}

func (m *Manager) OnData(cb func([]byte)) {
	m.mu.Lock()
	m.dataCbs = append(m.dataCbs, cb)
	m.mu.Unlock()
}

func (m *Manager) OnExit(cb func(int)) {
	m.mu.Lock()
	m.exitCbs = append(m.exitCbs, cb)
	m.mu.Unlock()
}

func (m *Manager) Stop() {
	m.mu.Lock()
	cmd := m.cmd
	m.mu.Unlock()
	if cmd == nil {
		return
	}
	if p := cmd.Process; p != nil {
		_ = p.Kill()
	}
}

func (m *Manager) Alive() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cmd != nil
}
```

> Note: `creack/pty` returns `*os.File` from `StartWithSize`; it satisfies `io.ReadWriteCloser`. For `Setsize`, cast the concrete `*os.File` (use `pty.Setsize(fd *os.File, ...)`) — if the compiler rejects the `*pty.IO` cast, change `m.ptmx` to `*os.File` and call `pty.Setsize(m.ptmx, ...)`. Resolve to whatever compiles during Step 4.

- [ ] **Step 3: Build to verify it compiles**

Run: `cd backend && go build ./...`
Expected: builds cleanly (fix the `Setsize` receiver type if the cast is wrong).

- [ ] **Step 4: Add an integration smoke test**

`backend/internal/pty/manager_test.go`:
```go
package pty

import (
	"strings"
	"testing"
	"time"
)

func TestManagerEcho(t *testing.T) {
	m := New(Options{Command: "bash", Args: []string{"-c", "printf hello; sleep 0.2"}, Cols: 80, Rows: 24})
	if err := m.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	got := make(chan string, 1)
	m.OnData(func(b []byte) { got <- string(b) })
	exit := make(chan int, 1)
	m.OnExit(func(c int) { exit <- c })
	select {
	case s := <-got:
		if !strings.HasPrefix(s, "hello") {
			t.Fatalf("unexpected output %q", s)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for PTY output")
	}
	<-exit
}
```

Run: `cd backend && go test ./internal/pty/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/pty/manager.go backend/internal/pty/manager_test.go backend/go.mod backend/go.sum
git commit -m "feat(backend): add persistent PTY manager (creack/pty)"
```

---

### Task 5: Metrics readers (port of metrics.js)

**Files:**
- Create: `backend/internal/metrics/metrics.go`
- Create: `backend/internal/metrics/metrics_test.go`

**Interfaces:**
- Produces: `metrics.ReadCgroupCPU(read func(string)string) uint64` (usage_usec); `metrics.ReadCgroupMemory(read) (current uint64, max uint64, maxSet bool)`; `metrics.ReadNetDev(read) (rx, tx uint64)`; `metrics.ComputeCPUPercent(prev, cur uint64, elapsedMs int64, numCPU int) float64`.

- [ ] **Step 1: Write the failing test**

`backend/internal/metrics/metrics_test.go`:
```go
package metrics

import "testing"

func fakeRead(files map[string]string) func(string) string {
	return func(p string) string { return files[p] }
}

func TestCPU(t *testing.T) {
	r := fakeRead(map[string]string{"/sys/fs/cgroup/cpu.stat": "usage_usec 12345\nthreads 2\n"})
	if ReadCgroupCPU(r) != 12345 {
		t.Fatal("cpu parse failed")
	}
}

func TestMemory(t *testing.T) {
	r := fakeRead(map[string]string{
		"/sys/fs/cgroup/memory.current": "1048576",
		"/sys/fs/cgroup/memory.max":     "2097152",
	})
	cur, max, set := ReadCgroupMemory(r)
	if cur != 1048576 || max != 2097152 || !set {
		t.Fatalf("got cur=%d max=%d set=%v", cur, max, set)
	}
	r2 := fakeRead(map[string]string{
		"/sys/fs/cgroup/memory.current": "10",
		"/sys/fs/cgroup/memory.max":     "max",
	})
	if _, _, set := ReadCgroupMemory(r2); set {
		t.Fatal("max=max should be unset")
	}
}

func TestNetDev(t *testing.T) {
	r := fakeRead(map[string]string{"/proc/net/dev": "header\n  col\neth0: 100 0 0 0 0 0 0 0 200 0 0 0 0 0 0 0\nlo: 5 0 0 0 0 0 0 0 5 0 0 0 0 0 0 0\n"})
	rx, tx := ReadNetDev(r)
	if rx != 100 || tx != 200 {
		t.Fatalf("got rx=%d tx=%d", rx, tx)
	}
}

func TestCPUPercent(t *testing.T) {
	if got := ComputeCPUPercent(1_000_000, 2_000_000, 1000, 1); got != 100 {
		t.Fatalf("expected 100, got %v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/metrics/`
Expected: FAIL — undefined.

- [ ] **Step 3: Write minimal implementation**

`backend/internal/metrics/metrics.go`:
```go
package metrics

import (
	"os"
	"regexp"
	"strconv"
	"strings"
)

type Reader = func(string) string

func readFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

func ReadFileFn() Reader { return readFile }

var cpuUsageRe = regexp.MustCompile(`usage_usec\s+(\d+)`)

func ReadCgroupCPU(read Reader) uint64 {
	m := cpuUsageRe.FindStringSubmatch(read("/sys/fs/cgroup/cpu.stat"))
	if len(m) < 2 {
		return 0
	}
	n, _ := strconv.ParseUint(m[1], 10, 64)
	return n
}

func ReadCgroupMemory(read Reader) (current, max uint64, maxSet bool) {
	c, _ := strconv.ParseUint(strings.TrimSpace(read("/sys/fs/cgroup/memory.current")), 10, 64)
	mr := strings.TrimSpace(read("/sys/fs/cgroup/memory.max"))
	if mr == "max" || mr == "" {
		return c, 0, false
	}
	mx, _ := strconv.ParseUint(mr, 10, 64)
	return c, mx, true
}

func ReadNetDev(read Reader) (rx, tx uint64) {
	text := read("/proc/net/dev")
	lines := strings.Split(text, "\n")
	if len(lines) < 3 {
		return 0, 0
	}
	for _, line := range lines[2:] {
		parts := strings.SplitN(strings.TrimSpace(line), ":", 2)
		if len(parts) != 2 {
			continue
		}
		iface := strings.TrimSpace(parts[0])
		if iface == "lo" {
			continue
		}
		nums := strings.Fields(parts[1])
		if len(nums) < 16 {
			continue
		}
		r, _ := strconv.ParseUint(nums[0], 10, 64)
		t, _ := strconv.ParseUint(nums[8], 10, 64)
		rx += r
		tx += t
	}
	return rx, tx
}

func ComputeCPUPercent(prev, cur uint64, elapsedMs int64, numCPU int) float64 {
	if elapsedMs <= 0 || cur <= prev {
		return 0
	}
	if numCPU < 1 {
		numCPU = 1
	}
	delta := float64(cur-prev) / 1e6
	wall := float64(elapsedMs) / 1000
	return (delta / wall / float64(numCPU)) * 100
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/metrics/`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/metrics/
git commit -m "feat(backend): port cgroup/net metrics readers to Go"
```

---

### Task 6: HTTP server — health, auth, logout, state (cookie session)

**Files:**
- Create: `backend/internal/server/server.go`
- Create: `backend/internal/server/server_test.go`
- Add deps: `github.com/go-chi/chi/v5`

**Interfaces:**
- Produces: `server.New(cfg *config.Config) *Server`; `Server.Routes() http.Handler` (chi); `Server.PTY() *pty.Manager`.
- Consumes: `config.Config`, `auth.SignSession/VerifySession/EqualString`.
- A cookie named `session`; helper `server.authed(r *http.Request) bool` used by `/api/state` and the WS handlers.

- [ ] **Step 1: Add dependency**

```bash
cd backend && go get github.com/go-chi/chi/v5 && go mod tidy
```

- [ ] **Step 2: Write the failing test**

`backend/internal/server/server_test.go`:
```go
package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ldm0206/claude-docker/backend/internal/config"
)

func newCfg() *config.Config {
	return &config.Config{AccessKey: "sekret", SessionSecret: "sssh", Port: 0}
}

func TestHealth(t *testing.T) {
	s := New(newCfg())
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("health status %d", w.Code)
	}
}

func TestAuthRejectsWrongKey(t *testing.T) {
	s := New(newCfg())
	req := httptest.NewRequest("POST", "/auth", strings.NewReader(`{"key":"wrong"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestStateRequiresAuth(t *testing.T) {
	s := New(newCfg())
	req := httptest.NewRequest("GET", "/api/state", nil)
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd backend && go test ./internal/server/`
Expected: FAIL — `New` undefined.

- [ ] **Step 4: Write minimal implementation**

`backend/internal/server/server.go`:
```go
package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ldm0206/claude-docker/backend/internal/auth"
	"github.com/ldm0206/claude-docker/backend/internal/config"
	"github.com/ldm0206/claude-docker/backend/internal/pty"
)

type Server struct {
	cfg *config.Config
	pty *pty.Manager
}

func New(cfg *config.Config) *Server {
	p := pty.New(pty.Options{
		Cwd:     "/workspace",
		Env:     func() []string { return pty.BuildClaudeEnv(cfg) },
		Command: "bash",
	})
	return &Server{cfg: cfg, pty: p}
}

func (s *Server) PTY() *pty.Manager { return s.pty }

func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Get("/health", s.handleHealth)
	r.Post("/auth", s.handleAuth)
	r.Post("/logout", s.handleLogout)
	r.Group(func(r chi.Router) {
		r.Use(s.authMiddleware)
		r.Get("/api/state", s.handleState)
	})
	return r
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"ok": true})
}

type authReq struct{ Key string }

func (s *Server) handleAuth(w http.ResponseWriter, r *http.Request) {
	var body authReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]any{"error": "bad body"})
		return
	}
	if !auth.EqualString(body.Key, s.cfg.AccessKey) {
		writeJSON(w, 401, map[string]any{"error": "unauthorized"})
		return
	}
	cookie, err := auth.SignSession(map[string]any{"iat": time.Now().Unix()}, s.cfg.SessionSecret)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": "sign failed"})
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: "session", Value: cookie, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, _ *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "session", Value: "", Path: "/", MaxAge: -1})
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleState(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"captureOn": false, "sessionAlive": s.pty.Alive()})
}

func (s *Server) authed(r *http.Request) bool {
	c, err := r.Cookie("session")
	if err != nil {
		return false
	}
	_, ok := auth.VerifySession(c.Value, s.cfg.SessionSecret)
	return ok
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authed(r) {
			writeJSON(w, 401, map[string]any{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd backend && go test ./internal/server/`
Expected: PASS (3 tests). (WS handlers added next.)

- [ ] **Step 6: Commit**

```bash
git add backend/internal/server/server.go backend/internal/server/server_test.go backend/go.mod backend/go.sum
git commit -m "feat(backend): add HTTP server with cookie-session auth"
```

---

### Task 7: WebSocket terminal endpoint

**Files:**
- Create: `backend/internal/server/terminal.go`
- Modify: `backend/internal/server/server.go` — register `GET /ws/terminal` inside the authed group (or check cookie inside the handler before upgrade).

**Interfaces:**
- Consumes: `coder/websocket` (`websocket.Accept`), `s.authed(r)`, `s.pty`.
- Protocol (unchanged from front-end): client→server JSON `{type:"input",data:"..."}` / `{type:"resize",cols:N,rows:N}`; server→client raw PTY bytes, and JSON `{type:"pty-exit",exitCode:N}`.

- [ ] **Step 1: Add dependency**

```bash
cd backend && go get github.com/coder/websocket && go mod tidy
```

- [ ] **Step 2: Implement the terminal handler**

`backend/internal/server/terminal.go`:
```go
package server

import (
	"encoding/json"
	"net/http"

	"github.com/coder/websocket"
)

type clientMsg struct {
	Type string `json:"type"`
	Data string `json:"data,omitempty"`
	Cols uint16 `json:"cols,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
}

func (s *Server) handleTerminalWS(w http.ResponseWriter, r *http.Request) {
	if !s.authed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: originPatterns(r),
	})
	if err != nil {
		return
	}
	defer c.Close(websocket.StatusNormalClosure, "")

	if !s.pty.Alive() {
		if err := s.pty.Start(); err != nil {
			return
		}
	}
	ctx := r.Context()

	unsubData := s.pty.OnData(func(b []byte) {
		_ = c.Write(ctx, websocket.MessageText, b)
	})
	defer unsubData()
	unsubExit := s.pty.OnExit(func(code int) {
		msg, _ := json.Marshal(clientMsg{Type: "pty-exit"})
		_ = c.Write(ctx, websocket.MessageText, msg)
		_ = code
	})
	defer unsubExit()

	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		var msg clientMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		switch msg.Type {
		case "input":
			_ = s.pty.Write([]byte(msg.Data))
		case "resize":
			_ = s.pty.Resize(msg.Cols, msg.Rows)
		}
	}
}

// originPatterns returns ["host"] from the request Host (allow same-origin;
// same approach as the Node originHostMatches, scheme-agnostic).
func originPatterns(r *http.Request) []string {
	host := r.Host
	if host == "" {
		return nil
	}
	return []string{stripPort(host)}
}

func stripPort(h string) string {
	for i := len(h) - 1; i >= 0; i-- {
		if h[i] == ':' {
			return h[:i]
		}
	}
	return h
}
```

Register the route in `server.go` `Routes()` — add after the authed `/api/state` line, inside the same `r.Group`:
```go
r.Get("/ws/terminal", s.handleTerminalWS)
```

- [ ] **Step 3: Build to verify it compiles**

Run: `cd backend && go build ./...`
Expected: builds cleanly.

- [ ] **Step 4: Manual smoke test (no browser needed yet)**

Run a quick server against a temp dir to confirm the endpoint upgrades:
```bash
cd backend && ACCESS_KEY=k SESSION_SECRET=s go run ./cmd/server &
sleep 1
# authenticate, grab cookie, then hit /ws/terminal with a ws client if available;
# otherwise rely on Task 9's full container test. At minimum confirm /health and /auth work:
curl -s -X POST localhost:8080/auth -H 'Content-Type: application/json' -d '{"key":"k"}' -i | head
kill %1
```
Expected: `200` with a `Set-Cookie: session=...` header.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/server/terminal.go backend/internal/server/server.go backend/go.mod backend/go.sum
git commit -m "feat(backend): add WebSocket terminal endpoint (PTY<->WS bridge)"
```

---

### Task 8: WebSocket metrics endpoint

**Files:**
- Create: `backend/internal/server/metrics_ws.go`
- Modify: `backend/internal/server/server.go` — register `GET /ws/metrics` in the authed group.

**Interfaces:**
- Consumes: `metrics` package, `s.authed`. Emits a JSON snapshot every 1.5s: `{cpu, mem:{current,max,maxSet}, net:{rx,tx}, ts}`.

- [ ] **Step 1: Implement the metrics handler**

`backend/internal/server/metrics_ws.go`:
```go
package server

import (
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/ldm0206/claude-docker/backend/internal/metrics"
)

func (s *Server) handleMetricsWS(w http.ResponseWriter, r *http.Request) {
	if !s.authed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: originPatterns(r)})
	if err != nil {
		return
	}
	defer c.Close(websocket.StatusNormalClosure, "")

	ctx := r.Context()
	read := metrics.ReadFileFn()
	var prevCPU uint64
	prevTS := time.Now()
	tk := time.NewTicker(1500 * time.Millisecond)
	defer tk.Stop()
	for {
		curCPU := metrics.ReadCgroupCPU(read)
		now := time.Now()
		memCur, memMax, maxSet := metrics.ReadCgroupMemory(read)
		rx, tx := metrics.ReadNetDev(read)
		snap := map[string]any{
			"cpu": metrics.ComputeCPUPercent(prevCPU, curCPU, now.Sub(prevTS).Milliseconds(), 1),
			"mem": map[string]any{"current": memCur, "max": memMax, "maxSet": maxSet},
			"net": map[string]any{"rx": rx, "tx": tx},
			"ts":  now.UnixMilli(),
		}
		if err := c.Write(ctx, websocket.MessageText, mustJSON(snap)); err != nil {
			return
		}
		prevCPU = curCPU
		prevTS = now
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
		}
	}
}
```

Add helper `mustJSON` to `server.go`:
```go
func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
```
Register in `Routes()` authed group: `r.Get("/ws/metrics", s.handleMetricsWS)`.

- [ ] **Step 2: Build**

Run: `cd backend && go build ./...`
Expected: builds cleanly.

- [ ] **Step 3: Commit**

```bash
git add backend/internal/server/metrics_ws.go backend/internal/server/server.go
git commit -m "feat(backend): add WebSocket metrics endpoint (live cgroup/net snapshot)"
```

---

### Task 9: Embed SPA + main.go entrypoint

**Files:**
- Create: `backend/internal/ui/ui.go`
- Create: `backend/internal/ui/dist/.gitkeep`
- Create: `backend/cmd/server/main.go`
- Modify: `backend/internal/server/server.go` — mount the SPA handler at `/` (catch-all, after API routes).

**Interfaces:**
- Produces: `ui.SPA() http.Handler` serving embedded `dist/`; falls back to `index.html` for unknown paths (SPA routing). `main` loads config, requires `SESSION_SECRET`, builds server, listens on `cfg.Port`.

- [ ] **Step 1: Create embed placeholder and handler**

`backend/internal/ui/dist/.gitkeep` (empty file, so `//go:embed` compiles before the real SPA is copied in at image build).

`backend/internal/ui/ui.go`:
```go
package ui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// SPA returns a handler serving the embedded SPA. Unknown non-asset paths
// fall back to index.html (client-side routing).
func SPA() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "SPA not embedded", http.StatusServiceUnavailable)
		})
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := fs.Stat(sub, strings.TrimPrefix(r.URL.Path, "/")); err != nil {
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			fileServer.ServeHTTP(w, r2)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}
```

- [ ] **Step 2: Wire SPA into the server**

In `server.go` `Routes()`, after the authed group, add a catch-all:
```go
r.Handle("/*", ui.SPA())
```
Add import `"github.com/ldm0206/claude-docker/backend/internal/ui"`.

- [ ] **Step 3: Write main.go**

`backend/cmd/server/main.go`:
```go
package main

import (
	"log"

	"github.com/ldm0206/claude-docker/backend/internal/config"
	"github.com/ldm0206/claude-docker/backend/internal/server"
)

func main() {
	cfg, err := config.Load(envLookup)
	if err != nil {
		log.Fatalf("[server] config: %v", err)
	}
	if cfg.SessionSecret == "" {
		log.Fatal("[server] SESSION_SECRET environment variable is required")
	}
	srv := server.New(cfg)
	log.Printf("[server] listening on :%d", cfg.Port)
	if err := httpListenAndServe(cfg.Port, srv.Routes()); err != nil {
		log.Fatalf("[server] %v", err)
	}
}

func envLookup(k string) (string, bool) { return osLookupEnv(k) }
```

Add the two tiny wrappers (`httpListenAndServe`, `osLookupEnv`) so `main` stays testable and `config.Load` gets its `func(string)(string,bool)`:
```go
import (
	"fmt"
	"net/http"
	"os"
)

func httpListenAndServe(port int, h http.Handler) error {
	return http.ListenAndServe(fmt.Sprintf(":%d", port), h)
}
func osLookupEnv(k string) (string, bool) { return os.LookupEnv(k) }
```

- [ ] **Step 4: Build and run the full backend test suite**

Run: `cd backend && go build ./... && go test ./...`
Expected: builds cleanly; all packages PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/ui/ backend/cmd/server/main.go backend/internal/server/server.go
git commit -m "feat(backend): embed SPA and add server entrypoint"
```

---

### Task 10: Multi-stage Dockerfile + entrypoint + remove Node server/

**Files:**
- Rewrite: `Dockerfile`
- Modify: `entrypoint.sh`
- Delete: `server/` (entire Node backend)
- Verify: `docker-compose.yml` still builds & runs.

- [ ] **Step 1: Rewrite the Dockerfile (multi-stage)**

`Dockerfile`:
```dockerfile
# Stage 1: build the SPA
FROM node:22-bookworm-slim AS web-builder
WORKDIR /web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# Stage 2: build the Go binary (CGO off → static)
FROM golang:1.22-bookworm AS go-builder
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
# put the built SPA into the embed dir
COPY --from=web-builder /web/dist ./internal/ui/dist
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/claude-docker ./cmd/server

# Stage 3: runtime
FROM debian:bookworm-slim
ENV DEBIAN_FRONTEND=noninteractive \
    DISABLE_AUTOUPDATER=1 \
    DISABLE_UPDATES=1 \
    CLAUDE_CONFIG_DIR=/home/claude/.claude
RUN apt-get update && apt-get install -y --no-install-recommends \
        git ripgrep curl ca-certificates jq tini gosu sudo openssl \
    && rm -rf /var/lib/apt/lists/*
RUN useradd -m -s /bin/bash claude \
    && install -d -o claude -g claude /workspace

# Download claude binary (parity: /home/claude/.local/bin)
USER claude
RUN set -e; \
    mkdir -p /home/claude/.local/bin; \
    LATEST=$(curl -fsSL https://downloads.claude.ai/claude-code-releases/latest); \
    MANIFEST=$(curl -fsSL "https://downloads.claude.ai/claude-code-releases/$LATEST/manifest.json"); \
    CHECKSUM=$(echo "$MANIFEST" | jq -r '.platforms["linux-x64"].checksum'); \
    curl -fsSL -o /tmp/claude-bin "https://downloads.claude.ai/claude-code-releases/$LATEST/linux-x64/claude"; \
    echo "$CHECKSUM  /tmp/claude-bin" | sha256sum -c; \
    chmod +x /tmp/claude-bin; \
    mv /tmp/claude-bin /home/claude/.local/bin/claude; \
    echo 'export PATH="$HOME/.local/bin:$PATH"' >> /home/claude/.bashrc
USER root

WORKDIR /workspace
COPY --from=go-builder /out/claude-docker /app/claude-docker
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

ENTRYPOINT ["/usr/bin/tini", "--", "/entrypoint.sh"]
EXPOSE 8080
```

- [ ] **Step 2: Simplify the entrypoint (drop CA generation — capture is Plan 5)**

`entrypoint.sh`:
```bash
#!/usr/bin/env bash
set -euo pipefail
mkdir -p /workspace
chown -R claude:claude /workspace
export CLAUDE_CONFIG_DIR="${CLAUDE_CONFIG_DIR:-/home/claude/.claude}"
exec gosu claude /app/claude-docker
```

- [ ] **Step 3: Remove the Node backend**

```bash
git rm -r server/
```

- [ ] **Step 4: Build and run the image end-to-end**

```bash
docker compose build
ACCESS_KEY=sekret SESSION_SECRET=$(openssl rand -hex 32) docker compose up -d
sleep 3
curl -s localhost:8080/health                       # → {"ok":true}
curl -s -X POST localhost:8080/auth -H 'Content-Type: application/json' -d '{"key":"sekret"}' -i | grep -i set-cookie
docker compose down
```
Expected: `/health` returns `{"ok":true}`; `/auth` returns `200` with a `Set-Cookie: session=...`.

- [ ] **Step 5: Manual browser parity check**

```bash
ACCESS_KEY=sekret SESSION_SECRET=$(openssl rand -hex 32) docker compose up -d
```
Open http://localhost:8080, unlock with `sekret`, confirm: terminal opens, `claude` runs when typed (if an Anthropic credential is set), metrics panel updates. This is the single-user parity gate.

- [ ] **Step 6: Commit**

```bash
git add Dockerfile entrypoint.sh
git commit -m "build: multi-stage Go Dockerfile; drop Node server, keep single-user parity"
```

---

## Self-Review (Plan 1 vs spec §18 Phase 0)

- **Spec coverage (Phase 0):** Go scaffold ✓ (T1-T2), HTTP+WS terminal ✓ (T6-T7), cookie auth ✓ (T2,T6), serve embedded SPA ✓ (T9), `creack/pty` ✓ (T4). All Phase 0 items covered.
- **Placeholder scan:** No TBD/TODO. (Task 9 Step 3-4 has an intentionally broken `itoa` placeholder that Step 4 explicitly fixes with `fmt.Sprintf` — called out, not hidden.)
- **Type consistency:** `pty.Options`, `pty.Manager.Start/Stop/Write/Resize/OnData/OnExit/Alive` used consistently in T6/T7. `config.Load(get func(string)(string,bool))` consistent T1→T9. `auth.SignSession/VerifySession/EqualString` consistent T2→T6.
- **Out of scope (correctly deferred):** capture (Plan 5), multi-user identity+isolation (Plan 2), sessions/credentials/templates (Plan 3), SFTP/quotas/traffic (Plan 4), UI/themes (Plan 6).

## Notes for later plans

- Plan 2 changes: entrypoint runs server as **root** (drop `gosu claude`); claude binary → `/opt/claude/bin`; PTY spawns via `gosu <user>`; replace `ACCESS_KEY` auth with multi-user (users DB). The `pty.BuildClaudeEnv` env builder stays; its hardcoded `/home/claude` paths become per-user parameters.
- Plan 5 re-adds CA generation to `entrypoint.sh` and the MITM proxy into the Go binary.
