package store

import (
	"context"
	"errors"
	"testing"
)

// TestGetDataSourceByID_IgnoresWorkspace proves the connection-pool lookup path
// can reach a datasource in ANY workspace.
//
// Regression: the pool manager used GetDataSource(context.Background(), id),
// which implicitly scoped to "default". Every datasource registered in another
// workspace therefore failed with `datasource %q not found` and was unusable —
// the multi-tenant columns existed but the feature was dead (ADR-0007).
func TestGetDataSourceByID_IgnoresWorkspace(t *testing.T) {
	st := newIsolationStore(t)
	acmeCtx := WithWorkspace(context.Background(), "acme")

	if err := st.CreateDataSource(acmeCtx, &DataSource{Name: "ds-acme", Type: "sqlite", DSN: "file:x"}); err != nil {
		t.Fatal(err)
	}
	list, err := st.ListDataSources(acmeCtx)
	if err != nil || len(list) != 1 {
		t.Fatalf("seed: %v (n=%d)", err, len(list))
	}
	id := list[0].ID

	// The scoped read from "default" must NOT see it (isolation still holds).
	if ds, _ := st.GetDataSource(context.Background(), id); ds != nil {
		t.Fatal("isolation broken: default context resolved an acme datasource")
	}

	// The infrastructure read must see it, and must carry the owning workspace.
	ds, err := st.GetDataSourceByID(id)
	if err != nil {
		t.Fatal(err)
	}
	if ds == nil {
		t.Fatal("GetDataSourceByID must reach datasources in any workspace")
	}
	if ds.WorkspaceID != "acme" {
		t.Fatalf("expected workspace_id=acme, got %q", ds.WorkspaceID)
	}
}

// TestBoundContext_NeverStarsOrEmpty proves the ADR-0007 anchor: governance is
// always written into a concrete workspace derived from its datasource, never
// "*" and never blank.
func TestBoundContext_NeverStarsOrEmpty(t *testing.T) {
	cross := WithWorkspace(context.Background(), WorkspaceAll)

	if got := WorkspaceID((&DataSource{WorkspaceID: "acme"}).BoundContext(cross)); got != "acme" {
		t.Fatalf("expected acme, got %q", got)
	}
	// Legacy rows (pre-migration) and the impossible "*" both collapse to default.
	if got := WorkspaceID((&DataSource{WorkspaceID: ""}).BoundContext(cross)); got != DefaultWorkspaceID {
		t.Fatalf("empty workspace must fall back to default, got %q", got)
	}
	if got := WorkspaceID((&DataSource{WorkspaceID: WorkspaceAll}).BoundContext(cross)); got != DefaultWorkspaceID {
		t.Fatalf("'*' must never be bound as a real workspace, got %q", got)
	}
	if CrossesWorkspaces((&DataSource{WorkspaceID: "acme"}).BoundContext(cross)) {
		t.Fatal("a bound context must never still be the cross-workspace view")
	}
}

// TestResolvePermissions_NeverAggregatesAcrossTenants is the security-critical
// one. An admin browsing in the cross-workspace ("*") view must not cause a
// query's effective permissions to be the UNION of every tenant's grants.
// Resolution narrows to the datasource's own workspace.
func TestResolvePermissions_NeverAggregatesAcrossTenants(t *testing.T) {
	st := newIsolationStore(t)
	acmeCtx := WithWorkspace(context.Background(), "acme")
	globexCtx := WithWorkspace(context.Background(), "globex")
	crossCtx := WithWorkspace(context.Background(), WorkspaceAll)

	if err := st.CreateRole(&Role{ID: "r1", Name: "analyst"}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddUserRole("u1", "r1"); err != nil {
		t.Fatal(err)
	}

	// One datasource, owned by acme.
	if err := st.CreateDataSource(acmeCtx, &DataSource{Name: "ds", Type: "sqlite", DSN: "file:x"}); err != nil {
		t.Fatal(err)
	}
	dss, _ := st.ListDataSources(acmeCtx)
	dsID := dss[0].ID

	// acme grants SELECT on orders.
	if err := st.CreateTablePermission(acmeCtx, &TablePermission{
		RoleID: "r1", DataSourceID: dsID, TableName: "orders", Ops: "SELECT",
	}); err != nil {
		t.Fatal(err)
	}
	// globex has a stray grant on the SAME datasource id for a different table.
	// It must never bleed into acme's resolution.
	if err := st.CreateTablePermission(globexCtx, &TablePermission{
		RoleID: "r1", DataSourceID: dsID, TableName: "salaries", Ops: "SELECT,DELETE",
	}); err != nil {
		t.Fatal(err)
	}

	eff, err := st.ResolvePermissions(crossCtx, "u1", dsID)
	if err != nil {
		t.Fatal(err)
	}
	if _, leaked := eff["salaries"]; leaked {
		t.Fatal("cross-tenant leak: globex grant surfaced while resolving an acme datasource")
	}
	if _, ok := eff["orders"]; !ok {
		t.Fatalf("expected the owning workspace's grant to resolve, got %v", keysOf(eff))
	}
}

// TestDeleteWorkspaceScoped_RefusesForeignRow proves the delete guard: an
// operator scoped to workspace A cannot delete workspace B's governance, and
// gets ErrNotFound (not a distinguishable "forbidden") so ids can't be probed.
func TestDeleteWorkspaceScoped_RefusesForeignRow(t *testing.T) {
	st := newIsolationStore(t)
	acmeCtx := WithWorkspace(context.Background(), "acme")
	globexCtx := WithWorkspace(context.Background(), "globex")

	p := &TablePermission{RoleID: "r1", DataSourceID: "ds1", TableName: "orders", Ops: "SELECT"}
	if err := st.CreateTablePermission(acmeCtx, p); err != nil {
		t.Fatal(err)
	}

	if err := st.DeleteTablePermission(globexCtx, p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound deleting a foreign-workspace row, got %v", err)
	}
	// Still there.
	if perms, _ := st.ListTablePermissions(acmeCtx, "r1", "ds1", ""); len(perms) != 1 {
		t.Fatal("guard failed: the foreign delete actually removed the row")
	}
	// The owner can delete it.
	if err := st.DeleteTablePermission(acmeCtx, p.ID); err != nil {
		t.Fatalf("owner delete must succeed: %v", err)
	}
	// Deleting again reports not-found rather than silently succeeding.
	if err := st.DeleteTablePermission(acmeCtx, p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound on second delete, got %v", err)
	}
}

// TestMoveDataSource_CarriesGovernance proves re-parenting a datasource moves
// its governance with it. Leaving grants behind would silently disable them.
func TestMoveDataSource_CarriesGovernance(t *testing.T) {
	st := newIsolationStore(t)
	acmeCtx := WithWorkspace(context.Background(), "acme")
	globexCtx := WithWorkspace(context.Background(), "globex")

	if err := st.CreateDataSource(acmeCtx, &DataSource{Name: "ds", Type: "sqlite", DSN: "file:x"}); err != nil {
		t.Fatal(err)
	}
	dss, _ := st.ListDataSources(acmeCtx)
	dsID := dss[0].ID
	if err := st.CreateTablePermission(acmeCtx, &TablePermission{
		RoleID: "r1", DataSourceID: dsID, TableName: "orders", Ops: "SELECT",
	}); err != nil {
		t.Fatal(err)
	}

	if err := st.MoveDataSource(dsID, "globex"); err != nil {
		t.Fatal(err)
	}

	if got, _ := st.ListDataSources(acmeCtx); len(got) != 0 {
		t.Fatalf("datasource should have left acme, got %v", dsNames(got))
	}
	if got, _ := st.ListDataSources(globexCtx); len(got) != 1 {
		t.Fatalf("datasource should have landed in globex, got %v", dsNames(got))
	}
	perms, _ := st.ListTablePermissions(globexCtx, "r1", dsID, "")
	if len(perms) != 1 {
		t.Fatal("governance must travel with the datasource, otherwise it is orphaned")
	}
	// "*" is not a real workspace and must be refused as a move target.
	if err := st.MoveDataSource(dsID, WorkspaceAll); err == nil {
		t.Fatal("expected MoveDataSource to reject the cross-workspace sentinel")
	}
}

func keysOf(m map[string]*TableEffective) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
