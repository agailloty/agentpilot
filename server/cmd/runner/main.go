package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/agailloty/agentpilot/server/internal/pty"
	"github.com/agailloty/agentpilot/server/internal/runner"
)

type runnerConfig struct {
	ServerURL string
	Token     string
	Name      string
	Roots     []string
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "inspect" {
		if err := runInspectCommand(os.Args[2:], os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "agentpilot-runner inspect: %v\n", err)
			os.Exit(1)
		}
		return
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := parseRunnerConfig(os.Args[1:], os.Getenv, os.Hostname)
	if err != nil {
		logger.Error("invalid runner configuration", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ptyMgr := pty.NewManager()
	defer func() {
		if err := ptyMgr.CloseAll(); err != nil {
			logger.Warn("failed to close active PTY sessions on shutdown", "error", err)
		}
	}()

	client, err := runner.NewClient(runner.Config{
		ServerURL:   cfg.ServerURL,
		Token:       cfg.Token,
		MachineName: cfg.Name,
		Roots:       cfg.Roots,
		Logger:      logger,
		PTYManager:  ptyMgr,
	})
	if err != nil {
		logger.Error("failed to initialize runner client", "error", err)
		os.Exit(1)
	}

	logger.Info("agentpilot-runner starting",
		"server", cfg.ServerURL,
		"name", cfg.Name,
		"roots", cfg.Roots,
	)

	if err := client.Run(ctx); err != nil {
		logger.Error("runner exited with error", "error", err)
		os.Exit(1)
	}
	logger.Info("runner shutdown complete")
}

func parseRunnerConfig(
	args []string,
	getenv func(string) string,
	hostname func() (string, error),
) (runnerConfig, error) {
	var (
		serverFlag = getenv("AGENTPILOT_SERVER_URL")
		tokenFlag  = getenv("AGENTPILOT_RUNNER_TOKEN")
		nameFlag   = getenv("AGENTPILOT_MACHINE_NAME")
		rootsFlag  = getenv("AGENTPILOT_PROJECT_ROOTS")
	)

	fs := flag.NewFlagSet("agentpilot-runner", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&serverFlag, "server", serverFlag, "Backend server URL (required)")
	fs.StringVar(&tokenFlag, "token", tokenFlag, "Runner Bearer token (required)")
	fs.StringVar(&nameFlag, "name", nameFlag, "Machine display name")
	fs.StringVar(&rootsFlag, "roots", rootsFlag, "Comma-separated project root directories")

	if err := fs.Parse(args); err != nil {
		return runnerConfig{}, err
	}

	serverURL := strings.TrimSpace(serverFlag)
	if serverURL == "" {
		return runnerConfig{}, errors.New("--server or AGENTPILOT_SERVER_URL is required")
	}
	parsed, err := url.Parse(serverURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "ws" && parsed.Scheme != "wss") {
		return runnerConfig{}, errors.New("--server must be a valid http(s) or ws(s) URL")
	}

	token := strings.TrimSpace(tokenFlag)
	if token == "" {
		return runnerConfig{}, errors.New("--token or AGENTPILOT_RUNNER_TOKEN is required")
	}

	name := strings.TrimSpace(nameFlag)
	if name == "" {
		host, err := hostname()
		if err != nil || strings.TrimSpace(host) == "" {
			name = "unknown-host"
		} else {
			name = strings.TrimSpace(host)
		}
	}

	return runnerConfig{
		ServerURL: serverURL,
		Token:     token,
		Name:      name,
		Roots:     splitCSV(rootsFlag),
	}, nil
}

func splitCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func runInspectCommand(args []string, stdout io.Writer) error {
	var (
		agentID      string
		cmdBinary    string
		cmdArgsRaw   string
		manifestPath string
		timeout      = 8 * time.Second
	)

	fs := flag.NewFlagSet("agentpilot-runner inspect", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&agentID, "agent-id", "", "Agent ID (required)")
	fs.StringVar(&cmdBinary, "cmd", "", "Agent CLI binary to inspect (required)")
	fs.StringVar(&cmdArgsRaw, "args", "", "Optional space-separated arguments for --cmd")
	fs.StringVar(&manifestPath, "manifest", "", "Optional path to existing manifest JSON for diff")
	fs.DurationVar(&timeout, "timeout", timeout, "Inspection timeout (default 8s)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ptyMgr := pty.NewManager()
	defer func() {
		_ = ptyMgr.CloseAll()
	}()

	res, err := runner.RunInspect(ctx, runner.InspectOptions{
		AgentID:      agentID,
		Command:      cmdBinary,
		Args:         splitCLIArgs(cmdArgsRaw),
		ManifestPath: manifestPath,
		Timeout:      timeout,
		PTYManager:   ptyMgr,
	})
	if err != nil {
		return err
	}

	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(res)
}

func splitCLIArgs(raw string) []string {
	var (
		args    []string
		cur     strings.Builder
		quote   rune
		inToken bool
	)

	for _, r := range raw {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote = r
			inToken = true
		case r == ' ' || r == '\t' || r == '\n':
			if inToken {
				args = append(args, cur.String())
				cur.Reset()
				inToken = false
			}
		default:
			cur.WriteRune(r)
			inToken = true
		}
	}
	if inToken {
		args = append(args, cur.String())
	}
	return args
}
