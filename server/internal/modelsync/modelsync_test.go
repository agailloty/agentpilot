package modelsync

import (
	"reflect"
	"testing"

	"github.com/agailloty/agentpilot/server/internal/manifests"
)

func TestParseColumnsAgyModels(t *testing.T) {
	// Real `agy models` output uses a tab separator; TUI pickers use 2+ spaces.
	raw := "Fetching available models...\n" +
		"Available models for your account:\n\n" +
		"gemini-4-neon-xhigh\tGemini 4 Neon (XHigh)\n" +
		"gemini-4-neon-high\tGemini 4 Neon (High)\n" +
		"gemini-4-carbon-high           Gemini 4 Carbon (High)\n" +
		"gemini-4-hydrogen-low          Gemini 4 Hydrogen (Low)\n" +
		"gemini-3.8-flash-medium        Gemini 3.8 Flash (Medium)\n" +
		"gemini-pro-orchestrator        Orchestrator\n\n" +
		"Some prose line that must be ignored\n"
	got, err := Parse(ParserColumns, "", "", raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	want := []Model{
		{Value: "gemini-4-neon-xhigh", Label: "Gemini 4 Neon (XHigh)"},
		{Value: "gemini-4-neon-high", Label: "Gemini 4 Neon (High)"},
		{Value: "gemini-4-carbon-high", Label: "Gemini 4 Carbon (High)"},
		{Value: "gemini-4-hydrogen-low", Label: "Gemini 4 Hydrogen (Low)"},
		{Value: "gemini-3.8-flash-medium", Label: "Gemini 3.8 Flash (Medium)"},
		{Value: "gemini-pro-orchestrator", Label: "Orchestrator"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse() = %+v, want %+v", got, want)
	}
}

func TestParseHelpFlagCopilotStyle(t *testing.T) {
	raw := `Usage: copilot [options]

Options:
  --mode=MODE        Set the initial agent mode. interactive, plan, autopilot
  --model=MODEL      Set the AI model. Pass auto to let Copilot pick the best available model.
                     Model IDs such as claude-sonnet-5.5 (default), gpt-5.4, gpt-6.1-sol,
                     gpt-6-astra, claude-opus-5.5, gpt-5.3-codex, gemini-3.8-flash, or auto
  --mouse[=VALUE]    Enable or disable mouse support. on (default), off
`
	got, err := Parse(ParserHelpFlag, "--model", "", raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	want := []Model{
		{Value: "auto", Label: "auto"},
		{Value: "claude-sonnet-5.5", Label: "claude-sonnet-5.5"},
		{Value: "gpt-5.4", Label: "gpt-5.4"},
		{Value: "gpt-6.1-sol", Label: "gpt-6.1-sol"},
		{Value: "gpt-6-astra", Label: "gpt-6-astra"},
		{Value: "claude-opus-5.5", Label: "claude-opus-5.5"},
		{Value: "gpt-5.3-codex", Label: "gpt-5.3-codex"},
		{Value: "gemini-3.8-flash", Label: "gemini-3.8-flash"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse() = %+v, want %+v", got, want)
	}
}

func TestParseHelpFlagBackticked(t *testing.T) {
	raw := `  -m, --model <MODEL>  Model to use: ` + "`gpt-6.2-sol`" + `, ` + "`gpt-6.2-astra`" + `, or ` + "`auto`" + `
  --sandbox <MODE>     Sandbox mode
`
	got, err := Parse(ParserHelpFlag, "", "", raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	want := []Model{
		{Value: "gpt-6.2-sol", Label: "gpt-6.2-sol"},
		{Value: "gpt-6.2-astra", Label: "gpt-6.2-astra"},
		{Value: "auto", Label: "auto"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse() = %+v, want %+v", got, want)
	}
}

func TestParseJSON(t *testing.T) {
	raw := `{"models":[{"id":"gpt-6.2-sol","label":"GPT 6.2 Sol"},{"id":"gpt-5.3-codex","name":"GPT 5.3 Codex"},"plain-id"]}`
	got, err := Parse(ParserJSON, "", "", raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	want := []Model{
		{Value: "gpt-6.2-sol", Label: "GPT 6.2 Sol"},
		{Value: "gpt-5.3-codex", Label: "GPT 5.3 Codex"},
		{Value: "plain-id", Label: "plain-id"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse() = %+v, want %+v", got, want)
	}

	got2, err := Parse(ParserJSON, "", "", `["a","b","a"]`)
	if err != nil {
		t.Fatalf("Parse() array error = %v", err)
	}
	if len(got2) != 2 {
		t.Fatalf("Parse() array = %+v, want 2 entries (dedup)", got2)
	}
}

func TestParseLines(t *testing.T) {
	got, err := Parse(ParserLines, "", "", "opus\nsonnet\n\nhaiku\n")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(got) != 3 || got[0].Value != "opus" || got[2].Value != "haiku" {
		t.Fatalf("Parse() = %+v", got)
	}
}

func TestParseRegex(t *testing.T) {
	raw := "❯ opus     Claude Opus 5.5\n  sonnet   Claude Sonnet 5.5\nheader line\n"
	got, err := Parse(ParserRegex, "", `^\s*[❯>•\-]?\s*([a-z][a-z0-9\[\]]*)\s{2,}(.+)$`, raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	want := []Model{
		{Value: "opus", Label: "Claude Opus 5.5"},
		{Value: "sonnet", Label: "Claude Sonnet 5.5"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse() = %+v, want %+v", got, want)
	}

	if _, err := Parse(ParserRegex, "", "", raw); err == nil {
		t.Fatalf("Parse() expected error for empty pattern")
	}
	if _, err := Parse("nope", "", "", raw); err == nil {
		t.Fatalf("Parse() expected error for unknown parser")
	}
}

func TestApplyChoicesPreservesDefault(t *testing.T) {
	opt := &manifests.ProviderOption{
		ID:           "model",
		Flag:         "--model",
		DefaultValue: "default",
		Choices: []manifests.OptionChoice{
			{Value: "default", Label: "Default"},
			{Value: "old-model", Label: "Old"},
		},
	}
	models := []Model{
		{Value: "gemini-4-neon-high", Label: "Gemini 4 Neon (High)"},
		{Value: "gemini-4-carbon-low", Label: "Gemini 4 Carbon (Low)"},
		{Value: "gemini-4-neon-high", Label: "dup"},
	}
	choices, ok := ApplyChoices(opt, models)
	if !ok {
		t.Fatalf("ApplyChoices() ok = false, want true")
	}
	want := []manifests.OptionChoice{
		{Value: "default", Label: "Default"},
		{Value: "gemini-4-neon-high", Label: "Gemini 4 Neon (High)"},
		{Value: "gemini-4-carbon-low", Label: "Gemini 4 Carbon (Low)"},
	}
	if !reflect.DeepEqual(choices, want) {
		t.Fatalf("ApplyChoices() = %+v, want %+v", choices, want)
	}

	if _, ok := ApplyChoices(opt, nil); ok {
		t.Fatalf("ApplyChoices() with empty models ok = true, want false")
	}
}

func TestApplyChoicesKeepsDefaultValueWhenNotDiscovered(t *testing.T) {
	opt := &manifests.ProviderOption{
		ID:           "model",
		Flag:         "--model",
		DefaultValue: "auto",
		Choices: []manifests.OptionChoice{
			{Value: "auto", Label: "Auto"},
			{Value: "gpt-5.4", Label: "GPT 5.4"},
		},
	}
	choices, ok := ApplyChoices(opt, []Model{{Value: "gpt-6.1-sol", Label: "GPT 6.1 Sol"}})
	if !ok {
		t.Fatalf("ApplyChoices() ok = false")
	}
	if choices[0].Value != "auto" {
		t.Fatalf("ApplyChoices() choices[0] = %+v, want auto preserved first", choices[0])
	}
	if len(choices) != 2 {
		t.Fatalf("ApplyChoices() = %+v, want 2 entries", choices)
	}
}

func TestApplyToManifest(t *testing.T) {
	m := manifests.Manifest{
		AgentID: "antigravity",
		ModelSync: &manifests.ModelSyncSpec{
			OptionID: "model",
			Parser:   ParserColumns,
			Args:     []string{"models"},
		},
		ProviderMenus: &manifests.ProviderMenus{
			Options: []manifests.ProviderOption{{
				ID:           "model",
				Flag:         "--model",
				DefaultValue: "default",
				Choices:      []manifests.OptionChoice{{Value: "default", Label: "Default"}, {Value: "gemini-2.5-pro", Label: "Gemini 2.5 Pro"}},
			}},
		},
	}
	raw := "gemini-4-neon-high   Gemini 4 Neon (High)\ngemini-3.8-flash   Gemini 3.8 Flash\n"
	updated, models, err := ApplyToManifest(m, raw)
	if err != nil {
		t.Fatalf("ApplyToManifest() error = %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("models = %+v", models)
	}
	choices := updated.ProviderMenus.Options[0].Choices
	if len(choices) != 3 || choices[0].Value != "default" || choices[1].Value != "gemini-4-neon-high" {
		t.Fatalf("choices = %+v", choices)
	}

	if _, _, err := ApplyToManifest(m, "no models here at all\n"); err == nil {
		t.Fatalf("ApplyToManifest() expected error for unparseable output")
	}
	noSpec := manifests.Manifest{AgentID: "shell"}
	if _, _, err := ApplyToManifest(noSpec, "x"); err == nil {
		t.Fatalf("ApplyToManifest() expected error for missing modelSync")
	}
}
