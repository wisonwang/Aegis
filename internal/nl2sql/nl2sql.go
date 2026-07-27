// Package nl2sql turns a natural-language question + a governed schema into a
// read-only SQL statement. The generator is intentionally pluggable:
//
//   - LLMGenerator  talks to any OpenAI-compatible chat-completions endpoint
//     (OpenAI, Volcengine Ark / doubao, Azure OpenAI, local vLLM, ...).
//   - StubGenerator  is deterministic and needs no network — used for tests,
//     offline demos, and as a safe fallback.
//
// Critically, Aegis never executes the generated SQL directly. The caller
// (proxy.NL2SQL) routes it through the same governed execution path as any
// hand-written query, so table/row/column governance, value masking and the
// audit trail still apply. NL2SQL only widens *who* can ask; it never widens
// *what* they may see.
package nl2sql

import (
	"context"
	"fmt"
	"strings"
)

// Request is the input to a Generator.
type Request struct {
	// SchemaMarkdown is the governed, semantically enriched schema (columns
	// the caller may not access are already absent). The model must only
	// reference tables/columns present here.
	SchemaMarkdown string
	// Question is the natural-language question.
	Question string
	// Dialect is the target SQL dialect for the datasource (mysql, postgres,
	// sqlite, trino, ...). It nudges the model toward dialect-correct syntax.
	Dialect string
	// SQLHint is an optional hand-written SQL the caller prefers over free
	// generation (human-in-the-loop / known-good query). When set it bypasses
	// the model but is still read-only validated and governed downstream.
	SQLHint string
}

// Result is the generated, read-only SQL plus a short explanation.
type Result struct {
	SQL         string `json:"sql"`
	Explanation string `json:"explanation,omitempty"`
}

// Generator turns a Request into a Result.
type Generator interface {
	Generate(ctx context.Context, req *Request) (*Result, error)
}

// StubGenerator is a deterministic, network-free generator for tests and
// offline operation. If SQLHint is supplied it is used (still validated as
// read-only). Otherwise it returns a per-keyword template or DefaultSQL.
type StubGenerator struct {
	// DefaultSQL is returned when no keyword matches and SQLHint is empty.
	DefaultSQL string
	// ByKeyword maps a substring (lower-cased) of the question to a SQL string.
	ByKeyword map[string]string
}

// Generate implements Generator.
func (g *StubGenerator) Generate(_ context.Context, req *Request) (*Result, error) {
	if strings.TrimSpace(req.SQLHint) != "" {
		if err := ValidateReadOnly(req.SQLHint); err != nil {
			return nil, err
		}
		return &Result{SQL: strings.TrimSpace(req.SQLHint)}, nil
	}
	q := strings.ToLower(req.Question)
	for kw, sql := range g.ByKeyword {
		if strings.Contains(q, strings.ToLower(kw)) {
			return &Result{SQL: sql, Explanation: "stub match for keyword " + kw}, nil
		}
	}
	if g.DefaultSQL == "" {
		return nil, fmt.Errorf("stub generator has no SQL for question %q", req.Question)
	}
	return &Result{SQL: g.DefaultSQL, Explanation: "stub default"}, nil
}

// ValidateReadOnly rejects any statement that is not a safe read. Generated
// SQL must be a SELECT or a read-only WITH (CTE) — no writes, DDL, or admin
// commands. This is a defense-in-depth check: even if the model misbehaves,
// Aegis refuses to run a mutating statement.
func ValidateReadOnly(sql string) error {
	s := strings.TrimSpace(sql)
	if s == "" {
		return fmt.Errorf("empty SQL")
	}
	up := strings.ToUpper(s)
	// Accept only read prefixes.
	ok := false
	for _, pre := range []string{"SELECT ", "SELECT\t", "SELECT\n", "WITH ", "WITH\t", "WITH\n", "(", "-- "} {
		if strings.HasPrefix(up, pre) {
			ok = true
			break
		}
	}
	// A leading "(" could be a subquery but we only allow it when it is
	// clearly a SELECT subquery start; keep it permissive but still gated by
	// the forbidden-keyword scan below.
	if !ok && !strings.HasPrefix(up, "(") {
		return fmt.Errorf("only read-only (SELECT / WITH) statements are allowed, got: %s", firstWord(s))
	}
	for _, kw := range []string{
		"INSERT", "UPDATE", "DELETE", "DROP", "ALTER", "CREATE",
		"TRUNCATE", "REPLACE", "GRANT", "REVOKE", "ATTACH", "PRAGMA",
		"EXEC", "EXECUTE", "CALL", "MERGE", "UPSERT",
	} {
		if hasWord(up, kw) {
			return fmt.Errorf("mutating/DDL keyword %q is not permitted by the NL2SQL gateway", kw)
		}
	}
	if strings.Contains(up, "INTO OUTFILE") || strings.Contains(up, "INTO DUMPFILE") {
		return fmt.Errorf("file export is not permitted by the NL2SQL gateway")
	}
	return nil
}

// hasWord reports whether kw appears as a whole word in s (s already upper).
func hasWord(s, kw string) bool {
	i := strings.Index(s, kw)
	for i >= 0 {
		before := i == 0 || !isIdent(s[i-1])
		after := i+len(kw) >= len(s) || !isIdent(s[i+len(kw)])
		if before && after {
			return true
		}
		i = strings.Index(s[i+len(kw):], kw)
		if i >= 0 {
			i += len(kw)
		}
	}
	return false
}

func isIdent(b byte) bool {
	return b == '_' || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

func firstWord(s string) string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return s
	}
	if len(f[0]) > 24 {
		return f[0][:24] + "..."
	}
	return f[0]
}
