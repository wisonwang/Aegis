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
	"context"
)

func newApprovalTestStore(t *testing.T) *store.Store {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// approvalMux wires the approval routes exactly as server.go does, so the test
// exercises the real Authenticate + RequireAdmin middleware (not just the
// handler bodies).
func approvalMux(h *Handler, cfg *config.Config) http.Handler {
	mux := http.NewServeMux()
	a := func(fn http.HandlerFunc) http.HandlerFunc { return Authenticate(cfg, RequireAdmin(fn)) }
	mux.HandleFunc("POST /admin/api/approvals", Authenticate(cfg, h.UserSubmitApproval))
	mux.HandleFunc("GET /admin/api/approvals", a(h.AdminListApprovals))
	mux.HandleFunc("POST /admin/api/approvals/{id}/approve", a(h.AdminApproveApproval))
	mux.HandleFunc("POST /admin/api/approvals/{id}/reject", a(h.AdminRejectApproval))
	mux.HandleFunc("POST /admin/api/approvals/{id}/revoke", a(h.AdminRevokeApproval))
	mux.HandleFunc("GET /api/v1/me/approvals", Authenticate(cfg, h.UserListMyApprovals))
	return mux
}

func approvalToken(t *testing.T, cfg *config.Config, uid, name string, roles []string) string {
	claims := &auth.Claims{UserID: uid, Username: name, DisplayName: name, Roles: roles}
	tok, err := auth.GenerateToken(claims, cfg.JWTSecret, cfg.JWTExpiry)
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	return tok
}

func approvalCall(mux http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func TestApprovalWorkflow_ClosedLoop(t *testing.T) {
	st := newApprovalTestStore(t)
	_ = st.CreateRole(&store.Role{ID: "r-analyst", Name: "analyst"})
	_ = st.CreateDataSource(context.Background(), &store.DataSource{ID: "ds1", Name: "demo", Type: "sqlite", DSN: ":memory:"})
	cfg := &config.Config{JWTSecret: "test-secret", JWTExpiry: "24h"}
	h := &Handler{Store: st}
	mux := approvalMux(h, cfg)

	adminTok := approvalToken(t, cfg, "admin", "Admin", []string{"admin"})
	userTok := approvalToken(t, cfg, "u1", "Alice", []string{"analyst"})

	// 1) Any authenticated user may submit.
	body := `{"datasource_id":"ds1","table_name":"orders","role":"analyst","ops":"SELECT,INSERT","justification":"need write"}`
	w := approvalCall(mux, http.MethodPost, "/admin/api/approvals", userTok, body)
	if w.Code != http.StatusCreated {
		t.Fatalf("submit: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.ID == "" || created.Status != store.ApprovalPending {
		t.Fatalf("created = %+v", created)
	}

	// 2) Non-admin cannot list the admin queue (gate enforced).
	w = approvalCall(mux, http.MethodGet, "/admin/api/approvals", userTok, "")
	if w.Code != http.StatusForbidden {
		t.Fatalf("list-gate: expected 403, got %d", w.Code)
	}

	// 3) Admin sees the pending request.
	w = approvalCall(mux, http.MethodGet, "/admin/api/approvals?status=pending", adminTok, "")
	if w.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d", w.Code)
	}
	var list struct {
		Approvals []store.ApprovalRequest `json:"approvals"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Approvals) != 1 || list.Approvals[0].Status != store.ApprovalPending {
		t.Fatalf("list = %+v", list.Approvals)
	}

	// 4) Applicant can track their own request.
	w = approvalCall(mux, http.MethodGet, "/api/v1/me/approvals", userTok, "")
	if w.Code != http.StatusOK {
		t.Fatalf("me: expected 200, got %d", w.Code)
	}

	// 5) Approve creates the actual role->table grant.
	w = approvalCall(mux, http.MethodPost, "/admin/api/approvals/"+created.ID+"/approve", adminTok, "")
	if w.Code != http.StatusOK {
		t.Fatalf("approve: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var appr struct {
		Status         string `json:"status"`
		GrantedPermID  string `json:"granted_perm_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &appr); err != nil {
		t.Fatalf("decode approve: %v", err)
	}
	if appr.GrantedPermID == "" {
		t.Fatal("expected granted_perm_id")
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

	// 6) Re-approving a non-pending request is rejected (idempotency guard).
	w = approvalCall(mux, http.MethodPost, "/admin/api/approvals/"+created.ID+"/approve", adminTok, "")
	if w.Code != http.StatusConflict {
		t.Fatalf("re-approve: expected 409, got %d", w.Code)
	}

	// 7) Revoke removes the grant and closes the loop.
	w = approvalCall(mux, http.MethodPost, "/admin/api/approvals/"+created.ID+"/revoke", adminTok, "")
	if w.Code != http.StatusOK {
		t.Fatalf("revoke: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	perms, _ = st.ListTablePermissions(context.Background(), "r-analyst", "ds1", "orders")
	if len(perms) != 0 {
		t.Fatalf("expected grant removed, got %d", len(perms))
	}
	ar, _ := st.GetApprovalRequest(created.ID)
	if ar.Status != store.ApprovalRevoked {
		t.Fatalf("status = %q", ar.Status)
	}
}

func TestApprovalWorkflow_RejectCreatesNoGrant(t *testing.T) {
	st := newApprovalTestStore(t)
	_ = st.CreateRole(&store.Role{ID: "r-analyst", Name: "analyst"})
	_ = st.CreateDataSource(context.Background(), &store.DataSource{ID: "ds1", Name: "demo", Type: "sqlite", DSN: ":memory:"})
	cfg := &config.Config{JWTSecret: "test-secret", JWTExpiry: "24h"}
	h := &Handler{Store: st}
	mux := approvalMux(h, cfg)

	adminTok := approvalToken(t, cfg, "admin", "Admin", []string{"admin"})
	userTok := approvalToken(t, cfg, "u1", "Alice", []string{"analyst"})

	w := approvalCall(mux, http.MethodPost, "/admin/api/approvals",
		userTok, `{"datasource_id":"ds1","table_name":"customers","role":"analyst","ops":"SELECT","justification":"x"}`)
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	w = approvalCall(mux, http.MethodPost, "/admin/api/approvals/"+created.ID+"/reject", adminTok, "")
	if w.Code != http.StatusOK {
		t.Fatalf("reject: expected 200, got %d", w.Code)
	}
	perms, _ := st.ListTablePermissions(context.Background(), "r-analyst", "ds1", "customers")
	if len(perms) != 0 {
		t.Fatalf("reject must not create a grant, got %d", len(perms))
	}
	ar, _ := st.GetApprovalRequest(created.ID)
	if ar.Status != store.ApprovalRejected {
		t.Fatalf("status = %q", ar.Status)
	}
}

func TestApprovalWorkflow_Validation(t *testing.T) {
	st := newApprovalTestStore(t)
	_ = st.CreateRole(&store.Role{ID: "r-analyst", Name: "analyst"})
	_ = st.CreateDataSource(context.Background(), &store.DataSource{ID: "ds1", Name: "demo", Type: "sqlite", DSN: ":memory:"})
	cfg := &config.Config{JWTSecret: "test-secret", JWTExpiry: "24h"}
	h := &Handler{Store: st}
	mux := approvalMux(h, cfg)
	userTok := approvalToken(t, cfg, "u1", "Alice", []string{"analyst"})

	// Invalid ops must be rejected before any DB write.
	w := approvalCall(mux, http.MethodPost, "/admin/api/approvals",
		userTok, `{"datasource_id":"ds1","table_name":"orders","role":"analyst","ops":"DROP","justification":"x"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid ops: expected 400, got %d", w.Code)
	}

	// Unknown datasource must 404.
	w = approvalCall(mux, http.MethodPost, "/admin/api/approvals",
		userTok, `{"datasource_id":"nope","table_name":"orders","role":"analyst","ops":"SELECT"}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown ds: expected 404, got %d", w.Code)
	}
}
