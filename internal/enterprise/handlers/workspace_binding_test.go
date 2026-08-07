package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wisonwang/aegis/internal/auth"
	"github.com/wisonwang/aegis/internal/store"
)

// withStar scopes the request to the platform-admin all-workspaces ("*") view.
// This reproduces the real ADR-0007 bug condition: in the "*" view the request
// context resolves WriteWorkspace to "default", so any handler that passed the
// bare r.Context() to the store silently orphaned the object into "default"
// instead of the datasource's own workspace.
func withStar(req *http.Request) *http.Request {
	return req.WithContext(store.WithWorkspace(req.Context(), store.WorkspaceAll))
}

// TestWorkspaceBinding_WritePathsBindToDataSourceWorkspace proves that writes
// performed from the "*" view bind to the owning datasource's workspace, not
// to "default". Covers GAP-1..GAP-6 of the multi-tenant audit.
func TestWorkspaceBinding_WritePathsBindToDataSourceWorkspace(t *testing.T) {
	h, st := newMetricHandler(t)
	const otherWS = "ws-other"

	// datasource in a non-default workspace; CreateDataSource stamps via WriteWorkspace(ctx)
	if err := st.CreateWorkspace(&store.Workspace{ID: otherWS, Name: "Other WS", Slug: "other-ws"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := st.CreateDataSource(store.WithWorkspace(context.Background(), otherWS),
		&store.DataSource{ID: "ds2", Name: "demo2", Type: "sqlite", DSN: ":memory:"}); err != nil {
		t.Fatalf("create ds2: %v", err)
	}
	admin := &auth.Claims{UserID: "admin", Username: "admin", Roles: []string{"admin"}}
	analyst := &auth.Claims{UserID: "u1", Username: "analyst1", Roles: []string{"analyst"}}

	// GAP-5: metric upsert must bind to ds2's workspace, not default.
	upBody := `{"name":"m1","sql_template":"SELECT 1","params":[]}`
	req := withStar(metricReqWithPath(http.MethodPost, "/admin/api/datasources/ds2/metrics", upBody, admin, map[string]string{"id": "ds2"}))
	w := httptest.NewRecorder()
	h.AdminUpsertMetric(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("upsert metric: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	metrics, err := st.ListMetrics(store.WithWorkspace(context.Background(), "*"), "ds2")
	if err != nil || len(metrics) != 1 {
		t.Fatalf("list metrics: %v len=%d", err, len(metrics))
	}
	if metrics[0].WorkspaceID != otherWS {
		t.Fatalf("GAP-5: metric workspace_id = %q, want %q", metrics[0].WorkspaceID, otherWS)
	}
	metricID := metrics[0].ID

	// GAP-4: folder create with workspace_id in body binds to that workspace.
	fBody := `{"name":"f1","workspace_id":"ws-other"}`
	req = withStar(metricReqWithPath(http.MethodPost, "/admin/api/folders", fBody, admin, nil))
	w = httptest.NewRecorder()
	h.AdminCreateFolder(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create folder: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var fRes struct{ ID string `json:"id"` }
	_ = json.Unmarshal(w.Body.Bytes(), &fRes)
	f, err := st.GetFolder(store.WithWorkspace(context.Background(), "*"), fRes.ID)
	if err != nil || f == nil {
		t.Fatalf("get folder: %v", err)
	}
	if f.WorkspaceID != otherWS {
		t.Fatalf("GAP-4: folder workspace_id = %q, want %q", f.WorkspaceID, otherWS)
	}

	// GAP-2: approval request submit binds to ds2's workspace.
	apBody := `{"datasource_id":"ds2","table_name":"customers","role":"analyst","ops":"SELECT","justification":"need it"}`
	req = withStar(metricReqWithPath(http.MethodPost, "/api/v1/me/approvals", apBody, analyst, nil))
	w = httptest.NewRecorder()
	h.UserSubmitApproval(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("submit approval: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var apRes struct{ ID string `json:"id"` }
	_ = json.Unmarshal(w.Body.Bytes(), &apRes)
	ar, err := st.GetApprovalRequest(apRes.ID)
	if err != nil || ar == nil {
		t.Fatalf("get approval: %v", err)
	}
	if ar.WorkspaceID != otherWS {
		t.Fatalf("GAP-2: approval workspace_id = %q, want %q", ar.WorkspaceID, otherWS)
	}

	// GAP-3: approving the request creates a table permission bound to ds2's workspace.
	req = withStar(metricReqWithPath(http.MethodPost, "/admin/api/approvals/"+apRes.ID+"/approve", "", admin, map[string]string{"id": apRes.ID}))
	w = httptest.NewRecorder()
	h.AdminApproveApproval(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("approve: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	perms, err := st.ListTablePermissions(store.WithWorkspace(context.Background(), "*"), "r-analyst", "ds2", "customers")
	if err != nil {
		t.Fatalf("list perms: %v", err)
	}
	if len(perms) != 1 {
		t.Fatalf("expected 1 permission, got %d", len(perms))
	}
	if perms[0].WorkspaceID != otherWS {
		t.Fatalf("GAP-3: permission workspace_id = %q, want %q", perms[0].WorkspaceID, otherWS)
	}

	// GAP-6: DeleteMetric from a non-owning workspace context must NOT delete.
	req = withStar(metricReqWithPath(http.MethodDelete, "/admin/api/datasources/ds2/metrics/"+metricID, "", admin, map[string]string{"id": "ds2", "mid": metricID}))
	req = req.WithContext(store.WithWorkspace(req.Context(), "default")) // scope to default, not ws-other
	w = httptest.NewRecorder()
	h.AdminDeleteMetric(w, req)
	if w.Code == http.StatusOK {
		t.Fatalf("GAP-6: delete from non-owning workspace unexpectedly succeeded (code %d)", w.Code)
	}
	// From a crossing (platform-admin) context it does delete.
	req = withStar(metricReqWithPath(http.MethodDelete, "/admin/api/datasources/ds2/metrics/"+metricID, "", admin, map[string]string{"id": "ds2", "mid": metricID}))
	w = httptest.NewRecorder()
	h.AdminDeleteMetric(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GAP-6: delete from crossing context expected 200, got %d: %s", w.Code, w.Body.String())
	}
	left, _ := st.ListMetrics(store.WithWorkspace(context.Background(), "*"), "ds2")
	if len(left) != 0 {
		t.Fatalf("GAP-6: expected metric deleted, left=%d", len(left))
	}
}
