package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := LoadFromEnv(func(string) (string, bool) {
		return "", false
	})
	if err != nil {
		t.Fatalf("LoadFromEnv() unexpected error: %v", err)
	}

	if cfg.ListenAddr != "127.0.0.1:8080" {
		t.Errorf("ListenAddr = %q, want %q", cfg.ListenAddr, "127.0.0.1:8080")
	}
	if cfg.DataDir != "./data" {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, "./data")
	}
	if cfg.ManifestsDir != "../docs/AgentManifests" {
		t.Errorf("ManifestsDir = %q, want %q", cfg.ManifestsDir, "../docs/AgentManifests")
	}
	if cfg.WebDir != "" {
		t.Errorf("WebDir = %q, want empty", cfg.WebDir)
	}
	if len(cfg.Tokens) != 0 {
		t.Errorf("len(Tokens) = %d, want 0", len(cfg.Tokens))
	}
	if cfg.NtfyURL != "" {
		t.Errorf("NtfyURL = %q, want empty", cfg.NtfyURL)
	}
	if cfg.NtfyTopic != "" {
		t.Errorf("NtfyTopic = %q, want empty", cfg.NtfyTopic)
	}
	if cfg.ApprovalIdleMS != 1500 {
		t.Errorf("ApprovalIdleMS = %d, want 1500", cfg.ApprovalIdleMS)
	}
	if cfg.ApprovalIdleDuration() != 1500*time.Millisecond {
		t.Errorf("ApprovalIdleDuration() = %v, want 1500ms", cfg.ApprovalIdleDuration())
	}
}

func TestLoadOverrides(t *testing.T) {
	env := map[string]string{
		"AGENTPILOT_LISTEN_ADDR":      "100.64.0.10:9090",
		"AGENTPILOT_DATA_DIR":         "/var/lib/agentpilot",
		"AGENTPILOT_MANIFESTS_DIR":    "/etc/agentpilot/manifests",
		"AGENTPILOT_WEB_DIR":          "/app/web",
		"AGENTPILOT_TOKENS":           "mobile-default:mobile:secret-m, runner:secret-r, bare-secret",
		"AGENTPILOT_NTFY_URL":         "https://ntfy.example.internal",
		"AGENTPILOT_NTFY_TOPIC":       "agentpilot-alerts",
		"AGENTPILOT_APPROVAL_IDLE_MS": "2500",
	}

	cfg, err := LoadFromEnv(func(k string) (string, bool) {
		v, ok := env[k]
		return v, ok
	})
	if err != nil {
		t.Fatalf("LoadFromEnv() unexpected error: %v", err)
	}

	if cfg.ListenAddr != "100.64.0.10:9090" {
		t.Errorf("ListenAddr = %q, want %q", cfg.ListenAddr, "100.64.0.10:9090")
	}
	if cfg.DataDir != "/var/lib/agentpilot" {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, "/var/lib/agentpilot")
	}
	if cfg.ManifestsDir != "/etc/agentpilot/manifests" {
		t.Errorf("ManifestsDir = %q, want %q", cfg.ManifestsDir, "/etc/agentpilot/manifests")
	}
	if cfg.WebDir != "/app/web" {
		t.Errorf("WebDir = %q, want %q", cfg.WebDir, "/app/web")
	}
	if cfg.NtfyURL != "https://ntfy.example.internal" {
		t.Errorf("NtfyURL = %q, want %q", cfg.NtfyURL, "https://ntfy.example.internal")
	}
	if cfg.NtfyTopic != "agentpilot-alerts" {
		t.Errorf("NtfyTopic = %q, want %q", cfg.NtfyTopic, "agentpilot-alerts")
	}
	if cfg.ApprovalIdleMS != 2500 {
		t.Errorf("ApprovalIdleMS = %d, want 2500", cfg.ApprovalIdleMS)
	}
	if len(cfg.Tokens) != 3 {
		t.Fatalf("len(Tokens) = %d, want 3", len(cfg.Tokens))
	}
	if cfg.Tokens[0].Name != "mobile-default" {
		t.Errorf("Tokens[0].Name = %q, want mobile-default", cfg.Tokens[0].Name)
	}

	if tok, ok := cfg.MatchToken("secret-m"); !ok || tok.Scope != ScopeMobile {
		t.Errorf("MatchToken(secret-m) = (%+v, %v), want scope=%q", tok, ok, ScopeMobile)
	}
	if tok, ok := cfg.MatchToken("secret-r"); !ok || tok.Scope != ScopeRunner {
		t.Errorf("MatchToken(secret-r) = (%+v, %v), want scope=%q", tok, ok, ScopeRunner)
	}
	if tok, ok := cfg.MatchToken("bare-secret"); !ok || tok.Scope != ScopeMobile {
		t.Errorf("MatchToken(bare-secret) = (%+v, %v), want scope=%q", tok, ok, ScopeMobile)
	}
	if _, ok := cfg.MatchToken("wrong"); ok {
		t.Error("MatchToken(wrong) = true, want false")
	}
	if _, ok := cfg.MatchToken(""); ok {
		t.Error("MatchToken(\"\") = true, want false")
	}
	if got := cfg.Tokens[0].String(); strings.Contains(got, "secret-m") {
		t.Errorf("Token.String() leaked secret: %q", got)
	}
}

func TestLoadValidationErrors(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
	}{
		{
			name: "non-integer approval idle",
			env:  map[string]string{"AGENTPILOT_APPROVAL_IDLE_MS": "abc"},
		},
		{
			name: "negative approval idle",
			env:  map[string]string{"AGENTPILOT_APPROVAL_IDLE_MS": "-1"},
		},
		{
			name: "invalid token scope",
			env:  map[string]string{"AGENTPILOT_TOKENS": "admin:secret"},
		},
		{
			name: "empty token value",
			env:  map[string]string{"AGENTPILOT_TOKENS": "mobile:"},
		},
		{
			name: "empty token name in named format",
			env:  map[string]string{"AGENTPILOT_TOKENS": ":mobile:secret"},
		},
		{
			name: "invalid scope in named format",
			env:  map[string]string{"AGENTPILOT_TOKENS": "myname:admin:secret"},
		},
		{
			name: "empty value in named format",
			env:  map[string]string{"AGENTPILOT_TOKENS": "myname:runner:"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadFromEnv(func(k string) (string, bool) {
				v, ok := tc.env[k]
				return v, ok
			})
			if err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
		})
	}
}
