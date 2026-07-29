package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/wisonwang/aegis/internal/alerting"
	"github.com/wisonwang/aegis/internal/auth"
	"github.com/wisonwang/aegis/internal/datasource"
	"github.com/wisonwang/aegis/internal/logging"
	"github.com/wisonwang/aegis/internal/metrics"
	"github.com/wisonwang/aegis/internal/nl2sql"
	"github.com/wisonwang/aegis/internal/permission"
	"github.com/wisonwang/aegis/internal/store"
	"github.com/xwb1989/sqlparser"
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
	store      *store.Store
	ds         *datasource.Manager
	guard      *Guard             // nil = behavior limits disabled
	detector   *alerting.Detector // nil = anomaly alerting disabled
	nl2sqlGen  nl2sql.Generator   // nil = NL2SQL gateway disabled
}

func New(store *store.Store, ds *datasource.Manager) *Proxy {
	return &Proxy{store: store, ds: ds}
}

// SetGuard installs AI-behavior limits (max rows / timeout / rate limit).
func (p *Proxy) SetGuard(g *Guard) { p.guard = g }

// SetDetector installs the anomaly-detection engine that watches governed
// outcomes and raises security alerts. Pass nil to disable alerting.
func (p *Proxy) SetDetector(d *alerting.Detector) { p.detector = d }

// SetNL2SQL installs the natural-language-to-SQL generator. Pass nil to
// disable the NL2SQL gateway.
func (p *Proxy) SetNL2SQL(g nl2sql.Generator) { p.nl2sqlGen = g }

// NL2SQLConfigured reports whether a generator is installed.
func (p *Proxy) NL2SQLConfigured() bool { return p.nl2sqlGen != nil }

// dialectOf maps a datasource type to a SQL dialect label for the LLM prompt.
func dialectOf(t string) string {
	switch t {
	case "mysql", "mariadb":
		return "MySQL"
	case "postgres", "postgresql":
		return "PostgreSQL"
	case "sqlite":
		return "SQLite"
	case "trino", "presto":
		return "Trino (PrestoSQL)"
	case "starrocks":
		return "StarRocks (MySQL dialect)"
	case "clickhouse":
		return "ClickHouse"
	default:
		return "standard SQL"
	}
}

// Channel context ------------------------------------------------------------

type ctxKey int

const channelKey ctxKey = 1
const sessionKey ctxKey = 2

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

// WithSession tags the request context with an AI conversation / session id so
// that every query an agent issues during one conversation can be tied back
// together in the audit trail. Callers should pass a stable id per conversation
// (e.g. the client's own conversation id); the API layer falls back to a
// generated id when none is supplied.
func WithSession(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, sessionKey, sessionID)
}

func sessionFrom(ctx context.Context) string {
	if v, ok := ctx.Value(sessionKey).(string); ok {
		return v
	}
	return ""
}

// audit persists one governed-query trace. Failures to write audit never
// affect the caller's result; they are best-effort but synchronous (SQLite is
// local and fast) so entries are never lost on process exit.
func (p *Proxy) audit(ctx context.Context, dsID string, claims *auth.Claims, sqlText, rewritten, status, errMsg string, rowCount int, started time.Time) {
	dsName := ""
	if ds, err := p.store.GetDataSource(ctx, dsID); err == nil && ds != nil {
		dsName = ds.Name
	}
	_ = p.store.InsertAudit(ctx, &store.AuditLog{
		UserID:       claims.UserID,
		Username:     claims.Username,
		Channel:      channelFrom(ctx),
		DataSourceID: dsID,
		DataSource:   dsName,
		SessionID:    sessionFrom(ctx),
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
	// Anomaly detection: observe every governed outcome (ok/denied/error) so
	// the engine can spot probing, bulk export and off-hours access.
	if p.detector != nil {
		p.detector.Observe(claims.Username, claims.IsAdmin(), ch, status, rowCount, time.Now())
	}
	// Structured signal for operators / SIEM: every non-ok governed outcome
	// is emitted as a discrete "governance decision" event carrying the
	// principal, channel, datasource and (truncated) attempted SQL so a
	// security team can alert on probing without scraping the audit table.
	if status != "ok" {
		logging.WithCtx(ctx,
			"datasource", dsID,
			"datasource_name", dsName,
			"channel", ch,
			"user", claims.Username,
		).Log(ctx, decisionLevel(status), "governance decision",
			"decision", status,
			"reason", errMsg,
			"sql_original", truncate(sqlText, 2000),
			"sql_rewritten", truncate(rewritten, 2000),
		)
	}
}

// decisionLevel maps a governance status to a slog level: errors are
// operational failures, denials are policy events worth flagging.
func decisionLevel(status string) slog.Level {
	if status == "error" {
		return slog.LevelError
	}
	return slog.LevelWarn
}

// truncate keeps attempted-SQL fields in the log from blowing up on very
// large statements while preserving enough context for investigation.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
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

	ds, err := p.store.GetDataSource(ctx, dsID)
	if err != nil || ds == nil {
		p.audit(ctx, dsID, claims, sql, "", "error", "datasource not found", 0, started)
		return nil, fmt.Errorf("datasource not found")
	}

	if datasource.IsNoSQL(ds.Type) {
		return p.executeNoSQL(ctx, ds, claims, json.RawMessage(sql), started, limited)
	}

	// ---- SQL-family path (mysql/postgres/sqlite/starrocks/clickhouse/trino/presto) ----
	perms, err := p.store.ResolvePermissions(ctx, claims.UserID, dsID)
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

	// For read queries under behavior governance, inject a LIMIT clause into
	// the rewritten SQL so the database engine stops scanning at max_rows.
	// This is the primary defense against table-dumping; maskRaw truncation
	// remains as a safety net for edge cases (e.g. UNION queries where LIMIT
	// may not apply to the whole result set, or backend drivers that buffer
	// all rows before streaming).
	execSQL := rr.SQL
	if limited && rr.IsRead && p.guard.MaxRows > 0 {
		execSQL = injectLimit(rr.SQL, p.guard.MaxRows)
	}

	raw, affected, err := p.ds.ExecSQL(ctx, ds, execSQL, args, rr.IsRead)
	if err != nil {
		p.audit(ctx, dsID, claims, sql, execSQL, "error", err.Error(), 0, started)
		return nil, fmt.Errorf("execute: %w", err)
	}

	if !rr.IsRead {
		p.audit(ctx, dsID, claims, sql, rr.SQL, "ok", "", int(affected), started)
		return &QueryResult{AffectedRows: affected, RewrittenSQL: rr.SQL}, nil
	}

	maxRows := 0
	maxBytes := 0
	if limited {
		maxRows = p.guard.MaxRows
		maxBytes = p.guard.MaxBytes
	}
	res, truncated, oversized := p.maskRaw(raw, rr.DeniedCols, rr.AllowedCols, rr.Masks, maxRows, maxBytes)
	// RewrittenSQL shows the actual SQL that was sent to the database,
	// including the injected LIMIT clause if present. This gives callers
	// transparency into what was executed (useful for debugging).
	res.RewrittenSQL = execSQL
	note := ""
	if truncated {
		note = fmt.Sprintf("result truncated at max_rows=%d", maxRows)
	}
	if oversized {
		note = "result body exceeds max_bytes limit"
	}
	p.audit(ctx, dsID, claims, sql, rr.SQL, "ok", note, len(res.Rows), started)
	return res, nil
}

// NL2SQL translates a natural-language question into a governed SQL query and
// executes it. The generated SQL is fed straight back into Execute, so the
// *exact same* table/row/column governance, value masking, behavior limits and
// audit trail apply as for any hand-written query. NL2SQL widens who can ask;
// it never widens what they may see.
//
// It returns the governed QueryResult and the generated SQL (for transparency).
// gen is nil when generation itself failed (caller should treat that as a
// 5xx); gen is non-nil but res nil when governance denied execution (4xx).
func (p *Proxy) NL2SQL(ctx context.Context, dsID string, claims *auth.Claims, question, sqlHint string) (res *QueryResult, gen *nl2sql.Result, err error) {
	if p.nl2sqlGen == nil {
		return nil, nil, fmt.Errorf("NL2SQL is not configured on this server")
	}
	if strings.TrimSpace(question) == "" && strings.TrimSpace(sqlHint) == "" {
		return nil, nil, fmt.Errorf("a question or sql_hint is required")
	}
	ds, err := p.store.GetDataSource(ctx, dsID)
	if err != nil || ds == nil {
		return nil, nil, fmt.Errorf("datasource not found")
	}
	schema, err := p.Catalog(ctx, dsID, claims)
	if err != nil {
		return nil, nil, fmt.Errorf("build catalog: %w", err)
	}
	gen, err = p.nl2sqlGen.Generate(ctx, &nl2sql.Request{
		SchemaMarkdown: schema.CatalogMarkdown(),
		Question:       question,
		Dialect:        dialectOf(ds.Type),
		SQLHint:        sqlHint,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("NL2SQL generation failed: %w", err)
	}
	if gen == nil || strings.TrimSpace(gen.SQL) == "" {
		return nil, nil, fmt.Errorf("NL2SQL returned no SQL")
	}
	res, err = p.Execute(ctx, dsID, claims, gen.SQL)
	if err != nil {
		// gen is non-nil here: this is a governance denial / execution error,
		// not a generation failure.
		return nil, gen, err
	}
	return res, gen, nil
}

// executeNoSQL runs the governed path for Mongo / Elasticsearch backends.
func (p *Proxy) executeNoSQL(ctx context.Context, ds *store.DataSource, claims *auth.Claims, payload json.RawMessage, started time.Time, limited bool) (*QueryResult, error) {
	// Mutating operations (insert/update/delete) take the write path; reads
	// (find/search) fall through to the read path below.
	if datasource.IsNoSQLWriteOp(payload) {
		return p.executeNoSQLWrite(ctx, ds, claims, payload, started, limited)
	}
	perms, err := p.store.ResolvePermissions(ctx, claims.UserID, ds.ID)
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
	maxBytes := 0
	if limited {
		maxRows = p.guard.MaxRows
		maxBytes = p.guard.MaxBytes
	}
	res, truncated, oversized := p.maskRaw(raw, nil, nil, gov.Masks, maxRows, maxBytes)
	res.RewrittenSQL = string(gov.Payload.Raw)
	note := ""
	if truncated {
		note = fmt.Sprintf("result truncated at max_rows=%d", maxRows)
	}
	if oversized {
		note = "result body exceeds max_bytes limit"
	}
	p.audit(ctx, ds.ID, claims, string(payload), string(gov.Payload.Raw), "ok", note, len(res.Rows), started)
	return res, nil
}

// executeNoSQLWrite runs the governed path for Mongo / Elasticsearch writes.
// Column governance is enforced at query time (projection / _source), so only
// value masking applies to the result layer. When limits.max_affected_rows is
// set, an affected-rows guard pre-checks the match count before the write runs.
func (p *Proxy) executeNoSQLWrite(ctx context.Context, ds *store.DataSource, claims *auth.Claims, payload json.RawMessage, started time.Time, limited bool) (*QueryResult, error) {
	perms, err := p.store.ResolvePermissions(ctx, claims.UserID, ds.ID)
	if err != nil {
		p.audit(ctx, ds.ID, claims, string(payload), "", "error", err.Error(), 0, started)
		return nil, err
	}
	// Match the SQL write path: the no-where guard only applies when behavior
	// limits are enabled (guard != nil); if limits are disabled, writes are
	// unrestricted, and AllowNoWhere relaxes the guard when set.
	allowNoWhere := p.guard == nil || p.guard.AllowNoWhere
	gov, err := permission.GovernNoSQLWrite(ds.Type, payload, perms, claims.IsAdmin(), allowNoWhere)
	if err != nil {
		p.audit(ctx, ds.ID, claims, string(payload), "", "denied", err.Error(), 0, started)
		return nil, err
	}
	// Affected-rows guard: pre-check the match count for update/delete.
	if limited && p.guard.MaxAffectedRows > 0 && gov.CountPayload.Raw != nil {
		n, err := p.ds.NoSQLCount(ctx, ds, gov.CountPayload)
		if err != nil {
			p.audit(ctx, ds.ID, claims, string(payload), string(gov.Payload.Raw), "error", err.Error(), 0, started)
			return nil, fmt.Errorf("pre-check count: %w", err)
		}
		if int(n) > p.guard.MaxAffectedRows {
			msg := fmt.Sprintf("write would affect %d rows, exceeding max_affected_rows=%d", n, p.guard.MaxAffectedRows)
			p.audit(ctx, ds.ID, claims, string(payload), string(gov.Payload.Raw), "denied", msg, int(n), started)
			return nil, fmt.Errorf("%s", msg)
		}
	}
	affected, err := p.ds.NoSQLWrite(ctx, ds, gov.Payload)
	if err != nil {
		p.audit(ctx, ds.ID, claims, string(payload), string(gov.Payload.Raw), "error", err.Error(), 0, started)
		return nil, fmt.Errorf("execute write: %w", err)
	}
	res := &QueryResult{AffectedRows: affected, RewrittenSQL: string(gov.Payload.Raw), Columns: []string{"affected_rows"}, Rows: []map[string]interface{}{{"affected_rows": affected}}}
	p.audit(ctx, ds.ID, claims, string(payload), string(gov.Payload.Raw), "ok", "", int(affected), started)
	return res, nil
}

// maskRaw applies column governance (denied/allowed) and value masking to a
// RawResult, producing the final QueryResult. Denied columns are dropped; masks
// transform surviving cell values. Truncation honours maxRows and maxBytes:
// rows are dropped once the accumulated row count hits maxRows, and once the
// estimated serialized body size exceeds maxBytes further rows are also cut.
// oversized is set when maxBytes triggered the cut (distinct from row-count truncation).
func (p *Proxy) maskRaw(raw *datasource.RawResult, denied, allowed []string, masks map[string]store.MaskSpec, maxRows, maxBytes int) (*QueryResult, bool, bool) {
	actions, outCols := columnActions(raw.Columns, denied, allowed, masks)
	truncated := false
	oversized := false
	estBytes := 0
	out := []map[string]interface{}{}
	for _, row := range raw.Rows {
		if maxRows > 0 && len(out) >= maxRows {
			truncated = true
			break
		}
		nr := map[string]interface{}{}
		rowBytes := 0
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
			// Estimate serialized size: string values count their length,
			// numeric values count 8 bytes, nil counts 0.
			switch x := v.(type) {
			case string:
				rowBytes += len(c) + len(x) + 6 // key + value + JSON overhead
			case nil:
				rowBytes += len(c) + 4
			default:
				rowBytes += len(c) + 14 // key + number overhead
			}
		}
		estBytes += rowBytes
		if maxBytes > 0 && estBytes > maxBytes {
			oversized = true
			break
		}
		out = append(out, nr)
	}
	return &QueryResult{Columns: outCols, Rows: out, Truncated: truncated || oversized}, truncated, oversized
}

// ListTables returns the tables a principal may access on a datasource,
// together with the operations granted.
func (p *Proxy) ListTables(ctx context.Context, dsID string, claims *auth.Claims) ([]TableInfo, error) {
	ds, err := p.store.GetDataSource(ctx, dsID)
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
	perms, err := p.store.ResolvePermissions(ctx, claims.UserID, dsID)
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
	ds, err := p.store.GetDataSource(ctx, dsID)
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
	perms, err := p.store.ResolvePermissions(ctx, claims.UserID, dsID)
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

// injectLimit parses a SELECT SQL and injects or replaces the LIMIT clause
// with maxRows. If the query already has a LIMIT ≤ maxRows, it is kept
// (the caller's own limit is stricter). If the LIMIT exceeds maxRows, it
// is replaced. If no LIMIT exists, one is appended. Non-SELECT statements
// and malformed SQL are returned unchanged (maskRaw truncation catches those).
func injectLimit(sql string, maxRows int) string {
	stmt, err := sqlparser.Parse(sql)
	if err != nil {
		// If we can't parse it, return it unchanged — maskRaw truncation
		// is the safety net.
		return sql
	}
	sel, ok := stmt.(*sqlparser.Select)
	if !ok {
		// UNION queries, SET operations etc. — not a simple SELECT.
		// maskRaw truncation handles these.
		return sql
	}
	if sel.Limit != nil {
		// Check existing LIMIT value.
		existing := limitValue(sel.Limit.Rowcount)
		if existing > 0 && existing <= maxRows {
			// Caller's own limit is stricter or equal — keep it.
			return sql
		}
		// Existing LIMIT exceeds maxRows (or is non-numeric) — replace.
		sel.Limit.Rowcount = sqlparser.NewIntVal([]byte(fmt.Sprintf("%d", maxRows)))
	} else {
		// No LIMIT — inject one.
		sel.Limit = &sqlparser.Limit{
			Rowcount: sqlparser.NewIntVal([]byte(fmt.Sprintf("%d", maxRows))),
		}
	}
	return sqlparser.String(stmt)
}

// limitValue extracts a numeric value from a LIMIT expression, returning 0
// for non-numeric (variable/placeholder) expressions.
func limitValue(expr sqlparser.Expr) int {
	if v, ok := expr.(*sqlparser.SQLVal); ok && v.Type == sqlparser.IntVal {
		n := 0
		for _, b := range v.Val {
			if b < '0' || b > '9' {
				return 0
			}
			n = n*10 + int(b-'0')
		}
		return n
	}
	return 0
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
