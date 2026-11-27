package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/agailloty/agentpilot/server/internal/approval"
	"github.com/agailloty/agentpilot/server/internal/protocol"
	"github.com/agailloty/agentpilot/server/internal/pty"
)

const defaultModelsSyncTimeout = 25 * time.Second

// ModelsSyncOptions configures a local model discovery run against an agent CLI.
type ModelsSyncOptions struct {
	Command    string
	Args       []string
	Send       string
	Cwd        string
	Timeout    time.Duration
	PTYManager *pty.Manager
}

// ModelsSyncResult is the outcome of a model discovery run.
type ModelsSyncResult struct {
	ExitCode   int    `json:"exitCode"`
	Output     string `json:"output"`
	DurationMs int64  `json:"durationMs"`
	Mode       string `json:"mode"` // "exec" or "pty"
}

// RunModelsSync discovers the model list of an agent CLI on the local host.
// When Args is non-empty the command runs non-interactively (mode "exec");
// otherwise Send is written into a scratch PTY session (mode "pty").
func RunModelsSync(ctx context.Context, opts ModelsSyncOptions) (ModelsSyncResult, error) {
	rawCmd := strings.TrimSpace(opts.Command)
	if rawCmd == "" {
		return ModelsSyncResult{}, errors.New("models-sync: command is required")
	}
	if len(opts.Args) == 0 && strings.TrimSpace(opts.Send) == "" {
		return ModelsSyncResult{}, errors.New("models-sync: args or send is required")
	}
	installed, resolvedPath := ResolveCommand(rawCmd)
	if !installed {
		return ModelsSyncResult{}, fmt.Errorf("models-sync: CLI tool %q is not installed on this host", rawCmd)
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultModelsSyncTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	if len(opts.Args) > 0 {
		cmd := exec.CommandContext(runCtx, resolvedPath, opts.Args...)
		if strings.TrimSpace(opts.Cwd) != "" {
			cmd.Dir = filepath.Clean(opts.Cwd)
		}
		var stdoutBuf, stderrBuf bytes.Buffer
		cmd.Stdout = &stdoutBuf
		cmd.Stderr = &stderrBuf
		err := cmd.Run()
		exitCode := 0
		if err != nil {
			var exitErr *exec.ExitError
			switch {
			case errors.As(err, &exitErr):
				exitCode = exitErr.ExitCode()
			case errors.Is(runCtx.Err(), context.DeadlineExceeded):
				exitCode = -1
			default:
				return ModelsSyncResult{}, err
			}
		}
		outBytes := stdoutBuf.Bytes()
		if (exitCode != 0 || len(bytes.TrimSpace(outBytes)) == 0) && stderrBuf.Len() > 0 {
			if len(bytes.TrimSpace(outBytes)) == 0 {
				outBytes = stderrBuf.Bytes()
			} else {
				outBytes = append(append(outBytes, '\n'), stderrBuf.Bytes()...)
			}
		}
		return ModelsSyncResult{
			ExitCode:   exitCode,
			Output:     string(approval.StripANSI(outBytes)),
			DurationMs: time.Since(start).Milliseconds(),
			Mode:       "exec",
		}, nil
	}

	ptyMgr := opts.PTYManager
	if ptyMgr == nil {
		ptyMgr = pty.NewManager()
	}
	sessionID := fmt.Sprintf("models-sync-%d", time.Now().UnixNano())
	raw, err := captureScratchPTY(runCtx, ptyMgr, sessionID, resolvedPath, nil, nil,
		opts.Send, timeout, defaultInspectInitWait, defaultInspectIdleWait, true)
	if err != nil {
		return ModelsSyncResult{}, err
	}
	return ModelsSyncResult{
		ExitCode:   0,
		Output:     stripANSIForHelp([]byte(raw)),
		DurationMs: time.Since(start).Milliseconds(),
		Mode:       "pty",
	}, nil
}

// handleModelsSync answers a models-sync request from the backend by running
// the discovery locally and returning the raw output for server-side parsing.
func (c *Client) handleModelsSync(ctx context.Context, wc *wsConn, env protocol.Envelope) {
	var payload protocol.ModelsSyncPayload
	if err := env.DecodePayload(&payload); err != nil {
		c.replyError(wc, env.SessionID, env.CorrelationID, err.Error())
		return
	}

	timeout := defaultModelsSyncTimeout
	if payload.TimeoutSec > 0 {
		timeout = time.Duration(payload.TimeoutSec) * time.Second
	}

	res, err := RunModelsSync(ctx, ModelsSyncOptions{
		Command:    payload.Command,
		Args:       payload.Args,
		Send:       payload.Send,
		Cwd:        payload.Cwd,
		Timeout:    timeout,
		PTYManager: c.ptyMgr,
	})
	if err != nil {
		c.replyError(wc, env.SessionID, env.CorrelationID, err.Error())
		return
	}
	c.replyAck(wc, env.SessionID, env.CorrelationID, protocol.ModelsSyncAckPayload{
		ExitCode:   res.ExitCode,
		Output:     res.Output,
		DurationMs: res.DurationMs,
		Mode:       res.Mode,
	})
}
