package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/wisonwang/aegis/internal/auth"
	"github.com/wisonwang/aegis/internal/datasource"
	"github.com/wisonwang/aegis/internal/metrics"
	"github.com/wisonwang/aegis/internal/permission"
	"github.com/wisonwang/aegis/internal/store"
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

// Proxy executes queries on behalf of a platform principal, enforcing
// governance via the permission engine and the datasource connection pools.
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
	// Observability: every governed outcome (ok/denied/error) is a data point.
	ch := channelFrom(ctx)
	metrics.RecordQuery(ch, status, time.Since(started))
	metrics.RecordRows(ch, status, rowCount)
}

// Execute runs a statement under the principal's permissions and returns a
// governed result. For SQL-family backends `sql` is a SQL string; for NoSQL
// backends it is the backend-specific JSON query document. Parameterized args
// are forwarded to SQL backends untouched. Every call leaves an audit trail.
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

	ds, err := p.store.GetDataSource(dsID)
	if err != nil || ds == nil {
		p.audit(ctx, dsID, claims, sql, "", "error", "datasource not found", 0, started)
		return nil, fmt.Errorf("datasource not found")
	}

	if datasource.IsNoSQL(ds.Type) {
		return p.executeNoSQL(ctx, ds, claims, json.RawMessage(sql), started, limited)
	}

	// ---- SQL-family path (mysql/postgres/sqlite/starrocks/clickhouse/trino/presto) ----
	perms, err := p.store.ResolvePermissions(claims.UserID, dsID)
	if err != nil {
		p.audit(ctx, dsID, claims, sql, "", "error", err.Error(), 0, started)
		return nil, err
	}
	rr, err := permission.Rewrite(sql, perms, claims.Attributes, claims.IsAdmin())
	if err != nil {
		p.audit(ctx, dsID, claims, sql, "", "denied", err.Error(), 0, started)
		return nil, err
	}

	// Write protections (behavior governance), inherited from admin exemption.
	if limited && !rr.IsRead {
		if !rr.WriteHasWhere && !p.guard.AllowNoWhere {
			err := fmt.Errorf("UPDATE/DELETE without WHERE is not permitted (row-policy-bounded writes are allowed; set allow_no_where_writes to override)")
			p.audit(ctx, dsID, claims, sql, rr.SQL, "denied", err.Error(), 0, started)
			return nil, err
		}
		if p.guard.MaxAffectedRows > 0 && rr.CountCheckSQL != "" {
			n, cerr := p.countAffectedSQL(ctx, ds, rr.CountCheckSQL)
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

	raw, affected, err := p.ds.ExecSQL(ctx, ds, rr.SQL, args, rr.IsRead)
	if err != nil {
		p.audit(ctx, dsID, claims, sql, rr.SQL, "error", err.Error(), 0, started)
		return nil, fmt.Errorf("execute: %w", err)
	}

	if !rr.IsRead {
		p.audit(ctx, dsID, claims, sql, rr.SQL, "ok", "", int(affected), started)
		return &QueryResult{AffectedRows: affected, RewrittenSQL: rr.SQL}, nil
	}

	maxRows := 0
	if limited {
		maxRows = p.guard.MaxRows
	}
	res, truncated := p.maskRaw(raw, rr.DeniedCols, rr.AllowedCols, rr.Masks, maxRows)
	res.RewrittenSQL = rr.SQL
	note := ""
	if truncated {
		note = fmt.Sprintf("result truncated at max_rows=%d", maxRows)
	}
	p.audit(ctx, dsID, claims, sql, rr.SQL, "ok", note, len(res.Rows), started)
	return res, nil
}

// executeNoSQL runs the governed path for Mongo / Elasticsearch backends.
func (p *Proxy) executeNoSQL(ctx context.Context, ds *store.DataSource, claims *auth.Claims, payload json.RawMessage, started time.Time, limited bool) (*QueryResult, error) {
	perms, err := p.store.ResolvePermissions(claims.UserID, ds.ID)
	if err != nil {
		p.audit(ctx, ds.ID, claims, string(payload), "", "error", err.Error(), 0, started)
		return nil, err
	}
	gov, err := permission.GovernNoSQL(ds.Type, payload, perms, claims.IsAdmin())
	if err != nil {
		p.audit(ctx, ds.ID, claims, string(payload), "", "denied", err.Error(), 0, started)
		return nil, err
	}
	raw, _, err := p.ds.NoSQLExec(ctx, ds, gov.Payload)
	if err != nil {
		p.audit(ctx, ds.ID, claims, string(payload), string(gov.Payload.Raw), "error", err.Error(), 0, started)
		return nil, fmt.Errorf("execute: %w", err)
	}
	maxRows := 0
	if limited {
		maxRows = p.guard.MaxRows
	}
	res, truncated := p.maskRaw(raw, nil, nil, gov.Masks, maxRows)
	res.RewrittenSQL = string(gov.Payload.Raw)
	note := ""
	if truncated {
		note = fmt.Sprintf("result truncated at max_rows=%d", maxRows)
	}
	p.audit(ctx, ds.ID, claims, string(payload), string(gov.Payload.Raw), "ok", note, len(res.Rows), started)
	return res, nil
}

// maskRaw applies column governance (denied/allowed) and value masking to a
// RawResult, producing the final QueryResult. Denied columns are dropped; masks
// transform surviving cell values. Truncation honours maxRows.
func (p *Proxy) maskRaw(raw *datasource.RawResult, denied, allowed []string, masks map[string]store.MaskSpec, maxRows int) (*QueryResult, bool) {
	actions, outCols := columnActions(raw.Columns, denied, allowed, masks)
	truncated := false
	out := []map[string]interface{}{}
	for _, row := range raw.Rows {
		if maxRows > 0 && len(out) >= maxRows {
			truncated = true
			break
		}
		nr := map[string]interface{}{}
		for i, c := range raw.Columns {
			a := actions[i]
			if !a.keep {
				continue
			}
			v := normalizeValue(row[c])
			if a.mask != nil && v != nil {
				v = applyMask(a.mask.Strategy, a.mask.Keep, v)
			}
			nr[c] = v
		}
		out = append(out, nr)
	}
	return &QueryResult{Columns: outCols, Rows: out, Truncated: truncated}, truncated
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
	physical, err := p.physicalTables(ctx, ds)
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

// physicalTables returns the raw (ungoverned) table/collection names of a
// datasource, dispatching to the correct backend for the datasource type.
func (p *Proxy) physicalTables(ctx context.Context, ds *store.DataSource) ([]string, error) {
	if datasource.IsNoSQL(ds.Type) {
		return p.ds.NoSQLListTables(ctx, ds)
	}
	return p.ds.ListTables(ds)
}

// describeColumns returns raw column/field metadata for a table/collection.
func (p *Proxy) describeColumns(ctx context.Context, ds *store.DataSource, table string) ([]datasource.ColumnMeta, error) {
	if datasource.IsNoSQL(ds.Type) {
		return p.ds.NoSQLDescribeTable(ctx, ds, table)
	}
	return p.ds.DescribeTable(ds, table)
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
	cols, err := p.describeColumns(ctx, ds, table)
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

// countAffectedSQL runs a portable SELECT COUNT(*) pre-check for a write
// statement so the proxy can reject over-cap writes before executing them.
func (p *Proxy) countAffectedSQL(ctx context.Context, ds *store.DataSource, sql string) (int64, error) {
	raw, _, err := p.ds.ExecSQL(ctx, ds, sql, nil, true)
	if err != nil {
		return 0, err
	}
	if len(raw.Rows) == 0 {
		return 0, fmt.Errorf("count returned no rows")
	}
	for _, v := range raw.Rows[0] {
		return toInt64(v), nil
	}
	return 0, nil
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

func toInt64(v interface{}) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case float64:
		return int64(x)
	case string:
		var n int64
		for _, ch := range x {
			if ch < '0' || ch > '9' {
				return 0
			}
			n = n*10 + int64(ch-'0')
		}
		return n
	default:
		return 0
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
