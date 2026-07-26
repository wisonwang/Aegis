package proxy

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/wisonwang/aegis/internal/auth"
	"github.com/wisonwang/aegis/internal/datasource"
	"github.com/wisonwang/aegis/internal/store"
)

// TestPostgresE2E is a real PostgreSQL end-to-end verification of the Aegis
// governance chain. It requires a live Postgres reachable via AEGIS_TEST_PG_DSN
// (lib/pq DSN) and is skipped otherwise so the suite stays hermetic in CI.
//
// Demonstrated data plane (seeded by the caller / run script):
//
//	orders(tenant_id, customer, amount, status)      -- 2 acme + 1 initech rows
//	customers(tenant_id, name, phone, email)         -- 2 acme + 1 initech rows (PII)
//	salary(tenant_id, name, amount)                  -- UNGRANTED (default-deny target)
//
// Control plane (this test):
//   - role "admin"  -> superuser bypass
//   - role "analyst" -> SELECT on orders/customers, row policy tenant_id = :tenant,
//     column masks phone/email on customers
func TestPostgresE2E(t *testing.T) {
	dsn := os.Getenv("AEGIS_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("set AEGIS_TEST_PG_DSN (lib/pq DSN) to run PostgreSQL end-to-end verification")
	}

	// --- control-plane store (temp sqlite) ---
	dir, err := os.MkdirTemp("", "aegis-pg-e2e")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	defer os.RemoveAll(dir)
	cpPath := filepath.Join(dir, "control.db")
	st, err := store.Open(cpPath)
	if err != nil {
		t.Fatalf("open control store: %v", err)
	}
	defer st.Close()

	const dsID = "pg-demo"
	if err := st.CreateDataSource(&store.DataSource{ID: dsID, Name: "pg-demo", Type: "postgres", DSN: dsn}); err != nil {
		t.Fatalf("create datasource: %v", err)
	}

	// admin (superuser bypass)
	adminRole := &store.Role{Name: "admin"}
	if err := st.CreateRole(adminRole); err != nil {
		t.Fatalf("create admin role: %v", err)
	}
	const adminID = "u-admin"
	if err := st.CreateUser(&store.User{ID: adminID, Username: "admin", Attributes: "{}"}); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if err := st.AddUserRole(adminID, adminRole.ID); err != nil {
		t.Fatalf("add admin role: %v", err)
	}

	// analyst (governed)
	analystRole := &store.Role{Name: "analyst"}
	if err := st.CreateRole(analystRole); err != nil {
		t.Fatalf("create analyst role: %v", err)
	}
	const analystID = "u-analyst"
	if err := st.CreateUser(&store.User{ID: analystID, Username: "analyst", Attributes: `{"tenant":"acme"}`}); err != nil {
		t.Fatalf("create analyst: %v", err)
	}
	if err := st.AddUserRole(analystID, analystRole.ID); err != nil {
		t.Fatalf("add analyst role: %v", err)
	}
	for _, tbl := range []string{"orders", "customers"} {
		if err := st.CreateTablePermission(&store.TablePermission{
			RoleID: analystRole.ID, DataSourceID: dsID, TableName: tbl, Ops: "SELECT",
		}); err != nil {
			t.Fatalf("grant %s: %v", tbl, err)
		}
		// row policy scoped to the principal's tenant attribute
		if err := st.CreateRowPolicy(&store.RowPolicy{
			RoleID: analystRole.ID, DataSourceID: dsID, TableName: tbl,
			Predicate: "tenant_id = :tenant", Priority: 10,
		}); err != nil {
			t.Fatalf("row policy %s: %v", tbl, err)
		}
	}
	// dynamic value masking on PII columns
	if err := st.UpsertColumnMask(&store.ColumnMask{RoleID: analystRole.ID, DataSourceID: dsID, TableName: "customers", ColumnName: "phone", Strategy: "phone"}); err != nil {
		t.Fatalf("mask phone: %v", err)
	}
	if err := st.UpsertColumnMask(&store.ColumnMask{RoleID: analystRole.ID, DataSourceID: dsID, TableName: "customers", ColumnName: "email", Strategy: "email"}); err != nil {
		t.Fatalf("mask email: %v", err)
	}

	dm := datasource.NewManager(st)
	px := New(st, dm)
	SetMaskKey("aegis-pg-e2e-test-secret") // deterministic keyed masking for assertions

	adminClaims := &auth.Claims{UserID: adminID, Username: "admin", Roles: []string{"admin"}, Attributes: map[string]string{}}
	analystClaims := &auth.Claims{UserID: analystID, Username: "analyst", Roles: []string{"analyst"}, Attributes: map[string]string{"tenant": "acme"}}

	ctx := context.Background()

	// 1) admin reads every tenant, no masking -----------------------------
	res, err := px.Execute(ctx, dsID, adminClaims, "SELECT * FROM orders")
	if err != nil {
		t.Fatalf("admin orders: %v", err)
	}
	if len(res.Rows) != 3 {
		t.Fatalf("admin orders: want 3 rows (all tenants), got %d", len(res.Rows))
	}

	// 2) analyst row policy: only the acme tenant -------------------------
	res, err = px.Execute(ctx, dsID, analystClaims, "SELECT * FROM orders")
	if err != nil {
		t.Fatalf("analyst orders: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("analyst orders: want 2 acme rows, got %d", len(res.Rows))
	}
	for _, row := range res.Rows {
		if row["tenant_id"] != "acme" {
			t.Fatalf("analyst orders: row leaked tenant %v (row policy not applied)", row["tenant_id"])
		}
	}

	// 3) analyst column masking on customers ------------------------------
	res, err = px.Execute(ctx, dsID, analystClaims, "SELECT * FROM customers")
	if err != nil {
		t.Fatalf("analyst customers: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("analyst customers: want 2 acme rows, got %d", len(res.Rows))
	}
	for _, row := range res.Rows {
		if row["tenant_id"] != "acme" {
			t.Fatalf("analyst customers: row leaked tenant %v", row["tenant_id"])
		}
		if row["phone"] == "13812345678" || row["phone"] == "13900001111" {
			t.Fatalf("analyst customers: phone not masked: %v", row["phone"])
		}
		if row["email"] == "ops@acme.com" || row["email"] == "bill@acme.com" {
			t.Fatalf("analyst customers: email not masked: %v", row["email"])
		}
		// phone keeps 3 head + 4 tail digits, e.g. 138****5678
		if len(row["phone"].(string)) < 8 {
			t.Fatalf("analyst customers: unexpected masked phone format: %v", row["phone"])
		}
	}

	// 4) admin sees PII unmasked ------------------------------------------
	res, err = px.Execute(ctx, dsID, adminClaims, "SELECT * FROM customers")
	if err != nil {
		t.Fatalf("admin customers: %v", err)
	}
	if len(res.Rows) != 3 {
		t.Fatalf("admin customers: want 3 rows, got %d", len(res.Rows))
	}
	sawRawPhone := false
	for _, row := range res.Rows {
		if row["phone"] == "13812345678" {
			sawRawPhone = true
		}
	}
	if !sawRawPhone {
		t.Fatalf("admin customers: expected at least one raw phone (admin bypass), none found")
	}

	// 5) default deny: ungranted table ------------------------------------
	_, err = px.Execute(ctx, dsID, analystClaims, "SELECT * FROM salary")
	if err == nil {
		t.Fatalf("analyst salary: expected access-denied error, got nil")
	}
	if !containsStr(err.Error(), "access denied") {
		t.Fatalf("analyst salary: expected 'access denied', got %q", err.Error())
	}

	// 6) ListTables honours governance ------------------------------------
	ti, err := px.ListTables(ctx, dsID, analystClaims)
	if err != nil {
		t.Fatalf("analyst list tables: %v", err)
	}
	names := map[string]bool{}
	for _, t := range ti {
		names[t.Name] = true
	}
	if !names["orders"] || !names["customers"] {
		t.Fatalf("analyst list tables: expected orders+customers, got %v", names)
	}
	if names["salary"] {
		t.Fatalf("analyst list tables: salary must not appear (default deny)")
	}
	if len(ti) != 2 {
		t.Fatalf("analyst list tables: expected exactly 2 tables, got %d", len(ti))
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
