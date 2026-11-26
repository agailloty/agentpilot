package runner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agailloty/agentpilot/server/internal/hub"
	"github.com/agailloty/agentpilot/server/internal/protocol"
)

func TestRingBufferBoundsAndDrain(t *testing.T) {
	rb := newRingBuffer(5)
	rb.Write([]byte("abc"))
	rb.Write([]byte("def")) // keeps "bcdef"
	got := string(rb.Drain())
	if got != "bcdef" {
		t.Fatalf("Drain() = %q, want %q", got, "bcdef")
	}
	if len(rb.Drain()) != 0 {
		t.Fatal("expected second Drain() to be empty")
	}

	rb.Write([]byte("123456789"))
	if got := string(rb.Drain()); got != "56789" {
		t.Fatalf("oversized Write Drain() = %q, want %q", got, "56789")
	}
}

func TestWSEndpointConversion(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"http://127.0.0.1:8080", "ws://127.0.0.1:8080/ws/runner"},
		{"https://host.ts.net:8443/", "wss://host.ts.net:8443/ws/runner"},
		{"ws://localhost:9000/base", "ws://localhost:9000/base/ws/runner"},
	}
	for _, tc := range cases {
		got, err := wsEndpoint(tc.in)
		if err != nil {
			t.Fatalf("wsEndpoint(%q) error = %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("wsEndpoint(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestClientReconnectAndHeaders(t *testing.T) {
	var attempts atomic.Int32
	connectedCh := make(chan struct{}, 2)

	h := hub.New(hub.Options{
		Hooks: hub.Hooks{
			OnRunnerConnect: func(machineName, osName string, roots []string) {
				if machineName == "test-machine" && osName == runtime.GOOS {
					select {
					case connectedCh <- struct{}{}:
					default:
					}
				}
			},
		},
	})
	defer h.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ws/runner" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer runner-tok" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		// Reject first dial attempt to exercise exponential backoff reconnect.
		if attempts.Add(1) == 1 {
			http.Error(w, "temporary unavailable", http.StatusServiceUnavailable)
			return
		}
		h.HandleRunnerWS(w, r)
	}))
	defer srv.Close()

	client, err := NewClient(Config{
		ServerURL:      srv.URL,
		Token:          "runner-tok",
		MachineName:    "test-machine",
		Roots:          []string{"/tmp/root1"},
		InitialBackoff: 20 * time.Millisecond,
		MaxBackoff:     80 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer func() { _ = client.PTYManager().CloseAll() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	runDone := make(chan error, 1)
	go func() {
		runDone <- client.Run(ctx)
	}()

	select {
	case <-connectedCh:
	case <-ctx.Done():
		t.Fatal("timed out waiting for runner reconnect")
	}

	info, ok := h.GetRunner("test-machine")
	if !ok {
		t.Fatal("expected test-machine to be registered in hub")
	}
	if !slices.Equal(info.Roots, []string{"/tmp/root1"}) {
		t.Fatalf("info.Roots = %v, want [/tmp/root1]", info.Roots)
	}

	cancel()
	if err := <-runDone; err != nil {
		t.Fatalf("client.Run() error = %v", err)
	}
}

func TestClientFSAndExecHandlers(t *testing.T) {
	connectedCh := make(chan struct{}, 1)
	h := hub.New(hub.Options{
		Hooks: hub.Hooks{
			OnRunnerConnect: func(string, string, []string) {
				select {
				case connectedCh <- struct{}{}:
				default:
				}
			},
		},
	})
	defer h.Close()

	srv := httptest.NewServer(http.HandlerFunc(h.HandleRunnerWS))
	defer srv.Close()

	client, err := NewClient(Config{
		ServerURL:      srv.URL,
		Token:          "tok",
		MachineName:    "fs-runner",
		InitialBackoff: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer func() { _ = client.PTYManager().CloseAll() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() { _ = client.Run(ctx) }()

	<-connectedCh

	tmpRoot := t.TempDir()
	projDir := filepath.Join(tmpRoot, "demo-proj")

	mkdirEnv, _ := protocol.NewEnvelope(protocol.TypeFSMkdir, "", "", protocol.FSMkdirPayload{
		Path:    projDir,
		GitInit: false,
	})
	mkdirReply, err := h.SendRequest(ctx, "fs-runner", mkdirEnv)
	if err != nil {
		t.Fatalf("fs-mkdir SendRequest error = %v", err)
	}
	if mkdirReply.Type != protocol.TypeAck {
		t.Fatalf("fs-mkdir reply type = %q, want ack (payload=%s)", mkdirReply.Type, string(mkdirReply.Payload))
	}
	var mkdirAck protocol.FSMkdirAckPayload
	if err := mkdirReply.DecodePayload(&mkdirAck); err != nil {
		t.Fatalf("decode mkdir ack: %v", err)
	}
	if mkdirAck.Path != projDir {
		t.Fatalf("mkdirAck.Path = %q, want %q", mkdirAck.Path, projDir)
	}
	if _, err := os.Stat(projDir); err != nil {
		t.Fatalf("created directory missing: %v", err)
	}

	// Create a file in tmpRoot to confirm fs-list returns directories only.
	if err := os.WriteFile(filepath.Join(tmpRoot, "ignored.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	listEnv, _ := protocol.NewEnvelope(protocol.TypeFSList, "", "", protocol.FSListPayload{Root: tmpRoot})
	listReply, err := h.SendRequest(ctx, "fs-runner", listEnv)
	if err != nil {
		t.Fatalf("fs-list SendRequest error = %v", err)
	}
	var listAck protocol.FSListAckPayload
	if err := listReply.DecodePayload(&listAck); err != nil {
		t.Fatalf("decode list ack: %v", err)
	}
	if len(listAck.Entries) != 1 || listAck.Entries[0].Name != "demo-proj" {
		t.Fatalf("listAck.Entries = %+v, want [demo-proj]", listAck.Entries)
	}

	execEnv, _ := protocol.NewEnvelope(protocol.TypeExecSync, "", "", protocol.ExecSyncPayload{
		Cwd:        projDir,
		Command:    "echo runner_exec_ok",
		TimeoutSec: 5,
	})
	execReply, err := h.SendRequest(ctx, "fs-runner", execEnv)
	if err != nil {
		t.Fatalf("exec-sync SendRequest error = %v", err)
	}
	if execReply.Type != protocol.TypeAck {
		var e protocol.ErrorPayload
		_ = json.Unmarshal(execReply.Payload, &e)
		t.Fatalf("exec-sync reply = %q (%s)", execReply.Type, e.Message)
	}
	var execAck protocol.ExecSyncAckPayload
	if err := execReply.DecodePayload(&execAck); err != nil {
		t.Fatalf("decode exec ack: %v", err)
	}
	if execAck.ExitCode != 0 || !strings.Contains(execAck.Output, "runner_exec_ok") {
		t.Fatalf("unexpected execAck: %+v", execAck)
	}

	delEnv, _ := protocol.NewEnvelope(protocol.TypeFSDelete, "", "", protocol.FSDeletePayload{Path: projDir})
	delReply, err := h.SendRequest(ctx, "fs-runner", delEnv)
	if err != nil {
		t.Fatalf("fs-delete SendRequest error = %v", err)
	}
	if delReply.Type != protocol.TypeAck {
		t.Fatalf("fs-delete reply type = %q, want ack (payload=%s)", delReply.Type, string(delReply.Payload))
	}
	if _, err := os.Stat(projDir); !os.IsNotExist(err) {
		t.Fatalf("expected %q to be deleted, stat err = %v", projDir, err)
	}

	probeEnv, _ := protocol.NewEnvelope(protocol.TypeToolsProbe, "", "", protocol.ToolsProbePayload{
		Commands: []string{"__shell__", "nonexistent-cli-xyz"},
	})
	probeReply, err := h.SendRequest(ctx, "fs-runner", probeEnv)
	if err != nil {
		t.Fatalf("tools-probe SendRequest error = %v", err)
	}
	if probeReply.Type != protocol.TypeAck {
		t.Fatalf("tools-probe reply type = %q, want ack", probeReply.Type)
	}
	var probeAck protocol.ToolsProbeAckPayload
	if err := probeReply.DecodePayload(&probeAck); err != nil {
		t.Fatalf("decode tools-probe ack: %v", err)
	}
	if !probeAck.Tools["__shell__"].Installed || probeAck.Tools["__shell__"].Path == "" {
		t.Fatalf("expected __shell__ installed with path, got %+v", probeAck.Tools["__shell__"])
	}
	if probeAck.Tools["nonexistent-cli-xyz"].Installed || probeAck.Tools["nonexistent-cli-xyz"].Path != "" {
		t.Fatalf("expected nonexistent-cli-xyz uninstalled, got %+v", probeAck.Tools["nonexistent-cli-xyz"])
	}

	badSessEnv, _ := protocol.NewEnvelope(protocol.TypeSessionCreate, "sess-bad-cli", "", protocol.SessionCreatePayload{
		Command: "nonexistent-cli-xyz",
	})
	badSessReply, err := h.SendRequest(ctx, "fs-runner", badSessEnv)
	if err != nil {
		t.Fatalf("session-create SendRequest error = %v", err)
	}
	if badSessReply.Type != protocol.TypeError {
		t.Fatalf("session-create reply type = %q, want error", badSessReply.Type)
	}
	var errPayload protocol.ErrorPayload
	if err := badSessReply.DecodePayload(&errPayload); err != nil {
		t.Fatalf("decode error payload: %v", err)
	}
	if !strings.Contains(errPayload.Message, "is not installed on this runner") {
		t.Fatalf("unexpected error message: %q", errPayload.Message)
	}
}

func TestClientFSListBrowser(t *testing.T) {
	connectedCh := make(chan struct{}, 1)
	h := hub.New(hub.Options{
		Hooks: hub.Hooks{
			OnRunnerConnect: func(string, string, []string) {
				select {
				case connectedCh <- struct{}{}:
				default:
				}
			},
		},
	})
	defer h.Close()

	srv := httptest.NewServer(http.HandlerFunc(h.HandleRunnerWS))
	defer srv.Close()

	tmpRoot := t.TempDir()
	for _, rel := range []string{"zeta", "Alpha", ".hidden", "beta", filepath.Join("Alpha", "nested")} {
		if err := os.MkdirAll(filepath.Join(tmpRoot, rel), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", rel, err)
		}
	}
	if err := os.WriteFile(filepath.Join(tmpRoot, "file.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	client, err := NewClient(Config{
		ServerURL:      srv.URL,
		Token:          "tok",
		MachineName:    "browse-runner",
		Roots:          []string{tmpRoot},
		InitialBackoff: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer func() { _ = client.PTYManager().CloseAll() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() { _ = client.Run(ctx) }()

	<-connectedCh

	// 1. Empty root defaults to first configured runner root, returning sorted subdirs (dot-dirs last).
	defaultEnv, _ := protocol.NewEnvelope(protocol.TypeFSList, "", "", protocol.FSListPayload{Root: ""})
	defaultReply, err := h.SendRequest(ctx, "browse-runner", defaultEnv)
	if err != nil {
		t.Fatalf("fs-list default SendRequest error = %v", err)
	}
	if defaultReply.Type != protocol.TypeAck {
		t.Fatalf("fs-list default reply = %q (%s)", defaultReply.Type, string(defaultReply.Payload))
	}
	var defaultAck protocol.FSListAckPayload
	if err := defaultReply.DecodePayload(&defaultAck); err != nil {
		t.Fatalf("decode default list ack: %v", err)
	}
	if defaultAck.CurrentPath != filepath.Clean(tmpRoot) {
		t.Errorf("CurrentPath = %q, want %q", defaultAck.CurrentPath, filepath.Clean(tmpRoot))
	}
	if defaultAck.ParentPath != filepath.Dir(filepath.Clean(tmpRoot)) {
		t.Errorf("ParentPath = %q, want %q", defaultAck.ParentPath, filepath.Dir(filepath.Clean(tmpRoot)))
	}
	gotNames := make([]string, 0, len(defaultAck.Entries))
	for _, e := range defaultAck.Entries {
		gotNames = append(gotNames, e.Name)
	}
	wantNames := []string{"Alpha", "beta", "zeta", ".hidden"}
	if !slices.Equal(gotNames, wantNames) {
		t.Fatalf("entry names = %v, want %v", gotNames, wantNames)
	}

	// 2. Nested directory returns its parentPath pointing back to tmpRoot.
	alphaDir := filepath.Join(tmpRoot, "Alpha")
	subEnv, _ := protocol.NewEnvelope(protocol.TypeFSList, "", "", protocol.FSListPayload{Root: alphaDir})
	subReply, err := h.SendRequest(ctx, "browse-runner", subEnv)
	if err != nil {
		t.Fatalf("fs-list nested SendRequest error = %v", err)
	}
	var subAck protocol.FSListAckPayload
	if err := subReply.DecodePayload(&subAck); err != nil {
		t.Fatalf("decode nested list ack: %v", err)
	}
	if subAck.CurrentPath != alphaDir || subAck.ParentPath != filepath.Clean(tmpRoot) {
		t.Fatalf("nested ack paths = (current=%q, parent=%q), want (%q, %q)", subAck.CurrentPath, subAck.ParentPath, alphaDir, filepath.Clean(tmpRoot))
	}
	if len(subAck.Entries) != 1 || subAck.Entries[0].Name != "nested" {
		t.Fatalf("nested entries = %+v, want [nested]", subAck.Entries)
	}

	// 3. On Windows, __drives__ lists available drive roots.
	if runtime.GOOS == "windows" {
		drivesEnv, _ := protocol.NewEnvelope(protocol.TypeFSList, "", "", protocol.FSListPayload{Root: "__drives__"})
		drivesReply, err := h.SendRequest(ctx, "browse-runner", drivesEnv)
		if err != nil {
			t.Fatalf("fs-list __drives__ SendRequest error = %v", err)
		}
		if drivesReply.Type != protocol.TypeAck {
			t.Fatalf("fs-list __drives__ reply = %q (%s)", drivesReply.Type, string(drivesReply.Payload))
		}
		var drivesAck protocol.FSListAckPayload
		if err := drivesReply.DecodePayload(&drivesAck); err != nil {
			t.Fatalf("decode drives ack: %v", err)
		}
		if drivesAck.CurrentPath != "__drives__" || drivesAck.ParentPath != "" || len(drivesAck.Entries) == 0 {
			t.Fatalf("unexpected drivesAck: %+v", drivesAck)
		}
		// Drive root parentPath points to __drives__.
		firstDrive := drivesAck.Entries[0].Path
		driveEnv, _ := protocol.NewEnvelope(protocol.TypeFSList, "", "", protocol.FSListPayload{Root: firstDrive})
		driveReply, err := h.SendRequest(ctx, "browse-runner", driveEnv)
		if err != nil {
			t.Fatalf("fs-list drive SendRequest error = %v", err)
		}
		if driveReply.Type == protocol.TypeAck {
			var driveAck protocol.FSListAckPayload
			if err := driveReply.DecodePayload(&driveAck); err != nil {
				t.Fatalf("decode drive ack: %v", err)
			}
			if driveAck.ParentPath != "__drives__" {
				t.Errorf("drive %q ParentPath = %q, want __drives__", firstDrive, driveAck.ParentPath)
			}
		}
	}
}
