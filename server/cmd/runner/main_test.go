package main

import (
	"errors"
	"slices"
	"testing"
)

func TestParseRunnerConfigFromFlags(t *testing.T) {
	cfg, err := parseRunnerConfig(
		[]string{
			"--server", "https://100.64.0.10:8080",
			"--token", "runner-secret",
			"--name", "homelab-pc",
			"--roots", "C:\\repos, D:\\projects",
		},
		func(string) string { return "" },
		func() (string, error) { return "fallback-host", nil },
	)
	if err != nil {
		t.Fatalf("parseRunnerConfig() unexpected error: %v", err)
	}

	if cfg.ServerURL != "https://100.64.0.10:8080" {
		t.Errorf("ServerURL = %q, want %q", cfg.ServerURL, "https://100.64.0.10:8080")
	}
	if cfg.Token != "runner-secret" {
		t.Errorf("Token = %q, want %q", cfg.Token, "runner-secret")
	}
	if cfg.Name != "homelab-pc" {
		t.Errorf("Name = %q, want %q", cfg.Name, "homelab-pc")
	}
	wantRoots := []string{`C:\repos`, `D:\projects`}
	if !slices.Equal(cfg.Roots, wantRoots) {
		t.Errorf("Roots = %v, want %v", cfg.Roots, wantRoots)
	}
}

func TestParseRunnerConfigFromEnvAndHostnameFallback(t *testing.T) {
	env := map[string]string{
		"AGENTPILOT_SERVER_URL":    "ws://127.0.0.1:8080",
		"AGENTPILOT_RUNNER_TOKEN":  "env-token",
		"AGENTPILOT_PROJECT_ROOTS": "/home/user/src,/srv/work",
	}

	cfg, err := parseRunnerConfig(
		nil,
		func(k string) string { return env[k] },
		func() (string, error) { return "linux-box", nil },
	)
	if err != nil {
		t.Fatalf("parseRunnerConfig() unexpected error: %v", err)
	}

	if cfg.Name != "linux-box" {
		t.Errorf("Name = %q, want %q", cfg.Name, "linux-box")
	}
	wantRoots := []string{"/home/user/src", "/srv/work"}
	if !slices.Equal(cfg.Roots, wantRoots) {
		t.Errorf("Roots = %v, want %v", cfg.Roots, wantRoots)
	}
}

func TestParseRunnerConfigValidation(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{
			name: "missing server",
			args: []string{"--token", "tok"},
		},
		{
			name: "invalid server scheme",
			args: []string{"--server", "ftp://example.com", "--token", "tok"},
		},
		{
			name: "missing token",
			args: []string{"--server", "http://127.0.0.1:8080"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseRunnerConfig(
				tc.args,
				func(string) string { return "" },
				func() (string, error) { return "", errors.New("no host") },
			)
			if err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
		})
	}
}
