package store

import (
	"fmt"
	"time"

	"github.com/wisonwang/aegis/internal/logging"
)

// schemaMigrationsTable records which migrations have already been applied.
// Introduced by ADR-0006 (Phase 0) to replace the previous "run migrate()
// unconditionally on every startup" approach with an ordered, idempotent,
// versioned runner.
const schemaMigrationsTable = `CREATE TABLE IF NOT EXISTS schema_migrations (
	version VARCHAR(64) PRIMARY KEY,
	applied_at DATETIME
)`

// Migration is a single, ordered schema change. Up runs exactly once, when the
// version is not yet recorded in schema_migrations. Down is intentionally
// omitted: rollbacks are performed manually from the MySQL / SQLite backup
// taken before deployment (safer than auto-revert on a live control plane).
type Migration struct {
	Version string
	Up      func(*Store) error
}

// migrations is the ordered list of all schema migrations. Append new entries
// (0002, 0003, ...) here. Never reorder or rename an existing version — doing
// so would either re-run or skip a migration on already-deployed databases.
var migrations = []Migration{
	// 0001 = the pre-ADR-0006 baseline (all CREATE TABLE + idempotent ALTERs
	// that previously lived inline in Store.migrate). Ported verbatim so the
	// runner is a drop-in replacement with identical behavior.
	{Version: "0001", Up: (*Store).legacyMigrate},
	// 0002 = ADR-0006 Phase 1: unify platform + workspace role systems behind
	// role_assignments, add roles.scope, backfill, align role_id typing, and
	// add best-effort foreign keys.
	{Version: "0002", Up: migratePhase1},
	// 0003 = repair 0002's backfill, which left role_assignments.created_at
	// NULL on migrated rows (broke the workspace members listing).
	{Version: "0003", Up: migratePhase1Fixups},
	// 0004 = move the seeded workspace roles out of the platform role name
	// space (roles.name is UNIQUE), which 0002 had collided with.
	{Version: "0004", Up: migrateWorkspaceRoleNamespace},
	// 0005 = collapse stacked workspace memberships to one level per
	// (workspace, user), restoring the invariant the legacy table's primary
	// key used to enforce.
	{Version: "0005", Up: migrateDedupeWorkspaceMembership},
}

// runMigrations applies every not-yet-applied migration in order, recording
// each version in schema_migrations. It is idempotent: already-applied versions
// are skipped, so calling it on every store open is safe.
func (s *Store) runMigrations() error {
	if _, err := s.db.Exec(schemaMigrationsTable); err != nil {
		return fmt.Errorf("migrations: create bookkeeping table: %w", err)
	}
	applied := make(map[string]bool)
	rows, err := s.db.Query(`SELECT version FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("migrations: read applied versions: %w", err)
	}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return fmt.Errorf("migrations: scan version: %w", err)
		}
		applied[v] = true
	}
	rows.Close()

	for _, m := range migrations {
		if applied[m.Version] {
			continue
		}
		logging.With("version", m.Version).Info("migration: applying")
		if err := m.Up(s); err != nil {
			return fmt.Errorf("migrations: apply %s: %w", m.Version, err)
		}
		if _, err := s.db.Exec(
			`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
			m.Version, time.Now()); err != nil {
			return fmt.Errorf("migrations: record %s: %w", m.Version, err)
		}
		logging.With("version", m.Version).Info("migration: applied")
	}
	return nil
}
