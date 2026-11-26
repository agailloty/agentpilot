package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/agailloty/agentpilot/server/internal/api"
	"github.com/agailloty/agentpilot/server/internal/auth"
	"github.com/agailloty/agentpilot/server/internal/config"
	"github.com/agailloty/agentpilot/server/internal/hub"
	"github.com/agailloty/agentpilot/server/internal/manifests"
	"github.com/agailloty/agentpilot/server/internal/notify"
	"github.com/agailloty/agentpilot/server/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, cfg, logger); err != nil {
		logger.Error("server exited with error", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	dbPath := filepath.Join(cfg.DataDir, "agentpilot.db")
	dbStore, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = dbStore.Close() }()

	if err := auth.BootstrapTokens(ctx, dbStore, cfg.Tokens); err != nil {
		return err
	}
	activeTokens, err := dbStore.CountActiveTokens(ctx)
	if err != nil {
		return err
	}

	manifestsCount := 0
	if strings.TrimSpace(cfg.ManifestsDir) != "" {
		loaded, err := manifests.LoadDir(cfg.ManifestsDir)
		if err != nil {
			return err
		}
		if err := manifests.SyncToStore(ctx, dbStore, loaded); err != nil {
			return err
		}
		manifestsCount = len(loaded)
	}

	runnerHub := hub.New(hub.Options{Logger: logger})
	defer runnerHub.Close()

	ntfyNotifier := notify.NewNtfyNotifier(cfg.NtfyURL, cfg.NtfyTopic, nil)
	authn := auth.NewAuthenticator(dbStore)
	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           newAppHandlerWithConfig(dbStore, runnerHub, cfg, ntfyNotifier, authn, logger),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("agentpilot-server listening",
			"addr", cfg.ListenAddr,
			"data_dir", cfg.DataDir,
			"manifests_dir", cfg.ManifestsDir,
			"web_dir", cfg.WebDir,
			"manifests_loaded", manifestsCount,
			"active_tokens", activeTokens,
			"ntfy_enabled", ntfyNotifier.Enabled(),
			"approval_idle_ms", cfg.ApprovalIdleMS,
		)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.Info("shutdown signal received")
		runnerHub.Close()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

func newAppHandler(dbStore *store.Store, runnerHub *hub.Hub, dataDir string, authn *auth.Authenticator, logger *slog.Logger) http.Handler {
	cfg := config.Config{
		DataDir:        dataDir,
		ApprovalIdleMS: config.DefaultApprovalIdleMS,
	}
	return newAppHandlerWithConfig(dbStore, runnerHub, cfg, nil, authn, logger)
}

func newAppHandlerWithConfig(dbStore *store.Store, runnerHub *hub.Hub, cfg config.Config, notifier notify.Notifier, authn *auth.Authenticator, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /healthz", http.HandlerFunc(handleHealth))
	mux.Handle("GET /ws/runner", authn.RequireScope(config.ScopeRunner)(http.HandlerFunc(runnerHub.HandleRunnerWS)))

	hooks := hub.Hooks{}
	if dbStore != nil {
		if notifier == nil {
			notifier = notify.NewNtfyNotifier(cfg.NtfyURL, cfg.NtfyTopic, nil)
		}
		apiServer := api.NewServerWithOptions(dbStore, runnerHub, api.ServerOptions{
			DataDir:              cfg.DataDir,
			Logger:               logger,
			Notifier:             notifier,
			NtfyURL:              cfg.NtfyURL,
			NtfyTopic:            cfg.NtfyTopic,
			ApprovalIdleDuration: cfg.ApprovalIdleDuration(),
		})
		apiServer.RegisterRoutes(mux, authn)

		hooks.OnOutputChunk = apiServer.HandleOutputChunk
		hooks.OnSessionExit = apiServer.HandleSessionExit
		hooks.OnChatDelta = apiServer.HandleChatDelta
		hooks.OnChatDone = apiServer.HandleChatDone
		hooks.OnRunnerConnect = func(machineName, osName string, roots []string) {
			hookCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := dbStore.UpsertMachine(hookCtx, store.Machine{
				Name:       machineName,
				OS:         osName,
				Roots:      roots,
				Online:     true,
				LastSeenAt: time.Now().UTC(),
			}); err != nil {
				logger.Warn("failed to upsert connected machine", "machine", machineName, "error", err)
			}
		}
		hooks.OnRunnerDisconnect = func(machineName string) {
			hookCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := dbStore.SetMachineOnline(hookCtx, machineName, false, time.Now().UTC()); err != nil && !errors.Is(err, store.ErrNotFound) {
				logger.Warn("failed to mark machine offline", "machine", machineName, "error", err)
			}
		}
	}
	runnerHub.SetHooks(hooks)

	apiHandler := auth.CORS(authn.RequireAuth()(mux))
	return spaHandler(cfg.WebDir, apiHandler)
}

func spaHandler(webDir string, apiHandler http.Handler) http.Handler {
	cleanWebDir := strings.TrimSpace(webDir)
	if cleanWebDir == "" {
		return apiHandler
	}
	absWebDir, err := filepath.Abs(cleanWebDir)
	if err != nil {
		absWebDir = filepath.Clean(cleanWebDir)
	}
	indexPath := filepath.Join(absWebDir, "index.html")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if strings.HasPrefix(p, "/api/") || p == "/api" ||
			strings.HasPrefix(p, "/ws/") || p == "/ws" ||
			p == "/healthz" {
			apiHandler.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			apiHandler.ServeHTTP(w, r)
			return
		}

		cleanURLPath := path.Clean("/" + p)
		relPath := strings.TrimPrefix(cleanURLPath, "/")
		targetPath := filepath.Join(absWebDir, filepath.FromSlash(relPath))

		if rel, err := filepath.Rel(absWebDir, targetPath); err == nil &&
			rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			if info, statErr := os.Stat(targetPath); statErr == nil && !info.IsDir() {
				http.ServeFile(w, r, targetPath)
				return
			}
		}

		http.ServeFile(w, r, indexPath)
	})
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
