package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/agailloty/agentpilot/server/internal/approval"
	"github.com/agailloty/agentpilot/server/internal/hub"
	"github.com/agailloty/agentpilot/server/internal/manifests"
	"github.com/agailloty/agentpilot/server/internal/protocol"
	"github.com/agailloty/agentpilot/server/internal/ringbuf"
	"github.com/agailloty/agentpilot/server/internal/store"
	"github.com/agailloty/agentpilot/server/internal/transcript"
)

const (
	defaultTermCols      = 80
	defaultTermRows      = 24
	mobileOutboxSize     = 256
	mobileWSReadLimit    = 1 << 20 // 1 MiB
	mobileWSWriteTimeout = 10 * time.Second
	snapshotWaitTimeout  = 2 * time.Second
	maxTranscriptLimit   = 4 << 20 // 4 MiB
)

type createSessionRequest struct {
	ProjectID string `json:"projectId"`
	AgentID   string `json:"agentId"`
	Cols      int    `json:"cols"`
	Rows      int    `json:"rows"`
}

type sessionResponse struct {
	ID              string            `json:"id"`
	ProjectID       string            `json:"projectId"`
	MachineName     string            `json:"machineName"`
	AgentID         string            `json:"agentId"`
	Cwd             string            `json:"cwd"`
	Status          string            `json:"status"`
	ExitCode        *int              `json:"exitCode,omitempty"`
	WaitingApproval bool              `json:"waitingApproval"`
	SnapshotPath    string            `json:"snapshotPath,omitempty"`
	SnapshotBytes   int64             `json:"snapshotBytes,omitempty"`
	Options         map[string]string `json:"options"`
	CreatedAt       time.Time         `json:"createdAt"`
	ClosedAt        *time.Time        `json:"closedAt,omitempty"`
}

type createMessageRequest struct {
	Content string `json:"content"`
}

type createMessageResponse struct {
	UserMessage      store.ChatMessage `json:"userMessage"`
	AssistantMessage store.ChatMessage `json:"assistantMessage"`
}

type updateOptionsRequest struct {
	Options map[string]string `json:"options"`
}

type menuActionRequest struct {
	ActionID string `json:"actionId"`
}

type menuActionResponse struct {
	ActionID   string `json:"actionId"`
	Label      string `json:"label"`
	Output     string `json:"output"`
	ExitCode   int    `json:"exitCode"`
	DurationMs int64  `json:"durationMs"`
}

type transcriptResponse struct {
	SessionID  string `json:"sessionId"`
	Offset     int64  `json:"offset"`
	NextOffset int64  `json:"nextOffset"`
	TotalBytes int64  `json:"totalBytes"`
	EOF        bool   `json:"eof"`
	Data       []byte `json:"data"`
}

type sessionSubscriber struct {
	outbox chan protocol.MobileFrame
	done   chan struct{}
	once   sync.Once
}

func (sub *sessionSubscriber) close() {
	sub.once.Do(func() {
		close(sub.done)
	})
}

type activeSession struct {
	id          string
	machineName string
	agentID     string
	agentName   string
	projectName string
	buf         *ringbuf.Buffer
	detector    *approval.Detector

	mu           sync.Mutex
	closed       bool
	exitCode     *int
	subscribers  map[*sessionSubscriber]struct{}
	snapshotDone chan struct{}
	snapshotOnce sync.Once
	snapshotPath string
	snapshotErr  error
}

func newActiveSession(id, machineName, agentID, agentName, projectName string) *activeSession {
	return &activeSession{
		id:           id,
		machineName:  machineName,
		agentID:      agentID,
		agentName:    agentName,
		projectName:  projectName,
		buf:          ringbuf.New(ringbuf.DefaultRingCapacity),
		subscribers:  make(map[*sessionSubscriber]struct{}),
		snapshotDone: make(chan struct{}),
	}
}

func (a *activeSession) appendOutput(data []byte) {
	if len(data) == 0 {
		return
	}
	frame := protocol.NewMobileOutputFrame(a.id, data)

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return
	}

	_, _ = a.buf.Write(data)
	a.broadcastLocked(frame)
}

func (a *activeSession) broadcastFrame(frame protocol.MobileFrame) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return
	}
	a.broadcastLocked(frame)
}

func (a *activeSession) broadcastLocked(frame protocol.MobileFrame) {
	for sub := range a.subscribers {
		select {
		case sub.outbox <- frame:
		default:
			sub.close()
			delete(a.subscribers, sub)
		}
	}
}

func (a *activeSession) closeDetector() {
	if a.detector != nil {
		a.detector.Close()
	}
}

func (a *activeSession) isWaitingApproval() bool {
	if a.detector == nil || a.isClosed() {
		return false
	}
	return a.detector.WaitingApproval()
}

func (a *activeSession) subscribe() (replay []byte, sub *sessionSubscriber, closed bool, exitCode *int) {
	a.mu.Lock()
	defer a.mu.Unlock()

	replay = a.buf.ReplayBytes()
	if a.closed {
		return replay, nil, true, a.exitCode
	}

	sub = &sessionSubscriber{
		outbox: make(chan protocol.MobileFrame, mobileOutboxSize),
		done:   make(chan struct{}),
	}
	a.subscribers[sub] = struct{}{}
	return replay, sub, false, nil
}

func (a *activeSession) unsubscribe(sub *sessionSubscriber) {
	if sub == nil {
		return
	}
	a.mu.Lock()
	delete(a.subscribers, sub)
	a.mu.Unlock()
	sub.close()
}

func (a *activeSession) isClosed() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.closed
}

func (a *activeSession) closeAndBroadcast(exitCode *int) (raw []byte, alreadyClosed bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.closed {
		return nil, true
	}
	a.closed = true
	if exitCode != nil {
		code := *exitCode
		a.exitCode = &code
	}

	raw = a.buf.FullBytes()
	codeVal := 0
	if a.exitCode != nil {
		codeVal = *a.exitCode
	}
	exitFrame := protocol.NewMobileSessionExitFrame(a.id, codeVal)
	for sub := range a.subscribers {
		select {
		case sub.outbox <- exitFrame:
		default:
		}
		sub.close()
		delete(a.subscribers, sub)
	}
	return raw, false
}

func (a *activeSession) markSnapshotDone(path string, err error) {
	a.mu.Lock()
	a.snapshotPath = path
	a.snapshotErr = err
	a.mu.Unlock()
	a.snapshotOnce.Do(func() {
		close(a.snapshotDone)
	})
}

func (a *activeSession) snapshotResult() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.snapshotPath, a.snapshotErr
}

// HandleOutputChunk buffers PTY output for an active session and fans it out to mobile WebSockets.
func (s *Server) HandleOutputChunk(sessionID string, data []byte) {
	activeSess := s.getActiveSession(sessionID)
	if activeSess == nil {
		return
	}
	activeSess.appendOutput(data)
	if activeSess.detector != nil && !activeSess.isClosed() {
		activeSess.detector.FeedOutput(data)
	}
}

// HandleSessionExit marks an active session closed, notifies mobile WebSockets, and snapshots the transcript.
func (s *Server) HandleSessionExit(sessionID string, exitCode int) {
	code := exitCode
	s.finalizeSession(context.Background(), sessionID, &code)
}

// HandleChatDelta forwards an incremental assistant output chunk to subscribed mobile WebSockets.
func (s *Server) HandleChatDelta(sessionID, messageID, delta string) {
	if delta == "" {
		return
	}
	activeSess := s.getActiveSession(sessionID)
	if activeSess == nil {
		return
	}
	activeSess.broadcastFrame(protocol.NewMobileChatDeltaFrame(sessionID, messageID, delta))
}

// HandleChatDone persists the final assistant message content/status and broadcasts chat-done.
func (s *Server) HandleChatDone(sessionID string, payload protocol.ChatDonePayload) {
	status := "done"
	if strings.TrimSpace(payload.Error) != "" {
		status = "error"
	}
	code := payload.ExitCode
	content := payload.Content
	if content == "" && payload.Error != "" {
		content = payload.Error
	}

	dbCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err := s.store.UpdateMessage(dbCtx, payload.MessageID, content, status, &code, payload.DurationMs)
	cancel()
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.logger.Warn("failed to update chat message on done", "session_id", sessionID, "message_id", payload.MessageID, "error", err)
	}

	if activeSess := s.getActiveSession(sessionID); activeSess != nil {
		donePayload := payload
		donePayload.Content = content
		activeSess.broadcastFrame(protocol.NewMobileChatDoneFrame(sessionID, donePayload, status))
	}
}

func (s *Server) ensureActiveSession(sess store.Session) *activeSession {
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()
	if existing, ok := s.sessions[sess.ID]; ok && !existing.isClosed() {
		return existing
	}
	activeSess := newActiveSession(sess.ID, sess.MachineName, sess.AgentID, sess.AgentID, "")
	s.sessions[sess.ID] = activeSess
	return activeSess
}

func (a *activeSession) closeChatAndBroadcast() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return true
	}
	a.closed = true
	closedFrame := protocol.NewMobileSessionClosedFrame(a.id)
	for sub := range a.subscribers {
		select {
		case sub.outbox <- closedFrame:
		default:
		}
		sub.close()
		delete(a.subscribers, sub)
	}
	return false
}

func (s *Server) registerActiveSession(id, machineName, agentID, agentName, projectName string, patterns []*regexp.Regexp) *activeSession {
	activeSess := newActiveSession(id, machineName, agentID, agentName, projectName)
	if len(patterns) > 0 {
		activeSess.detector = approval.NewDetector(patterns, s.approvalIdleDuration, func(pattern string) {
			s.handleApprovalDetected(id, agentID, agentName, projectName, pattern)
		})
	}
	s.sessionsMu.Lock()
	s.sessions[id] = activeSess
	s.sessionsMu.Unlock()
	return activeSess
}

func (s *Server) handleApprovalDetected(sessionID, agentID, agentName, projectName, pattern string) {
	activeSess := s.getActiveSession(sessionID)
	if activeSess == nil || activeSess.isClosed() {
		return
	}
	if strings.TrimSpace(agentName) == "" {
		agentName = agentID
	}
	message := fmt.Sprintf("%s attend une confirmation", agentName)
	activeSess.broadcastFrame(protocol.NewMobileApprovalNeededFrame(sessionID, agentID, pattern, message))

	if s.notifier != nil {
		go func() {
			notifyCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := s.notifier.NotifyApprovalNeeded(notifyCtx, agentName, projectName, sessionID); err != nil {
				s.logger.Warn("failed to send approval notification", "session_id", sessionID, "error", err)
			}
		}()
	}
}

func (s *Server) notifySessionExitAsync(agentName, agentID, projectName, sessionID string, exitCode int) {
	if s.notifier == nil {
		return
	}
	if strings.TrimSpace(agentName) == "" {
		agentName = agentID
	}
	go func() {
		notifyCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.notifier.NotifySessionExit(notifyCtx, agentName, projectName, sessionID, exitCode); err != nil {
			s.logger.Warn("failed to send session exit notification", "session_id", sessionID, "error", err)
		}
	}()
}

func (s *Server) getActiveSession(id string) *activeSession {
	s.sessionsMu.RLock()
	activeSess := s.sessions[id]
	s.sessionsMu.RUnlock()
	return activeSess
}

func (s *Server) removeActiveSession(id string, expected *activeSession) {
	s.sessionsMu.Lock()
	if cur, ok := s.sessions[id]; ok && cur == expected {
		delete(s.sessions, id)
	}
	s.sessionsMu.Unlock()
	if expected != nil {
		expected.closeDetector()
	}
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req createSessionRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	projectID := strings.TrimSpace(req.ProjectID)
	agentID := strings.TrimSpace(req.AgentID)
	if projectID == "" {
		writeError(w, http.StatusBadRequest, "projectId is required")
		return
	}
	if agentID == "" {
		writeError(w, http.StatusBadRequest, "agentId is required")
		return
	}

	proj, err := s.store.GetProject(r.Context(), projectID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		s.logger.Error("get project failed", "project_id", projectID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load project")
		return
	}

	storedManifest, err := s.store.GetManifest(r.Context(), agentID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "agent not found")
			return
		}
		s.logger.Error("get manifest failed", "agent_id", agentID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load agent")
		return
	}
	manifest, err := manifests.FromStoreManifest(storedManifest)
	if err != nil {
		s.logger.Error("decode manifest failed", "agent_id", agentID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to decode agent manifest")
		return
	}

	if s.hub == nil {
		writeError(w, http.StatusServiceUnavailable, "runner offline")
		return
	}
	if _, ok := s.hub.GetRunner(proj.MachineName); !ok {
		writeError(w, http.StatusServiceUnavailable, "runner offline")
		return
	}

	candidates := manifest.Candidates()
	toolMap, err := s.probeRunnerTools(r.Context(), proj.MachineName, candidates)
	if err != nil {
		if errors.Is(err, hub.ErrRunnerNotFound) || errors.Is(err, hub.ErrRunnerDisconnected) {
			writeError(w, http.StatusServiceUnavailable, "runner offline")
			return
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	_, installed := pickInstalledCandidate(candidates, toolMap)
	if !installed {
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("CLI tool %q is not installed on machine %q", manifest.LaunchCommand, proj.MachineName))
		return
	}

	sessionID, err := newSessionID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate session id")
		return
	}

	defaultOpts := manifest.DefaultOptions()
	created, err := s.store.CreateSession(r.Context(), store.Session{
		ID:          sessionID,
		ProjectID:   proj.ID,
		MachineName: proj.MachineName,
		AgentID:     manifest.AgentID,
		Cwd:         proj.Path,
		Status:      "running",
		Options:     defaultOpts,
	})
	if err != nil {
		s.logger.Error("persist session failed", "session_id", sessionID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to save session")
		return
	}

	s.registerActiveSession(sessionID, proj.MachineName, manifest.AgentID, manifest.Name, proj.Name, nil)
	writeJSON(w, http.StatusCreated, toSessionResponse(created))
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := store.SessionFilter{
		ProjectID:   strings.TrimSpace(q.Get("projectId")),
		MachineName: strings.TrimSpace(q.Get("machine")),
		Status:      strings.TrimSpace(q.Get("status")),
	}
	searchQuery := strings.TrimSpace(q.Get("q"))

	sessions, err := s.store.ListSessionsFiltered(r.Context(), filter)
	if err != nil {
		s.logger.Error("list sessions failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list sessions")
		return
	}

	out := make([]sessionResponse, 0, len(sessions))
	for _, sess := range sessions {
		activeSess := s.getActiveSession(sess.ID)
		if searchQuery != "" && !s.sessionMatchesQuery(r.Context(), sess, activeSess, searchQuery) {
			continue
		}
		resp := toSessionResponse(sess)
		if activeSess != nil {
			resp.WaitingApproval = activeSess.isWaitingApproval()
		}
		out = append(out, resp)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) sessionMatchesQuery(ctx context.Context, sess store.Session, activeSess *activeSession, query string) bool {
	if sess.MatchesMetadata(query) {
		return true
	}

	if matched, err := s.store.SessionMatchesMessageQuery(ctx, sess.ID, query); err == nil && matched {
		return true
	}

	var raw []byte
	if activeSess != nil {
		raw = activeSess.buf.FullBytes()
	} else if sess.SnapshotPath != "" {
		var err error
		raw, err = transcript.ReadAll(sess.SnapshotPath)
		if err != nil {
			s.logger.Warn("failed to read snapshot for session search", "session_id", sess.ID, "error", err)
			return false
		}
	}

	if len(raw) == 0 {
		return false
	}

	needle := strings.ToLower(strings.TrimSpace(query))
	if strings.Contains(strings.ToLower(string(raw)), needle) {
		return true
	}
	stripped := approval.StripANSI(raw)
	return strings.Contains(strings.ToLower(string(stripped)), needle)
}

func (s *Server) handleListSessionMessages(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "session id is required")
		return
	}
	if _, err := s.store.GetSession(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "session not found")
			return
		}
		s.logger.Error("get session failed", "session_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load session")
		return
	}
	messages, err := s.store.ListMessages(r.Context(), id)
	if err != nil {
		s.logger.Error("list session messages failed", "session_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list messages")
		return
	}
	if messages == nil {
		messages = []store.ChatMessage{}
	}
	writeJSON(w, http.StatusOK, messages)
}

func (s *Server) handleCreateSessionMessage(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "session id is required")
		return
	}

	var req createMessageRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	content := strings.TrimSpace(req.Content)
	if content == "" {
		writeError(w, http.StatusBadRequest, "content is required")
		return
	}

	sess, err := s.store.GetSession(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "session not found")
			return
		}
		s.logger.Error("get session failed", "session_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load session")
		return
	}
	if sess.Status != "running" {
		writeError(w, http.StatusConflict, "session is closed")
		return
	}

	if s.hub == nil {
		writeError(w, http.StatusServiceUnavailable, "runner offline")
		return
	}
	if _, ok := s.hub.GetRunner(sess.MachineName); !ok {
		writeError(w, http.StatusServiceUnavailable, "runner offline")
		return
	}

	storedManifest, err := s.store.GetManifest(r.Context(), sess.AgentID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "agent not found")
			return
		}
		s.logger.Error("get manifest failed", "agent_id", sess.AgentID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load agent")
		return
	}
	manifest, err := manifests.FromStoreManifest(storedManifest)
	if err != nil {
		s.logger.Error("decode manifest failed", "agent_id", sess.AgentID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to decode agent manifest")
		return
	}

	userMsg, err := s.store.CreateMessage(r.Context(), store.ChatMessage{
		SessionID: id,
		Role:      "user",
		Content:   content,
		Status:    "done",
	})
	if err != nil {
		s.logger.Error("persist user message failed", "session_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to save message")
		return
	}

	userCount, err := s.store.CountUserMessages(r.Context(), id)
	if err != nil {
		s.logger.Error("count user messages failed", "session_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to count messages")
		return
	}
	turnIndex := userCount - 1

	assistantMsg, err := s.store.CreateMessage(r.Context(), store.ChatMessage{
		SessionID: id,
		Role:      "assistant",
		Content:   "",
		Status:    "streaming",
	})
	if err != nil {
		s.logger.Error("persist assistant message failed", "session_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to save assistant message")
		return
	}

	activeSess := s.ensureActiveSession(sess)
	activeSess.broadcastFrame(protocol.NewMobileChatMessageFrame(id, toMobileChatMessage(userMsg)))
	activeSess.broadcastFrame(protocol.NewMobileChatMessageFrame(id, toMobileChatMessage(assistantMsg)))

	cliArgs := buildChatTurnArgs(manifest, sess.Cwd, content, turnIndex, sess.Options)
	env, err := protocol.NewEnvelope(protocol.TypeChatTurn, id, "", protocol.ChatTurnPayload{
		MessageID: assistantMsg.ID,
		Command:   manifest.LaunchCommand,
		Args:      cliArgs,
		Cwd:       sess.Cwd,
		Prompt:    content,
		ChatMode:  manifest.ChatMode,
		Env:       manifest.Env,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build chat-turn request")
		return
	}

	reqCtx, cancel := context.WithTimeout(r.Context(), runnerRequestTimeout)
	reply, err := s.hub.SendRequest(reqCtx, sess.MachineName, env)
	cancel()
	if err != nil {
		errMsg := err.Error()
		failCode := -1
		_ = s.store.UpdateMessage(context.Background(), assistantMsg.ID, errMsg, "error", &failCode, 0)
		activeSess.broadcastFrame(protocol.NewMobileChatDoneFrame(id, protocol.ChatDonePayload{
			MessageID: assistantMsg.ID,
			Content:   errMsg,
			ExitCode:  failCode,
			Error:     errMsg,
		}, "error"))
		if errors.Is(err, hub.ErrRunnerNotFound) || errors.Is(err, hub.ErrRunnerDisconnected) {
			writeError(w, http.StatusServiceUnavailable, "runner offline")
			return
		}
		writeError(w, http.StatusBadGateway, errMsg)
		return
	}
	if reply.Type == protocol.TypeError {
		var errPayload protocol.ErrorPayload
		_ = reply.DecodePayload(&errPayload)
		msg := errPayload.Message
		if msg == "" {
			msg = "runner failed to start chat turn"
		}
		failCode := -1
		_ = s.store.UpdateMessage(context.Background(), assistantMsg.ID, msg, "error", &failCode, 0)
		activeSess.broadcastFrame(protocol.NewMobileChatDoneFrame(id, protocol.ChatDonePayload{
			MessageID: assistantMsg.ID,
			Content:   msg,
			ExitCode:  failCode,
			Error:     msg,
		}, "error"))
		writeError(w, http.StatusBadGateway, msg)
		return
	}

	writeJSON(w, http.StatusAccepted, createMessageResponse{
		UserMessage:      userMsg,
		AssistantMessage: assistantMsg,
	})
}

func buildChatTurnArgs(manifest manifests.Manifest, cwd, prompt string, turnIndex int, options map[string]string) []string {
	if manifest.ChatMode == manifests.ChatModeShell {
		return nil
	}
	substitute := func(tmpl string) string {
		arg := strings.ReplaceAll(tmpl, "{cwd}", cwd)
		return strings.ReplaceAll(arg, "{prompt}", prompt)
	}
	args := make([]string, 0, len(manifest.HeadlessArgs)+len(manifest.ContinueArgs)+4)
	if turnIndex > 0 && len(manifest.ContinueHeadlessArgs) > 0 {
		// Full replacement template for continuation turns (e.g.
		// `codex exec resume --last "{prompt}"`).
		for _, tmpl := range manifest.ContinueHeadlessArgs {
			args = append(args, substitute(tmpl))
		}
	} else {
		for _, tmpl := range manifest.HeadlessArgs {
			args = append(args, substitute(tmpl))
		}
		if turnIndex > 0 && len(manifest.ContinueArgs) > 0 {
			args = append(args, manifest.ContinueArgs...)
		}
	}
	if manifest.ProviderMenus != nil {
		for _, opt := range manifest.ProviderMenus.Options {
			val := strings.TrimSpace(options[opt.ID])
			if val == "" || val == "default" || (opt.DefaultValue != "" && val == opt.DefaultValue) {
				continue
			}
			args = append(args, opt.Flag, val)
		}
	}
	return args
}

func (s *Server) handleCancelSessionTurn(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "session id is required")
		return
	}
	sess, err := s.store.GetSession(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "session not found")
			return
		}
		s.logger.Error("get session failed", "session_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load session")
		return
	}

	if s.hub != nil {
		if _, ok := s.hub.GetRunner(sess.MachineName); ok {
			cancelEnv, err := protocol.NewEnvelope(protocol.TypeChatCancel, id, "", protocol.ChatCancelPayload{})
			if err == nil {
				reqCtx, cancel := context.WithTimeout(r.Context(), runnerRequestTimeout)
				_, _ = s.hub.SendRequest(reqCtx, sess.MachineName, cancelEnv)
				cancel()
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]bool{"cancelled": true})
}

type sessionCommandResponse struct {
	SessionID     string            `json:"sessionId"`
	AgentID       string            `json:"agentId"`
	AgentName     string            `json:"agentName,omitempty"`
	ChatMode      string            `json:"chatMode"`
	LaunchCommand string            `json:"launchCommand"`
	FirstTurnArgs []string          `json:"firstTurnArgs,omitempty"`
	NextTurnArgs  []string          `json:"nextTurnArgs,omitempty"`
	Cwd           string            `json:"cwd"`
	MachineName   string            `json:"machineName"`
	Options       map[string]string `json:"options,omitempty"`
}

// handleGetSessionCommand exposes the exact CLI command the backend builds for
// headless chat turns, with a {prompt} placeholder, for mobile-side debugging.
// Environment variables from the manifest are deliberately omitted (no secrets).
func (s *Server) handleGetSessionCommand(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "session id is required")
		return
	}

	sess, err := s.store.GetSession(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "session not found")
			return
		}
		s.logger.Error("get session failed", "session_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load session")
		return
	}

	storedManifest, err := s.store.GetManifest(r.Context(), sess.AgentID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "agent not found")
			return
		}
		s.logger.Error("get manifest failed", "agent_id", sess.AgentID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load agent")
		return
	}
	manifest, err := manifests.FromStoreManifest(storedManifest)
	if err != nil {
		s.logger.Error("decode manifest failed", "agent_id", sess.AgentID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to decode agent manifest")
		return
	}

	firstTurnArgs := buildChatTurnArgs(manifest, sess.Cwd, "{prompt}", 0, sess.Options)
	nextTurnArgs := buildChatTurnArgs(manifest, sess.Cwd, "{prompt}", 1, sess.Options)

	writeJSON(w, http.StatusOK, sessionCommandResponse{
		SessionID:     sess.ID,
		AgentID:       sess.AgentID,
		AgentName:     manifest.Name,
		ChatMode:      manifest.ChatMode,
		LaunchCommand: manifest.LaunchCommand,
		FirstTurnArgs: firstTurnArgs,
		NextTurnArgs:  nextTurnArgs,
		Cwd:           sess.Cwd,
		MachineName:   sess.MachineName,
		Options:       sess.Options,
	})
}

func (s *Server) handleUpdateSessionOptions(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "session id is required")
		return
	}

	var req updateOptionsRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Options == nil {
		writeError(w, http.StatusBadRequest, "options object is required")
		return
	}

	sess, err := s.store.GetSession(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "session not found")
			return
		}
		s.logger.Error("get session failed", "session_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load session")
		return
	}

	storedManifest, err := s.store.GetManifest(r.Context(), sess.AgentID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "agent not found")
			return
		}
		s.logger.Error("get manifest failed", "agent_id", sess.AgentID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load agent")
		return
	}
	manifest, err := manifests.FromStoreManifest(storedManifest)
	if err != nil {
		s.logger.Error("decode manifest failed", "agent_id", sess.AgentID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to decode agent manifest")
		return
	}

	allowed := make(map[string]map[string]struct{})
	if manifest.ProviderMenus != nil {
		for _, opt := range manifest.ProviderMenus.Options {
			choices := make(map[string]struct{}, len(opt.Choices))
			for _, ch := range opt.Choices {
				choices[ch.Value] = struct{}{}
			}
			allowed[opt.ID] = choices
		}
	}

	merged := make(map[string]string, len(sess.Options)+len(req.Options))
	for k, v := range sess.Options {
		merged[k] = v
	}
	for k, v := range req.Options {
		choices, ok := allowed[k]
		if !ok {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown option %q for agent %q", k, manifest.AgentID))
			return
		}
		if _, valid := choices[v]; !valid {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid value %q for option %q", v, k))
			return
		}
		merged[k] = v
	}

	if err := s.store.UpdateSessionOptions(r.Context(), id, merged); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "session not found")
			return
		}
		s.logger.Error("update session options failed", "session_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update session options")
		return
	}

	updated, err := s.store.GetSession(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to reload session")
		return
	}
	writeJSON(w, http.StatusOK, toSessionResponse(updated))
}

func (s *Server) handleSessionMenuAction(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "session id is required")
		return
	}

	var req menuActionRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	actionID := strings.TrimSpace(req.ActionID)
	if actionID == "" {
		writeError(w, http.StatusBadRequest, "actionId is required")
		return
	}

	sess, err := s.store.GetSession(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "session not found")
			return
		}
		s.logger.Error("get session failed", "session_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load session")
		return
	}

	storedManifest, err := s.store.GetManifest(r.Context(), sess.AgentID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "agent not found")
			return
		}
		s.logger.Error("get manifest failed", "agent_id", sess.AgentID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load agent")
		return
	}
	manifest, err := manifests.FromStoreManifest(storedManifest)
	if err != nil {
		s.logger.Error("decode manifest failed", "agent_id", sess.AgentID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to decode agent manifest")
		return
	}

	action, ok := manifest.FindInspectAction(actionID)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("unknown menu action %q", actionID))
		return
	}

	if s.hub == nil {
		writeError(w, http.StatusServiceUnavailable, "runner offline")
		return
	}
	if _, online := s.hub.GetRunner(sess.MachineName); !online {
		writeError(w, http.StatusServiceUnavailable, "runner offline")
		return
	}

	env, err := protocol.NewEnvelope(protocol.TypeMenuInspect, id, "", protocol.MenuInspectPayload{
		Command:    manifest.LaunchCommand,
		Args:       action.Args,
		Cwd:        sess.Cwd,
		TimeoutSec: 15,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build menu-inspect request")
		return
	}

	reqCtx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	reply, err := s.hub.SendRequest(reqCtx, sess.MachineName, env)
	cancel()
	if err != nil {
		if errors.Is(err, hub.ErrRunnerNotFound) || errors.Is(err, hub.ErrRunnerDisconnected) {
			writeError(w, http.StatusServiceUnavailable, "runner offline")
			return
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if reply.Type == protocol.TypeError {
		var errPayload protocol.ErrorPayload
		_ = reply.DecodePayload(&errPayload)
		msg := errPayload.Message
		if msg == "" {
			msg = "runner menu-inspect failed"
		}
		writeError(w, http.StatusBadGateway, msg)
		return
	}

	var ack protocol.MenuInspectAckPayload
	if err := reply.DecodePayload(&ack); err != nil {
		writeError(w, http.StatusBadGateway, "invalid menu-inspect response from runner")
		return
	}

	writeJSON(w, http.StatusOK, menuActionResponse{
		ActionID:   action.ID,
		Label:      action.Label,
		Output:     ack.Output,
		ExitCode:   ack.ExitCode,
		DurationMs: ack.DurationMs,
	})
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "session id is required")
		return
	}

	sess, err := s.store.GetSession(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "session not found")
			return
		}
		s.logger.Error("get session failed", "session_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load session")
		return
	}

	if s.hub != nil {
		if _, ok := s.hub.GetRunner(sess.MachineName); ok {
			cancelEnv, err := protocol.NewEnvelope(protocol.TypeChatCancel, id, "", protocol.ChatCancelPayload{})
			if err == nil {
				reqCtx, cancel := context.WithTimeout(r.Context(), runnerRequestTimeout)
				_, _ = s.hub.SendRequest(reqCtx, sess.MachineName, cancelEnv)
				cancel()
			}
		}
	}

	if activeSess := s.getActiveSession(id); activeSess != nil {
		activeSess.closeChatAndBroadcast()
		s.removeActiveSession(id, activeSess)
	}

	if sess.Status != "closed" {
		if err := s.store.CloseSession(r.Context(), id, "closed", sess.ExitCode, sess.SnapshotPath, sess.SnapshotBytes, time.Now().UTC()); err != nil && !errors.Is(err, store.ErrNotFound) {
			s.logger.Error("close session in store failed", "session_id", id, "error", err)
			writeError(w, http.StatusInternalServerError, "failed to close session")
			return
		}
		codeVal := 0
		if sess.ExitCode != nil {
			codeVal = *sess.ExitCode
		}
		agentName := sess.AgentID
		if m, mErr := s.store.GetManifest(r.Context(), sess.AgentID); mErr == nil && strings.TrimSpace(m.Name) != "" {
			agentName = m.Name
		}
		projectName := ""
		if p, pErr := s.store.GetProject(r.Context(), sess.ProjectID); pErr == nil {
			projectName = p.Name
		}
		s.notifySessionExitAsync(agentName, sess.AgentID, projectName, id, codeVal)
	}

	updated, err := s.store.GetSession(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "session not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to reload session")
		return
	}

	writeJSON(w, http.StatusOK, toSessionResponse(updated))
}

func (s *Server) finalizeSession(ctx context.Context, sessionID string, exitCode *int) {
	activeSess := s.getActiveSession(sessionID)
	if activeSess == nil {
		return
	}

	activeSess.closeDetector()
	raw, alreadyClosed := activeSess.closeAndBroadcast(exitCode)
	if alreadyClosed {
		return
	}

	codeVal := 0
	if exitCode != nil {
		codeVal = *exitCode
	}
	s.notifySessionExitAsync(activeSess.agentName, activeSess.agentID, activeSess.projectName, sessionID, codeVal)

	closedAt := time.Now().UTC()
	if err := s.store.CloseSession(ctx, sessionID, "closed", exitCode, "", 0, closedAt); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.logger.Warn("failed to close session in store", "session_id", sessionID, "error", err)
	}

	transcript.WriteSnapshotAsync(context.Background(), s.dataDir, sessionID, raw, func(path string, n int64, err error) {
		if err != nil {
			s.logger.Error("failed to write session transcript snapshot", "session_id", sessionID, "error", err)
		} else {
			dbCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if dbErr := s.store.UpdateSessionSnapshot(dbCtx, sessionID, path, n); dbErr != nil && !errors.Is(dbErr, store.ErrNotFound) {
				s.logger.Warn("failed to update session snapshot metadata", "session_id", sessionID, "error", dbErr)
			}
			cancel()
		}
		activeSess.markSnapshotDone(path, err)
		s.removeActiveSession(sessionID, activeSess)
	})
}

func (s *Server) handleGetSessionTranscript(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "session id is required")
		return
	}

	var offset int64
	if rawOffset := strings.TrimSpace(r.URL.Query().Get("offset")); rawOffset != "" {
		v, err := strconv.ParseInt(rawOffset, 10, 64)
		if err != nil || v < 0 {
			writeError(w, http.StatusBadRequest, "invalid offset query parameter")
			return
		}
		offset = v
	}

	limit := transcript.DefaultPageLimit
	if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
		v, err := strconv.Atoi(rawLimit)
		if err != nil || v <= 0 {
			writeError(w, http.StatusBadRequest, "invalid limit query parameter")
			return
		}
		if v > maxTranscriptLimit {
			v = maxTranscriptLimit
		}
		limit = v
	}

	sess, err := s.store.GetSession(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "session not found")
			return
		}
		s.logger.Error("get session failed", "session_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load session")
		return
	}

	var (
		chunk      []byte
		nextOffset int64
		totalBytes int64
		eof        bool
	)

	activeSess := s.getActiveSession(id)
	if activeSess != nil {
		if activeSess.isClosed() {
			timer := time.NewTimer(snapshotWaitTimeout)
			select {
			case <-activeSess.snapshotDone:
				timer.Stop()
			case <-timer.C:
			case <-r.Context().Done():
				timer.Stop()
				return
			}

			snapPath, snapErr := activeSess.snapshotResult()
			if snapErr == nil && snapPath != "" {
				chunk, nextOffset, totalBytes, eof, err = transcript.ReadPaginated(snapPath, offset, limit)
			} else {
				chunk, nextOffset, totalBytes, eof, err = transcript.ReadPaginatedBytes(activeSess.buf.FullBytes(), offset, limit)
			}
		} else {
			chunk, nextOffset, totalBytes, eof, err = transcript.ReadPaginatedBytes(activeSess.buf.FullBytes(), offset, limit)
		}
	} else {
		if sess.SnapshotPath == "" {
			writeError(w, http.StatusNotFound, "transcript not available")
			return
		}
		chunk, nextOffset, totalBytes, eof, err = transcript.ReadPaginated(sess.SnapshotPath, offset, limit)
	}

	if err != nil {
		s.logger.Error("read session transcript failed", "session_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to read transcript")
		return
	}
	if chunk == nil {
		chunk = []byte{}
	}

	writeJSON(w, http.StatusOK, transcriptResponse{
		SessionID:  id,
		Offset:     offset,
		NextOffset: nextOffset,
		TotalBytes: totalBytes,
		EOF:        eof,
		Data:       chunk,
	})
}

func (s *Server) handleSessionWS(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "session id is required")
		return
	}

	sess, err := s.store.GetSession(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "session not found")
			return
		}
		s.logger.Error("get session for ws failed", "session_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load session")
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols:       []string{"agentpilot.v1"},
		InsecureSkipVerify: true,
	})
	if err != nil {
		s.logger.Warn("mobile session websocket accept failed", "session_id", id, "error", err)
		return
	}
	conn.SetReadLimit(mobileWSReadLimit)

	connCtx, cancelConn := context.WithCancel(r.Context())
	defer cancelConn()

	var (
		activeSess    *activeSession
		sub           *sessionSubscriber
		alreadyClosed bool
	)
	if sess.Status == "running" {
		activeSess = s.ensureActiveSession(sess)
		_, sub, alreadyClosed, _ = activeSess.subscribe()
	} else {
		alreadyClosed = true
	}

	storedMessages, err := s.store.ListMessages(r.Context(), id)
	if err != nil {
		s.logger.Warn("failed to load session messages for ws sync", "session_id", id, "error", err)
		storedMessages = nil
	}
	syncMessages := make([]protocol.MobileChatMessage, 0, len(storedMessages))
	for _, m := range storedMessages {
		syncMessages = append(syncMessages, toMobileChatMessage(m))
	}

	if err := writeMobileFrame(connCtx, conn, protocol.NewMobileChatSyncFrame(id, syncMessages)); err != nil {
		if activeSess != nil {
			activeSess.unsubscribe(sub)
		}
		_ = conn.CloseNow()
		return
	}

	if alreadyClosed {
		_ = writeMobileFrame(connCtx, conn, protocol.NewMobileSessionClosedFrame(id))
		_ = conn.Close(websocket.StatusNormalClosure, "session closed")
		return
	}
	defer activeSess.unsubscribe(sub)

	var writeWG sync.WaitGroup
	writeWG.Add(1)
	go func() {
		defer writeWG.Done()
		defer cancelConn()
		s.mobileWSWriteLoop(connCtx, conn, sub)
	}()

	s.mobileWSReadLoop(connCtx, conn, sess.MachineName, id)
	cancelConn()
	writeWG.Wait()
	_ = conn.CloseNow()
}

func (s *Server) mobileWSWriteLoop(ctx context.Context, conn *websocket.Conn, sub *sessionSubscriber) {
	for {
		select {
		case frame := <-sub.outbox:
			if err := writeMobileFrame(ctx, conn, frame); err != nil {
				return
			}
		case <-sub.done:
			for {
				select {
				case frame := <-sub.outbox:
					if err := writeMobileFrame(ctx, conn, frame); err != nil {
						return
					}
				default:
					_ = conn.Close(websocket.StatusNormalClosure, "session closed")
					return
				}
			}
		case <-ctx.Done():
			return
		}
	}
}

func (s *Server) mobileWSReadLoop(ctx context.Context, conn *websocket.Conn, machineName, sessionID string) {
	for {
		_, raw, err := conn.Read(ctx)
		if err != nil {
			return
		}

		frame, err := protocol.UnmarshalMobileFrame(raw)
		if err != nil {
			s.logger.Warn("invalid mobile ws frame", "session_id", sessionID, "error", err)
			continue
		}

		switch frame.Type {
		case protocol.MobileTypeInput:
			if activeSess := s.getActiveSession(sessionID); activeSess != nil && activeSess.detector != nil {
				activeSess.detector.FeedInput()
			}
			if s.hub == nil {
				continue
			}
			env, err := protocol.NewEnvelope(protocol.TypeInputWrite, sessionID, "", protocol.DataPayload{Data: frame.Data})
			if err != nil {
				continue
			}
			if err := s.hub.SendFireAndForget(machineName, env); err != nil {
				s.logger.Warn("forward input-write failed", "session_id", sessionID, "machine", machineName, "error", err)
			}
		case protocol.MobileTypeResize:
			if s.hub == nil || frame.Cols <= 0 || frame.Rows <= 0 {
				continue
			}
			env, err := protocol.NewEnvelope(protocol.TypeSessionResize, sessionID, "", protocol.ResizePayload{
				Cols: frame.Cols,
				Rows: frame.Rows,
			})
			if err != nil {
				continue
			}
			if err := s.hub.SendFireAndForget(machineName, env); err != nil {
				s.logger.Warn("forward session-resize failed", "session_id", sessionID, "machine", machineName, "error", err)
			}
		default:
			s.logger.Warn("unsupported mobile ws frame type", "session_id", sessionID, "type", frame.Type)
		}
	}
}

func writeMobileFrame(ctx context.Context, conn *websocket.Conn, frame protocol.MobileFrame) error {
	raw, err := frame.Marshal()
	if err != nil {
		return err
	}
	wCtx, cancel := context.WithTimeout(ctx, mobileWSWriteTimeout)
	defer cancel()
	return conn.Write(wCtx, websocket.MessageText, raw)
}

func toSessionResponse(sess store.Session) sessionResponse {
	opts := sess.Options
	if opts == nil {
		opts = map[string]string{}
	}
	return sessionResponse{
		ID:              sess.ID,
		ProjectID:       sess.ProjectID,
		MachineName:     sess.MachineName,
		AgentID:         sess.AgentID,
		Cwd:             sess.Cwd,
		Status:          sess.Status,
		ExitCode:        sess.ExitCode,
		WaitingApproval: false,
		SnapshotPath:    sess.SnapshotPath,
		SnapshotBytes:   sess.SnapshotBytes,
		Options:         opts,
		CreatedAt:       sess.CreatedAt,
		ClosedAt:        sess.ClosedAt,
	}
}

func toMobileChatMessage(msg store.ChatMessage) protocol.MobileChatMessage {
	return protocol.MobileChatMessage{
		ID:         msg.ID,
		SessionID:  msg.SessionID,
		Role:       msg.Role,
		Content:    msg.Content,
		Status:     msg.Status,
		ExitCode:   msg.ExitCode,
		DurationMs: msg.DurationMs,
		CreatedAt:  msg.CreatedAt,
	}
}

func newSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
