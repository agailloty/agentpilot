package approval

import (
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func compileTestPatterns(t *testing.T, raw ...string) []*regexp.Regexp {
	t.Helper()
	out := make([]*regexp.Regexp, 0, len(raw))
	for _, p := range raw {
		re, err := regexp.Compile(p)
		if err != nil {
			t.Fatalf("regexp.Compile(%q) error = %v", p, err)
		}
		out = append(out, re)
	}
	return out
}

func TestDetectorStripsANSIAndFiresAfterIdle(t *testing.T) {
	patterns := compileTestPatterns(t, `(?i)allow.*\(y/n\)`, `(?i)\[y/n\]`)

	var (
		mu      sync.Mutex
		fired   []string
		firedCh = make(chan string, 4)
	)
	d := NewDetector(patterns, 40*time.Millisecond, func(pat string) {
		mu.Lock()
		fired = append(fired, pat)
		mu.Unlock()
		firedCh <- pat
	})
	defer d.Close()

	// Split ANSI CSI and OSC sequences across chunks to verify boundary handling.
	d.FeedOutput([]byte("\x1b]0;Copilot CLI\x07\x1b[33mAllow running `go test`"))
	d.FeedOutput([]byte("\x1b[0m \x1b[1;32m[y/N]\x1b[0m "))

	select {
	case pat := <-firedCh:
		if pat == "" {
			t.Fatal("expected non-empty matched pattern")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for approval callback")
	}

	if !d.WaitingApproval() {
		t.Fatal("expected WaitingApproval() = true after callback fired")
	}

	// Repeated matching/cursor chunks for the same prompt must not double-fire.
	d.FeedOutput([]byte("\x1b[?25h"))
	d.FeedOutput([]byte(" "))
	time.Sleep(80 * time.Millisecond)

	mu.Lock()
	count := len(fired)
	mu.Unlock()
	if count != 1 {
		t.Fatalf("fired count = %d, want 1", count)
	}
}

func TestDetectorCancelsOnContinuedNonMatchingOutput(t *testing.T) {
	patterns := compileTestPatterns(t, `(?i)\[y/n\]`)
	var count atomic.Int32

	d := NewDetector(patterns, 50*time.Millisecond, func(string) {
		count.Add(1)
	})
	defer d.Close()

	d.FeedOutput([]byte("Do you want to proceed? [y/n]\n"))
	time.Sleep(15 * time.Millisecond)
	// Stream enough non-matching lines to push past the prompt.
	d.FeedOutput([]byte("Compiling package one...\nCompiling package two...\nCompiling package three...\n"))

	time.Sleep(90 * time.Millisecond)
	if got := count.Load(); got != 0 {
		t.Fatalf("fired count = %d, want 0 after continued non-matching output", got)
	}
	if d.WaitingApproval() {
		t.Fatal("expected WaitingApproval() = false")
	}

	// Next prompt should still be able to notify.
	d.FeedOutput([]byte("Apply changes? [y/n] "))
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if count.Load() == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := count.Load(); got != 1 {
		t.Fatalf("fired count = %d, want 1 for subsequent prompt", got)
	}
}

func TestDetectorFeedInputCancelsAndRearms(t *testing.T) {
	patterns := compileTestPatterns(t, `(?i)\[y/n\]`, `(?i)do you want to`)
	firedCh := make(chan string, 4)

	d := NewDetector(patterns, 40*time.Millisecond, func(pat string) {
		firedCh <- pat
	})
	defer d.Close()

	// Cancel before idle timer fires.
	d.FeedOutput([]byte("Allow? [y/n] "))
	time.Sleep(15 * time.Millisecond)
	d.FeedInput()
	time.Sleep(70 * time.Millisecond)

	select {
	case pat := <-firedCh:
		t.Fatalf("unexpected notification after FeedInput: %q", pat)
	default:
	}

	// Second prompt fires, then FeedInput clears notified state so a third prompt fires again.
	d.FeedOutput([]byte("Do you want to run tests?"))
	select {
	case <-firedCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for second prompt notification")
	}
	if !d.WaitingApproval() {
		t.Fatal("expected WaitingApproval() = true")
	}

	d.FeedInput()
	if d.WaitingApproval() {
		t.Fatal("expected WaitingApproval() = false after FeedInput")
	}

	// Echoing user's "y\n" must not re-trigger the cleared prompt.
	d.FeedOutput([]byte("y\n"))
	time.Sleep(70 * time.Millisecond)
	select {
	case pat := <-firedCh:
		t.Fatalf("unexpected notification on echoed input: %q", pat)
	default:
	}

	d.FeedOutput([]byte("Proceed? [y/n] "))
	select {
	case <-firedCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for third prompt notification")
	}
}

func TestDetectorCloseStopsPendingTimerCleanly(t *testing.T) {
	patterns := compileTestPatterns(t, `(?i)\[y/n\]`)
	var count atomic.Int32

	d := NewDetector(patterns, 40*time.Millisecond, func(string) {
		count.Add(1)
	})

	d.FeedOutput([]byte("Confirm? [y/n] "))
	d.Close()
	d.Close() // Idempotent close.
	d.FeedOutput([]byte("Confirm? [y/n] "))
	d.FeedInput()

	time.Sleep(80 * time.Millisecond)
	if got := count.Load(); got != 0 {
		t.Fatalf("fired count = %d, want 0 after Close()", got)
	}
	if d.WaitingApproval() {
		t.Fatal("expected WaitingApproval() = false after Close()")
	}
}

func TestDetectorTailWindowBounded(t *testing.T) {
	patterns := compileTestPatterns(t, `(?i)\[y/n\]`)
	var count atomic.Int32

	d := NewDetector(patterns, 30*time.Millisecond, func(string) {
		count.Add(1)
	})
	defer d.Close()

	d.FeedOutput([]byte("Old prompt [y/n]\n"))
	d.FeedOutput([]byte(strings.Repeat("a", 2048)))

	time.Sleep(60 * time.Millisecond)
	if got := count.Load(); got != 0 {
		t.Fatalf("fired count = %d, want 0 after prompt evicted from rolling tail", got)
	}
}
