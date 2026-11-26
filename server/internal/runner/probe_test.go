package runner

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveCommandShellAndMissing(t *testing.T) {
	for _, cmd := range []string{"__shell__", "", "  __shell__  "} {
		installed, path := ResolveCommand(cmd)
		if !installed {
			t.Fatalf("ResolveCommand(%q) installed = false, want true", cmd)
		}
		if !filepath.IsAbs(path) {
			t.Errorf("ResolveCommand(%q) path = %q, want absolute path", cmd, path)
		}
		if _, err := os.Stat(path); err != nil {
			t.Errorf("Stat(%q) error = %v", path, err)
		}
	}

	installed, path := ResolveCommand("nonexistent-cli-xyz-9999")
	if installed || path != "" {
		t.Fatalf("ResolveCommand(nonexistent) = (%v, %q), want (false, \"\")", installed, path)
	}
}

func TestResolveCommandUserLocalBin(t *testing.T) {
	tmpHome := t.TempDir()
	localBin := filepath.Join(tmpHome, ".local", "bin")
	if err := os.MkdirAll(localBin, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	t.Setenv("HOME", tmpHome)
	t.Setenv("USERPROFILE", tmpHome)
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("PATH", t.TempDir())

	binName := "vibe-test-cli"
	if runtime.GOOS == "windows" {
		binName += ".cmd"
	}
	binPath := filepath.Join(localBin, binName)
	if err := os.WriteFile(binPath, []byte("echo ok\n"), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	installed, got := ResolveCommand("vibe-test-cli")
	if !installed || got != binPath {
		t.Fatalf("ResolveCommand(vibe-test-cli) = (%v, %q), want (true, %q)", installed, got, binPath)
	}

	if runtime.GOOS != "windows" {
		nonExec := filepath.Join(localBin, "nonexec-cli")
		if err := os.WriteFile(nonExec, []byte("echo no\n"), 0o644); err != nil {
			t.Fatalf("WriteFile(nonexec) error = %v", err)
		}
		if ok, p := ResolveCommand("nonexec-cli"); ok || p != "" {
			t.Fatalf("ResolveCommand(nonexec-cli) = (%v, %q), want (false, \"\")", ok, p)
		}
	}
}

func TestResolveCommandWindowsAppDataNpm(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-specific %APPDATA%\\npm fallback test")
	}

	tmpAppData := t.TempDir()
	npmDir := filepath.Join(tmpAppData, "npm")
	if err := os.MkdirAll(npmDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("APPDATA", tmpAppData)
	t.Setenv("PATH", t.TempDir())

	cmdPath := filepath.Join(npmDir, "copilot-npm-test.cmd")
	if err := os.WriteFile(cmdPath, []byte("@echo off\r\necho 1.0.0\r\n"), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	installed, got := ResolveCommand("copilot-npm-test")
	if !installed || got != cmdPath {
		t.Fatalf("ResolveCommand(copilot-npm-test) = (%v, %q), want (true, %q)", installed, got, cmdPath)
	}
}

func TestResolveCommandCopilotShimGuard(t *testing.T) {
	writeShim := func(t *testing.T, exitZero bool, output string) string {
		t.Helper()
		shimDir := filepath.Join(t.TempDir(), "Microsoft VS Code", "bin", "copilot-shim")
		if err := os.MkdirAll(shimDir, 0o755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		var fileName, body string
		if runtime.GOOS == "windows" {
			fileName = "copilot.cmd"
			code := "1"
			if exitZero {
				code = "0"
			}
			body = "@echo off\r\necho " + output + "\r\nexit /b " + code + "\r\n"
		} else {
			fileName = "copilot"
			code := "1"
			if exitZero {
				code = "0"
			}
			body = "#!/bin/sh\necho \"" + output + "\"\nexit " + code + "\n"
		}
		full := filepath.Join(shimDir, fileName)
		if err := os.WriteFile(full, []byte(body), 0o755); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
		t.Setenv("PATH", shimDir)
		t.Setenv("HOME", t.TempDir())
		t.Setenv("USERPROFILE", t.TempDir())
		t.Setenv("APPDATA", t.TempDir())
		return full
	}

	t.Run("non-zero stub", func(t *testing.T) {
		writeShim(t, false, "GitHub Copilot CLI was not found")
		installed, path := ResolveCommand("copilot")
		if installed || path != "" {
			t.Fatalf("ResolveCommand(copilot) = (%v, %q), want (false, \"\")", installed, path)
		}
	})

	t.Run("zero exit with was not found text", func(t *testing.T) {
		writeShim(t, true, "GitHub Copilot CLI was not found")
		installed, path := ResolveCommand("copilot")
		if installed || path != "" {
			t.Fatalf("ResolveCommand(copilot) = (%v, %q), want (false, \"\")", installed, path)
		}
	})

	t.Run("valid shim with version output", func(t *testing.T) {
		full := writeShim(t, true, "1.2.3")
		installed, path := ResolveCommand("copilot")
		if !installed || path != full {
			t.Fatalf("ResolveCommand(copilot) = (%v, %q), want (true, %q)", installed, path, full)
		}
	})
}
