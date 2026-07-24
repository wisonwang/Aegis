package permission

import (
	"fmt"
	"strings"

	"github.com/fosun/aegis/internal/store"
	"github.com/xwb1989/sqlparser"
)

// RewriteResult is the output of governance enforcement.
type RewriteResult struct {
	SQL         string   // rewritten, safe SQL to execute against the backend
	IsRead      bool     // true for SELECT (use Query), false for DML (use Exec)
	DeniedCols  []string // result columns that must be stripped (deny wins)
	AllowedCols []string // if non-empty, only these result columns are permitted
	// WriteHasWhere reports whether the (row-policy-injected) write statement
	// still carries a WHERE clause. When false, the statement would touch every
	// row of the table — the proxy rejects such UPDATE/DELETE for non-admins.
	WriteHasWhere bool
	// CountCheckSQL is a portable `SELECT COUNT(*) FROM <table> <where>` for
	// single-table writes, used by the proxy to pre-estimate affected rows
	// before executing (so a row cap can be enforced without irreversible damage).
	// Empty for multi-table writes (not pre-estimated).
	CountCheckSQL string
}

// Rewrite applies centralized governance to a SQL statement.
//
//   - perms: resolved per-table governance for the principal (keyed by lower table name)
//   - attrs: the principal's attribute map, substituted into :placeholder tokens in row policies
//   - superuser: when true (platform admin), no enforcement is applied
//
// The database's own account permissions are never exposed: the caller is a
// platform principal, and the platform talks to the backend with a single
// service identity. All table/row/column decisions happen here.
func Rewrite(sql string, perms map[string]*store.TableEffective, attrs map[string]string, superuser bool) (*RewriteResult, error) {
	if superuser {
		return &RewriteResult{SQL: sql, IsRead: !isWriteKeyword(sql), WriteHasWhere: true}, nil
	}
	stmt, err := sqlparser.Parse(sql)
	if err != nil {
		return nil, fmt.Errorf("parse sql: %w", err)
	}

	res := &RewriteResult{}
	var tables []string

	switch node := stmt.(type) {
	case *sqlparser.Select:
		tables, err = rewriteSelect(node, perms, attrs)
		res.IsRead = true
	case *sqlparser.Update:
		tables, err = dmlTables(node.TableExprs, perms, "UPDATE")
		if err == nil {
			err = applyRowPolicyDML(tables, &node.Where, perms, attrs)
		}
		if err == nil {
			finalizeWrite(res, node.Where, node.TableExprs)
		}
	case *sqlparser.Delete:
		tables, err = dmlTables(node.TableExprs, perms, "DELETE")
		if err == nil {
			err = applyRowPolicyDML(tables, &node.Where, perms, attrs)
		}
		if err == nil {
			finalizeWrite(res, node.Where, node.TableExprs)
		}
	case *sqlparser.Insert:
		err = enforceInsert(node.Table, perms)
		// INSERT/REPLACE carry explicit VALUES/SELECT, so they are not a
		// whole-table risk; never trip the no-WHERE guard.
		res.WriteHasWhere = true
	default:
		return nil, fmt.Errorf("statement type not permitted by the proxy")
	}
	if err != nil {
		return nil, err
	}

	computeColumnMask(tables, perms, res)
	res.SQL = sqlparser.String(stmt)
	return res, nil
}

// rewriteSelect enforces per-table SELECT permission and injects row-level
// policies by wrapping each top-level table in a filtered derived subquery.
func rewriteSelect(sel *sqlparser.Select, perms map[string]*store.TableEffective, attrs map[string]string) ([]string, error) {
	var tables []string
	for _, te := range sel.From {
		for _, ate := range collectTopLevelTables(te) {
			tn, ok := ate.Expr.(sqlparser.TableName)
			if !ok {
				// a subquery used as a table source: not governed at this level
				continue
			}
			name := tn.Name.String()
			key := strings.ToLower(name)
			eff, ok := perms[key]
			if !ok {
				return nil, fmt.Errorf("access denied to table %q (no permission granted)", name)
			}
			if !eff.Ops["SELECT"] {
				return nil, fmt.Errorf("SELECT denied on table %q", name)
			}
			tables = append(tables, key)
			if len(eff.RowPolicies) > 0 {
				sub, err := wrapWithPolicy(name, eff.RowPolicies, attrs)
				if err != nil {
					return nil, err
				}
				ate.Expr = sub
				// MySQL/PostgreSQL require every derived table to carry an
				// alias. Reuse the original table name when none was given so
				// qualified column references (e.g. orders.id) keep working.
				if ate.As.IsEmpty() {
					ate.As = sqlparser.NewTableIdent(name)
				}
			}
		}
	}
	return tables, nil
}

// dmlTables checks operation permission on every top-level table of an
// UPDATE/DELETE and returns the referenced table keys.
func dmlTables(tes sqlparser.TableExprs, perms map[string]*store.TableEffective, op string) ([]string, error) {
	var tables []string
	for _, te := range tes {
		for _, ate := range collectTopLevelTables(te) {
			tn, ok := ate.Expr.(sqlparser.TableName)
			if !ok {
				continue
			}
			name := tn.Name.String()
			key := strings.ToLower(name)
			eff, ok := perms[key]
			if !ok {
				return nil, fmt.Errorf("access denied to table %q", name)
			}
			if !eff.Ops[op] {
				return nil, fmt.Errorf("%s denied on table %q", op, name)
			}
			tables = append(tables, key)
		}
	}
	return tables, nil
}

func enforceInsert(tn sqlparser.TableName, perms map[string]*store.TableEffective) error {
	name := tn.Name.String()
	key := strings.ToLower(name)
	eff, ok := perms[key]
	if !ok {
		return fmt.Errorf("access denied to table %q", name)
	}
	if !eff.Ops["INSERT"] {
		return fmt.Errorf("INSERT denied on table %q", name)
	}
	return nil
}

// applyRowPolicyDML appends combined row policies to an UPDATE/DELETE WHERE.
func applyRowPolicyDML(tables []string, where **sqlparser.Where, perms map[string]*store.TableEffective, attrs map[string]string) error {
	var combined sqlparser.Expr
	for _, key := range tables {
		eff := perms[key]
		if eff == nil || len(eff.RowPolicies) == 0 {
			continue
		}
		expr, err := combinePredicates(eff.RowPolicies, attrs)
		if err != nil {
			return err
		}
		if combined == nil {
			combined = expr
		} else {
			combined = &sqlparser.AndExpr{Left: combined, Right: expr}
		}
	}
	if combined == nil {
		return nil
	}
	if *where == nil {
		*where = &sqlparser.Where{Type: sqlparser.WhereStr, Expr: combined}
	} else {
		(*where).Expr = &sqlparser.AndExpr{Left: (*where).Expr, Right: combined}
	}
	return nil
}

// finalizeWrite records the facts about a write statement that the proxy needs
// for AI-behavior governance: whether the (row-policy-injected) statement still
// has a WHERE clause, and a portable row-count estimate for single-table writes.
func finalizeWrite(res *RewriteResult, where *sqlparser.Where, tes sqlparser.TableExprs) {
	res.WriteHasWhere = where != nil
	// Pre-estimate affected rows only for single-table writes. Joins imply
	// multi-table writes where a simple COUNT is ambiguous, so we skip the
	// estimate there (the cap is simply not enforced for that rare case).
	if len(tes) == 1 {
		if tn, ok := singleTable(tes[0]); ok {
			c := "SELECT COUNT(*) FROM " + sqlparser.String(tn)
			if where != nil {
				c += " " + sqlparser.String(where)
			}
			res.CountCheckSQL = c
		}
	}
}

// singleTable extracts the table name from a single top-level table expression,
// returning false for joins or subqueries.
func singleTable(te sqlparser.TableExpr) (sqlparser.TableName, bool) {
	if ate, ok := te.(*sqlparser.AliasedTableExpr); ok {
		if tn, ok := ate.Expr.(sqlparser.TableName); ok {
			return tn, true
		}
	}
	return sqlparser.TableName{}, false
}

// wrapWithPolicy builds (SELECT * FROM <table> WHERE <combined policy>) AS <orig alias>.
func wrapWithPolicy(origTable string, policies []string, attrs map[string]string) (sqlparser.SimpleTableExpr, error) {
	combined, err := combinePredicates(policies, attrs)
	if err != nil {
		return nil, err
	}
	inner := &sqlparser.Select{
		SelectExprs: sqlparser.SelectExprs{&sqlparser.StarExpr{}},
		From: sqlparser.TableExprs{
			&sqlparser.AliasedTableExpr{
				Expr: sqlparser.TableName{Name: sqlparser.NewTableIdent(origTable)},
			},
		},
		Where: &sqlparser.Where{Type: sqlparser.WhereStr, Expr: combined},
	}
	return &sqlparser.Subquery{Select: inner}, nil
}

func combinePredicates(policies []string, attrs map[string]string) (sqlparser.Expr, error) {
	var combined sqlparser.Expr
	for _, p := range policies {
		substituted := substituteAttrs(p, attrs)
		expr, err := parsePredicate(substituted)
		if err != nil {
			return nil, fmt.Errorf("invalid row policy %q: %w", p, err)
		}
		if combined == nil {
			combined = expr
		} else {
			combined = &sqlparser.AndExpr{Left: combined, Right: expr}
		}
	}
	return combined, nil
}

// parsePredicate parses a SQL boolean expression via a throwaway SELECT.
func parsePredicate(pred string) (sqlparser.Expr, error) {
	stmt, err := sqlparser.Parse("SELECT 1 FROM t WHERE " + pred)
	if err != nil {
		return nil, err
	}
	sel := stmt.(*sqlparser.Select)
	if sel.Where == nil {
		return nil, fmt.Errorf("empty predicate")
	}
	return sel.Where.Expr, nil
}

// substituteAttrs replaces :name tokens with quoted, escaped attribute values.
// Unknown attributes become NULL (safe deny).
func substituteAttrs(pred string, attrs map[string]string) string {
	var b strings.Builder
	i := 0
	for i < len(pred) {
		c := pred[i]
		if c == ':' {
			j := i + 1
			for j < len(pred) && isIdentChar(pred[j]) {
				j++
			}
			key := pred[i+1 : j]
			if v, ok := attrs[key]; ok {
				b.WriteString(quoteLit(v))
			} else {
				b.WriteString("NULL")
			}
			i = j
			continue
		}
		b.WriteByte(c)
		i++
	}
	return b.String()
}

func quoteLit(v string) string {
	return "'" + strings.ReplaceAll(v, "'", "''") + "'"
}

// isWriteKeyword reports whether a raw SQL string is a write (non-SELECT)
// statement, used only for the superuser fast path.
func isWriteKeyword(sql string) bool {
	s := strings.TrimSpace(sql)
	if i := strings.IndexAny(s, " \t\n"); i >= 0 {
		s = strings.ToUpper(s[:i])
	} else {
		s = strings.ToUpper(s)
	}
	switch s {
	case "INSERT", "UPDATE", "DELETE", "REPLACE", "CREATE", "DROP", "ALTER", "TRUNCATE":
		return true
	}
	return false
}

func isIdentChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') || c == '_'
}

// collectTopLevelTables flattens a (possibly joined) FROM expression into its
// leaf AliasedTableExprs without descending into nested subqueries.
func collectTopLevelTables(te sqlparser.TableExpr) []*sqlparser.AliasedTableExpr {
	var out []*sqlparser.AliasedTableExpr
	switch n := te.(type) {
	case *sqlparser.AliasedTableExpr:
		out = append(out, n)
	case *sqlparser.JoinTableExpr:
		out = append(out, collectTopLevelTables(n.LeftExpr)...)
		out = append(out, collectTopLevelTables(n.RightExpr)...)
	}
	return out
}

// computeColumnMask aggregates denied/allowed columns from referenced tables.
func computeColumnMask(tables []string, perms map[string]*store.TableEffective, res *RewriteResult) {
	deniedSet := map[string]bool{}
	allowSet := map[string]bool{}
	allowActive := false
	for _, key := range tables {
		eff := perms[key]
		if eff == nil {
			continue
		}
		for _, c := range eff.DeniedCols {
			deniedSet[strings.ToLower(c)] = true
		}
		if len(eff.AllowedCols) > 0 {
			allowActive = true
			for _, c := range eff.AllowedCols {
				allowSet[strings.ToLower(c)] = true
			}
		}
	}
	for c := range deniedSet {
		res.DeniedCols = append(res.DeniedCols, c)
	}
	if allowActive {
		for c := range allowSet {
			res.AllowedCols = append(res.AllowedCols, c)
		}
	}
}
