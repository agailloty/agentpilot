package hub

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/agailloty/agentpilot/server/internal/protocol"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func wsURL(httpURL string) string {
	return "ws" + httpURL[len("http"):]
}

func TestHubConnectRequestFireAndForgetAndHooks(t *testing.T) {
	var (
		mu             sync.Mutex
		connected      []string
		disconnected   []string
		outputChunks   = make(map[string]string)
		sessionExits   = make(map[string]int)
		syncedSessions []string
	)
	connectedCh := make(chan struct{}, 1)
	disconnectedCh := make(chan struct{}, 1)
	outputCh := make(chan struct{}, 4)
	exitCh := make(chan struct{}, 1)
	syncCh := make(chan struct{}, 1)

	h := New(Options{
		Logger:       testLogger(),
		PingInterval: 50 * time.Millisecond,
		PingTimeout:  500 * time.Millisecond,
		WriteTimeout: time.Second,
		Hooks: Hooks{
			OnRunnerConnect: func(machineName, os string, roots []string) {
				mu.Lock()
				connected = append(connected, machineName+":"+os)
				mu.Unlock()
				select {
				case connectedCh <- struct{}{}:
				default:
				}
			},
			OnRunnerDisconnect: func(machineName string) {
				mu.Lock()
				disconnected = append(disconnected, machineName)
				mu.Unlock()
				select {
				case disconnectedCh <- struct{}{}:
				default:
				}
			},
			OnOutputChunk: func(sessionID string, data []byte) {
				mu.Lock()
				outputChunks[sessionID] += string(data)
				mu.Unlock()
				select {
				case outputCh <- struct{}{}:
				default:
				}
			},
			OnSessionExit: func(sessionID string, exitCode int) {
				mu.Lock()
				sessionExits[sessionID] = exitCode
				mu.Unlock()
				select {
				case exitCh <- struct{}{}:
				default:
				}
			},
			OnSessionSync: func(_ string, activeSessionIDs []string) {
				mu.Lock()
				syncedSessions = append([]string(nil), activeSessionIDs...)
				mu.Unlock()
				select {
				case syncCh <- struct{}{}:
				default:
				}
			},
		},
	})
	defer h.Close()

	srv := httptest.NewServer(http.HandlerFunc(h.HandleRunnerWS))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	headers := http.Header{
		"X-Machine-Name":  []string{"workstation"},
		"X-Machine-OS":    []string{"linux"},
		"X-Project-Roots": []string{`["/srv/code","/home/me/repos"]`},
	}
	conn, _, err := websocket.Dial(ctx, wsURL(srv.URL), &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer func() { _ = conn.CloseNow() }()

	select {
	case <-connectedCh:
	case <-ctx.Done():
		t.Fatal("timed out waiting for OnRunnerConnect")
	}

	info, ok := h.GetRunner("workstation")
	if !ok {
		t.Fatal("GetRunner(workstation) = false, want true")
	}
	if info.OS != "linux" || !slices.Equal(info.Roots, []string{"/srv/code", "/home/me/repos"}) {
		t.Fatalf("unexpected runner info: %+v", info)
	}
	if list := h.ListRunners(); len(list) != 1 || list[0].MachineName != "workstation" {
		t.Fatalf("ListRunners() = %+v, want [workstation]", list)
	}

	// Fake runner message loop handling session-create and input-write.
	runnerDone := make(chan struct{})
	go func() {
		defer close(runnerDone)
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			env, err := protocol.UnmarshalEnvelope(data)
			if err != nil {
				continue
			}
			switch env.Type {
			case protocol.TypeSessionCreate:
				ack, _ := protocol.NewEnvelope(protocol.TypeAck, env.SessionID, env.CorrelationID, nil)
				raw, _ := ack.Marshal()
				_ = conn.Write(ctx, websocket.MessageText, raw)
			case protocol.TypeInputWrite:
				var p protocol.DataPayload
				_ = env.DecodePayload(&p)
				chunk, _ := protocol.NewEnvelope(protocol.TypeOutputChunk, env.SessionID, "", protocol.DataPayload{Data: p.Data})
				raw, _ := chunk.Marshal()
				_ = conn.Write(ctx, websocket.MessageText, raw)

				exitEnv, _ := protocol.NewEnvelope(protocol.TypeSessionExit, env.SessionID, "", protocol.SessionExitPayload{ExitCode: 7})
				rawExit, _ := exitEnv.Marshal()
				_ = conn.Write(ctx, websocket.MessageText, rawExit)
			}
		}
	}()

	// Send session-sync from fake runner.
	syncEnv, _ := protocol.NewEnvelope(protocol.TypeSessionSync, "", "", protocol.SessionSyncPayload{ActiveSessionIDs: []string{"s-1"}})
	syncBytes, _ := syncEnv.Marshal()
	if err := conn.Write(ctx, websocket.MessageText, syncBytes); err != nil {
		t.Fatalf("write session-sync error = %v", err)
	}
	select {
	case <-syncCh:
	case <-ctx.Done():
		t.Fatal("timed out waiting for OnSessionSync")
	}

	// Correlated SendRequest.
	createEnv, _ := protocol.NewEnvelope(protocol.TypeSessionCreate, "s-1", "", protocol.SessionCreatePayload{Command: "bash"})
	reply, err := h.SendRequest(ctx, "workstation", createEnv)
	if err != nil {
		t.Fatalf("SendRequest() error = %v", err)
	}
	if reply.Type != protocol.TypeAck || reply.SessionID != "s-1" || reply.CorrelationID == "" {
		t.Fatalf("unexpected reply envelope: %+v", reply)
	}

	// Fire-and-forget input-write.
	inEnv, _ := protocol.NewEnvelope(protocol.TypeInputWrite, "s-1", "", protocol.DataPayload{Data: []byte("pong")})
	if err := h.SendFireAndForget("workstation", inEnv); err != nil {
		t.Fatalf("SendFireAndForget() error = %v", err)
	}

	select {
	case <-outputCh:
	case <-ctx.Done():
		t.Fatal("timed out waiting for OnOutputChunk")
	}
	select {
	case <-exitCh:
	case <-ctx.Done():
		t.Fatal("timed out waiting for OnSessionExit")
	}

	// Close runner connection and verify disconnect hook.
	_ = conn.Close(websocket.StatusNormalClosure, "bye")
	<-runnerDone

	select {
	case <-disconnectedCh:
	case <-ctx.Done():
		t.Fatal("timed out waiting for OnRunnerDisconnect")
	}

	if _, ok := h.GetRunner("workstation"); ok {
		t.Fatal("expected workstation to be removed after disconnect")
	}

	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(connected, []string{"workstation:linux"}) {
		t.Errorf("connected = %v", connected)
	}
	if !slices.Equal(disconnected, []string{"workstation"}) {
		t.Errorf("disconnected = %v", disconnected)
	}
	if outputChunks["s-1"] != "pong" {
		t.Errorf("outputChunks[s-1] = %q, want %q", outputChunks["s-1"], "pong")
	}
	if sessionExits["s-1"] != 7 {
		t.Errorf("sessionExits[s-1] = %d, want 7", sessionExits["s-1"])
	}
	if !slices.Equal(syncedSessions, []string{"s-1"}) {
		t.Errorf("syncedSessions = %v, want [s-1]", syncedSessions)
	}
}

func TestHubSendRequestErrorsAndTimeout(t *testing.T) {
	h := New(Options{Logger: testLogger()})
	defer h.Close()

	ctx := context.Background()
	req, _ := protocol.NewEnvelope(protocol.TypeFSList, "", "", protocol.FSListPayload{Root: "/tmp"})

	if _, err := h.SendRequest(ctx, "missing", req); !errors.Is(err, ErrRunnerNotFound) {
		t.Fatalf("SendRequest(missing) error = %v, want ErrRunnerNotFound", err)
	}
	if err := h.SendFireAndForget("missing", req); !errors.Is(err, ErrRunnerNotFound) {
		t.Fatalf("SendFireAndForget(missing) error = %v, want ErrRunnerNotFound", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(h.HandleRunnerWS))
	defer srv.Close()

	connectedCh := make(chan struct{}, 1)
	h.SetHooks(Hooks{
		OnRunnerConnect: func(string, string, []string) {
			select {
			case connectedCh <- struct{}{}:
			default:
			}
		},
	})

	conn, _, err := websocket.Dial(ctx, wsURL(srv.URL), &websocket.DialOptions{
		HTTPHeader: http.Header{"X-Machine-Name": []string{"silent-box"}},
	})
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer func() { _ = conn.CloseNow() }()

	<-connectedCh

	timeoutCtx, cancel := context.WithTimeout(ctx, 60*time.Millisecond)
	defer cancel()

	// Runner does not answer, so SendRequest must time out cleanly.
	if _, err := h.SendRequest(timeoutCtx, "silent-box", req); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("SendRequest(timeout) error = %v, want context.DeadlineExceeded", err)
	}

	// Disconnect while SendRequest is waiting should return ErrRunnerDisconnected.
	errCh := make(chan error, 1)
	go func() {
		waitCtx, waitCancel := context.WithTimeout(ctx, 2*time.Second)
		defer waitCancel()
		_, err := h.SendRequest(waitCtx, "silent-box", req)
		errCh <- err
	}()

	time.Sleep(20 * time.Millisecond)
	h.DisconnectRunner("silent-box")

	if err := <-errCh; !errors.Is(err, ErrRunnerDisconnected) {
		t.Fatalf("SendRequest(disconnected) error = %v, want ErrRunnerDisconnected", err)
	}
}

func TestHubRejectsMissingMachineNameAndInvalidRoots(t *testing.T) {
	h := New(Options{Logger: testLogger()})
	srv := httptest.NewServer(http.HandlerFunc(h.HandleRunnerWS))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing X-Machine-Name status = %d, want 400", resp.StatusCode)
	}

	reqBadRoots, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	reqBadRoots.Header.Set("X-Machine-Name", "pc")
	reqBadRoots.Header.Set("X-Project-Roots", "not-json")
	resp2, err := http.DefaultClient.Do(reqBadRoots)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid X-Project-Roots status = %d, want 400", resp2.StatusCode)
	}
}
