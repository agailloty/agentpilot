package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Project represents a workspace folder on a runner machine.
type Project struct {
	ID          string
	Name        string
	MachineName string
	Path        string
	Archived    bool
	CreatedAt   time.Time
}

// CreateProject inserts a new project record.
func (s *Store) CreateProject(ctx context.Context, p Project) (Project, error) {
	if p.Name == "" {
		return Project{}, errors.New("store: project name is required")
	}
	if p.MachineName == "" {
		return Project{}, errors.New("store: project machine_name is required")
	}
	if p.Path == "" {
		return Project{}, errors.New("store: project path is required")
	}

	if p.ID == "" {
		id, err := newID()
		if err != nil {
			return Project{}, err
		}
		p.ID = id
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now().UTC()
	}

	const q = `
INSERT INTO projects (id, name, machine_name, path, archived, created_at)
VALUES (?, ?, ?, ?, ?, ?)`

	if _, err := s.db.ExecContext(ctx, q,
		p.ID,
		p.Name,
		p.MachineName,
		p.Path,
		boolToInt(p.Archived),
		formatTime(p.CreatedAt),
	); err != nil {
		return Project{}, fmt.Errorf("store: create project: %w", err)
	}

	return s.GetProject(ctx, p.ID)
}

// GetProject returns a project by ID.
func (s *Store) GetProject(ctx context.Context, id string) (Project, error) {
	const q = `
SELECT id, name, machine_name, path, archived, created_at
FROM projects
WHERE id = ?`
	return scanProject(s.db.QueryRowContext(ctx, q, id))
}

// ListProjects returns non-archived projects, optionally filtered by machine name.
func (s *Store) ListProjects(ctx context.Context, machineName string) ([]Project, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if machineName != "" {
		const q = `
SELECT id, name, machine_name, path, archived, created_at
FROM projects
WHERE archived = 0 AND machine_name = ?
ORDER BY created_at DESC, name ASC`
		rows, err = s.db.QueryContext(ctx, q, machineName)
	} else {
		const q = `
SELECT id, name, machine_name, path, archived, created_at
FROM projects
WHERE archived = 0
ORDER BY created_at DESC, name ASC`
		rows, err = s.db.QueryContext(ctx, q)
	}
	if err != nil {
		return nil, fmt.Errorf("store: list projects: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var projects []Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate projects: %w", err)
	}
	return projects, nil
}

// DeleteProject deletes a project by ID (cascading to its sessions).
func (s *Store) DeleteProject(ctx context.Context, id string) error {
	const q = `DELETE FROM projects WHERE id = ?`
	res, err := s.db.ExecContext(ctx, q, id)
	if err != nil {
		return fmt.Errorf("store: delete project: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: delete project rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func scanProject(row rowScanner) (Project, error) {
	var (
		p           Project
		archivedInt int
		createdRaw  string
	)
	if err := row.Scan(&p.ID, &p.Name, &p.MachineName, &p.Path, &archivedInt, &createdRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Project{}, ErrNotFound
		}
		return Project{}, fmt.Errorf("store: scan project: %w", err)
	}

	created, err := parseTime(createdRaw)
	if err != nil {
		return Project{}, fmt.Errorf("store: parse project created_at: %w", err)
	}
	p.Archived = archivedInt != 0
	p.CreatedAt = created
	return p, nil
}
