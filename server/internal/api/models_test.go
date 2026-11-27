package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/agailloty/agentpilot/server/internal/manifests"
)

// setupModelsTestEnv registers a fake agent whose CLI is the `go` binary and
// whose modelSync discovers "models" via `go version` (parser lines).
func setupModelsTestEnv(t *testing.T) *apiTestEnv {
	t.Helper()
	env := setupAPITestEnv(t)
	m := manifests.Manifest{
		AgentID:       "fakecli",
		Name:          "Fake CLI",
		LaunchCommand: "go",
		ChatMode:      manifests.ChatModeHeadless,
		HeadlessArgs:  []string{"-p", "{prompt}"},
		ProviderMenus: &manifests.ProviderMenus{
			Options: []manifests.ProviderOption{{
				ID:           "model",
				Label:        "Model",
				Flag:         "--model",
				DefaultValue: "default",
				Choices: []manifests.OptionChoice{
					{Value: "default", Label: "Default"},
					{Value: "stale-model", Label: "Stale"},
				},
			}},
		},
		ModelSync: &manifests.ModelSyncSpec{
			OptionID: "model",
			Args:     []string{"version"},
			Parser:   "lines",
		},
		Commands:         []manifests.Command{},
		ApprovalPatterns: []string{},
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("fake manifest invalid: %v", err)
	}
	if err := manifests.SyncToStore(context.Background(), env.store, []manifests.Manifest{m}); err != nil {
		t.Fatalf("SyncToStore(fakecli) error = %v", err)
	}
	return env
}

func waitMachineOnline(t *testing.T, env *apiTestEnv, name string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := env.store.GetMachineByName(context.Background(), name); err == nil {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("machine %q never came online", name)
}

func TestRefreshAgentModels(t *testing.T) {
	env := setupModelsTestEnv(t)
	rootDir := t.TempDir()
	env.connectRunner(t, "models-box", []string{rootDir})
	waitMachineOnline(t, env, "models-box")

	resp := env.doReq(t, http.MethodPost, "/api/agents/fakecli/models/refresh?machine=models-box", env.mobileTok, nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST refresh status = %d, want 200", resp.StatusCode)
	}
	var out modelsRefreshResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode refresh response: %v", err)
	}
	if out.AgentID != "fakecli" || out.OptionID != "model" || out.Mode != "exec" {
		t.Fatalf("unexpected refresh response: %+v", out)
	}
	if out.Count != 1 || len(out.Models) != 2 || out.Models[0].Value != "default" {
		t.Fatalf("unexpected models: %+v (count %d)", out.Models, out.Count)
	}
	if !strings.Contains(out.Models[1].Value, "go version") {
		t.Fatalf("discovered model = %+v, want go version line", out.Models[1])
	}

	// The stored manifest must have been updated (stale choice replaced).
	stored, err := env.store.GetManifest(context.Background(), "fakecli")
	if err != nil {
		t.Fatalf("GetManifest() error = %v", err)
	}
	updated, err := manifests.FromStoreManifest(stored)
	if err != nil {
		t.Fatalf("FromStoreManifest() error = %v", err)
	}
	opt := updated.FindOption("model")
	if opt == nil {
		t.Fatalf("model option missing after refresh")
	}
	values := make([]string, 0, len(opt.Choices))
	for _, c := range opt.Choices {
		values = append(values, c.Value)
	}
	if strings.Join(values, ",") != "default,"+out.Models[1].Value {
		t.Fatalf("stored choices = %v", values)
	}

	// GET /api/agents must expose the refreshed choices.
	resp2 := env.doReq(t, http.MethodGet, "/api/agents?machine=models-box", env.mobileTok, nil)
	defer func() { _ = resp2.Body.Close() }()
	var agents []agentResponse
	if err := json.NewDecoder(resp2.Body).Decode(&agents); err != nil {
		t.Fatalf("decode agents: %v", err)
	}
	for _, a := range agents {
		if a.AgentID == "fakecli" {
			if a.ProviderMenus == nil || len(a.ProviderMenus.Options) != 1 {
				t.Fatalf("fakecli providerMenus missing in GET /api/agents")
			}
			choices := a.ProviderMenus.Options[0].Choices
			if len(choices) != 2 || choices[0].Value != "default" || !strings.Contains(choices[1].Value, "go version") {
				t.Fatalf("GET /api/agents choices = %+v", choices)
			}
			return
		}
	}
	t.Fatalf("fakecli missing in GET /api/agents")
}

func TestRefreshAgentModelsErrors(t *testing.T) {
	env := setupModelsTestEnv(t)

	// Unknown agent -> 404
	resp := env.doReq(t, http.MethodPost, "/api/agents/nope/models/refresh", env.mobileTok, nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown agent status = %d, want 404", resp.StatusCode)
	}

	// Agent without modelSync (shell) -> 400
	resp = env.doReq(t, http.MethodPost, "/api/agents/shell/models/refresh", env.mobileTok, nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("shell status = %d, want 400", resp.StatusCode)
	}

	// No runner online -> 503
	resp = env.doReq(t, http.MethodPost, "/api/agents/fakecli/models/refresh", env.mobileTok, nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("no-runner status = %d, want 503", resp.StatusCode)
	}

	// Explicit offline machine -> 503
	rootDir := t.TempDir()
	env.connectRunner(t, "models-box", []string{rootDir})
	waitMachineOnline(t, env, "models-box")
	resp = env.doReq(t, http.MethodPost, "/api/agents/fakecli/models/refresh?machine=ghost-box", env.mobileTok, nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("offline machine status = %d, want 503", resp.StatusCode)
	}

	// Unauthenticated -> 401
	resp = env.doReq(t, http.MethodPost, "/api/agents/fakecli/models/refresh", "", nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no-token status = %d, want 401", resp.StatusCode)
	}
}

func TestBuildChatTurnArgsContinueHeadlessArgs(t *testing.T) {
	m := manifests.Manifest{
		AgentID:              "codex",
		ChatMode:             manifests.ChatModeHeadless,
		HeadlessArgs:         []string{"exec", "{prompt}", "--sandbox", "workspace-write"},
		ContinueHeadlessArgs: []string{"exec", "resume", "--last", "{prompt}"},
		ProviderMenus: &manifests.ProviderMenus{
			Options: []manifests.ProviderOption{
				{ID: "model", Flag: "--model", DefaultValue: "default", Choices: []manifests.OptionChoice{{Value: "default", Label: "Default"}}},
			},
		},
	}

	turn0 := buildChatTurnArgs(m, "/repo/app", "hello", 0, map[string]string{"model": "gpt-6.2-sol"})
	want0 := []string{"exec", "hello", "--sandbox", "workspace-write", "--model", "gpt-6.2-sol"}
	if strings.Join(turn0, "|") != strings.Join(want0, "|") {
		t.Fatalf("turn0 = %v, want %v", turn0, want0)
	}

	turn1 := buildChatTurnArgs(m, "/repo/app", "follow up", 1, map[string]string{"model": "default"})
	want1 := []string{"exec", "resume", "--last", "follow up"}
	if strings.Join(turn1, "|") != strings.Join(want1, "|") {
		t.Fatalf("turn1 = %v, want %v", turn1, want1)
	}
}
