package runner

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestRunModelsSyncExecMode(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not in PATH")
	}
	res, err := RunModelsSync(context.Background(), ModelsSyncOptions{
		Command: "go",
		Args:    []string{"version"},
		Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("RunModelsSync() error = %v", err)
	}
	if res.Mode != "exec" || res.ExitCode != 0 {
		t.Fatalf("RunModelsSync() = %+v, want mode=exec exit=0", res)
	}
	if !strings.Contains(res.Output, "go version") {
		t.Fatalf("RunModelsSync() output = %q, want go version banner", res.Output)
	}
}

func TestRunModelsSyncErrors(t *testing.T) {
	if _, err := RunModelsSync(context.Background(), ModelsSyncOptions{}); err == nil {
		t.Fatalf("expected error for empty command")
	}
	if _, err := RunModelsSync(context.Background(), ModelsSyncOptions{Command: "go"}); err == nil {
		t.Fatalf("expected error when neither args nor send is set")
	}
	if _, err := RunModelsSync(context.Background(), ModelsSyncOptions{
		Command: "definitely-not-installed-cli-xyz",
		Args:    []string{"--version"},
	}); err == nil {
		t.Fatalf("expected error for not-installed CLI")
	}
}
