package manifests

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/agailloty/agentpilot/server/internal/store"
)

// ChatMode values supported by agent manifests.
const (
	ChatModeHeadless = "headless"
	ChatModeShell    = "shell"
)

// Command describes a slash command or quick action exposed by an agent manifest.
type Command struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Args        string `json:"args,omitempty"`
	SendsRaw    string `json:"sendsRaw,omitempty"`
}

// OptionChoice represents one selectable value for a ProviderOption.
type OptionChoice struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// ProviderOption describes a configurable CLI flag exposed in the mobile provider menu.
type ProviderOption struct {
	ID           string         `json:"id"`
	Label        string         `json:"label"`
	Description  string         `json:"description,omitempty"`
	Flag         string         `json:"flag"`
	DefaultValue string         `json:"defaultValue,omitempty"`
	Choices      []OptionChoice `json:"choices"`
}

// InspectAction describes a pre-declared safe CLI inspection command in the provider menu.
type InspectAction struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Description string   `json:"description,omitempty"`
	Args        []string `json:"args"`
}

// ProviderMenus holds provider-specific session options and read-only inspect actions.
type ProviderMenus struct {
	Options        []ProviderOption `json:"options"`
	InspectActions []InspectAction  `json:"inspectActions"`
}

// Manifest defines an agent CLI capability catalog and launch configuration.
type Manifest struct {
	AgentID           string            `json:"agentId"`
	Name              string            `json:"name"`
	LaunchCommand     string            `json:"launchCommand"`
	CommandCandidates []string          `json:"commandCandidates,omitempty"`
	Args              []string          `json:"args,omitempty"`
	ChatMode          string            `json:"chatMode,omitempty"`
	HeadlessArgs      []string          `json:"headlessArgs,omitempty"`
	ContinueArgs      []string          `json:"continueArgs,omitempty"`
	ProviderMenus     *ProviderMenus    `json:"providerMenus,omitempty"`
	Commands          []Command         `json:"commands"`
	ApprovalPatterns  []string          `json:"approvalPatterns"`
	Env               map[string]string `json:"env,omitempty"`
}

// Candidates returns LaunchCommand followed by any distinct non-empty entries in CommandCandidates.
func (m Manifest) Candidates() []string {
	seen := make(map[string]struct{}, 1+len(m.CommandCandidates))
	out := make([]string, 0, 1+len(m.CommandCandidates))
	add := func(raw string) {
		cmd := strings.TrimSpace(raw)
		if cmd == "" {
			return
		}
		if _, exists := seen[cmd]; exists {
			return
		}
		seen[cmd] = struct{}{}
		out = append(out, cmd)
	}
	add(m.LaunchCommand)
	for _, c := range m.CommandCandidates {
		add(c)
	}
	return out
}

// DefaultOptions returns a map of option IDs to their default values from ProviderMenus.
func (m Manifest) DefaultOptions() map[string]string {
	out := make(map[string]string)
	if m.ProviderMenus == nil {
		return out
	}
	for _, opt := range m.ProviderMenus.Options {
		if opt.DefaultValue != "" {
			out[opt.ID] = opt.DefaultValue
		} else if len(opt.Choices) > 0 {
			out[opt.ID] = opt.Choices[0].Value
		}
	}
	return out
}

// FindInspectAction returns the declared InspectAction matching actionID.
func (m Manifest) FindInspectAction(actionID string) (InspectAction, bool) {
	if m.ProviderMenus == nil {
		return InspectAction{}, false
	}
	for _, act := range m.ProviderMenus.InspectActions {
		if act.ID == actionID {
			return act, true
		}
	}
	return InspectAction{}, false
}

// Validate checks required fields, command metadata, provider menus, and compiles all approval pattern regexes.
func (m *Manifest) Validate() error {
	if strings.TrimSpace(m.AgentID) == "" {
		return errors.New("manifests: agentId is required")
	}
	if strings.TrimSpace(m.Name) == "" {
		return errors.New("manifests: name is required")
	}
	if strings.TrimSpace(m.LaunchCommand) == "" {
		return errors.New("manifests: launchCommand is required")
	}

	mode := strings.TrimSpace(m.ChatMode)
	if mode != "" && mode != ChatModeHeadless && mode != ChatModeShell {
		return fmt.Errorf("manifests: invalid chatMode %q (want %q or %q)", mode, ChatModeHeadless, ChatModeShell)
	}

	if m.ProviderMenus != nil {
		seenOptIDs := make(map[string]struct{}, len(m.ProviderMenus.Options))
		for i, opt := range m.ProviderMenus.Options {
			if strings.TrimSpace(opt.ID) == "" {
				return fmt.Errorf("manifests: providerMenus.options[%d].id is required", i)
			}
			if _, dup := seenOptIDs[opt.ID]; dup {
				return fmt.Errorf("manifests: duplicate providerMenus option id %q", opt.ID)
			}
			seenOptIDs[opt.ID] = struct{}{}
			if strings.TrimSpace(opt.Label) == "" {
				return fmt.Errorf("manifests: providerMenus.options[%d].label is required", i)
			}
			if strings.TrimSpace(opt.Flag) == "" {
				return fmt.Errorf("manifests: providerMenus.options[%d].flag is required", i)
			}
			if len(opt.Choices) == 0 {
				return fmt.Errorf("manifests: providerMenus.options[%d].choices must not be empty", i)
			}
			choiceValues := make(map[string]struct{}, len(opt.Choices))
			for j, ch := range opt.Choices {
				if strings.TrimSpace(ch.Value) == "" {
					return fmt.Errorf("manifests: providerMenus.options[%d].choices[%d].value is required", i, j)
				}
				if strings.TrimSpace(ch.Label) == "" {
					return fmt.Errorf("manifests: providerMenus.options[%d].choices[%d].label is required", i, j)
				}
				choiceValues[ch.Value] = struct{}{}
			}
			if opt.DefaultValue != "" {
				if _, ok := choiceValues[opt.DefaultValue]; !ok {
					return fmt.Errorf("manifests: providerMenus.options[%d].defaultValue %q is not in choices", i, opt.DefaultValue)
				}
			}
		}

		seenActIDs := make(map[string]struct{}, len(m.ProviderMenus.InspectActions))
		for i, act := range m.ProviderMenus.InspectActions {
			if strings.TrimSpace(act.ID) == "" {
				return fmt.Errorf("manifests: providerMenus.inspectActions[%d].id is required", i)
			}
			if _, dup := seenActIDs[act.ID]; dup {
				return fmt.Errorf("manifests: duplicate providerMenus inspectAction id %q", act.ID)
			}
			seenActIDs[act.ID] = struct{}{}
			if strings.TrimSpace(act.Label) == "" {
				return fmt.Errorf("manifests: providerMenus.inspectActions[%d].label is required", i)
			}
			if len(act.Args) == 0 {
				return fmt.Errorf("manifests: providerMenus.inspectActions[%d].args must not be empty", i)
			}
		}
	}

	for i, cmd := range m.Commands {
		if strings.TrimSpace(cmd.Name) == "" {
			return fmt.Errorf("manifests: command[%d].name is required", i)
		}
		if strings.TrimSpace(cmd.Description) == "" {
			return fmt.Errorf("manifests: command[%d].description is required", i)
		}
	}

	if _, err := m.CompileApprovalPatterns(); err != nil {
		return err
	}

	m.normalize()
	return nil
}

// CompileApprovalPatterns compiles every regex string in ApprovalPatterns, failing fast on invalid syntax.
func (m Manifest) CompileApprovalPatterns() ([]*regexp.Regexp, error) {
	compiled := make([]*regexp.Regexp, 0, len(m.ApprovalPatterns))
	for i, pattern := range m.ApprovalPatterns {
		if strings.TrimSpace(pattern) == "" {
			return nil, fmt.Errorf("manifests: approvalPatterns[%d] must not be empty", i)
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("manifests: invalid approvalPatterns[%d] %q: %w", i, pattern, err)
		}
		compiled = append(compiled, re)
	}
	return compiled, nil
}

func (m *Manifest) normalize() {
	if strings.TrimSpace(m.ChatMode) == "" {
		m.ChatMode = ChatModeHeadless
	}
	if m.Commands == nil {
		m.Commands = []Command{}
	}
	if m.ApprovalPatterns == nil {
		m.ApprovalPatterns = []string{}
	}
	if m.ProviderMenus != nil {
		if m.ProviderMenus.Options == nil {
			m.ProviderMenus.Options = []ProviderOption{}
		}
		if m.ProviderMenus.InspectActions == nil {
			m.ProviderMenus.InspectActions = []InspectAction{}
		}
	}
}

// LoadDir reads and validates all *.json manifest files in dir (skipping schema.json).
func LoadDir(dir string) ([]Manifest, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("manifests: read dir %q: %w", dir, err)
	}

	seenIDs := make(map[string]string)
	out := make([]Manifest, 0, len(entries))

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "schema.json" || !strings.EqualFold(filepath.Ext(name), ".json") {
			continue
		}

		fullPath := filepath.Join(dir, name)
		raw, err := os.ReadFile(fullPath)
		if err != nil {
			return nil, fmt.Errorf("manifests: read %q: %w", fullPath, err)
		}

		var m Manifest
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("manifests: parse %q: %w", fullPath, err)
		}
		if err := m.Validate(); err != nil {
			return nil, fmt.Errorf("manifests: validate %q: %w", fullPath, err)
		}

		if prevFile, exists := seenIDs[m.AgentID]; exists {
			return nil, fmt.Errorf("manifests: duplicate agentId %q in %q and %q", m.AgentID, prevFile, name)
		}
		seenIDs[m.AgentID] = name
		out = append(out, m)
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].AgentID < out[j].AgentID
	})
	return out, nil
}

// SyncToStore upserts all provided manifests into the SQLite agent_manifests table.
func SyncToStore(ctx context.Context, s *store.Store, manifests []Manifest) error {
	for _, m := range manifests {
		if err := m.Validate(); err != nil {
			return err
		}
		raw, err := json.Marshal(m)
		if err != nil {
			return fmt.Errorf("manifests: marshal %q: %w", m.AgentID, err)
		}
		if _, err := s.UpsertManifest(ctx, store.AgentManifest{
			AgentID:       m.AgentID,
			Name:          m.Name,
			LaunchCommand: m.LaunchCommand,
			ManifestJSON:  string(raw),
		}); err != nil {
			return fmt.Errorf("manifests: sync %q: %w", m.AgentID, err)
		}
	}
	return nil
}

// FromStoreManifest decodes a store.AgentManifest record back into a Manifest.
func FromStoreManifest(sm store.AgentManifest) (Manifest, error) {
	var m Manifest
	if err := json.Unmarshal([]byte(sm.ManifestJSON), &m); err != nil {
		return Manifest{}, fmt.Errorf("manifests: decode stored manifest %q: %w", sm.AgentID, err)
	}
	m.normalize()
	return m, nil
}
