package proxy

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/wisonwang/aegis/internal/auth"
	"github.com/wisonwang/aegis/internal/datasource"
	"github.com/wisonwang/aegis/internal/nl2sql"
	"github.com/wisonwang/aegis/internal/store"
)

// newNL2SQLTestStack builds a sqlite-backed datasource with a `customers`
// table, grants an analyst SELECT, and installs a deterministic StubGenerator
// so the NL2SQL -> governed-execution loop can be exercised without an LLM.
func newNL2SQLTestStack(t *testing.T) *Proxy {
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
	if _, err := raw.Exec(`CREATE TABLE customers (id INTEGER PRIMARY KEY, name TEXT, phone TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO customers VALUES (1,'Alice','13800001234'),(2,'Bob','13900005678')`); err != nil {
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
	mgr := datasource.NewManager(st)
	p := New(st, mgr)
	p.SetNL2SQL(&nl2sql.StubGenerator{
		ByKeyword: map[string]string{"customers": "SELECT id, name, phone FROM customers"},
	})
	return p
}

func nl2sqlClaims() *auth.Claims {
	return &auth.Claims{UserID: "u1", Username: "analyst1", Roles: []string{"analyst"}}
}

func TestNL2SQLRoutesThroughGovernedPath(t *testing.T) {
	p := newNL2SQLTestStack(t)
	res, gen, err := p.NL2SQL(context.Background(), "ds1", nl2sqlClaims(),
		"list all customers", "")
	if err != nil {
		t.Fatalf("nl2sql: %v", err)
	}
	if gen == nil || gen.SQL != "SELECT id, name, phone FROM customers" {
		t.Fatalf("unexpected generated sql: %+v", gen)
	}
	// Governance: only id/name/phone (granted + masked), nothing leaked.
	wantCols := map[string]bool{"id": false, "name": false, "phone": false}
	for _, c := range res.Columns {
		if _, ok := wantCols[c]; !ok {
			t.Fatalf("unexpected column %q in %v", c, res.Columns)
		}
		wantCols[c] = true
	}
	for c, found := range wantCols {
		if !found {
			t.Fatalf("expected column %q missing", c)
		}
	}
	// Masking: phone must be a string, not the raw number.
	phone, ok := res.Rows[0]["phone"].(string)
	if !ok {
		t.Fatalf("phone not masked to string: %v (%T)", res.Rows[0]["phone"], res.Rows[0]["phone"])
	}
	if phone == "13800001234" {
		t.Fatalf("phone leaked raw value: %s", phone)
	}
	// Audit: an ok entry should record the generated SQL.
	audits, _, err := p.store.ListAudits(context.Background(), store.AuditFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(audits) == 0 {
		t.Fatal("expected an audit entry for the NL2SQL execution")
	}
	if audits[0].Status != "ok" {
		t.Fatalf("audit status = %s", audits[0].Status)
	}
}

func TestNL2SQLNotConfigured(t *testing.T) {
	st, _ := store.Open(":memory:")
	if err := st.CreateDataSource(context.Background(), &store.DataSource{ID: "ds1", Name: "x", Type: "sqlite", DSN: ":memory:"}); err != nil {
		t.Fatal(err)
	}
	p := New(st, datasource.NewManager(st)) // no generator installed
	res, gen, err := p.NL2SQL(context.Background(), "ds1", nl2sqlClaims(), "q", "")
	if err == nil {
		t.Fatal("expected error when NL2SQL not configured")
	}
	if gen != nil || res != nil {
		t.Fatal("gen and res must be nil when not configured (signals 5xx, not 4xx)")
	}
}

func TestNL2SQLGenerationFailureIs5xx(t *testing.T) {
	// Stub with no default and no keyword match -> generation error (gen nil).
	st, _ := store.Open(":memory:")
	if err := st.CreateDataSource(context.Background(), &store.DataSource{ID: "ds1", Name: "x", Type: "sqlite", DSN: ":memory:"}); err != nil {
		t.Fatal(err)
	}
	p := New(st, datasource.NewManager(st))
	p.SetNL2SQL(&nl2sql.StubGenerator{}) // empty: never produces SQL
	res, gen, err := p.NL2SQL(context.Background(), "ds1", nl2sqlClaims(), "unsupported question", "")
	if err == nil {
		t.Fatal("expected generation failure")
	}
	if gen != nil || res != nil {
		t.Fatal("gen/res must be nil on generation failure (=> 502, not 403)")
	}
}
