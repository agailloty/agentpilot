package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Session represents a chat or agent session tied to a project.
type Session struct {
	ID            string
	ProjectID     string
	MachineName   string
	AgentID       string
	Cwd           string
	Status        string
	ExitCode      *int
	SnapshotPath  string
	SnapshotBytes int64
	Options       map[string]string
	CreatedAt     time.Time
	ClosedAt      *time.Time
}

// CreateSession inserts a new session record.
func (s *Store) CreateSession(ctx context.Context, sess Session) (Session, error) {
	if sess.ProjectID == "" {
		return Session{}, errors.New("store: session project_id is required")
	}
	if sess.MachineName == "" {
		return Session{}, errors.New("store: session machine_name is required")
	}
	if sess.AgentID == "" {
		return Session{}, errors.New("store: session agent_id is required")
	}
	if sess.Cwd == "" {
		return Session{}, errors.New("store: session cwd is required")
	}

	if sess.ID == "" {
		id, err := newID()
		if err != nil {
			return Session{}, err
		}
		sess.ID = id
	}
	if sess.Status == "" {
		sess.Status = "running"
	}
	if sess.CreatedAt.IsZero() {
		sess.CreatedAt = time.Now().UTC()
	}

	var exitCode sql.NullInt64
	if sess.ExitCode != nil {
		exitCode = sql.NullInt64{Int64: int64(*sess.ExitCode), Valid: true}
	}
	closedRaw := ""
	if sess.ClosedAt != nil && !sess.ClosedAt.IsZero() {
		closedRaw = formatTime(*sess.ClosedAt)
	}
	optionsRaw, err := marshalOptions(sess.Options)
	if err != nil {
		return Session{}, fmt.Errorf("store: marshal session options: %w", err)
	}

	const q = `
INSERT INTO sessions (id, project_id, machine_name, agent_id, cwd, status, exit_code, snapshot_path, snapshot_bytes, options_json, created_at, closed_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	if _, err := s.db.ExecContext(ctx, q,
		sess.ID,
		sess.ProjectID,
		sess.MachineName,
		sess.AgentID,
		sess.Cwd,
		sess.Status,
		exitCode,
		sess.SnapshotPath,
		sess.SnapshotBytes,
		optionsRaw,
		formatTime(sess.CreatedAt),
		closedRaw,
	); err != nil {
		return Session{}, fmt.Errorf("store: create session: %w", err)
	}

	return s.GetSession(ctx, sess.ID)
}

// GetSession returns a session by ID.
func (s *Store) GetSession(ctx context.Context, id string) (Session, error) {
	const q = `
SELECT id, project_id, machine_name, agent_id, cwd, status, exit_code, snapshot_path, snapshot_bytes, options_json, created_at, closed_at
FROM sessions
WHERE id = ?`
	return scanSession(s.db.QueryRowContext(ctx, q, id))
}

// MatchesMetadata reports whether term matches session id, agentId, machineName, or cwd case-insensitively.
func (sess Session) MatchesMetadata(term string) bool {
	needle := strings.ToLower(strings.TrimSpace(term))
	if needle == "" {
		return true
	}
	return strings.Contains(strings.ToLower(sess.ID), needle) ||
		strings.Contains(strings.ToLower(sess.AgentID), needle) ||
		strings.Contains(strings.ToLower(sess.MachineName), needle) ||
		strings.Contains(strings.ToLower(sess.Cwd), needle)
}

// SessionFilter filters sessions returned by ListSessionsFiltered.
type SessionFilter struct {
	ProjectID   string
	MachineName string
	Status      string
}

// ListSessions returns sessions, optionally filtered by project ID, ordered newest first.
func (s *Store) ListSessions(ctx context.Context, projectID string) ([]Session, error) {
	return s.ListSessionsFiltered(ctx, SessionFilter{ProjectID: projectID})
}

// ListSessionsFiltered returns sessions matching all non-empty filter fields, ordered newest first.
func (s *Store) ListSessionsFiltered(ctx context.Context, filter SessionFilter) ([]Session, error) {
	query := `
SELECT id, project_id, machine_name, agent_id, cwd, status, exit_code, snapshot_path, snapshot_bytes, options_json, created_at, closed_at
FROM sessions`
	var (
		conds []string
		args  []any
	)
	if filter.ProjectID != "" {
		conds = append(conds, "project_id = ?")
		args = append(args, filter.ProjectID)
	}
	if filter.MachineName != "" {
		conds = append(conds, "machine_name = ?")
		args = append(args, filter.MachineName)
	}
	if filter.Status != "" {
		conds = append(conds, "status = ?")
		args = append(args, filter.Status)
	}
	if len(conds) > 0 {
		query += " WHERE " + conds[0]
		for _, c := range conds[1:] {
			query += " AND " + c
		}
	}
	query += " ORDER BY created_at DESC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var sessions []Session
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, sess)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate sessions: %w", err)
	}
	return sessions, nil
}

// UpdateSessionSnapshot updates only snapshot_path and snapshot_bytes for a session.
func (s *Store) UpdateSessionSnapshot(ctx context.Context, id, snapshotPath string, snapshotBytes int64) error {
	const q = `
UPDATE sessions
SET snapshot_path = ?, snapshot_bytes = ?
WHERE id = ?`
	res, err := s.db.ExecContext(ctx, q, snapshotPath, snapshotBytes, id)
	if err != nil {
		return fmt.Errorf("store: update session snapshot: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update session snapshot rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateSession updates mutable session fields (status, exit code, snapshot metadata, closed_at).
func (s *Store) UpdateSession(ctx context.Context, sess Session) error {
	var exitCode sql.NullInt64
	if sess.ExitCode != nil {
		exitCode = sql.NullInt64{Int64: int64(*sess.ExitCode), Valid: true}
	}
	closedRaw := ""
	if sess.ClosedAt != nil && !sess.ClosedAt.IsZero() {
		closedRaw = formatTime(*sess.ClosedAt)
	}

	const q = `
UPDATE sessions
SET status = ?, exit_code = ?, snapshot_path = ?, snapshot_bytes = ?, closed_at = ?
WHERE id = ?`
	res, err := s.db.ExecContext(ctx, q,
		sess.Status,
		exitCode,
		sess.SnapshotPath,
		sess.SnapshotBytes,
		closedRaw,
		sess.ID,
	)
	if err != nil {
		return fmt.Errorf("store: update session: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update session rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// CloseSession marks a session closed with final status, exit code, and snapshot metadata.
func (s *Store) CloseSession(ctx context.Context, id string, status string, exitCode *int, snapshotPath string, snapshotBytes int64, closedAt time.Time) error {
	if status == "" {
		status = "closed"
	}
	if closedAt.IsZero() {
		closedAt = time.Now().UTC()
	}
	return s.UpdateSession(ctx, Session{
		ID:            id,
		Status:        status,
		ExitCode:      exitCode,
		SnapshotPath:  snapshotPath,
		SnapshotBytes: snapshotBytes,
		ClosedAt:      &closedAt,
	})
}

// UpdateSessionOptions persists updated provider option key/value pairs for a session.
func (s *Store) UpdateSessionOptions(ctx context.Context, id string, options map[string]string) error {
	optionsRaw, err := marshalOptions(options)
	if err != nil {
		return fmt.Errorf("store: marshal session options: %w", err)
	}
	const q = `UPDATE sessions SET options_json = ? WHERE id = ?`
	res, err := s.db.ExecContext(ctx, q, optionsRaw, id)
	if err != nil {
		return fmt.Errorf("store: update session options: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update session options rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func scanSession(row rowScanner) (Session, error) {
	var (
		sess       Session
		exitCode   sql.NullInt64
		optionsRaw string
		createdRaw string
		closedRaw  string
	)
	if err := row.Scan(
		&sess.ID,
		&sess.ProjectID,
		&sess.MachineName,
		&sess.AgentID,
		&sess.Cwd,
		&sess.Status,
		&exitCode,
		&sess.SnapshotPath,
		&sess.SnapshotBytes,
		&optionsRaw,
		&createdRaw,
		&closedRaw,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Session{}, ErrNotFound
		}
		return Session{}, fmt.Errorf("store: scan session: %w", err)
	}

	if exitCode.Valid {
		code := int(exitCode.Int64)
		sess.ExitCode = &code
	}
	sess.Options = unmarshalOptions(optionsRaw)
	created, err := parseTime(createdRaw)
	if err != nil {
		return Session{}, fmt.Errorf("store: parse session created_at: %w", err)
	}
	sess.CreatedAt = created

	if closedRaw != "" {
		closed, err := parseTime(closedRaw)
		if err != nil {
			return Session{}, fmt.Errorf("store: parse session closed_at: %w", err)
		}
		sess.ClosedAt = &closed
	}
	return sess, nil
}

func marshalOptions(options map[string]string) (string, error) {
	if len(options) == 0 {
		return "{}", nil
	}
	raw, err := json.Marshal(options)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func unmarshalOptions(raw string) map[string]string {
	out := make(map[string]string)
	if strings.TrimSpace(raw) == "" {
		return out
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil || out == nil {
		return make(map[string]string)
	}
	return out
}
