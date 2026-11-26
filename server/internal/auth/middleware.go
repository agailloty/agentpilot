package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// RequireScope returns HTTP middleware requiring a valid, non-revoked Bearer token with the given scope.
func (a *Authenticator) RequireScope(scope string) func(http.Handler) http.Handler {
	return a.require(scope)
}

// RequireAuth returns HTTP middleware requiring any valid, non-revoked Bearer token regardless of scope.
func (a *Authenticator) RequireAuth() func(http.Handler) http.Handler {
	return a.require("")
}

// CORS adds cross-origin headers for browser clients (such as Flutter Web)
// and short-circuits valid CORS preflight OPTIONS requests with 204 No Content.
// All non-preflight requests pass through to next without bypassing auth.
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Max-Age", "86400")
		}

		if origin != "" && r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (a *Authenticator) require(requiredScope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawToken, ok := ExtractBearerToken(r.Header.Get("Authorization"))
			if !ok && strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket") {
				rawToken, ok = ExtractWebSocketBearerToken(r.Header.Values("Sec-WebSocket-Protocol"))
			}
			if !ok {
				writeUnauthorized(w)
				return
			}

			tok, err := a.Authenticate(r.Context(), rawToken, requiredScope)
			if err != nil {
				writeUnauthorized(w)
				return
			}

			ctx := context.WithValue(r.Context(), tokenContextKey{}, tok)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ExtractBearerToken extracts the token from an `Authorization: Bearer <token>` header.
func ExtractBearerToken(header string) (string, bool) {
	scheme, token, ok := strings.Cut(strings.TrimSpace(header), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return "", false
	}
	return token, true
}

// ExtractWebSocketBearerToken extracts a token from `Sec-WebSocket-Protocol`
// entries formatted as `bearer.<token>`.
func ExtractWebSocketBearerToken(protocols []string) (string, bool) {
	for _, headerVal := range protocols {
		for _, item := range strings.Split(headerVal, ",") {
			proto := strings.TrimSpace(item)
			if !strings.HasPrefix(proto, "bearer.") {
				continue
			}
			secret := strings.TrimPrefix(proto, "bearer.")
			if secret != "" {
				return secret, true
			}
		}
	}
	return "", false
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
}
