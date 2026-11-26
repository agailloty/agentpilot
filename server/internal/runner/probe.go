package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/agailloty/agentpilot/server/internal/pty"
)

const shimProbeTimeout = 2 * time.Second

// ResolveCommand probes whether cmd is installed and executable on this host and
// returns its cleaned absolute path. Empty commands or "__shell__" resolve to
// the platform default interactive shell.
func ResolveCommand(cmd string) (installed bool, resolvedPath string) {
	trimmed := strings.TrimSpace(cmd)
	if trimmed == "" || trimmed == "__shell__" {
		shellCmd, _ := pty.DefaultShell()
		found, err := exec.LookPath(shellCmd)
		if err != nil {
			return false, ""
		}
		cleaned := cleanAbsPath(found)
		if !isExecutableFile(cleaned) {
			return false, ""
		}
		return true, cleaned
	}

	var resolved string
	if found, err := exec.LookPath(trimmed); err == nil {
		resolved = found
	} else if !filepath.IsAbs(trimmed) && !strings.ContainsAny(trimmed, `/\`) {
		if fallback, ok := probeUserCLIDirs(trimmed); ok {
			resolved = fallback
		}
	}

	if resolved == "" {
		return false, ""
	}

	cleaned := cleanAbsPath(resolved)
	if !isExecutableFile(cleaned) {
		return false, ""
	}
	if isFailedCopilotShim(cleaned) {
		return false, ""
	}
	return true, cleaned
}

func probeUserCLIDirs(cmd string) (string, bool) {
	candidates := []string{cmd}
	if runtime.GOOS == "windows" && filepath.Ext(cmd) == "" {
		candidates = append(candidates, cmd+".exe", cmd+".cmd", cmd+".bat")
	}

	for _, dir := range userCLIDirs() {
		for _, name := range candidates {
			candidatePath := filepath.Join(dir, name)
			if isExecutableFile(candidatePath) {
				return candidatePath, true
			}
		}
	}
	return "", false
}

func userCLIDirs() []string {
	var dirs []string
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		dirs = append(dirs, filepath.Join(home, ".local", "bin"))
	}
	if runtime.GOOS == "windows" {
		if appData := strings.TrimSpace(os.Getenv("APPDATA")); appData != "" {
			dirs = append(dirs, filepath.Join(appData, "npm"))
		}
	}
	return dirs
}

func cleanAbsPath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(p)
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return false
	}
	return true
}

func isFailedCopilotShim(path string) bool {
	if !strings.Contains(strings.ToLower(path), "copilot-shim") {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), shimProbeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	if err != nil {
		return true
	}
	return strings.Contains(strings.ToLower(string(out)), "was not found")
}
