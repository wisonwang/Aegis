package proxy

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/fosun/aegis/internal/auth"
	"github.com/fosun/aegis/internal/datasource"
	"github.com/fosun/aegis/internal/permission"
	"github.com/fosun/aegis/internal/store"
)

// QueryResult is the governed, safe result returned to a caller.
type QueryResult struct {
	Columns      []string               `json:"columns"`
	Rows         []map[string]interface{} `json:"rows"`
	AffectedRows int64                  `json:"affected_rows,omitempty"`
	RewrittenSQL string                 `json:"rewritten_sql"`
	Truncated    bool                   `json:"truncated,omitempty"` // rows were cut at the MaxRows cap
}

// TableInfo describes a table the principal may access.
type TableInfo struct {
	Name string   `json:"name"`
	Ops  []string `json:"ops"`
}

// Proxy executes SQL on behalf of a platform principal, enforcing governance
// via the permission engine and the datasource connection pools.
type Proxy struct {
	store *store.Store
	ds    *datasource.Manager
	guard *Guard // nil = behavior limits disabled
}

func New(store *store.Store, ds *datasource.Manager) *Proxy {
	return &Proxy{store: store, ds: ds}
}

// SetGuard installs AI-behavior limits (max rows / timeout / rate limit).
func (p *Proxy) SetGuard(g *Guard) { p.guard = g }

// Channel context ------------------------------------------------------------

type ctxKey int

const channelKey ctxKey = 1

// WithChannel tags the request context with the access channel ("dataapi" or
// "mcp") so audit entries record where a query came from.
func WithChannel(ctx context.Context, channel string) context.Context {
	return context.WithValue(ctx, channelKey, channel)
}

func channelFrom(ctx context.Context) string {
	if v, ok := ctx.Value(channelKey).(string); ok && v != "" {
		return v
	}
	return "dataapi"
}

// audit persists one governed-query trace. Failures to write audit never
// affect the caller's result; they are best-effort but synchronous (SQLite is
// local and fast) so entries are never lost on process exit.
func (p *Proxy) audit(ctx context.Context, dsID string, claims *auth.Claims, sqlText, rewritten, status, errMsg string, rowCount int, started time.Time) {
	dsName := ""
	if ds, err := p.store.GetDataSource(dsID); err == nil && ds != nil {
		dsName = ds.Name
	}
	_ = p.store.InsertAudit(&store.AuditLog{
		UserID:       claims.UserID,
		Username:     claims.Username,
		Channel:      channelFrom(ctx),
		DataSourceID: dsID,
		DataSource:   dsName,
		SQLText:      sqlText,
		RewrittenSQL: rewritten,
		Status:       status,
		Error:        errMsg,
		RowCount:     rowCount,
		DurationMS:   time.Since(started).Milliseconds(),
	})
}

// Execute runs a SQL statement under the principal's permissions and returns a
// governed result. Parameterized args are forwarded to the backend untouched.
// Every call — allowed, denied or failed — leaves an audit trail.
func (p *Proxy) Execute(ctx context.Context, dsID string, claims *auth.Claims, sql string, args ...interface{}) (*QueryResult, error) {
	started := time.Now()

	// Behavior governance: rate limit + per-query timeout (admin may be exempt).
	limited := p.guard != nil && !(p.guard.AdminExempt && claims.IsAdmin())
	if limited {
		if !p.guard.Allow(claims.UserID) {
			err := fmt.Errorf("rate limit exceeded: max %d queries/min per principal", p.guard.RatePerMin)
			p.audit(ctx, dsID, claims, sql, "", "denied", err.Error(), 0, started)
			return nil, err
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.guard.Timeout)
		defer cancel()
	}

	perms, err := p.store.ResolvePermissions(claims.UserID, dsID)
	if err != nil {
		p.audit(ctx, dsID, claims, sql, "", "error", err.Error(), 0, started)
		return nil, err
	}
	rr, err := permission.Rewrite(sql, perms, claims.Attributes, claims.IsAdmin())
	if err != nil {
		// Governance rejection (no table grant, forbidden op, parse refusal).
		p.audit(ctx, dsID, claims, sql, "", "denied", err.Error(), 0, started)
		return nil, err
	}

	db, err := p.ds.Get(dsID)
	if err != nil {
		p.audit(ctx, dsID, claims, sql, rr.SQL, "error", err.Error(), 0, started)
		return nil, err
	}

	// Write protections (behavior governance). Gated by `limited`, so they
	// inherit admin exemption and only apply when a Guard is installed.
	if limited && !rr.IsRead {
		if !rr.WriteHasWhere && !p.guard.AllowNoWhere {
			err := fmt.Errorf("UPDATE/DELETE without WHERE is not permitted (row-policy-bounded writes are allowed; set allow_no_where_writes to override)")
			p.audit(ctx, dsID, claims, sql, rr.SQL, "denied", err.Error(), 0, started)
			return nil, err
		}
		if p.guard.MaxAffectedRows > 0 && rr.CountCheckSQL != "" {
			n, cerr := p.countAffected(ctx, db, rr.CountCheckSQL)
			if cerr != nil {
				err := fmt.Errorf("cannot estimate affected rows: %w", cerr)
				p.audit(ctx, dsID, claims, sql, rr.SQL, "error", err.Error(), 0, started)
				return nil, err
			}
			if n > int64(p.guard.MaxAffectedRows) {
				err := fmt.Errorf("write would affect %d rows, exceeding max_affected_rows=%d", n, p.guard.MaxAffectedRows)
				p.audit(ctx, dsID, claims, sql, rr.SQL, "denied", err.Error(), 0, started)
				return nil, err
			}
		}
	}

	if !rr.IsRead {
		res, err := db.ExecContext(ctx, rr.SQL, args...)
		if err != nil {
			p.audit(ctx, dsID, claims, sql, rr.SQL, "error", err.Error(), 0, started)
			return nil, fmt.Errorf("execute: %w", err)
		}
		n, _ := res.RowsAffected()
		p.audit(ctx, dsID, claims, sql, rr.SQL, "ok", "", int(n), started)
		return &QueryResult{AffectedRows: n, RewrittenSQL: rr.SQL}, nil
	}

	rows, err := db.QueryContext(ctx, rr.SQL, args...)
	if err != nil {
		p.audit(ctx, dsID, claims, sql, rr.SQL, "error", err.Error(), 0, started)
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		p.audit(ctx, dsID, claims, sql, rr.SQL, "error", err.Error(), 0, started)
		return nil, err
	}
	keep, outCols := maskColumns(cols, rr.DeniedCols, rr.AllowedCols)

	maxRows := 0
	if limited {
		maxRows = p.guard.MaxRows
	}
	truncated := false
	out := []map[string]interface{}{}
	for rows.Next() {
		if maxRows > 0 && len(out) >= maxRows {
			truncated = true
			break
		}
		scan := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range scan {
			ptrs[i] = &scan[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			p.audit(ctx, dsID, claims, sql, rr.SQL, "error", err.Error(), 0, started)
			return nil, err
		}
		row := map[string]interface{}{}
		for i, c := range cols {
			if keep[i] {
				row[c] = normalizeValue(scan[i])
			}
		}
		out = append(out, row)
	}
	if !truncated {
		if err := rows.Err(); err != nil {
			p.audit(ctx, dsID, claims, sql, rr.SQL, "error", err.Error(), 0, started)
			return nil, err
		}
	}
	note := ""
	if truncated {
		note = fmt.Sprintf("result truncated at max_rows=%d", maxRows)
	}
	p.audit(ctx, dsID, claims, sql, rr.SQL, "ok", note, len(out), started)
	return &QueryResult{Columns: outCols, Rows: out, RewrittenSQL: rr.SQL, Truncated: truncated}, nil
}

// ListTables returns the tables a principal may access on a datasource,
// together with the operations granted.
func (p *Proxy) ListTables(ctx context.Context, dsID string, claims *auth.Claims) ([]TableInfo, error) {
	ds, err := p.store.GetDataSource(dsID)
	if err != nil {
		return nil, err
	}
	if ds == nil {
		return nil, fmt.Errorf("datasource not found")
	}
	physical, err := p.ds.ListTables(ds)
	if err != nil {
		return nil, err
	}
	perms, err := p.store.ResolvePermissions(claims.UserID, dsID)
	if err != nil {
		return nil, err
	}
	var out []TableInfo
	for _, t := range physical {
		if claims.IsAdmin() {
			out = append(out, TableInfo{Name: t, Ops: allOps()})
			continue
		}
		eff, ok := perms[strings.ToLower(t)]
		if !ok {
			continue
		}
		out = append(out, TableInfo{Name: t, Ops: opsList(eff.Ops)})
	}
	return out, nil
}

// DescribeTable returns column metadata for a table, with denied/non-allowed
// columns removed per the principal's governance.
func (p *Proxy) DescribeTable(ctx context.Context, dsID, table string, claims *auth.Claims) ([]datasource.ColumnMeta, error) {
	ds, err := p.store.GetDataSource(dsID)
	if err != nil {
		return nil, err
	}
	if ds == nil {
		return nil, fmt.Errorf("datasource not found")
	}
	cols, err := p.ds.DescribeTable(ds, table)
	if err != nil {
		return nil, err
	}
	if claims.IsAdmin() {
		return cols, nil
	}
	perms, err := p.store.ResolvePermissions(claims.UserID, dsID)
	if err != nil {
		return nil, err
	}
	eff := perms[strings.ToLower(table)]
	if eff == nil {
		return nil, fmt.Errorf("access denied to table %q", table)
	}
	denied := toSet(eff.DeniedCols)
	allowSet := toSet(eff.AllowedCols)
	allowActive := len(eff.AllowedCols) > 0
	out := cols[:0]
	for _, c := range cols {
		lc := strings.ToLower(c.Name)
		if denied[lc] {
			continue
		}
		if allowActive && !allowSet[lc] {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

// maskColumns returns which result columns to keep, applying column governance.
func maskColumns(cols, denied, allowed []string) ([]bool, []string) {
	deniedSet := toSet(denied)
	allowSet := toSet(allowed)
	allowActive := len(allowed) > 0
	keep := make([]bool, len(cols))
	outCols := make([]string, 0, len(cols))
	for i, c := range cols {
		lc := strings.ToLower(c)
		if deniedSet[lc] {
			keep[i] = false
			continue
		}
		if allowActive && !allowSet[lc] {
			keep[i] = false
			continue
		}
		keep[i] = true
		outCols = append(outCols, c)
	}
	return keep, outCols
}

// countAffected runs a portable SELECT COUNT(*) pre-check for a write statement
// so the proxy can reject over-cap writes before executing them.
func (p *Proxy) countAffected(ctx context.Context, db *sql.DB, sql string) (int64, error) {
	rows, err := db.QueryContext(ctx, sql)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	if !rows.Next() {
		return 0, fmt.Errorf("count returned no rows")
	}
	var n int64
	if err := rows.Scan(&n); err != nil {
		return 0, err
	}
	return n, rows.Err()
}

func normalizeValue(v interface{}) interface{} {
	switch x := v.(type) {
	case []byte:
		return string(x)
	case nil:
		return nil
	default:
		return x
	}
}

func toSet(in []string) map[string]bool {
	s := map[string]bool{}
	for _, v := range in {
		s[strings.ToLower(v)] = true
	}
	return s
}

func allOps() []string { return []string{"SELECT", "INSERT", "UPDATE", "DELETE"} }

func opsList(ops map[string]bool) []string {
	out := []string{}
	for _, o := range []string{"SELECT", "INSERT", "UPDATE", "DELETE"} {
		if ops[o] {
			out = append(out, o)
		}
	}
	return out
}
