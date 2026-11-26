package integration

import (
	"bytes"
	"context"
	"crypto/subtle"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agailloty/agentpilot/server/internal/hub"
	"github.com/agailloty/agentpilot/server/internal/protocol"
	"github.com/agailloty/agentpilot/server/internal/runner"
)

func newlineForOS() string {
	if runtime.GOOS == "windows" {
		return "\r\n"
	}
	return "\n"
}

func testAuthMiddleware(runnerToken string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scheme, tok, ok := strings.Cut(strings.TrimSpace(r.Header.Get("Authorization")), " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(tok)), []byte(runnerToken)) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func waitUntilOutputContains(t *testing.T, ctx context.Context, mu *sync.Mutex, buf *bytes.Buffer, notify <-chan struct{}, want string) {
	t.Helper()
	for {
		mu.Lock()
		got := buf.String()
		mu.Unlock()
		if strings.Contains(got, want) {
			return
		}
		select {
		case <-notify:
		case <-ctx.Done():
			mu.Lock()
			got = buf.String()
			mu.Unlock()
			t.Fatalf("timed out waiting for %q in output; got:\n%s", want, got)
		}
	}
}

func TestM1EndToEndPTYWebSocketReconnectAndCommands(t *testing.T) {
	const (
		authToken   = "m1-integration-secret"
		machineName = "homelab-pc"
		sessionID   = "sess-e2e-1"
	)

	var (
		mu             sync.Mutex
		outputs        = make(map[string]*bytes.Buffer)
		syncedSessions []string
		exitCodes      = make(map[string]int)
	)
	connectCh := make(chan struct{}, 8)
	disconnectCh := make(chan struct{}, 8)
	outputCh := make(chan struct{}, 64)
	syncCh := make(chan []string, 8)
	exitCh := make(chan string, 8)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := hub.New(hub.Options{
		Logger:       logger,
		PingInterval: 100 * time.Millisecond,
		PingTimeout:  time.Second,
		WriteTimeout: 2 * time.Second,
		Hooks: hub.Hooks{
			OnRunnerConnect: func(name, _ string, _ []string) {
				if name == machineName {
					select {
					case connectCh <- struct{}{}:
					default:
					}
				}
			},
			OnRunnerDisconnect: func(name string) {
				if name == machineName {
					select {
					case disconnectCh <- struct{}{}:
					default:
					}
				}
			},
			OnOutputChunk: func(sid string, data []byte) {
				mu.Lock()
				b, ok := outputs[sid]
				if !ok {
					b = &bytes.Buffer{}
					outputs[sid] = b
				}
				b.Write(data)
				mu.Unlock()
				select {
				case outputCh <- struct{}{}:
				default:
				}
			},
			OnSessionSync: func(name string, activeIDs []string) {
				if name != machineName {
					return
				}
				copied := append([]string(nil), activeIDs...)
				mu.Lock()
				syncedSessions = copied
				mu.Unlock()
				select {
				case syncCh <- copied:
				default:
				}
			},
			OnSessionExit: func(sid string, code int) {
				mu.Lock()
				exitCodes[sid] = code
				mu.Unlock()
				select {
				case exitCh <- sid:
				default:
				}
			},
		},
	})
	defer h.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws/runner", h.HandleRunnerWS)
	mux.HandleFunc("POST /debug/session", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	srv := httptest.NewServer(testAuthMiddleware(authToken, mux))
	defer srv.Close()

	// 1. Verify unauthenticated /ws/runner and /debug/session return 401.
	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/ws/runner"},
		{http.MethodPost, "/debug/session"},
	} {
		req, _ := http.NewRequest(tc.method, srv.URL+tc.path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s request error = %v", tc.method, tc.path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("unauthenticated %s %s status = %d, want 401", tc.method, tc.path, resp.StatusCode)
		}
	}

	// 2. Start runner.Client and wait for initial connection + session-sync.
	tmpRoot := t.TempDir()
	client, err := runner.NewClient(runner.Config{
		ServerURL:      srv.URL,
		Token:          authToken,
		MachineName:    machineName,
		Roots:          []string{tmpRoot},
		Logger:         logger,
		InitialBackoff: 25 * time.Millisecond,
		MaxBackoff:     100 * time.Millisecond,
		PingInterval:   100 * time.Millisecond,
		PingTimeout:    time.Second,
		WriteTimeout:   2 * time.Second,
	})
	if err != nil {
		t.Fatalf("runner.NewClient() error = %v", err)
	}
	defer func() { _ = client.PTYManager().CloseAll() }()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	runDone := make(chan error, 1)
	go func() {
		runDone <- client.Run(ctx)
	}()

	select {
	case <-connectCh:
	case <-ctx.Done():
		t.Fatal("timed out waiting for initial runner connect")
	}
	select {
	case <-syncCh:
	case <-ctx.Done():
		t.Fatal("timed out waiting for initial session-sync")
	}

	// 3. Create interactive shell session via hub and test echo roundtrip.
	createEnv, _ := protocol.NewEnvelope(
		protocol.TypeSessionCreate,
		sessionID,
		"",
		protocol.SessionCreatePayload{
			Command: "__shell__",
			Cwd:     tmpRoot,
			Cols:    80,
			Rows:    24,
		},
	)
	createReply, err := h.SendRequest(ctx, machineName, createEnv)
	if err != nil {
		t.Fatalf("session-create SendRequest error = %v", err)
	}
	if createReply.Type != protocol.TypeAck {
		t.Fatalf("session-create reply = %q (%s), want ack", createReply.Type, string(createReply.Payload))
	}

	nl := newlineForOS()
	in1, _ := protocol.NewEnvelope(protocol.TypeInputWrite, sessionID, "", protocol.DataPayload{
		Data: []byte("echo m1_roundtrip_ok" + nl),
	})
	if err := h.SendFireAndForget(machineName, in1); err != nil {
		t.Fatalf("SendFireAndForget(echo 1) error = %v", err)
	}

	mu.Lock()
	if _, ok := outputs[sessionID]; !ok {
		outputs[sessionID] = &bytes.Buffer{}
	}
	sessBuf := outputs[sessionID]
	mu.Unlock()
	waitUntilOutputContains(t, ctx, &mu, sessBuf, outputCh, "m1_roundtrip_ok")

	// 4. Force WS disconnect while PTY keeps running, write while disconnected, and verify replay + continued streaming.
	if !h.DisconnectRunner(machineName) {
		t.Fatal("DisconnectRunner() returned false")
	}
	select {
	case <-disconnectCh:
	case <-ctx.Done():
		t.Fatal("timed out waiting for runner disconnect")
	}

	// Write directly to the surviving local PTY while WS is down to verify disconnect ring buffer replay.
	localSess, ok := client.PTYManager().Get(sessionID)
	if !ok {
		t.Fatal("expected local PTY session to remain active while WS is disconnected")
	}
	if _, err := localSess.Write([]byte("echo during_disconnect_ok" + nl)); err != nil {
		t.Fatalf("localSess.Write during disconnect error = %v", err)
	}

	// Wait for runner to reconnect and emit session-sync containing sessionID.
	select {
	case <-connectCh:
	case <-ctx.Done():
		t.Fatal("timed out waiting for runner reconnect")
	}

	var reconnectedIDs []string
	select {
	case reconnectedIDs = <-syncCh:
	case <-ctx.Done():
		t.Fatal("timed out waiting for session-sync after reconnect")
	}
	if !slices.Contains(reconnectedIDs, sessionID) {
		mu.Lock()
		lastSync := syncedSessions
		mu.Unlock()
		t.Fatalf("session-sync after reconnect = %v (last=%v), want %q present", reconnectedIDs, lastSync, sessionID)
	}

	waitUntilOutputContains(t, ctx, &mu, sessBuf, outputCh, "during_disconnect_ok")

	// Continue sending input over the reconnected WebSocket.
	in2, _ := protocol.NewEnvelope(protocol.TypeInputWrite, sessionID, "", protocol.DataPayload{
		Data: []byte("echo after_reconnect_ok" + nl),
	})
	if err := h.SendFireAndForget(machineName, in2); err != nil {
		t.Fatalf("SendFireAndForget(echo 2) error = %v", err)
	}
	waitUntilOutputContains(t, ctx, &mu, sessBuf, outputCh, "after_reconnect_ok")

	// 5. Close session cleanly.
	closeEnv, _ := protocol.NewEnvelope(protocol.TypeSessionClose, sessionID, "", nil)
	closeReply, err := h.SendRequest(ctx, machineName, closeEnv)
	if err != nil {
		t.Fatalf("session-close SendRequest error = %v", err)
	}
	if closeReply.Type != protocol.TypeAck {
		t.Fatalf("session-close reply = %q, want ack", closeReply.Type)
	}

	select {
	case exitedID := <-exitCh:
		if exitedID != sessionID {
			t.Fatalf("OnSessionExit id = %q, want %q", exitedID, sessionID)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for OnSessionExit")
	}

	// 6. Test fs-mkdir and exec-sync end-to-end.
	workDir := filepath.Join(tmpRoot, "e2e-project")
	mkdirEnv, _ := protocol.NewEnvelope(protocol.TypeFSMkdir, "", "", protocol.FSMkdirPayload{
		Path:    workDir,
		GitInit: false,
	})
	mkdirReply, err := h.SendRequest(ctx, machineName, mkdirEnv)
	if err != nil {
		t.Fatalf("fs-mkdir SendRequest error = %v", err)
	}
	if mkdirReply.Type != protocol.TypeAck {
		t.Fatalf("fs-mkdir reply = %q (%s), want ack", mkdirReply.Type, string(mkdirReply.Payload))
	}
	if info, err := os.Stat(workDir); err != nil || !info.IsDir() {
		t.Fatalf("expected %q to exist as directory (err=%v)", workDir, err)
	}

	execEnv, _ := protocol.NewEnvelope(protocol.TypeExecSync, "", "", protocol.ExecSyncPayload{
		Cwd:        workDir,
		Command:    "echo exec_ok",
		TimeoutSec: 5,
	})
	execReply, err := h.SendRequest(ctx, machineName, execEnv)
	if err != nil {
		t.Fatalf("exec-sync SendRequest error = %v", err)
	}
	if execReply.Type != protocol.TypeAck {
		t.Fatalf("exec-sync reply = %q (%s), want ack", execReply.Type, string(execReply.Payload))
	}
	var execAck protocol.ExecSyncAckPayload
	if err := execReply.DecodePayload(&execAck); err != nil {
		t.Fatalf("decode exec-sync ack: %v", err)
	}
	if execAck.ExitCode != 0 || !strings.Contains(execAck.Output, "exec_ok") {
		t.Fatalf("unexpected exec-sync result: %+v", execAck)
	}

	cancel()
	if err := <-runDone; err != nil {
		t.Fatalf("client.Run() error = %v", err)
	}
}
