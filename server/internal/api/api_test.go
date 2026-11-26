package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/agailloty/agentpilot/server/internal/auth"
	"github.com/agailloty/agentpilot/server/internal/config"
	"github.com/agailloty/agentpilot/server/internal/hub"
	"github.com/agailloty/agentpilot/server/internal/manifests"
	"github.com/agailloty/agentpilot/server/internal/notify"
	"github.com/agailloty/agentpilot/server/internal/protocol"
	"github.com/agailloty/agentpilot/server/internal/runner"
	"github.com/agailloty/agentpilot/server/internal/store"
	"github.com/agailloty/agentpilot/server/internal/transcript"
)

type apiTestEnv struct {
	store     *store.Store
	hub       *hub.Hub
	authn     *auth.Authenticator
	apiSrv    *Server
	server    *httptest.Server
	dataDir   string
	mobileTok string
	runnerTok string
}

func setupAPITestEnv(t *testing.T) *apiTestEnv {
	return setupAPITestEnvWithOptions(t, ServerOptions{
		ApprovalIdleDuration: time.Duration(config.DefaultApprovalIdleMS) * time.Millisecond,
	})
}

func setupAPITestEnvWithOptions(t *testing.T, opts ServerOptions) *apiTestEnv {
	t.Helper()

	s, err := store.Open(filepath.Join(t.TempDir(), "api_test.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	mobileTok := "mobile-api-secret"
	runnerTok := "runner-api-secret"
	if err := auth.BootstrapTokens(context.Background(), s, []config.Token{
		{Name: "mobile", Scope: config.ScopeMobile, Value: mobileTok},
		{Name: "runner", Scope: config.ScopeRunner, Value: runnerTok},
	}); err != nil {
		t.Fatalf("BootstrapTokens() error = %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := hub.New(hub.Options{
		Logger: logger,
		Hooks: hub.Hooks{
			OnRunnerConnect: func(machineName, osName string, roots []string) {
				_, _ = s.UpsertMachine(context.Background(), store.Machine{
					Name:       machineName,
					OS:         osName,
					Roots:      roots,
					Online:     true,
					LastSeenAt: time.Now().UTC(),
				})
			},
			OnRunnerDisconnect: func(machineName string) {
				_ = s.SetMachineOnline(context.Background(), machineName, false, time.Now().UTC())
			},
		},
	})
	t.Cleanup(h.Close)

	manifestDir := filepath.Join("..", "..", "..", "docs", "AgentManifests")
	loaded, err := manifests.LoadDir(manifestDir)
	if err != nil {
		t.Fatalf("manifests.LoadDir() error = %v", err)
	}
	if err := manifests.SyncToStore(context.Background(), s, loaded); err != nil {
		t.Fatalf("manifests.SyncToStore() error = %v", err)
	}

	authn := auth.NewAuthenticator(s)
	if opts.DataDir == "" {
		opts.DataDir = t.TempDir()
	}
	if opts.Logger == nil {
		opts.Logger = logger
	}
	apiSrv := NewServerWithOptions(s, h, opts)
	h.SetHooks(hub.Hooks{
		OnRunnerConnect: func(machineName, osName string, roots []string) {
			_, _ = s.UpsertMachine(context.Background(), store.Machine{
				Name:       machineName,
				OS:         osName,
				Roots:      roots,
				Online:     true,
				LastSeenAt: time.Now().UTC(),
			})
		},
		OnRunnerDisconnect: func(machineName string) {
			_ = s.SetMachineOnline(context.Background(), machineName, false, time.Now().UTC())
		},
		OnOutputChunk: apiSrv.HandleOutputChunk,
		OnSessionExit: apiSrv.HandleSessionExit,
		OnChatDelta:   apiSrv.HandleChatDelta,
		OnChatDone:    apiSrv.HandleChatDone,
	})

	mux := http.NewServeMux()
	mux.Handle("GET /ws/runner", authn.RequireScope(config.ScopeRunner)(http.HandlerFunc(h.HandleRunnerWS)))
	apiSrv.RegisterRoutes(mux, authn)

	srv := httptest.NewServer(auth.CORS(authn.RequireAuth()(mux)))
	t.Cleanup(srv.Close)

	return &apiTestEnv{
		store:     s,
		hub:       h,
		authn:     authn,
		apiSrv:    apiSrv,
		server:    srv,
		dataDir:   opts.DataDir,
		mobileTok: mobileTok,
		runnerTok: runnerTok,
	}
}

func (e *apiTestEnv) doReq(t *testing.T, method, path, token string, body any) *http.Response {
	t.Helper()
	var bodyReader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		bodyReader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, e.server.URL+path, bodyReader)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("http.Do(%s %s) error = %v", method, path, err)
	}
	return resp
}

func (e *apiTestEnv) connectRunner(t *testing.T, machineName string, roots []string) context.CancelFunc {
	t.Helper()
	client, err := runner.NewClient(runner.Config{
		ServerURL:      e.server.URL,
		Token:          e.runnerTok,
		MachineName:    machineName,
		Roots:          roots,
		InitialBackoff: 15 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("runner.NewClient() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = client.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		_ = client.PTYManager().CloseAll()
		<-done
	})

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := e.hub.GetRunner(machineName); ok {
			return cancel
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("runner %q did not connect in time", machineName)
	return cancel
}

func TestAPIAuthEnforcedOnAllRoutes(t *testing.T) {
	env := setupAPITestEnv(t)

	routes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/machines"},
		{http.MethodGet, "/api/machines/some-box/fs"},
		{http.MethodGet, "/api/projects"},
		{http.MethodPost, "/api/projects"},
		{http.MethodDelete, "/api/projects/some-id"},
		{http.MethodPost, "/api/projects/some-id/exec"},
		{http.MethodGet, "/api/agents"},
		{http.MethodGet, "/api/agents/shell/commands"},
		{http.MethodGet, "/api/config/notifications"},
		{http.MethodGet, "/api/sessions"},
		{http.MethodPost, "/api/sessions"},
		{http.MethodDelete, "/api/sessions/some-id"},
		{http.MethodGet, "/api/sessions/some-id/transcript"},
		{http.MethodGet, "/api/sessions/some-id/messages"},
		{http.MethodPost, "/api/sessions/some-id/messages"},
		{http.MethodPost, "/api/sessions/some-id/cancel"},
		{http.MethodGet, "/api/sessions/some-id/command"},
		{http.MethodPatch, "/api/sessions/some-id/options"},
		{http.MethodPost, "/api/sessions/some-id/menu-action"},
		{http.MethodGet, "/ws/sessions/some-id"},
		{http.MethodGet, "/api/tokens"},
		{http.MethodPost, "/api/tokens"},
		{http.MethodDelete, "/api/tokens/some-id"},
	}

	for _, rt := range routes {
		for _, authCase := range []struct {
			name  string
			token string
		}{
			{"no token", ""},
			{"invalid token", "bogus-token"},
			{"runner token", env.runnerTok},
		} {
			t.Run(rt.method+" "+rt.path+" "+authCase.name, func(t *testing.T) {
				resp := env.doReq(t, rt.method, rt.path, authCase.token, nil)
				defer func() { _ = resp.Body.Close() }()
				if resp.StatusCode != http.StatusUnauthorized {
					t.Fatalf("status = %d, want 401", resp.StatusCode)
				}
			})
		}
	}
}

func TestMachinesAndProjectsLifecycle(t *testing.T) {
	env := setupAPITestEnv(t)

	// Offline machine persisted in DB should be returned with online=false.
	if _, err := env.store.UpsertMachine(context.Background(), store.Machine{
		Name:       "offline-box",
		OS:         "linux",
		Roots:      []string{"/srv/projects"},
		Online:     true, // Stale DB state; live hub is authoritative.
		LastSeenAt: time.Now().UTC().Add(-time.Hour),
	}); err != nil {
		t.Fatalf("UpsertMachine() error = %v", err)
	}

	rootDir := t.TempDir()
	cancelRunner := env.connectRunner(t, "online-box", []string{rootDir})

	// Wait until OnRunnerConnect upserts online-box in DB.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := env.store.GetMachineByName(context.Background(), "online-box"); err == nil {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}

	// GET /api/machines
	resp := env.doReq(t, http.MethodGet, "/api/machines", env.mobileTok, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/machines status = %d, want 200", resp.StatusCode)
	}
	var machines []machineResponse
	if err := json.NewDecoder(resp.Body).Decode(&machines); err != nil {
		_ = resp.Body.Close()
		t.Fatalf("decode machines: %v", err)
	}
	_ = resp.Body.Close()

	if len(machines) != 2 {
		t.Fatalf("len(machines) = %d, want 2: %+v", len(machines), machines)
	}
	byName := map[string]machineResponse{}
	for _, m := range machines {
		byName[m.Name] = m
	}
	if byName["offline-box"].Online {
		t.Error("expected offline-box to report online=false")
	}
	if !byName["online-box"].Online {
		t.Error("expected online-box to report online=true")
	}

	// Reject path traversal in POST /api/projects.
	badResp := env.doReq(t, http.MethodPost, "/api/projects", env.mobileTok, createProjectRequest{
		Name:        "../escape",
		MachineName: "online-box",
		ParentPath:  rootDir,
	})
	_ = badResp.Body.Close()
	if badResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("traversal status = %d, want 400", badResp.StatusCode)
	}

	// Reject creating project on offline machine.
	offlineResp := env.doReq(t, http.MethodPost, "/api/projects", env.mobileTok, createProjectRequest{
		Name:        "my-app",
		MachineName: "offline-box",
		ParentPath:  "/srv/projects",
	})
	_ = offlineResp.Body.Close()
	if offlineResp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("offline machine status = %d, want 503", offlineResp.StatusCode)
	}

	// POST /api/projects on online runner.
	createResp := env.doReq(t, http.MethodPost, "/api/projects", env.mobileTok, createProjectRequest{
		Name:        "alpha-proj",
		MachineName: "online-box",
		ParentPath:  rootDir,
		GitInit:     false,
	})
	if createResp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(createResp.Body)
		_ = createResp.Body.Close()
		t.Fatalf("POST /api/projects status = %d, want 201 (body=%s)", createResp.StatusCode, string(raw))
	}
	var created projectResponse
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		_ = createResp.Body.Close()
		t.Fatalf("decode created project: %v", err)
	}
	_ = createResp.Body.Close()

	expectedPath := filepath.Join(rootDir, "alpha-proj")
	if created.Path != expectedPath {
		t.Errorf("created.Path = %q, want %q", created.Path, expectedPath)
	}
	if _, err := os.Stat(expectedPath); err != nil {
		t.Fatalf("expected project folder %q on disk: %v", expectedPath, err)
	}

	// Create a second project on another machine in DB to test ?machine= filter.
	if _, err := env.store.CreateProject(context.Background(), store.Project{
		Name:        "beta-proj",
		MachineName: "offline-box",
		Path:        "/srv/projects/beta-proj",
	}); err != nil {
		t.Fatalf("CreateProject(beta) error = %v", err)
	}

	// GET /api/projects?machine=online-box
	listResp := env.doReq(t, http.MethodGet, "/api/projects?machine=online-box", env.mobileTok, nil)
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/projects?machine= status = %d, want 200", listResp.StatusCode)
	}
	var filtered []projectResponse
	if err := json.NewDecoder(listResp.Body).Decode(&filtered); err != nil {
		_ = listResp.Body.Close()
		t.Fatalf("decode filtered projects: %v", err)
	}
	_ = listResp.Body.Close()
	if len(filtered) != 1 || filtered[0].ID != created.ID {
		t.Fatalf("filtered projects = %+v, want [%s]", filtered, created.ID)
	}

	// DELETE /api/projects/{id}?deleteFiles=true
	delResp := env.doReq(t, http.MethodDelete, "/api/projects/"+created.ID+"?deleteFiles=true", env.mobileTok, nil)
	if delResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(delResp.Body)
		_ = delResp.Body.Close()
		t.Fatalf("DELETE /api/projects status = %d, want 200 (body=%s)", delResp.StatusCode, string(raw))
	}
	var delBody map[string]bool
	if err := json.NewDecoder(delResp.Body).Decode(&delBody); err != nil {
		_ = delResp.Body.Close()
		t.Fatalf("decode delete response: %v", err)
	}
	_ = delResp.Body.Close()
	if !delBody["deleted"] {
		t.Fatalf("expected deleted=true, got %+v", delBody)
	}
	if _, err := os.Stat(expectedPath); !os.IsNotExist(err) {
		t.Fatalf("expected project directory %q to be deleted, stat err = %v", expectedPath, err)
	}

	// Deleting again returns 404.
	notFoundResp := env.doReq(t, http.MethodDelete, "/api/projects/"+created.ID, env.mobileTok, nil)
	_ = notFoundResp.Body.Close()
	if notFoundResp.StatusCode != http.StatusNotFound {
		t.Fatalf("second DELETE status = %d, want 404", notFoundResp.StatusCode)
	}

	// Disconnect runner and verify deleteFiles=true fails with 503 when runner is offline,
	// whereas deleteFiles=false succeeds.
	cancelRunner()
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := env.hub.GetRunner("online-box"); !ok {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}

	betaListResp := env.doReq(t, http.MethodGet, "/api/projects?machine=offline-box", env.mobileTok, nil)
	var betaProjects []projectResponse
	_ = json.NewDecoder(betaListResp.Body).Decode(&betaProjects)
	_ = betaListResp.Body.Close()
	if len(betaProjects) != 1 {
		t.Fatalf("expected beta project in DB, got %+v", betaProjects)
	}

	delOfflineFilesResp := env.doReq(t, http.MethodDelete, "/api/projects/"+betaProjects[0].ID+"?deleteFiles=true", env.mobileTok, nil)
	_ = delOfflineFilesResp.Body.Close()
	if delOfflineFilesResp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("DELETE deleteFiles=true offline status = %d, want 503", delOfflineFilesResp.StatusCode)
	}

	delOfflineDBResp := env.doReq(t, http.MethodDelete, "/api/projects/"+betaProjects[0].ID, env.mobileTok, nil)
	_ = delOfflineDBResp.Body.Close()
	if delOfflineDBResp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE deleteFiles=false offline status = %d, want 200", delOfflineDBResp.StatusCode)
	}
}

func TestAgentsEndpoints(t *testing.T) {
	env := setupAPITestEnv(t)

	resp := env.doReq(t, http.MethodGet, "/api/agents", env.mobileTok, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/agents status = %d, want 200", resp.StatusCode)
	}
	var agents []agentResponse
	if err := json.NewDecoder(resp.Body).Decode(&agents); err != nil {
		_ = resp.Body.Close()
		t.Fatalf("decode agents: %v", err)
	}
	_ = resp.Body.Close()

	if len(agents) == 0 {
		t.Fatal("expected at least 1 agent manifest")
	}
	foundShell := false
	foundMistral := false
	for _, a := range agents {
		if a.AgentID == "shell" {
			foundShell = true
			if a.LaunchCommand != "__shell__" {
				t.Errorf("shell LaunchCommand = %q, want __shell__", a.LaunchCommand)
			}
			if a.ChatMode != manifests.ChatModeShell {
				t.Errorf("shell ChatMode = %q, want %q", a.ChatMode, manifests.ChatModeShell)
			}
		}
		if a.AgentID == "mistral" {
			foundMistral = true
			if a.ChatMode != manifests.ChatModeHeadless {
				t.Errorf("mistral ChatMode = %q, want %q", a.ChatMode, manifests.ChatModeHeadless)
			}
			if a.ProviderMenus == nil || len(a.ProviderMenus.Options) < 4 || len(a.ProviderMenus.InspectActions) < 4 {
				t.Errorf("mistral ProviderMenus incomplete: %+v", a.ProviderMenus)
			}
		}
	}
	if !foundShell || !foundMistral {
		t.Fatalf("expected shell and mistral in %+v", agents)
	}

	cmdResp := env.doReq(t, http.MethodGet, "/api/agents/shell/commands", env.mobileTok, nil)
	if cmdResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/agents/shell/commands status = %d, want 200", cmdResp.StatusCode)
	}
	var cmds []manifests.Command
	if err := json.NewDecoder(cmdResp.Body).Decode(&cmds); err != nil {
		_ = cmdResp.Body.Close()
		t.Fatalf("decode shell commands: %v", err)
	}
	_ = cmdResp.Body.Close()
	if len(cmds) == 0 {
		t.Fatal("expected shell commands to be non-empty")
	}

	missingResp := env.doReq(t, http.MethodGet, "/api/agents/does-not-exist/commands", env.mobileTok, nil)
	_ = missingResp.Body.Close()
	if missingResp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /api/agents/does-not-exist/commands status = %d, want 404", missingResp.StatusCode)
	}
}

func TestAgentsProbingAndUninstalledSessionGuard(t *testing.T) {
	env := setupAPITestEnv(t)
	ctx := context.Background()

	if err := manifests.SyncToStore(ctx, env.store, []manifests.Manifest{
		{
			AgentID:          "missing-cli",
			Name:             "Missing CLI",
			LaunchCommand:    "nonexistent-cli-xyz",
			Commands:         []manifests.Command{},
			ApprovalPatterns: []string{},
		},
	}); err != nil {
		t.Fatalf("SyncToStore(missing-cli) error = %v", err)
	}

	rootDir := t.TempDir()
	env.connectRunner(t, "probe-box", []string{rootDir})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := env.store.GetMachineByName(ctx, "probe-box"); err == nil {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}

	resp := env.doReq(t, http.MethodGet, "/api/agents", env.mobileTok, nil)
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		t.Fatalf("GET /api/agents status = %d, want 200", resp.StatusCode)
	}
	var agents []agentResponse
	if err := json.NewDecoder(resp.Body).Decode(&agents); err != nil {
		_ = resp.Body.Close()
		t.Fatalf("decode agents: %v", err)
	}
	_ = resp.Body.Close()

	byID := make(map[string]agentResponse, len(agents))
	for _, a := range agents {
		byID[a.AgentID] = a
	}

	shellAgent, ok := byID["shell"]
	if !ok {
		t.Fatalf("shell agent missing in %+v", agents)
	}
	if !shellAgent.Installed || shellAgent.ExecutablePath == "" {
		t.Fatalf("expected shell Installed=true and non-empty ExecutablePath, got %+v", shellAgent)
	}
	shellInst, ok := shellAgent.Installations["probe-box"]
	if !ok || !shellInst.Online || !shellInst.Installed || shellInst.Path != shellAgent.ExecutablePath {
		t.Fatalf("unexpected shell installation for probe-box: %+v", shellInst)
	}

	missingAgent, ok := byID["missing-cli"]
	if !ok {
		t.Fatalf("missing-cli agent missing in %+v", agents)
	}
	if missingAgent.Installed || missingAgent.ExecutablePath != "" {
		t.Fatalf("expected missing-cli Installed=false and empty ExecutablePath, got %+v", missingAgent)
	}
	missingInst, ok := missingAgent.Installations["probe-box"]
	if !ok || !missingInst.Online || missingInst.Installed || missingInst.Path != "" {
		t.Fatalf("unexpected missing-cli installation for probe-box: %+v", missingInst)
	}

	// Filtering by unknown machine marks top-level installed=false while preserving installations map.
	filteredResp := env.doReq(t, http.MethodGet, "/api/agents?machine=unknown-box", env.mobileTok, nil)
	if filteredResp.StatusCode != http.StatusOK {
		_ = filteredResp.Body.Close()
		t.Fatalf("GET /api/agents?machine=unknown-box status = %d, want 200", filteredResp.StatusCode)
	}
	var filteredAgents []agentResponse
	if err := json.NewDecoder(filteredResp.Body).Decode(&filteredAgents); err != nil {
		_ = filteredResp.Body.Close()
		t.Fatalf("decode filtered agents: %v", err)
	}
	_ = filteredResp.Body.Close()
	for _, a := range filteredAgents {
		if a.AgentID == "shell" && (a.Installed || a.ExecutablePath != "" || !a.Installations["probe-box"].Installed) {
			t.Fatalf("unexpected shell state for ?machine=unknown-box: %+v", a)
		}
	}

	// Create a project on probe-box and verify launching missing-cli returns 422 without inserting a session.
	projResp := env.doReq(t, http.MethodPost, "/api/projects", env.mobileTok, createProjectRequest{
		Name:        "guard-proj",
		MachineName: "probe-box",
		ParentPath:  rootDir,
	})
	if projResp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(projResp.Body)
		_ = projResp.Body.Close()
		t.Fatalf("POST /api/projects status = %d, want 201 (%s)", projResp.StatusCode, string(raw))
	}
	var proj projectResponse
	if err := json.NewDecoder(projResp.Body).Decode(&proj); err != nil {
		_ = projResp.Body.Close()
		t.Fatalf("decode project: %v", err)
	}
	_ = projResp.Body.Close()

	sessResp := env.doReq(t, http.MethodPost, "/api/sessions", env.mobileTok, createSessionRequest{
		ProjectID: proj.ID,
		AgentID:   "missing-cli",
	})
	sessRaw, _ := io.ReadAll(sessResp.Body)
	_ = sessResp.Body.Close()
	if sessResp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("POST /api/sessions for uninstalled CLI status = %d, want 422 (body=%s)", sessResp.StatusCode, string(sessRaw))
	}
	if !strings.Contains(string(sessRaw), "nonexistent-cli-xyz") {
		t.Errorf("expected 422 error body to mention command name, got %s", string(sessRaw))
	}

	storedSessions, err := env.store.ListSessionsFiltered(ctx, store.SessionFilter{ProjectID: proj.ID})
	if err != nil {
		t.Fatalf("ListSessionsFiltered() error = %v", err)
	}
	if len(storedSessions) != 0 {
		t.Fatalf("expected 0 sessions in DB after 422 guard, got %d: %+v", len(storedSessions), storedSessions)
	}
}

func TestNotificationConfigEndpoint(t *testing.T) {
	unconfigured := setupAPITestEnv(t)
	resp := unconfigured.doReq(t, http.MethodGet, "/api/config/notifications", unconfigured.mobileTok, nil)
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		t.Fatalf("GET /api/config/notifications status = %d, want 200", resp.StatusCode)
	}
	var cfgResp notificationConfigResponse
	if err := json.NewDecoder(resp.Body).Decode(&cfgResp); err != nil {
		_ = resp.Body.Close()
		t.Fatalf("decode notification config: %v", err)
	}
	_ = resp.Body.Close()
	if cfgResp.Enabled || cfgResp.NtfyURL != "" || cfgResp.NtfyTopic != "" {
		t.Fatalf("unexpected unconfigured response: %+v", cfgResp)
	}

	configured := setupAPITestEnvWithOptions(t, ServerOptions{
		NtfyURL:              "https://ntfy.tailnet.ts.net/",
		NtfyTopic:            "/agentpilot-alerts/",
		ApprovalIdleDuration: 50 * time.Millisecond,
	})
	resp2 := configured.doReq(t, http.MethodGet, "/api/config/notifications", configured.mobileTok, nil)
	if resp2.StatusCode != http.StatusOK {
		_ = resp2.Body.Close()
		t.Fatalf("GET /api/config/notifications status = %d, want 200", resp2.StatusCode)
	}
	var cfgResp2 notificationConfigResponse
	if err := json.NewDecoder(resp2.Body).Decode(&cfgResp2); err != nil {
		_ = resp2.Body.Close()
		t.Fatalf("decode notification config: %v", err)
	}
	_ = resp2.Body.Close()
	if !cfgResp2.Enabled || cfgResp2.NtfyURL != "https://ntfy.tailnet.ts.net" || cfgResp2.NtfyTopic != "agentpilot-alerts" {
		t.Fatalf("unexpected configured response: %+v", cfgResp2)
	}
}

func TestNtfySessionExitNotification(t *testing.T) {
	type ntfyCall struct {
		path     string
		title    string
		priority string
		tags     string
		body     string
	}
	var (
		ntfyMu    sync.Mutex
		ntfyCalls []ntfyCall
	)
	ntfySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		ntfyMu.Lock()
		ntfyCalls = append(ntfyCalls, ntfyCall{
			path:     r.URL.Path,
			title:    r.Header.Get("Title"),
			priority: r.Header.Get("Priority"),
			tags:     r.Header.Get("Tags"),
			body:     string(raw),
		})
		ntfyMu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer ntfySrv.Close()

	notifier := notify.NewNtfyNotifier(ntfySrv.URL, "agentpilot-test", ntfySrv.Client())
	env := setupAPITestEnvWithOptions(t, ServerOptions{
		Notifier:             notifier,
		NtfyURL:              ntfySrv.URL,
		NtfyTopic:            "agentpilot-test",
		ApprovalIdleDuration: 60 * time.Millisecond,
	})

	testManifest := manifests.Manifest{
		AgentID:          "test-agent",
		Name:             "Test Agent",
		LaunchCommand:    "__shell__",
		ChatMode:         manifests.ChatModeShell,
		Commands:         []manifests.Command{},
		ApprovalPatterns: []string{},
	}
	if err := manifests.SyncToStore(context.Background(), env.store, []manifests.Manifest{testManifest}); err != nil {
		t.Fatalf("SyncToStore(test-agent) error = %v", err)
	}

	rootDir := t.TempDir()
	env.connectRunner(t, "notify-box", []string{rootDir})

	projResp := env.doReq(t, http.MethodPost, "/api/projects", env.mobileTok, createProjectRequest{
		Name:        "notify-proj",
		MachineName: "notify-box",
		ParentPath:  rootDir,
	})
	if projResp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(projResp.Body)
		_ = projResp.Body.Close()
		t.Fatalf("POST /api/projects status = %d, want 201 (%s)", projResp.StatusCode, string(raw))
	}
	var proj projectResponse
	if err := json.NewDecoder(projResp.Body).Decode(&proj); err != nil {
		_ = projResp.Body.Close()
		t.Fatalf("decode project: %v", err)
	}
	_ = projResp.Body.Close()

	sessResp := env.doReq(t, http.MethodPost, "/api/sessions", env.mobileTok, createSessionRequest{
		ProjectID: proj.ID,
		AgentID:   "test-agent",
	})
	if sessResp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(sessResp.Body)
		_ = sessResp.Body.Close()
		t.Fatalf("POST /api/sessions status = %d, want 201 (%s)", sessResp.StatusCode, string(raw))
	}
	var sess sessionResponse
	if err := json.NewDecoder(sessResp.Body).Decode(&sess); err != nil {
		_ = sessResp.Body.Close()
		t.Fatalf("decode session: %v", err)
	}
	_ = sessResp.Body.Close()

	// Closing session triggers ntfy session exit notification.
	delResp := env.doReq(t, http.MethodDelete, "/api/sessions/"+sess.ID, env.mobileTok, nil)
	_ = delResp.Body.Close()
	if delResp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE /api/sessions/{id} status = %d, want 200", delResp.StatusCode)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		ntfyMu.Lock()
		n := len(ntfyCalls)
		ntfyMu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	ntfyMu.Lock()
	if len(ntfyCalls) < 1 {
		ntfyMu.Unlock()
		t.Fatalf("expected 1 ntfy exit notification, got %d", len(ntfyCalls))
	}
	exitCall := ntfyCalls[0]
	ntfyMu.Unlock()
	if exitCall.path != "/agentpilot-test" || exitCall.title != "Session Test Agent terminée" {
		t.Errorf("unexpected ntfy exit call: %+v", exitCall)
	}
}

func TestSessionHistorySearch(t *testing.T) {
	env := setupAPITestEnv(t)
	ctx := context.Background()

	proj1, err := env.store.CreateProject(ctx, store.Project{
		Name:        "alpha-api",
		MachineName: "linux-box",
		Path:        "/srv/alpha-api",
	})
	if err != nil {
		t.Fatalf("CreateProject(1) error = %v", err)
	}
	proj2, err := env.store.CreateProject(ctx, store.Project{
		Name:        "beta-web",
		MachineName: "win-box",
		Path:        `C:\repos\beta-web`,
	})
	if err != nil {
		t.Fatalf("CreateProject(2) error = %v", err)
	}

	// 1. Closed session with gzipped transcript snapshot containing ANSI-styled marker.
	snapDone := make(chan struct {
		path  string
		bytes int64
		err   error
	}, 1)
	transcript.WriteSnapshotAsync(
		ctx,
		env.dataDir,
		"sess-closed-gz",
		[]byte("\x1b[32mDeployed release v4.2.0\x1b[0m\nMigration complete\n"),
		func(path string, n int64, err error) {
			snapDone <- struct {
				path  string
				bytes int64
				err   error
			}{path, n, err}
		},
	)
	snap := <-snapDone
	if snap.err != nil {
		t.Fatalf("WriteSnapshotAsync() error = %v", snap.err)
	}

	exitZero := 0
	closedAt := time.Now().UTC().Add(-10 * time.Minute)
	if _, err := env.store.CreateSession(ctx, store.Session{
		ID:            "sess-closed-gz",
		ProjectID:     proj1.ID,
		MachineName:   "linux-box",
		AgentID:       "copilot",
		Cwd:           proj1.Path,
		Status:        "closed",
		ExitCode:      &exitZero,
		SnapshotPath:  snap.path,
		SnapshotBytes: snap.bytes,
		CreatedAt:     closedAt.Add(-5 * time.Minute),
		ClosedAt:      &closedAt,
	}); err != nil {
		t.Fatalf("CreateSession(closed) error = %v", err)
	}

	// 2. Active running session with in-memory ringbuf content.
	if _, err := env.store.CreateSession(ctx, store.Session{
		ID:          "sess-running-buf",
		ProjectID:   proj2.ID,
		MachineName: "win-box",
		AgentID:     "mistral",
		Cwd:         proj2.Path,
		Status:      "running",
		CreatedAt:   time.Now().UTC(),
	}); err != nil {
		t.Fatalf("CreateSession(running) error = %v", err)
	}
	env.apiSrv.registerActiveSession("sess-running-buf", "win-box", "mistral", "Mistral", proj2.Name, nil)
	env.apiSrv.HandleOutputChunk("sess-running-buf", []byte("Running \x1b[1mPanicCheck\x1b[0m in worker\n"))

	querySessions := func(q string) []sessionResponse {
		t.Helper()
		resp := env.doReq(t, http.MethodGet, "/api/sessions?q="+q, env.mobileTok, nil)
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /api/sessions?q=%s status = %d, want 200", q, resp.StatusCode)
		}
		var out []sessionResponse
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode sessions: %v", err)
		}
		return out
	}

	// Metadata match (case-insensitive on agentId).
	byMeta := querySessions("MISTRAL")
	if len(byMeta) != 1 || byMeta[0].ID != "sess-running-buf" {
		t.Fatalf("metadata query MISTRAL = %+v, want [sess-running-buf]", byMeta)
	}

	// Metadata match (case-insensitive on machineName/cwd).
	byCwd := querySessions("alpha-api")
	if len(byCwd) != 1 || byCwd[0].ID != "sess-closed-gz" {
		t.Fatalf("metadata query alpha-api = %+v, want [sess-closed-gz]", byCwd)
	}

	// Closed .gz transcript match (case-insensitive).
	byGz := querySessions("release%20V4.2.0")
	if len(byGz) != 1 || byGz[0].ID != "sess-closed-gz" {
		t.Fatalf("closed .gz transcript query = %+v, want [sess-closed-gz]", byGz)
	}

	// Active ringbuf transcript match (case-insensitive across ANSI codes).
	byLive := querySessions("paniccheck")
	if len(byLive) != 1 || byLive[0].ID != "sess-running-buf" {
		t.Fatalf("active ringbuf transcript query = %+v, want [sess-running-buf]", byLive)
	}

	// No match returns empty JSON array.
	noMatch := querySessions("definitely-not-present")
	if len(noMatch) != 0 {
		t.Fatalf("noMatch = %+v, want empty", noMatch)
	}
}

func TestMobileWebSocketSubprotocolAuthAndOrigin(t *testing.T) {
	env := setupAPITestEnv(t)
	ctx := context.Background()

	proj, err := env.store.CreateProject(ctx, store.Project{
		Name:        "web-proj",
		MachineName: "web-box",
		Path:        "/srv/web-proj",
	})
	if err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}
	if _, err := env.store.CreateSession(ctx, store.Session{
		ID:          "sess-web-subproto",
		ProjectID:   proj.ID,
		MachineName: "web-box",
		AgentID:     "shell",
		Cwd:         proj.Path,
		Status:      "running",
		CreatedAt:   time.Now().UTC(),
	}); err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	env.apiSrv.registerActiveSession("sess-web-subproto", "web-box", "shell", "Shell", proj.Name, nil)

	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(env.server.URL, "http") + "/ws/sessions/sess-web-subproto"
	conn, resp, err := websocket.Dial(dialCtx, wsURL, &websocket.DialOptions{
		Subprotocols: []string{"agentpilot.v1", "bearer." + env.mobileTok},
		HTTPHeader:   http.Header{"Origin": []string{"http://127.0.0.1:8090"}},
	})
	if err != nil {
		t.Fatalf("websocket.Dial() error = %v", err)
	}
	defer func() { _ = conn.CloseNow() }()

	if gotProto := resp.Header.Get("Sec-WebSocket-Protocol"); gotProto != "agentpilot.v1" {
		t.Errorf("negotiated Sec-WebSocket-Protocol = %q, want agentpilot.v1", gotProto)
	}

	_, rawSync, err := conn.Read(dialCtx)
	if err != nil {
		t.Fatalf("conn.Read(chat-sync) error = %v", err)
	}
	syncFrame, err := protocol.UnmarshalMobileFrame(rawSync)
	if err != nil || syncFrame.Type != protocol.MobileTypeChatSync {
		t.Fatalf("first frame = %+v (err=%v), want chat-sync", syncFrame, err)
	}
}

func TestStructuredChatMultiTurnStreamingCancelOptionsAndMenuAction(t *testing.T) {
	env := setupAPITestEnv(t)
	ctx := context.Background()

	// Register a testable agent with providerMenus and a real executable ("go") for menu-action testing.
	testAgent := manifests.Manifest{
		AgentID:       "go-inspect",
		Name:          "Go Inspect Agent",
		LaunchCommand: "go",
		ChatMode:      manifests.ChatModeHeadless,
		HeadlessArgs:  []string{"version"},
		ContinueArgs:  []string{"--continue"},
		ProviderMenus: &manifests.ProviderMenus{
			Options: []manifests.ProviderOption{
				{
					ID:           "model",
					Label:        "Model",
					Flag:         "--model",
					DefaultValue: "default",
					Choices: []manifests.OptionChoice{
						{Value: "default", Label: "Default"},
						{Value: "fast", Label: "Fast"},
					},
				},
			},
			InspectActions: []manifests.InspectAction{
				{
					ID:    "version",
					Label: "Go Version",
					Args:  []string{"version"},
				},
			},
		},
		Commands:         []manifests.Command{},
		ApprovalPatterns: []string{},
	}
	if err := manifests.SyncToStore(ctx, env.store, []manifests.Manifest{testAgent}); err != nil {
		t.Fatalf("SyncToStore(go-inspect) error = %v", err)
	}

	rootDir := t.TempDir()
	env.connectRunner(t, "chat-box", []string{rootDir})

	projResp := env.doReq(t, http.MethodPost, "/api/projects", env.mobileTok, createProjectRequest{
		Name:        "chat-proj",
		MachineName: "chat-box",
		ParentPath:  rootDir,
	})
	if projResp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(projResp.Body)
		_ = projResp.Body.Close()
		t.Fatalf("POST /api/projects status = %d, want 201 (%s)", projResp.StatusCode, string(raw))
	}
	var proj projectResponse
	_ = json.NewDecoder(projResp.Body).Decode(&proj)
	_ = projResp.Body.Close()

	// 1. Create shell chat session.
	sessResp := env.doReq(t, http.MethodPost, "/api/sessions", env.mobileTok, createSessionRequest{
		ProjectID: proj.ID,
		AgentID:   "shell",
	})
	if sessResp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(sessResp.Body)
		_ = sessResp.Body.Close()
		t.Fatalf("POST /api/sessions status = %d, want 201 (%s)", sessResp.StatusCode, string(raw))
	}
	var sess sessionResponse
	_ = json.NewDecoder(sessResp.Body).Decode(&sess)
	_ = sessResp.Body.Close()

	wsCtx, cancelWS := context.WithTimeout(ctx, 15*time.Second)
	defer cancelWS()

	wsURL := "ws" + strings.TrimPrefix(env.server.URL, "http") + "/ws/sessions/" + sess.ID
	wsConn, _, err := websocket.Dial(wsCtx, wsURL, &websocket.DialOptions{
		Subprotocols: []string{"agentpilot.v1"},
		HTTPHeader:   http.Header{"Authorization": []string{"Bearer " + env.mobileTok}},
	})
	if err != nil {
		t.Fatalf("websocket.Dial() error = %v", err)
	}
	defer func() { _ = wsConn.CloseNow() }()

	_, rawSync, err := wsConn.Read(wsCtx)
	if err != nil {
		t.Fatalf("wsConn.Read(chat-sync) error = %v", err)
	}
	syncFrame, err := protocol.UnmarshalMobileFrame(rawSync)
	if err != nil || syncFrame.Type != protocol.MobileTypeChatSync || len(syncFrame.Messages) != 0 {
		t.Fatalf("initial frame = %+v (err=%v), want empty chat-sync", syncFrame, err)
	}

	// 2. Send two turns and verify WS streaming (chat-message, chat-delta, chat-done) + DB persistence.
	for _, marker := range []string{"turn_one_ok", "turn_two_unique_needle"} {
		msgResp := env.doReq(t, http.MethodPost, "/api/sessions/"+sess.ID+"/messages", env.mobileTok, createMessageRequest{
			Content: "echo " + marker,
		})
		if msgResp.StatusCode != http.StatusAccepted {
			raw, _ := io.ReadAll(msgResp.Body)
			_ = msgResp.Body.Close()
			t.Fatalf("POST messages status = %d, want 202 (%s)", msgResp.StatusCode, string(raw))
		}
		var created createMessageResponse
		_ = json.NewDecoder(msgResp.Body).Decode(&created)
		_ = msgResp.Body.Close()

		var sawDelta bool
		var doneFrame protocol.MobileFrame
		for {
			_, raw, err := wsConn.Read(wsCtx)
			if err != nil {
				t.Fatalf("wsConn.Read() waiting for chat-done error = %v", err)
			}
			frame, err := protocol.UnmarshalMobileFrame(raw)
			if err != nil {
				t.Fatalf("UnmarshalMobileFrame() error = %v", err)
			}
			if frame.Type == protocol.MobileTypeChatDelta && frame.MessageID == created.AssistantMessage.ID {
				sawDelta = true
			}
			if frame.Type == protocol.MobileTypeChatDone && frame.MessageID == created.AssistantMessage.ID {
				doneFrame = frame
				break
			}
		}
		if !sawDelta {
			t.Errorf("expected at least one chat-delta for %s", marker)
		}
		if doneFrame.Status != "done" || !strings.Contains(doneFrame.Content, marker) {
			t.Fatalf("unexpected chat-done frame for %s: %+v", marker, doneFrame)
		}
	}

	// Verify GET /api/sessions/{id}/messages returns all 4 messages in order.
	listMsgResp := env.doReq(t, http.MethodGet, "/api/sessions/"+sess.ID+"/messages", env.mobileTok, nil)
	if listMsgResp.StatusCode != http.StatusOK {
		_ = listMsgResp.Body.Close()
		t.Fatalf("GET messages status = %d, want 200", listMsgResp.StatusCode)
	}
	var messages []store.ChatMessage
	_ = json.NewDecoder(listMsgResp.Body).Decode(&messages)
	_ = listMsgResp.Body.Close()
	if len(messages) != 4 {
		t.Fatalf("len(messages) = %d, want 4: %+v", len(messages), messages)
	}
	if messages[0].Role != "user" || messages[1].Role != "assistant" || !strings.Contains(messages[3].Content, "turn_two_unique_needle") {
		t.Fatalf("unexpected persisted messages: %+v", messages)
	}

	// Verify GET /api/sessions?q= searches session_messages.content.
	searchResp := env.doReq(t, http.MethodGet, "/api/sessions?q=turn_two_unique_needle", env.mobileTok, nil)
	var searchItems []sessionResponse
	_ = json.NewDecoder(searchResp.Body).Decode(&searchItems)
	_ = searchResp.Body.Close()
	if len(searchItems) != 1 || searchItems[0].ID != sess.ID {
		t.Fatalf("GET /api/sessions?q=turn_two_unique_needle = %+v, want [%s]", searchItems, sess.ID)
	}

	// 3. Test POST /api/sessions/{id}/cancel on a long-running shell command.
	sleepCmd := "sleep 10"
	if runtime.GOOS == "windows" {
		sleepCmd = "ping -n 10 127.0.0.1 >nul"
	}
	longResp := env.doReq(t, http.MethodPost, "/api/sessions/"+sess.ID+"/messages", env.mobileTok, createMessageRequest{
		Content: sleepCmd,
	})
	if longResp.StatusCode != http.StatusAccepted {
		raw, _ := io.ReadAll(longResp.Body)
		_ = longResp.Body.Close()
		t.Fatalf("POST long message status = %d, want 202 (%s)", longResp.StatusCode, string(raw))
	}
	var longCreated createMessageResponse
	_ = json.NewDecoder(longResp.Body).Decode(&longCreated)
	_ = longResp.Body.Close()

	cancelResp := env.doReq(t, http.MethodPost, "/api/sessions/"+sess.ID+"/cancel", env.mobileTok, nil)
	if cancelResp.StatusCode != http.StatusOK {
		_ = cancelResp.Body.Close()
		t.Fatalf("POST cancel status = %d, want 200", cancelResp.StatusCode)
	}
	_ = cancelResp.Body.Close()

	for {
		_, raw, err := wsConn.Read(wsCtx)
		if err != nil {
			t.Fatalf("wsConn.Read() waiting for cancelled chat-done error = %v", err)
		}
		frame, err := protocol.UnmarshalMobileFrame(raw)
		if err != nil {
			t.Fatalf("UnmarshalMobileFrame() error = %v", err)
		}
		if frame.Type == protocol.MobileTypeChatDone && frame.MessageID == longCreated.AssistantMessage.ID {
			if frame.Status != "error" || frame.Error != "cancelled" {
				t.Fatalf("expected cancelled error frame, got %+v", frame)
			}
			break
		}
	}

	// 4. Test PATCH /api/sessions/{id}/options and POST /api/sessions/{id}/menu-action.
	goSessResp := env.doReq(t, http.MethodPost, "/api/sessions", env.mobileTok, createSessionRequest{
		ProjectID: proj.ID,
		AgentID:   "go-inspect",
	})
	if goSessResp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(goSessResp.Body)
		_ = goSessResp.Body.Close()
		t.Fatalf("POST /api/sessions(go-inspect) status = %d, want 201 (%s)", goSessResp.StatusCode, string(raw))
	}
	var goSess sessionResponse
	_ = json.NewDecoder(goSessResp.Body).Decode(&goSess)
	_ = goSessResp.Body.Close()
	if goSess.Options["model"] != "default" {
		t.Fatalf("default options = %+v, want model=default", goSess.Options)
	}

	// Valid PATCH options.
	patchResp := env.doReq(t, http.MethodPatch, "/api/sessions/"+goSess.ID+"/options", env.mobileTok, updateOptionsRequest{
		Options: map[string]string{"model": "fast"},
	})
	if patchResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(patchResp.Body)
		_ = patchResp.Body.Close()
		t.Fatalf("PATCH options status = %d, want 200 (%s)", patchResp.StatusCode, string(raw))
	}
	var patched sessionResponse
	_ = json.NewDecoder(patchResp.Body).Decode(&patched)
	_ = patchResp.Body.Close()
	if patched.Options["model"] != "fast" {
		t.Fatalf("patched.Options = %+v, want model=fast", patched.Options)
	}

	// Invalid PATCH option value -> 400.
	badPatch := env.doReq(t, http.MethodPatch, "/api/sessions/"+goSess.ID+"/options", env.mobileTok, updateOptionsRequest{
		Options: map[string]string{"model": "nonexistent-model"},
	})
	_ = badPatch.Body.Close()
	if badPatch.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid option value status = %d, want 400", badPatch.StatusCode)
	}

	// Valid menu-action -> 200 with go version output.
	actResp := env.doReq(t, http.MethodPost, "/api/sessions/"+goSess.ID+"/menu-action", env.mobileTok, menuActionRequest{
		ActionID: "version",
	})
	if actResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(actResp.Body)
		_ = actResp.Body.Close()
		t.Fatalf("POST menu-action status = %d, want 200 (%s)", actResp.StatusCode, string(raw))
	}
	var actResult menuActionResponse
	_ = json.NewDecoder(actResp.Body).Decode(&actResult)
	_ = actResp.Body.Close()
	if actResult.ActionID != "version" || actResult.ExitCode != 0 || !strings.Contains(actResult.Output, "go version") {
		t.Fatalf("unexpected menu-action result: %+v", actResult)
	}

	// Unknown menu-action -> 404 (never executes arbitrary commands).
	unknownAct := env.doReq(t, http.MethodPost, "/api/sessions/"+goSess.ID+"/menu-action", env.mobileTok, menuActionRequest{
		ActionID: "rm -rf /",
	})
	_ = unknownAct.Body.Close()
	if unknownAct.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown menu-action status = %d, want 404", unknownAct.StatusCode)
	}

	// 5. DELETE /api/sessions/{id} closes session and broadcasts session-closed.
	delResp := env.doReq(t, http.MethodDelete, "/api/sessions/"+sess.ID, env.mobileTok, nil)
	_ = delResp.Body.Close()
	if delResp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE session status = %d, want 200", delResp.StatusCode)
	}
	for {
		_, raw, err := wsConn.Read(wsCtx)
		if err != nil {
			t.Fatalf("wsConn.Read() waiting for session-closed error = %v", err)
		}
		frame, err := protocol.UnmarshalMobileFrame(raw)
		if err != nil {
			t.Fatalf("UnmarshalMobileFrame() error = %v", err)
		}
		if frame.Type == protocol.MobileTypeSessionClosed {
			break
		}
	}
}

func TestBuildChatTurnArgs(t *testing.T) {
	m := manifests.Manifest{
		AgentID:      "mistral",
		ChatMode:     manifests.ChatModeHeadless,
		HeadlessArgs: []string{"--workdir", "{cwd}", "-p", "{prompt}", "--output", "text", "--auto-approve"},
		ContinueArgs: []string{"-c"},
		ProviderMenus: &manifests.ProviderMenus{
			Options: []manifests.ProviderOption{
				{ID: "model", Flag: "--model", DefaultValue: "default"},
				{ID: "thinking", Flag: "--thinking", DefaultValue: "default"},
				{ID: "approvalPolicy", Flag: "--approval-policy", DefaultValue: "auto-approve"},
			},
		},
	}

	turn0 := buildChatTurnArgs(m, "/repo/app", "hello", 0, map[string]string{
		"model":          "default",
		"thinking":       "high",
		"approvalPolicy": "auto-approve",
	})
	want0 := []string{"--workdir", "/repo/app", "-p", "hello", "--output", "text", "--auto-approve", "--thinking", "high"}
	if strings.Join(turn0, "|") != strings.Join(want0, "|") {
		t.Fatalf("turn0 args = %v, want %v", turn0, want0)
	}

	turn1 := buildChatTurnArgs(m, "/repo/app", "follow up", 1, map[string]string{
		"model":          "devstral-2",
		"thinking":       "default",
		"approvalPolicy": "smart-approve",
	})
	want1 := []string{"--workdir", "/repo/app", "-p", "follow up", "--output", "text", "--auto-approve", "-c", "--model", "devstral-2", "--approval-policy", "smart-approve"}
	if strings.Join(turn1, "|") != strings.Join(want1, "|") {
		t.Fatalf("turn1 args = %v, want %v", turn1, want1)
	}
}

func TestMachineFSBrowser(t *testing.T) {
	env := setupAPITestEnv(t)

	rootDir := t.TempDir()
	for _, rel := range []string{"beta", "Alpha", ".hidden", filepath.Join("Alpha", "nested")} {
		if err := os.MkdirAll(filepath.Join(rootDir, rel), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", rel, err)
		}
	}
	if err := os.WriteFile(filepath.Join(rootDir, "readme.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	env.connectRunner(t, "fs-box", []string{rootDir})

	// 1. 400 on .. traversal in ?path=.
	for _, badPath := range []string{"../etc", rootDir + "/../escape", rootDir + `\..\escape`} {
		resp := env.doReq(t, http.MethodGet, "/api/machines/fs-box/fs?path="+url.QueryEscape(badPath), env.mobileTok, nil)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("GET /api/machines/fs-box/fs?path=%q status = %d, want 400", badPath, resp.StatusCode)
		}
	}

	// 2. 503 on offline machine.
	offlineResp := env.doReq(t, http.MethodGet, "/api/machines/offline-box/fs", env.mobileTok, nil)
	_ = offlineResp.Body.Close()
	if offlineResp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("GET /api/machines/offline-box/fs status = %d, want 503", offlineResp.StatusCode)
	}

	// 3. 200 on default root (?path= omitted).
	rootResp := env.doReq(t, http.MethodGet, "/api/machines/fs-box/fs", env.mobileTok, nil)
	if rootResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(rootResp.Body)
		_ = rootResp.Body.Close()
		t.Fatalf("GET /api/machines/fs-box/fs status = %d, want 200 (body=%s)", rootResp.StatusCode, string(raw))
	}
	var rootBody machineFSResponse
	if err := json.NewDecoder(rootResp.Body).Decode(&rootBody); err != nil {
		_ = rootResp.Body.Close()
		t.Fatalf("decode rootBody: %v", err)
	}
	_ = rootResp.Body.Close()

	if rootBody.MachineName != "fs-box" || rootBody.OS != runtime.GOOS {
		t.Errorf("unexpected machine/os: %+v", rootBody)
	}
	if rootBody.CurrentPath != filepath.Clean(rootDir) {
		t.Errorf("CurrentPath = %q, want %q", rootBody.CurrentPath, filepath.Clean(rootDir))
	}
	if rootBody.ParentPath == "" {
		t.Errorf("expected non-empty ParentPath for temp dir %q", rootDir)
	}
	if len(rootBody.Roots) != 1 || rootBody.Roots[0] != rootDir {
		t.Errorf("Roots = %v, want [%s]", rootBody.Roots, rootDir)
	}
	wantOrder := []string{"Alpha", "beta", ".hidden"}
	if len(rootBody.Entries) != len(wantOrder) {
		t.Fatalf("Entries = %+v, want %v", rootBody.Entries, wantOrder)
	}
	for i, wantName := range wantOrder {
		if rootBody.Entries[i].Name != wantName {
			t.Errorf("Entries[%d].Name = %q, want %q", i, rootBody.Entries[i].Name, wantName)
		}
	}

	// 4. 200 on nested subfolder.
	alphaDir := filepath.Join(rootDir, "Alpha")
	subResp := env.doReq(t, http.MethodGet, "/api/machines/fs-box/fs?path="+url.QueryEscape(alphaDir), env.mobileTok, nil)
	if subResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(subResp.Body)
		_ = subResp.Body.Close()
		t.Fatalf("GET nested fs status = %d, want 200 (body=%s)", subResp.StatusCode, string(raw))
	}
	var subBody machineFSResponse
	if err := json.NewDecoder(subResp.Body).Decode(&subBody); err != nil {
		_ = subResp.Body.Close()
		t.Fatalf("decode subBody: %v", err)
	}
	_ = subResp.Body.Close()

	if subBody.CurrentPath != alphaDir {
		t.Errorf("sub CurrentPath = %q, want %q", subBody.CurrentPath, alphaDir)
	}
	if subBody.ParentPath != filepath.Clean(rootDir) {
		t.Errorf("sub ParentPath = %q, want %q", subBody.ParentPath, filepath.Clean(rootDir))
	}
	if len(subBody.Entries) != 1 || subBody.Entries[0].Name != "nested" {
		t.Fatalf("sub Entries = %+v, want [nested]", subBody.Entries)
	}

	// 5. Navigating up via parentPath returns rootDir again.
	upResp := env.doReq(t, http.MethodGet, "/api/machines/fs-box/fs?path="+url.QueryEscape(subBody.ParentPath), env.mobileTok, nil)
	if upResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(upResp.Body)
		_ = upResp.Body.Close()
		t.Fatalf("GET parentPath fs status = %d, want 200 (body=%s)", upResp.StatusCode, string(raw))
	}
	var upBody machineFSResponse
	if err := json.NewDecoder(upResp.Body).Decode(&upBody); err != nil {
		_ = upResp.Body.Close()
		t.Fatalf("decode upBody: %v", err)
	}
	_ = upResp.Body.Close()
	if upBody.CurrentPath != filepath.Clean(rootDir) {
		t.Errorf("up CurrentPath = %q, want %q", upBody.CurrentPath, filepath.Clean(rootDir))
	}
}
