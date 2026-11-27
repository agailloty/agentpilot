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

	"github.com/agailloty/agentpilot/server/internal/manifests"
	"github.com/agailloty/agentpilot/server/internal/modelsync"
	"github.com/agailloty/agentpilot/server/internal/pty"
	"github.com/agailloty/agentpilot/server/internal/runner"
	"github.com/agailloty/agentpilot/server/internal/version"
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

	if len(os.Args) > 1 && os.Args[1] == "models" {
		if err := runModelsCommand(os.Args[2:], os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "agentpilot-runner models: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Printf("agentpilot-runner %s\n", version.Version)
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

// modelsCommandResult is the JSON report emitted by the runner models subcommand.
type modelsCommandResult struct {
	AgentID    string            `json:"agentId"`
	Command    string            `json:"command"`
	Mode       string            `json:"mode"`
	ExitCode   int               `json:"exitCode"`
	DurationMs int64             `json:"durationMs"`
	Parser     string            `json:"parser"`
	Models     []modelsync.Model `json:"models"`
	Manifest   string            `json:"manifest,omitempty"`
	Written    bool              `json:"written,omitempty"`
	Raw        string            `json:"raw,omitempty"`
}

// runModelsCommand discovers the live model list of an agent CLI on this host,
// prints it as JSON, and optionally rewrites the model choices of a manifest
// file (--manifest + --write).
func runModelsCommand(args []string, stdout io.Writer) error {
	var (
		agentID      string
		cmdBinary    string
		cmdArgsRaw   string
		send         string
		parser       string
		flagName     string
		pattern      string
		manifestPath string
		write        bool
		cwd          string
		raw          bool
		timeout      = 25 * time.Second
	)

	fs := flag.NewFlagSet("agentpilot-runner models", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&agentID, "agent-id", "", "Agent ID (required)")
	fs.StringVar(&cmdBinary, "cmd", "", "Agent CLI binary to query (required)")
	fs.StringVar(&cmdArgsRaw, "args", "", "Optional space-separated arguments for --cmd (e.g. \"models\")")
	fs.StringVar(&send, "send", "", "Optional text written into a scratch PTY session (e.g. \"/model\\n\")")
	fs.StringVar(&parser, "parser", "", "Output parser: columns, help-flag, json, lines, regex (default from --manifest, else columns)")
	fs.StringVar(&flagName, "flag", "", "Flag whose help section lists models (help-flag parser, default --model)")
	fs.StringVar(&pattern, "pattern", "", "Regexp with capture groups for the regex parser")
	fs.StringVar(&manifestPath, "manifest", "", "Optional manifest JSON file; its modelSync spec provides defaults and --write target")
	fs.BoolVar(&write, "write", false, "Rewrite the model choices of --manifest in place")
	fs.StringVar(&cwd, "cwd", "", "Optional working directory for the discovery run")
	fs.BoolVar(&raw, "raw", false, "Include raw CLI output in the JSON report")
	fs.DurationVar(&timeout, "timeout", timeout, "Discovery timeout (default 25s)")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(agentID) == "" {
		return errors.New("--agent-id is required")
	}
	if strings.TrimSpace(cmdBinary) == "" {
		return errors.New("--cmd is required")
	}

	// Defaults may come from the manifest's modelSync spec.
	var manifest *manifests.Manifest
	if manifestPath != "" {
		rawManifest, err := os.ReadFile(manifestPath)
		if err != nil {
			return fmt.Errorf("read manifest: %w", err)
		}
		var m manifests.Manifest
		if err := json.Unmarshal(rawManifest, &m); err != nil {
			return fmt.Errorf("parse manifest: %w", err)
		}
		manifest = &m
		if m.ModelSync != nil {
			if parser == "" {
				parser = m.ModelSync.Parser
			}
			if cmdArgsRaw == "" && len(m.ModelSync.Args) > 0 {
				cmdArgsRaw = strings.Join(m.ModelSync.Args, " ")
			}
			if send == "" {
				send = m.ModelSync.Send
			}
			if flagName == "" {
				flagName = m.ModelSync.Flag
			}
			if pattern == "" {
				pattern = m.ModelSync.Pattern
			}
			if timeout == 25*time.Second && m.ModelSync.TimeoutSec > 0 {
				timeout = time.Duration(m.ModelSync.TimeoutSec) * time.Second
			}
		}
	}
	if parser == "" {
		parser = modelsync.ParserColumns
	}
	if !modelsync.IsKnownParser(parser) {
		return fmt.Errorf("unknown parser %q (known: %s)", parser, strings.Join(modelsync.Parsers(), ", "))
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ptyMgr := pty.NewManager()
	defer func() {
		_ = ptyMgr.CloseAll()
	}()

	res, err := runner.RunModelsSync(ctx, runner.ModelsSyncOptions{
		Command:    cmdBinary,
		Args:       splitCLIArgs(cmdArgsRaw),
		Send:       send,
		Cwd:        cwd,
		Timeout:    timeout,
		PTYManager: ptyMgr,
	})
	if err != nil {
		return err
	}

	models, parseErr := modelsync.Parse(parser, flagName, pattern, res.Output)
	if parseErr != nil {
		return parseErr
	}

	out := modelsCommandResult{
		AgentID:    agentID,
		Command:    cmdBinary,
		Mode:       res.Mode,
		ExitCode:   res.ExitCode,
		DurationMs: res.DurationMs,
		Parser:     parser,
		Models:     models,
		Manifest:   manifestPath,
	}
	if raw {
		out.Raw = res.Output
	}

	if write {
		if manifest == nil || manifest.ModelSync == nil {
			return errors.New("--write requires --manifest pointing to a manifest with a modelSync block")
		}
		if len(models) == 0 {
			return errors.New("no models parsed from CLI output; manifest left unchanged")
		}
		effSpec := manifests.ModelSyncSpec{OptionID: manifest.ModelSync.OptionID}
		if manifest.ModelSync != nil {
			effSpec = *manifest.ModelSync
		}
		if parser != "" {
			effSpec.Parser = parser
		}
		if flagName != "" {
			effSpec.Flag = flagName
		}
		if pattern != "" {
			effSpec.Pattern = pattern
		}
		updated, _, err := modelsync.ApplyWithSpec(effSpec, *manifest, res.Output)
		if err != nil {
			return err
		}
		encoded, err := json.MarshalIndent(updated, "", "  ")
		if err != nil {
			return err
		}
		encoded = append(encoded, '\n')
		if err := os.WriteFile(manifestPath, encoded, 0o644); err != nil {
			return fmt.Errorf("write manifest: %w", err)
		}
		out.Written = true
		manifest = &updated
	}

	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
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
