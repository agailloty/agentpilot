package manifests

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/agailloty/agentpilot/server/internal/store"
)

func TestLoadDirInitialAgentManifests(t *testing.T) {
	manifestDir := filepath.Join("..", "..", "..", "docs", "AgentManifests")
	loaded, err := LoadDir(manifestDir)
	if err != nil {
		t.Fatalf("LoadDir(%q) error = %v", manifestDir, err)
	}

	wantAgents := map[string]struct {
		name               string
		launchCommand      string
		chatMode           string
		minCommands        int
		minPatterns        int
		minOptions         int
		minInspectActions  int
		wantHeadlessArgs   bool
		wantContinueArgs   bool
	}{
		"shell": {
			name:              "Interactive Shell",
			launchCommand:     "__shell__",
			chatMode:          ChatModeShell,
			minCommands:       4,
			minPatterns:       0,
			minOptions:        0,
			minInspectActions: 0,
		},
		"copilot": {
			name:              "GitHub Copilot CLI",
			launchCommand:     "copilot",
			chatMode:          ChatModeHeadless,
			minCommands:       10,
			minPatterns:       3,
			minOptions:        1,
			minInspectActions: 2,
			wantHeadlessArgs:  true,
			wantContinueArgs:  true,
		},
		"antigravity": {
			name:              "Antigravity CLI",
			launchCommand:     "agy",
			chatMode:          ChatModeHeadless,
			minCommands:       10,
			minPatterns:       3,
			minOptions:        1,
			minInspectActions: 2,
			wantHeadlessArgs:  true,
			wantContinueArgs:  true,
		},
		"mistral": {
			name:              "Mistral Vibe",
			launchCommand:     "vibe",
			chatMode:          ChatModeHeadless,
			minCommands:       8,
			minPatterns:       3,
			minOptions:        4,
			minInspectActions: 4,
			wantHeadlessArgs:  true,
			wantContinueArgs:  true,
		},
	}

	if len(loaded) != len(wantAgents) {
		t.Fatalf("loaded %d manifests, want %d: %+v", len(loaded), len(wantAgents), loaded)
	}

	idPattern := regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	byID := make(map[string]Manifest, len(loaded))
	for _, m := range loaded {
		byID[m.AgentID] = m
	}

	for id, want := range wantAgents {
		m, ok := byID[id]
		if !ok {
			t.Fatalf("manifest %q not found in %q", id, manifestDir)
		}
		if !idPattern.MatchString(m.AgentID) {
			t.Errorf("%s: agentId %q does not match schema pattern", id, m.AgentID)
		}
		if m.Name != want.name {
			t.Errorf("%s: Name = %q, want %q", id, m.Name, want.name)
		}
		if m.LaunchCommand != want.launchCommand {
			t.Errorf("%s: LaunchCommand = %q, want %q", id, m.LaunchCommand, want.launchCommand)
		}
		if m.ChatMode != want.chatMode {
			t.Errorf("%s: ChatMode = %q, want %q", id, m.ChatMode, want.chatMode)
		}
		if want.wantHeadlessArgs && len(m.HeadlessArgs) == 0 {
			t.Errorf("%s: expected non-empty HeadlessArgs", id)
		}
		if want.wantContinueArgs && len(m.ContinueArgs) == 0 {
			t.Errorf("%s: expected non-empty ContinueArgs", id)
		}
		if m.ProviderMenus == nil {
			t.Errorf("%s: expected non-nil ProviderMenus", id)
		} else {
			if len(m.ProviderMenus.Options) < want.minOptions {
				t.Errorf("%s: len(Options) = %d, want >= %d", id, len(m.ProviderMenus.Options), want.minOptions)
			}
			if len(m.ProviderMenus.InspectActions) < want.minInspectActions {
				t.Errorf("%s: len(InspectActions) = %d, want >= %d", id, len(m.ProviderMenus.InspectActions), want.minInspectActions)
			}
		}
		if len(m.Commands) < want.minCommands {
			t.Errorf("%s: len(Commands) = %d, want >= %d", id, len(m.Commands), want.minCommands)
		}
		for _, cmd := range m.Commands {
			if strings.TrimSpace(cmd.Name) == "" || strings.TrimSpace(cmd.Description) == "" {
				t.Errorf("%s: invalid command %+v", id, cmd)
			}
			if cmd.SendsRaw == "" {
				t.Errorf("%s: command %q should define sendsRaw", id, cmd.Name)
			}
		}
		if m.ApprovalPatterns == nil {
			t.Errorf("%s: expected ApprovalPatterns to be non-nil slice", id)
		}
		if len(m.ApprovalPatterns) < want.minPatterns {
			t.Errorf("%s: len(ApprovalPatterns) = %d, want >= %d", id, len(m.ApprovalPatterns), want.minPatterns)
		}
		if _, err := m.CompileApprovalPatterns(); err != nil {
			t.Errorf("%s: CompileApprovalPatterns() error = %v", id, err)
		}

		// Verify strict schema conformity (no unknown fields) on the raw JSON file.
		raw, err := os.ReadFile(filepath.Join(manifestDir, id+".json"))
		if err != nil {
			t.Fatalf("read %s.json: %v", id, err)
		}
		var strict struct {
			Schema string `json:"$schema"`
			Manifest
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&strict); err != nil {
			t.Errorf("%s.json failed strict schema decode: %v", id, err)
		}
	}
}

func TestLoadDirSkipsSchemaAndNonJSON(t *testing.T) {
	dir := t.TempDir()
	// Invalid JSON in schema.json should be skipped.
	if err := os.WriteFile(filepath.Join(dir, "schema.json"), []byte(`{"not":"a manifest"}`), 0o600); err != nil {
		t.Fatalf("write schema.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitkeep"), []byte(""), 0o600); err != nil {
		t.Fatalf("write .gitkeep: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "demo.json"), []byte(`{
		"agentId": "demo",
		"name": "Demo Agent",
		"launchCommand": "demo-cli",
		"args": ["--yes"],
		"commands": [{"name": "/help", "description": "Show help", "sendsRaw": "/help\n"}],
		"approvalPatterns": ["Allow\\? \\[y/N\\]"],
		"env": {"DEMO_MODE": "1"}
	}`), 0o600); err != nil {
		t.Fatalf("write demo.json: %v", err)
	}

	loaded, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir() error = %v", err)
	}
	if len(loaded) != 1 || loaded[0].AgentID != "demo" {
		t.Fatalf("loaded = %+v, want [demo]", loaded)
	}
	if loaded[0].Env["DEMO_MODE"] != "1" {
		t.Errorf("Env[DEMO_MODE] = %q, want 1", loaded[0].Env["DEMO_MODE"])
	}
}

func TestLoadDirValidationErrors(t *testing.T) {
	cases := []struct {
		name    string
		content string
		wantErr string
	}{
		{
			name:    "missing agentId",
			content: `{"name":"Test","launchCommand":"test"}`,
			wantErr: "agentId is required",
		},
		{
			name:    "missing name",
			content: `{"agentId":"test","launchCommand":"test"}`,
			wantErr: "name is required",
		},
		{
			name:    "missing launchCommand",
			content: `{"agentId":"test","name":"Test"}`,
			wantErr: "launchCommand is required",
		},
		{
			name:    "command missing name",
			content: `{"agentId":"test","name":"Test","launchCommand":"test","commands":[{"description":"desc"}]}`,
			wantErr: "command[0].name is required",
		},
		{
			name:    "command missing description",
			content: `{"agentId":"test","name":"Test","launchCommand":"test","commands":[{"name":"/help"}]}`,
			wantErr: "command[0].description is required",
		},
		{
			name:    "invalid approval regex",
			content: `{"agentId":"test","name":"Test","launchCommand":"test","approvalPatterns":["[unclosed"]}`,
			wantErr: "invalid approvalPatterns[0]",
		},
		{
			name:    "empty approval regex",
			content: `{"agentId":"test","name":"Test","launchCommand":"test","approvalPatterns":[""]}`,
			wantErr: "must not be empty",
		},
		{
			name:    "invalid chatMode",
			content: `{"agentId":"test","name":"Test","launchCommand":"test","chatMode":"pty"}`,
			wantErr: "invalid chatMode",
		},
		{
			name:    "provider option default not in choices",
			content: `{"agentId":"test","name":"Test","launchCommand":"test","providerMenus":{"options":[{"id":"m","label":"M","flag":"--m","defaultValue":"bad","choices":[{"value":"ok","label":"OK"}]}],"inspectActions":[]}}`,
			wantErr: "is not in choices",
		},
		{
			name:    "inspect action empty args",
			content: `{"agentId":"test","name":"Test","launchCommand":"test","providerMenus":{"options":[],"inspectActions":[{"id":"v","label":"Version","args":[]}]}}`,
			wantErr: "args must not be empty",
		},
		{
			name:    "malformed json",
			content: `{not json`,
			wantErr: "parse",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte(tc.content), 0o600); err != nil {
				t.Fatalf("WriteFile error = %v", err)
			}
			_, err := LoadDir(dir)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestLoadDirRejectsDuplicateAgentID(t *testing.T) {
	dir := t.TempDir()
	raw := `{"agentId":"dup","name":"Dup","launchCommand":"dup"}`
	if err := os.WriteFile(filepath.Join(dir, "a.json"), []byte(raw), 0o600); err != nil {
		t.Fatalf("write a.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.json"), []byte(raw), 0o600); err != nil {
		t.Fatalf("write b.json: %v", err)
	}

	_, err := LoadDir(dir)
	if err == nil || !strings.Contains(err.Error(), "duplicate agentId") {
		t.Fatalf("expected duplicate agentId error, got %v", err)
	}
}

func TestSyncToStore(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer func() { _ = s.Close() }()

	manifestDir := filepath.Join("..", "..", "..", "docs", "AgentManifests")
	loaded, err := LoadDir(manifestDir)
	if err != nil {
		t.Fatalf("LoadDir() error = %v", err)
	}

	ctx := context.Background()
	if err := SyncToStore(ctx, s, loaded); err != nil {
		t.Fatalf("SyncToStore() error = %v", err)
	}

	stored, err := s.GetManifest(ctx, "shell")
	if err != nil {
		t.Fatalf("GetManifest(shell) error = %v", err)
	}
	decoded, err := FromStoreManifest(stored)
	if err != nil {
		t.Fatalf("FromStoreManifest() error = %v", err)
	}
	if decoded.AgentID != "shell" || len(decoded.Commands) == 0 {
		t.Fatalf("unexpected decoded manifest: %+v", decoded)
	}
}

func TestManifestCandidates(t *testing.T) {
	m := Manifest{
		LaunchCommand:     "vibe",
		CommandCandidates: []string{"vibe", "", "  mistral  ", "vibe"},
	}
	got := m.Candidates()
	want := []string{"vibe", "mistral"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("Candidates() = %v, want %v", got, want)
	}
}
