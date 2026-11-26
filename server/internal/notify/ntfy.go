package notify

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultPublishTimeout = 5 * time.Second

// Notifier dispatches push notifications for session events.
type Notifier interface {
	Enabled() bool
	NotifyApprovalNeeded(ctx context.Context, agentName, projectName, sessionID string) error
	NotifySessionExit(ctx context.Context, agentName, projectName, sessionID string, exitCode int) error
}

// NtfyNotifier publishes push notifications to a self-hosted ntfy topic over HTTP.
type NtfyNotifier struct {
	baseURL string
	topic   string
	client  *http.Client
}

// NewNtfyNotifier creates an ntfy HTTP notifier. If baseURL or topic is empty, calls are no-ops.
func NewNtfyNotifier(baseURL, topic string, client *http.Client) *NtfyNotifier {
	if client == nil {
		client = &http.Client{Timeout: defaultPublishTimeout}
	}
	return &NtfyNotifier{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		topic:   strings.Trim(strings.TrimSpace(topic), "/"),
		client:  client,
	}
}

// Enabled reports whether both ntfy URL and topic are configured.
func (n *NtfyNotifier) Enabled() bool {
	return n != nil && n.baseURL != "" && n.topic != ""
}

// NotifyApprovalNeeded publishes an alert when an agent session is waiting for confirmation.
func (n *NtfyNotifier) NotifyApprovalNeeded(ctx context.Context, agentName, projectName, sessionID string) error {
	if !n.Enabled() {
		return nil
	}
	if strings.TrimSpace(agentName) == "" {
		agentName = "Agent"
	}
	title := fmt.Sprintf("%s attend une confirmation", agentName)
	body := title
	if projectName != "" {
		body = fmt.Sprintf("%s — projet %s", title, projectName)
	}
	if sessionID != "" {
		body = fmt.Sprintf("%s (session %s)", body, sessionID)
	}
	return n.publish(ctx, title, "high", "warning,robot", body)
}

// NotifySessionExit publishes a notification when an agent session terminates.
func (n *NtfyNotifier) NotifySessionExit(ctx context.Context, agentName, projectName, sessionID string, exitCode int) error {
	if !n.Enabled() {
		return nil
	}
	if strings.TrimSpace(agentName) == "" {
		agentName = "Agent"
	}
	title := fmt.Sprintf("Session %s terminée", agentName)
	body := fmt.Sprintf("%s (code %d)", title, exitCode)
	if projectName != "" {
		body = fmt.Sprintf("%s — projet %s", body, projectName)
	}
	if sessionID != "" {
		body = fmt.Sprintf("%s (session %s)", body, sessionID)
	}
	tags := "white_check_mark"
	if exitCode != 0 {
		tags = "x"
	}
	return n.publish(ctx, title, "default", tags, body)
}

func (n *NtfyNotifier) publish(ctx context.Context, title, priority, tags, body string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	reqCtx, cancel := context.WithTimeout(ctx, defaultPublishTimeout)
	defer cancel()

	endpoint := n.baseURL + "/" + url.PathEscape(n.topic)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("notify: build ntfy request: %w", err)
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	req.Header.Set("Title", title)
	req.Header.Set("Priority", priority)
	req.Header.Set("Tags", tags)

	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("notify: publish to ntfy: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("notify: ntfy returned HTTP %d", resp.StatusCode)
	}
	return nil
}
