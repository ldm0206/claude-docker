package pty

import (
	"os"
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
	ptmx    *os.File
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
	f := m.ptmx
	m.mu.Unlock()
	if f == nil {
		return nil
	}
	return pty.Setsize(f, &pty.Winsize{Cols: cols, Rows: rows})
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
