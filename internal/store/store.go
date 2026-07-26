package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

// Models ---------------------------------------------------------------------

type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	DisplayName  string    `json:"display_name"`
	PasswordHash string    `json:"-"`
	ExternalID   string    `json:"external_id"` // OIDC subject / SSO identity
	Status       string    `json:"status"`      // active | disabled
	Attributes   string    `json:"attributes"`  // JSON object, e.g. {"tenant":"acme"}
	CreatedAt    time.Time `json:"created_at"`
}

type Role struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

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
	db *sql.DB
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open store %s: %w", path, err)
	}
	db.SetMaxOpenConns(1) // sqlite single-writer
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY, username TEXT UNIQUE NOT NULL,
			display_name TEXT, password_hash TEXT, external_id TEXT UNIQUE,
			status TEXT, attributes TEXT, created_at DATETIME)`,
		`CREATE TABLE IF NOT EXISTS roles (
			id TEXT PRIMARY KEY, name TEXT UNIQUE NOT NULL, description TEXT)`,
		`CREATE TABLE IF NOT EXISTS user_roles (
			user_id TEXT, role_id TEXT, PRIMARY KEY(user_id, role_id))`,
		`CREATE TABLE IF NOT EXISTS datasources (
			id TEXT PRIMARY KEY, name TEXT UNIQUE NOT NULL, type TEXT,
			dsn TEXT, created_at DATETIME)`,
		`CREATE TABLE IF NOT EXISTS table_permissions (
			id TEXT PRIMARY KEY, role_id TEXT, datasource_id TEXT,
			table_name TEXT, ops TEXT, allowed_cols TEXT, denied_cols TEXT)`,
		`CREATE TABLE IF NOT EXISTS row_policies (
			id TEXT PRIMARY KEY, role_id TEXT, datasource_id TEXT,
			table_name TEXT, predicate TEXT, priority INTEGER)`,
		`CREATE TABLE IF NOT EXISTS column_masks (
			id TEXT PRIMARY KEY, role_id TEXT, datasource_id TEXT,
			table_name TEXT, column_name TEXT, strategy TEXT, keep INTEGER, updated_at DATETIME)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_masks_key
			ON column_masks(role_id, datasource_id, table_name, column_name)`,
		`CREATE TABLE IF NOT EXISTS audit_logs (
			id TEXT PRIMARY KEY, ts DATETIME, user_id TEXT, username TEXT,
			channel TEXT, datasource_id TEXT, datasource TEXT,
			session_id TEXT, sql_text TEXT, rewritten_sql TEXT, status TEXT,
			error TEXT, row_count INTEGER, duration_ms INTEGER)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_ts ON audit_logs(ts DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_user ON audit_logs(username)`,
		`CREATE TABLE IF NOT EXISTS schema_semantics (
			id TEXT PRIMARY KEY, datasource_id TEXT NOT NULL,
			table_name TEXT NOT NULL, column_name TEXT NOT NULL DEFAULT '',
			description TEXT, synonyms TEXT, examples TEXT, updated_at DATETIME)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_semantics_key
			ON schema_semantics(datasource_id, table_name, column_name)`,
		`CREATE TABLE IF NOT EXISTS security_alerts (
			id TEXT PRIMARY KEY, ts DATETIME, level TEXT, rule TEXT,
			principal TEXT, channel TEXT, detail TEXT, resolved INTEGER DEFAULT 0)`,
		`CREATE INDEX IF NOT EXISTS idx_alerts_ts ON security_alerts(ts DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_alerts_principal ON security_alerts(principal)`,
		`CREATE TABLE IF NOT EXISTS data_classifications (
			id TEXT PRIMARY KEY, datasource_id TEXT NOT NULL,
			table_name TEXT NOT NULL, column_name TEXT NOT NULL DEFAULT '',
			level TEXT, tags TEXT, updated_at DATETIME)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_classifications_key
			ON data_classifications(datasource_id, table_name, column_name)`,
	}
	for _, st := range stmts {
		if _, err := s.db.Exec(st); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	// Idempotent migration: add session_id to existing audit_logs tables that
	// were created before this column existed. ALTER errors if the column is
	// already present, which we safely ignore.
	if _, err := s.db.Exec(`ALTER TABLE audit_logs ADD COLUMN session_id TEXT`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column") {
			_ = err
		}
	}
	if _, err := s.db.Exec(`ALTER TABLE users ADD COLUMN external_id TEXT UNIQUE`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column") {
			_ = err
		}
	}
	if err := migrateDatasets(s); err != nil {
		return err
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
	_, err := s.db.Exec(
		`INSERT INTO users (id,username,display_name,password_hash,external_id,status,attributes,created_at)
		 VALUES (?,?,?,?,?,?,?,?)`,
		u.ID, u.Username, u.DisplayName, u.PasswordHash, u.ExternalID, u.Status, u.Attributes, u.CreatedAt)
	return err
}

func (s *Store) GetUserByUsername(username string) (*User, error) {
	u := &User{}
	err := s.db.QueryRow(
		`SELECT id,username,display_name,password_hash,external_id,status,attributes,created_at
		 FROM users WHERE username=?`, username).
		Scan(&u.ID, &u.Username, &u.DisplayName, &u.PasswordHash, &u.ExternalID, &u.Status, &u.Attributes, &u.CreatedAt)
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
		`SELECT id,username,display_name,password_hash,external_id,status,attributes,created_at
		 FROM users WHERE external_id=?`, externalID).
		Scan(&u.ID, &u.Username, &u.DisplayName, &u.PasswordHash, &u.ExternalID, &u.Status, &u.Attributes, &u.CreatedAt)
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
		`SELECT id,username,display_name,password_hash,external_id,status,attributes,created_at
		 FROM users WHERE id=?`, id).
		Scan(&u.ID, &u.Username, &u.DisplayName, &u.PasswordHash, &u.ExternalID, &u.Status, &u.Attributes, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

func (s *Store) ListUsers() ([]*User, error) {
	rows, err := s.db.Query(
		`SELECT id,username,display_name,password_hash,external_id,status,attributes,created_at
		 FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u := &User{}
		if err := rows.Scan(&u.ID, &u.Username, &u.DisplayName, &u.PasswordHash, &u.ExternalID, &u.Status, &u.Attributes, &u.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, nil
}

func (s *Store) UpdateUser(u *User) error {
	_, err := s.db.Exec(
		`UPDATE users SET display_name=?, status=?, attributes=? WHERE id=?`,
		u.DisplayName, u.Status, u.Attributes, u.ID)
	return err
}

func (s *Store) SetUserPassword(id, hash string) error {
	_, err := s.db.Exec(`UPDATE users SET password_hash=? WHERE id=?`, hash, id)
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

// Roles ----------------------------------------------------------------------

func (s *Store) CreateRole(r *Role) error {
	if r.ID == "" {
		r.ID = uid()
	}
	_, err := s.db.Exec(`INSERT INTO roles (id,name,description) VALUES (?,?,?)`, r.ID, r.Name, r.Description)
	return err
}

func (s *Store) GetRole(name string) (*Role, error) {
	r := &Role{}
	err := s.db.QueryRow(`SELECT id,name,description FROM roles WHERE name=?`, name).
		Scan(&r.ID, &r.Name, &r.Description)
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
	err := s.db.QueryRow(`SELECT id,name,description FROM roles WHERE id=?`, id).
		Scan(&r.ID, &r.Name, &r.Description)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return r, nil
}

func (s *Store) ListRoles() ([]*Role, error) {
	rows, err := s.db.Query(`SELECT id,name,description FROM roles ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Role
	for rows.Next() {
		r := &Role{}
		if err := rows.Scan(&r.ID, &r.Name, &r.Description); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
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

func (s *Store) AddUserRole(userID, roleID string) error {
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO user_roles (user_id,role_id) VALUES (?,?)`, userID, roleID)
	return err
}

func (s *Store) RemoveUserRole(userID, roleID string) error {
	_, err := s.db.Exec(`DELETE FROM user_roles WHERE user_id=? AND role_id=?`, userID, roleID)
	return err
}

func (s *Store) ListRolesForUser(userID string) ([]*Role, error) {
	rows, err := s.db.Query(
		`SELECT r.id,r.name,r.description FROM roles r
		 JOIN user_roles ur ON ur.role_id=r.id WHERE ur.user_id=? ORDER BY r.name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Role
	for rows.Next() {
		r := &Role{}
		if err := rows.Scan(&r.ID, &r.Name, &r.Description); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// DataSources ----------------------------------------------------------------

func (s *Store) CreateDataSource(d *DataSource) error {
	if d.ID == "" {
		d.ID = uid()
	}
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now()
	}
	_, err := s.db.Exec(
		`INSERT INTO datasources (id,name,type,dsn,created_at) VALUES (?,?,?,?,?)`,
		d.ID, d.Name, d.Type, d.DSN, d.CreatedAt)
	return err
}

func (s *Store) GetDataSource(id string) (*DataSource, error) {
	d := &DataSource{}
	err := s.db.QueryRow(
		`SELECT id,name,type,dsn,created_at FROM datasources WHERE id=?`, id).
		Scan(&d.ID, &d.Name, &d.Type, &d.DSN, &d.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return d, nil
}

func (s *Store) ListDataSources() ([]*DataSource, error) {
	rows, err := s.db.Query(`SELECT id,name,type,dsn,created_at FROM datasources ORDER BY name`)
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

func (s *Store) CreateTablePermission(p *TablePermission) error {
	if p.ID == "" {
		p.ID = uid()
	}
	_, err := s.db.Exec(
		`INSERT INTO table_permissions (id,role_id,datasource_id,table_name,ops,allowed_cols,denied_cols)
		 VALUES (?,?,?,?,?,?,?)`,
		p.ID, p.RoleID, p.DataSourceID, p.TableName, p.Ops, p.AllowedCols, p.DeniedCols)
	return err
}

// ListTablePermissions returns permissions for a role+datsource; tableName "" means all.
func (s *Store) ListTablePermissions(roleID, dsID, tableName string) ([]*TablePermission, error) {
	q := `SELECT id,role_id,datasource_id,table_name,ops,allowed_cols,denied_cols
	      FROM table_permissions WHERE role_id=? AND datasource_id=?`
	args := []interface{}{roleID, dsID}
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

func (s *Store) CreateRowPolicy(p *RowPolicy) error {
	if p.ID == "" {
		p.ID = uid()
	}
	_, err := s.db.Exec(
		`INSERT INTO row_policies (id,role_id,datasource_id,table_name,predicate,priority)
		 VALUES (?,?,?,?,?,?)`,
		p.ID, p.RoleID, p.DataSourceID, p.TableName, p.Predicate, p.Priority)
	return err
}

// ListRowPolicies returns policies for a role+datsource; table "" means all tables.
func (s *Store) ListRowPolicies(roleID, dsID, table string) ([]*RowPolicy, error) {
	q := `SELECT id,role_id,datasource_id,table_name,predicate,priority
	      FROM row_policies WHERE role_id=? AND datasource_id=?`
	args := []interface{}{roleID, dsID}
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
func (s *Store) UpsertColumnMask(p *ColumnMask) error {
	if p.ID == "" {
		var found string
		err := s.db.QueryRow(
			`SELECT id FROM column_masks WHERE role_id=? AND datasource_id=? AND table_name=? AND column_name=?`,
			p.RoleID, p.DataSourceID, p.TableName, p.ColumnName).Scan(&found)
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
		`INSERT INTO column_masks (id,role_id,datasource_id,table_name,column_name,strategy,keep,updated_at)
		 VALUES (?,?,?,?,?,?,?,?)
		 ON CONFLICT(id) DO UPDATE SET
		   role_id=excluded.role_id, datasource_id=excluded.datasource_id,
		   table_name=excluded.table_name, column_name=excluded.column_name,
		   strategy=excluded.strategy, keep=excluded.keep, updated_at=excluded.updated_at`,
		p.ID, p.RoleID, p.DataSourceID, p.TableName, p.ColumnName, p.Strategy, p.Keep, p.UpdatedAt)
	return err
}

// ListColumnMasks returns masking rules. An empty roleID selects all roles.
// An empty table selects all tables. Results are ordered for stability.
func (s *Store) ListColumnMasks(roleID, dsID, table string) ([]*ColumnMask, error) {
	q := `SELECT id,role_id,datasource_id,table_name,column_name,strategy,keep,updated_at
	      FROM column_masks WHERE datasource_id=?`
	args := []interface{}{dsID}
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
func (s *Store) ResolvePermissions(userID, dsID string) (map[string]*TableEffective, error) {
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
		perms, err := s.ListTablePermissions(r.ID, dsID, "")
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
		pols, err := s.ListRowPolicies(r.ID, dsID, "")
		if err != nil {
			return nil, err
		}
		for _, p := range pols {
			a := get(p.TableName)
			a.policies = append(a.policies, p.Predicate)
		}
		masks, err := s.ListColumnMasks(r.ID, dsID, "")
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
