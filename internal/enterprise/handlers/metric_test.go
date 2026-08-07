package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/wisonwang/aegis/internal/auth"
	"github.com/wisonwang/aegis/internal/datasource"
	"github.com/wisonwang/aegis/internal/proxy"
	"github.com/wisonwang/aegis/internal/requestctx"
	"github.com/wisonwang/aegis/internal/store"
)

func newMetricHandler(t *testing.T) (*Handler, *store.Store) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "metric.db")
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if _, err := raw.Exec(`CREATE TABLE customers (id INTEGER PRIMARY KEY, name TEXT, region TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO customers VALUES (1,'Alice','East'),(2,'Bob','East'),(3,'Cara','West')`); err != nil {
		t.Fatalf("insert rows: %v", err)
	}
	_ = raw.Close()

	if err := st.CreateDataSource(context.Background(), &store.DataSource{ID: "ds1", Name: "demo", Type: "sqlite", DSN: dbPath}); err != nil {
		t.Fatalf("create ds: %v", err)
	}
	if err := st.CreateRole(&store.Role{ID: "r-analyst", Name: "analyst"}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if err := st.CreateUser(&store.User{ID: "u1", Username: "analyst1", Status: "active"}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := st.AddUserRole("u1", "r-analyst"); err != nil {
		t.Fatalf("add user role: %v", err)
	}
	if err := st.CreateTablePermission(context.Background(), &store.TablePermission{
		ID: "tp1", RoleID: "r-analyst", DataSourceID: "ds1", TableName: "customers", Ops: "SELECT",
	}); err != nil {
		t.Fatalf("create perm: %v", err)
	}

	px := proxy.New(st, datasource.NewManager(st))
	return New(st, px), st
}

func metricReq(method, path string, body string, claims *auth.Claims) *http.Request {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	if claims != nil {
		req = req.WithContext(requestctx.WithClaims(req.Context(), claims))
	}
	return req
}

func metricReqWithPath(method, path, body string, claims *auth.Claims, params map[string]string) *http.Request {
	req := metricReq(method, path, body, claims)
	ctx := req.Context()
	for k, v := range params {
		ctx = requestctx.WithPathParam(ctx, k, v)
	}
	return req.WithContext(ctx)
}

func TestMetricHandlers_AdminUpsertListRunDelete(t *testing.T) {
	h, st := newMetricHandler(t)
	adminClaims := &auth.Claims{UserID: "admin", Username: "admin", DisplayName: "Admin", Roles: []string{"admin"}}
	userClaims := &auth.Claims{UserID: "u1", Username: "analyst1", DisplayName: "Analyst", Roles: []string{"analyst"}}

	upsertBody := `{
		"name":"customers_in_region",
		"description":"Count customers by region",
		"sql_template":"SELECT count(*) AS cnt FROM customers WHERE region = :region",
		"params":[{"name":"region","type":"string","required":true}],
		"unit":"count"
	}`
	req := metricReqWithPath(http.MethodPost, "/admin/api/datasources/ds1/metrics", upsertBody, adminClaims, map[string]string{"id": "ds1"})
	w := httptest.NewRecorder()
	h.AdminUpsertMetric(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("upsert: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	req = metricReqWithPath(http.MethodGet, "/admin/api/datasources/ds1/metrics", "", adminClaims, map[string]string{"id": "ds1"})
	w = httptest.NewRecorder()
	h.AdminListMetrics(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("admin list: expected 200, got %d", w.Code)
	}
	var listed struct {
		Metrics []store.MetricDefinition `json:"metrics"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed.Metrics) != 1 || listed.Metrics[0].Name != "customers_in_region" {
		t.Fatalf("metrics = %+v", listed.Metrics)
	}

	req = metricReqWithPath(http.MethodGet, "/api/v1/datasources/ds1/metrics", "", userClaims, map[string]string{"id": "ds1"})
	w = httptest.NewRecorder()
	h.ListMetrics(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("user list: expected 200, got %d", w.Code)
	}

	runBody := `{"params":{"region":"East"}}`
	req = metricReqWithPath(http.MethodPost, "/api/v1/datasources/ds1/metrics/customers_in_region/run", runBody, userClaims, map[string]string{"id": "ds1", "name": "customers_in_region"})
	w = httptest.NewRecorder()
	h.RunMetric(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("run: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var runRes struct {
		SQL         string             `json:"sql"`
		SessionID   string             `json:"session_id"`
		QueryResult *proxy.QueryResult `json:"query_result"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &runRes); err != nil {
		t.Fatalf("decode run: %v", err)
	}
	if runRes.SessionID == "" {
		t.Fatal("expected generated session_id")
	}
	if runRes.SQL != "SELECT count(*) AS cnt FROM customers WHERE region = 'East'" {
		t.Fatalf("unexpected sql: %s", runRes.SQL)
	}
	if runRes.QueryResult == nil || len(runRes.QueryResult.Rows) != 1 {
		t.Fatalf("unexpected query result: %+v", runRes.QueryResult)
	}

	metricID := listed.Metrics[0].ID
	req = metricReqWithPath(http.MethodDelete, "/admin/api/datasources/ds1/metrics/"+metricID, "", adminClaims, map[string]string{"id": "ds1", "mid": metricID})
	w = httptest.NewRecorder()
	h.AdminDeleteMetric(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if metrics, err := st.ListMetrics(context.Background(), "ds1"); err != nil || len(metrics) != 0 {
		t.Fatalf("metrics after delete = %+v, err=%v", metrics, err)
	}
}
