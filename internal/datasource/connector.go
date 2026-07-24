package datasource

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/wisonwang/aegis/internal/store"
)

// RawResult is a backend-agnostic query result: ordered column names plus rows
// keyed by column name. Both SQL-family backends (*sql.DB / Trino HTTP) and
// NoSQL connectors (Mongo / Elasticsearch) normalise to this shape so the proxy
// can apply column governance and value masking uniformly.
type RawResult struct {
	Columns []string
	Rows    []map[string]interface{}
}

// Connector abstracts a non-SQL backend (Mongo, Elasticsearch). SQL-family
// backends are handled directly by *sql.DB + the permission engine and do not
// use this interface.
type Connector interface {
	// Kind returns the normalised datasource type this connector serves.
	Kind() string
	// Open establishes a session against the backend described by ds.
	Open(ds *store.DataSource) (Session, error)
}

// Session executes already-governed, backend-specific queries. The proxy is
// responsible for applying table/column governance *before* calling Exec; the
// connector only runs the governed query and returns raw rows.
type Session interface {
	// Exec runs a governed query and returns normalised rows plus affected rows
	// (0 when the backend does not report them, e.g. search/read APIs).
	Exec(ctx context.Context, payload QueryPayload) (*RawResult, int64, error)
	// ListCollections returns the physical collections/indices of the backend.
	ListCollections(ctx context.Context) ([]string, error)
	// DescribeCollection returns column-like metadata for a collection/index.
	DescribeCollection(ctx context.Context, name string) ([]ColumnMeta, error)
	Close() error
}

// QueryPayload carries a backend-specific query. For Mongo it is a JSON document
// describing collection/filter/projection/...; for ES it is a JSON search body.
type QueryPayload struct {
	Raw json.RawMessage
}

// ---- driver / type classification -----------------------------------------

// sqlDriverTypes maps a datasource type to its database/sql driver name.
// StarRocks and ClickHouse both speak the MySQL wire protocol, so they reuse
// the existing mysql driver with no new dependency.
var sqlDriverTypes = map[string]string{
	"mysql":      "mysql",
	"postgres":   "postgres",
	"postgresql": "postgres",
	"sqlite":     "sqlite",
	"starrocks":  "mysql",
	"clickhouse": "mysql",
}

// driverName resolves the database/sql driver for a datasource type.
func driverName(t string) string {
	if d, ok := sqlDriverTypes[strings.ToLower(t)]; ok {
		return d
	}
	return "sqlite"
}

// IsSQLDriverType reports whether the type is served by a pooled *sql.DB.
func IsSQLDriverType(t string) bool {
	_, ok := sqlDriverTypes[strings.ToLower(t)]
	return ok
}

// IsTrinoFamily reports whether the type is a Trino/Presto HTTP endpoint
// (ANSI-SQL over the /v1/statement REST API).
func IsTrinoFamily(t string) bool {
	switch strings.ToLower(t) {
	case "trino", "presto", "prestosql":
		return true
	}
	return false
}

// IsSQLFamily reports whether governance is applied via SQL rewriting
// (mysql/postgres/sqlite/starrocks/clickhouse/trino/presto).
func IsSQLFamily(t string) bool {
	return IsSQLDriverType(t) || IsTrinoFamily(t)
}

// IsNoSQLType reports whether the type is a document/search backend served by a
// dedicated Connector (Mongo / Elasticsearch).
func IsNoSQLType(t string) bool {
	switch strings.ToLower(t) {
	case "mongo", "mongodb", "es", "elasticsearch":
		return true
	}
	return false
}

// IsNoSQL is an alias for IsNoSQLType, used by callers that dispatch on the
// datasource type generically.
func IsNoSQL(t string) bool { return IsNoSQLType(t) }

// NormalizeType collapses aliases to the canonical internal type name.
func NormalizeType(t string) string {
	switch strings.ToLower(t) {
	case "postgresql":
		return "postgres"
	case "mongodb":
		return "mongo"
	case "elasticsearch":
		return "es"
	case "prestosql":
		return "presto"
	}
	return strings.ToLower(t)
}

// KnownTypes lists every datasource type Aegis can register.
func KnownTypes() []string {
	return []string{"mysql", "postgres", "sqlite", "starrocks", "clickhouse", "trino", "presto", "mongo", "es"}
}

// ---- helpers ----------------------------------------------------------------

// normalizeCell converts driver values into JSON-friendly types. []byte is
// decoded to a string because backends return TEXT/BLOB as bytes.
func normalizeCell(v interface{}) interface{} {
	switch x := v.(type) {
	case []byte:
		return string(x)
	case nil:
		return nil
	default:
		return x
	}
}

// scanRowsToRaw reads a *sql.Rows into a RawResult.
func scanRowsToRaw(rows *sql.Rows) (*RawResult, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	raw := &RawResult{Columns: cols}
	for rows.Next() {
		scan := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range scan {
			ptrs[i] = &scan[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]interface{}, len(cols))
		for i, c := range cols {
			row[c] = normalizeCell(scan[i])
		}
		raw.Rows = append(raw.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return raw, nil
}

// describeFromRows maps a driver-agnostic DESCRIBE result (columns: name, type,
// [nullable], [key], ...) into ColumnMeta. Positional, so it tolerates the
// differing column counts of MySQL/StarRocks/ClickHouse/Postgres.
func describeFromRows(rows *sql.Rows) ([]ColumnMeta, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []ColumnMeta
	for rows.Next() {
		vals := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		get := func(i int) string {
			if i >= len(vals) || vals[i] == nil {
				return ""
			}
			return strings.TrimSpace(toString(vals[i]))
		}
		out = append(out, ColumnMeta{
			Name:     get(0),
			Type:     get(1),
			Nullable: get(2),
			Key:      get(3),
		})
	}
	return out, rows.Err()
}

func toString(v interface{}) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	case nil:
		return ""
	default:
		return jsonString(x)
	}
}

func jsonString(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// safeIdent rejects identifiers containing anything but alphanumerics,
// underscores and dots, guarding internal DESCRIBE calls against injection.
func safeIdent(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r == '_' || r == '.' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}
