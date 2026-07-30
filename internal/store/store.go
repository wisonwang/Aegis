package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
	_ "github.com/go-sql-driver/mysql"

	"github.com/wisonwang/aegis/internal/logging"
)

// Models ---------------------------------------------------------------------

type User struct {
	ID           string         `json:"id"`
	Username     string         `json:"username"`
	DisplayName  string         `json:"display_name"`
	Email        string         `json:"email"`
	Type         string         `json:"type"` // human | service (default human)
	PasswordHash string         `json:"-"`
	ExternalID   sql.NullString `json:"-"` // OIDC subject / SSO identity; nullable (NULL for local users)
	Status       string         `json:"status"`     // active | disabled
	Attributes   string         `json:"attributes"` // JSON object, e.g. {"tenant":"acme"}
	LastLoginAt  sql.NullTime   `json:"last_login_at"`
	CreatedAt    time.Time      `json:"created_at"`
}

type Role struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	System      bool   `json:"system"` // true for built-in roles (admin, analyst, ...) that cannot be edited/deleted
}

// APIKey is a per-user bearer credential. The plaintext is shown only once at
// creation; only its SHA-256 hash (key_hash) and a short prefix are stored.
type APIKey struct {
	ID         string     `json:"id"`
	UserID     string     `json:"user_id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	Status     string     `json:"status"` // active | revoked
}

// ErrNotFound signals a requested entity does not exist (e.g. revoking a key
// that isn't there or doesn't belong to the caller).
var ErrNotFound = errors.New("not found")

type DataSource struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"` // mysql | postgres | sqlite
	DSN       string    `json:"dsn"`  // driver-specific connection string
	CreatedAt time.Time `json:"created_at"`
}

// TablePermission grants a role a set of operations on a table, optionally
// restricting the visible columns.
type TablePermission struct {
	ID          string `json:"id"`
	RoleID      string `json:"role_id"`
	DataSourceID string `json:"datasource_id"`
	TableName   string `json:"table_name"`
	Ops         string `json:"ops"`         // comma separated: SELECT,INSERT,UPDATE,DELETE
	AllowedCols string `json:"allowed_cols"` // JSON array; empty means "all allowed"
	DeniedCols  string `json:"denied_cols"`  // JSON array; always wins over allowed
}

// RowPolicy is a SQL predicate injected into queries for the role. It may
// reference user attributes via :attr placeholders (e.g. tenant_id = :tenant).
type RowPolicy struct {
	ID          string `json:"id"`
	RoleID      string `json:"role_id"`
	DataSourceID string `json:"datasource_id"`
	TableName   string `json:"table_name"`
	Predicate   string `json:"predicate"`
	Priority    int    `json:"priority"`
}

// ColumnMask is a dynamic-masking rule applied to a column's *values* (as
// opposed to DeniedCols, which drops the column entirely). It keeps the column
// in the result but transforms the cell so PII never reaches the caller — the
// AI supply gateway can hand out useful-but-safe data. Scope is per-role so
// different roles can mask differently; admin bypasses masking.
type ColumnMask struct {
	ID           string    `json:"id"`
	RoleID       string    `json:"role_id"`
	DataSourceID string    `json:"datasource_id"`
	TableName    string    `json:"table_name"`
	ColumnName   string    `json:"column_name"`
	Strategy     string    `json:"strategy"` // phone|email|card|hash|redact|partial
	Keep         int       `json:"keep"`     // param for the "partial" strategy
	UpdatedAt    time.Time `json:"updated_at"`
}

// MaskSpec is the resolved masking rule for a single column.
type MaskSpec struct {
	Column   string `json:"column"`
	Strategy string `json:"strategy"`
	Keep     int    `json:"keep"`
}

// TableEffective is the merged governance view for a single table, aggregated
// across all of a user's roles.
type TableEffective struct {
	TableName   string
	Ops         map[string]bool
	AllowedCols []string // empty => all columns allowed (unless denied)
	DeniedCols  []string
	RowPolicies []string // SQL predicate fragments (already substituted)
	Masks       []MaskSpec // dynamic value-masking rules for result columns
}

// Store ----------------------------------------------------------------------

type Store struct {
	db  *sql.DB
	kind string // "sqlite" | "mysql"
}

// isMySQL reports whether the control-plane store is backed by MySQL (vs SQLite).
func (s *Store) isMySQL() bool { return s.kind == "mysql" }

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open store %s: %w", path, err)
	}
	db.SetMaxOpenConns(1) // sqlite single-writer
	s := &Store{db: db, kind: "sqlite"}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

// OpenMySQL opens the control-plane store against a MySQL server. dsn is a
// go-sql-driver DSN, e.g. "user:pass@tcp(127.0.0.1:3306)/aegis?parseTime=true".
func OpenMySQL(dsn string) (*Store, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("open mysql store: %w", err)
	}
	db.SetMaxOpenConns(20)
	db.SetConnMaxLifetime(time.Hour)
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping mysql store: %w", err)
	}
	s := &Store{db: db, kind: "mysql"}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// insertIgnore returns the SQL keyword that makes an INSERT skip rows that
// would violate a unique/primary key: "IGNORE" for MySQL, "OR IGNORE" for SQLite.
func (s *Store) insertIgnore() string {
	if s.isMySQL() {
		return "IGNORE"
	}
	return "OR IGNORE"
}

// upsertSuffix builds the conflict-resolution tail of an INSERT. SQLite uses
// "ON CONFLICT(cols) DO UPDATE SET a=excluded.a,..." while MySQL uses
// "ON DUPLICATE KEY UPDATE a=VALUES(a),...". cols is the conflict target
// (ignored by MySQL, which keys off the unique/primary index) and sets lists
// the columns to refresh on conflict.
func (s *Store) upsertSuffix(conflict string, sets []string) string {
	parts := make([]string, len(sets))
	for i, c := range sets {
		if s.isMySQL() {
			parts[i] = c + "=VALUES(" + c + ")"
		} else {
			parts[i] = c + "=excluded." + c
		}
	}
	if s.isMySQL() {
		return "ON DUPLICATE KEY UPDATE " + strings.Join(parts, ", ")
	}
	return "ON CONFLICT(" + conflict + ") DO UPDATE SET " + strings.Join(parts, ", ")
}

// isAlreadyExists reports whether err is a benign "object already exists"
// condition for either backend (index or column add idempotency).
func isAlreadyExists(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "already exists") || // sqlite index
		strings.Contains(msg, "Duplicate key name") || // mysql index (1061)
		strings.Contains(msg, "duplicate column") || // sqlite column
		strings.Contains(msg, "Duplicate column name") || // mysql column (1060)
		strings.Contains(msg, "ER_DUP_FIELDNAME")
}

// createIndex runs a CREATE INDEX statement, tolerating an already-existing
// index on either backend (MySQL emits errno 1061; SQLite "already exists").
// Any other error is returned so migrate() can surface it.
func (s *Store) createIndex(ddl string) error {
	if _, err := s.db.Exec(ddl); err != nil {
		if isAlreadyExists(err) {
			return nil
		}
		return fmt.Errorf("createIndex: %w", err)
	}
	return nil
}

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id VARCHAR(64) PRIMARY KEY, username VARCHAR(191) UNIQUE NOT NULL,
			display_name TEXT, password_hash TEXT, external_id VARCHAR(191) UNIQUE,
			status TEXT, attributes TEXT, created_at DATETIME)`,
		`CREATE TABLE IF NOT EXISTS roles (
			id VARCHAR(64) PRIMARY KEY, name VARCHAR(191) UNIQUE NOT NULL, description TEXT, is_system INTEGER DEFAULT 0)`,
		`CREATE TABLE IF NOT EXISTS user_roles (
			user_id VARCHAR(64), role_id VARCHAR(64), PRIMARY KEY(user_id, role_id))`,
		`CREATE TABLE IF NOT EXISTS datasources (
			id VARCHAR(64) PRIMARY KEY, name VARCHAR(191) UNIQUE NOT NULL, type TEXT,
			dsn TEXT, created_at DATETIME)`,
		`CREATE TABLE IF NOT EXISTS table_permissions (
			id VARCHAR(64) PRIMARY KEY, role_id TEXT, datasource_id TEXT,
			table_name TEXT, ops TEXT, allowed_cols TEXT, denied_cols TEXT)`,
		`CREATE TABLE IF NOT EXISTS row_policies (
			id VARCHAR(64) PRIMARY KEY, role_id TEXT, datasource_id TEXT,
			table_name TEXT, predicate TEXT, priority INTEGER)`,
		`CREATE TABLE IF NOT EXISTS column_masks (
			id VARCHAR(64) PRIMARY KEY, role_id VARCHAR(64), datasource_id VARCHAR(191),
			table_name VARCHAR(191), column_name VARCHAR(191), strategy TEXT, keep INTEGER, updated_at DATETIME)`,
		`CREATE UNIQUE INDEX idx_masks_key
			ON column_masks(role_id, datasource_id, table_name, column_name)`,
		`CREATE TABLE IF NOT EXISTS audit_logs (
			id VARCHAR(64) PRIMARY KEY, ts DATETIME, user_id TEXT, username VARCHAR(191),
			channel TEXT, datasource_id TEXT, datasource TEXT,
			session_id TEXT, sql_text TEXT, rewritten_sql TEXT, status TEXT,
			error TEXT, row_count INTEGER, duration_ms INTEGER)`,
		`CREATE INDEX idx_audit_ts ON audit_logs(ts DESC)`,
		`CREATE INDEX idx_audit_user ON audit_logs(username)`,
		`CREATE TABLE IF NOT EXISTS schema_semantics (
			id VARCHAR(64) PRIMARY KEY, datasource_id VARCHAR(191) NOT NULL,
			table_name VARCHAR(191) NOT NULL, column_name VARCHAR(191) NOT NULL DEFAULT '',
			description TEXT, synonyms TEXT, examples TEXT, updated_at DATETIME)`,
		`CREATE UNIQUE INDEX idx_semantics_key
			ON schema_semantics(datasource_id, table_name, column_name)`,
		`CREATE TABLE IF NOT EXISTS security_alerts (
			id VARCHAR(64) PRIMARY KEY, ts DATETIME, level TEXT, rule TEXT,
			principal VARCHAR(191), channel TEXT, detail TEXT, resolved INTEGER DEFAULT 0)`,
		`CREATE INDEX idx_alerts_ts ON security_alerts(ts DESC)`,
		`CREATE INDEX idx_alerts_principal ON security_alerts(principal)`,
		`CREATE TABLE IF NOT EXISTS data_classifications (
			id VARCHAR(64) PRIMARY KEY, datasource_id VARCHAR(191) NOT NULL,
			table_name VARCHAR(191) NOT NULL, column_name VARCHAR(191) NOT NULL DEFAULT '',
			level TEXT, tags TEXT, updated_at DATETIME)`,
		`CREATE UNIQUE INDEX idx_classifications_key
			ON data_classifications(datasource_id, table_name, column_name)`,
		`CREATE TABLE IF NOT EXISTS approval_requests (
			id VARCHAR(64) PRIMARY KEY, applicant_id VARCHAR(64), applicant_name TEXT,
			datasource_id TEXT, datasource_name TEXT, table_name TEXT,
			role_name TEXT, ops TEXT, justification TEXT, status VARCHAR(32),
			approver_id TEXT, approver_name TEXT, granted_perm_id TEXT,
			created_at DATETIME, resolved_at DATETIME)`,
		`CREATE INDEX idx_approval_status ON approval_requests(status)`,
		`CREATE INDEX idx_approval_applicant ON approval_requests(applicant_id)`,
		`CREATE TABLE IF NOT EXISTS metric_definitions (
			id VARCHAR(64) PRIMARY KEY, datasource_id VARCHAR(191) NOT NULL,
			name VARCHAR(191) NOT NULL, description TEXT, sql_template TEXT,
			params TEXT, unit TEXT, created_at DATETIME, updated_at DATETIME)`,
		`CREATE UNIQUE INDEX idx_metrics_key
			ON metric_definitions(datasource_id, name)`,
		`CREATE TABLE IF NOT EXISTS workspaces (
			id VARCHAR(64) PRIMARY KEY, name VARCHAR(191) NOT NULL, slug VARCHAR(191) UNIQUE NOT NULL,
			settings TEXT, created_at DATETIME)`,
		`CREATE TABLE IF NOT EXISTS workspace_members (
			workspace_id VARCHAR(64), user_id VARCHAR(64), role VARCHAR(32), is_default INTEGER DEFAULT 0,
			created_at DATETIME, PRIMARY KEY(workspace_id, user_id))`,
		`CREATE TABLE IF NOT EXISTS api_keys (
			id VARCHAR(64) PRIMARY KEY, user_id VARCHAR(64) NOT NULL, name VARCHAR(191) NOT NULL,
			prefix VARCHAR(64) NOT NULL, key_hash VARCHAR(255) NOT NULL,
			created_at DATETIME, last_used_at DATETIME, expires_at DATETIME,
			status VARCHAR(32) DEFAULT 'active')`,
		`CREATE INDEX idx_api_keys_user ON api_keys(user_id)`,
	}
	for _, st := range stmts {
		t := strings.TrimSpace(st)
		if strings.HasPrefix(t, "CREATE INDEX") || strings.HasPrefix(t, "CREATE UNIQUE INDEX") {
			if err := s.createIndex(st); err != nil {
				return err
			}
			continue
		}
		if _, err := s.db.Exec(st); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	// Idempotent migration: add session_id to existing audit_logs tables that
	// were created before this column existed. ALTER errors if the column is
	// already present, which we safely ignore.
	if _, err := s.db.Exec(`ALTER TABLE audit_logs ADD COLUMN session_id TEXT`); err != nil {
		if !isAlreadyExists(err) {
			logging.With("error", err.Error()).Warn("migration: add session_id to audit_logs failed")
		}
	}
	if _, err := s.db.Exec(`ALTER TABLE users ADD COLUMN external_id VARCHAR(191) UNIQUE`); err != nil {
		if !isAlreadyExists(err) {
			logging.With("error", err.Error()).Warn("migration: add external_id to users failed")
		}
	}
	for _, alt := range []string{
		`ALTER TABLE users ADD COLUMN email VARCHAR(191) NOT NULL DEFAULT ''`,
		`ALTER TABLE users ADD COLUMN type VARCHAR(32) NOT NULL DEFAULT 'human'`,
		`ALTER TABLE users ADD COLUMN last_login_at DATETIME`,
		`ALTER TABLE roles ADD COLUMN is_system INTEGER DEFAULT 0`,
	} {
		if _, err := s.db.Exec(alt); err != nil {
			if !isAlreadyExists(err) {
				logging.With("error", err.Error()).Warn("migration: add column failed: " + alt)
			}
		}
	}
	if err := migrateDatasets(s); err != nil {
		return err
	}
	if err := migrateWorkspaces(s); err != nil {
		return err
	}
	return nil
}

// migrateWorkspaces brings an existing single-tenant deployment forward to the
// multi-tenant model (ADR-001) with zero data loss:
//  1. seed the `default` workspace,
//  2. backfill workspace_members for every existing user
//     (platform admin -> workspace_admin, others -> member, default=true),
//  3. add a NOT NULL DEFAULT 'default' workspace_id column to every governed
//     table so legacy rows stay visible inside the default workspace.
func migrateWorkspaces(s *Store) error {
	if _, err := s.db.Exec(
		`INSERT ` + s.insertIgnore() + ` INTO workspaces (id,name,slug,settings,created_at)
		 VALUES (?,?,?,?,?)`,
		DefaultWorkspaceID, "Default", "default", "{}", time.Now()); err != nil {
		return err
	}
	// Backfill membership for users that are not yet members of default.
	users, err := s.ListUsers("")
	if err != nil {
		return err
	}
	for _, u := range users {
		ok, err := s.IsWorkspaceMember(DefaultWorkspaceID, u.ID)
		if err != nil {
			return err
		}
		if ok {
			continue
		}
		role := WsRoleMember
		if rs, rerr := s.ListRolesForUser(u.ID); rerr == nil {
			for _, r := range rs {
				if r.Name == "admin" {
					role = WsRoleAdmin
				}
			}
		}
		if err := s.AddWorkspaceMember(DefaultWorkspaceID, u.ID, role, true); err != nil {
			return err
		}
	}
	// Add workspace_id discriminator to every governed table. Existing rows
	// inherit 'default' thanks to the NOT NULL DEFAULT clause.
	governed := []string{
		"datasources", "table_permissions", "row_policies", "column_masks",
		"schema_semantics", "data_classifications", "approval_requests",
		"metric_definitions", "audit_logs", "datasets",
	}
	for _, t := range governed {
		if _, err := s.db.Exec(
			`ALTER TABLE ` + t + ` ADD COLUMN workspace_id VARCHAR(191) NOT NULL DEFAULT 'default'`); err != nil {
			if !isAlreadyExists(err) {
				logging.With("error", err.Error(), "table", t).
					Warn("migration: add workspace_id failed")
			}
		}
	}
	return nil
}

func uid() string { return uuid.NewString() }

// Users ----------------------------------------------------------------------

func (s *Store) CreateUser(u *User) error {
	if u.ID == "" {
		u.ID = uid()
	}
	if u.Status == "" {
		u.Status = "active"
	}
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now()
	}
	// external_id is UNIQUE: non-SSO users have no external identity, so
	// bind NULL (which UNIQUE permits many of) instead of an empty string
	// (which would collide on the second local user and silently drop the row).
	var extID interface{}
	if u.ExternalID.Valid {
		extID = u.ExternalID.String
	} else {
		extID = nil
	}
	_, err := s.db.Exec(
		`INSERT INTO users (id,username,display_name,password_hash,external_id,email,type,status,attributes,created_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?)`,
		u.ID, u.Username, u.DisplayName, u.PasswordHash, extID, u.Email, u.Type, u.Status, u.Attributes, u.CreatedAt)
	return err
}

func (s *Store) GetUserByUsername(username string) (*User, error) {
	u := &User{}
	err := s.db.QueryRow(
		`SELECT id,username,display_name,password_hash,external_id,COALESCE(email,'') AS email,COALESCE(type,'human') AS type,status,attributes,last_login_at,created_at
		 FROM users WHERE username=?`, username).
		Scan(&u.ID, &u.Username, &u.DisplayName, &u.PasswordHash, &u.ExternalID, &u.Email, &u.Type, &u.Status, &u.Attributes, &u.LastLoginAt, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

func (s *Store) GetUserByExternalID(externalID string) (*User, error) {
	if externalID == "" {
		return nil, nil
	}
	u := &User{}
	err := s.db.QueryRow(
		`SELECT id,username,display_name,password_hash,external_id,COALESCE(email,'') AS email,COALESCE(type,'human') AS type,status,attributes,last_login_at,created_at
		 FROM users WHERE external_id=?`, externalID).
		Scan(&u.ID, &u.Username, &u.DisplayName, &u.PasswordHash, &u.ExternalID, &u.Email, &u.Type, &u.Status, &u.Attributes, &u.LastLoginAt, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

func (s *Store) GetUser(id string) (*User, error) {
	u := &User{}
	err := s.db.QueryRow(
		`SELECT id,username,display_name,password_hash,external_id,COALESCE(email,'') AS email,COALESCE(type,'human') AS type,status,attributes,last_login_at,created_at
		 FROM users WHERE id=?`, id).
		Scan(&u.ID, &u.Username, &u.DisplayName, &u.PasswordHash, &u.ExternalID, &u.Email, &u.Type, &u.Status, &u.Attributes, &u.LastLoginAt, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

func (s *Store) ListUsers(ws string) ([]*User, error) {
	q := `SELECT id,username,display_name,password_hash,external_id,COALESCE(email,'') AS email,COALESCE(type,'human') AS type,status,attributes,last_login_at,created_at
	      FROM users`
	args := []interface{}{}
	if ws != "" {
		q += ` WHERE id IN (SELECT user_id FROM workspace_members WHERE workspace_id=?)`
		args = append(args, ws)
	}
	q += ` ORDER BY username`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u := &User{}
		if err := rows.Scan(&u.ID, &u.Username, &u.DisplayName, &u.PasswordHash, &u.ExternalID, &u.Email, &u.Type, &u.Status, &u.Attributes, &u.LastLoginAt, &u.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, nil
}

func (s *Store) UpdateUser(u *User) error {
	_, err := s.db.Exec(
		`UPDATE users SET display_name=?, email=?, type=?, status=?, attributes=? WHERE id=?`,
		u.DisplayName, u.Email, u.Type, u.Status, u.Attributes, u.ID)
	return err
}

func (s *Store) SetUserPassword(id, hash string) error {
	_, err := s.db.Exec(`UPDATE users SET password_hash=? WHERE id=?`, hash, id)
	return err
}

// UpdateLastLogin records the timestamp of a user's most recent successful
// authentication (used by the admin user list).
func (s *Store) UpdateLastLogin(id string) error {
	_, err := s.db.Exec(`UPDATE users SET last_login_at=? WHERE id=?`, time.Now(), id)
	return err
}

func (s *Store) DeleteUser(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM user_roles WHERE user_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM users WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) UserAttributes(id string) (map[string]string, error) {
	u, err := s.GetUser(id)
	if err != nil || u == nil {
		return nil, err
	}
	m := map[string]string{}
	if u.Attributes != "" {
		_ = json.Unmarshal([]byte(u.Attributes), &m)
	}
	return m, nil
}

// CreateAPIKey mints a new per-user API key. The plaintext is returned exactly
// once; only its hash and a 12-char prefix are persisted.
func (s *Store) CreateAPIKey(userID, name string, expiresAt time.Time) (plaintext, keyID string, err error) {
	buf := make([]byte, 24)
	if _, err = rand.Read(buf); err != nil {
		return "", "", err
	}
	plaintext = "ak_" + hex.EncodeToString(buf)
	sum := sha256.Sum256([]byte(plaintext))
	keyID = uid()
	now := time.Now()
	var exp interface{}
	if !expiresAt.IsZero() {
		exp = expiresAt
	}
	_, err = s.db.Exec(
		`INSERT INTO api_keys (id,user_id,name,prefix,key_hash,created_at,expires_at,status)
		 VALUES (?,?,?,?,?,?,?,?)`,
		keyID, userID, name, plaintext[:12], hex.EncodeToString(sum[:]), now, exp, "active")
	if err != nil {
		return "", "", err
	}
	return plaintext, keyID, nil
}

// ListAPIKeys returns a user's keys without any secret material.
func (s *Store) ListAPIKeys(userID string) ([]*APIKey, error) {
	rows, err := s.db.Query(
		`SELECT id,user_id,name,prefix,created_at,last_used_at,expires_at,status
		 FROM api_keys WHERE user_id=? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*APIKey, 0)
	for rows.Next() {
		k := &APIKey{}
		var lu, exp sql.NullTime
		if err := rows.Scan(&k.ID, &k.UserID, &k.Name, &k.Prefix, &k.CreatedAt, &lu, &exp, &k.Status); err != nil {
			return nil, err
		}
		if lu.Valid {
			t := lu.Time
			k.LastUsedAt = &t
		}
		if exp.Valid {
			t := exp.Time
			k.ExpiresAt = &t
		}
		out = append(out, k)
	}
	return out, nil
}

// RevokeAPIKey disables a key. It returns ErrNotFound when the key is absent
// or does not belong to the user (so callers can 404 safely).
func (s *Store) RevokeAPIKey(userID, keyID string) error {
	res, err := s.db.Exec(
		`UPDATE api_keys SET status='revoked' WHERE id=? AND user_id=? AND status='active'`,
		keyID, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// LookupAPIKey resolves a presented key to its APIKey + owning User. It
// returns (nil, nil, nil) for unknown/revoked/expired keys.
func (s *Store) LookupAPIKey(key string) (*APIKey, *User, error) {
	if key == "" {
		return nil, nil, nil
	}
	sum := sha256.Sum256([]byte(key))
	hash := hex.EncodeToString(sum[:])
	k := &APIKey{}
	var lu, exp sql.NullTime
	err := s.db.QueryRow(
		`SELECT id,user_id,name,prefix,created_at,last_used_at,expires_at,status
		 FROM api_keys WHERE key_hash=?`, hash).
		Scan(&k.ID, &k.UserID, &k.Name, &k.Prefix, &k.CreatedAt, &lu, &exp, &k.Status)
	if err == sql.ErrNoRows {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if k.Status != "active" {
		return nil, nil, nil
	}
	if exp.Valid && exp.Time.Before(time.Now()) {
		return nil, nil, nil
	}
	if lu.Valid {
		t := lu.Time
		k.LastUsedAt = &t
	}
	if exp.Valid {
		t := exp.Time
		k.ExpiresAt = &t
	}
	u, err := s.GetUser(k.UserID)
	if err != nil {
		return nil, nil, err
	}
	if u == nil {
		return nil, nil, nil
	}
	return k, u, nil
}

// TouchAPIKey records a successful use of a key.
func (s *Store) TouchAPIKey(id string) error {
	_, err := s.db.Exec(`UPDATE api_keys SET last_used_at=? WHERE id=?`, time.Now(), id)
	return err
}

// Roles ----------------------------------------------------------------------

func (s *Store) CreateRole(r *Role) error {
	if r.ID == "" {
		r.ID = uid()
	}
	sys := 0
	if r.System {
		sys = 1
	}
	_, err := s.db.Exec(`INSERT INTO roles (id,name,description,is_system) VALUES (?,?,?,?)`, r.ID, r.Name, r.Description, sys)
	return err
}

func (s *Store) GetRole(name string) (*Role, error) {
	r := &Role{}
	err := s.db.QueryRow(`SELECT id,name,description,is_system FROM roles WHERE name=?`, name).
		Scan(&r.ID, &r.Name, &r.Description, &r.System)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return r, nil
}

func (s *Store) GetRoleByID(id string) (*Role, error) {
	r := &Role{}
	err := s.db.QueryRow(`SELECT id,name,description,is_system FROM roles WHERE id=?`, id).
		Scan(&r.ID, &r.Name, &r.Description, &r.System)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return r, nil
}

func (s *Store) ListRoles() ([]*Role, error) {
	rows, err := s.db.Query(`SELECT id,name,description,is_system FROM roles ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Role
	for rows.Next() {
		r := &Role{}
		if err := rows.Scan(&r.ID, &r.Name, &r.Description, &r.System); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// SetRoleSystem flips the built-in flag on a role (used to backfill system
// roles when an older schema lacked the column).
func (s *Store) SetRoleSystem(id string, system bool) error {
	_, err := s.db.Exec(`UPDATE roles SET is_system=? WHERE id=?`, system, id)
	return err
}

func (s *Store) DeleteRole(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM user_roles WHERE role_id=?`,
		`DELETE FROM table_permissions WHERE role_id=?`,
		`DELETE FROM row_policies WHERE role_id=?`,
		`DELETE FROM column_masks WHERE role_id=?`,
		`DELETE FROM roles WHERE id=?`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}


// UpdateRole changes a role's name and description. Built-in (system) roles
// are protected by the caller, not here.
func (s *Store) UpdateRole(id, name, description string) error {
	_, err := s.db.Exec(`UPDATE roles SET name=?, description=? WHERE id=?`, name, description, id)
	return err
}

func (s *Store) AddUserRole(userID, roleID string) error {
	_, err := s.db.Exec(
		`INSERT ` + s.insertIgnore() + ` INTO user_roles (user_id,role_id) VALUES (?,?)`, userID, roleID)
	return err
}

func (s *Store) RemoveUserRole(userID, roleID string) error {
	_, err := s.db.Exec(`DELETE FROM user_roles WHERE user_id=? AND role_id=?`, userID, roleID)
	return err
}

func (s *Store) ListRolesForUser(userID string) ([]*Role, error) {
	rows, err := s.db.Query(
		`SELECT r.id,r.name,r.description,r.is_system FROM roles r
		 JOIN user_roles ur ON ur.role_id=r.id WHERE ur.user_id=? ORDER BY r.name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Role
	for rows.Next() {
		r := &Role{}
		if err := rows.Scan(&r.ID, &r.Name, &r.Description, &r.System); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// DataSources ----------------------------------------------------------------

func (s *Store) CreateDataSource(ctx context.Context, d *DataSource) error {
	if d.ID == "" {
		d.ID = uid()
	}
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now()
	}
	_, err := s.db.Exec(
		`INSERT INTO datasources (id,name,type,dsn,created_at,workspace_id) VALUES (?,?,?,?,?,?)`,
		d.ID, d.Name, d.Type, d.DSN, d.CreatedAt, WriteWorkspace(ctx))
	return err
}

func (s *Store) GetDataSource(ctx context.Context, id string) (*DataSource, error) {
	d := &DataSource{}
	q := `SELECT id,name,type,dsn,created_at FROM datasources WHERE id=?`
	args := []interface{}{id}
	if !CrossesWorkspaces(ctx) {
		q += ` AND workspace_id=?`
		args = append(args, WorkspaceID(ctx))
	}
	err := s.db.QueryRow(q, args...).
		Scan(&d.ID, &d.Name, &d.Type, &d.DSN, &d.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return d, nil
}

func (s *Store) ListDataSources(ctx context.Context) ([]*DataSource, error) {
	q := `SELECT id,name,type,dsn,created_at FROM datasources`
	args := []interface{}{}
	if !CrossesWorkspaces(ctx) {
		q += ` WHERE workspace_id=?`
		args = append(args, WorkspaceID(ctx))
	}
	q += ` ORDER BY name`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*DataSource
	for rows.Next() {
		d := &DataSource{}
		if err := rows.Scan(&d.ID, &d.Name, &d.Type, &d.DSN, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

func (s *Store) UpdateDataSource(d *DataSource) error {
	_, err := s.db.Exec(`UPDATE datasources SET name=?, type=?, dsn=? WHERE id=?`,
		d.Name, d.Type, d.DSN, d.ID)
	return err
}

func (s *Store) DeleteDataSource(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM table_permissions WHERE datasource_id=?`,
		`DELETE FROM row_policies WHERE datasource_id=?`,
		`DELETE FROM column_masks WHERE datasource_id=?`,
		`DELETE FROM schema_semantics WHERE datasource_id=?`,
		`DELETE FROM data_classifications WHERE datasource_id=?`,
		`DELETE FROM datasets WHERE datasource_id=?`,
		`DELETE FROM datasources WHERE id=?`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Table permissions ----------------------------------------------------------

func (s *Store) CreateTablePermission(ctx context.Context, p *TablePermission) error {
	if p.ID == "" {
		p.ID = uid()
	}
	_, err := s.db.Exec(
		`INSERT INTO table_permissions (id,role_id,datasource_id,table_name,ops,allowed_cols,denied_cols,workspace_id)
		 VALUES (?,?,?,?,?,?,?,?)`,
		p.ID, p.RoleID, p.DataSourceID, p.TableName, p.Ops, p.AllowedCols, p.DeniedCols, WriteWorkspace(ctx))
	return err
}

// ListTablePermissions returns permissions for a role+datsource; tableName "" means all.
// An empty roleID selects all roles (consistent with ListColumnMasks). Results are
// scoped to the active workspace from ctx (platform admin may pass WorkspaceAll).
func (s *Store) ListTablePermissions(ctx context.Context, roleID, dsID, tableName string) ([]*TablePermission, error) {
	q := `SELECT id,role_id,datasource_id,table_name,ops,allowed_cols,denied_cols
	      FROM table_permissions WHERE datasource_id=?`
	args := []interface{}{dsID}
	if !CrossesWorkspaces(ctx) {
		q += ` AND workspace_id=?`
		args = append(args, WorkspaceID(ctx))
	}
	if roleID != "" {
		q += ` AND role_id=?`
		args = append(args, roleID)
	}
	if tableName != "" {
		q += ` AND table_name=?`
		args = append(args, tableName)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*TablePermission
	for rows.Next() {
		p := &TablePermission{}
		if err := rows.Scan(&p.ID, &p.RoleID, &p.DataSourceID, &p.TableName, &p.Ops, &p.AllowedCols, &p.DeniedCols); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func (s *Store) DeleteTablePermission(id string) error {
	_, err := s.db.Exec(`DELETE FROM table_permissions WHERE id=?`, id)
	return err
}

// Row policies ---------------------------------------------------------------

func (s *Store) CreateRowPolicy(ctx context.Context, p *RowPolicy) error {
	if p.ID == "" {
		p.ID = uid()
	}
	_, err := s.db.Exec(
		`INSERT INTO row_policies (id,role_id,datasource_id,table_name,predicate,priority,workspace_id)
		 VALUES (?,?,?,?,?,?,?)`,
		p.ID, p.RoleID, p.DataSourceID, p.TableName, p.Predicate, p.Priority, WriteWorkspace(ctx))
	return err
}

// ListRowPolicies returns policies for a role+datsource; table "" means all tables.
// An empty roleID selects all roles. Scoped to the active workspace from ctx.
func (s *Store) ListRowPolicies(ctx context.Context, roleID, dsID, table string) ([]*RowPolicy, error) {
	q := `SELECT id,role_id,datasource_id,table_name,predicate,priority
	      FROM row_policies WHERE datasource_id=?`
	args := []interface{}{dsID}
	if !CrossesWorkspaces(ctx) {
		q += ` AND workspace_id=?`
		args = append(args, WorkspaceID(ctx))
	}
	if roleID != "" {
		q += ` AND role_id=?`
		args = append(args, roleID)
	}
	if table != "" {
		q += ` AND table_name=?`
		args = append(args, table)
	}
	q += ` ORDER BY priority`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*RowPolicy
	for rows.Next() {
		p := &RowPolicy{}
		if err := rows.Scan(&p.ID, &p.RoleID, &p.DataSourceID, &p.TableName, &p.Predicate, &p.Priority); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func (s *Store) DeleteRowPolicy(id string) error {
	_, err := s.db.Exec(`DELETE FROM row_policies WHERE id=?`, id)
	return err
}

// Column masks ---------------------------------------------------------------

// UpsertColumnMask inserts or updates a masking rule, keyed by the
// (role, datasource, table, column) natural key.
func (s *Store) UpsertColumnMask(ctx context.Context, p *ColumnMask) error {
	ws := WriteWorkspace(ctx)
	if p.ID == "" {
		var found string
		err := s.db.QueryRow(
			`SELECT id FROM column_masks WHERE role_id=? AND datasource_id=? AND table_name=? AND column_name=? AND workspace_id=?`,
			p.RoleID, p.DataSourceID, p.TableName, p.ColumnName, ws).Scan(&found)
		if err == nil {
			p.ID = found
		} else {
			p.ID = uid()
		}
	}
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = time.Now()
	}
	_, err := s.db.Exec(
		`INSERT INTO column_masks (id,role_id,datasource_id,table_name,column_name,strategy,keep,updated_at,workspace_id)
		 VALUES (?,?,?,?,?,?,?,?,?)
		 ` + s.upsertSuffix("id", []string{"role_id","datasource_id","table_name","column_name","strategy","keep","updated_at","workspace_id"}) ,
		p.ID, p.RoleID, p.DataSourceID, p.TableName, p.ColumnName, p.Strategy, p.Keep, p.UpdatedAt, ws)
	return err
}

// ListColumnMasks returns masking rules. An empty roleID selects all roles.
// An empty table selects all tables. Results are ordered for stability.
// Scoped to the active workspace from ctx.
func (s *Store) ListColumnMasks(ctx context.Context, roleID, dsID, table string) ([]*ColumnMask, error) {
	q := `SELECT id,role_id,datasource_id,table_name,column_name,strategy,keep,updated_at
	      FROM column_masks WHERE datasource_id=?`
	args := []interface{}{dsID}
	if !CrossesWorkspaces(ctx) {
		q += ` AND workspace_id=?`
		args = append(args, WorkspaceID(ctx))
	}
	if roleID != "" {
		q += ` AND role_id=?`
		args = append(args, roleID)
	}
	if table != "" {
		q += ` AND table_name=?`
		args = append(args, table)
	}
	q += ` ORDER BY table_name, column_name`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ColumnMask
	for rows.Next() {
		m := &ColumnMask{}
		if err := rows.Scan(&m.ID, &m.RoleID, &m.DataSourceID, &m.TableName, &m.ColumnName, &m.Strategy, &m.Keep, &m.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

func (s *Store) DeleteColumnMask(id string) error {
	_, err := s.db.Exec(`DELETE FROM column_masks WHERE id=?`, id)
	return err
}

// Permission resolution ------------------------------------------------------

// ResolvePermissions aggregates all of a user's roles into a per-table
// governance view for the given datasource. Default policy is deny: a table
// only appears here if at least one role grants an operation on it.
func (s *Store) ResolvePermissions(ctx context.Context, userID, dsID string) (map[string]*TableEffective, error) {
	roles, err := s.ListRolesForUser(userID)
	if err != nil {
		return nil, err
	}
	type agg struct {
		ops        map[string]bool
		allowed    [][]string
		allowedAll bool
		denied     map[string]bool
		policies   []string
		masks      map[string]MaskSpec // keyed by lower column name
	}
	m := map[string]*agg{}
	get := func(t string) *agg {
		if a, ok := m[t]; ok {
			return a
		}
		a := &agg{ops: map[string]bool{}, denied: map[string]bool{}, masks: map[string]MaskSpec{}}
		m[t] = a
		return a
	}
	for _, r := range roles {
		perms, err := s.ListTablePermissions(ctx, r.ID, dsID, "")
		if err != nil {
			return nil, err
		}
		for _, p := range perms {
			a := get(p.TableName)
			for _, op := range strings.Split(p.Ops, ",") {
				op = strings.TrimSpace(strings.ToUpper(op))
				if op != "" {
					a.ops[op] = true
				}
			}
			if strings.TrimSpace(p.AllowedCols) == "" {
				a.allowedAll = true
			} else {
				var cols []string
				if err := json.Unmarshal([]byte(p.AllowedCols), &cols); err == nil {
					a.allowed = append(a.allowed, cols)
				}
			}
			if strings.TrimSpace(p.DeniedCols) != "" {
				var cols []string
				if err := json.Unmarshal([]byte(p.DeniedCols), &cols); err == nil {
					for _, c := range cols {
						a.denied[strings.ToLower(c)] = true
					}
				}
			}
		}
		pols, err := s.ListRowPolicies(ctx, r.ID, dsID, "")
		if err != nil {
			return nil, err
		}
		for _, p := range pols {
			a := get(p.TableName)
			a.policies = append(a.policies, p.Predicate)
		}
		masks, err := s.ListColumnMasks(ctx, r.ID, dsID, "")
		if err != nil {
			return nil, err
		}
		for _, mk := range masks {
			a := get(mk.TableName)
			// First role (in ListRolesForUser order) to define a mask for a
			// column wins; later roles cannot unmask it. Deterministic.
			if _, exists := a.masks[strings.ToLower(mk.ColumnName)]; !exists {
				a.masks[strings.ToLower(mk.ColumnName)] = MaskSpec{
					Column:   mk.ColumnName,
					Strategy: mk.Strategy,
					Keep:     mk.Keep,
				}
			}
		}
	}

	out := map[string]*TableEffective{}
	for name, a := range m {
		allowed := []string{}
		if !a.allowedAll && len(a.allowed) > 0 {
			set := map[string]bool{}
			for _, c := range a.allowed[0] {
				set[strings.ToLower(c)] = true
			}
			for _, roleCols := range a.allowed[1:] {
				ns := map[string]bool{}
				for _, c := range roleCols {
					if set[strings.ToLower(c)] {
						ns[strings.ToLower(c)] = true
					}
				}
				set = ns
			}
			for c := range set {
				allowed = append(allowed, c)
			}
		}
		denied := make([]string, 0, len(a.denied))
		for c := range a.denied {
			denied = append(denied, c)
		}
		masks := make([]MaskSpec, 0, len(a.masks))
		for _, ms := range a.masks {
			masks = append(masks, ms)
		}
		out[strings.ToLower(name)] = &TableEffective{
			TableName:   name,
			Ops:         a.ops,
			AllowedCols: allowed,
			DeniedCols:  denied,
			RowPolicies: a.policies,
			Masks:       masks,
		}
	}
	return out, nil
}
