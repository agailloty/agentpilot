package config

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultListenAddr     = "127.0.0.1:8080"
	DefaultDataDir        = "./data"
	DefaultManifestsDir   = "../docs/AgentManifests"
	DefaultApprovalIdleMS = 1500

	ScopeMobile = "mobile"
	ScopeRunner = "runner"
)

// Token represents a Bearer token, its optional name, and its authorization scope.
type Token struct {
	Name  string
	Scope string
	Value string
}

// String redacts the token secret so it is never leaked in logs.
func (t Token) String() string {
	if t.Name != "" {
		return t.Name + ":" + t.Scope + ":[REDACTED]"
	}
	return t.Scope + ":[REDACTED]"
}

// Config holds server configuration loaded from environment variables.
type Config struct {
	ListenAddr     string
	DataDir        string
	ManifestsDir   string
	WebDir         string
	Tokens         []Token
	NtfyURL        string
	NtfyTopic      string
	ApprovalIdleMS int
}

// Load reads configuration from process environment variables.
func Load() (Config, error) {
	return LoadFromEnv(os.LookupEnv)
}

// LoadFromEnv reads configuration using the provided lookup function.
func LoadFromEnv(lookup func(string) (string, bool)) (Config, error) {
	cfg := Config{
		ListenAddr:     DefaultListenAddr,
		DataDir:        DefaultDataDir,
		ManifestsDir:   DefaultManifestsDir,
		ApprovalIdleMS: DefaultApprovalIdleMS,
	}

	if v, ok := lookup("AGENTPILOT_LISTEN_ADDR"); ok && strings.TrimSpace(v) != "" {
		cfg.ListenAddr = strings.TrimSpace(v)
	}
	if v, ok := lookup("AGENTPILOT_DATA_DIR"); ok && strings.TrimSpace(v) != "" {
		cfg.DataDir = strings.TrimSpace(v)
	}
	if v, ok := lookup("AGENTPILOT_MANIFESTS_DIR"); ok && strings.TrimSpace(v) != "" {
		cfg.ManifestsDir = strings.TrimSpace(v)
	}
	if v, ok := lookup("AGENTPILOT_WEB_DIR"); ok {
		cfg.WebDir = strings.TrimSpace(v)
	}
	if v, ok := lookup("AGENTPILOT_NTFY_URL"); ok {
		cfg.NtfyURL = strings.TrimSpace(v)
	}
	if v, ok := lookup("AGENTPILOT_NTFY_TOPIC"); ok {
		cfg.NtfyTopic = strings.TrimSpace(v)
	}
	if v, ok := lookup("AGENTPILOT_APPROVAL_IDLE_MS"); ok && strings.TrimSpace(v) != "" {
		ms, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || ms < 0 {
			return Config{}, fmt.Errorf("invalid AGENTPILOT_APPROVAL_IDLE_MS: must be a non-negative integer")
		}
		cfg.ApprovalIdleMS = ms
	}
	if v, ok := lookup("AGENTPILOT_TOKENS"); ok && strings.TrimSpace(v) != "" {
		tokens, err := parseTokens(v)
		if err != nil {
			return Config{}, err
		}
		cfg.Tokens = tokens
	}

	return cfg, nil
}

// ApprovalIdleDuration returns ApprovalIdleMS as a time.Duration.
func (c Config) ApprovalIdleDuration() time.Duration {
	return time.Duration(c.ApprovalIdleMS) * time.Millisecond
}

// MatchToken validates a candidate Bearer token in constant time per entry.
func (c Config) MatchToken(candidate string) (Token, bool) {
	if candidate == "" {
		return Token{}, false
	}

	var matched Token
	found := false
	candBytes := []byte(candidate)
	for _, tok := range c.Tokens {
		if subtle.ConstantTimeCompare(candBytes, []byte(tok.Value)) == 1 {
			matched = tok
			found = true
		}
	}
	return matched, found
}

func parseTokens(raw string) ([]Token, error) {
	parts := strings.Split(raw, ",")
	tokens := make([]Token, 0, len(parts))
	for _, part := range parts {
		entry := strings.TrimSpace(part)
		if entry == "" {
			continue
		}

		fields := strings.SplitN(entry, ":", 3)
		switch len(fields) {
		case 1:
			tokens = append(tokens, Token{
				Scope: ScopeMobile,
				Value: entry,
			})
		case 2:
			scope := strings.TrimSpace(fields[0])
			value := strings.TrimSpace(fields[1])
			if scope != ScopeMobile && scope != ScopeRunner {
				return nil, fmt.Errorf("invalid token scope %q in AGENTPILOT_TOKENS: expected %q or %q", scope, ScopeMobile, ScopeRunner)
			}
			if value == "" {
				return nil, errors.New("empty token value in AGENTPILOT_TOKENS")
			}
			tokens = append(tokens, Token{
				Scope: scope,
				Value: value,
			})
		case 3:
			name := strings.TrimSpace(fields[0])
			scope := strings.TrimSpace(fields[1])
			value := strings.TrimSpace(fields[2])
			if name == "" {
				return nil, errors.New("empty token name in AGENTPILOT_TOKENS")
			}
			if scope != ScopeMobile && scope != ScopeRunner {
				return nil, fmt.Errorf("invalid token scope %q in AGENTPILOT_TOKENS: expected %q or %q", scope, ScopeMobile, ScopeRunner)
			}
			if value == "" {
				return nil, errors.New("empty token value in AGENTPILOT_TOKENS")
			}
			tokens = append(tokens, Token{
				Name:  name,
				Scope: scope,
				Value: value,
			})
		}
	}
	return tokens, nil
}
