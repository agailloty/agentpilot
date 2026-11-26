package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/agailloty/agentpilot/server/internal/approval"
	"github.com/agailloty/agentpilot/server/internal/protocol"
	"github.com/agailloty/agentpilot/server/internal/pty"
)

const (
	defaultExecTimeout        = 60 * time.Second
	defaultChatTurnTimeout    = 5 * time.Minute
	defaultMenuInspectTimeout = 15 * time.Second
)

func (c *Client) handleEnvelope(ctx context.Context, wc *wsConn, env protocol.Envelope, handlerWG *sync.WaitGroup) {
	switch env.Type {
	case protocol.TypeSessionCreate:
		c.handleSessionCreate(wc, env)
	case protocol.TypeInputWrite:
		c.handleInputWrite(wc, env)
	case protocol.TypeSessionResize:
		c.handleSessionResize(wc, env)
	case protocol.TypeSessionClose:
		c.handleSessionClose(wc, env)
	case protocol.TypeFSMkdir:
		c.handleFSMkdir(ctx, wc, env)
	case protocol.TypeFSList:
		c.handleFSList(wc, env)
	case protocol.TypeFSDelete:
		c.handleFSDelete(wc, env)
	case protocol.TypeExecSync:
		handlerWG.Add(1)
		go func() {
			defer handlerWG.Done()
			c.handleExecSync(ctx, wc, env)
		}()
	case protocol.TypeToolsProbe:
		handlerWG.Add(1)
		go func() {
			defer handlerWG.Done()
			c.handleToolsProbe(wc, env)
		}()
	case protocol.TypeChatTurn:
		c.handleChatTurn(ctx, wc, env, handlerWG)
	case protocol.TypeChatCancel:
		c.handleChatCancel(wc, env)
	case protocol.TypeMenuInspect:
		handlerWG.Add(1)
		go func() {
			defer handlerWG.Done()
			c.handleMenuInspect(ctx, wc, env)
		}()
	default:
		if env.CorrelationID != "" {
			c.replyError(wc, env.SessionID, env.CorrelationID, fmt.Sprintf("unsupported message type: %s", env.Type))
		}
	}
}

func (c *Client) handleSessionCreate(wc *wsConn, env protocol.Envelope) {
	if strings.TrimSpace(env.SessionID) == "" {
		c.replyError(wc, env.SessionID, env.CorrelationID, "sessionId is required")
		return
	}

	var payload protocol.SessionCreatePayload
	if err := env.DecodePayload(&payload); err != nil {
		c.replyError(wc, env.SessionID, env.CorrelationID, err.Error())
		return
	}

	rawCmd := strings.TrimSpace(payload.Command)
	args := payload.Args
	if rawCmd == "" || rawCmd == "__shell__" {
		_, defaultArgs := pty.DefaultShell()
		if len(args) == 0 {
			args = defaultArgs
		}
	}

	installed, resolvedPath := ResolveCommand(rawCmd)
	if !installed {
		displayCmd := rawCmd
		if displayCmd == "" {
			displayCmd = "__shell__"
		}
		c.replyError(wc, env.SessionID, env.CorrelationID, fmt.Sprintf("CLI tool %q is not installed on this runner", displayCmd))
		return
	}

	c.registerStream(env.SessionID)
	sess, err := c.ptyMgr.Spawn(env.SessionID, pty.SpawnOptions{
		Command: resolvedPath,
		Args:    args,
		Cwd:     payload.Cwd,
		Env:     payload.Env,
		Cols:    payload.Cols,
		Rows:    payload.Rows,
	})
	if err != nil {
		c.stateMu.Lock()
		delete(c.streams, env.SessionID)
		c.stateMu.Unlock()
		c.replyError(wc, env.SessionID, env.CorrelationID, err.Error())
		return
	}

	go c.pumpSession(env.SessionID, sess)
	c.replyAck(wc, env.SessionID, env.CorrelationID, nil)
}

func (c *Client) pumpSession(sessionID string, sess pty.Session) {
	buf := make([]byte, ptyReadBufSize)
	for {
		n, err := sess.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			c.emitOutputChunk(sessionID, chunk)
		}
		if err != nil {
			break
		}
	}

	exitCode, _ := sess.Wait()
	_ = c.ptyMgr.Close(sessionID)
	c.emitSessionExit(sessionID, exitCode)
}

func (c *Client) handleInputWrite(wc *wsConn, env protocol.Envelope) {
	var payload protocol.DataPayload
	if err := env.DecodePayload(&payload); err != nil {
		if env.CorrelationID != "" {
			c.replyError(wc, env.SessionID, env.CorrelationID, err.Error())
		}
		return
	}

	sess, ok := c.ptyMgr.Get(env.SessionID)
	if !ok {
		if env.CorrelationID != "" {
			c.replyError(wc, env.SessionID, env.CorrelationID, pty.ErrSessionNotFound.Error())
		}
		return
	}

	if _, err := sess.Write(payload.Data); err != nil {
		if env.CorrelationID != "" {
			c.replyError(wc, env.SessionID, env.CorrelationID, err.Error())
		}
		return
	}
	if env.CorrelationID != "" {
		c.replyAck(wc, env.SessionID, env.CorrelationID, nil)
	}
}

func (c *Client) handleSessionResize(wc *wsConn, env protocol.Envelope) {
	var payload protocol.ResizePayload
	if err := env.DecodePayload(&payload); err != nil {
		if env.CorrelationID != "" {
			c.replyError(wc, env.SessionID, env.CorrelationID, err.Error())
		}
		return
	}

	sess, ok := c.ptyMgr.Get(env.SessionID)
	if !ok {
		if env.CorrelationID != "" {
			c.replyError(wc, env.SessionID, env.CorrelationID, pty.ErrSessionNotFound.Error())
		}
		return
	}

	if err := sess.Resize(payload.Cols, payload.Rows); err != nil {
		if env.CorrelationID != "" {
			c.replyError(wc, env.SessionID, env.CorrelationID, err.Error())
		}
		return
	}
	if env.CorrelationID != "" {
		c.replyAck(wc, env.SessionID, env.CorrelationID, nil)
	}
}

func (c *Client) handleSessionClose(wc *wsConn, env protocol.Envelope) {
	if err := c.ptyMgr.Close(env.SessionID); err != nil {
		c.replyError(wc, env.SessionID, env.CorrelationID, err.Error())
		return
	}
	c.replyAck(wc, env.SessionID, env.CorrelationID, nil)
}

func (c *Client) handleFSMkdir(ctx context.Context, wc *wsConn, env protocol.Envelope) {
	var payload protocol.FSMkdirPayload
	if err := env.DecodePayload(&payload); err != nil {
		c.replyError(wc, env.SessionID, env.CorrelationID, err.Error())
		return
	}
	target := strings.TrimSpace(payload.Path)
	if target == "" {
		c.replyError(wc, env.SessionID, env.CorrelationID, "path is required")
		return
	}
	target = filepath.Clean(target)

	if err := os.MkdirAll(target, 0o755); err != nil {
		c.replyError(wc, env.SessionID, env.CorrelationID, err.Error())
		return
	}

	if payload.GitInit {
		gitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		cmd := exec.CommandContext(gitCtx, "git", "init", target)
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			c.replyError(wc, env.SessionID, env.CorrelationID, fmt.Sprintf("git init failed: %v (%s)", err, strings.TrimSpace(string(out))))
			return
		}
	}

	c.replyAck(wc, env.SessionID, env.CorrelationID, protocol.FSMkdirAckPayload{Path: target})
}

func (c *Client) handleFSList(wc *wsConn, env protocol.Envelope) {
	var payload protocol.FSListPayload
	if err := env.DecodePayload(&payload); err != nil {
		c.replyError(wc, env.SessionID, env.CorrelationID, err.Error())
		return
	}
	root := strings.TrimSpace(payload.Root)
	if root == "" {
		if len(c.cfg.Roots) > 0 && strings.TrimSpace(c.cfg.Roots[0]) != "" {
			root = strings.TrimSpace(c.cfg.Roots[0])
		} else {
			home, err := os.UserHomeDir()
			if err != nil {
				c.replyError(wc, env.SessionID, env.CorrelationID, err.Error())
				return
			}
			root = home
		}
	}

	if root == "__drives__" {
		if runtime.GOOS != "windows" {
			c.replyError(wc, env.SessionID, env.CorrelationID, "drives listing is only supported on windows")
			return
		}
		entries := make([]protocol.FSEntry, 0, 8)
		for ch := 'A'; ch <= 'Z'; ch++ {
			drive := fmt.Sprintf("%c:\\", ch)
			if _, err := os.Stat(drive); err == nil {
				entries = append(entries, protocol.FSEntry{
					Name: drive,
					Path: drive,
				})
			}
		}
		c.replyAck(wc, env.SessionID, env.CorrelationID, protocol.FSListAckPayload{
			CurrentPath: "__drives__",
			ParentPath:  "",
			Entries:     entries,
		})
		return
	}

	root = filepath.Clean(root)

	dirEntries, err := os.ReadDir(root)
	if err != nil {
		c.replyError(wc, env.SessionID, env.CorrelationID, err.Error())
		return
	}

	entries := make([]protocol.FSEntry, 0, len(dirEntries))
	for _, entry := range dirEntries {
		if !entry.IsDir() {
			continue
		}
		entries = append(entries, protocol.FSEntry{
			Name: entry.Name(),
			Path: filepath.Join(root, entry.Name()),
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		iDot := strings.HasPrefix(entries[i].Name, ".")
		jDot := strings.HasPrefix(entries[j].Name, ".")
		if iDot != jDot {
			return !iDot
		}
		li := strings.ToLower(entries[i].Name)
		lj := strings.ToLower(entries[j].Name)
		if li != lj {
			return li < lj
		}
		return entries[i].Name < entries[j].Name
	})

	parentPath := filepath.Dir(root)
	if parentPath == root {
		if runtime.GOOS == "windows" {
			parentPath = "__drives__"
		} else {
			parentPath = ""
		}
	}

	c.replyAck(wc, env.SessionID, env.CorrelationID, protocol.FSListAckPayload{
		CurrentPath: root,
		ParentPath:  parentPath,
		Entries:     entries,
	})
}

func (c *Client) handleFSDelete(wc *wsConn, env protocol.Envelope) {
	var payload protocol.FSDeletePayload
	if err := env.DecodePayload(&payload); err != nil {
		c.replyError(wc, env.SessionID, env.CorrelationID, err.Error())
		return
	}
	target := strings.TrimSpace(payload.Path)
	if target == "" {
		c.replyError(wc, env.SessionID, env.CorrelationID, "path is required")
		return
	}
	target = filepath.Clean(target)
	if isDangerousDeletePath(target) {
		c.replyError(wc, env.SessionID, env.CorrelationID, "refusing to delete root or base path")
		return
	}

	if err := os.RemoveAll(target); err != nil {
		c.replyError(wc, env.SessionID, env.CorrelationID, err.Error())
		return
	}
	c.replyAck(wc, env.SessionID, env.CorrelationID, protocol.FSDeleteAckPayload{Path: target})
}

func isDangerousDeletePath(cleaned string) bool {
	if cleaned == "" || cleaned == "." || cleaned == ".." || cleaned == string(filepath.Separator) || cleaned == "/" {
		return true
	}
	vol := filepath.VolumeName(cleaned)
	return vol != "" && (cleaned == vol || cleaned == vol+string(filepath.Separator))
}

func (c *Client) handleExecSync(ctx context.Context, wc *wsConn, env protocol.Envelope) {
	var payload protocol.ExecSyncPayload
	if err := env.DecodePayload(&payload); err != nil {
		c.replyError(wc, env.SessionID, env.CorrelationID, err.Error())
		return
	}
	if strings.TrimSpace(payload.Command) == "" {
		c.replyError(wc, env.SessionID, env.CorrelationID, "command is required")
		return
	}

	timeout := defaultExecTimeout
	if payload.TimeoutSec > 0 {
		timeout = time.Duration(payload.TimeoutSec) * time.Second
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(execCtx, "cmd.exe", "/C", payload.Command)
	} else {
		cmd = exec.CommandContext(execCtx, "sh", "-c", payload.Command)
	}
	if strings.TrimSpace(payload.Cwd) != "" {
		cmd.Dir = filepath.Clean(payload.Cwd)
	}

	start := time.Now()
	out, err := cmd.CombinedOutput()
	durationMs := time.Since(start).Milliseconds()

	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		switch {
		case errors.As(err, &exitErr):
			exitCode = exitErr.ExitCode()
		case errors.Is(execCtx.Err(), context.DeadlineExceeded):
			exitCode = -1
		default:
			c.replyError(wc, env.SessionID, env.CorrelationID, err.Error())
			return
		}
	}

	c.replyAck(wc, env.SessionID, env.CorrelationID, protocol.ExecSyncAckPayload{
		ExitCode:   exitCode,
		Output:     string(out),
		DurationMs: durationMs,
	})
}

func (c *Client) handleToolsProbe(wc *wsConn, env protocol.Envelope) {
	var payload protocol.ToolsProbePayload
	if err := env.DecodePayload(&payload); err != nil {
		c.replyError(wc, env.SessionID, env.CorrelationID, err.Error())
		return
	}

	tools := make(map[string]protocol.ToolStatus, len(payload.Commands))
	for _, rawCmd := range payload.Commands {
		cmd := strings.TrimSpace(rawCmd)
		if cmd == "" {
			continue
		}
		if _, seen := tools[cmd]; seen {
			continue
		}
		installed, resolvedPath := ResolveCommand(cmd)
		tools[cmd] = protocol.ToolStatus{
			Command:   cmd,
			Installed: installed,
			Path:      resolvedPath,
		}
	}

	c.replyAck(wc, env.SessionID, env.CorrelationID, protocol.ToolsProbeAckPayload{
		Tools: tools,
	})
}

type chatStreamWriter struct {
	mu      sync.Mutex
	full    strings.Builder
	onDelta func(delta string)
}

func (w *chatStreamWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	cleaned := string(approval.StripANSI(p))
	if cleaned != "" {
		w.mu.Lock()
		w.full.WriteString(cleaned)
		w.mu.Unlock()
		if w.onDelta != nil {
			w.onDelta(cleaned)
		}
	}
	return len(p), nil
}

func (w *chatStreamWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.full.String()
}

func (c *Client) handleChatTurn(ctx context.Context, wc *wsConn, env protocol.Envelope, handlerWG *sync.WaitGroup) {
	if strings.TrimSpace(env.SessionID) == "" {
		c.replyError(wc, env.SessionID, env.CorrelationID, "sessionId is required")
		return
	}

	var payload protocol.ChatTurnPayload
	if err := env.DecodePayload(&payload); err != nil {
		c.replyError(wc, env.SessionID, env.CorrelationID, err.Error())
		return
	}
	if strings.TrimSpace(payload.MessageID) == "" {
		c.replyError(wc, env.SessionID, env.CorrelationID, "messageId is required")
		return
	}

	timeout := defaultChatTurnTimeout
	if payload.TimeoutSec > 0 {
		timeout = time.Duration(payload.TimeoutSec) * time.Second
	}
	turnCtx, cancel := context.WithTimeout(ctx, timeout)

	var cmd *exec.Cmd
	rawCmd := strings.TrimSpace(payload.Command)
	isShellMode := strings.EqualFold(strings.TrimSpace(payload.ChatMode), "shell") || rawCmd == "" || rawCmd == "__shell__"
	if isShellMode {
		if runtime.GOOS == "windows" {
			cmd = exec.CommandContext(turnCtx, "cmd.exe", "/C", payload.Prompt)
		} else {
			cmd = exec.CommandContext(turnCtx, "sh", "-c", payload.Prompt)
		}
	} else {
		installed, resolvedPath := ResolveCommand(rawCmd)
		if !installed {
			cancel()
			c.replyError(wc, env.SessionID, env.CorrelationID, fmt.Sprintf("CLI tool %q is not installed on this runner", rawCmd))
			return
		}
		cmd = exec.CommandContext(turnCtx, resolvedPath, payload.Args...)
	}

	if strings.TrimSpace(payload.Cwd) != "" {
		cmd.Dir = filepath.Clean(payload.Cwd)
	}
	if len(payload.Env) > 0 {
		merged := os.Environ()
		for k, v := range payload.Env {
			merged = append(merged, k+"="+v)
		}
		cmd.Env = merged
	}

	writer := &chatStreamWriter{
		onDelta: func(delta string) {
			deltaEnv, err := protocol.NewEnvelope(protocol.TypeChatDelta, env.SessionID, "", protocol.ChatDeltaPayload{
				MessageID: payload.MessageID,
				Delta:     delta,
			})
			if err == nil {
				c.emitChatEnvelope(deltaEnv)
			}
		},
	}
	var stderrBuf bytes.Buffer
	cmd.Stdout = writer
	if isShellMode {
		cmd.Stderr = writer
	} else {
		cmd.Stderr = &stderrBuf
	}

	start := time.Now()
	if err := cmd.Start(); err != nil {
		cancel()
		c.replyError(wc, env.SessionID, env.CorrelationID, err.Error())
		return
	}

	seq := c.registerChatTurn(env.SessionID, cancel)
	c.replyAck(wc, env.SessionID, env.CorrelationID, nil)

	handlerWG.Add(1)
	go func() {
		defer handlerWG.Done()
		defer cancel()
		defer c.unregisterChatTurn(env.SessionID, seq)

		err := cmd.Wait()
		durationMs := time.Since(start).Milliseconds()

		exitCode := 0
		errMsg := ""
		if err != nil {
			var exitErr *exec.ExitError
			switch {
			case errors.Is(turnCtx.Err(), context.Canceled):
				exitCode = -1
				errMsg = "cancelled"
			case errors.Is(turnCtx.Err(), context.DeadlineExceeded):
				exitCode = -1
				errMsg = "timeout"
			case errors.As(err, &exitErr):
				exitCode = exitErr.ExitCode()
			default:
				exitCode = -1
				errMsg = err.Error()
			}
		}

		content := writer.String()
		if !isShellMode && err != nil {
			stderrText := strings.TrimSpace(string(approval.StripANSI(stderrBuf.Bytes())))
			if stderrText != "" {
				if errMsg == "" {
					errMsg = stderrText
				}
				if strings.TrimSpace(content) == "" {
					content = stderrText
				}
			}
		}

		doneEnv, envErr := protocol.NewEnvelope(protocol.TypeChatDone, env.SessionID, "", protocol.ChatDonePayload{
			MessageID:  payload.MessageID,
			Content:    content,
			ExitCode:   exitCode,
			DurationMs: durationMs,
			Error:      errMsg,
		})
		if envErr == nil {
			c.emitChatEnvelope(doneEnv)
		}
	}()
}

func (c *Client) handleChatCancel(wc *wsConn, env protocol.Envelope) {
	if strings.TrimSpace(env.SessionID) != "" {
		c.cancelChatTurn(env.SessionID)
	}
	c.replyAck(wc, env.SessionID, env.CorrelationID, nil)
}

func (c *Client) handleMenuInspect(ctx context.Context, wc *wsConn, env protocol.Envelope) {
	var payload protocol.MenuInspectPayload
	if err := env.DecodePayload(&payload); err != nil {
		c.replyError(wc, env.SessionID, env.CorrelationID, err.Error())
		return
	}
	rawCmd := strings.TrimSpace(payload.Command)
	if rawCmd == "" {
		c.replyError(wc, env.SessionID, env.CorrelationID, "command is required")
		return
	}
	installed, resolvedPath := ResolveCommand(rawCmd)
	if !installed {
		c.replyError(wc, env.SessionID, env.CorrelationID, fmt.Sprintf("CLI tool %q is not installed on this runner", rawCmd))
		return
	}

	timeout := defaultMenuInspectTimeout
	if payload.TimeoutSec > 0 {
		timeout = time.Duration(payload.TimeoutSec) * time.Second
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, resolvedPath, payload.Args...)
	if strings.TrimSpace(payload.Cwd) != "" {
		cmd.Dir = filepath.Clean(payload.Cwd)
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	start := time.Now()
	err := cmd.Run()
	durationMs := time.Since(start).Milliseconds()

	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		switch {
		case errors.As(err, &exitErr):
			exitCode = exitErr.ExitCode()
		case errors.Is(execCtx.Err(), context.DeadlineExceeded):
			exitCode = -1
		default:
			c.replyError(wc, env.SessionID, env.CorrelationID, err.Error())
			return
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

	cleaned := string(approval.StripANSI(outBytes))
	c.replyAck(wc, env.SessionID, env.CorrelationID, protocol.MenuInspectAckPayload{
		ExitCode:   exitCode,
		Output:     cleaned,
		DurationMs: durationMs,
	})
}

func (c *Client) replyAck(wc *wsConn, sessionID, correlationID string, payload any) {
	env, err := protocol.NewEnvelope(protocol.TypeAck, sessionID, correlationID, payload)
	if err != nil {
		return
	}
	c.sendReply(wc, env)
}

func (c *Client) replyError(wc *wsConn, sessionID, correlationID, message string) {
	env, err := protocol.NewEnvelope(protocol.TypeError, sessionID, correlationID, protocol.ErrorPayload{Message: message})
	if err != nil {
		return
	}
	c.sendReply(wc, env)
}
