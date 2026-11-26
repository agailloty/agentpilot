package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/agailloty/agentpilot/server/internal/protocol"
)

const (
	defaultPingInterval   = 20 * time.Second
	defaultPingTimeout    = 10 * time.Second
	defaultWriteTimeout   = 10 * time.Second
	defaultOutboxSize     = 256
	defaultMaxMessageSize = 4 << 20 // 4 MiB
)

var (
	// ErrRunnerNotFound is returned when targeting a machine with no active runner connection.
	ErrRunnerNotFound = errors.New("hub: runner not found")
	// ErrRunnerDisconnected is returned when the runner connection closes before a request completes.
	ErrRunnerDisconnected = errors.New("hub: runner disconnected")
	// ErrOutboxFull is returned when a runner's outbound queue cannot accept more messages.
	ErrOutboxFull = errors.New("hub: runner outbox full")
)

// RunnerInfo describes a currently connected runner machine.
type RunnerInfo struct {
	MachineName string    `json:"machineName"`
	OS          string    `json:"os"`
	Roots       []string  `json:"roots"`
	ConnectedAt time.Time `json:"connectedAt"`
}

// Hooks receives lifecycle and stream events from connected runners.
type Hooks struct {
	OnOutputChunk      func(sessionID string, data []byte)
	OnSessionExit      func(sessionID string, exitCode int)
	OnChatDelta        func(sessionID, messageID, delta string)
	OnChatDone         func(sessionID string, payload protocol.ChatDonePayload)
	OnRunnerConnect    func(machineName, os string, roots []string)
	OnRunnerDisconnect func(machineName string)
	OnSessionSync      func(machineName string, activeSessionIDs []string)
}

// Options configures Hub timeouts, limits, logging, and event hooks.
type Options struct {
	PingInterval   time.Duration
	PingTimeout    time.Duration
	WriteTimeout   time.Duration
	OutboxSize     int
	MaxMessageSize int64
	Logger         *slog.Logger
	Hooks          Hooks
}

func (o Options) withDefaults() Options {
	if o.PingInterval <= 0 {
		o.PingInterval = defaultPingInterval
	}
	if o.PingTimeout <= 0 {
		o.PingTimeout = defaultPingTimeout
	}
	if o.WriteTimeout <= 0 {
		o.WriteTimeout = defaultWriteTimeout
	}
	if o.OutboxSize <= 0 {
		o.OutboxSize = defaultOutboxSize
	}
	if o.MaxMessageSize <= 0 {
		o.MaxMessageSize = defaultMaxMessageSize
	}
	return o
}

type runnerConn struct {
	info   RunnerInfo
	conn   *websocket.Conn
	outbox chan protocol.Envelope
	done   chan struct{}
	cancel context.CancelFunc

	closeOnce sync.Once

	pendingMu sync.Mutex
	pending   map[string]chan protocol.Envelope
}

func (rc *runnerConn) closeDone() {
	rc.closeOnce.Do(func() {
		close(rc.done)
	})
}

func (rc *runnerConn) registerPending(correlationID string) chan protocol.Envelope {
	ch := make(chan protocol.Envelope, 1)
	rc.pendingMu.Lock()
	rc.pending[correlationID] = ch
	rc.pendingMu.Unlock()
	return ch
}

func (rc *runnerConn) removePending(correlationID string) {
	rc.pendingMu.Lock()
	delete(rc.pending, correlationID)
	rc.pendingMu.Unlock()
}

func (rc *runnerConn) resolvePending(correlationID string, env protocol.Envelope) bool {
	rc.pendingMu.Lock()
	ch, ok := rc.pending[correlationID]
	if ok {
		delete(rc.pending, correlationID)
	}
	rc.pendingMu.Unlock()
	if !ok {
		return false
	}
	select {
	case ch <- env:
	default:
	}
	return true
}

// Hub manages connected runners, correlated request/reply RPCs, and stream callbacks.
type Hub struct {
	opts   Options
	logger *slog.Logger

	mu      sync.RWMutex
	runners map[string]*runnerConn

	hooksMu sync.RWMutex
	hooks   Hooks

	seq atomic.Uint64
}

// New constructs a Hub with the provided Options.
func New(opts Options) *Hub {
	opts = opts.withDefaults()
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Hub{
		opts:    opts,
		logger:  logger,
		runners: make(map[string]*runnerConn),
		hooks:   opts.Hooks,
	}
}

// SetHooks replaces the hub event callbacks in a thread-safe manner.
func (h *Hub) SetHooks(hooks Hooks) {
	h.hooksMu.Lock()
	h.hooks = hooks
	h.hooksMu.Unlock()
}

func (h *Hub) currentHooks() Hooks {
	h.hooksMu.RLock()
	defer h.hooksMu.RUnlock()
	return h.hooks
}

// GetRunner returns metadata for a connected runner by machine name.
func (h *Hub) GetRunner(machineName string) (RunnerInfo, bool) {
	h.mu.RLock()
	rc, ok := h.runners[machineName]
	h.mu.RUnlock()
	if !ok {
		return RunnerInfo{}, false
	}
	return cloneRunnerInfo(rc.info), true
}

// ListRunners returns a sorted snapshot of all currently connected runners.
func (h *Hub) ListRunners() []RunnerInfo {
	h.mu.RLock()
	out := make([]RunnerInfo, 0, len(h.runners))
	for _, rc := range h.runners {
		out = append(out, cloneRunnerInfo(rc.info))
	}
	h.mu.RUnlock()

	sort.Slice(out, func(i, j int) bool {
		return out[i].MachineName < out[j].MachineName
	})
	return out
}

// DisconnectRunner closes the active WebSocket connection for machineName if connected.
func (h *Hub) DisconnectRunner(machineName string) bool {
	h.mu.RLock()
	rc, ok := h.runners[machineName]
	h.mu.RUnlock()
	if !ok {
		return false
	}
	rc.cancel()
	return true
}

// Close terminates all active runner WebSocket connections.
func (h *Hub) Close() {
	h.mu.RLock()
	conns := make([]*runnerConn, 0, len(h.runners))
	for _, rc := range h.runners {
		conns = append(conns, rc)
	}
	h.mu.RUnlock()

	for _, rc := range conns {
		rc.cancel()
	}
}

// HandleRunnerWS upgrades an authenticated runner connection and serves its message loops.
func (h *Hub) HandleRunnerWS(w http.ResponseWriter, r *http.Request) {
	machineName := strings.TrimSpace(r.Header.Get("X-Machine-Name"))
	if machineName == "" {
		http.Error(w, "missing X-Machine-Name header", http.StatusBadRequest)
		return
	}

	machineOS := strings.TrimSpace(r.Header.Get("X-Machine-OS"))
	roots, err := parseRootsHeader(r.Header.Get("X-Project-Roots"))
	if err != nil {
		http.Error(w, "invalid X-Project-Roots header", http.StatusBadRequest)
		return
	}

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		h.logger.Warn("runner websocket accept failed", "machine", machineName, "error", err)
		return
	}
	conn.SetReadLimit(h.opts.MaxMessageSize)

	connCtx, cancelConn := context.WithCancel(r.Context())
	rc := &runnerConn{
		info: RunnerInfo{
			MachineName: machineName,
			OS:          machineOS,
			Roots:       roots,
			ConnectedAt: time.Now().UTC(),
		},
		conn:    conn,
		outbox:  make(chan protocol.Envelope, h.opts.OutboxSize),
		done:    make(chan struct{}),
		cancel:  cancelConn,
		pending: make(map[string]chan protocol.Envelope),
	}

	var prev *runnerConn
	h.mu.Lock()
	prev = h.runners[machineName]
	h.runners[machineName] = rc
	h.mu.Unlock()

	if prev != nil {
		prev.cancel()
	}

	h.logger.Info("runner connected",
		"machine", machineName,
		"os", machineOS,
		"roots", roots,
	)
	if cb := h.currentHooks().OnRunnerConnect; cb != nil {
		cb(machineName, machineOS, append([]string(nil), roots...))
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		h.writeLoop(connCtx, rc)
	}()
	go func() {
		defer wg.Done()
		h.pingLoop(connCtx, rc)
	}()

	h.readLoop(connCtx, rc)

	cancelConn()
	rc.closeDone()
	wg.Wait()
	_ = conn.CloseNow()

	wasCurrent := false
	h.mu.Lock()
	if h.runners[machineName] == rc {
		delete(h.runners, machineName)
		wasCurrent = true
	}
	h.mu.Unlock()

	h.logger.Info("runner disconnected", "machine", machineName, "active_replaced", !wasCurrent)
	if wasCurrent {
		if cb := h.currentHooks().OnRunnerDisconnect; cb != nil {
			cb(machineName)
		}
	}
}

// SendRequest sends an envelope with a correlationId to the named runner and waits for its ack or error reply.
func (h *Hub) SendRequest(ctx context.Context, machineName string, env protocol.Envelope) (protocol.Envelope, error) {
	if env.Type == "" {
		return protocol.Envelope{}, protocol.ErrMissingType
	}

	h.mu.RLock()
	rc, ok := h.runners[machineName]
	h.mu.RUnlock()
	if !ok {
		return protocol.Envelope{}, fmt.Errorf("%w: %s", ErrRunnerNotFound, machineName)
	}

	if env.CorrelationID == "" {
		env.CorrelationID = h.nextCorrelationID()
	}

	replyCh := rc.registerPending(env.CorrelationID)
	defer rc.removePending(env.CorrelationID)

	select {
	case rc.outbox <- env:
	case <-rc.done:
		return protocol.Envelope{}, ErrRunnerDisconnected
	case <-ctx.Done():
		return protocol.Envelope{}, ctx.Err()
	}

	select {
	case reply := <-replyCh:
		return reply, nil
	case <-rc.done:
		return protocol.Envelope{}, ErrRunnerDisconnected
	case <-ctx.Done():
		return protocol.Envelope{}, ctx.Err()
	}
}

// SendFireAndForget enqueues an asynchronous message (such as input-write or session-resize) to a runner.
func (h *Hub) SendFireAndForget(machineName string, env protocol.Envelope) error {
	if env.Type == "" {
		return protocol.ErrMissingType
	}

	h.mu.RLock()
	rc, ok := h.runners[machineName]
	h.mu.RUnlock()
	if !ok {
		return fmt.Errorf("%w: %s", ErrRunnerNotFound, machineName)
	}

	select {
	case <-rc.done:
		return ErrRunnerDisconnected
	default:
	}

	select {
	case rc.outbox <- env:
		return nil
	case <-rc.done:
		return ErrRunnerDisconnected
	default:
		return ErrOutboxFull
	}
}

func (h *Hub) readLoop(ctx context.Context, rc *runnerConn) {
	for {
		_, data, err := rc.conn.Read(ctx)
		if err != nil {
			rc.cancel()
			return
		}

		env, err := protocol.UnmarshalEnvelope(data)
		if err != nil {
			h.logger.Warn("invalid envelope from runner",
				"machine", rc.info.MachineName,
				"error", err,
			)
			continue
		}
		h.dispatchInbound(rc, env)
	}
}

func (h *Hub) writeLoop(ctx context.Context, rc *runnerConn) {
	for {
		select {
		case <-ctx.Done():
			return
		case env := <-rc.outbox:
			data, err := env.Marshal()
			if err != nil {
				h.logger.Warn("failed to marshal outbound envelope",
					"machine", rc.info.MachineName,
					"type", env.Type,
					"error", err,
				)
				continue
			}
			wCtx, cancel := context.WithTimeout(ctx, h.opts.WriteTimeout)
			err = rc.conn.Write(wCtx, websocket.MessageText, data)
			cancel()
			if err != nil {
				rc.cancel()
				return
			}
		}
	}
}

func (h *Hub) pingLoop(ctx context.Context, rc *runnerConn) {
	ticker := time.NewTicker(h.opts.PingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pCtx, cancel := context.WithTimeout(ctx, h.opts.PingTimeout)
			err := rc.conn.Ping(pCtx)
			cancel()
			if err != nil {
				rc.cancel()
				return
			}
		}
	}
}

func (h *Hub) dispatchInbound(rc *runnerConn, env protocol.Envelope) {
	switch env.Type {
	case protocol.TypeAck, protocol.TypeError:
		if env.CorrelationID != "" {
			rc.resolvePending(env.CorrelationID, env)
		}
	case protocol.TypeOutputChunk:
		var payload protocol.DataPayload
		if err := env.DecodePayload(&payload); err != nil {
			h.logger.Warn("invalid output-chunk payload", "machine", rc.info.MachineName, "error", err)
			return
		}
		if cb := h.currentHooks().OnOutputChunk; cb != nil {
			cb(env.SessionID, payload.Data)
		}
	case protocol.TypeSessionExit:
		var payload protocol.SessionExitPayload
		if err := env.DecodePayload(&payload); err != nil {
			h.logger.Warn("invalid session-exit payload", "machine", rc.info.MachineName, "error", err)
			return
		}
		if cb := h.currentHooks().OnSessionExit; cb != nil {
			cb(env.SessionID, payload.ExitCode)
		}
	case protocol.TypeSessionSync:
		var payload protocol.SessionSyncPayload
		if err := env.DecodePayload(&payload); err != nil {
			h.logger.Warn("invalid session-sync payload", "machine", rc.info.MachineName, "error", err)
			return
		}
		if cb := h.currentHooks().OnSessionSync; cb != nil {
			cb(rc.info.MachineName, payload.ActiveSessionIDs)
		}
	case protocol.TypeChatDelta:
		var payload protocol.ChatDeltaPayload
		if err := env.DecodePayload(&payload); err != nil {
			h.logger.Warn("invalid chat-delta payload", "machine", rc.info.MachineName, "error", err)
			return
		}
		if cb := h.currentHooks().OnChatDelta; cb != nil {
			cb(env.SessionID, payload.MessageID, payload.Delta)
		}
	case protocol.TypeChatDone:
		var payload protocol.ChatDonePayload
		if err := env.DecodePayload(&payload); err != nil {
			h.logger.Warn("invalid chat-done payload", "machine", rc.info.MachineName, "error", err)
			return
		}
		if cb := h.currentHooks().OnChatDone; cb != nil {
			cb(env.SessionID, payload)
		}
	default:
		h.logger.Warn("unhandled message type from runner",
			"machine", rc.info.MachineName,
			"type", env.Type,
		)
	}
}

func (h *Hub) nextCorrelationID() string {
	n := h.seq.Add(1)
	return "req-" + strconv.FormatUint(n, 10)
}

func parseRootsHeader(raw string) ([]string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	var roots []string
	if err := json.Unmarshal([]byte(trimmed), &roots); err != nil {
		return nil, err
	}
	return roots, nil
}

func cloneRunnerInfo(info RunnerInfo) RunnerInfo {
	out := info
	if info.Roots != nil {
		out.Roots = append([]string(nil), info.Roots...)
	}
	return out
}
