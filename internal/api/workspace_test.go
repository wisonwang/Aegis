package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wisonwang/aegis/internal/auth"
	"github.com/wisonwang/aegis/internal/config"
	"github.com/wisonwang/aegis/internal/store"
)

// workspaceMux wires the workspace routes exactly as server.go does, including
// the real Authenticate + WorkspaceResolver + RequireAdmin composition, so the
// test verifies the resolver's fail-closed behaviour at the HTTP boundary
// (not just the handler bodies).
func workspaceMux(h *Handler, st *store.Store, cfg *config.Config) http.Handler {
	mux := http.NewServeMux()
	a := func(fn http.HandlerFunc) http.HandlerFunc {
		return Authenticate(cfg, WorkspaceResolver(st, RequireAdmin(fn)))
	}
	mux.HandleFunc("GET /api/v1/workspaces", Authenticate(cfg, h.ListMyWorkspaces))
	mux.HandleFunc("GET /api/v1/datasources", Authenticate(cfg, WorkspaceResolver(st, h.ListDataSources)))
	mux.HandleFunc("POST /admin/api/workspaces", a(h.AdminCreateWorkspace))
	mux.HandleFunc("GET /admin/api/workspaces/{id}", a(h.AdminGetWorkspace))
	mux.HandleFunc("DELETE /admin/api/workspaces/{id}", a(h.AdminDeleteWorkspace))
	return mux
}

func workspaceToken(t *testing.T, cfg *config.Config, uid, name string, roles []string) string {
	t.Helper()
	claims := &auth.Claims{UserID: uid, Username: name, DisplayName: name, Roles: roles}
	tok, err := auth.GenerateToken(claims, cfg.JWTSecret, cfg.JWTExpiry)
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	return tok
}

func workspaceCall(mux http.Handler, method, path, token, body string, headers ...map[string]string) *httptest.ResponseRecorder {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	for _, hm := range headers {
		for k, v := range hm {
			r.Header.Set(k, v)
		}
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

// TestWorkspaceHTTP_AdminCRUDAndResolver exercises the full HTTP wiring: an
// admin can create a workspace (becoming its admin), list it, and the default
// workspace is protected from deletion.
func TestWorkspaceHTTP_AdminCRUDAndResolver(t *testing.T) {
	st := newApprovalTestStore(t) // :memory: store, helper lives in approval_test.go
	cfg := &config.Config{JWTSecret: "test-secret", JWTExpiry: "24h"}
	h := &Handler{Store: st}
	mux := workspaceMux(h, st, cfg)

	adminTok := workspaceToken(t, cfg, "admin", "Admin", []string{"admin"})
	userTok := workspaceToken(t, cfg, "u1", "Alice", []string{"analyst"})

	// Non-admin cannot create a workspace (RequireAdmin gate).
	w := workspaceCall(mux, http.MethodPost, "/admin/api/workspaces", userTok, `{"name":"Acme"}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("non-admin create: expected 403, got %d", w.Code)
	}

	// Admin creates a workspace and becomes its admin.
	w = workspaceCall(mux, http.MethodPost, "/admin/api/workspaces", adminTok, `{"name":"Acme"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("admin create: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.ID == "" || created.Slug != "acme" {
		t.Fatalf("created = %+v", created)
	}

	// Admin can fetch it.
	w = workspaceCall(mux, http.MethodGet, "/admin/api/workspaces/"+created.ID, adminTok, "")
	if w.Code != http.StatusOK {
		t.Fatalf("admin get: expected 200, got %d", w.Code)
	}

	// The creator sees it via their own workspace list.
	w = workspaceCall(mux, http.MethodGet, "/api/v1/workspaces", adminTok, "")
	if w.Code != http.StatusOK {
		t.Fatalf("list mine: expected 200, got %d", w.Code)
	}
	var mine struct {
		Workspaces []store.Workspace `json:"workspaces"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &mine); err != nil {
		t.Fatalf("decode mine: %v", err)
	}
	found := false
	for _, ws := range mine.Workspaces {
		if ws.ID == created.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("creator should see their workspace in /api/v1/workspaces: %+v", mine.Workspaces)
	}

	// The default workspace is protected from deletion.
	w = workspaceCall(mux, http.MethodDelete, "/admin/api/workspaces/"+store.DefaultWorkspaceID, adminTok, "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("delete default: expected 400, got %d", w.Code)
	}
}

// TestWorkspaceHTTP_ResolverFailClosed proves the WorkspaceResolver rejects a
// non-admin who tries to scope a request to a workspace they do not belong to
// (ADR-001 R1: no path to accidentally read another tenant). It also confirms a
// member can scope to their own workspace, and an admin with no hint gets the
// cross-workspace view.
func TestWorkspaceHTTP_ResolverFailClosed(t *testing.T) {
	st := newApprovalTestStore(t)
	cfg := &config.Config{JWTSecret: "test-secret", JWTExpiry: "24h"}
	h := &Handler{Store: st}
	mux := workspaceMux(h, st, cfg)

	// Seed a workspace "acme" and a member "u1".
	if err := st.CreateWorkspace(&store.Workspace{ID: "acme", Name: "Acme", Slug: "acme", Settings: "{}"}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddWorkspaceMember("acme", "u1", store.WsRoleMember, true); err != nil {
		t.Fatal(err)
	}
	memberTok := workspaceToken(t, cfg, "u1", "Member", []string{"analyst"})

	// Member requesting a workspace they are NOT in -> 403 (fail-closed).
	w := workspaceCall(mux, http.MethodGet, "/api/v1/datasources", memberTok, "",
		map[string]string{"X-Workspace-Id": "globex"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("foreign workspace: expected 403, got %d: %s", w.Code, w.Body.String())
	}

	// Member requesting their own workspace -> 200 (scoped, no leak).
	w = workspaceCall(mux, http.MethodGet, "/api/v1/datasources", memberTok, "",
		map[string]string{"X-Workspace-Id": "acme"})
	if w.Code != http.StatusOK {
		t.Fatalf("own workspace: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Member with no hint falls back to their default (acme) -> 200.
	w = workspaceCall(mux, http.MethodGet, "/api/v1/datasources", memberTok, "")
	if w.Code != http.StatusOK {
		t.Fatalf("default fallback: expected 200, got %d", w.Code)
	}

	// Admin with no hint gets the cross-workspace ("*") view -> 200.
	adminTok := workspaceToken(t, cfg, "admin", "Admin", []string{"admin"})
	w = workspaceCall(mux, http.MethodGet, "/api/v1/datasources", adminTok, "")
	if w.Code != http.StatusOK {
		t.Fatalf("admin cross view: expected 200, got %d", w.Code)
	}
}
