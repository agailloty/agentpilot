package pty

import (
	"bytes"
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func newlineForOS() string {
	if runtime.GOOS == "windows" {
		return "\r\n"
	}
	return "\n"
}

func TestManagerSpawnEchoResizeClose(t *testing.T) {
	mgr := NewManager()
	t.Cleanup(func() {
		_ = mgr.CloseAll()
	})

	cmd, args := DefaultShell()
	sess, err := mgr.Spawn("sess-1", SpawnOptions{
		Command: cmd,
		Args:    args,
		Cols:    80,
		Rows:    24,
	})
	if err != nil {
		t.Fatalf("Spawn() error = %v", err)
	}

	if sess.ID() != "sess-1" {
		t.Fatalf("ID() = %q, want %q", sess.ID(), "sess-1")
	}

	got, ok := mgr.Get("sess-1")
	if !ok || got.ID() != "sess-1" {
		t.Fatalf("Get(sess-1) = (%v, %v), want active session", got, ok)
	}

	ids := mgr.ActiveIDs()
	if len(ids) != 1 || ids[0] != "sess-1" {
		t.Fatalf("ActiveIDs() = %v, want [sess-1]", ids)
	}

	var (
		mu  sync.Mutex
		buf bytes.Buffer
	)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		tmp := make([]byte, 4096)
		for {
			n, err := sess.Read(tmp)
			if n > 0 {
				mu.Lock()
				buf.Write(tmp[:n])
				mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()

	const marker = "hello_pty"
	if _, err := sess.Write([]byte("echo " + marker + newlineForOS())); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	found := false
	for time.Now().Before(deadline) {
		mu.Lock()
		out := buf.String()
		mu.Unlock()
		if strings.Contains(out, marker) {
			found = true
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !found {
		mu.Lock()
		out := buf.String()
		mu.Unlock()
		t.Fatalf("did not observe %q in PTY output; got:\n%s", marker, out)
	}

	if err := sess.Resize(100, 30); err != nil {
		t.Fatalf("Resize(100, 30) error = %v", err)
	}

	if err := mgr.Close("sess-1"); err != nil {
		t.Fatalf("Close(sess-1) error = %v", err)
	}

	// Idempotent close on the session itself should not fail.
	if err := sess.Close(); err != nil {
		t.Fatalf("second sess.Close() error = %v", err)
	}

	if _, ok := mgr.Get("sess-1"); ok {
		t.Fatal("expected sess-1 to be removed after Close")
	}
	if len(mgr.ActiveIDs()) != 0 {
		t.Fatalf("ActiveIDs() = %v, want empty", mgr.ActiveIDs())
	}

	select {
	case <-readDone:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for PTY reader goroutine to exit after Close")
	}
}

func TestManagerValidationAndCloseAll(t *testing.T) {
	mgr := NewManager()
	t.Cleanup(func() {
		_ = mgr.CloseAll()
	})

	cmd, args := DefaultShell()
	if _, err := mgr.Spawn("s-a", SpawnOptions{Command: cmd, Args: args}); err != nil {
		t.Fatalf("Spawn(s-a) error = %v", err)
	}
	if _, err := mgr.Spawn("s-b", SpawnOptions{Command: cmd, Args: args}); err != nil {
		t.Fatalf("Spawn(s-b) error = %v", err)
	}

	if _, err := mgr.Spawn("s-a", SpawnOptions{Command: cmd, Args: args}); !errors.Is(err, ErrSessionExists) {
		t.Fatalf("duplicate Spawn error = %v, want ErrSessionExists", err)
	}

	ids := mgr.ActiveIDs()
	if len(ids) != 2 || ids[0] != "s-a" || ids[1] != "s-b" {
		t.Fatalf("ActiveIDs() = %v, want [s-a s-b]", ids)
	}

	if err := mgr.Close("missing"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("Close(missing) error = %v, want ErrSessionNotFound", err)
	}

	sess, _ := mgr.Get("s-a")
	if err := sess.Resize(0, 24); err == nil {
		t.Fatal("expected error on Resize(0, 24)")
	}

	if err := mgr.CloseAll(); err != nil {
		t.Fatalf("CloseAll() error = %v", err)
	}
	if len(mgr.ActiveIDs()) != 0 {
		t.Fatalf("ActiveIDs() = %v, want empty", mgr.ActiveIDs())
	}
}

func TestMergeEnv(t *testing.T) {
	base := []string{"PATH=/usr/bin", "FOO=old", "KEEP=yes"}
	overrides := map[string]string{"foo": "new", "BAR": "added"}

	ci := mergeEnv(base, overrides, true)
	ciMap := envSliceToMap(ci)
	if ciMap["foo"] != "new" {
		t.Fatalf("expected case-insensitive override for foo, got %v", ci)
	}
	if ciMap["FOO"] != "" {
		t.Fatalf("expected old FOO to be replaced, got %v", ci)
	}
	if ciMap["BAR"] != "added" || ciMap["KEEP"] != "yes" {
		t.Fatalf("unexpected env entries: %v", ci)
	}

	cs := mergeEnv(base, overrides, false)
	csMap := envSliceToMap(cs)
	if csMap["FOO"] != "old" || csMap["foo"] != "new" {
		t.Fatalf("expected case-sensitive keys FOO and foo to stay distinct, got %v", cs)
	}
}

func envSliceToMap(entries []string) map[string]string {
	m := make(map[string]string, len(entries))
	for _, entry := range entries {
		k, v, ok := strings.Cut(entry, "=")
		if ok {
			m[k] = v
		}
	}
	return m
}
