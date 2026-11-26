package pty

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	gopty "github.com/aymanbagabas/go-pty"
)

const (
	defaultCols = 80
	defaultRows = 24
)

// SpawnOptions configures a process launched inside a pseudo-terminal.
type SpawnOptions struct {
	Command string
	Args    []string
	Cwd     string
	Env     map[string]string
	Cols    int
	Rows    int
}

// Session represents an active pseudo-terminal session attached to a child process.
type Session interface {
	ID() string
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	Resize(cols, rows int) error
	Wait() (int, error)
	Close() error
}

// DefaultShell returns the platform-appropriate interactive shell command and flags.
func DefaultShell() (string, []string) {
	if runtime.GOOS == "windows" {
		return "cmd.exe", []string{"/Q"}
	}
	if _, err := exec.LookPath("bash"); err == nil {
		return "bash", nil
	}
	return "sh", nil
}

type ptySession struct {
	id  string
	pty gopty.Pty
	cmd *gopty.Cmd

	waitOnce sync.Once
	exitCode int
	waitErr  error

	closeOnce sync.Once
	closeErr  error
}

var _ Session = (*ptySession)(nil)

func spawnSession(id string, opts SpawnOptions) (*ptySession, error) {
	if strings.TrimSpace(id) == "" {
		return nil, errors.New("pty: session ID is required")
	}
	if strings.TrimSpace(opts.Command) == "" {
		return nil, errors.New("pty: command is required")
	}

	p, err := gopty.New()
	if err != nil {
		return nil, fmt.Errorf("create pty: %w", err)
	}

	cols := opts.Cols
	if cols <= 0 {
		cols = defaultCols
	}
	rows := opts.Rows
	if rows <= 0 {
		rows = defaultRows
	}
	if err := p.Resize(cols, rows); err != nil {
		_ = p.Close()
		return nil, fmt.Errorf("initial pty resize (%dx%d): %w", cols, rows, err)
	}

	cmdName := opts.Command
	if !filepath.IsAbs(cmdName) && !strings.ContainsAny(cmdName, `/\`) {
		if resolved, err := exec.LookPath(cmdName); err == nil {
			cmdName = resolved
		}
	}

	cmd := p.Command(cmdName, opts.Args...)
	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}
	if len(opts.Env) > 0 {
		cmd.Env = mergeEnv(os.Environ(), opts.Env, runtime.GOOS == "windows")
	}

	if err := cmd.Start(); err != nil {
		_ = p.Close()
		return nil, fmt.Errorf("start command %q: %w", opts.Command, err)
	}

	return &ptySession{
		id:  id,
		pty: p,
		cmd: cmd,
	}, nil
}

func (s *ptySession) ID() string {
	return s.id
}

func (s *ptySession) Read(p []byte) (int, error) {
	return s.pty.Read(p)
}

func (s *ptySession) Write(p []byte) (int, error) {
	return s.pty.Write(p)
}

func (s *ptySession) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return fmt.Errorf("pty: invalid dimensions %dx%d", cols, rows)
	}
	return s.pty.Resize(cols, rows)
}

func (s *ptySession) Wait() (int, error) {
	s.waitOnce.Do(func() {
		err := s.cmd.Wait()
		if s.cmd.ProcessState != nil {
			s.exitCode = s.cmd.ProcessState.ExitCode()
			s.waitErr = nil
			return
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			s.exitCode = exitErr.ExitCode()
			s.waitErr = nil
			return
		}
		if err != nil {
			s.exitCode = -1
			s.waitErr = err
			return
		}
		s.exitCode = 0
	})
	return s.exitCode, s.waitErr
}

func (s *ptySession) Close() error {
	s.closeOnce.Do(func() {
		var killErr error
		if s.cmd != nil && s.cmd.Process != nil {
			if err := s.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				killErr = err
			}
		}
		ptyErr := s.pty.Close()
		// Reap the process so Unix doesn't keep zombies and Windows frees ConPTY attribute lists.
		_, _ = s.Wait()
		s.closeErr = errors.Join(ptyErr, killErr)
	})
	return s.closeErr
}

func mergeEnv(base []string, overrides map[string]string, caseInsensitive bool) []string {
	out := make([]string, 0, len(base)+len(overrides))
	indexByKey := make(map[string]int, len(base)+len(overrides))

	normalize := func(k string) string {
		if caseInsensitive {
			return strings.ToUpper(k)
		}
		return k
	}

	for _, entry := range base {
		k, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		nk := normalize(k)
		if idx, exists := indexByKey[nk]; exists {
			out[idx] = entry
			continue
		}
		indexByKey[nk] = len(out)
		out = append(out, entry)
	}

	keys := make([]string, 0, len(overrides))
	for k := range overrides {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		entry := k + "=" + overrides[k]
		nk := normalize(k)
		if idx, exists := indexByKey[nk]; exists {
			out[idx] = entry
			continue
		}
		indexByKey[nk] = len(out)
		out = append(out, entry)
	}
	return out
}
