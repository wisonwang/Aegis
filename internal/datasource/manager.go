package datasource

import (
	"database/sql"
	"fmt"
	"strings"
	"sync"

	"github.com/fosun/aegis/internal/store"
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
}

func NewManager(s *store.Store) *Manager {
	return &Manager{store: s, pools: map[string]*sql.DB{}}
}

// Get returns a pooled connection for the datasource, opening it on first use.
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
	ds, err := m.store.GetDataSource(dsID)
	if err != nil {
		return nil, err
	}
	if ds == nil {
		return nil, fmt.Errorf("datasource %q not found", dsID)
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

// ListTables returns the physical tables of a datasource (used to present
// governance targets in the admin UI).
func (m *Manager) ListTables(ds *store.DataSource) ([]string, error) {
	db, err := m.Get(ds.ID)
	if err != nil {
		return nil, err
	}
	var query string
	switch strings.ToLower(ds.Type) {
	case "mysql":
		query = `SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE() AND table_type='BASE TABLE' ORDER BY table_name`
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
	return out, nil
}

// DescribeTable returns column metadata for a table.
func (m *Manager) DescribeTable(ds *store.DataSource, table string) ([]ColumnMeta, error) {
	db, err := m.Get(ds.ID)
	if err != nil {
		return nil, err
	}
	var query string
	switch strings.ToLower(ds.Type) {
	case "mysql":
		query = `SELECT column_name, data_type, is_nullable, column_key FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name=? ORDER BY ordinal_position`
	case "postgres":
		query = `SELECT column_name, data_type, is_nullable, '' FROM information_schema.columns WHERE table_schema='public' AND table_name=? ORDER BY ordinal_position`
	case "sqlite":
		query = `SELECT name, type, 'YES', '' FROM pragma_table_info(?)`
	default:
		return nil, fmt.Errorf("unsupported datasource type %q", ds.Type)
	}
	rows, err := db.Query(query, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ColumnMeta
	for rows.Next() {
		var c ColumnMeta
		if err := rows.Scan(&c.Name, &c.Type, &c.Nullable, &c.Key); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// ColumnMeta describes a single column of a table.
type ColumnMeta struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable string `json:"nullable"`
	Key      string `json:"key"`
}

func driverName(t string) string {
	switch strings.ToLower(t) {
	case "mysql":
		return "mysql"
	case "postgres", "postgresql":
		return "postgres"
	default:
		return "sqlite"
	}
}
