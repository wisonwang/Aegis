package enterprise

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/wisonwang/aegis/internal/api"
	"github.com/wisonwang/aegis/internal/auth"
	"github.com/wisonwang/aegis/internal/capabilities"
	"github.com/wisonwang/aegis/internal/config"
	"github.com/wisonwang/aegis/internal/store"
)

func newEnterpriseTestStack(t *testing.T, caps *capabilities.Capabilities) (*gin.Engine, *store.Store, *config.Config) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	cfg := &config.Config{JWTSecret: "test-secret", JWTExpiry: "24h"}
	if err := st.CreateUser(&store.User{ID: "admin", Username: "admin", DisplayName: "Admin", Status: "active"}); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if err := st.CreateUser(&store.User{ID: "u1", Username: "analyst1", DisplayName: "Analyst", Status: "active"}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := st.CreateRole(&store.Role{ID: "r-analyst", Name: "analyst"}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if err := st.AddUserRole("u1", "r-analyst"); err != nil {
		t.Fatalf("bind role: %v", err)
	}
	if err := st.CreateDataSource(context.Background(), &store.DataSource{ID: "ds1", Name: "demo", Type: "sqlite", DSN: ":memory:"}); err != nil {
		t.Fatalf("create datasource: %v", err)
	}
	if err := st.UpsertMetric(context.Background(), &store.MetricDefinition{
		DataSourceID: "ds1",
		Name:         "customer_count",
		Description:  "Customer count",
		SQLTemplate:  "SELECT 1 AS cnt",
		Unit:         "count",
	}); err != nil {
		t.Fatalf("seed metric: %v", err)
	}

	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		ctx := c.Request.Context()
		for _, p := range c.Params {
			ctx = api.WithPathParam(ctx, p.Key, p.Value)
		}
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	h := &api.Handler{Store: st, Cfg: cfg}
	Register(engine, cfg, st, h, caps)
	return engine, st, cfg
}

func testToken(t *testing.T, cfg *config.Config, claims *auth.Claims) string {
	t.Helper()
	tok, err := auth.GenerateToken(claims, cfg.JWTSecret, cfg.JWTExpiry)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	return tok
}

func callJSON(engine http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	return w
}

func TestRegister_DataProductsCapabilityGate(t *testing.T) {
	engine, _, cfg := newEnterpriseTestStack(t, capabilities.Community())
	adminTok := testToken(t, cfg, &auth.Claims{UserID: "admin", Username: "admin", DisplayName: "Admin", Roles: []string{"admin"}})

	w := callJSON(engine, http.MethodGet, "/admin/api/datasources/ds1/metrics", adminTok, "")
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("community gate: expected 402, got %d: %s", w.Code, w.Body.String())
	}
	var denied map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &denied); err != nil {
		t.Fatalf("decode 402 body: %v", err)
	}
	if denied["capability"] != string(capabilities.CapDataProducts) {
		t.Fatalf("unexpected capability body: %+v", denied)
	}

	cfg.Edition = "enterprise"
	entCaps, err := capabilities.New(cfg)
	if err != nil {
		t.Fatalf("enterprise caps: %v", err)
	}
	engine, _, cfg = newEnterpriseTestStack(t, entCaps)
	adminTok = testToken(t, cfg, &auth.Claims{UserID: "admin", Username: "admin", DisplayName: "Admin", Roles: []string{"admin"}})

	w = callJSON(engine, http.MethodGet, "/admin/api/datasources/ds1/metrics", adminTok, "")
	if w.Code != http.StatusOK {
		t.Fatalf("enterprise allow: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var listed struct {
		Metrics []store.MetricDefinition `json:"metrics"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode metric list: %v", err)
	}
	if len(listed.Metrics) != 1 || listed.Metrics[0].Name != "customer_count" {
		t.Fatalf("unexpected metrics: %+v", listed.Metrics)
	}
}

func TestRegister_ApprovalWorkflowCapabilityGate(t *testing.T) {
	body := `{"datasource_id":"ds1","table_name":"orders","role":"analyst","ops":"SELECT","justification":"need access"}`

	engine, _, cfg := newEnterpriseTestStack(t, capabilities.Community())
	userTok := testToken(t, cfg, &auth.Claims{UserID: "u1", Username: "analyst1", DisplayName: "Analyst", Roles: []string{"analyst"}})

	w := callJSON(engine, http.MethodPost, "/admin/api/approvals", userTok, body)
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("community approval gate: expected 402, got %d: %s", w.Code, w.Body.String())
	}
	var denied map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &denied); err != nil {
		t.Fatalf("decode 402 body: %v", err)
	}
	if denied["capability"] != string(capabilities.CapApprovalWorkflow) {
		t.Fatalf("unexpected capability body: %+v", denied)
	}

	cfg.Edition = "enterprise"
	entCaps, err := capabilities.New(cfg)
	if err != nil {
		t.Fatalf("enterprise caps: %v", err)
	}
	engine, _, cfg = newEnterpriseTestStack(t, entCaps)
	userTok = testToken(t, cfg, &auth.Claims{UserID: "u1", Username: "analyst1", DisplayName: "Analyst", Roles: []string{"analyst"}})

	w = callJSON(engine, http.MethodPost, "/admin/api/approvals", userTok, body)
	if w.Code != http.StatusCreated {
		t.Fatalf("enterprise approval allow: expected 201, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRegister_AdminRoutesStillRequireAdmin(t *testing.T) {
	cfg := &config.Config{JWTSecret: "test-secret", JWTExpiry: "24h", Edition: "enterprise"}
	entCaps, err := capabilities.New(cfg)
	if err != nil {
		t.Fatalf("enterprise caps: %v", err)
	}
	engine, _, cfg := newEnterpriseTestStack(t, entCaps)
	userTok := testToken(t, cfg, &auth.Claims{UserID: "u1", Username: "analyst1", DisplayName: "Analyst", Roles: []string{"analyst"}})

	w := callJSON(engine, http.MethodGet, "/admin/api/datasources/ds1/metrics", userTok, "")
	if w.Code != http.StatusForbidden {
		t.Fatalf("admin gate: expected 403, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "admin role required") {
		t.Fatalf("unexpected admin gate body: %s", w.Body.String())
	}
}
