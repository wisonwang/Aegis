package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wisonwang/aegis/internal/auth"
	"github.com/wisonwang/aegis/internal/config"
	"github.com/wisonwang/aegis/internal/store"
)

// TestAuthenticate_DisabledAccountFailClosed verifies that disabling a user
// takes effect on every subsequent request (not just at next login): a token
// issued while active must be rejected once the account is disabled.
func TestAuthenticate_DisabledAccountFailClosed(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	cfg := &config.Config{JWTSecret: "test-secret", JWTExpiry: "24h"}
	if err := st.CreateUser(&store.User{ID: "u1", Username: "alice", Status: "active"}); err != nil {
		t.Fatalf("create user: %v", err)
	}

	// A handler that only succeeds when the middleware lets the request through.
	ok := func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"ok": "1"})
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /protected", Authenticate(st, cfg, ok))

	tok, err := auth.GenerateToken(&auth.Claims{UserID: "u1", Username: "alice", Roles: []string{"analyst"}}, cfg.JWTSecret, cfg.JWTExpiry)
	if err != nil {
		t.Fatalf("token: %v", err)
	}

	// Active account: request succeeds.
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("active: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Disable the account.
	if err := st.UpdateUser(&store.User{ID: "u1", Status: "disabled"}); err != nil {
		t.Fatalf("disable: %v", err)
	}

	// Same token now rejected (fail-closed).
	req2 := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req2.Header.Set("Authorization", "Bearer "+tok)
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	if w2.Code != http.StatusForbidden {
		t.Fatalf("disabled: expected 403, got %d: %s", w2.Code, w2.Body.String())
	}
}
