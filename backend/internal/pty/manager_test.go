//go:build linux

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
