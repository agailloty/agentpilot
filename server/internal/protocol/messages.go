package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Runner <-> Server WebSocket message types.
const (
	TypeSessionCreate = "session-create"
	TypeInputWrite    = "input-write"
	TypeSessionResize = "session-resize"
	TypeSessionClose  = "session-close"
	TypeOutputChunk   = "output-chunk"
	TypeSessionExit   = "session-exit"
	TypeSessionSync   = "session-sync"
	TypeFSMkdir       = "fs-mkdir"
	TypeFSList        = "fs-list"
	TypeFSDelete      = "fs-delete"
	TypeExecSync      = "exec-sync"
	TypeToolsProbe    = "tools-probe"
	TypeChatTurn      = "chat-turn"
	TypeChatDelta     = "chat-delta"
	TypeChatDone      = "chat-done"
	TypeChatCancel    = "chat-cancel"
	TypeMenuInspect   = "menu-inspect"
	TypeModelsSync    = "models-sync"
	TypeAck           = "ack"
	TypeError         = "error"
)

// Mobile <-> Server WebSocket frame types.
const (
	MobileTypeReplay         = "replay"
	MobileTypeOutput         = "output"
	MobileTypeApprovalNeeded = "approval-needed"
	MobileTypeSessionExit    = "session-exit"
	MobileTypeInput          = "input"
	MobileTypeResize         = "resize"
	MobileTypeChatSync       = "chat-sync"
	MobileTypeChatMessage    = "chat-message"
	MobileTypeChatDelta      = "chat-delta"
	MobileTypeChatDone       = "chat-done"
	MobileTypeSessionClosed  = "session-closed"
)

var (
	// ErrEmptyPayload is returned when decoding an envelope with no payload.
	ErrEmptyPayload = errors.New("protocol: empty payload")
	// ErrMissingType is returned when an envelope or mobile frame lacks a type.
	ErrMissingType = errors.New("protocol: missing message type")
)

// Envelope is the top-level JSON wire wrapper exchanged between runner and server.
type Envelope struct {
	Type          string          `json:"type"`
	SessionID     string          `json:"sessionId,omitempty"`
	CorrelationID string          `json:"correlationId,omitempty"`
	Payload       json.RawMessage `json:"payload,omitempty"`
}

// NewEnvelope constructs an Envelope and marshals payload if non-nil.
func NewEnvelope(msgType, sessionID, correlationID string, payload any) (Envelope, error) {
	if msgType == "" {
		return Envelope{}, ErrMissingType
	}
	env := Envelope{
		Type:          msgType,
		SessionID:     sessionID,
		CorrelationID: correlationID,
	}
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return Envelope{}, fmt.Errorf("marshal %s payload: %w", msgType, err)
		}
		env.Payload = raw
	}
	return env, nil
}

// Marshal serializes the envelope to JSON.
func (e Envelope) Marshal() ([]byte, error) {
	if e.Type == "" {
		return nil, ErrMissingType
	}
	return json.Marshal(e)
}

// DecodePayload unmarshals the envelope's payload into dst.
func (e Envelope) DecodePayload(dst any) error {
	if len(e.Payload) == 0 {
		return ErrEmptyPayload
	}
	if err := json.Unmarshal(e.Payload, dst); err != nil {
		return fmt.Errorf("unmarshal %s payload: %w", e.Type, err)
	}
	return nil
}

// UnmarshalEnvelope parses a JSON-encoded Envelope and validates its type.
func UnmarshalEnvelope(data []byte) (Envelope, error) {
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return Envelope{}, fmt.Errorf("unmarshal envelope: %w", err)
	}
	if env.Type == "" {
		return Envelope{}, ErrMissingType
	}
	return env, nil
}

// SessionCreatePayload instructs the runner to spawn a PTY session.
type SessionCreatePayload struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Cwd     string            `json:"cwd,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Cols    int               `json:"cols,omitempty"`
	Rows    int               `json:"rows,omitempty"`
}

// DataPayload carries raw terminal bytes (base64-encoded in JSON) for input-write and output-chunk.
type DataPayload struct {
	Data []byte `json:"data"`
}

// ResizePayload updates the PTY window dimensions.
type ResizePayload struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

// SessionExitPayload reports the exit code of a terminated PTY session.
type SessionExitPayload struct {
	ExitCode int `json:"exitCode"`
}

// SessionSyncPayload reconciles active session IDs after runner reconnect.
type SessionSyncPayload struct {
	ActiveSessionIDs []string `json:"activeSessionIds"`
}

// FSMkdirPayload requests directory creation (and optional git init) on the runner host.
type FSMkdirPayload struct {
	Path    string `json:"path"`
	GitInit bool   `json:"gitInit,omitempty"`
}

// FSMkdirAckPayload confirms directory creation on the runner host.
type FSMkdirAckPayload struct {
	Path string `json:"path"`
}

// FSListPayload requests directory listing under Root on the runner host.
type FSListPayload struct {
	Root string `json:"root"`
}

// FSEntry describes a single directory entry returned by fs-list.
type FSEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// FSListAckPayload returns directory entries for a fs-list request.
type FSListAckPayload struct {
	CurrentPath string    `json:"currentPath"`
	ParentPath  string    `json:"parentPath,omitempty"`
	Entries     []FSEntry `json:"entries"`
}

// FSDeletePayload requests recursive deletion of a project directory on the runner host.
type FSDeletePayload struct {
	Path string `json:"path"`
}

// FSDeleteAckPayload confirms directory deletion on the runner host.
type FSDeleteAckPayload struct {
	Path string `json:"path"`
}

// ExecSyncPayload requests a synchronous one-shot command execution on the runner host.
type ExecSyncPayload struct {
	Cwd        string `json:"cwd,omitempty"`
	Command    string `json:"command"`
	TimeoutSec int    `json:"timeoutSec,omitempty"`
}

// ExecSyncAckPayload returns the result of a one-shot command execution.
type ExecSyncAckPayload struct {
	ExitCode   int    `json:"exitCode"`
	Output     string `json:"output"`
	DurationMs int64  `json:"durationMs"`
}

// ToolsProbePayload requests availability and resolved executable paths for CLI commands.
type ToolsProbePayload struct {
	Commands []string `json:"commands"`
}

// ToolStatus reports whether a probed command is installed on the runner host.
type ToolStatus struct {
	Command   string `json:"command"`
	Installed bool   `json:"installed"`
	Path      string `json:"path,omitempty"`
}

// ToolsProbeAckPayload returns probed command statuses keyed by command name.
type ToolsProbeAckPayload struct {
	Tools map[string]ToolStatus `json:"tools"`
}

// ChatTurnPayload instructs the runner to execute one structured chat turn.
type ChatTurnPayload struct {
	MessageID  string            `json:"messageId"`
	Command    string            `json:"command"`
	Args       []string          `json:"args,omitempty"`
	Cwd        string            `json:"cwd,omitempty"`
	Prompt     string            `json:"prompt"`
	ChatMode   string            `json:"chatMode,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	TimeoutSec int               `json:"timeoutSec,omitempty"`
}

// ChatDeltaPayload streams incremental cleaned text chunks for an in-flight chat turn.
type ChatDeltaPayload struct {
	MessageID string `json:"messageId"`
	Delta     string `json:"delta"`
}

// ChatDonePayload reports completion of a chat turn.
type ChatDonePayload struct {
	MessageID  string `json:"messageId"`
	Content    string `json:"content"`
	ExitCode   int    `json:"exitCode"`
	DurationMs int64  `json:"durationMs"`
	Error      string `json:"error,omitempty"`
}

// ChatCancelPayload requests cancellation of an active chat turn on the runner.
type ChatCancelPayload struct {
	MessageID string `json:"messageId,omitempty"`
}

// MenuInspectPayload requests safe execution of a manifest-declared inspect action.
type MenuInspectPayload struct {
	Command    string   `json:"command"`
	Args       []string `json:"args,omitempty"`
	Cwd        string   `json:"cwd,omitempty"`
	TimeoutSec int      `json:"timeoutSec,omitempty"`
}

// MenuInspectAckPayload returns the cleaned output of a manifest-declared inspect action.
type MenuInspectAckPayload struct {
	ExitCode   int    `json:"exitCode"`
	Output     string `json:"output"`
	DurationMs int64  `json:"durationMs"`
}

// ModelsSyncPayload requests discovery of the live model list of an agent CLI.
// Args runs the command non-interactively; otherwise Send is written into a
// scratch PTY session (e.g. "/model\n").
type ModelsSyncPayload struct {
	Command    string   `json:"command"`
	Args       []string `json:"args,omitempty"`
	Send       string   `json:"send,omitempty"`
	Cwd        string   `json:"cwd,omitempty"`
	TimeoutSec int      `json:"timeoutSec,omitempty"`
}

// ModelsSyncAckPayload returns the raw (ANSI-stripped) output of a model
// discovery run. Mode is "exec" or "pty".
type ModelsSyncAckPayload struct {
	ExitCode   int    `json:"exitCode"`
	Output     string `json:"output"`
	DurationMs int64  `json:"durationMs"`
	Mode       string `json:"mode"`
}

// ErrorPayload carries an error description in an error or failed ack message.
type ErrorPayload struct {
	Message string `json:"message"`
}

// MobileChatMessage is the wire representation of a structured chat message sent over WebSocket.
type MobileChatMessage struct {
	ID         string    `json:"id"`
	SessionID  string    `json:"sessionId"`
	Role       string    `json:"role"`
	Content    string    `json:"content"`
	Status     string    `json:"status"`
	ExitCode   *int      `json:"exitCode,omitempty"`
	DurationMs int64     `json:"durationMs"`
	CreatedAt  time.Time `json:"createdAt"`
}

// MobileFrame represents a WebSocket JSON frame exchanged between the mobile app and server.
type MobileFrame struct {
	Type        string              `json:"type"`
	SessionID   string              `json:"sessionId,omitempty"`
	AgentID     string              `json:"agentId,omitempty"`
	Data        []byte              `json:"data,omitempty"`
	Cols        int                 `json:"cols,omitempty"`
	Rows        int                 `json:"rows,omitempty"`
	ExitCode    *int                `json:"exitCode,omitempty"`
	Pattern     string              `json:"pattern,omitempty"`
	Message     string              `json:"message,omitempty"`
	MessageID   string              `json:"messageId,omitempty"`
	Delta       string              `json:"delta,omitempty"`
	Content     string              `json:"content,omitempty"`
	Status      string              `json:"status,omitempty"`
	DurationMs  int64               `json:"durationMs,omitempty"`
	Error       string              `json:"error,omitempty"`
	Messages    []MobileChatMessage `json:"messages,omitempty"`
	ChatMessage *MobileChatMessage  `json:"chatMessage,omitempty"`
}

// Marshal serializes a MobileFrame to JSON.
func (f MobileFrame) Marshal() ([]byte, error) {
	if f.Type == "" {
		return nil, ErrMissingType
	}
	return json.Marshal(f)
}

// UnmarshalMobileFrame parses a JSON-encoded MobileFrame and validates its type.
func UnmarshalMobileFrame(data []byte) (MobileFrame, error) {
	var frame MobileFrame
	if err := json.Unmarshal(data, &frame); err != nil {
		return MobileFrame{}, fmt.Errorf("unmarshal mobile frame: %w", err)
	}
	if frame.Type == "" {
		return MobileFrame{}, ErrMissingType
	}
	return frame, nil
}

// NewMobileReplayFrame creates a replay frame carrying buffered ANSI output.
func NewMobileReplayFrame(sessionID string, data []byte) MobileFrame {
	return MobileFrame{
		Type:      MobileTypeReplay,
		SessionID: sessionID,
		Data:      data,
	}
}

// NewMobileOutputFrame creates a live output frame carrying ANSI output bytes.
func NewMobileOutputFrame(sessionID string, data []byte) MobileFrame {
	return MobileFrame{
		Type:      MobileTypeOutput,
		SessionID: sessionID,
		Data:      data,
	}
}

// NewMobileApprovalNeededFrame creates a notification frame when an agent requests confirmation.
func NewMobileApprovalNeededFrame(sessionID, agentID, pattern, message string) MobileFrame {
	return MobileFrame{
		Type:      MobileTypeApprovalNeeded,
		SessionID: sessionID,
		AgentID:   agentID,
		Pattern:   pattern,
		Message:   message,
	}
}

// NewMobileSessionExitFrame creates a frame reporting session termination and exit code.
func NewMobileSessionExitFrame(sessionID string, exitCode int) MobileFrame {
	code := exitCode
	return MobileFrame{
		Type:      MobileTypeSessionExit,
		SessionID: sessionID,
		ExitCode:  &code,
	}
}

// NewMobileInputFrame creates a client-to-server frame carrying stdin bytes.
func NewMobileInputFrame(sessionID string, data []byte) MobileFrame {
	return MobileFrame{
		Type:      MobileTypeInput,
		SessionID: sessionID,
		Data:      data,
	}
}

// NewMobileResizeFrame creates a client-to-server frame requesting terminal resize.
func NewMobileResizeFrame(sessionID string, cols, rows int) MobileFrame {
	return MobileFrame{
		Type:      MobileTypeResize,
		SessionID: sessionID,
		Cols:      cols,
		Rows:      rows,
	}
}

// NewMobileChatSyncFrame creates an initial sync frame containing persisted chat messages.
func NewMobileChatSyncFrame(sessionID string, messages []MobileChatMessage) MobileFrame {
	if messages == nil {
		messages = []MobileChatMessage{}
	}
	return MobileFrame{
		Type:      MobileTypeChatSync,
		SessionID: sessionID,
		Messages:  messages,
	}
}

// NewMobileChatMessageFrame creates a frame announcing a newly created user or assistant message.
func NewMobileChatMessageFrame(sessionID string, msg MobileChatMessage) MobileFrame {
	copied := msg
	return MobileFrame{
		Type:        MobileTypeChatMessage,
		SessionID:   sessionID,
		MessageID:   msg.ID,
		ChatMessage: &copied,
	}
}

// NewMobileChatDeltaFrame creates a frame streaming an incremental assistant text chunk.
func NewMobileChatDeltaFrame(sessionID, messageID, delta string) MobileFrame {
	return MobileFrame{
		Type:      MobileTypeChatDelta,
		SessionID: sessionID,
		MessageID: messageID,
		Delta:     delta,
	}
}

// NewMobileChatDoneFrame creates a frame reporting completion of an assistant turn.
func NewMobileChatDoneFrame(sessionID string, payload ChatDonePayload, status string) MobileFrame {
	code := payload.ExitCode
	return MobileFrame{
		Type:       MobileTypeChatDone,
		SessionID:  sessionID,
		MessageID:  payload.MessageID,
		Content:    payload.Content,
		Status:     status,
		ExitCode:   &code,
		DurationMs: payload.DurationMs,
		Error:      payload.Error,
	}
}

// NewMobileSessionClosedFrame creates a frame notifying subscribers that a chat session was closed.
func NewMobileSessionClosedFrame(sessionID string) MobileFrame {
	return MobileFrame{
		Type:      MobileTypeSessionClosed,
		SessionID: sessionID,
	}
}
