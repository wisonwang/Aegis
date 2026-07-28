package proxy

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/wisonwang/aegis/internal/auth"
	"github.com/wisonwang/aegis/internal/datasource"
	"github.com/wisonwang/aegis/internal/store"
)

// newMetricTestStack builds a sqlite datasource with customers(id,name,phone,
// region), grants an analyst SELECT + phone masking + a pii classification on
// phone, so the governed metric path (masking + lineage) can be exercised.
func newMetricTestStack(t *testing.T) *Proxy {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if _, err := raw.Exec(`CREATE TABLE customers (id INTEGER PRIMARY KEY, name TEXT, phone TEXT, region TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO customers VALUES (1,'Alice','13800001234','East'),(2,'Bob','13900005678','East'),(3,'Cara','13700009999','West')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	raw.Close()

	if err := st.CreateDataSource(context.Background(), &store.DataSource{ID: "ds1", Name: "sqlite1", Type: "sqlite", DSN: dbPath}); err != nil {
		t.Fatalf("create ds: %v", err)
	}
	if err := st.CreateRole(&store.Role{ID: "r1", Name: "analyst"}); err != nil {
		t.Fatalf("role: %v", err)
	}
	if err := st.CreateUser(&store.User{ID: "u1", Username: "analyst1", Status: "active"}); err != nil {
		t.Fatalf("user: %v", err)
	}
	if err := st.AddUserRole("u1", "r1"); err != nil {
		t.Fatalf("add role: %v", err)
	}
	if err := st.CreateTablePermission(context.Background(), &store.TablePermission{
		ID: "tp1", RoleID: "r1", DataSourceID: "ds1", TableName: "customers", Ops: "SELECT",
	}); err != nil {
		t.Fatalf("perm: %v", err)
	}
	if err := st.UpsertColumnMask(context.Background(), &store.ColumnMask{
		ID: "m1", RoleID: "r1", DataSourceID: "ds1", TableName: "customers",
		ColumnName: "phone", Strategy: "phone",
	}); err != nil {
		t.Fatalf("mask: %v", err)
	}
	if err := st.UpsertClassification(context.Background(), &store.DataClassification{
		ID: "c1", DataSourceID: "ds1", TableName: "customers", ColumnName: "phone",
		Level: "pii", Tags: `["contact"]`,
	}); err != nil {
		t.Fatalf("class: %v", err)
	}
	return New(st, datasource.NewManager(st))
}

func metricClaims() *auth.Claims {
	return &auth.Claims{UserID: "u1", Username: "analyst1", Roles: []string{"analyst"}}
}

func TestResolveMetricThroughGovernedPath(t *testing.T) {
	p := newMetricTestStack(t)
	// Curated metric: count of customers in a region, parameterized.
	if err := p.store.UpsertMetric(context.Background(), &store.MetricDefinition{
		DataSourceID: "ds1", Name: "customers_in_region",
		Description: "Number of customers in a given region",
		SQLTemplate:  "SELECT count(*) AS cnt FROM customers WHERE region = :region",
		Params: []store.MetricParam{
			{Name: "region", Type: "string", Required: true, Description: "region code"},
		},
		Unit: "count",
	}); err != nil {
		t.Fatalf("upsert metric: %v", err)
	}

	res, err := p.ResolveMetric(context.Background(), "ds1", metricClaims(),
		"customers_in_region", map[string]interface{}{"region": "East"})
	if err != nil {
		t.Fatalf("resolve metric: %v", err)
	}
	if res.SQL != "SELECT count(*) AS cnt FROM customers WHERE region = 'East'" {
		t.Fatalf("unexpected rendered sql: %s", res.SQL)
	}
	if res.QueryResult == nil || len(res.QueryResult.Rows) == 0 {
		t.Fatal("expected a result row")
	}
	cnt, _ := res.QueryResult.Rows[0]["cnt"].(int64)
	if cnt != 2 {
		t.Fatalf("expected cnt=2 for East, got %d", cnt)
	}
	// Lineage: customers is referenced; phone is pii, so HasPII must be true.
	if res.Lineage == nil || len(res.Lineage.Tables) == 0 {
		t.Fatal("expected lineage tables")
	}
	if !res.Lineage.HasPII {
		t.Fatalf("expected HasPII=true because customers.phone is classified pii; got %+v", res.Lineage)
	}
	// Audit recorded the governed execution.
	audits, _, err := p.store.ListAudits(context.Background(), store.AuditFilter{Limit: 5})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(audits) == 0 || audits[0].Status != "ok" {
		t.Fatalf("expected ok audit entry, got %+v", audits)
	}
}

func TestResolveMetricMaskingApplies(t *testing.T) {
	p := newMetricTestStack(t)
	if err := p.store.UpsertMetric(context.Background(), &store.MetricDefinition{
		DataSourceID: "ds1", Name: "customer_contacts",
		Description: "Customer id, name and phone",
		SQLTemplate:  "SELECT id, name, phone FROM customers",
	}); err != nil {
		t.Fatalf("upsert metric: %v", err)
	}
	res, err := p.ResolveMetric(context.Background(), "ds1", metricClaims(), "customer_contacts", nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	phone, ok := res.QueryResult.Rows[0]["phone"].(string)
	if !ok {
		t.Fatalf("phone not masked to string: %v", res.QueryResult.Rows[0]["phone"])
	}
	if phone == "13800001234" {
		t.Fatalf("phone leaked raw value: %s", phone)
	}
	if !res.Lineage.HasPII {
		t.Fatal("expected HasPII from the pii-classified phone column")
	}
}

func TestResolveMetricParamValidation(t *testing.T) {
	p := newMetricTestStack(t)
	if err := p.store.UpsertMetric(context.Background(), &store.MetricDefinition{
		DataSourceID: "ds1", Name: "by_region",
		SQLTemplate: "SELECT count(*) AS cnt FROM customers WHERE region = :region",
		Params: []store.MetricParam{
			{Name: "region", Type: "string", Required: true},
		},
	}); err != nil {
		t.Fatal(err)
	}
	// Missing required param.
	if _, err := p.ResolveMetric(context.Background(), "ds1", metricClaims(), "by_region", nil); err == nil {
		t.Fatal("expected error on missing required param")
	}
	// Bad enum value.
	if err := p.store.UpsertMetric(context.Background(), &store.MetricDefinition{
		DataSourceID: "ds1", Name: "by_tier",
		SQLTemplate: "SELECT count(*) AS cnt FROM customers WHERE region = :tier",
		Params: []store.MetricParam{
			{Name: "tier", Type: "enum", Required: true, Enum: []string{"A", "B"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ResolveMetric(context.Background(), "ds1", metricClaims(), "by_tier", map[string]interface{}{"tier": "Z"}); err == nil {
		t.Fatal("expected error on invalid enum value")
	}
}

func TestResolveMetricInjectionSafe(t *testing.T) {
	// White-box: rendering must double the embedded quote so the value stays
	// inside its string literal. This is the injection-proofing guarantee.
	def := &store.MetricDefinition{
		SQLTemplate: "SELECT count(*) AS cnt FROM customers WHERE region = :region",
		Params:      []store.MetricParam{{Name: "region", Type: "string", Required: true}},
	}
	rendered, err := renderMetricSQL(def, map[string]interface{}{"region": "East' OR '1'='1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, "'East'' OR ''1''=''1'") {
		t.Fatalf("injection not escaped: %s", rendered)
	}

	// End-to-end: even if an attacker reaches execution, the predicate is a
	// single comparison. The governed path either errors on the mangled
	// literal (safe) or returns rows only for the literal string (0), never
	// the full table (3).
	p := newMetricTestStack(t)
	if err := p.store.UpsertMetric(context.Background(), &store.MetricDefinition{
		DataSourceID: "ds1", Name: "safe_region",
		SQLTemplate: "SELECT count(*) AS cnt FROM customers WHERE region = :region",
		Params:       []store.MetricParam{{Name: "region", Type: "string", Required: true}},
	}); err != nil {
		t.Fatal(err)
	}
	res, err := p.ResolveMetric(context.Background(), "ds1", metricClaims(), "safe_region",
		map[string]interface{}{"region": "East' OR '1'='1"})
	if err == nil {
		cnt, _ := res.QueryResult.Rows[0]["cnt"].(int64)
		if cnt != 0 {
			t.Fatalf("injection altered result set: cnt=%d", cnt)
		}
	}
}

func TestResolveMetricUnauthorizedTableDenied(t *testing.T) {
	p := newMetricTestStack(t)
	// Analyst has no permission on a table not granted; metric targeting it
	// must be denied by the governed Execute path.
	if err := p.store.UpsertMetric(context.Background(), &store.MetricDefinition{
		DataSourceID: "ds1", Name: "secrets_leak",
		SQLTemplate:  "SELECT * FROM nonexistent_table",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ResolveMetric(context.Background(), "ds1", metricClaims(), "secrets_leak", nil); err == nil {
		t.Fatal("expected governance denial for unauthorized table")
	}
}
