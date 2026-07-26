package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wisonwang/aegis/internal/auth"
	"github.com/wisonwang/aegis/internal/config"
	"github.com/wisonwang/aegis/internal/store"
)

// mockDirectory is an in-memory auth.Directory used to exercise the LDAP
// handler without a live directory server.
type mockDirectory struct {
	id  *auth.LDAPIdentity
	err error
}

func (m *mockDirectory) Authenticate(ctx context.Context, username, password string) (*auth.LDAPIdentity, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.id, nil
}

func newLDAPTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestLDAPLogin_SuccessProvisionsRoleAndToken(t *testing.T) {
	st := newLDAPTestStore(t)
	_ = st.CreateRole(&store.Role{ID: "r-analyst", Name: "analyst"})

	dir := &mockDirectory{id: &auth.LDAPIdentity{
		DN:          "uid=alice,ou=people,dc=example,dc=com",
		Username:    "alice",
		DisplayName: "Alice",
		Email:       "alice@example.com",
		Groups:      []string{"aegis-analysts"},
	}}
	cfg := &config.Config{JWTSecret: "test-secret", JWTExpiry: "24h", LDAP: config.LDAPConfig{
		ClaimMappings: map[string]string{"aegis-analysts": "analyst"},
	}}
	h := &LDAPHandler{Dir: dir, Store: st, Cfg: cfg}

	body := `{"username":"alice","password":"secret"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/ldap/login", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.LDAPLogin(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp loginResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Token == "" {
		t.Fatal("expected token")
	}
	if resp.User.Username != "alice" {
		t.Fatalf("username = %q", resp.User.Username)
	}
	if !containsStr(resp.User.Roles, "analyst") {
		t.Fatalf("expected analyst role, got %v", resp.User.Roles)
	}

	claims, err := auth.ParseToken(resp.Token, cfg.JWTSecret)
	if err != nil {
		t.Fatalf("parse token: %v", err)
	}
	if !containsStr(claims.Roles, "analyst") {
		t.Fatalf("token roles = %v", claims.Roles)
	}

	// A repeat login must link to the same user, not create a second one.
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/ldap/login", strings.NewReader(body))
	w2 := httptest.NewRecorder()
	h.LDAPLogin(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("second login expected 200, got %d", w2.Code)
	}
	var resp2 loginResponse
	_ = json.Unmarshal(w2.Body.Bytes(), &resp2)
	if resp2.User.ID != resp.User.ID {
		t.Fatalf("expected stable user id, got %q vs %q", resp.User.ID, resp2.User.ID)
	}
}

func TestLDAPLogin_DefaultRolesApplied(t *testing.T) {
	st := newLDAPTestStore(t)
	_ = st.CreateRole(&store.Role{ID: "r-viewer", Name: "viewer"})

	dir := &mockDirectory{id: &auth.LDAPIdentity{
		DN:       "uid=bob,ou=people,dc=example,dc=com",
		Username: "bob",
	}}
	cfg := &config.Config{JWTSecret: "s", JWTExpiry: "24h", LDAP: config.LDAPConfig{
		DefaultRoles: []string{"viewer"},
	}}
	h := &LDAPHandler{Dir: dir, Store: st, Cfg: cfg}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/ldap/login", strings.NewReader(`{"username":"bob","password":"x"}`))
	w := httptest.NewRecorder()
	h.LDAPLogin(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp loginResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if !containsStr(resp.User.Roles, "viewer") {
		t.Fatalf("expected default viewer role, got %v", resp.User.Roles)
	}
}

func TestLDAPLogin_UnmappedGroupAutoCreatesRole(t *testing.T) {
	st := newLDAPTestStore(t) // role not pre-created
	dir := &mockDirectory{id: &auth.LDAPIdentity{
		DN:       "uid=carol,ou=people,dc=example,dc=com",
		Username: "carol",
		Groups:   []string{"aegis-viewer"},
	}}
	cfg := &config.Config{JWTSecret: "s", JWTExpiry: "24h", LDAP: config.LDAPConfig{
		ClaimMappings: map[string]string{"aegis-viewer": "viewer"},
	}}
	h := &LDAPHandler{Dir: dir, Store: st, Cfg: cfg}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/ldap/login", strings.NewReader(`{"username":"carol","password":"x"}`))
	w := httptest.NewRecorder()
	h.LDAPLogin(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp loginResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if !containsStr(resp.User.Roles, "viewer") {
		t.Fatalf("expected auto-created viewer role, got %v", resp.User.Roles)
	}
}

func TestLDAPLogin_InvalidCredentials(t *testing.T) {
	st := newLDAPTestStore(t)
	dir := &mockDirectory{err: errors.New("invalid credentials")}
	cfg := &config.Config{JWTSecret: "s", JWTExpiry: "24h"}
	h := &LDAPHandler{Dir: dir, Store: st, Cfg: cfg}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/ldap/login", strings.NewReader(`{"username":"alice","password":"bad"}`))
	w := httptest.NewRecorder()
	h.LDAPLogin(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestLDAPLogin_MissingBody(t *testing.T) {
	st := newLDAPTestStore(t)
	dir := &mockDirectory{id: &auth.LDAPIdentity{DN: "x", Username: "x"}}
	cfg := &config.Config{JWTSecret: "s", JWTExpiry: "24h"}
	h := &LDAPHandler{Dir: dir, Store: st, Cfg: cfg}

	// Missing username/password
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/ldap/login", strings.NewReader(`{"username":"","password":""}`))
	w := httptest.NewRecorder()
	h.LDAPLogin(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func containsStr(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
