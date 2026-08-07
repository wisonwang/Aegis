package store

import (
	"path/filepath"
	"testing"
	"time"
)

// newRoleStore opens a fresh control-plane store. Migrations 0001-0003 run on
// open, so the three fixed workspace roles are already seeded.
func newRoleStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "roles.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// TestListRolesExcludesWorkspaceScope pins the ADR-0006 Phase 1 invariant:
// after unifying both role systems into one table, the three workspace roles
// (workspace_admin/member/viewer) must NOT leak into the platform role list.
// They are membership levels, not grantable platform roles — showing them in
// role management or a permission-grant picker would let an operator attach
// governance rules to a role that the authorizer never evaluates.
func TestListRolesExcludesWorkspaceScope(t *testing.T) {
	st := newRoleStore(t)

	// A bare store has no platform roles (the server seeds them), so create one
	// to prove the predicate does not over-filter.
	if err := st.CreateRole(&Role{Name: "analyst", Description: "platform role"}); err != nil {
		t.Fatalf("create platform role: %v", err)
	}

	roles, err := st.ListRoles()
	if err != nil {
		t.Fatalf("list roles: %v", err)
	}
	for _, r := range roles {
		switch r.Name {
		case WsRoleAdmin, WsRoleMember, WsRoleViewer:
			t.Fatalf("workspace role %q leaked into platform role list", r.Name)
		}
	}

	// The predicate must not over-filter — hiding a real platform role is the
	// worse failure mode, so assert the freshly created one is visible.
	var sawAnalyst bool
	for _, r := range roles {
		if r.Name == "analyst" {
			sawAnalyst = true
		}
	}
	if !sawAnalyst {
		t.Fatal("platform role \"analyst\" missing from ListRoles")
	}
}

// TestAddUserRoleRejectsWorkspaceScope guards the second half of the same
// invariant: because both role kinds now share one table, a caller holding a
// workspace role id could otherwise grant it globally (workspace_id=''), which
// would mean "member of every workspace" and bypass membership entirely.
func TestAddUserRoleRejectsWorkspaceScope(t *testing.T) {
	st := newRoleStore(t)

	u := &User{Username: "carol", DisplayName: "Carol", Status: "active"}
	if err := st.CreateUser(u); err != nil {
		t.Fatalf("create user: %v", err)
	}

	if err := st.AddUserRole(u.ID, "ws-role:"+WsRoleMember); err == nil {
		t.Fatal("expected AddUserRole to reject a workspace-scoped role")
	}
	if err := st.AddUserRole(u.ID, "no-such-role"); err == nil {
		t.Fatal("expected AddUserRole to reject an unknown role")
	}

	// A genuine platform role must still be grantable.
	pr := &Role{Name: "analyst", Description: "platform role"}
	if err := st.CreateRole(pr); err != nil {
		t.Fatalf("create platform role: %v", err)
	}
	roles, err := st.ListRoles()
	if err != nil || len(roles) == 0 {
		t.Fatalf("list roles: %v (n=%d)", err, len(roles))
	}
	if err := st.AddUserRole(u.ID, pr.ID); err != nil {
		t.Fatalf("granting platform role %q failed: %v", pr.Name, err)
	}
	got, err := st.ListRolesForUser(u.ID)
	if err != nil {
		t.Fatalf("list roles for user: %v", err)
	}
	if len(got) != 1 || got[0].ID != pr.ID {
		t.Fatalf("expected exactly the granted platform role, got %+v", got)
	}
}

// TestWorkspaceRolesDoNotOccupyPlatformNameSpace pins the fix for the collision
// that 0002 introduced: roles.name is UNIQUE, so seeding a workspace role named
// "viewer" made that name unavailable to platform roles. An IdP group mapped to
// a platform role called "viewer" then resolved to the workspace row and the
// grant was rejected — SSO users silently ended up with no roles at all.
func TestWorkspaceRolesDoNotOccupyPlatformNameSpace(t *testing.T) {
	st := newRoleStore(t)

	// The exact scenario: an LDAP/OIDC mapping auto-provisions a platform role
	// whose name equals a workspace membership level.
	for _, name := range []string{WsRoleViewer, WsRoleMember, WsRoleAdmin} {
		r := &Role{Name: name, Description: "platform role from IdP mapping"}
		if err := st.CreateRole(r); err != nil {
			t.Fatalf("platform role %q must be creatable despite the workspace role: %v", name, err)
		}
		got, err := st.GetRole(name)
		if err != nil {
			t.Fatalf("get role %q: %v", name, err)
		}
		if got == nil || got.ID != r.ID {
			t.Fatalf("GetRole(%q) resolved to %+v, want the platform role %s", name, got, r.ID)
		}
		// And it must be grantable — the workspace-scope guard must not fire.
		u := &User{Username: "u-" + name, DisplayName: name, Status: "active"}
		if err := st.CreateUser(u); err != nil {
			t.Fatalf("create user: %v", err)
		}
		if err := st.AddUserRole(u.ID, got.ID); err != nil {
			t.Fatalf("granting platform role %q failed: %v", name, err)
		}
	}

	// Workspace membership still resolves by id and reports the clean level.
	if err := st.CreateWorkspace(&Workspace{ID: "acme", Name: "Acme", Slug: "acme", Settings: "{}"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	u := &User{Username: "erin", DisplayName: "Erin", Status: "active"}
	if err := st.CreateUser(u); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := st.AddWorkspaceMember("acme", u.ID, WsRoleViewer, false); err != nil {
		t.Fatalf("add member: %v", err)
	}
	members, err := st.ListWorkspaceMembers("acme")
	if err != nil {
		t.Fatalf("list members: %v", err)
	}
	if len(members) != 1 || members[0].Role != WsRoleViewer {
		t.Fatalf("expected one %q member, got %+v", WsRoleViewer, members)
	}
}

// TestListWorkspaceMembersToleratesNullCreatedAt reproduces the regression that
// migration 0002 introduced: backfilled assignments had a NULL created_at, and
// scanning straight into time.Time failed the entire members listing. Migration
// 0003 backfills the value; this test covers the defensive scan that keeps a
// stray NULL (e.g. hand-inserted row) from taking the endpoint down again.
func TestListWorkspaceMembersToleratesNullCreatedAt(t *testing.T) {
	st := newRoleStore(t)

	if err := st.CreateWorkspace(&Workspace{ID: "acme", Name: "Acme", Slug: "acme", Settings: "{}"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	u := &User{Username: "dave", DisplayName: "Dave", Status: "active"}
	if err := st.CreateUser(u); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := st.AddWorkspaceMember("acme", u.ID, WsRoleMember, true); err != nil {
		t.Fatalf("add member: %v", err)
	}

	// Force the NULL that migration 0002 used to leave behind.
	if _, err := st.db.Exec(
		`UPDATE role_assignments SET created_at=NULL WHERE workspace_id=?`, "acme"); err != nil {
		t.Fatalf("null out created_at: %v", err)
	}

	members, err := st.ListWorkspaceMembers("acme")
	if err != nil {
		t.Fatalf("list members with NULL created_at: %v", err)
	}
	if len(members) != 1 {
		t.Fatalf("expected 1 member, got %d", len(members))
	}
	if members[0].Role != WsRoleMember {
		t.Fatalf("expected role %q, got %q", WsRoleMember, members[0].Role)
	}
	if !members[0].CreatedAt.IsZero() {
		t.Fatalf("expected zero time for NULL created_at, got %v", members[0].CreatedAt)
	}
}

// TestAddWorkspaceMemberReplacesLevel pins the set-semantics that the legacy
// workspace_members primary key (workspace_id, user_id) used to enforce
// implicitly. role_assignments keys on role_id too, so without an explicit
// replace a "change role" call would stack workspace_admin AND member on the
// same user — and duplicate the workspace in their switcher.
func TestAddWorkspaceMemberReplacesLevel(t *testing.T) {
	st := newRoleStore(t)

	if err := st.CreateWorkspace(&Workspace{ID: "acme", Name: "Acme", Slug: "acme", Settings: "{}"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	u := &User{Username: "frank", DisplayName: "Frank", Status: "active"}
	if err := st.CreateUser(u); err != nil {
		t.Fatalf("create user: %v", err)
	}

	if err := st.AddWorkspaceMember("acme", u.ID, WsRoleMember, true); err != nil {
		t.Fatalf("add member: %v", err)
	}
	members, _ := st.ListWorkspaceMembers("acme")
	joinedAt := members[0].CreatedAt

	// Promote: must replace, not append.
	if err := st.AddWorkspaceMember("acme", u.ID, WsRoleAdmin, true); err != nil {
		t.Fatalf("promote member: %v", err)
	}
	members, err := st.ListWorkspaceMembers("acme")
	if err != nil {
		t.Fatalf("list members: %v", err)
	}
	if len(members) != 1 {
		t.Fatalf("expected exactly one membership row after promotion, got %d: %+v", len(members), members)
	}
	if members[0].Role != WsRoleAdmin {
		t.Fatalf("expected role %q after promotion, got %q", WsRoleAdmin, members[0].Role)
	}
	if !members[0].CreatedAt.Equal(joinedAt) {
		t.Fatalf("promotion must preserve the original join time: was %v, now %v", joinedAt, members[0].CreatedAt)
	}

	// And the workspace must appear exactly once in the user's list.
	wss, err := st.UserWorkspaces(u.ID)
	if err != nil {
		t.Fatalf("user workspaces: %v", err)
	}
	if len(wss) != 1 {
		t.Fatalf("expected 1 workspace for the user, got %d", len(wss))
	}
}

// TestDedupeWorkspaceMembershipKeepsHighestLevel covers migration 0005 against
// data already corrupted by the append bug: the most privileged level wins and
// the original join time survives, so nobody is silently demoted.
func TestDedupeWorkspaceMembershipKeepsHighestLevel(t *testing.T) {
	st := newRoleStore(t)

	if err := st.CreateWorkspace(&Workspace{ID: "acme", Name: "Acme", Slug: "acme", Settings: "{}"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	u := &User{Username: "gina", DisplayName: "Gina", Status: "active"}
	if err := st.CreateUser(u); err != nil {
		t.Fatalf("create user: %v", err)
	}

	// Recreate the corrupted shape directly (bypassing the now-fixed setter).
	early := time.Now().Add(-48 * time.Hour)
	for _, tc := range []struct {
		level     string
		isDefault bool
		at        time.Time
	}{
		{WsRoleViewer, false, time.Now()},
		{WsRoleAdmin, true, early},
		{WsRoleMember, false, time.Now()},
	} {
		if _, err := st.db.Exec(
			`INSERT INTO role_assignments (principal_id,role_id,workspace_id,is_default,created_at) VALUES (?,?,?,?,?)`,
			u.ID, wsRoleIDPrefix+tc.level, "acme", tc.isDefault, tc.at); err != nil {
			t.Fatalf("seed duplicate: %v", err)
		}
	}

	if err := migrateDedupeWorkspaceMembership(st); err != nil {
		t.Fatalf("dedupe: %v", err)
	}

	members, err := st.ListWorkspaceMembers("acme")
	if err != nil {
		t.Fatalf("list members: %v", err)
	}
	if len(members) != 1 {
		t.Fatalf("expected 1 member after dedupe, got %d: %+v", len(members), members)
	}
	if members[0].Role != WsRoleAdmin {
		t.Fatalf("dedupe must keep the highest level, got %q", members[0].Role)
	}
	if !members[0].IsDefault {
		t.Fatal("dedupe must preserve the default-workspace marker")
	}
	if members[0].CreatedAt.After(early.Add(time.Minute)) {
		t.Fatalf("dedupe must keep the earliest join time, got %v", members[0].CreatedAt)
	}
}

// TestMigrationsAreIdempotent proves the Phase 0 runner does not re-apply work:
// reopening the same file must be a no-op, and every version must be recorded
// exactly once.
func TestMigrationsAreIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mig.db")

	st, err := Open(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	_ = st.Close()

	st2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st2.Close()

	rows, err := st2.db.Query(`SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	defer rows.Close()
	seen := map[string]int{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		seen[v]++
	}
	for _, m := range migrations {
		if seen[m.Version] != 1 {
			t.Fatalf("migration %s recorded %d times, want 1", m.Version, seen[m.Version])
		}
	}
}
