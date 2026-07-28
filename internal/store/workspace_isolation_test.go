package store

import (
	"context"
	"path/filepath"
	"testing"
)

// newIsolationStore opens a fresh, empty control-plane store in a temp file.
// The migration seeds the "default" workspace and backfills nothing (no users
// exist yet), so every workspace below is created explicitly.
func newIsolationStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "iso.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func dsNames(dss []*DataSource) []string {
	out := make([]string, 0, len(dss))
	for _, d := range dss {
		out = append(out, d.Name)
	}
	return out
}

// TestWorkspaceIsolation_DataSources proves that ListDataSources and
// GetDataSource are hard-scoped to the workspace carried by ctx, and that the
// platform-admin cross-workspace ("*") view sees everything. This is the core
// ADR-001 invariant: a scoped read can never return another tenant's rows.
func TestWorkspaceIsolation_DataSources(t *testing.T) {
	st := newIsolationStore(t)

	if err := st.CreateWorkspace(&Workspace{ID: "acme", Name: "Acme", Slug: "acme", Settings: "{}"}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateWorkspace(&Workspace{ID: "globex", Name: "Globex", Slug: "globex", Settings: "{}"}); err != nil {
		t.Fatal(err)
	}

	acmeCtx := WithWorkspace(context.Background(), "acme")
	globexCtx := WithWorkspace(context.Background(), "globex")
	crossCtx := WithWorkspace(context.Background(), WorkspaceAll)
	defaultCtx := context.Background() // no workspace -> "default"

	if err := st.CreateDataSource(acmeCtx, &DataSource{Name: "ds-acme", Type: "sqlite", DSN: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateDataSource(globexCtx, &DataSource{Name: "ds-globex", Type: "sqlite", DSN: "x"}); err != nil {
		t.Fatal(err)
	}

	acmeDS, err := st.ListDataSources(acmeCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(acmeDS) != 1 || acmeDS[0].Name != "ds-acme" {
		t.Fatalf("acme scope: expected [ds-acme], got %v", dsNames(acmeDS))
	}

	globexDS, err := st.ListDataSources(globexCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(globexDS) != 1 || globexDS[0].Name != "ds-globex" {
		t.Fatalf("globex scope: expected [ds-globex], got %v", dsNames(globexDS))
	}

	// A scoped read must NEVER leak the other tenant's object.
	if _, err := st.GetDataSource(acmeCtx, firstID(globexDS)); err != nil {
		// GetDataSource returns (nil, nil) when not found, so this branch
		// should not be hit; we assert the result is nil below.
	}
	if ds, _ := st.GetDataSource(acmeCtx, firstID(globexDS)); ds != nil {
		t.Fatalf("cross-tenant leak: acme context resolved a globex datasource %q", ds.Name)
	}

	allDS, err := st.ListDataSources(crossCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(allDS) != 2 {
		t.Fatalf("cross view: expected 2 datasources, got %v", dsNames(allDS))
	}

	defDS, err := st.ListDataSources(defaultCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(defDS) != 0 {
		t.Fatalf("default scope: expected 0 datasources, got %v", dsNames(defDS))
	}
}

func firstID(dss []*DataSource) string {
	if len(dss) == 0 {
		return ""
	}
	return dss[0].ID
}

// TestWriteWorkspace_NeverStars proves that an admin writing while resolved to
// the cross-workspace ("*") view does NOT persist the impossible "*"
// discriminator. The object must land in the "default" workspace instead, so
// it remains reachable by the admin cross-view and is not orphaned.
func TestWriteWorkspace_NeverStars(t *testing.T) {
	st := newIsolationStore(t)
	crossCtx := WithWorkspace(context.Background(), WorkspaceAll)

	if err := st.CreateDataSource(crossCtx, &DataSource{Name: "ds-star", Type: "sqlite", DSN: "x"}); err != nil {
		t.Fatal(err)
	}

	// If the row had been stamped "*", a "default"-scoped list would see 0.
	def, err := st.ListDataSources(WithWorkspace(context.Background(), DefaultWorkspaceID))
	if err != nil {
		t.Fatal(err)
	}
	if len(def) != 1 || def[0].Name != "ds-star" {
		t.Fatalf("cross-write must fall back to default workspace, got %v", dsNames(def))
	}
}

// TestWorkspaceIsolation_Permissions proves that governance objects (table
// permissions here) are scoped exactly like datasources: a permission created
// in one workspace is invisible from another scoped context and visible from
// the cross-workspace view.
func TestWorkspaceIsolation_Permissions(t *testing.T) {
	st := newIsolationStore(t)
	acmeCtx := WithWorkspace(context.Background(), "acme")
	globexCtx := WithWorkspace(context.Background(), "globex")
	crossCtx := WithWorkspace(context.Background(), WorkspaceAll)

	if err := st.CreateDataSource(acmeCtx, &DataSource{Name: "ds", Type: "sqlite", DSN: "x"}); err != nil {
		t.Fatal(err)
	}
	acmeDS, err := st.ListDataSources(acmeCtx)
	if err != nil || len(acmeDS) != 1 {
		t.Fatalf("seed acme datasource: %v (n=%d)", err, len(acmeDS))
	}
	dsID := acmeDS[0].ID

	if err := st.CreateTablePermission(acmeCtx, &TablePermission{
		RoleID: "r1", DataSourceID: dsID, TableName: "orders", Ops: "SELECT",
	}); err != nil {
		t.Fatal(err)
	}

	acmePerms, err := st.ListTablePermissions(acmeCtx, "r1", dsID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(acmePerms) != 1 {
		t.Fatalf("acme scope: expected 1 permission, got %d", len(acmePerms))
	}

	globexPerms, err := st.ListTablePermissions(globexCtx, "r1", dsID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(globexPerms) != 0 {
		t.Fatalf("globex scope: expected 0 permissions (no leak), got %d", len(globexPerms))
	}

	crossPerms, err := st.ListTablePermissions(crossCtx, "r1", dsID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(crossPerms) != 1 {
		t.Fatalf("cross view: expected 1 permission, got %d", len(crossPerms))
	}
}

// TestResolveWorkspaceID enforces the ADR-001 decision matrix shared by the
// HTTP WorkspaceResolver and the MCP resolveContext: non-admins are confined to
// their own membership; admins may reach any workspace or the cross-view.
func TestResolveWorkspaceID(t *testing.T) {
	st := newIsolationStore(t)
	if err := st.CreateWorkspace(&Workspace{ID: "acme", Name: "Acme", Slug: "acme", Settings: "{}"}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddWorkspaceMember("acme", "u1", WsRoleMember, true); err != nil {
		t.Fatal(err)
	}

	// Non-admin, no hint -> their default workspace.
	id, err := st.ResolveWorkspaceID("u1", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if id != "acme" {
		t.Fatalf("non-admin no hint: expected acme, got %s", id)
	}

	// Non-admin asks for a workspace they don't belong to -> rejected.
	if _, err := st.ResolveWorkspaceID("u1", false, "globex"); err == nil {
		t.Fatal("expected rejection: non-member requesting foreign workspace")
	}

	// Non-admin asks for the cross-workspace view -> rejected.
	if _, err := st.ResolveWorkspaceID("u1", false, WorkspaceAll); err == nil {
		t.Fatal("expected rejection: non-admin requesting cross-workspace view")
	}

	// Admin, no hint -> cross-workspace view sentinel.
	id, err = st.ResolveWorkspaceID("admin", true, "")
	if err != nil {
		t.Fatal(err)
	}
	if id != WorkspaceAll {
		t.Fatalf("admin no hint: expected %s, got %s", WorkspaceAll, id)
	}

	// Admin, explicit id -> that id (admin may reach any).
	id, err = st.ResolveWorkspaceID("admin", true, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if id != "acme" {
		t.Fatalf("admin explicit: expected acme, got %s", id)
	}
}
