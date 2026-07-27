package proxy

import (
	"context"
	"testing"

	"github.com/wisonwang/aegis/internal/auth"
	"github.com/wisonwang/aegis/internal/datasource"
)

// adminClaims is a superuser principal: Estimate on a write still goes
// through EXPLAIN of the COUNT(*) pre-check because Rewrite bypasses
// row/column policy but still builds CountCheckSQL for single-table writes.
func adminClaims() *auth.Claims {
	return &auth.Claims{UserID: "admin", Username: "admin", Roles: []string{"admin"}}
}

func TestEstimateLineageAndRisk(t *testing.T) {
	p := newMetricTestStack(t)
	est, err := p.Estimate(context.Background(), "ds1", metricClaims(), "SELECT * FROM customers")
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}
	// customers.phone is classified pii -> lineage must flag it.
	if !est.HasPII {
		t.Fatalf("expected HasPII=true; got %+v", est)
	}
	if est.MaxSensitivity != "pii" {
		t.Fatalf("expected max_sensitivity=pii, got %q", est.MaxSensitivity)
	}
	// Full-table scan on a 3-row sqlite table.
	if est.EstimatedRows <= 0 {
		t.Fatalf("expected positive estimated rows, got %d", est.EstimatedRows)
	}
	if est.RiskLevel != "high" {
		t.Fatalf("expected risk=high for pii scan, got %q", est.RiskLevel)
	}
	if len(est.Warnings) == 0 {
		t.Fatal("expected at least the PII warning")
	}
}

func TestEstimateDeniedForUnauthorizedTable(t *testing.T) {
	p := newMetricTestStack(t)
	// Analyst has no permission on a table that is not granted.
	if _, err := p.Estimate(context.Background(), "ds1", metricClaims(), "SELECT * FROM nonexistent_table"); err == nil {
		t.Fatal("expected governance denial for unauthorized table")
	}
}

func TestEstimateWriteAffectedRows(t *testing.T) {
	p := newMetricTestStack(t)
	// Admin (superuser) can estimate a write; the estimate explains the
	// SELECT COUNT(*) pre-check so it never touches data.
	est, err := p.Estimate(context.Background(), "ds1", adminClaims(), "DELETE FROM customers")
	if err != nil {
		t.Fatalf("estimate write: %v", err)
	}
	if est.ReadOnly {
		t.Fatal("expected read_only=false for a DELETE")
	}
	if est.EstimatedRows <= 0 {
		t.Fatalf("expected positive affected-row estimate, got %d", est.EstimatedRows)
	}
	// The governed SQL is the original DELETE (superuser bypass); the
	// affected-row number comes from the read-only COUNT fallback.
	if est.GovernedSQL != "DELETE FROM customers" {
		t.Fatalf("expected governed SQL = original DELETE, got %q", est.GovernedSQL)
	}
}

func TestParseExplainRowsDialects(t *testing.T) {
	// MySQL / StarRocks / ClickHouse: numeric "rows" column.
	mysql := &datasource.RawResult{
		Columns: []string{"id", "rows", "Extra"},
		Rows:   []map[string]interface{}{{"id": 1, "rows": int64(120), "Extra": "Using where"}},
	}
	if got := parseExplainRows(mysql); got != 120 {
		t.Fatalf("mysql parse: want 120, got %d", got)
	}

	// PostgreSQL / SQLite QUERY PLAN: text cell with rows=N.
	pg := &datasource.RawResult{
		Columns: []string{"QUERY PLAN"},
		Rows:   []map[string]interface{}{{"QUERY PLAN": "Seq Scan on customers  (cost=0.00..1.50 rows=50 width=32)"}},
	}
	if got := parseExplainRows(pg); got != 50 {
		t.Fatalf("pg parse: want 50, got %d", got)
	}

	// Join: take the maximum (worst-case) row estimate.
	join := &datasource.RawResult{
		Columns: []string{"rows"},
		Rows:   []map[string]interface{}{{"rows": int64(10)}, {"rows": int64(999)}},
	}
	if got := parseExplainRows(join); got != 999 {
		t.Fatalf("join parse: want 999 (max), got %d", got)
	}

	// Empty plan -> unknown.
	if got := parseExplainRows(&datasource.RawResult{}); got != unknownRows {
		t.Fatalf("empty parse: want unknownRows (%d), got %d", unknownRows, got)
	}
}
