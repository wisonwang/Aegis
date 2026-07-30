package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wisonwang/aegis/internal/auth"
	"github.com/wisonwang/aegis/internal/requestctx"
	"github.com/wisonwang/aegis/internal/store"
)

func newTestHandler(t *testing.T) (*Handler, *store.Store) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return New(st, nil), st
}

func withClaims(r *http.Request, c *auth.Claims) *http.Request {
	return r.WithContext(requestctx.WithClaims(r.Context(), c))
}

func withPath(r *http.Request, name, value string) *http.Request {
	return r.WithContext(requestctx.WithPathParam(r.Context(), name, value))
}

func TestApprovalWorkflow_ClosedLoop(t *testing.T) {
	h, st := newTestHandler(t)
	_ = st.CreateRole(&store.Role{ID: "r-analyst", Name: "analyst"})
	_ = st.CreateDataSource(context.Background(), &store.DataSource{ID: "ds1", Name: "demo", Type: "sqlite", DSN: ":memory:"})

	userClaims := &auth.Claims{UserID: "u1", Username: "alice", DisplayName: "Alice", Roles: []string{"analyst"}}
	adminClaims := &auth.Claims{UserID: "admin", Username: "admin", DisplayName: "Admin", Roles: []string{"admin"}}

	submitBody := `{"datasource_id":"ds1","table_name":"orders","role":"analyst","ops":"SELECT,INSERT","justification":"need write"}`
	req := httptest.NewRequest(http.MethodPost, "/admin/api/approvals", strings.NewReader(submitBody))
	req = withClaims(req, userClaims)
	w := httptest.NewRecorder()
	h.UserSubmitApproval(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("submit: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var created struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode submit: %v", err)
	}
	if created.ID == "" || created.Status != store.ApprovalPending {
		t.Fatalf("created = %+v", created)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/me/approvals", nil)
	req = withClaims(req, userClaims)
	w = httptest.NewRecorder()
	h.UserListMyApprovals(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list mine: expected 200, got %d", w.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/admin/api/approvals/"+created.ID+"/approve", nil)
	req = withClaims(withPath(req, "id", created.ID), adminClaims)
	w = httptest.NewRecorder()
	h.AdminApproveApproval(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("approve: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	perms, err := st.ListTablePermissions(context.Background(), "r-analyst", "ds1", "orders")
	if err != nil {
		t.Fatalf("list perms: %v", err)
	}
	if len(perms) != 1 {
		t.Fatalf("expected 1 grant, got %d", len(perms))
	}
	if !strings.Contains(perms[0].Ops, "SELECT") || !strings.Contains(perms[0].Ops, "INSERT") {
		t.Fatalf("grant ops = %q", perms[0].Ops)
	}

	req = httptest.NewRequest(http.MethodPost, "/admin/api/approvals/"+created.ID+"/revoke", nil)
	req = withClaims(withPath(req, "id", created.ID), adminClaims)
	w = httptest.NewRecorder()
	h.AdminRevokeApproval(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("revoke: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	perms, err = st.ListTablePermissions(context.Background(), "r-analyst", "ds1", "orders")
	if err != nil {
		t.Fatalf("list perms after revoke: %v", err)
	}
	if len(perms) != 0 {
		t.Fatalf("expected grant removed, got %d", len(perms))
	}
}
