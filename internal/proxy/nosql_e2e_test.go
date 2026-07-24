package proxy

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/wisonwang/aegis/internal/auth"
	"github.com/wisonwang/aegis/internal/config"
	"github.com/wisonwang/aegis/internal/datasource"
	"github.com/wisonwang/aegis/internal/store"
)

// fakeNoSQLConnector is an in-memory stand-in for a Mongo/ES backend so the
// full NoSQL governance + proxy path can be exercised without a live server.
// It honours Mongo projection the way a real backend would, so column
// allow/deny enforcement (applied at query time for NoSQL) is observable.
type fakeNoSQLConnector struct {
	lastWrite json.RawMessage
	lastCount int64
	calls     int
}

func (c *fakeNoSQLConnector) Kind() string { return "mongo" }

func (c *fakeNoSQLConnector) Open(ds *store.DataSource) (datasource.Session, error) {
	return &fakeNoSQLSession{conn: c}, nil
}

type fakeNoSQLSession struct {
	conn *fakeNoSQLConnector
}

func (s *fakeNoSQLSession) Exec(ctx context.Context, payload datasource.QueryPayload) (*datasource.RawResult, int64, error) {
	full := &datasource.RawResult{
		Columns: []string{"status", "amount", "customer", "secret"},
		Rows: []map[string]interface{}{
			{"status": "open", "amount": 1234.5, "customer": "acme", "secret": "x"},
		},
	}
	// Honour a Mongo projection (allowed-cols enforcement happens at the backend).
	var q struct {
		Projection map[string]int `json:"projection"`
	}
	if err := json.Unmarshal(payload.Raw, &q); err == nil && len(q.Projection) > 0 {
		keep := map[string]bool{}
		for k, v := range q.Projection {
			if v == 1 {
				keep[k] = true
			}
		}
		cols := make([]string, 0, len(full.Columns))
		for _, c := range full.Columns {
			if keep[c] {
				cols = append(cols, c)
			}
		}
		rows := make([]map[string]interface{}, 0, len(full.Rows))
		for _, r := range full.Rows {
			nr := map[string]interface{}{}
			for k, v := range r {
				if keep[k] {
					nr[k] = v
				}
			}
			rows = append(rows, nr)
		}
		return &datasource.RawResult{Columns: cols, Rows: rows}, 0, nil
	}
	return full, 0, nil
}

func (s *fakeNoSQLSession) Write(ctx context.Context, payload datasource.WritePayload) (int64, error) {
	s.conn.lastWrite = payload.Raw
	return 1, nil
}

func (s *fakeNoSQLSession) Count(ctx context.Context, payload datasource.QueryPayload) (int64, error) {
	s.conn.lastCount++
	return 5, nil
}

func (s *fakeNoSQLSession) ListCollections(ctx context.Context) ([]string, error) {
	return []string{"orders", "customers"}, nil
}

func (s *fakeNoSQLSession) DescribeCollection(ctx context.Context, name string) ([]datasource.ColumnMeta, error) {
	return []datasource.ColumnMeta{
		{Name: "status", Type: "string"},
		{Name: "amount", Type: "double"},
		{Name: "customer", Type: "string"},
	}, nil
}

func (s *fakeNoSQLSession) Close() error { return nil }

func newNoSQLTestStack(t *testing.T) (*Proxy, *fakeNoSQLConnector) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	ds := &store.DataSource{ID: "ds1", Name: "mongo1", Type: "mongo", DSN: "mongodb://localhost/test"}
	if err := st.CreateDataSource(ds); err != nil {
		t.Fatalf("create ds: %v", err)
	}
	if err := st.CreateRole(&store.Role{ID: "r1", Name: "analyst"}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if err := st.CreateUser(&store.User{ID: "u1", Username: "analyst1", Status: "active"}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := st.AddUserRole("u1", "r1"); err != nil {
		t.Fatalf("add role: %v", err)
	}
	if err := st.CreateTablePermission(&store.TablePermission{
		ID: "tp1", RoleID: "r1", DataSourceID: "ds1", TableName: "orders",
		Ops: "SELECT,INSERT,UPDATE,DELETE",
		AllowedCols: `["status","amount","customer"]`,
	}); err != nil {
		t.Fatalf("perm: %v", err)
	}
	if err := st.UpsertColumnMask(&store.ColumnMask{
		ID: "m1", RoleID: "r1", DataSourceID: "ds1", TableName: "orders",
		ColumnName: "amount", Strategy: "partial", Keep: 2,
	}); err != nil {
		t.Fatalf("mask: %v", err)
	}
	mgr := datasource.NewManager(st)
	conn := &fakeNoSQLConnector{}
	mgr.SetConnectorFunc(func(t string) (datasource.Connector, error) { return conn, nil })
	p := New(st, mgr)
	return p, conn
}

func nosqlClaims() *auth.Claims {
	return &auth.Claims{UserID: "u1", Roles: []string{"analyst"}}
}

func TestNoSQLReadGovernanceAndMasking(t *testing.T) {
	p, _ := newNoSQLTestStack(t)
	res, err := p.Execute(context.Background(), "ds1", nosqlClaims(),
		`{"collection":"orders","filter":{"status":"open"}}`)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, c := range res.Columns {
		if c == "secret" {
			t.Fatalf("denied column 'secret' leaked into result: %v", res.Columns)
		}
	}
	for _, want := range []string{"status", "amount", "customer"} {
		found := false
		for _, c := range res.Columns {
			if c == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected column %q in %v", want, res.Columns)
		}
	}
	// amount must be masked: a string, not the raw number 1234.5.
	amt, ok := res.Rows[0]["amount"].(string)
	if !ok || amt == "1234.5" {
		t.Fatalf("amount not masked: %v (%T)", res.Rows[0]["amount"], res.Rows[0]["amount"])
	}
}

func TestNoSQLWriteColumnProjection(t *testing.T) {
	p, conn := newNoSQLTestStack(t)
	res, err := p.Execute(context.Background(), "ds1", nosqlClaims(),
		`{"op":"insert","collection":"orders","document":{"status":"open","amount":99,"customer":"acme","secret":"leak"}}`)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if res.AffectedRows != 1 {
		t.Fatalf("affected = %d, want 1", res.AffectedRows)
	}
	var w map[string]json.RawMessage
	if err := json.Unmarshal(conn.lastWrite, &w); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(w["document"], &doc); err != nil {
		t.Fatalf("document: %v", err)
	}
	if _, ok := doc["secret"]; ok {
		t.Fatalf("denied column 'secret' was sent to backend: %v", doc)
	}
	for _, keep := range []string{"status", "amount", "customer"} {
		if _, ok := doc[keep]; !ok {
			t.Fatalf("allowed column %q dropped: %v", keep, doc)
		}
	}
}

func TestNoSQLWriteNoWhereDenied(t *testing.T) {
	p, _ := newNoSQLTestStack(t)
	// Enable behavior limits with no-where writes disallowed.
	p.SetGuard(NewGuard(config.Limits{RatePerMin: 1000, AllowNoWhere: false}))
	_, err := p.Execute(context.Background(), "ds1", nosqlClaims(),
		`{"op":"delete","collection":"orders"}`)
	if err == nil {
		t.Fatal("expected denial for no-where delete")
	}
	if !strings.Contains(err.Error(), "row-bounding") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNoSQLCatalogSurfacesCollections(t *testing.T) {
	p, _ := newNoSQLTestStack(t)
	schema, err := p.Catalog(context.Background(), "ds1", nosqlClaims())
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	var orders *SemanticTable
	for i := range schema.Tables {
		if schema.Tables[i].Name == "orders" {
			orders = &schema.Tables[i]
		}
	}
	if orders == nil {
		t.Fatalf("orders not in catalog: %v", schema.Tables)
	}
	var hasAmount, hasMask, hasSecret bool
	for _, c := range orders.Columns {
		if c.Name == "amount" {
			hasAmount = true
			if c.Masked == "partial" {
				hasMask = true
			}
		}
		if c.Name == "secret" {
			hasSecret = true
		}
	}
	if !hasAmount {
		t.Fatal("amount column missing")
	}
	if !hasMask {
		t.Fatal("amount should be marked masked=partial")
	}
	if hasSecret {
		t.Fatal("secret should be excluded from catalog (allowed_cols)")
	}
}
