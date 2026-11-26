package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agailloty/agentpilot/server/internal/manifests"
)

func TestInspectHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_INSPECT_HELPER") != "1" {
		return
	}

	// Wait briefly for /help input, or print help immediately if mode=immediate.
	if os.Getenv("GO_INSPECT_MODE") != "immediate" {
		reader := bufio.NewReader(os.Stdin)
		done := make(chan string, 1)
		go func() {
			line, _ := reader.ReadString('\r')
			if line == "" {
				line, _ = reader.ReadString('\n')
			}
			done <- line
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	}

	fmt.Print("\x1b[1;36mAvailable commands:\x1b[0m\r\n")
	fmt.Print("  \x1b[32m/help\x1b[0m              Show available commands\r\n")
	fmt.Print("  /model [model]     Select or switch active model\r\n")
	fmt.Print("  /add-dir <path>  - Allow access to a directory\r\n")
	fmt.Print("  /new-cmd           Brand new discovered command\r\n")
	os.Exit(0)
}

func TestParseHelpOutput(t *testing.T) {
	raw := []byte(
		"\x1b[2J\x1b[H/help\r\n" +
			"\x1b[36mCommands:\x1b[0m\r\n" +
			"  \x1b[1m/help\x1b[0m              Show available commands\r\n" +
			"  /model [model]     Switch active model\r\n" +
			"  * /add-dir <path>  - Allow directory access\r\n" +
			"  /compact:          Compact conversation context\r\n" +
			"  Ignore path /usr/local/bin in prose\r\n" +
			"  /clear\r\n",
	)

	cmds := ParseHelpOutput(raw)
	if len(cmds) != 5 {
		t.Fatalf("len(ParseHelpOutput) = %d, want 5: %+v", len(cmds), cmds)
	}

	want := []manifests.Command{
		{Name: "/help", Description: "Show available commands"},
		{Name: "/model", Description: "Switch active model", Args: "[model]"},
		{Name: "/add-dir", Description: "Allow directory access", Args: "<path>"},
		{Name: "/compact", Description: "Compact conversation context"},
		{Name: "/clear", Description: ""},
	}

	for i := range want {
		if cmds[i] != want[i] {
			t.Errorf("cmds[%d] = %+v, want %+v", i, cmds[i], want[i])
		}
	}
}

func TestDiffManifestCommands(t *testing.T) {
	existing := []manifests.Command{
		{Name: "/help", Description: "Show help"},
		{Name: "/legacy", Description: "Removed command"},
		{Name: "/model", Description: "Select model", Args: "[model]"},
	}
	discovered := []manifests.Command{
		{Name: "/help", Description: "Show available commands"},
		{Name: "/model", Description: "Switch model", Args: "[model]"},
		{Name: "/usage", Description: "Show token usage"},
	}

	diff := DiffManifestCommands(existing, discovered)

	if len(diff.Added) != 1 || diff.Added[0].Name != "/usage" {
		t.Fatalf("diff.Added = %+v, want [/usage]", diff.Added)
	}
	if len(diff.Removed) != 1 || diff.Removed[0].Name != "/legacy" {
		t.Fatalf("diff.Removed = %+v, want [/legacy]", diff.Removed)
	}
	if len(diff.Unchanged) != 2 || diff.Unchanged[0].Name != "/help" || diff.Unchanged[1].Name != "/model" {
		t.Fatalf("diff.Unchanged = %+v, want [/help /model]", diff.Unchanged)
	}
}

func TestRunInspectWithDummyPTYAndManifestDiff(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}

	manifestPath := filepath.Join(t.TempDir(), "copilot.json")
	originalManifest := `{
  "agentId": "copilot",
  "name": "GitHub Copilot CLI",
  "launchCommand": "copilot",
  "commands": [
    {"name": "/help", "description": "Show help"},
    {"name": "/model", "description": "Switch model", "args": "[model]"},
    {"name": "/deprecated", "description": "Old command"}
  ],
  "approvalPatterns": []
}
`
	if err := os.WriteFile(manifestPath, []byte(originalManifest), 0o644); err != nil {
		t.Fatalf("os.WriteFile(manifest) error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := RunInspect(ctx, InspectOptions{
		AgentID:      "copilot",
		Command:      exe,
		Args:         []string{"-test.run=^TestInspectHelperProcess$"},
		Env:          map[string]string{"GO_WANT_INSPECT_HELPER": "1"},
		ManifestPath: manifestPath,
		Timeout:      6 * time.Second,
		InitWait:     100 * time.Millisecond,
		IdleWait:     300 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("RunInspect() error = %v", err)
	}

	if res.AgentID != "copilot" {
		t.Errorf("AgentID = %q, want copilot", res.AgentID)
	}
	if len(res.Discovered) != 4 {
		t.Fatalf("len(Discovered) = %d, want 4: %+v", len(res.Discovered), res.Discovered)
	}
	if res.Added == nil || res.Removed == nil || res.Unchanged == nil {
		t.Fatalf("expected non-nil diff slices in InspectResult: %+v", res)
	}
	if len(*res.Added) != 2 {
		t.Fatalf("Added = %+v, want 2 (/add-dir, /new-cmd)", *res.Added)
	}
	if len(*res.Removed) != 1 || (*res.Removed)[0].Name != "/deprecated" {
		t.Fatalf("Removed = %+v, want [/deprecated]", *res.Removed)
	}
	if len(*res.Unchanged) != 2 {
		t.Fatalf("Unchanged = %+v, want 2 (/help, /model)", *res.Unchanged)
	}

	// Verify JSON serialization contains expected fields.
	encoded, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("json.Marshal(res) error = %v", err)
	}
	for _, key := range []string{`"agentId"`, `"discovered"`, `"added"`, `"removed"`, `"unchanged"`} {
		if !strings.Contains(string(encoded), key) {
			t.Errorf("JSON output missing key %s: %s", key, string(encoded))
		}
	}

	// Verify manifest file on disk was never modified.
	afterBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("os.ReadFile(manifest) after inspect error = %v", err)
	}
	if !bytes.Equal(afterBytes, []byte(originalManifest)) {
		t.Fatal("RunInspect modified the manifest file on disk, expected read-only comparison")
	}
}
