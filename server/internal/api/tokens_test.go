package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestTokensEndpointsLifecycle(t *testing.T) {
	env := setupAPITestEnv(t)

	// 1. GET /api/tokens lists bootstrapped tokens without leaking hashes or secrets.
	listResp := env.doReq(t, http.MethodGet, "/api/tokens", env.mobileTok, nil)
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/tokens status = %d, want 200", listResp.StatusCode)
	}
	rawList, err := io.ReadAll(listResp.Body)
	_ = listResp.Body.Close()
	if err != nil {
		t.Fatalf("read list body: %v", err)
	}
	listBodyStr := string(rawList)
	for _, forbidden := range []string{"tokenHash", "token_hash", env.mobileTok, env.runnerTok} {
		if strings.Contains(listBodyStr, forbidden) {
			t.Fatalf("GET /api/tokens leaked %q in response: %s", forbidden, listBodyStr)
		}
	}

	var listed []tokenResponse
	if err := json.Unmarshal(rawList, &listed); err != nil {
		t.Fatalf("unmarshal tokens: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("len(listed) = %d, want 2: %+v", len(listed), listed)
	}

	var currentMobileID string
	for _, tok := range listed {
		if tok.Name == "mobile" && tok.Scope == "mobile" {
			currentMobileID = tok.ID
		}
	}
	if currentMobileID == "" {
		t.Fatal("expected bootstrapped mobile token in GET /api/tokens")
	}

	// 2. POST /api/tokens validation errors -> 400.
	for _, bad := range []createTokenRequest{
		{Name: "", Scope: "mobile"},
		{Name: "   ", Scope: "runner"},
		{Name: "admin", Scope: "admin"},
	} {
		badResp := env.doReq(t, http.MethodPost, "/api/tokens", env.mobileTok, bad)
		_ = badResp.Body.Close()
		if badResp.StatusCode != http.StatusBadRequest {
			t.Fatalf("POST /api/tokens(%+v) status = %d, want 400", bad, badResp.StatusCode)
		}
	}

	// 3. POST /api/tokens creates new mobile token -> 201 with 64-char hex secret.
	createResp := env.doReq(t, http.MethodPost, "/api/tokens", env.mobileTok, createTokenRequest{
		Name:  "pixel-9",
		Scope: "mobile",
	})
	if createResp.StatusCode != http.StatusCreated {
		rawErr, _ := io.ReadAll(createResp.Body)
		_ = createResp.Body.Close()
		t.Fatalf("POST /api/tokens status = %d, want 201 (%s)", createResp.StatusCode, string(rawErr))
	}
	var created createTokenResponse
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		_ = createResp.Body.Close()
		t.Fatalf("decode created token: %v", err)
	}
	_ = createResp.Body.Close()

	if created.ID == "" || created.Name != "pixel-9" || created.Scope != "mobile" {
		t.Fatalf("unexpected created token metadata: %+v", created)
	}
	if len(created.Token) != 64 {
		t.Fatalf("len(created.Token) = %d, want 64 hex chars", len(created.Token))
	}

	// Newly created token works immediately for authentication.
	authCheckResp := env.doReq(t, http.MethodGet, "/api/tokens", created.Token, nil)
	rawAuthCheck, _ := io.ReadAll(authCheckResp.Body)
	_ = authCheckResp.Body.Close()
	if authCheckResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/tokens with newly created token status = %d, want 200", authCheckResp.StatusCode)
	}
	if strings.Contains(string(rawAuthCheck), created.Token) {
		t.Fatal("GET /api/tokens leaked newly created plaintext secret")
	}

	// 4. Duplicate (name, scope) returns 409 Conflict.
	dupResp := env.doReq(t, http.MethodPost, "/api/tokens", env.mobileTok, createTokenRequest{
		Name:  "pixel-9",
		Scope: "mobile",
	})
	_ = dupResp.Body.Close()
	if dupResp.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate POST /api/tokens status = %d, want 409", dupResp.StatusCode)
	}

	// 5. Self-revocation guard returns 409 Conflict.
	selfRevokeResp := env.doReq(t, http.MethodDelete, "/api/tokens/"+currentMobileID, env.mobileTok, nil)
	rawSelf, _ := io.ReadAll(selfRevokeResp.Body)
	_ = selfRevokeResp.Body.Close()
	if selfRevokeResp.StatusCode != http.StatusConflict {
		t.Fatalf("self DELETE /api/tokens/%s status = %d, want 409 (%s)", currentMobileID, selfRevokeResp.StatusCode, string(rawSelf))
	}

	// 6. Revoking unknown token returns 404 Not Found.
	notFoundResp := env.doReq(t, http.MethodDelete, "/api/tokens/does-not-exist", env.mobileTok, nil)
	_ = notFoundResp.Body.Close()
	if notFoundResp.StatusCode != http.StatusNotFound {
		t.Fatalf("DELETE unknown token status = %d, want 404", notFoundResp.StatusCode)
	}

	// 7. Revoking another token returns 200 {"revoked":true}, and that token then fails with 401.
	revokeResp := env.doReq(t, http.MethodDelete, "/api/tokens/"+created.ID, env.mobileTok, nil)
	if revokeResp.StatusCode != http.StatusOK {
		rawErr, _ := io.ReadAll(revokeResp.Body)
		_ = revokeResp.Body.Close()
		t.Fatalf("DELETE /api/tokens/%s status = %d, want 200 (%s)", created.ID, revokeResp.StatusCode, string(rawErr))
	}
	var revokeBody map[string]bool
	if err := json.NewDecoder(revokeResp.Body).Decode(&revokeBody); err != nil {
		_ = revokeResp.Body.Close()
		t.Fatalf("decode revoke response: %v", err)
	}
	_ = revokeResp.Body.Close()
	if !revokeBody["revoked"] {
		t.Fatalf("expected revoked=true, got %+v", revokeBody)
	}

	revokedAuthResp := env.doReq(t, http.MethodGet, "/api/tokens", created.Token, nil)
	_ = revokedAuthResp.Body.Close()
	if revokedAuthResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked token auth status = %d, want 401", revokedAuthResp.StatusCode)
	}
}
