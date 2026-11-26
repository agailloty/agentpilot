package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/agailloty/agentpilot/server/internal/manifests"
	"github.com/agailloty/agentpilot/server/internal/pty"
)

const (
	defaultInspectTimeout  = 8 * time.Second
	defaultInspectInitWait = 400 * time.Millisecond
	defaultInspectIdleWait = 600 * time.Millisecond
)

var (
	reSlashHelpLine = regexp.MustCompile(`^(/[A-Za-z0-9][A-Za-z0-9_-]*)(?:[:\s]+(.*))?$`)
	reHelpBullet    = regexp.MustCompile(`^(?:[*•\-]+)\s+(/.*)$`)
)

// InspectOptions configures a scratch PTY /help inspection run.
type InspectOptions struct {
	AgentID      string
	Command      string
	Args         []string
	Env          map[string]string
	ManifestPath string
	Timeout      time.Duration
	InitWait     time.Duration
	IdleWait     time.Duration
	PTYManager   *pty.Manager
}

// ManifestDiff holds the comparison between an existing manifest and discovered commands.
type ManifestDiff struct {
	Added     []manifests.Command `json:"added"`
	Removed   []manifests.Command `json:"removed"`
	Unchanged []manifests.Command `json:"unchanged"`
}

// InspectResult is the JSON report emitted by the runner inspect subcommand.
type InspectResult struct {
	AgentID    string               `json:"agentId"`
	Discovered []manifests.Command  `json:"discovered"`
	Added      *[]manifests.Command `json:"added,omitempty"`
	Removed    *[]manifests.Command `json:"removed,omitempty"`
	Unchanged  *[]manifests.Command `json:"unchanged,omitempty"`
}

// RunInspect spawns a scratch PTY session for opts.Command, sends /help, collects
// output until idle or timeout, extracts slash commands, and optionally diffs
// against an existing manifest file without modifying it.
func RunInspect(ctx context.Context, opts InspectOptions) (InspectResult, error) {
	agentID := strings.TrimSpace(opts.AgentID)
	command := strings.TrimSpace(opts.Command)
	if agentID == "" {
		return InspectResult{}, errors.New("inspect: agent-id is required")
	}
	if command == "" {
		return InspectResult{}, errors.New("inspect: cmd is required")
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultInspectTimeout
	}
	initWait := opts.InitWait
	if initWait <= 0 {
		initWait = defaultInspectInitWait
	}
	idleWait := opts.IdleWait
	if idleWait <= 0 {
		idleWait = defaultInspectIdleWait
	}

	ptyMgr := opts.PTYManager
	if ptyMgr == nil {
		ptyMgr = pty.NewManager()
	}

	sessionID := fmt.Sprintf("inspect-%s-%d", agentID, time.Now().UnixNano())
	sess, err := ptyMgr.Spawn(sessionID, pty.SpawnOptions{
		Command: command,
		Args:    opts.Args,
		Env:     opts.Env,
		Cols:    120,
		Rows:    40,
	})
	if err != nil {
		return InspectResult{}, fmt.Errorf("inspect: spawn %q: %w", command, err)
	}
	defer func() {
		_ = ptyMgr.Close(sessionID)
	}()

	chunkCh := make(chan []byte, 32)
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		buf := make([]byte, 4096)
		for {
			n, readErr := sess.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				select {
				case chunkCh <- chunk:
				case <-ctx.Done():
					return
				}
			}
			if readErr != nil {
				return
			}
		}
	}()

	var collected bytes.Buffer
	inspectCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Brief initial wait for prompt/startup banner, unless the process exits early.
	initTimer := time.NewTimer(initWait)
initLoop:
	for {
		select {
		case chunk := <-chunkCh:
			collected.Write(chunk)
		case <-doneCh:
			initTimer.Stop()
			drainChunks(&collected, chunkCh)
			break initLoop
		case <-initTimer.C:
			break initLoop
		case <-inspectCtx.Done():
			initTimer.Stop()
			drainChunks(&collected, chunkCh)
			break initLoop
		}
	}

	// Only write /help\r if the scratch session has not already exited.
	select {
	case <-doneCh:
		drainChunks(&collected, chunkCh)
	default:
		if inspectCtx.Err() == nil {
			_, _ = sess.Write([]byte("/help\r"))
			idleTimer := time.NewTimer(idleWait)
		collectLoop:
			for {
				select {
				case chunk := <-chunkCh:
					collected.Write(chunk)
					resetTimer(idleTimer, idleWait)
				case <-doneCh:
					idleTimer.Stop()
					drainChunks(&collected, chunkCh)
					break collectLoop
				case <-idleTimer.C:
					drainChunks(&collected, chunkCh)
					break collectLoop
				case <-inspectCtx.Done():
					idleTimer.Stop()
					drainChunks(&collected, chunkCh)
					break collectLoop
				}
			}
		}
	}

	discovered := ParseHelpOutput(collected.Bytes())
	res := InspectResult{
		AgentID:    agentID,
		Discovered: discovered,
	}

	if manifestPath := strings.TrimSpace(opts.ManifestPath); manifestPath != "" {
		rawManifest, err := os.ReadFile(manifestPath)
		if err != nil {
			return InspectResult{}, fmt.Errorf("inspect: read manifest %q: %w", manifestPath, err)
		}
		var m manifests.Manifest
		if err := json.Unmarshal(rawManifest, &m); err != nil {
			return InspectResult{}, fmt.Errorf("inspect: parse manifest %q: %w", manifestPath, err)
		}
		diff := DiffManifestCommands(m.Commands, discovered)
		res.Added = &diff.Added
		res.Removed = &diff.Removed
		res.Unchanged = &diff.Unchanged
	}

	return res, nil
}

func drainChunks(dst *bytes.Buffer, ch <-chan []byte) {
	for {
		select {
		case chunk := <-ch:
			dst.Write(chunk)
		default:
			return
		}
	}
}

func resetTimer(t *time.Timer, d time.Duration) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	t.Reset(d)
}

// ParseHelpOutput strips ANSI escape codes and extracts slash commands and descriptions.
func ParseHelpOutput(raw []byte) []manifests.Command {
	if len(raw) == 0 {
		return []manifests.Command{}
	}

	clean := stripANSIForHelp(raw)

	lines := strings.Split(clean, "\n")
	out := make([]manifests.Command, 0)
	indexByName := make(map[string]int)

	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		if m := reHelpBullet.FindStringSubmatch(line); m != nil {
			line = strings.TrimSpace(m[1])
		}
		m := reSlashHelpLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}

		name := m[1]
		rest := strings.TrimSpace(m[2])
		args, desc := splitHelpArgsAndDesc(rest)

		// Ignore echoed bare `/help` prompt lines that carry neither args nor description.
		if name == "/help" && args == "" && desc == "" {
			continue
		}

		if idx, exists := indexByName[name]; exists {
			if out[idx].Description == "" && desc != "" {
				out[idx].Description = desc
			}
			if out[idx].Args == "" && args != "" {
				out[idx].Args = args
			}
			continue
		}

		indexByName[name] = len(out)
		out = append(out, manifests.Command{
			Name:        name,
			Description: desc,
			Args:        args,
		})
	}

	return out
}

func splitHelpArgsAndDesc(rest string) (args string, desc string) {
	var argTokens []string
	cur := strings.TrimSpace(rest)

	for len(cur) > 0 && (cur[0] == '[' || cur[0] == '<') {
		closeChar := byte(']')
		if cur[0] == '<' {
			closeChar = '>'
		}
		end := strings.IndexByte(cur, closeChar)
		if end < 0 {
			break
		}
		argTokens = append(argTokens, cur[:end+1])
		cur = strings.TrimSpace(cur[end+1:])
	}

	cur = strings.TrimLeft(cur, ":-–— \t")
	cur = strings.TrimSpace(cur)
	return strings.Join(argTokens, " "), cur
}

// DiffManifestCommands compares discovered slash commands against an existing manifest command list.
func DiffManifestCommands(existing, discovered []manifests.Command) ManifestDiff {
	existingByName := make(map[string]manifests.Command, len(existing))
	for _, cmd := range existing {
		name := strings.TrimSpace(cmd.Name)
		if name != "" {
			existingByName[name] = cmd
		}
	}

	discoveredByName := make(map[string]manifests.Command, len(discovered))
	for _, cmd := range discovered {
		name := strings.TrimSpace(cmd.Name)
		if name != "" {
			discoveredByName[name] = cmd
		}
	}

	diff := ManifestDiff{
		Added:     []manifests.Command{},
		Removed:   []manifests.Command{},
		Unchanged: []manifests.Command{},
	}

	for _, cmd := range discovered {
		name := strings.TrimSpace(cmd.Name)
		if name == "" {
			continue
		}
		if _, ok := existingByName[name]; !ok {
			diff.Added = append(diff.Added, cmd)
		}
	}

	for _, cmd := range existing {
		name := strings.TrimSpace(cmd.Name)
		if name == "" {
			continue
		}
		if _, ok := discoveredByName[name]; ok {
			diff.Unchanged = append(diff.Unchanged, cmd)
		} else {
			diff.Removed = append(diff.Removed, cmd)
		}
	}

	return diff
}

// stripANSIForHelp removes ANSI escape sequences while preserving word/line
// separation for ConPTY cursor-movement sequences (CSI C/G/H/f).
func stripANSIForHelp(raw []byte) string {
	const (
		stNormal = iota
		stEsc
		stEscInterm
		stCSI
		stOSC
		stOSCEsc
	)

	normalized := bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
	normalized = bytes.ReplaceAll(normalized, []byte("\r"), []byte("\n"))

	var out bytes.Buffer
	out.Grow(len(normalized))

	state := stNormal
	var csiParams bytes.Buffer
	lastRow := 0

	for _, b := range normalized {
		switch state {
		case stNormal:
			switch {
			case b == 0x1b:
				state = stEsc
			case b == '\n' || b == '\t':
				out.WriteByte(b)
			case b < 0x20 || b == 0x7f:
				// Drop other C0/DEL control bytes.
			default:
				out.WriteByte(b)
			}
		case stEsc:
			switch {
			case b == '[':
				state = stCSI
				csiParams.Reset()
			case b == ']', b == 'P', b == 'X', b == '^', b == '_':
				state = stOSC
			case b >= 0x20 && b <= 0x2f:
				state = stEscInterm
			default:
				state = stNormal
			}
		case stEscInterm:
			if b < 0x20 || b > 0x2f {
				state = stNormal
			}
		case stCSI:
			switch {
			case b >= 0x40 && b <= 0x7e:
				applyCSISpacing(&out, csiParams.String(), b, &lastRow)
				state = stNormal
			case b == 0x1b:
				state = stEsc
			case b < 0x20 || b > 0x3f:
				state = stNormal
			default:
				csiParams.WriteByte(b)
			}
		case stOSC:
			switch b {
			case 0x07:
				state = stNormal
			case 0x1b:
				state = stOSCEsc
			}
		case stOSCEsc:
			switch b {
			case '\\':
				state = stNormal
			case 0x1b:
				state = stOSCEsc
			default:
				state = stOSC
			}
		}
	}

	return out.String()
}

func applyCSISpacing(out *bytes.Buffer, params string, final byte, lastRow *int) {
	switch final {
	case 'C':
		n := 1
		if parsed, err := strconv.Atoi(strings.TrimSpace(params)); err == nil && parsed > 0 {
			n = min(parsed, 40)
		}
		for i := 0; i < n; i++ {
			out.WriteByte(' ')
		}
	case 'G':
		out.WriteByte(' ')
	case 'H', 'f':
		row := 1
		col := 1
		if params != "" {
			parts := strings.SplitN(params, ";", 2)
			if len(parts) >= 1 && parts[0] != "" {
				if v, err := strconv.Atoi(parts[0]); err == nil && v > 0 {
					row = v
				}
			}
			if len(parts) == 2 && parts[1] != "" {
				if v, err := strconv.Atoi(parts[1]); err == nil && v > 0 {
					col = v
				}
			}
		}
		if *lastRow != 0 && row != *lastRow || col <= 1 {
			out.WriteByte('\n')
		} else {
			out.WriteByte(' ')
		}
		*lastRow = row
	}
}
