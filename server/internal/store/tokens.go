package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Token represents a persisted SHA-256 hashed Bearer token.
type Token struct {
	ID        string
	Name      string
	Scope     string
	TokenHash string
	RevokedAt *time.Time
	CreatedAt time.Time
}

// IsRevoked reports whether the token has been revoked.
func (t Token) IsRevoked() bool {
	return t.RevokedAt != nil
}

// CreateToken inserts a new token record.
func (s *Store) CreateToken(ctx context.Context, t Token) (Token, error) {
	if err := validateTokenInput(t); err != nil {
		return Token{}, err
	}
	if t.ID == "" {
		id, err := newID()
		if err != nil {
			return Token{}, err
		}
		t.ID = id
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}

	revokedRaw := ""
	if t.RevokedAt != nil && !t.RevokedAt.IsZero() {
		revokedRaw = formatTime(*t.RevokedAt)
	}

	const q = `
INSERT INTO tokens (id, name, scope, token_hash, revoked_at, created_at)
VALUES (?, ?, ?, ?, ?, ?)`
	if _, err := s.db.ExecContext(ctx, q,
		t.ID,
		t.Name,
		t.Scope,
		t.TokenHash,
		revokedRaw,
		formatTime(t.CreatedAt),
	); err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return Token{}, fmt.Errorf("%w: token (%s, %s) already exists", ErrConflict, t.Name, t.Scope)
		}
		return Token{}, fmt.Errorf("store: create token: %w", err)
	}

	return s.GetTokenByHash(ctx, t.TokenHash)
}

// UpsertToken inserts or updates a token keyed by (name, scope).
func (s *Store) UpsertToken(ctx context.Context, t Token) (Token, error) {
	if err := validateTokenInput(t); err != nil {
		return Token{}, err
	}
	if t.ID == "" {
		id, err := newID()
		if err != nil {
			return Token{}, err
		}
		t.ID = id
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}

	const q = `
INSERT INTO tokens (id, name, scope, token_hash, revoked_at, created_at)
VALUES (?, ?, ?, ?, '', ?)
ON CONFLICT(name, scope) DO UPDATE SET
	token_hash = excluded.token_hash,
	revoked_at = ''`

	if _, err := s.db.ExecContext(ctx, q,
		t.ID,
		t.Name,
		t.Scope,
		t.TokenHash,
		formatTime(t.CreatedAt),
	); err != nil {
		return Token{}, fmt.Errorf("store: upsert token: %w", err)
	}

	return s.GetTokenByHash(ctx, t.TokenHash)
}

// GetTokenByHash looks up a token by its SHA-256 hex hash.
func (s *Store) GetTokenByHash(ctx context.Context, tokenHash string) (Token, error) {
	const q = `
SELECT id, name, scope, token_hash, revoked_at, created_at
FROM tokens
WHERE token_hash = ?`
	return scanToken(s.db.QueryRowContext(ctx, q, tokenHash))
}

// ListTokens returns all tokens ordered by creation time and name.
func (s *Store) ListTokens(ctx context.Context) ([]Token, error) {
	const q = `
SELECT id, name, scope, token_hash, revoked_at, created_at
FROM tokens
ORDER BY created_at ASC, name ASC`

	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("store: list tokens: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var tokens []Token
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate tokens: %w", err)
	}
	return tokens, nil
}

// CountActiveTokens returns the number of non-revoked tokens in the database.
func (s *Store) CountActiveTokens(ctx context.Context) (int, error) {
	const q = `SELECT COUNT(*) FROM tokens WHERE revoked_at = ''`
	var count int
	if err := s.db.QueryRowContext(ctx, q).Scan(&count); err != nil {
		return 0, fmt.Errorf("store: count active tokens: %w", err)
	}
	return count, nil
}

// RevokeToken marks a token revoked by ID.
func (s *Store) RevokeToken(ctx context.Context, id string) error {
	now := formatTime(time.Now().UTC())
	const q = `UPDATE tokens SET revoked_at = ? WHERE id = ?`
	res, err := s.db.ExecContext(ctx, q, now, id)
	if err != nil {
		return fmt.Errorf("store: revoke token: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: revoke token rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func validateTokenInput(t Token) error {
	if t.Name == "" {
		return errors.New("store: token name is required")
	}
	if t.Scope != "mobile" && t.Scope != "runner" {
		return fmt.Errorf("store: invalid token scope %q", t.Scope)
	}
	if t.TokenHash == "" {
		return errors.New("store: token_hash is required")
	}
	return nil
}

func scanToken(row rowScanner) (Token, error) {
	var (
		t          Token
		revokedRaw string
		createdRaw string
	)
	if err := row.Scan(&t.ID, &t.Name, &t.Scope, &t.TokenHash, &revokedRaw, &createdRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Token{}, ErrNotFound
		}
		return Token{}, fmt.Errorf("store: scan token: %w", err)
	}

	created, err := parseTime(createdRaw)
	if err != nil {
		return Token{}, fmt.Errorf("store: parse token created_at: %w", err)
	}
	t.CreatedAt = created

	if revokedRaw != "" {
		revoked, err := parseTime(revokedRaw)
		if err != nil {
			return Token{}, fmt.Errorf("store: parse token revoked_at: %w", err)
		}
		t.RevokedAt = &revoked
	}
	return t, nil
}
