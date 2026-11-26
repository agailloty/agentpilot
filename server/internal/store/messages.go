package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ChatMessage represents a persisted user, assistant, or system message in a chat session.
type ChatMessage struct {
	ID         string    `json:"id"`
	SessionID  string    `json:"sessionId"`
	Role       string    `json:"role"`
	Content    string    `json:"content"`
	Status     string    `json:"status"`
	ExitCode   *int      `json:"exitCode,omitempty"`
	DurationMs int64     `json:"durationMs"`
	CreatedAt  time.Time `json:"createdAt"`
}

// CreateMessage inserts a new chat message for a session.
func (s *Store) CreateMessage(ctx context.Context, msg ChatMessage) (ChatMessage, error) {
	if strings.TrimSpace(msg.SessionID) == "" {
		return ChatMessage{}, errors.New("store: message session_id is required")
	}
	if strings.TrimSpace(msg.Role) == "" {
		return ChatMessage{}, errors.New("store: message role is required")
	}
	if msg.ID == "" {
		id, err := newID()
		if err != nil {
			return ChatMessage{}, err
		}
		msg.ID = id
	}
	if msg.Status == "" {
		msg.Status = "done"
	}
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now().UTC()
	}

	var exitCode sql.NullInt64
	if msg.ExitCode != nil {
		exitCode = sql.NullInt64{Int64: int64(*msg.ExitCode), Valid: true}
	}

	const q = `
INSERT INTO session_messages (id, session_id, role, content, status, exit_code, duration_ms, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`

	if _, err := s.db.ExecContext(ctx, q,
		msg.ID,
		msg.SessionID,
		msg.Role,
		msg.Content,
		msg.Status,
		exitCode,
		msg.DurationMs,
		formatTime(msg.CreatedAt),
	); err != nil {
		return ChatMessage{}, fmt.Errorf("store: create message: %w", err)
	}

	return s.GetMessage(ctx, msg.ID)
}

// GetMessage returns a single chat message by ID.
func (s *Store) GetMessage(ctx context.Context, id string) (ChatMessage, error) {
	const q = `
SELECT id, session_id, role, content, status, exit_code, duration_ms, created_at
FROM session_messages
WHERE id = ?`
	return scanChatMessage(s.db.QueryRowContext(ctx, q, id))
}

// ListMessages returns all messages for a session ordered chronologically.
func (s *Store) ListMessages(ctx context.Context, sessionID string) ([]ChatMessage, error) {
	const q = `
SELECT id, session_id, role, content, status, exit_code, duration_ms, created_at
FROM session_messages
WHERE session_id = ?
ORDER BY created_at ASC, rowid ASC`

	rows, err := s.db.QueryContext(ctx, q, sessionID)
	if err != nil {
		return nil, fmt.Errorf("store: list messages: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]ChatMessage, 0)
	for rows.Next() {
		msg, err := scanChatMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate messages: %w", err)
	}
	return out, nil
}

// UpdateMessage updates content, status, exit code, and duration for an existing message.
func (s *Store) UpdateMessage(ctx context.Context, id, content, status string, exitCode *int, durationMs int64) error {
	var code sql.NullInt64
	if exitCode != nil {
		code = sql.NullInt64{Int64: int64(*exitCode), Valid: true}
	}
	const q = `
UPDATE session_messages
SET content = ?, status = ?, exit_code = ?, duration_ms = ?
WHERE id = ?`
	res, err := s.db.ExecContext(ctx, q, content, status, code, durationMs, id)
	if err != nil {
		return fmt.Errorf("store: update message: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update message rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// CountUserMessages returns the total number of user messages persisted for a session.
func (s *Store) CountUserMessages(ctx context.Context, sessionID string) (int, error) {
	const q = `SELECT COUNT(*) FROM session_messages WHERE session_id = ? AND role = 'user'`
	var count int
	if err := s.db.QueryRowContext(ctx, q, sessionID).Scan(&count); err != nil {
		return 0, fmt.Errorf("store: count user messages: %w", err)
	}
	return count, nil
}

// SessionMatchesMessageQuery reports whether any message in sessionID contains query (case-insensitive).
func (s *Store) SessionMatchesMessageQuery(ctx context.Context, sessionID, query string) (bool, error) {
	needle := strings.ToLower(strings.TrimSpace(query))
	if needle == "" {
		return true, nil
	}
	const q = `SELECT EXISTS(SELECT 1 FROM session_messages WHERE session_id = ? AND LOWER(content) LIKE ?)`
	var matched int
	if err := s.db.QueryRowContext(ctx, q, sessionID, "%"+needle+"%").Scan(&matched); err != nil {
		return false, fmt.Errorf("store: search session messages: %w", err)
	}
	return matched == 1, nil
}

func scanChatMessage(row rowScanner) (ChatMessage, error) {
	var (
		msg        ChatMessage
		exitCode   sql.NullInt64
		createdRaw string
	)
	if err := row.Scan(
		&msg.ID,
		&msg.SessionID,
		&msg.Role,
		&msg.Content,
		&msg.Status,
		&exitCode,
		&msg.DurationMs,
		&createdRaw,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ChatMessage{}, ErrNotFound
		}
		return ChatMessage{}, fmt.Errorf("store: scan chat message: %w", err)
	}
	if exitCode.Valid {
		code := int(exitCode.Int64)
		msg.ExitCode = &code
	}
	created, err := parseTime(createdRaw)
	if err != nil {
		return ChatMessage{}, fmt.Errorf("store: parse message created_at: %w", err)
	}
	msg.CreatedAt = created
	return msg, nil
}
