package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/agailloty/agentpilot/server/internal/api"
	"github.com/agailloty/agentpilot/server/internal/auth"
	"github.com/agailloty/agentpilot/server/internal/config"
	"github.com/agailloty/agentpilot/server/internal/hub"
	"github.com/agailloty/agentpilot/server/internal/manifests"
	"github.com/agailloty/agentpilot/server/internal/protocol"
	"github.com/agailloty/agentpilot/server/internal/runner"
	"github.com/agailloty/agentpilot/server/internal/store"
)

func doAuthJSONRequest(t *testing.T, ctx context.Context, method, url, token string, body any) *http.Response {
	t.Helper()
	var bodyReader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		bodyReader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		t.Fatalf("http.NewRequestWithContext() error = %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("http.Do(%s %s) error = %v", method, url, err)
	}
	return resp
}

func dialMobileSessionWS(t *testing.T, ctx context.Context, baseURL, sessionID, token string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(baseURL, "http") + "/ws/sessions/" + sessionID
	conn, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Authorization": []string{"Bearer " + token},
		},
	})
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("websocket.Dial(%s) error = %v (status=%d)", wsURL, err, status)
	}
	conn.SetReadLimit(4 << 20)
	return conn
}

func readMobileFrame(t *testing.T, ctx context.Context, conn *websocket.Conn) protocol.MobileFrame {
	t.Helper()
	_, raw, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("ws.Read() error = %v", err)
	}
	frame, err := protocol.UnmarshalMobileFrame(raw)
	if err != nil {
		t.Fatalf("UnmarshalMobileFrame() error = %v (raw=%s)", err, string(raw))
	}
	return frame
}

func TestM2SessionsStreamingExecAndTranscriptEndToEnd(t *testing.T) {
	const (
		mobileToken = "m2-mobile-secret"
		runnerToken = "m2-runner-secret"
		machineName = "m2-homelab-box"
	)

	dataDir := t.TempDir()
	dbStore, err := store.Open(filepath.Join(dataDir, "agentpilot.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer func() { _ = dbStore.Close() }()

	if err := auth.BootstrapTokens(context.Background(), dbStore, []config.Token{
		{Name: "mobile", Scope: config.ScopeMobile, Value: mobileToken},
		{Name: "runner", Scope: config.ScopeRunner, Value: runnerToken},
	}); err != nil {
		t.Fatalf("BootstrapTokens() error = %v", err)
	}

	loadedManifests, err := manifests.LoadDir(filepath.Join("..", "..", "..", "docs", "AgentManifests"))
	if err != nil {
		t.Fatalf("manifests.LoadDir() error = %v", err)
	}
	if err := manifests.SyncToStore(context.Background(), dbStore, loadedManifests); err != nil {
		t.Fatalf("manifests.SyncToStore() error = %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	runnerHub := hub.New(hub.Options{Logger: logger})
	defer runnerHub.Close()

	apiSrv := api.NewServer(dbStore, runnerHub, dataDir, logger)
	runnerHub.SetHooks(hub.Hooks{
		OnRunnerConnect: func(name, osName string, roots []string) {
			_, _ = dbStore.UpsertMachine(context.Background(), store.Machine{
				Name:       name,
				OS:         osName,
				Roots:      roots,
				Online:     true,
				LastSeenAt: time.Now().UTC(),
			})
		},
		OnRunnerDisconnect: func(name string) {
			_ = dbStore.SetMachineOnline(context.Background(), name, false, time.Now().UTC())
		},
		OnOutputChunk: apiSrv.HandleOutputChunk,
		OnSessionExit: apiSrv.HandleSessionExit,
		OnChatDelta:   apiSrv.HandleChatDelta,
		OnChatDone:    apiSrv.HandleChatDone,
	})

	authn := auth.NewAuthenticator(dbStore)
	mux := http.NewServeMux()
	mux.Handle("GET /ws/runner", authn.RequireScope(config.ScopeRunner)(http.HandlerFunc(runnerHub.HandleRunnerWS)))
	apiSrv.RegisterRoutes(mux, authn)

	srv := httptest.NewServer(authn.RequireAuth()(mux))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 1. Unauthenticated requests to /api/sessions, /ws/sessions/..., /api/projects/.../exec return 401.
	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/sessions"},
		{http.MethodPost, "/api/sessions"},
		{http.MethodGet, "/ws/sessions/nonexistent"},
		{http.MethodPost, "/api/projects/nonexistent/exec"},
	} {
		resp := doAuthJSONRequest(t, ctx, tc.method, srv.URL+tc.path, "", nil)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("unauthenticated %s %s status = %d, want 401", tc.method, tc.path, resp.StatusCode)
		}
	}

	// Start runner client and wait for connection.
	projectsRoot := t.TempDir()
	client, err := runner.NewClient(runner.Config{
		ServerURL:      srv.URL,
		Token:          runnerToken,
		MachineName:    machineName,
		Roots:          []string{projectsRoot},
		Logger:         logger,
		InitialBackoff: 15 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("runner.NewClient() error = %v", err)
	}
	defer func() { _ = client.PTYManager().CloseAll() }()

	runnerDone := make(chan error, 1)
	go func() {
		runnerDone <- client.Run(ctx)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := runnerHub.GetRunner(machineName); ok {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if _, ok := runnerHub.GetRunner(machineName); !ok {
		t.Fatal("runner did not connect in time")
	}

	// 2. POST /api/projects creates a project on runner.
	projResp := doAuthJSONRequest(t, ctx, http.MethodPost, srv.URL+"/api/projects", mobileToken, map[string]any{
		"name":        "m2-project",
		"machineName": machineName,
		"parentPath":  projectsRoot,
		"gitInit":     false,
	})
	if projResp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(projResp.Body)
		_ = projResp.Body.Close()
		t.Fatalf("POST /api/projects status = %d, want 201 (body=%s)", projResp.StatusCode, string(raw))
	}
	var project struct {
		ID   string `json:"id"`
		Path string `json:"path"`
	}
	if err := json.NewDecoder(projResp.Body).Decode(&project); err != nil {
		_ = projResp.Body.Close()
		t.Fatalf("decode project: %v", err)
	}
	_ = projResp.Body.Close()

	// 3. POST /api/sessions opens a shell session.
	sessResp := doAuthJSONRequest(t, ctx, http.MethodPost, srv.URL+"/api/sessions", mobileToken, map[string]any{
		"projectId": project.ID,
		"agentId":   "shell",
		"cols":      80,
		"rows":      24,
	})
	if sessResp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(sessResp.Body)
		_ = sessResp.Body.Close()
		t.Fatalf("POST /api/sessions status = %d, want 201 (body=%s)", sessResp.StatusCode, string(raw))
	}
	var createdSession struct {
		ID              string `json:"id"`
		ProjectID       string `json:"projectId"`
		MachineName     string `json:"machineName"`
		AgentID         string `json:"agentId"`
		Cwd             string `json:"cwd"`
		Status          string `json:"status"`
		WaitingApproval bool   `json:"waitingApproval"`
	}
	if err := json.NewDecoder(sessResp.Body).Decode(&createdSession); err != nil {
		_ = sessResp.Body.Close()
		t.Fatalf("decode session: %v", err)
	}
	_ = sessResp.Body.Close()
	if createdSession.Status != "running" || createdSession.AgentID != "shell" || createdSession.ProjectID != project.ID {
		t.Fatalf("unexpected created session: %+v", createdSession)
	}

	// Verify GET /api/sessions filters return the running session.
	listResp := doAuthJSONRequest(t, ctx, http.MethodGet, srv.URL+"/api/sessions?projectId="+project.ID+"&machine="+machineName+"&status=running", mobileToken, nil)
	if listResp.StatusCode != http.StatusOK {
		_ = listResp.Body.Close()
		t.Fatalf("GET /api/sessions status = %d, want 200", listResp.StatusCode)
	}
	var listed []struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&listed); err != nil {
		_ = listResp.Body.Close()
		t.Fatalf("decode GET /api/sessions: %v", err)
	}
	_ = listResp.Body.Close()
	if len(listed) != 1 || listed[0].ID != createdSession.ID {
		t.Fatalf("GET /api/sessions filtered = %+v, want [%s]", listed, createdSession.ID)
	}

	// 4. Mobile connects to WS /ws/sessions/{id}, sends structured chat turn `echo m2_session_ok`, receives chat-delta + chat-done containing `m2_session_ok`.
	ws1 := dialMobileSessionWS(t, ctx, srv.URL, createdSession.ID, mobileToken)
	initialFrame := readMobileFrame(t, ctx, ws1)
	if initialFrame.Type != protocol.MobileTypeChatSync {
		t.Fatalf("first WS frame type = %q, want %q", initialFrame.Type, protocol.MobileTypeChatSync)
	}

	msgResp := doAuthJSONRequest(t, ctx, http.MethodPost, srv.URL+"/api/sessions/"+createdSession.ID+"/messages", mobileToken, map[string]any{
		"content": "echo m2_session_ok",
	})
	if msgResp.StatusCode != http.StatusAccepted {
		raw, _ := io.ReadAll(msgResp.Body)
		_ = msgResp.Body.Close()
		t.Fatalf("POST /api/sessions/{id}/messages status = %d, want 202 (body=%s)", msgResp.StatusCode, string(raw))
	}
	var sentTurn struct {
		AssistantMessage struct {
			ID string `json:"id"`
		} `json:"assistantMessage"`
	}
	if err := json.NewDecoder(msgResp.Body).Decode(&sentTurn); err != nil {
		_ = msgResp.Body.Close()
		t.Fatalf("decode sentTurn: %v", err)
	}
	_ = msgResp.Body.Close()

	var liveOutput strings.Builder
	for {
		frame := readMobileFrame(t, ctx, ws1)
		if frame.Type == protocol.MobileTypeChatDelta && frame.MessageID == sentTurn.AssistantMessage.ID {
			liveOutput.WriteString(frame.Delta)
		}
		if frame.Type == protocol.MobileTypeChatDone && frame.MessageID == sentTurn.AssistantMessage.ID {
			if !strings.Contains(frame.Content, "m2_session_ok") {
				t.Fatalf("chat-done content = %q, want substring m2_session_ok", frame.Content)
			}
			break
		}
	}
	if !strings.Contains(liveOutput.String(), "m2_session_ok") {
		t.Fatalf("streamed deltas = %q, want substring m2_session_ok", liveOutput.String())
	}

	// 5. Mobile disconnects and reconnects to WS /ws/sessions/{id}, receives chat-sync containing persisted `m2_session_ok`.
	_ = ws1.Close(websocket.StatusNormalClosure, "reconnect test")

	ws2 := dialMobileSessionWS(t, ctx, srv.URL, createdSession.ID, mobileToken)
	syncFrame := readMobileFrame(t, ctx, ws2)
	if syncFrame.Type != protocol.MobileTypeChatSync {
		t.Fatalf("reconnect first frame type = %q, want %q", syncFrame.Type, protocol.MobileTypeChatSync)
	}
	if len(syncFrame.Messages) != 2 || !strings.Contains(syncFrame.Messages[1].Content, "m2_session_ok") {
		t.Fatalf("reconnect chat-sync messages = %+v, want persisted assistant reply containing m2_session_ok", syncFrame.Messages)
	}
	_ = ws2.Close(websocket.StatusNormalClosure, "done")

	// 6. POST /api/projects/{id}/exec runs `echo m2_oneshot_ok` and returns exitCode: 0 and output containing `m2_oneshot_ok`.
	execResp := doAuthJSONRequest(t, ctx, http.MethodPost, srv.URL+"/api/projects/"+project.ID+"/exec", mobileToken, map[string]any{
		"command":    "echo m2_oneshot_ok",
		"timeoutSec": 10,
	})
	if execResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(execResp.Body)
		_ = execResp.Body.Close()
		t.Fatalf("POST /api/projects/{id}/exec status = %d, want 200 (body=%s)", execResp.StatusCode, string(raw))
	}
	var execResult struct {
		ExitCode   int    `json:"exitCode"`
		Output     string `json:"output"`
		DurationMs int64  `json:"durationMs"`
	}
	if err := json.NewDecoder(execResp.Body).Decode(&execResult); err != nil {
		_ = execResp.Body.Close()
		t.Fatalf("decode exec result: %v", err)
	}
	_ = execResp.Body.Close()
	if execResult.ExitCode != 0 || !strings.Contains(execResult.Output, "m2_oneshot_ok") {
		t.Fatalf("unexpected exec result: %+v", execResult)
	}

	// 7. DELETE /api/sessions/{id} closes session and persists closed status while retaining chat messages.
	delResp := doAuthJSONRequest(t, ctx, http.MethodDelete, srv.URL+"/api/sessions/"+createdSession.ID, mobileToken, nil)
	if delResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(delResp.Body)
		_ = delResp.Body.Close()
		t.Fatalf("DELETE /api/sessions/{id} status = %d, want 200 (body=%s)", delResp.StatusCode, string(raw))
	}
	_ = delResp.Body.Close()

	storedSess, err := dbStore.GetSession(ctx, createdSession.ID)
	if err != nil || storedSess.Status != "closed" {
		t.Fatalf("expected closed session in DB, got %+v (err=%v)", storedSess, err)
	}

	historyResp := doAuthJSONRequest(t, ctx, http.MethodGet, srv.URL+"/api/sessions/"+createdSession.ID+"/messages", mobileToken, nil)
	if historyResp.StatusCode != http.StatusOK {
		_ = historyResp.Body.Close()
		t.Fatalf("GET messages after close status = %d, want 200", historyResp.StatusCode)
	}
	var history []store.ChatMessage
	_ = json.NewDecoder(historyResp.Body).Decode(&history)
	_ = historyResp.Body.Close()
	if len(history) != 2 || !strings.Contains(history[1].Content, "m2_session_ok") {
		t.Fatalf("unexpected persisted message history: %+v", history)
	}

	cancel()
	if err := <-runnerDone; err != nil {
		t.Fatalf("runner.Run() error = %v", err)
	}
}
