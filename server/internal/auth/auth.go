package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/agailloty/agentpilot/server/internal/config"
	"github.com/agailloty/agentpilot/server/internal/store"
)

var (
	// ErrUnauthorized is returned when a Bearer token is missing, invalid, revoked, or wrong-scope.
	ErrUnauthorized = errors.New("auth: unauthorized")

	// ErrNoActiveTokens is returned at startup when no active tokens exist after bootstrap.
	ErrNoActiveTokens = errors.New("auth: no active tokens configured; set AGENTPILOT_TOKENS")
)

type tokenContextKey struct{}

// HashToken returns the lowercase hexadecimal SHA-256 digest of secret.
func HashToken(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// BootstrapTokens idempotently seeds configured tokens into SQLite (stored as SHA-256 hashes)
// and refuses startup when zero active tokens exist afterwards.
func BootstrapTokens(ctx context.Context, s *store.Store, entries []config.Token) error {
	if s == nil {
		return errors.New("auth: store is required")
	}

	scopeCounters := map[string]int{}
	for _, entry := range entries {
		scope := strings.TrimSpace(entry.Scope)
		if scope != config.ScopeMobile && scope != config.ScopeRunner {
			return fmt.Errorf("auth: invalid token scope %q", entry.Scope)
		}
		if entry.Value == "" {
			return errors.New("auth: empty token secret")
		}

		scopeCounters[scope]++
		name := strings.TrimSpace(entry.Name)
		if name == "" {
			name = fmt.Sprintf("%s-%d", scope, scopeCounters[scope])
		}

		if _, err := s.UpsertToken(ctx, store.Token{
			Name:      name,
			Scope:     scope,
			TokenHash: HashToken(entry.Value),
		}); err != nil {
			return fmt.Errorf("auth: bootstrap token %q (%s): %w", name, scope, err)
		}
	}

	active, err := s.CountActiveTokens(ctx)
	if err != nil {
		return fmt.Errorf("auth: check active tokens: %w", err)
	}
	if active == 0 {
		return ErrNoActiveTokens
	}
	return nil
}

// Authenticator validates Bearer tokens against SHA-256 hashed tokens in the store.
type Authenticator struct {
	store *store.Store
}

// NewAuthenticator creates an Authenticator backed by s.
func NewAuthenticator(s *store.Store) *Authenticator {
	return &Authenticator{store: s}
}

// Authenticate validates rawToken against active tokens in the store.
// If requiredScope is non-empty, the token scope must match requiredScope.
func (a *Authenticator) Authenticate(ctx context.Context, rawToken string, requiredScope string) (store.Token, error) {
	if a == nil || a.store == nil || rawToken == "" {
		return store.Token{}, ErrUnauthorized
	}

	candHash := HashToken(rawToken)
	tok, err := a.store.GetTokenByHash(ctx, candHash)
	if err != nil {
		return store.Token{}, ErrUnauthorized
	}

	if subtle.ConstantTimeCompare([]byte(candHash), []byte(tok.TokenHash)) != 1 {
		return store.Token{}, ErrUnauthorized
	}
	if tok.IsRevoked() {
		return store.Token{}, ErrUnauthorized
	}
	if requiredScope != "" && subtle.ConstantTimeCompare([]byte(tok.Scope), []byte(requiredScope)) != 1 {
		return store.Token{}, ErrUnauthorized
	}

	return tok, nil
}

// TokenFromContext retrieves the authenticated token metadata attached by middleware.
func TokenFromContext(ctx context.Context) (store.Token, bool) {
	tok, ok := ctx.Value(tokenContextKey{}).(store.Token)
	return tok, ok
}
