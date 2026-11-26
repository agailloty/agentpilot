package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Machine represents a registered runner host.
type Machine struct {
	ID         string
	Name       string
	OS         string
	Roots      []string
	Online     bool
	LastSeenAt time.Time
	CreatedAt  time.Time
}

// UpsertMachine inserts a new machine or updates an existing machine by unique name.
func (s *Store) UpsertMachine(ctx context.Context, m Machine) (Machine, error) {
	if m.Name == "" {
		return Machine{}, errors.New("store: machine name is required")
	}

	now := time.Now().UTC()
	if m.ID == "" {
		id, err := newID()
		if err != nil {
			return Machine{}, err
		}
		m.ID = id
	}
	if m.LastSeenAt.IsZero() {
		m.LastSeenAt = now
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = now
	}

	roots := m.Roots
	if roots == nil {
		roots = []string{}
	}
	rootsJSON, err := json.Marshal(roots)
	if err != nil {
		return Machine{}, fmt.Errorf("store: marshal machine roots: %w", err)
	}

	const q = `
INSERT INTO machines (id, name, os, roots_json, online, last_seen_at, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(name) DO UPDATE SET
	os = excluded.os,
	roots_json = excluded.roots_json,
	online = excluded.online,
	last_seen_at = excluded.last_seen_at`

	if _, err := s.db.ExecContext(ctx, q,
		m.ID,
		m.Name,
		m.OS,
		string(rootsJSON),
		boolToInt(m.Online),
		formatTime(m.LastSeenAt),
		formatTime(m.CreatedAt),
	); err != nil {
		return Machine{}, fmt.Errorf("store: upsert machine: %w", err)
	}

	return s.GetMachineByName(ctx, m.Name)
}

// GetMachine returns a machine by ID.
func (s *Store) GetMachine(ctx context.Context, id string) (Machine, error) {
	const q = `
SELECT id, name, os, roots_json, online, last_seen_at, created_at
FROM machines
WHERE id = ?`
	return scanMachine(s.db.QueryRowContext(ctx, q, id))
}

// GetMachineByName returns a machine by unique name.
func (s *Store) GetMachineByName(ctx context.Context, name string) (Machine, error) {
	const q = `
SELECT id, name, os, roots_json, online, last_seen_at, created_at
FROM machines
WHERE name = ?`
	return scanMachine(s.db.QueryRowContext(ctx, q, name))
}

// ListMachines returns all machines ordered by name.
func (s *Store) ListMachines(ctx context.Context) ([]Machine, error) {
	const q = `
SELECT id, name, os, roots_json, online, last_seen_at, created_at
FROM machines
ORDER BY name ASC`

	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("store: list machines: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var machines []Machine
	for rows.Next() {
		m, err := scanMachine(rows)
		if err != nil {
			return nil, err
		}
		machines = append(machines, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate machines: %w", err)
	}
	return machines, nil
}

// SetMachineOnline updates a machine's online status and last_seen_at timestamp.
func (s *Store) SetMachineOnline(ctx context.Context, name string, online bool, lastSeenAt time.Time) error {
	if lastSeenAt.IsZero() {
		lastSeenAt = time.Now().UTC()
	}
	const q = `UPDATE machines SET online = ?, last_seen_at = ? WHERE name = ?`
	res, err := s.db.ExecContext(ctx, q, boolToInt(online), formatTime(lastSeenAt), name)
	if err != nil {
		return fmt.Errorf("store: set machine online: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: set machine online rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanMachine(row rowScanner) (Machine, error) {
	var (
		m           Machine
		rootsJSON   string
		onlineInt   int
		lastSeenRaw string
		createdRaw  string
	)
	if err := row.Scan(&m.ID, &m.Name, &m.OS, &rootsJSON, &onlineInt, &lastSeenRaw, &createdRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Machine{}, ErrNotFound
		}
		return Machine{}, fmt.Errorf("store: scan machine: %w", err)
	}

	if err := json.Unmarshal([]byte(rootsJSON), &m.Roots); err != nil {
		return Machine{}, fmt.Errorf("store: unmarshal machine roots: %w", err)
	}
	lastSeen, err := parseTime(lastSeenRaw)
	if err != nil {
		return Machine{}, fmt.Errorf("store: parse machine last_seen_at: %w", err)
	}
	created, err := parseTime(createdRaw)
	if err != nil {
		return Machine{}, fmt.Errorf("store: parse machine created_at: %w", err)
	}

	m.Online = onlineInt != 0
	m.LastSeenAt = lastSeen
	m.CreatedAt = created
	return m, nil
}
