package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/agailloty/agentpilot/server/internal/config"
	"github.com/agailloty/agentpilot/server/internal/store"
)

func openAuthTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestBootstrapTokensAndEmptyRejection(t *testing.T) {
	ctx := context.Background()
	s := openAuthTestStore(t)

	if err := BootstrapTokens(ctx, s, nil); !errors.Is(err, ErrNoActiveTokens) {
		t.Fatalf("BootstrapTokens(empty) = %v, want ErrNoActiveTokens", err)
	}

	entries := []config.Token{
		{Name: "mobile-default", Scope: config.ScopeMobile, Value: "mobile-secret-1"},
		{Name: "runner-pc1", Scope: config.ScopeRunner, Value: "runner-secret-1"},
	}
	if err := BootstrapTokens(ctx, s, entries); err != nil {
		t.Fatalf("BootstrapTokens() error = %v", err)
	}

	// Idempotent re-run with same entries.
	if err := BootstrapTokens(ctx, s, entries); err != nil {
		t.Fatalf("BootstrapTokens(repeat) error = %v", err)
	}

	// Subsequent startup with empty env succeeds when DB already has active tokens.
	if err := BootstrapTokens(ctx, s, nil); err != nil {
		t.Fatalf("BootstrapTokens(existing DB) error = %v", err)
	}

	// Stored token_hash must be SHA-256 hex, never plaintext.
	tok, err := s.GetTokenByHash(ctx, HashToken("mobile-secret-1"))
	if err != nil {
		t.Fatalf("GetTokenByHash() error = %v", err)
	}
	if tok.TokenHash == "mobile-secret-1" || len(tok.TokenHash) != 64 {
		t.Errorf("unexpected stored token_hash %q", tok.TokenHash)
	}
}

func TestRequireScopeMiddleware(t *testing.T) {
	ctx := context.Background()
	s := openAuthTestStore(t)

	err := BootstrapTokens(ctx, s, []config.Token{
		{Name: "mobile-default", Scope: config.ScopeMobile, Value: "secret-mobile"},
		{Name: "runner-pc1", Scope: config.ScopeRunner, Value: "secret-runner"},
		{Name: "mobile-old", Scope: config.ScopeMobile, Value: "secret-revoked"},
	})
	if err != nil {
		t.Fatalf("BootstrapTokens() error = %v", err)
	}

	revokedTok, err := s.GetTokenByHash(ctx, HashToken("secret-revoked"))
	if err != nil {
		t.Fatalf("GetTokenByHash(revoked) error = %v", err)
	}
	if err := s.RevokeToken(ctx, revokedTok.ID); err != nil {
		t.Fatalf("RevokeToken() error = %v", err)
	}

	authn := NewAuthenticator(s)
	mux := http.NewServeMux()
	mux.Handle("GET /mobile", authn.RequireScope(config.ScopeMobile)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, ok := TokenFromContext(r.Context())
		if !ok || tok.Scope != config.ScopeMobile {
			t.Errorf("TokenFromContext() = (%+v, %v), want mobile token", tok, ok)
		}
		w.WriteHeader(http.StatusOK)
	})))
	mux.Handle("GET /runner", authn.RequireScope(config.ScopeRunner)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, ok := TokenFromContext(r.Context())
		if !ok || tok.Scope != config.ScopeRunner {
			t.Errorf("TokenFromContext() = (%+v, %v), want runner token", tok, ok)
		}
		w.WriteHeader(http.StatusOK)
	})))

	cases := []struct {
		name       string
		path       string
		authHeader string
		wantStatus int
	}{
		{
			name:       "missing token",
			path:       "/mobile",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "malformed header basic scheme",
			path:       "/mobile",
			authHeader: "Basic dXNlcjpwYXNz",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "malformed header empty bearer",
			path:       "/mobile",
			authHeader: "Bearer   ",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "unknown token",
			path:       "/mobile",
			authHeader: "Bearer does-not-exist",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "mobile token on runner route",
			path:       "/runner",
			authHeader: "Bearer secret-mobile",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "runner token on mobile route",
			path:       "/mobile",
			authHeader: "Bearer secret-runner",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "revoked token",
			path:       "/mobile",
			authHeader: "Bearer secret-revoked",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "valid mobile token on mobile route",
			path:       "/mobile",
			authHeader: "Bearer secret-mobile",
			wantStatus: http.StatusOK,
		},
		{
			name:       "valid runner token on runner route",
			path:       "/runner",
			authHeader: "Bearer secret-runner",
			wantStatus: http.StatusOK,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d (body=%s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantStatus == http.StatusUnauthorized && rec.Header().Get("WWW-Authenticate") != "Bearer" {
				t.Errorf("missing WWW-Authenticate: Bearer header")
			}
		})
	}
}

func TestCORSMiddlewarePreflightAndNonPreflight(t *testing.T) {
	ctx := context.Background()
	s := openAuthTestStore(t)
	if err := BootstrapTokens(ctx, s, []config.Token{
		{Name: "mobile-default", Scope: config.ScopeMobile, Value: "secret-mobile"},
	}); err != nil {
		t.Fatalf("BootstrapTokens() error = %v", err)
	}

	authn := NewAuthenticator(s)
	handler := CORS(authn.RequireAuth()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	t.Run("preflight OPTIONS with Origin and Access-Control-Request-Method returns 204", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/api/sessions", nil)
		req.Header.Set("Origin", "http://127.0.0.1:8090")
		req.Header.Set("Access-Control-Request-Method", "POST")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", rec.Code)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://127.0.0.1:8090" {
			t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, "http://127.0.0.1:8090")
		}
		if got := rec.Header().Get("Vary"); got != "Origin" {
			t.Errorf("Vary = %q, want Origin", got)
		}
		if got := rec.Header().Get("Access-Control-Allow-Methods"); got == "" {
			t.Errorf("expected Access-Control-Allow-Methods to be set")
		}
		if got := rec.Header().Get("Access-Control-Allow-Headers"); got == "" {
			t.Errorf("expected Access-Control-Allow-Headers to be set")
		}
		if got := rec.Header().Get("Access-Control-Max-Age"); got != "86400" {
			t.Errorf("Access-Control-Max-Age = %q, want 86400", got)
		}
	})

	t.Run("normal GET without token still returns 401 with CORS headers", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
		req.Header.Set("Origin", "http://127.0.0.1:8090")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://127.0.0.1:8090" {
			t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, "http://127.0.0.1:8090")
		}
	})

	t.Run("OPTIONS without Access-Control-Request-Method and without token returns 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/api/sessions", nil)
		req.Header.Set("Origin", "http://127.0.0.1:8090")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})
}

func TestExtractWebSocketBearerTokenAndUpgradeAuth(t *testing.T) {
	if tok, ok := ExtractWebSocketBearerToken([]string{"agentpilot.v1, bearer.my-secret"}); !ok || tok != "my-secret" {
		t.Fatalf("ExtractWebSocketBearerToken() = (%q, %v), want (my-secret, true)", tok, ok)
	}
	if tok, ok := ExtractWebSocketBearerToken([]string{"agentpilot.v1", "Bearer.nope", "bearer."}); ok || tok != "" {
		t.Fatalf("ExtractWebSocketBearerToken(invalid) = (%q, %v), want ('', false)", tok, ok)
	}

	ctx := context.Background()
	s := openAuthTestStore(t)
	if err := BootstrapTokens(ctx, s, []config.Token{
		{Name: "mobile-default", Scope: config.ScopeMobile, Value: "secret-mobile"},
	}); err != nil {
		t.Fatalf("BootstrapTokens() error = %v", err)
	}

	authn := NewAuthenticator(s)
	handler := authn.RequireScope(config.ScopeMobile)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Subprotocol token accepted on websocket upgrade.
	wsReq := httptest.NewRequest(http.MethodGet, "/ws/sessions/s1", nil)
	wsReq.Header.Set("Upgrade", "websocket")
	wsReq.Header.Add("Sec-WebSocket-Protocol", "agentpilot.v1, bearer.secret-mobile")
	wsRec := httptest.NewRecorder()
	handler.ServeHTTP(wsRec, wsReq)
	if wsRec.Code != http.StatusOK {
		t.Fatalf("websocket subprotocol auth status = %d, want 200", wsRec.Code)
	}

	// Subprotocol header ignored on regular non-websocket HTTP requests.
	httpReq := httptest.NewRequest(http.MethodGet, "/ws/sessions/s1", nil)
	httpReq.Header.Add("Sec-WebSocket-Protocol", "agentpilot.v1, bearer.secret-mobile")
	httpRec := httptest.NewRecorder()
	handler.ServeHTTP(httpRec, httpReq)
	if httpRec.Code != http.StatusUnauthorized {
		t.Fatalf("non-websocket request with Sec-WebSocket-Protocol status = %d, want 401", httpRec.Code)
	}
}
