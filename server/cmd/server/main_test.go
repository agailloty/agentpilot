package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agailloty/agentpilot/server/internal/auth"
	"github.com/agailloty/agentpilot/server/internal/config"
	"github.com/agailloty/agentpilot/server/internal/hub"
	"github.com/agailloty/agentpilot/server/internal/manifests"
	"github.com/agailloty/agentpilot/server/internal/runner"
	"github.com/agailloty/agentpilot/server/internal/store"
)

func setupTestApp(t *testing.T, tokens []config.Token) (*store.Store, http.Handler) {
	t.Helper()
	dataDir := t.TempDir()
	s, err := store.Open(filepath.Join(dataDir, "server.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if err := auth.BootstrapTokens(context.Background(), s, tokens); err != nil {
		t.Fatalf("BootstrapTokens() error = %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	runnerHub := hub.New(hub.Options{Logger: logger})
	t.Cleanup(runnerHub.Close)

	authn := auth.NewAuthenticator(s)
	return s, newAppHandler(s, runnerHub, dataDir, authn, logger)
}

func TestRunRefusesStartupWithoutTokens(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{
		ListenAddr: "127.0.0.1:0",
		DataDir:    t.TempDir(),
	}
	err := run(context.Background(), cfg, logger)
	if !errors.Is(err, auth.ErrNoActiveTokens) {
		t.Fatalf("run() error = %v, want ErrNoActiveTokens", err)
	}
}

func TestBearerAuthRequiredOnAllRoutes(t *testing.T) {
	_, handler := setupTestApp(t, []config.Token{
		{Name: "mobile-default", Scope: config.ScopeMobile, Value: "valid-mobile-token"},
		{Name: "runner-pc1", Scope: config.ScopeRunner, Value: "valid-runner-token"},
	})

	cases := []struct {
		name       string
		method     string
		path       string
		authHeader string
		wantStatus int
	}{
		{
			name:       "missing auth on healthz",
			method:     http.MethodGet,
			path:       "/healthz",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "missing auth on ws runner",
			method:     http.MethodGet,
			path:       "/ws/runner",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "missing auth on api config notifications",
			method:     http.MethodGet,
			path:       "/api/config/notifications",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "missing auth on api sessions",
			method:     http.MethodGet,
			path:       "/api/sessions",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "missing auth on ws sessions",
			method:     http.MethodGet,
			path:       "/ws/sessions/s1",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "missing auth on project exec",
			method:     http.MethodPost,
			path:       "/api/projects/p1/exec",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "missing auth on unknown route",
			method:     http.MethodGet,
			path:       "/unknown",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "wrong scheme",
			method:     http.MethodGet,
			path:       "/healthz",
			authHeader: "Basic dXNlcjpwYXNz",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "empty bearer token",
			method:     http.MethodGet,
			path:       "/healthz",
			authHeader: "Bearer   ",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "invalid bearer token",
			method:     http.MethodGet,
			path:       "/healthz",
			authHeader: "Bearer wrong-token",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "mobile token rejected on ws runner",
			method:     http.MethodGet,
			path:       "/ws/runner",
			authHeader: "Bearer valid-mobile-token",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "runner token rejected on api config notifications",
			method:     http.MethodGet,
			path:       "/api/config/notifications",
			authHeader: "Bearer valid-runner-token",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "runner token rejected on api sessions",
			method:     http.MethodGet,
			path:       "/api/sessions",
			authHeader: "Bearer valid-runner-token",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "runner token rejected on ws sessions",
			method:     http.MethodGet,
			path:       "/ws/sessions/s1",
			authHeader: "Bearer valid-runner-token",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "runner token rejected on project exec",
			method:     http.MethodPost,
			path:       "/api/projects/p1/exec",
			authHeader: "Bearer valid-runner-token",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "valid bearer token on healthz",
			method:     http.MethodGet,
			path:       "/healthz",
			authHeader: "Bearer valid-mobile-token",
			wantStatus: http.StatusOK,
		},
		{
			name:       "valid bearer token on api sessions",
			method:     http.MethodGet,
			path:       "/api/sessions",
			authHeader: "Bearer valid-mobile-token",
			wantStatus: http.StatusOK,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d (body=%s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
		})
	}
}

func TestAppHandlerWiresRunnerLifecycleAndManifests(t *testing.T) {
	dataDir := t.TempDir()
	s, err := store.Open(filepath.Join(dataDir, "server.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer func() { _ = s.Close() }()

	if err := auth.BootstrapTokens(context.Background(), s, []config.Token{
		{Name: "mobile", Scope: config.ScopeMobile, Value: "mobile-tok"},
		{Name: "runner", Scope: config.ScopeRunner, Value: "runner-tok"},
	}); err != nil {
		t.Fatalf("BootstrapTokens() error = %v", err)
	}

	loaded, err := manifests.LoadDir(filepath.Join("..", "..", "..", "docs", "AgentManifests"))
	if err != nil {
		t.Fatalf("manifests.LoadDir() error = %v", err)
	}
	if err := manifests.SyncToStore(context.Background(), s, loaded); err != nil {
		t.Fatalf("manifests.SyncToStore() error = %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	runnerHub := hub.New(hub.Options{Logger: logger})
	defer runnerHub.Close()

	authn := auth.NewAuthenticator(s)
	srv := httptest.NewServer(newAppHandler(s, runnerHub, dataDir, authn, logger))
	defer srv.Close()

	client, err := runner.NewClient(runner.Config{
		ServerURL:      srv.URL,
		Token:          "runner-tok",
		MachineName:    "wired-box",
		Roots:          []string{"/workspace"},
		InitialBackoff: 15 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer func() { _ = client.PTYManager().CloseAll() }()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = client.Run(ctx)
	}()

	// Wait for machine to appear online in SQLite.
	deadline := time.Now().Add(5 * time.Second)
	var m store.Machine
	for time.Now().Before(deadline) {
		m, err = s.GetMachineByName(context.Background(), "wired-box")
		if err == nil && m.Online {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if err != nil || !m.Online || len(m.Roots) != 1 || m.Roots[0] != "/workspace" {
		t.Fatalf("connected machine in store = %+v (err=%v), want online with root /workspace", m, err)
	}

	// Verify /api/agents serves synced shell manifest.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/agents", nil)
	req.Header.Set("Authorization", "Bearer mobile-tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/agents error = %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/agents status = %d, want 200", resp.StatusCode)
	}

	// Disconnect runner and confirm SQLite marks it offline.
	cancel()
	<-done

	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		m, err = s.GetMachineByName(context.Background(), "wired-box")
		if err == nil && !m.Online {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if err != nil || m.Online {
		t.Fatalf("disconnected machine in store = %+v (err=%v), want online=false", m, err)
	}
}

func TestSPAStaticServingPreservesAPIAuth(t *testing.T) {
	dataDir := t.TempDir()
	webDir := t.TempDir()

	indexContent := "<!doctype html><html><body>AgentPilot SPA</body></html>"
	appJSContent := `console.log("agentpilot-web");`

	if err := os.WriteFile(filepath.Join(webDir, "index.html"), []byte(indexContent), 0o600); err != nil {
		t.Fatalf("WriteFile(index.html) error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(webDir, "assets"), 0o750); err != nil {
		t.Fatalf("MkdirAll(assets) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(webDir, "assets", "app.js"), []byte(appJSContent), 0o600); err != nil {
		t.Fatalf("WriteFile(assets/app.js) error = %v", err)
	}

	s, err := store.Open(filepath.Join(dataDir, "server.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if err := auth.BootstrapTokens(context.Background(), s, []config.Token{
		{Name: "mobile-default", Scope: config.ScopeMobile, Value: "valid-mobile-token"},
		{Name: "runner-pc1", Scope: config.ScopeRunner, Value: "valid-runner-token"},
	}); err != nil {
		t.Fatalf("BootstrapTokens() error = %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	runnerHub := hub.New(hub.Options{Logger: logger})
	t.Cleanup(runnerHub.Close)

	authn := auth.NewAuthenticator(s)
	handler := newAppHandlerWithConfig(s, runnerHub, config.Config{
		DataDir:        dataDir,
		WebDir:         webDir,
		ApprovalIdleMS: config.DefaultApprovalIdleMS,
	}, nil, authn, logger)

	cases := []struct {
		name         string
		method       string
		path         string
		authHeader   string
		wantStatus   int
		wantContains string
	}{
		{
			name:         "GET / serves index.html publicly",
			method:       http.MethodGet,
			path:         "/",
			wantStatus:   http.StatusOK,
			wantContains: "AgentPilot SPA",
		},
		{
			name:         "GET /assets/app.js serves static asset publicly",
			method:       http.MethodGet,
			path:         "/assets/app.js",
			wantStatus:   http.StatusOK,
			wantContains: `console.log("agentpilot-web");`,
		},
		{
			name:         "GET /projects/abc falls back to index.html",
			method:       http.MethodGet,
			path:         "/projects/abc",
			wantStatus:   http.StatusOK,
			wantContains: "AgentPilot SPA",
		},
		{
			name:       "POST /projects/abc requires Bearer auth",
			method:     http.MethodPost,
			path:       "/projects/abc",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "GET /api/machines still requires Bearer auth",
			method:     http.MethodGet,
			path:       "/api/machines",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "GET /api/sessions still requires Bearer auth",
			method:     http.MethodGet,
			path:       "/api/sessions",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "GET /ws/runner still requires Bearer auth",
			method:     http.MethodGet,
			path:       "/ws/runner",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "GET /healthz still requires Bearer auth",
			method:     http.MethodGet,
			path:       "/healthz",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "GET /api/sessions succeeds with valid mobile token",
			method:     http.MethodGet,
			path:       "/api/sessions",
			authHeader: "Bearer valid-mobile-token",
			wantStatus: http.StatusOK,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body=%s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantContains != "" && !strings.Contains(rec.Body.String(), tc.wantContains) {
				t.Errorf("body = %q, want substring %q", rec.Body.String(), tc.wantContains)
			}
		})
	}
}
