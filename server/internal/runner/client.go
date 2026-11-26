package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/agailloty/agentpilot/server/internal/protocol"
	"github.com/agailloty/agentpilot/server/internal/pty"
)

const (
	defaultInitialBackoff = 250 * time.Millisecond
	defaultMaxBackoff     = 30 * time.Second
	defaultPingInterval   = 20 * time.Second
	defaultPingTimeout    = 10 * time.Second
	defaultWriteTimeout   = 10 * time.Second
	defaultOutboxSize     = 256
	defaultMaxMessageSize = 4 << 20   // 4 MiB
	defaultDisconnectBuf  = 256 << 10 // 256 KiB
	ptyReadBufSize        = 32 * 1024
)

// Config configures an outbound runner WebSocket client.
type Config struct {
	ServerURL            string
	Token                string
	MachineName          string
	Roots                []string
	Logger               *slog.Logger
	PTYManager           *pty.Manager
	InitialBackoff       time.Duration
	MaxBackoff           time.Duration
	PingInterval         time.Duration
	PingTimeout          time.Duration
	WriteTimeout         time.Duration
	OutboxSize           int
	MaxMessageSize       int64
	DisconnectBufferSize int
}

func (c Config) withDefaults() Config {
	if c.InitialBackoff <= 0 {
		c.InitialBackoff = defaultInitialBackoff
	}
	if c.MaxBackoff <= 0 {
		c.MaxBackoff = defaultMaxBackoff
	}
	if c.MaxBackoff < c.InitialBackoff {
		c.MaxBackoff = c.InitialBackoff
	}
	if c.PingInterval <= 0 {
		c.PingInterval = defaultPingInterval
	}
	if c.PingTimeout <= 0 {
		c.PingTimeout = defaultPingTimeout
	}
	if c.WriteTimeout <= 0 {
		c.WriteTimeout = defaultWriteTimeout
	}
	if c.OutboxSize <= 0 {
		c.OutboxSize = defaultOutboxSize
	}
	if c.MaxMessageSize <= 0 {
		c.MaxMessageSize = defaultMaxMessageSize
	}
	if c.DisconnectBufferSize <= 0 {
		c.DisconnectBufferSize = defaultDisconnectBuf
	}
	return c
}

type ringBuffer struct {
	cap  int
	data []byte
}

func newRingBuffer(capacity int) *ringBuffer {
	return &ringBuffer{cap: capacity}
}

func (b *ringBuffer) Write(p []byte) {
	if len(p) == 0 || b.cap <= 0 {
		return
	}
	if len(p) >= b.cap {
		b.data = append(b.data[:0], p[len(p)-b.cap:]...)
		return
	}
	excess := len(b.data) + len(p) - b.cap
	if excess > 0 {
		copy(b.data, b.data[excess:])
		b.data = b.data[:len(b.data)-excess]
	}
	b.data = append(b.data, p...)
}

func (b *ringBuffer) Drain() []byte {
	if len(b.data) == 0 {
		return nil
	}
	out := make([]byte, len(b.data))
	copy(out, b.data)
	b.data = b.data[:0]
	return out
}

type sessionStream struct {
	id     string
	buf    *ringBuffer
	exited *int
}

type wsConn struct {
	conn   *websocket.Conn
	outbox chan protocol.Envelope
	cancel context.CancelFunc
}

type activeChatTurn struct {
	seq    uint64
	cancel context.CancelFunc
}

// Client maintains an outbound WebSocket connection to the backend hub and
// executes PTY, chat, and filesystem commands locally.
type Client struct {
	cfg      Config
	endpoint string
	logger   *slog.Logger
	ptyMgr   *pty.Manager

	stateMu    sync.Mutex
	activeConn *wsConn
	streams    map[string]*sessionStream

	chatMu      sync.Mutex
	chatSeq     uint64
	chatCancels map[string]activeChatTurn
}

// NewClient constructs a runner Client after validating required connection settings.
func NewClient(cfg Config) (*Client, error) {
	cfg = cfg.withDefaults()
	if strings.TrimSpace(cfg.ServerURL) == "" {
		return nil, errors.New("runner: ServerURL is required")
	}
	if strings.TrimSpace(cfg.Token) == "" {
		return nil, errors.New("runner: Token is required")
	}
	if strings.TrimSpace(cfg.MachineName) == "" {
		return nil, errors.New("runner: MachineName is required")
	}

	endpoint, err := wsEndpoint(cfg.ServerURL)
	if err != nil {
		return nil, err
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	ptyMgr := cfg.PTYManager
	if ptyMgr == nil {
		ptyMgr = pty.NewManager()
	}

	return &Client{
		cfg:         cfg,
		endpoint:    endpoint,
		logger:      logger,
		ptyMgr:      ptyMgr,
		streams:     make(map[string]*sessionStream),
		chatCancels: make(map[string]activeChatTurn),
	}, nil
}

// PTYManager returns the runner's local PTY session manager.
func (c *Client) PTYManager() *pty.Manager {
	return c.ptyMgr
}

// IsConnected reports whether the client currently holds an active WebSocket connection.
func (c *Client) IsConnected() bool {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	return c.activeConn != nil
}

// Run connects to the backend and reconnects with exponential backoff until ctx is cancelled.
func (c *Client) Run(ctx context.Context) error {
	backoff := c.cfg.InitialBackoff

	for {
		if ctx.Err() != nil {
			return nil
		}

		dialCtx, cancelDial := context.WithTimeout(ctx, 15*time.Second)
		conn, _, err := websocket.Dial(dialCtx, c.endpoint, &websocket.DialOptions{
			HTTPHeader: c.dialHeaders(),
		})
		cancelDial()

		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			c.logger.Warn("runner websocket dial failed",
				"endpoint", c.endpoint,
				"backoff", backoff,
				"error", err,
			)
			if !sleepWithContext(ctx, backoff) {
				return nil
			}
			backoff = min(backoff*2, c.cfg.MaxBackoff)
			continue
		}

		backoff = c.cfg.InitialBackoff
		c.logger.Info("runner connected to server",
			"endpoint", c.endpoint,
			"machine", c.cfg.MachineName,
		)

		c.serveConn(ctx, conn)
		if ctx.Err() != nil {
			return nil
		}

		c.logger.Warn("runner connection lost; reconnecting", "backoff", backoff)
		if !sleepWithContext(ctx, backoff) {
			return nil
		}
		backoff = min(backoff*2, c.cfg.MaxBackoff)
	}
}

func (c *Client) dialHeaders() http.Header {
	roots := c.cfg.Roots
	if roots == nil {
		roots = []string{}
	}
	rootsJSON, _ := json.Marshal(roots)

	return http.Header{
		"Authorization":   []string{"Bearer " + c.cfg.Token},
		"X-Machine-Name":  []string{c.cfg.MachineName},
		"X-Machine-OS":    []string{runtime.GOOS},
		"X-Project-Roots": []string{string(rootsJSON)},
	}
}

func (c *Client) serveConn(parentCtx context.Context, conn *websocket.Conn) {
	conn.SetReadLimit(c.cfg.MaxMessageSize)

	connCtx, cancelConn := context.WithCancel(parentCtx)
	defer cancelConn()

	c.stateMu.Lock()
	outboxCap := c.cfg.OutboxSize
	if minCap := len(c.streams)*2 + 16; minCap > outboxCap {
		outboxCap = minCap
	}
	wc := &wsConn{
		conn:   conn,
		outbox: make(chan protocol.Envelope, outboxCap),
		cancel: cancelConn,
	}

	// Queue session-sync and any buffered disconnect output before exposing wc as activeConn,
	// so live PTY reads cannot overtake replayed chunks.
	activeIDs := c.ptyMgr.ActiveIDs()
	if syncEnv, err := protocol.NewEnvelope(
		protocol.TypeSessionSync,
		"",
		"",
		protocol.SessionSyncPayload{ActiveSessionIDs: activeIDs},
	); err == nil {
		wc.outbox <- syncEnv
	}

	streamIDs := make([]string, 0, len(c.streams))
	for id := range c.streams {
		streamIDs = append(streamIDs, id)
	}
	sort.Strings(streamIDs)

	for _, id := range streamIDs {
		st := c.streams[id]
		if buffered := st.buf.Drain(); len(buffered) > 0 {
			if chunkEnv, err := protocol.NewEnvelope(
				protocol.TypeOutputChunk,
				id,
				"",
				protocol.DataPayload{Data: buffered},
			); err == nil {
				wc.outbox <- chunkEnv
			}
		}
		if st.exited != nil {
			if exitEnv, err := protocol.NewEnvelope(
				protocol.TypeSessionExit,
				id,
				"",
				protocol.SessionExitPayload{ExitCode: *st.exited},
			); err == nil {
				wc.outbox <- exitEnv
			}
			delete(c.streams, id)
		}
	}

	c.activeConn = wc
	c.stateMu.Unlock()

	var (
		wg        sync.WaitGroup
		handlerWG sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		c.writeLoop(connCtx, wc)
	}()
	go func() {
		defer wg.Done()
		c.pingLoop(connCtx, wc)
	}()

	c.readLoop(connCtx, wc, &handlerWG)

	cancelConn()
	wg.Wait()
	handlerWG.Wait()
	_ = conn.CloseNow()

	c.deactivateConn(wc, nil)
}

func (c *Client) readLoop(ctx context.Context, wc *wsConn, handlerWG *sync.WaitGroup) {
	for {
		_, data, err := wc.conn.Read(ctx)
		if err != nil {
			wc.cancel()
			return
		}

		env, err := protocol.UnmarshalEnvelope(data)
		if err != nil {
			c.logger.Warn("invalid envelope from server", "error", err)
			continue
		}
		c.handleEnvelope(ctx, wc, env, handlerWG)
	}
}

func (c *Client) writeLoop(ctx context.Context, wc *wsConn) {
	for {
		select {
		case <-ctx.Done():
			return
		case env := <-wc.outbox:
			data, err := env.Marshal()
			if err != nil {
				c.logger.Warn("failed to marshal outbound envelope", "type", env.Type, "error", err)
				continue
			}
			wCtx, cancel := context.WithTimeout(ctx, c.cfg.WriteTimeout)
			err = wc.conn.Write(wCtx, websocket.MessageText, data)
			cancel()
			if err != nil {
				c.deactivateConn(wc, &env)
				wc.cancel()
				return
			}
		}
	}
}

func (c *Client) pingLoop(ctx context.Context, wc *wsConn) {
	ticker := time.NewTicker(c.cfg.PingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pCtx, cancel := context.WithTimeout(ctx, c.cfg.PingTimeout)
			err := wc.conn.Ping(pCtx)
			cancel()
			if err != nil {
				wc.cancel()
				return
			}
		}
	}
}

func (c *Client) deactivateConn(wc *wsConn, unsent *protocol.Envelope) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	if c.activeConn != wc {
		return
	}
	c.activeConn = nil

	if unsent != nil {
		c.rebufferLocked(*unsent)
	}
	for {
		select {
		case pending := <-wc.outbox:
			c.rebufferLocked(pending)
		default:
			return
		}
	}
}

func (c *Client) rebufferLocked(env protocol.Envelope) {
	if env.SessionID == "" {
		return
	}
	switch env.Type {
	case protocol.TypeOutputChunk:
		var p protocol.DataPayload
		if err := env.DecodePayload(&p); err != nil || len(p.Data) == 0 {
			return
		}
		st := c.ensureStreamLocked(env.SessionID)
		st.buf.Write(p.Data)
	case protocol.TypeSessionExit:
		var p protocol.SessionExitPayload
		if err := env.DecodePayload(&p); err != nil {
			return
		}
		st := c.ensureStreamLocked(env.SessionID)
		code := p.ExitCode
		st.exited = &code
	}
}

func (c *Client) ensureStreamLocked(sessionID string) *sessionStream {
	st, ok := c.streams[sessionID]
	if !ok {
		st = &sessionStream{
			id:  sessionID,
			buf: newRingBuffer(c.cfg.DisconnectBufferSize),
		}
		c.streams[sessionID] = st
	}
	return st
}

func (c *Client) registerStream(sessionID string) {
	c.stateMu.Lock()
	c.streams[sessionID] = &sessionStream{
		id:  sessionID,
		buf: newRingBuffer(c.cfg.DisconnectBufferSize),
	}
	c.stateMu.Unlock()
}

func (c *Client) emitOutputChunk(sessionID string, chunk []byte) {
	if len(chunk) == 0 {
		return
	}
	env, err := protocol.NewEnvelope(protocol.TypeOutputChunk, sessionID, "", protocol.DataPayload{Data: chunk})
	if err != nil {
		return
	}

	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	st := c.streams[sessionID]
	if c.activeConn != nil {
		select {
		case c.activeConn.outbox <- env:
			return
		default:
			// Outbox saturated; drop connection to trigger reconnect and preserve ordering in ring buffer.
			wc := c.activeConn
			c.activeConn = nil
			wc.cancel()
			for {
				select {
				case pending := <-wc.outbox:
					c.rebufferLocked(pending)
				default:
					goto bufferCurrent
				}
			}
		}
	}

bufferCurrent:
	if st != nil {
		st.buf.Write(chunk)
	}
}

func (c *Client) emitSessionExit(sessionID string, exitCode int) {
	env, err := protocol.NewEnvelope(protocol.TypeSessionExit, sessionID, "", protocol.SessionExitPayload{ExitCode: exitCode})
	if err != nil {
		return
	}

	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	if c.activeConn != nil {
		select {
		case c.activeConn.outbox <- env:
			delete(c.streams, sessionID)
			return
		default:
			wc := c.activeConn
			c.activeConn = nil
			wc.cancel()
			for {
				select {
				case pending := <-wc.outbox:
					c.rebufferLocked(pending)
				default:
					goto bufferExit
				}
			}
		}
	}

bufferExit:
	st := c.ensureStreamLocked(sessionID)
	code := exitCode
	st.exited = &code
}

func (c *Client) sendReply(wc *wsConn, env protocol.Envelope) {
	c.stateMu.Lock()
	active := c.activeConn == wc
	c.stateMu.Unlock()
	if !active {
		return
	}
	select {
	case wc.outbox <- env:
	default:
		c.logger.Warn("dropping reply due to full outbox", "type", env.Type, "correlation_id", env.CorrelationID)
	}
}

func (c *Client) emitChatEnvelope(env protocol.Envelope) {
	c.stateMu.Lock()
	wc := c.activeConn
	c.stateMu.Unlock()
	if wc == nil {
		return
	}
	select {
	case wc.outbox <- env:
	default:
		c.logger.Warn("dropping chat event due to full outbox", "type", env.Type, "session_id", env.SessionID)
	}
}

func (c *Client) registerChatTurn(sessionID string, cancel context.CancelFunc) uint64 {
	c.chatMu.Lock()
	prev, hadPrev := c.chatCancels[sessionID]
	c.chatSeq++
	seq := c.chatSeq
	c.chatCancels[sessionID] = activeChatTurn{seq: seq, cancel: cancel}
	c.chatMu.Unlock()

	if hadPrev && prev.cancel != nil {
		prev.cancel()
	}
	return seq
}

func (c *Client) unregisterChatTurn(sessionID string, seq uint64) {
	c.chatMu.Lock()
	if cur, ok := c.chatCancels[sessionID]; ok && cur.seq == seq {
		delete(c.chatCancels, sessionID)
	}
	c.chatMu.Unlock()
}

func (c *Client) cancelChatTurn(sessionID string) bool {
	c.chatMu.Lock()
	cur, ok := c.chatCancels[sessionID]
	if ok {
		delete(c.chatCancels, sessionID)
	}
	c.chatMu.Unlock()
	if ok && cur.cancel != nil {
		cur.cancel()
	}
	return ok
}

func wsEndpoint(serverURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(serverURL))
	if err != nil || parsed.Host == "" {
		return "", fmt.Errorf("runner: invalid server URL %q", serverURL)
	}
	switch parsed.Scheme {
	case "http":
		parsed.Scheme = "ws"
	case "https":
		parsed.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("runner: unsupported URL scheme %q", parsed.Scheme)
	}
	parsed.Path = path.Join("/", strings.TrimSuffix(parsed.Path, "/"), "ws", "runner")
	return parsed.String(), nil
}

func sleepWithContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
