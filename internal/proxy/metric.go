package proxy

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/wisonwang/aegis/internal/auth"
	"github.com/wisonwang/aegis/internal/store"
)

// MetricLineage is a governance-aware summary of what a curated metric touches:
// which tables, which sensitive columns, and the highest sensitivity level
// among them. Agents use it to decide whether a metric result needs extra
// care (e.g. avoid echoing PII) before surfacing it to a user.
type MetricLineage struct {
	Tables        []string `json:"tables"`         // tables referenced by the metric
	Columns       []string `json:"columns"`        // sensitive columns among those tables
	MaxSensitivity string  `json:"max_sensitivity"` // highest level: public|internal|confidential|restricted|pii
	HasPII        bool     `json:"has_pii"`        // any referenced column is pii
}

// MetricResult bundles the governed execution result with the rendered SQL and
// the lineage so callers (REST / MCP) can show both the answer and its
// governance context.
type MetricResult struct {
	QueryResult *QueryResult            `json:"query_result"`
	SQL         string                  `json:"sql"`
	Lineage     *MetricLineage          `json:"lineage"`
	Definition  *store.MetricDefinition `json:"definition"`
}

// ResolveMetric expands a curated metric definition with caller-supplied
// parameters and executes it through the governed Execute path. Exactly like
// NL2SQL, this widens *how* an agent can ask but never what it may see: table,
// row, column governance, value masking, behavior limits and audit all apply.
func (p *Proxy) ResolveMetric(ctx context.Context, dsID string, claims *auth.Claims, metricName string, params map[string]interface{}) (*MetricResult, error) {
	def, err := p.store.GetMetric(dsID, metricName)
	if err != nil {
		return nil, fmt.Errorf("lookup metric: %w", err)
	}
	if def == nil {
		return nil, fmt.Errorf("metric %q not found on datasource %s", metricName, dsID)
	}

	// Validate + normalize caller params against the definition.
	norm, err := validateMetricParams(def, params)
	if err != nil {
		return nil, err
	}

	// Render the SQL template with SQL-safe literals. This is the only place
	// caller input enters the query text, and it is injection-proof by
	// construction (typed escaping + enum allow-lists).
	rendered, err := renderMetricSQL(def, norm)
	if err != nil {
		return nil, err
	}

	lineage, err := p.computeLineage(ctx, dsID, def)
	if err != nil {
		return nil, err
	}

	res, err := p.Execute(ctx, dsID, claims, rendered)
	if err != nil {
		return nil, err
	}
	return &MetricResult{QueryResult: res, SQL: rendered, Lineage: lineage, Definition: def}, nil
}

// validateMetricParams checks every supplied value against its declared type
// and enum, fills defaults, and rejects unknown or missing-required params.
func validateMetricParams(def *store.MetricDefinition, provided map[string]interface{}) (map[string]interface{}, error) {
	norm := map[string]interface{}{}
	declared := map[string]*store.MetricParam{}
	for i := range def.Params {
		declared[def.Params[i].Name] = &def.Params[i]
	}
	// Apply declared defaults first; caller values override.
	for name, pd := range declared {
		if pd.Default != nil {
			norm[name] = pd.Default
		}
	}
	for name, val := range provided {
		pd, ok := declared[name]
		if !ok {
			return nil, fmt.Errorf("unknown parameter %q", name)
		}
		v, err := coerceMetricValue(pd, val)
		if err != nil {
			return nil, err
		}
		norm[name] = v
	}
	for name := range declared {
		if _, ok := norm[name]; !ok {
			return nil, fmt.Errorf("parameter %q is required", name)
		}
	}
	return norm, nil
}

// coerceMetricValue validates and normalizes one parameter value.
func coerceMetricValue(pd *store.MetricParam, val interface{}) (interface{}, error) {
	switch pd.Type {
	case "number":
		f, err := toFloat(val)
		if err != nil {
			return nil, fmt.Errorf("parameter %q must be a number: %v", pd.Name, err)
		}
		return f, nil
	case "bool":
		b, ok := val.(bool)
		if !ok {
			return nil, fmt.Errorf("parameter %q must be a boolean", pd.Name)
		}
		return b, nil
	case "enum":
		s, ok := val.(string)
		if !ok {
			return nil, fmt.Errorf("parameter %q must be a string", pd.Name)
		}
		found := false
		for _, e := range pd.Enum {
			if e == s {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("parameter %q must be one of %v", pd.Name, pd.Enum)
		}
		return s, nil
	case "date", "string":
		s, ok := val.(string)
		if !ok {
			return nil, fmt.Errorf("parameter %q must be a string", pd.Name)
		}
		return s, nil
	default:
		return nil, fmt.Errorf("parameter %q has unsupported type %q", pd.Name, pd.Type)
	}
}

// renderMetricSQL substitutes :name placeholders with SQL-safe literals, in
// order of appearance. Unknown placeholders error out.
func renderMetricSQL(def *store.MetricDefinition, norm map[string]interface{}) (string, error) {
	re := regexp.MustCompile(`:([A-Za-z_][A-Za-z0-9_]*)`)
	var out strings.Builder
	last := 0
	for _, m := range re.FindAllStringSubmatchIndex(def.SQLTemplate, -1) {
		name := def.SQLTemplate[m[2]:m[3]]
		out.WriteString(def.SQLTemplate[last:m[0]])
		last = m[1]
		var pd *store.MetricParam
		for i := range def.Params {
			if def.Params[i].Name == name {
				pd = &def.Params[i]
				break
			}
		}
		if pd == nil {
			return "", fmt.Errorf("unknown parameter :%s in metric template", name)
		}
		v, ok := norm[name]
		if !ok {
			return "", fmt.Errorf("parameter :%s not provided", name)
		}
		lit, err := metricLiteral(pd, v)
		if err != nil {
			return "", err
		}
		out.WriteString(lit)
	}
	out.WriteString(def.SQLTemplate[last:])
	return out.String(), nil
}

// metricLiteral renders a validated parameter value as a SQL literal.
func metricLiteral(pd *store.MetricParam, v interface{}) (string, error) {
	switch pd.Type {
	case "number":
		f, _ := v.(float64)
		return strconv.FormatFloat(f, 'f', -1, 64), nil
	case "bool":
		b, _ := v.(bool)
		if b {
			return "TRUE", nil
		}
		return "FALSE", nil
	case "enum", "date", "string":
		s, _ := v.(string)
		// Escape single quotes to defeat SQL injection via string params.
		return "'" + strings.ReplaceAll(s, "'", "''") + "'", nil
	default:
		return "", fmt.Errorf("cannot render param %q of type %q", pd.Name, pd.Type)
	}
}

// computeLineage derives a governance-aware summary of the tables and sensitive
// columns a metric touches, from its SQL template and the datasource's
// classification index. It never blocks execution — it only informs.
// computeLineage derives lineage for a curated metric definition; it simply
// delegates to computeLineageForSQL with the definition's SQL template, so the
// same logic can serve arbitrary SQL (e.g. the query cost estimator).
func (p *Proxy) computeLineage(ctx context.Context, dsID string, def *store.MetricDefinition) (*MetricLineage, error) {
	return p.computeLineageForSQL(ctx, dsID, def.SQLTemplate)
}

// computeLineageForSQL derives a governance-aware summary of the tables and
// sensitive columns a SQL statement touches, from its FROM/JOIN tables and the
// datasource's classification index. It never blocks execution — it only informs.
func (p *Proxy) computeLineageForSQL(ctx context.Context, dsID string, sql string) (*MetricLineage, error) {
	tables := extractTables(sql)
	classes, err := p.store.ClassificationIndexFor(dsID)
	if err != nil {
		return nil, err
	}
	lin := &MetricLineage{Tables: tables}
	maxRank := 0
	for _, t := range tables {
		lt := strings.ToLower(t)
		// Table-level classification.
		if c := classes.Table(lt); c != nil && c.Level != "" {
			r := sensitivityRank(c.Level)
			if r > maxRank {
				maxRank = r
			}
		}
		// Column-level classifications.
		for col, c := range classes[lt] {
			if col == "" || c == nil || c.Level == "" {
				continue
			}
			if sensitivityRank(c.Level) >= sensitivityRank("confidential") {
				lin.Columns = append(lin.Columns, lt+"."+col)
			}
			if c.Level == "pii" {
				lin.HasPII = true
			}
			if r := sensitivityRank(c.Level); r > maxRank {
				maxRank = r
			}
		}
	}
	lin.MaxSensitivity = sensitivityLabel(maxRank)
	return lin, nil
}

// extractTables pulls table names from FROM / JOIN clauses. Qualified names
// (schema.table) keep the full identifier; this is only used for lineage
// display, so precision beyond "which tables" is unnecessary.
func extractTables(sql string) []string {
	re := regexp.MustCompile(`(?i)\b(?:FROM|JOIN)\s+([A-Za-z_][A-Za-z0-9_.$]*)`)
	seen := map[string]bool{}
	var out []string
	for _, m := range re.FindAllStringSubmatch(sql, -1) {
		name := strings.TrimRight(m[1], ",);")
		if name == "" {
			continue
		}
		if !seen[strings.ToLower(name)] {
			seen[strings.ToLower(name)] = true
			out = append(out, name)
		}
	}
	return out
}

// sensitivityRank maps a classification level to an ordinal for comparison.
func sensitivityRank(level string) int {
	switch strings.ToLower(level) {
	case "pii":
		return 4
	case "restricted":
		return 3
	case "confidential":
		return 2
	case "internal":
		return 1
	default:
		return 0 // public / unknown
	}
}

func sensitivityLabel(rank int) string {
	switch rank {
	case 4:
		return "pii"
	case 3:
		return "restricted"
	case 2:
		return "confidential"
	case 1:
		return "internal"
	default:
		return "public"
	}
}

// toFloat coerces a JSON-decoded number (float64) or numeric string into a
// float64 for the "number" metric parameter type.
func toFloat(v interface{}) (float64, error) {
	switch n := v.(type) {
	case float64:
		return n, nil
	case float32:
		return float64(n), nil
	case int:
		return float64(n), nil
	case int64:
		return float64(n), nil
	case string:
		return strconv.ParseFloat(n, 64)
	default:
		return 0, fmt.Errorf("not a number: %T", v)
	}
}
