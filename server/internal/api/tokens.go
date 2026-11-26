package api

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/agailloty/agentpilot/server/internal/auth"
	"github.com/agailloty/agentpilot/server/internal/config"
	"github.com/agailloty/agentpilot/server/internal/store"
)

type tokenResponse struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Scope     string     `json:"scope"`
	CreatedAt time.Time  `json:"createdAt"`
	RevokedAt *time.Time `json:"revokedAt"`
}

type createTokenRequest struct {
	Name  string `json:"name"`
	Scope string `json:"scope"`
}

type createTokenResponse struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Scope     string    `json:"scope"`
	Token     string    `json:"token"`
	CreatedAt time.Time `json:"createdAt"`
}

func (s *Server) handleListTokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := s.store.ListTokens(r.Context())
	if err != nil {
		s.logger.Error("list tokens failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list tokens")
		return
	}

	out := make([]tokenResponse, 0, len(tokens))
	for _, tok := range tokens {
		out = append(out, tokenResponse{
			ID:        tok.ID,
			Name:      tok.Name,
			Scope:     tok.Scope,
			CreatedAt: tok.CreatedAt,
			RevokedAt: tok.RevokedAt,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	var req createTokenRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	name := strings.TrimSpace(req.Name)
	scope := strings.TrimSpace(req.Scope)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if scope != config.ScopeMobile && scope != config.ScopeRunner {
		writeError(w, http.StatusBadRequest, "scope must be mobile or runner")
		return
	}

	var rawSecret [32]byte
	if _, err := rand.Read(rawSecret[:]); err != nil {
		s.logger.Error("generate token secret failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}
	secret := hex.EncodeToString(rawSecret[:])

	created, err := s.store.CreateToken(r.Context(), store.Token{
		Name:      name,
		Scope:     scope,
		TokenHash: auth.HashToken(secret),
	})
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, "token with this name and scope already exists")
			return
		}
		s.logger.Error("create token failed", "name", name, "scope", scope, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create token")
		return
	}

	writeJSON(w, http.StatusCreated, createTokenResponse{
		ID:        created.ID,
		Name:      created.Name,
		Scope:     created.Scope,
		Token:     secret,
		CreatedAt: created.CreatedAt,
	})
}

func (s *Server) handleRevokeToken(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "token id is required")
		return
	}

	if currentTok, ok := auth.TokenFromContext(r.Context()); ok && id == currentTok.ID {
		writeError(w, http.StatusConflict, "cannot revoke active token")
		return
	}

	if err := s.store.RevokeToken(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "token not found")
			return
		}
		s.logger.Error("revoke token failed", "token_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to revoke token")
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"revoked": true})
}
