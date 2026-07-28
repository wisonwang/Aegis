package store

import (
	"context"
	"testing"
)

// Regression: an empty roleID must mean "all roles", not "no rows". The
// governance UI lists every role's grants/policies for a table, so the
// role_id filter must be omitted when roleID is "".
func TestListTablePermissions_AllRoles(t *testing.T) {
	st, _ := Open(":memory:")
	defer st.Close()
	_ = st.CreateRole(&Role{ID: "r1", Name: "role1"})
	_ = st.CreateRole(&Role{ID: "r2", Name: "role2"})
	_ = st.CreateDataSource(context.Background(), &DataSource{ID: "ds1", Name: "demo", Type: "sqlite", DSN: ":memory:"})
	_ = st.CreateTablePermission(context.Background(), &TablePermission{RoleID: "r1", DataSourceID: "ds1", TableName: "orders", Ops: "SELECT"})
	_ = st.CreateTablePermission(context.Background(), &TablePermission{RoleID: "r2", DataSourceID: "ds1", TableName: "orders", Ops: "INSERT"})
	_ = st.CreateRowPolicy(context.Background(), &RowPolicy{RoleID: "r1", DataSourceID: "ds1", TableName: "orders", Predicate: "tenant_id = :tenant", Priority: 10})

	perms, err := st.ListTablePermissions(context.Background(), "", "ds1", "orders")
	if err != nil {
		t.Fatalf("list perms: %v", err)
	}
	if len(perms) != 2 {
		t.Fatalf("expected 2 perms across roles, got %d", len(perms))
	}
	pols, err := st.ListRowPolicies(context.Background(), "", "ds1", "orders")
	if err != nil {
		t.Fatalf("list policies: %v", err)
	}
	if len(pols) != 1 {
		t.Fatalf("expected 1 policy across roles, got %d", len(pols))
	}
}
