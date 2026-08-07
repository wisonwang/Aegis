package datasource

import (
	"context"
	"database/sql"
	"fmt"
	"sync"

	"github.com/wisonwang/aegis/internal/store"
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	_ "modernc.org/sqlite"
)

// Manager lazily opens and pools *sql.DB connections for each registered
// datasource. The platform, not the caller, owns the real database
// credentials; applications never connect to the backend directly.
type Manager struct {
	store *store.Store
	mu    sync.RWMutex
	pools map[string]*sql.DB
	// connectorFn builds the Connector for a NoSQL datasource type. It is
	// overridable by tests to inject a fake backend without a live Mongo/ES.
	connectorFn func(string) (Connector, error)
}

func NewManager(s *store.Store) *Manager {
	return &Manager{store: s, pools: map[string]*sql.DB{}, connectorFn: newConnector}
}

// SetConnectorFunc overrides the connector factory. Used by tests to inject a
// fake NoSQL backend.
func (m *Manager) SetConnectorFunc(fn func(string) (Connector, error)) {
	m.connectorFn = fn
}

// Get returns a pooled *sql.DB connection for a SQL-driver datasource, opening
// it on first use. Trino/Presto (HTTP) and NoSQL backends are not pooled here.
func (m *Manager) Get(dsID string) (*sql.DB, error) {
	m.mu.RLock()
	if db, ok := m.pools[dsID]; ok {
		m.mu.RUnlock()
		return db, nil
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	if db, ok := m.pools[dsID]; ok {
		return db, nil
	}
	// Pool lookup is infrastructure, not authorization: the caller has already
	// passed governance by the time it needs a connection. Using the
	// workspace-scoped GetDataSource here would implicitly pin every pool to
	// the "default" workspace and make datasources in any other workspace
	// permanently unreachable (ADR-0007).
	ds, err := m.store.GetDataSourceByID(dsID)
	if err != nil {
		return nil, err
	}
	if ds == nil {
		return nil, fmt.Errorf("datasource %q not found", dsID)
	}
	if !IsSQLDriverType(ds.Type) {
		return nil, fmt.Errorf("datasource %q is type %q, not a SQL-driver backend", ds.Name, ds.Type)
	}
	db, err := sql.Open(driverName(ds.Type), ds.DSN)
	if err != nil {
		return nil, fmt.Errorf("open datasource %q: %w", ds.Name, err)
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(10)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect datasource %q: %w", ds.Name, err)
	}
	m.pools[dsID] = db
	return db, nil
}

// ExecSQL runs a SQL statement against a SQL-family backend. For pooled
// *sql.DB backends it uses database/sql; for Trino/Presto it uses the HTTP
// statement API. Both normalise to RawResult so the proxy can apply
// governance masking uniformly.
func (m *Manager) ExecSQL(ctx context.Context, ds *store.DataSource, sqlText string, args []interface{}, isRead bool) (*RawResult, int64, error) {
	if IsSQLDriverType(ds.Type) {
		db, err := m.Get(ds.ID)
		if err != nil {
			return nil, 0, err
		}
		if !isRead {
			res, err := db.ExecContext(ctx, sqlText, args...)
			if err != nil {
				return nil, 0, err
			}
			n, _ := res.RowsAffected()
			return &RawResult{}, n, nil
		}
		rows, err := db.QueryContext(ctx, sqlText, args...)
		if err != nil {
			return nil, 0, err
		}
		defer rows.Close()
		raw, err := scanRowsToRaw(rows)
		return raw, 0, err
	}
	if IsTrinoFamily(ds.Type) {
		return execTrino(ctx, ds, sqlText, isRead)
	}
	return nil, 0, fmt.Errorf("unsupported datasource type %q", ds.Type)
}

// ListTables returns the physical tables/collections of a datasource.
func (m *Manager) ListTables(ds *store.DataSource) ([]string, error) {
	if IsTrinoFamily(ds.Type) {
		return trinoListTables(context.Background(), ds)
	}
	if IsNoSQLType(ds.Type) {
		return nil, fmt.Errorf("use NoSQLListTables for %q", ds.Type)
	}
	db, err := m.Get(ds.ID)
	if err != nil {
		return nil, err
	}
	var query string
	switch NormalizeType(ds.Type) {
	case "mysql", "starrocks", "clickhouse":
		query = `SHOW TABLES`
	case "postgres":
		query = `SELECT table_name FROM information_schema.tables WHERE table_schema='public' AND table_type='BASE TABLE' ORDER BY table_name`
	case "sqlite":
		query = `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`
	default:
		return nil, fmt.Errorf("unsupported datasource type %q", ds.Type)
	}
	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// DescribeTable returns column metadata for a table.
func (m *Manager) DescribeTable(ds *store.DataSource, table string) ([]ColumnMeta, error) {
	if IsTrinoFamily(ds.Type) {
		return trinoDescribeTable(context.Background(), ds, table)
	}
	if IsNoSQLType(ds.Type) {
		return nil, fmt.Errorf("use NoSQLDescribeTable for %q", ds.Type)
	}
	if !safeIdent(table) {
		return nil, fmt.Errorf("invalid table identifier %q", table)
	}
	db, err := m.Get(ds.ID)
	if err != nil {
		return nil, err
	}
	var query string
	switch NormalizeType(ds.Type) {
	case "mysql", "starrocks", "clickhouse":
		query = `DESCRIBE ` + table
	case "postgres":
		query = `SELECT column_name, data_type, is_nullable, '' FROM information_schema.columns WHERE table_schema='public' AND table_name=$1 ORDER BY ordinal_position`
	case "sqlite":
		query = `SELECT name, type, 'YES', '' FROM pragma_table_info(?)`
	default:
		return nil, fmt.Errorf("unsupported datasource type %q", ds.Type)
	}
	var rows *sql.Rows
	if NormalizeType(ds.Type) == "sqlite" || NormalizeType(ds.Type) == "postgres" {
		rows, err = db.Query(query, table)
	} else {
		rows, err = db.Query(query)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return describeFromRows(rows)
}

// NoSQLExec runs a governed NoSQL query via the matching Connector.
func (m *Manager) NoSQLExec(ctx context.Context, ds *store.DataSource, payload QueryPayload) (*RawResult, int64, error) {
	c, err := m.connectorFn(ds.Type)
	if err != nil {
		return nil, 0, err
	}
	sess, err := c.Open(ds)
	if err != nil {
		return nil, 0, err
	}
	defer sess.Close()
	return sess.Exec(ctx, payload)
}

// NoSQLWrite runs a governed NoSQL write via the matching Connector.
func (m *Manager) NoSQLWrite(ctx context.Context, ds *store.DataSource, payload WritePayload) (int64, error) {
	c, err := m.connectorFn(ds.Type)
	if err != nil {
		return 0, err
	}
	sess, err := c.Open(ds)
	if err != nil {
		return 0, err
	}
	defer sess.Close()
	return sess.Write(ctx, payload)
}

// NoSQLCount returns the number of documents matching a governed filter.
func (m *Manager) NoSQLCount(ctx context.Context, ds *store.DataSource, payload QueryPayload) (int64, error) {
	c, err := m.connectorFn(ds.Type)
	if err != nil {
		return 0, err
	}
	sess, err := c.Open(ds)
	if err != nil {
		return 0, err
	}
	defer sess.Close()
	return sess.Count(ctx, payload)
}

// NoSQLListTables lists collections/indices of a NoSQL backend.
func (m *Manager) NoSQLListTables(ctx context.Context, ds *store.DataSource) ([]string, error) {
	c, err := m.connectorFn(ds.Type)
	if err != nil {
		return nil, err
	}
	sess, err := c.Open(ds)
	if err != nil {
		return nil, err
	}
	defer sess.Close()
	return sess.ListCollections(ctx)
}

// NoSQLDescribeTable returns field metadata for a collection/index.
func (m *Manager) NoSQLDescribeTable(ctx context.Context, ds *store.DataSource, name string) ([]ColumnMeta, error) {
	c, err := m.connectorFn(ds.Type)
	if err != nil {
		return nil, err
	}
	sess, err := c.Open(ds)
	if err != nil {
		return nil, err
	}
	defer sess.Close()
	return sess.DescribeCollection(ctx, name)
}

// newConnector builds the Connector for a NoSQL datasource type.
func newConnector(t string) (Connector, error) {
	switch NormalizeType(t) {
	case "mongo":
		return &mongoConnector{}, nil
	case "es":
		return &esConnector{}, nil
	}
	return nil, fmt.Errorf("no connector for datasource type %q", t)
}

// ColumnMeta describes a single column of a table.
type ColumnMeta struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable string `json:"nullable"`
	Key      string `json:"key"`
}
