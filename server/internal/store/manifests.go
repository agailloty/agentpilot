package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// AgentManifest represents an imported agent CLI manifest.
type AgentManifest struct {
	AgentID       string
	Name          string
	LaunchCommand string
	ManifestJSON  string
	UpdatedAt     time.Time
}

// UpsertManifest inserts or updates an agent manifest keyed by agent_id.
func (s *Store) UpsertManifest(ctx context.Context, m AgentManifest) (AgentManifest, error) {
	if m.AgentID == "" {
		return AgentManifest{}, errors.New("store: agent_id is required")
	}
	if m.Name == "" {
		return AgentManifest{}, errors.New("store: manifest name is required")
	}
	if m.LaunchCommand == "" {
		return AgentManifest{}, errors.New("store: manifest launch_command is required")
	}
	if m.ManifestJSON == "" {
		return AgentManifest{}, errors.New("store: manifest_json is required")
	}
	if m.UpdatedAt.IsZero() {
		m.UpdatedAt = time.Now().UTC()
	}

	const q = `
INSERT INTO agent_manifests (agent_id, name, launch_command, manifest_json, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(agent_id) DO UPDATE SET
	name = excluded.name,
	launch_command = excluded.launch_command,
	manifest_json = excluded.manifest_json,
	updated_at = excluded.updated_at`

	if _, err := s.db.ExecContext(ctx, q,
		m.AgentID,
		m.Name,
		m.LaunchCommand,
		m.ManifestJSON,
		formatTime(m.UpdatedAt),
	); err != nil {
		return AgentManifest{}, fmt.Errorf("store: upsert manifest: %w", err)
	}

	return s.GetManifest(ctx, m.AgentID)
}

// GetManifest returns an agent manifest by agent_id.
func (s *Store) GetManifest(ctx context.Context, agentID string) (AgentManifest, error) {
	const q = `
SELECT agent_id, name, launch_command, manifest_json, updated_at
FROM agent_manifests
WHERE agent_id = ?`
	return scanManifest(s.db.QueryRowContext(ctx, q, agentID))
}

// ListManifests returns all imported agent manifests ordered by agent_id.
func (s *Store) ListManifests(ctx context.Context) ([]AgentManifest, error) {
	const q = `
SELECT agent_id, name, launch_command, manifest_json, updated_at
FROM agent_manifests
ORDER BY agent_id ASC`

	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("store: list manifests: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var manifests []AgentManifest
	for rows.Next() {
		m, err := scanManifest(rows)
		if err != nil {
			return nil, err
		}
		manifests = append(manifests, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate manifests: %w", err)
	}
	return manifests, nil
}

func scanManifest(row rowScanner) (AgentManifest, error) {
	var (
		m          AgentManifest
		updatedRaw string
	)
	if err := row.Scan(&m.AgentID, &m.Name, &m.LaunchCommand, &m.ManifestJSON, &updatedRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AgentManifest{}, ErrNotFound
		}
		return AgentManifest{}, fmt.Errorf("store: scan manifest: %w", err)
	}

	updated, err := parseTime(updatedRaw)
	if err != nil {
		return AgentManifest{}, fmt.Errorf("store: parse manifest updated_at: %w", err)
	}
	m.UpdatedAt = updated
	return m, nil
}
