package proxy

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/wisonwang/aegis/internal/auth"
	"github.com/wisonwang/aegis/internal/datasource"
	"github.com/wisonwang/aegis/internal/permission"
)

// QueryEstimate is a pre-execution cost/risk preview of a governed query.
// It runs the SAME governance rewrite as Execute (so row/column policies and
// value masking are already reflected in the SQL it explains) but never mutates
// data: EXPLAIN is a read-only plan, and writes are estimated via their
// SELECT COUNT(*) pre-check. Agents use it to decide whether to run a query
// at all — tighten a filter, avoid a million-row scan, or handle PII carefully.
type QueryEstimate struct {
	GovernedSQL   string   `json:"governed_sql"`    // the SQL EXPLAIN actually ran over
	ReadOnly      bool     `json:"read_only"`        // true for SELECT
	EstimatedRows int64    `json:"estimated_rows"`   // from EXPLAIN; -1 when unknown/unavailable
	Tables        []string `json:"tables"`           // tables the query touches
	Columns       []string `json:"columns"`          // sensitive columns among them
	MaxSensitivity string  `json:"max_sensitivity"` // public|internal|confidential|restricted|pii
	HasPII        bool     `json:"has_pii"`
	RiskLevel     string   `json:"risk_level"`       // low|medium|high|unknown
	Warnings      []string `json:"warnings"`
	Note          string   `json:"note,omitempty"`   // dialect limitation / parse failure detail
}

// unknownRows marks an estimate where the backend could not produce a row count.
const unknownRows int64 = -1

// Estimate returns a cost/risk preview without executing the query. The
// datasource type selects the EXPLAIN dialect; NoSQL backends return a note
// explaining estimates are unavailable.
func (p *Proxy) Estimate(ctx context.Context, dsID string, claims *auth.Claims, sql string) (*QueryEstimate, error) {
	ds, err := p.store.GetDataSource(ctx, dsID)
	if err != nil || ds == nil {
		return nil, fmt.Errorf("datasource not found")
	}
	if datasource.IsNoSQL(ds.Type) {
		return &QueryEstimate{
			ReadOnly:      false,
			EstimatedRows: unknownRows,
			RiskLevel:     "unknown",
			Note:          "cost estimate is not available for NoSQL datasources",
		}, nil
	}

	perms, err := p.store.ResolvePermissions(ctx, claims.UserID, dsID)
	if err != nil {
		return nil, err
	}
	rr, err := permission.Rewrite(sql, perms, claims.Attributes, claims.IsAdmin())
	if err != nil {
		// A governance denial is itself the most useful estimate: the caller
		// learns the query would be rejected before sending real data.
		return nil, err
	}

	est := &QueryEstimate{ReadOnly: rr.IsRead, GovernedSQL: rr.SQL}

	// Pick the SQL EXPLAIN runs over: for reads, the governed SELECT; for
	// writes with a single-table count pre-check, explain that COUNT(*)
	// instead (estimates affected rows without touching data).
	explainSQL := "EXPLAIN " + rr.SQL
	if !rr.IsRead && rr.CountCheckSQL != "" {
		explainSQL = "EXPLAIN " + rr.CountCheckSQL
	}
	if datasource.NormalizeType(ds.Type) == "sqlite" {
		// SQLite needs the QUERY PLAN variant to expose row estimates
		// (and even then, modernc's build omits the rows figure), so
		// sqlite is handled entirely by the COUNT fallback below.
		explainSQL = "EXPLAIN QUERY PLAN " + strings.TrimPrefix(explainSQL, "EXPLAIN ")
	}

	raw, _, eerr := p.ds.ExecSQL(ctx, ds, explainSQL, nil, true)
	if eerr == nil {
		est.EstimatedRows = parseExplainRows(raw)
	}
	if est.EstimatedRows == unknownRows {
		// Dialects that do not expose row estimates through EXPLAIN (notably
		// SQLite) fall back to a read-only COUNT so the agent still gets a
		// concrete number. The fallback never mutates data.
		if fb := countFallbackSQL(ds.Type, rr); fb != "" {
			if craw, _, ferr := p.ds.ExecSQL(ctx, ds, fb, nil, true); ferr == nil {
				if n, ok := firstCellInt(craw); ok {
					est.EstimatedRows = n
					est.Note = "exact row count (EXPLAIN row estimate unavailable on this backend)"
				} else {
					est.Note = "count fallback returned no usable rows"
				}
			} else {
				est.Note = "count fallback failed: " + ferr.Error()
			}
		} else if eerr != nil {
			est.Note = "could not read EXPLAIN plan: " + eerr.Error()
		}
	}

	// Lineage (tables + sensitivity), reused from the metric path.
	if lin, lerr := p.computeLineageForSQL(ctx, dsID, rr.SQL); lerr == nil && lin != nil {
		est.Tables = lin.Tables
		est.Columns = lin.Columns
		est.MaxSensitivity = lin.MaxSensitivity
		est.HasPII = lin.HasPII
	}

	est.assessRisk()
	return est, nil
}

// assessRisk synthesises a low/medium/high flag from data sensitivity and the
// EXPLAIN row estimate, attaching human-readable warnings for agents.
func (e *QueryEstimate) assessRisk() {
	risk := 0 // 0 low, 1 medium, 2 high
	switch e.MaxSensitivity {
	case "pii", "restricted":
		risk = 2
	case "confidential":
		risk = maxInt(risk, 1)
	}
	if e.EstimatedRows >= 1_000_000 {
		risk = 2
		e.Warnings = append(e.Warnings, fmt.Sprintf("扫描约 %s 行（百万级），建议加 WHERE 过滤或 LIMIT", comma(e.EstimatedRows)))
	} else if e.EstimatedRows >= 50_000 {
		risk = maxInt(risk, 1)
		e.Warnings = append(e.Warnings, fmt.Sprintf("扫描约 %s 行", comma(e.EstimatedRows)))
	}
	if e.HasPII {
		e.Warnings = append(e.Warnings, "结果含 PII 列，输出前请确认已脱敏或谨慎处理")
	}
	switch risk {
	case 2:
		e.RiskLevel = "high"
	case 1:
		e.RiskLevel = "medium"
	default:
		e.RiskLevel = "low"
		e.Warnings = append(e.Warnings, "成本与敏感度均较低")
	}
}

// parseExplainRows extracts the worst-case row estimate from an EXPLAIN result,
// dialect-agnostically: it accepts a numeric column literally named "rows"
// (MySQL/StarRocks/ClickHouse) and any text cell containing "rows=N"
// (PostgreSQL / SQLite QUERY PLAN). When several rows are returned (joins),
// the maximum is taken as the worst-case scan volume.
var reRows = regexp.MustCompile(`(?i)rows\s*=\s*(\d+)`)

func parseExplainRows(raw *datasource.RawResult) int64 {
	if raw == nil || len(raw.Rows) == 0 {
		return unknownRows
	}
	max := int64(-1)
	// 1) numeric column literally named "rows".
	for _, col := range raw.Columns {
		if strings.EqualFold(col, "rows") {
			for _, row := range raw.Rows {
				if v, ok := row[col]; ok {
					if n := toInt64(v); n > max {
						max = n
					}
				}
			}
		}
	}
	// 2) text cells containing "rows=N" (postgres / sqlite plan text).
	for _, row := range raw.Rows {
		for _, col := range raw.Columns {
			if v, ok := row[col]; ok {
				s := toStr(v)
				for _, m := range reRows.FindAllStringSubmatch(s, -1) {
					if n, err := strconv.ParseInt(m[1], 10, 64); err == nil && n > max {
						max = n
					}
				}
			}
		}
	}
	return max
}

func toStr(v interface{}) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", x)
	}
}

// countFallbackSQL returns a read-only COUNT query used when a backend's
// EXPLAIN does not expose a row estimate (currently SQLite). For reads it
// wraps the governed SELECT; for single-table writes the permission engine
// already produced a SELECT COUNT(*) pre-check we can reuse directly.
func countFallbackSQL(dsType string, rr *permission.RewriteResult) string {
	if datasource.NormalizeType(dsType) != "sqlite" {
		return "" // only SQLite lacks EXPLAIN row estimates
	}
	if !rr.IsRead {
		return rr.CountCheckSQL // already a SELECT COUNT(*)
	}
	return "SELECT COUNT(*) FROM (" + rr.SQL + ") AS _aegis_est"
}

// firstCellInt reads the first integer-valued cell of a single-row COUNT
// result across whatever column name the backend uses.
func firstCellInt(raw *datasource.RawResult) (int64, bool) {
	if raw == nil || len(raw.Rows) == 0 {
		return 0, false
	}
	for _, col := range raw.Columns {
		if v, ok := raw.Rows[0][col]; ok {
			switch x := v.(type) {
			case int64:
				return x, true
			case int:
				return int64(x), true
			case float64:
				return int64(x), true
			case string:
				if n, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64); err == nil {
					return n, true
				}
			case []byte:
				if n, err := strconv.ParseInt(strings.TrimSpace(string(x)), 10, 64); err == nil {
					return n, true
				}
			}
		}
	}
	return 0, false
}

// comma groups an integer with thousands separators for readable warnings.
func comma(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := strconv.FormatInt(n, 10)
	if len(s) <= 3 {
		if neg {
			return "-" + s
		}
		return s
	}
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
