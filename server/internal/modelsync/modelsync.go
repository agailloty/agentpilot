// Package modelsync discovers the live list of models exposed by an agent CLI
// (e.g. `agy models`, `copilot --help`) and applies it to a manifest provider
// option, so the models shown in the UI always match what the CLI itself offers.
package modelsync

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/agailloty/agentpilot/server/internal/manifests"
)

// Parser names usable in a manifest modelSync block (aliases of the
// manifests constants, kept as the single source of truth).
const (
	ParserColumns  = manifests.ModelSyncParserColumns  // two-column "slug   Label" lines (e.g. `agy models`)
	ParserHelpFlag = manifests.ModelSyncParserHelpFlag // model ids enumerated in a flag section of --help output
	ParserJSON     = manifests.ModelSyncParserJSON     // JSON array, or object with a "models" array
	ParserLines    = manifests.ModelSyncParserLines    // one model id per line
	ParserRegex    = manifests.ModelSyncParserRegex    // custom regexp per line (group 1 = value, group 2 optional = label)
)

// DefaultFlag is the CLI flag whose help text enumerates models for ParserHelpFlag.
const DefaultFlag = "--model"

// Model is one discovered model choice.
type Model struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Parsers lists the supported parser names.
func Parsers() []string {
	return manifests.ModelSyncParsers
}

// IsKnownParser reports whether name is a supported parser.
func IsKnownParser(name string) bool {
	return manifests.IsKnownModelSyncParser(name)
}

var (
	// reColumnLine matches "slug<TAB or 2+ spaces>Label" rows (agy models uses a
	// tab separator; TUI pickers usually align with several spaces).
	reColumnLine = regexp.MustCompile(`^(\S+)(?:\t+|\s{2,})(.+)$`)
	// reSlugLine matches a bare slug on its own line.
	reSlugLine = regexp.MustCompile(`^[a-z0-9][a-z0-9._:/-]*$`)
	// reModelToken matches model-id-like tokens inside help text: dashed ids
	// (gpt-5.4, claude-sonnet-5.5, gemini-3.8-flash) or the "auto" alias.
	reModelToken = regexp.MustCompile(`\b([a-z][a-z0-9]*(?:-[a-z0-9.]+)+|auto)\b`)
	// reBacktick matches backticked values in help text.
	reBacktick = regexp.MustCompile("`([^`]+)`")
)

// Parse extracts model choices from raw CLI output using the named parser.
// flag and pattern are only used by ParserHelpFlag and ParserRegex respectively.
func Parse(parser, flag, pattern, raw string) ([]Model, error) {
	switch parser {
	case ParserColumns:
		return parseColumns(raw), nil
	case ParserHelpFlag:
		return parseHelpFlag(raw, flag), nil
	case ParserJSON:
		return parseJSON(raw)
	case ParserLines:
		return parseLines(raw), nil
	case ParserRegex:
		return parseRegex(pattern, raw)
	case "":
		return nil, errors.New("modelsync: parser is required")
	default:
		return nil, fmt.Errorf("modelsync: unknown parser %q (known: %s)", parser, strings.Join(Parsers(), ", "))
	}
}

// parseColumns reads two-column "slug   Label" output (e.g. `agy models`).
func parseColumns(raw string) []Model {
	out := make([]Model, 0)
	seen := make(map[string]struct{})
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimRight(line, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		var value, label string
		if m := reColumnLine.FindStringSubmatch(trimmed); m != nil && reSlugLine.MatchString(m[1]) {
			value, label = m[1], strings.TrimSpace(m[2])
		} else if reSlugLine.MatchString(trimmed) {
			value, label = trimmed, trimmed
		} else {
			continue
		}
		if _, dup := seen[value]; dup {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, Model{Value: value, Label: label})
	}
	return out
}

// parseHelpFlag extracts model ids from the help section of a flag (default
// --model): the line(s) mentioning the flag up to the next flag definition.
// Backticked values win; otherwise dashed model-id tokens (and "auto") are
// collected, ignoring a trailing "(default)" marker.
func parseHelpFlag(raw, flag string) []Model {
	if strings.TrimSpace(flag) == "" {
		flag = DefaultFlag
	}
	lines := strings.Split(raw, "\n")
	section := make([]string, 0, 4)
	inSection := false
	for _, line := range lines {
		if strings.Contains(line, flag) {
			inSection = true
			section = append(section, line)
			continue
		}
		if inSection {
			// Stop at the next flag definition or a blank line.
			if strings.TrimSpace(line) == "" || (strings.Contains(line, "--") && !strings.Contains(line, flag)) {
				break
			}
			section = append(section, line)
		}
	}
	text := strings.Join(section, "\n")

	out := make([]Model, 0)
	seen := make(map[string]struct{})
	add := func(v string) {
		v = strings.TrimSpace(v)
		v = strings.TrimSuffix(v, " (default)")
		v = strings.Trim(v, "`.,;:")
		if v == "" {
			return
		}
		if _, dup := seen[v]; dup {
			return
		}
		seen[v] = struct{}{}
		out = append(out, Model{Value: v, Label: v})
	}

	if matches := reBacktick.FindAllStringSubmatch(text, -1); len(matches) > 0 {
		for _, m := range matches {
			add(m[1])
		}
		return out
	}
	for _, m := range reModelToken.FindAllStringSubmatch(text, -1) {
		add(m[1])
	}
	return out
}

// parseJSON accepts a JSON array of strings/objects, or an object with a
// "models" (or "data"/"items") array of such entries.
func parseJSON(raw string) ([]Model, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, errors.New("modelsync: empty JSON output")
	}
	var arr []any
	if err := json.Unmarshal([]byte(trimmed), &arr); err == nil {
		return modelsFromArray(arr)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &obj); err != nil {
		return nil, fmt.Errorf("modelsync: output is not JSON: %w", err)
	}
	for _, key := range []string{"models", "data", "items"} {
		if rawArr, ok := obj[key]; ok {
			var arr []any
			if err := json.Unmarshal(rawArr, &arr); err == nil {
				return modelsFromArray(arr)
			}
		}
	}
	return nil, errors.New("modelsync: JSON object has no models/data/items array")
}

func modelsFromArray(arr []any) ([]Model, error) {
	out := make([]Model, 0, len(arr))
	seen := make(map[string]struct{}, len(arr))
	for _, item := range arr {
		var value, label string
		switch v := item.(type) {
		case string:
			value = strings.TrimSpace(v)
			label = value
		case map[string]any:
			value = firstString(v, "id", "value", "slug", "name", "model")
			if value == "" {
				continue
			}
			label = firstString(v, "label", "name", "description", "displayName")
			if label == "" {
				label = value
			}
		default:
			continue
		}
		if value == "" {
			continue
		}
		if _, dup := seen[value]; dup {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, Model{Value: value, Label: label})
	}
	return out, nil
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// parseLines treats every non-empty line as a model id.
func parseLines(raw string) []Model {
	out := make([]Model, 0)
	seen := make(map[string]struct{})
	for _, line := range strings.Split(raw, "\n") {
		v := strings.TrimSpace(strings.TrimRight(line, "\r"))
		if v == "" {
			continue
		}
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, Model{Value: v, Label: v})
	}
	return out
}

// parseRegex applies pattern to every line; group 1 is the value, group 2 (if
// present) the label.
func parseRegex(pattern, raw string) ([]Model, error) {
	if strings.TrimSpace(pattern) == "" {
		return nil, errors.New("modelsync: pattern is required for parser \"regex\"")
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("modelsync: invalid pattern: %w", err)
	}
	if re.NumSubexp() < 1 {
		return nil, errors.New("modelsync: pattern must have at least one capture group")
	}
	out := make([]Model, 0)
	seen := make(map[string]struct{})
	for _, line := range strings.Split(raw, "\n") {
		m := re.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil {
			continue
		}
		value := strings.TrimSpace(m[1])
		if value == "" {
			continue
		}
		label := value
		if re.NumSubexp() >= 2 && strings.TrimSpace(m[2]) != "" {
			label = strings.TrimSpace(m[2])
		}
		if _, dup := seen[value]; dup {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, Model{Value: value, Label: label})
	}
	return out, nil
}

// ApplyChoices replaces the discovered choices of opt with models, preserving
// the leading "default" pseudo-choice (meaning "do not pass the flag") and the
// option's defaultValue. ok is false when models is empty (the caller should
// keep the existing choices).
func ApplyChoices(opt *manifests.ProviderOption, models []Model) (choices []manifests.OptionChoice, ok bool) {
	if opt == nil || len(models) == 0 {
		return nil, false
	}
	out := make([]manifests.OptionChoice, 0, len(models)+1)
	seen := make(map[string]struct{}, len(models)+1)

	// Preserve the "default" pseudo-choice first (means: no flag passed).
	for _, ch := range opt.Choices {
		if ch.Value == "default" {
			out = append(out, ch)
			seen[ch.Value] = struct{}{}
			break
		}
	}
	for _, m := range models {
		value := strings.TrimSpace(m.Value)
		if value == "" {
			continue
		}
		if _, dup := seen[value]; dup {
			continue
		}
		seen[value] = struct{}{}
		label := strings.TrimSpace(m.Label)
		if label == "" {
			label = value
		}
		out = append(out, manifests.OptionChoice{Value: value, Label: label})
	}
	// Keep the declared defaultValue selectable even if the CLI no longer lists it.
	if opt.DefaultValue != "" {
		if _, dup := seen[opt.DefaultValue]; !dup {
			out = append([]manifests.OptionChoice{{Value: opt.DefaultValue, Label: opt.DefaultValue}}, out...)
			seen[opt.DefaultValue] = struct{}{}
		}
	}
	return out, len(out) > 0
}

// ApplyToManifest parses raw CLI output with the manifest's modelSync spec and,
// when at least one model is found, replaces the target option's choices.
// It returns the updated manifest and the parsed models.
func ApplyToManifest(m manifests.Manifest, raw string) (manifests.Manifest, []Model, error) {
	if m.ModelSync == nil {
		return m, nil, errors.New("modelsync: manifest has no modelSync spec")
	}
	return ApplyWithSpec(*m.ModelSync, m, raw)
}

// ApplyWithSpec is ApplyToManifest with an explicit spec, allowing callers to
// override the manifest's parser/flag/pattern (e.g. CLI flags of the runner
// `models` subcommand take precedence over the manifest defaults).
func ApplyWithSpec(spec manifests.ModelSyncSpec, m manifests.Manifest, raw string) (manifests.Manifest, []Model, error) {
	models, err := Parse(spec.Parser, spec.Flag, spec.Pattern, raw)
	if err != nil {
		return m, nil, err
	}
	if len(models) == 0 {
		return m, nil, errors.New("modelsync: no models parsed from CLI output")
	}
	if m.ProviderMenus == nil {
		return m, nil, fmt.Errorf("modelsync: manifest %q has no providerMenus", m.AgentID)
	}
	found := false
	for i := range m.ProviderMenus.Options {
		if m.ProviderMenus.Options[i].ID == spec.OptionID {
			choices, ok := ApplyChoices(&m.ProviderMenus.Options[i], models)
			if !ok {
				return m, nil, errors.New("modelsync: no models parsed from CLI output")
			}
			m.ProviderMenus.Options[i].Choices = choices
			found = true
			break
		}
	}
	if !found {
		return m, nil, fmt.Errorf("modelsync: option %q not found in manifest %q", spec.OptionID, m.AgentID)
	}
	return m, models, nil
}
