package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/wisonwang/aegis/internal/logging"
)

// migratePhase1 implements ADR-0006 Phase 1: unify the previously-disjoint
// platform-role and workspace-role systems behind a single role_assignments
// table, add a scope discriminator to roles, and align the governance tables.
//
// It is safe to run on both fresh and already-deployed databases:
//   - the role_assignments table is created IF NOT EXISTS,
//   - platform/workspace assignments are backfilled from the legacy
//     user_roles / workspace_members tables via idempotent INSERT OR/IGNORE,
//   - column/type changes are guarded, and foreign keys are best-effort so a
//     stray orphan or an unsupported engine never blocks startup.
func migratePhase1(s *Store) error {
	// 1. roles.scope discriminator (platform | workspace).
	s.alterAddColumn("roles", "scope VARCHAR(32) NOT NULL DEFAULT 'platform'")

	// 2. seed the three fixed workspace roles as rows in roles (scope='workspace').
	for _, name := range []string{WsRoleAdmin, WsRoleMember, WsRoleViewer} {
		if _, err := s.db.Exec(
			`INSERT `+s.insertIgnore()+` INTO roles (id,name,scope,is_system,description) VALUES (?,?,?,?,?)`,
			"ws-role:"+name, name, "workspace", 1, "workspace role"); err != nil {
			return fmt.Errorf("seed workspace role %s: %w", name, err)
		}
	}

	// 3. the unified assignment table. principal_id+role_id+workspace_id is the
	// key; workspace_id == '' denotes a platform-global assignment.
	if _, err := s.db.Exec(
		`CREATE TABLE IF NOT EXISTS role_assignments (
			principal_id VARCHAR(64) NOT NULL,
			role_id VARCHAR(64) NOT NULL,
			workspace_id VARCHAR(191) NOT NULL DEFAULT '',
			is_default INTEGER NOT NULL DEFAULT 0,
			granted_by VARCHAR(64),
			created_at DATETIME,
			PRIMARY KEY(principal_id, role_id, workspace_id)
		)`); err != nil {
		return fmt.Errorf("create role_assignments: %w", err)
	}

	// 4. backfill platform assignments (global scope) from user_roles.
	if _, err := s.db.Exec(
		`INSERT `+s.insertIgnore()+` INTO role_assignments (principal_id, role_id, workspace_id, is_default)
		 SELECT user_id, role_id, '', 0 FROM user_roles`); err != nil {
		return fmt.Errorf("backfill platform roles: %w", err)
	}

	// 5. backfill workspace assignments from workspace_members, mapping the
	// stored role string to the seeded workspace-role id.
	if _, err := s.db.Exec(
		`INSERT `+s.insertIgnore()+` INTO role_assignments (principal_id, role_id, workspace_id, is_default)
		 SELECT wm.user_id, r.id, wm.workspace_id, wm.is_default
		 FROM workspace_members wm JOIN roles r ON r.name = wm.role AND r.scope = 'workspace'`); err != nil {
		return fmt.Errorf("backfill workspace roles: %w", err)
	}

	// 6. unify role_id typing on the governance tables so FKs are well-defined
	// (TEXT -> VARCHAR(64)). SQLite types are dynamic, so only MySQL needs it.
	if s.isMySQL() {
		s.alterModifyColumn("table_permissions", "role_id", "VARCHAR(64) NOT NULL")
		s.alterModifyColumn("row_policies", "role_id", "VARCHAR(64) NOT NULL")
	}

	// 7. best-effort referential integrity. Failures (e.g. a stray orphan or an
	// engine that rejects ALTER) are logged and ignored so a live control plane
	// never fails to start.
	s.addForeignKey("role_assignments", "fk_ra_role", "role_id", "roles(id)", "ON DELETE CASCADE")
	s.addForeignKey("role_assignments", "fk_ra_principal", "principal_id", "users(id)", "ON DELETE CASCADE")
	s.addForeignKey("table_permissions", "fk_tp_role", "role_id", "roles(id)", "ON DELETE CASCADE")
	s.addForeignKey("row_policies", "fk_rp_role", "role_id", "roles(id)", "ON DELETE CASCADE")
	s.addForeignKey("column_masks", "fk_cm_role", "role_id", "roles(id)", "ON DELETE CASCADE")
	return nil
}

// migratePhase1Fixups repairs data produced by 0002. The Phase 1 backfill
// (0002 steps 4/5) inserted assignments without created_at, leaving historical
// rows NULL — which broke ListWorkspaceMembers' time.Time scan. 0002 is left
// untouched on purpose (never edit a shipped migration); this follow-up
// migration restores the timestamps instead.
func migratePhase1Fixups(s *Store) error {
	// 1. recover the real join time from the legacy workspace_members rows.
	// Correlated subquery only — portable across MySQL and SQLite.
	if _, err := s.db.Exec(
		`UPDATE role_assignments SET created_at = (
			SELECT wm.created_at FROM workspace_members wm
			WHERE wm.workspace_id = role_assignments.workspace_id
			  AND wm.user_id = role_assignments.principal_id
		 )
		 WHERE created_at IS NULL AND workspace_id <> ''
		   AND EXISTS (
			SELECT 1 FROM workspace_members wm2
			WHERE wm2.workspace_id = role_assignments.workspace_id
			  AND wm2.user_id = role_assignments.principal_id
			  AND wm2.created_at IS NOT NULL
		 )`); err != nil {
		// Non-fatal: the fallback below still guarantees a non-NULL value.
		logging.With("error", err.Error()).Warn("migration 0003: workspace created_at recovery skipped")
	}

	// 2. fallback for everything still NULL (platform assignments from
	// user_roles, which never had a timestamp to recover).
	if _, err := s.db.Exec(
		`UPDATE role_assignments SET created_at = ? WHERE created_at IS NULL`,
		time.Now()); err != nil {
		return fmt.Errorf("backfill role_assignments.created_at: %w", err)
	}
	return nil
}

// migrateWorkspaceRoleNamespace frees the platform role name space that 0002
// accidentally occupied. roles.name is UNIQUE, so seeding workspace roles as
// "viewer"/"member"/"workspace_admin" made those names unavailable to platform
// roles — an IdP group mapped to a platform role named "viewer" resolved to the
// workspace row instead and the grant was rejected.
//
// Renaming is safe: role_assignments references roles.id, never roles.name, and
// the membership level exposed by the API is derived from the id prefix.
func migrateWorkspaceRoleNamespace(s *Store) error {
	for _, level := range []string{WsRoleAdmin, WsRoleMember, WsRoleViewer} {
		if _, err := s.db.Exec(
			`UPDATE roles SET name=? WHERE id=? AND scope='workspace'`,
			wsRoleNamePrefix+level, wsRoleIDPrefix+level); err != nil {
			return fmt.Errorf("namespace workspace role %s: %w", level, err)
		}
	}
	return nil
}

// wsRoleRank orders the membership hierarchy so de-duplication keeps the most
// privileged level a user was ever granted (losing privilege silently would
// lock an operator out of their own workspace).
func wsRoleRank(roleID string) int {
	switch workspaceRoleLevel(roleID) {
	case WsRoleAdmin:
		return 3
	case WsRoleMember:
		return 2
	case WsRoleViewer:
		return 1
	default:
		return 0
	}
}

// migrateDedupeWorkspaceMembership collapses stacked workspace assignments to
// one level per (workspace, user).
//
// The legacy workspace_members table was keyed on (workspace_id, user_id), so
// this invariant held implicitly. role_assignments keys on role_id as well, so
// between 0002 and this fix a "change role" call appended a second level
// instead of replacing the first — leaving e.g. admin as both workspace_admin
// and member, and duplicating the workspace in the user's list.
func migrateDedupeWorkspaceMembership(s *Store) error {
	type assignment struct {
		roleID    string
		isDefault bool
		createdAt sql.NullTime
	}
	type key struct{ ws, principal string }

	rows, err := s.db.Query(
		`SELECT workspace_id, principal_id, role_id, is_default, created_at
		 FROM role_assignments WHERE workspace_id <> ''`)
	if err != nil {
		return fmt.Errorf("scan workspace assignments: %w", err)
	}
	groups := map[key][]assignment{}
	for rows.Next() {
		var k key
		var a assignment
		if err := rows.Scan(&k.ws, &k.principal, &a.roleID, &a.isDefault, &a.createdAt); err != nil {
			rows.Close()
			return fmt.Errorf("scan assignment: %w", err)
		}
		groups[k] = append(groups[k], a)
	}
	rows.Close()

	for k, as := range groups {
		if len(as) < 2 {
			continue
		}
		winner := as[0]
		for _, a := range as[1:] {
			if wsRoleRank(a.roleID) > wsRoleRank(winner.roleID) {
				winner.roleID = a.roleID
			}
			// Keep the earliest join time and any default marker.
			if a.isDefault {
				winner.isDefault = true
			}
			if a.createdAt.Valid && (!winner.createdAt.Valid || a.createdAt.Time.Before(winner.createdAt.Time)) {
				winner.createdAt = a.createdAt
			}
		}
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(
			`DELETE FROM role_assignments WHERE workspace_id=? AND principal_id=?`,
			k.ws, k.principal); err != nil {
			tx.Rollback()
			return fmt.Errorf("dedupe delete: %w", err)
		}
		created := time.Now()
		if winner.createdAt.Valid {
			created = winner.createdAt.Time
		}
		if _, err := tx.Exec(
			`INSERT INTO role_assignments (principal_id,role_id,workspace_id,is_default,created_at) VALUES (?,?,?,?,?)`,
			k.principal, winner.roleID, k.ws, winner.isDefault, created); err != nil {
			tx.Rollback()
			return fmt.Errorf("dedupe insert: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		logging.With("workspace", k.ws, "principal", k.principal, "kept", winner.roleID, "dropped", len(as)-1).
			Info("migration 0005: collapsed stacked workspace membership")
	}
	return nil
}

func (s *Store) alterAddColumn(table, colDef string) {
	q := `ALTER TABLE ` + table + ` ADD COLUMN ` + colDef
	if _, err := s.db.Exec(q); err != nil {
		if !isAlreadyExists(err) {
			logging.With("error", err.Error(), "table", table).Warn("migration: alterAddColumn skipped")
		}
	}
}

func (s *Store) alterModifyColumn(table, col, newDef string) {
	q := `ALTER TABLE ` + table + ` MODIFY COLUMN ` + col + ` ` + newDef
	if _, err := s.db.Exec(q); err != nil {
		if !isAlreadyExists(err) {
			logging.With("error", err.Error(), "table", table).Warn("migration: alterModifyColumn skipped")
		}
	}
}

func (s *Store) addForeignKey(table, name, col, ref, action string) {
	// SQLite has no ALTER TABLE ... ADD CONSTRAINT (the only way is a full
	// table rebuild) and does not enforce foreign keys unless explicitly
	// enabled. Skip quietly instead of logging five warnings on every open;
	// referential integrity there is upheld by the repository layer.
	if !s.isMySQL() {
		return
	}
	q := `ALTER TABLE ` + table + ` ADD CONSTRAINT ` + name +
		` FOREIGN KEY (` + col + `) REFERENCES ` + ref + ` ` + action
	if _, err := s.db.Exec(q); err != nil {
		if !isAlreadyExists(err) {
			logging.With("error", err.Error(), "table", table, "fk", name).
				Warn("migration: addForeignKey skipped")
		}
	}
}
