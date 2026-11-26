package notify

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestNtfyNotifierNoOpWhenUnconfigured(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cases := []struct {
		name  string
		url   string
		topic string
	}{
		{name: "empty url", url: "", topic: "agentpilot"},
		{name: "empty topic", url: srv.URL, topic: ""},
		{name: "both empty", url: "", topic: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := NewNtfyNotifier(tc.url, tc.topic, srv.Client())
			if n.Enabled() {
				t.Fatal("expected Enabled() = false")
			}
			if err := n.NotifyApprovalNeeded(context.Background(), "Copilot", "proj", "s1"); err != nil {
				t.Fatalf("NotifyApprovalNeeded() error = %v", err)
			}
			if err := n.NotifySessionExit(context.Background(), "Copilot", "proj", "s1", 0); err != nil {
				t.Fatalf("NotifySessionExit() error = %v", err)
			}
		})
	}

	if hits.Load() != 0 {
		t.Fatalf("expected 0 HTTP requests for unconfigured notifier, got %d", hits.Load())
	}
}

func TestNtfyNotifierPublishesApprovalAndSessionExit(t *testing.T) {
	type capturedReq struct {
		method   string
		path     string
		title    string
		priority string
		tags     string
		body     string
	}
	var got []capturedReq
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		got = append(got, capturedReq{
			method:   r.Method,
			path:     r.URL.Path,
			title:    r.Header.Get("Title"),
			priority: r.Header.Get("Priority"),
			tags:     r.Header.Get("Tags"),
			body:     string(raw),
		})
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := NewNtfyNotifier(srv.URL+"/", "/agentpilot-alerts/", srv.Client())
	if !n.Enabled() {
		t.Fatal("expected Enabled() = true")
	}

	if err := n.NotifyApprovalNeeded(context.Background(), "Copilot", "my-repo", "sess-1"); err != nil {
		t.Fatalf("NotifyApprovalNeeded() error = %v", err)
	}
	if err := n.NotifySessionExit(context.Background(), "Copilot", "my-repo", "sess-1", 1); err != nil {
		t.Fatalf("NotifySessionExit() error = %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("captured %d requests, want 2", len(got))
	}

	approvalReq := got[0]
	if approvalReq.method != http.MethodPost || approvalReq.path != "/agentpilot-alerts" {
		t.Errorf("approval request = %s %s, want POST /agentpilot-alerts", approvalReq.method, approvalReq.path)
	}
	if approvalReq.title != "Copilot attend une confirmation" {
		t.Errorf("approval Title = %q, want %q", approvalReq.title, "Copilot attend une confirmation")
	}
	if approvalReq.priority != "high" {
		t.Errorf("approval Priority = %q, want high", approvalReq.priority)
	}
	if approvalReq.tags != "warning,robot" {
		t.Errorf("approval Tags = %q, want warning,robot", approvalReq.tags)
	}
	if !strings.Contains(approvalReq.body, "Copilot attend une confirmation") || !strings.Contains(approvalReq.body, "my-repo") {
		t.Errorf("approval body = %q, missing expected content", approvalReq.body)
	}

	exitReq := got[1]
	if exitReq.title != "Session Copilot terminée" {
		t.Errorf("exit Title = %q, want %q", exitReq.title, "Session Copilot terminée")
	}
	if exitReq.priority != "default" {
		t.Errorf("exit Priority = %q, want default", exitReq.priority)
	}
	if exitReq.tags != "x" {
		t.Errorf("exit Tags = %q, want x", exitReq.tags)
	}
	if !strings.Contains(exitReq.body, "Session Copilot terminée") || !strings.Contains(exitReq.body, "code 1") {
		t.Errorf("exit body = %q, missing expected content", exitReq.body)
	}
}

func TestNtfyNotifierReturnsErrorOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	n := NewNtfyNotifier(srv.URL, "agentpilot", srv.Client())
	err := n.NotifyApprovalNeeded(context.Background(), "Copilot", "proj", "s1")
	if err == nil || !strings.Contains(err.Error(), "HTTP 502") {
		t.Fatalf("expected HTTP 502 error, got %v", err)
	}
}
