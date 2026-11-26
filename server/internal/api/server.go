package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/agailloty/agentpilot/server/internal/auth"
	"github.com/agailloty/agentpilot/server/internal/config"
	"github.com/agailloty/agentpilot/server/internal/hub"
	"github.com/agailloty/agentpilot/server/internal/notify"
	"github.com/agailloty/agentpilot/server/internal/store"
)

const maxRequestBodyBytes = 1 << 20 // 1 MiB

// ServerOptions configures optional API server behavior such as notifications and approval detection.
type ServerOptions struct {
	DataDir              string
	Logger               *slog.Logger
	Notifier             notify.Notifier
	NtfyURL              string
	NtfyTopic            string
	ApprovalIdleDuration time.Duration
}

type notificationConfigResponse struct {
	Enabled   bool   `json:"enabled"`
	NtfyURL   string `json:"ntfyUrl"`
	NtfyTopic string `json:"ntfyTopic"`
}

// Server exposes authenticated REST and WebSocket endpoints for mobile clients.
type Server struct {
	store                *store.Store
	hub                  *hub.Hub
	dataDir              string
	logger               *slog.Logger
	notifier             notify.Notifier
	ntfyURL              string
	ntfyTopic            string
	approvalIdleDuration time.Duration

	sessionsMu sync.RWMutex
	sessions   map[string]*activeSession
}

// NewServer constructs an API Server backed by the given store, runner hub, and data directory.
func NewServer(s *store.Store, h *hub.Hub, dataDir string, logger *slog.Logger) *Server {
	return NewServerWithOptions(s, h, ServerOptions{
		DataDir:              dataDir,
		Logger:               logger,
		ApprovalIdleDuration: time.Duration(config.DefaultApprovalIdleMS) * time.Millisecond,
	})
}

// NewServerWithOptions constructs an API Server with explicit notification and approval detector settings.
func NewServerWithOptions(s *store.Store, h *hub.Hub, opts ServerOptions) *Server {
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	dataDir := opts.DataDir
	if dataDir == "" {
		dataDir = config.DefaultDataDir
	}
	idleDur := opts.ApprovalIdleDuration
	if idleDur < 0 {
		idleDur = time.Duration(config.DefaultApprovalIdleMS) * time.Millisecond
	}
	ntfyURL := strings.TrimRight(strings.TrimSpace(opts.NtfyURL), "/")
	ntfyTopic := strings.Trim(strings.TrimSpace(opts.NtfyTopic), "/")
	notifier := opts.Notifier
	if notifier == nil && ntfyURL != "" && ntfyTopic != "" {
		notifier = notify.NewNtfyNotifier(ntfyURL, ntfyTopic, nil)
	}
	return &Server{
		store:                s,
		hub:                  h,
		dataDir:              dataDir,
		logger:               logger,
		notifier:             notifier,
		ntfyURL:              ntfyURL,
		ntfyTopic:            ntfyTopic,
		approvalIdleDuration: idleDur,
		sessions:             make(map[string]*activeSession),
	}
}

// RegisterRoutes mounts all /api/... and /ws/sessions/... endpoints onto mux protected by mobile Bearer auth.
func (s *Server) RegisterRoutes(mux *http.ServeMux, authn *auth.Authenticator) {
	mobileOnly := authn.RequireScope(config.ScopeMobile)
	mux.Handle("GET /api/machines", mobileOnly(http.HandlerFunc(s.handleListMachines)))
	mux.Handle("GET /api/machines/{name}/fs", mobileOnly(http.HandlerFunc(s.handleListMachineFS)))
	mux.Handle("GET /api/projects", mobileOnly(http.HandlerFunc(s.handleListProjects)))
	mux.Handle("POST /api/projects", mobileOnly(http.HandlerFunc(s.handleCreateProject)))
	mux.Handle("DELETE /api/projects/{id}", mobileOnly(http.HandlerFunc(s.handleDeleteProject)))
	mux.Handle("POST /api/projects/{id}/exec", mobileOnly(http.HandlerFunc(s.handleExecProject)))
	mux.Handle("GET /api/agents", mobileOnly(http.HandlerFunc(s.handleListAgents)))
	mux.Handle("GET /api/agents/{id}/commands", mobileOnly(http.HandlerFunc(s.handleGetAgentCommands)))
	mux.Handle("GET /api/config/notifications", mobileOnly(http.HandlerFunc(s.handleGetNotificationConfig)))
	mux.Handle("POST /api/sessions", mobileOnly(http.HandlerFunc(s.handleCreateSession)))
	mux.Handle("GET /api/sessions", mobileOnly(http.HandlerFunc(s.handleListSessions)))
	mux.Handle("DELETE /api/sessions/{id}", mobileOnly(http.HandlerFunc(s.handleDeleteSession)))
	mux.Handle("GET /api/sessions/{id}/transcript", mobileOnly(http.HandlerFunc(s.handleGetSessionTranscript)))
	mux.Handle("GET /api/sessions/{id}/messages", mobileOnly(http.HandlerFunc(s.handleListSessionMessages)))
	mux.Handle("POST /api/sessions/{id}/messages", mobileOnly(http.HandlerFunc(s.handleCreateSessionMessage)))
	mux.Handle("POST /api/sessions/{id}/cancel", mobileOnly(http.HandlerFunc(s.handleCancelSessionTurn)))
	mux.Handle("GET /api/sessions/{id}/command", mobileOnly(http.HandlerFunc(s.handleGetSessionCommand)))
	mux.Handle("PATCH /api/sessions/{id}/options", mobileOnly(http.HandlerFunc(s.handleUpdateSessionOptions)))
	mux.Handle("POST /api/sessions/{id}/menu-action", mobileOnly(http.HandlerFunc(s.handleSessionMenuAction)))
	mux.Handle("GET /ws/sessions/{id}", mobileOnly(http.HandlerFunc(s.handleSessionWS)))
	mux.Handle("GET /api/tokens", mobileOnly(http.HandlerFunc(s.handleListTokens)))
	mux.Handle("POST /api/tokens", mobileOnly(http.HandlerFunc(s.handleCreateToken)))
	mux.Handle("DELETE /api/tokens/{id}", mobileOnly(http.HandlerFunc(s.handleRevokeToken)))
}

// Handler returns a standalone HTTP handler with authentication middleware applied.
func (s *Server) Handler(authn *auth.Authenticator) http.Handler {
	mux := http.NewServeMux()
	s.RegisterRoutes(mux, authn)
	return authn.RequireAuth()(mux)
}

func (s *Server) handleGetNotificationConfig(w http.ResponseWriter, _ *http.Request) {
	enabled := s.ntfyURL != "" && s.ntfyTopic != ""
	writeJSON(w, http.StatusOK, notificationConfigResponse{
		Enabled:   enabled,
		NtfyURL:   s.ntfyURL,
		NtfyTopic: s.ntfyTopic,
	})
}

func decodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxRequestBodyBytes))
	return dec.Decode(dst)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
