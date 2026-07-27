package permission

import (
	"fmt"
	"strings"

	"github.com/wisonwang/aegis/internal/store"
	"github.com/xwb1989/sqlparser"
)

// RewriteResult is the output of governance enforcement.
type RewriteResult struct {
	SQL         string   // rewritten, safe SQL to execute against the backend
	IsRead      bool     // true for SELECT (use Query), false for DML (use Exec)
	DeniedCols  []string // result columns that must be stripped (deny wins)
	AllowedCols []string // if non-empty, only these result columns are permitted
	Masks       map[string]store.MaskSpec // result columns to value-mask (keyed by lower column name)
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

	res := &RewriteResult{Masks: map[string]store.MaskSpec{}}
	var tables []string

	switch node := stmt.(type) {
	case *sqlparser.Select:
		tables, err = rewriteSelect(node, perms, attrs)
		res.IsRead = true
	case *sqlparser.Update:
		var targets []*sqlparser.AliasedTableExpr
		for _, te := range node.TableExprs {
			targets = append(targets, collectTopLevelTables(te)...)
		}
		tables, err = dmlTables(node.TableExprs, perms, "UPDATE")
		if err == nil {
			err = applyRowPolicyDML(tables, &node.Where, perms, attrs)
		}
		if err == nil {
			nested := []string{}
			if e := enforceTableRefs(node, targetSet(targets), perms, attrs, &nested); e != nil {
				err = e
			} else {
				tables = append(tables, nested...)
			}
		}
		if err == nil {
			finalizeWrite(res, node.Where, node.TableExprs)
		}
	case *sqlparser.Delete:
		var targets []*sqlparser.AliasedTableExpr
		for _, te := range node.TableExprs {
			targets = append(targets, collectTopLevelTables(te)...)
		}
		tables, err = dmlTables(node.TableExprs, perms, "DELETE")
		if err == nil {
			err = applyRowPolicyDML(tables, &node.Where, perms, attrs)
		}
		if err == nil {
			nested := []string{}
			if e := enforceTableRefs(node, targetSet(targets), perms, attrs, &nested); e != nil {
				err = e
			} else {
				tables = append(tables, nested...)
			}
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
// policies. Unlike a naive top-level-only pass, it recurses into nested
// subqueries (the "second hardening layer"): every table reference — whether in
// the top-level FROM, a derived table, a scalar subquery in the SELECT list, or
// an IN/EXISTS subquery in WHERE — is governed, closing the nested-subquery
// bypass gap documented in the blueprint.
func rewriteSelect(sel *sqlparser.Select, perms map[string]*store.TableEffective, attrs map[string]string) ([]string, error) {
	var tables []string
	if err := enforceTableRefs(sel, nil, perms, attrs, &tables); err != nil {
		return nil, err
	}
	return tables, nil
}

// enforceTableRefs walks a statement subtree and governs every real table
// reference: it checks SELECT permission and wraps the table in a filtered
// derived subquery that carries its row policies. Tables whose AliasedTableExpr
// is present in skip are left untouched — used so a DML target table (which is
// governed by appending its policy to the top-level WHERE) is not also
// re-wrapped when it happens to be referenced again inside a nested subquery.
//
// Delegating the traversal to sqlparser.Walk guarantees that policies are
// injected at every depth: a wrapped table becomes a derived subquery whose
// inner table must NOT be re-governed (returning false stops the walk from
// descending into it), while a bare table name has no children to visit.
func enforceTableRefs(node sqlparser.SQLNode, skip map[*sqlparser.AliasedTableExpr]bool, perms map[string]*store.TableEffective, attrs map[string]string, tables *[]string) error {
	return sqlparser.Walk(func(n sqlparser.SQLNode) (bool, error) {
		ate, ok := n.(*sqlparser.AliasedTableExpr)
		if !ok {
			// Descend into everything else (SELECT, WHERE, subqueries, ...).
			return true, nil
		}
		if skip[ate] {
			// DML target table: governed elsewhere via the top-level WHERE.
			return false, nil
		}
		tn, ok := ate.Expr.(sqlparser.TableName)
		if !ok {
			// A derived table / subquery used as a table source: descend so the
			// tables it references get governed in their own scope.
			return true, nil
		}
		name := tn.Name.String()
		key := strings.ToLower(name)
		eff, ok := perms[key]
		if !ok {
			return false, fmt.Errorf("access denied to table %q (no permission granted)", name)
		}
		if !eff.Ops["SELECT"] {
			return false, fmt.Errorf("SELECT denied on table %q", name)
		}
		*tables = append(*tables, key)
		if len(eff.RowPolicies) > 0 {
			sub, err := wrapWithPolicy(name, eff.RowPolicies, attrs)
			if err != nil {
				return false, err
			}
			ate.Expr = sub
			// MySQL/PostgreSQL require every derived table to carry an alias.
			// Reuse the original table name when none was given so qualified
			// column references (e.g. orders.id) keep working.
			if ate.As.IsEmpty() {
				ate.As = sqlparser.NewTableIdent(name)
			}
		}
		// Do not descend into this table expression: a wrapped table becomes a
		// derived subquery whose inner table must not be re-governed (avoids
		// double-wrapping), and a bare table name has no children to visit.
		return false, nil
	}, node)
}

// RewriteVirtual applies governance to a dataset: a curated query (definition)
// exposed to callers as a single virtual table named `tableKey`. The definition
// is wrapped as a derived table aliased to tableKey so the existing permission
// engine can treat it exactly like a physical table — SELECT permission is
// checked on tableKey, row policies are injected *into* the definition (so they
// filter the dataset's rows, not a re-wrapped outer query), and column masks
// apply to the result by column name. The underlying physical tables referenced
// by the definition are NOT separately governed: the dataset is the unit of
// access.
//
// superuser bypasses all enforcement (the body is still wrapped so column names
// resolve, but no policy/mask is applied).
func RewriteVirtual(tableKey, definition string, perms map[string]*store.TableEffective, attrs map[string]string, superuser bool) (*RewriteResult, error) {
	if superuser {
		return &RewriteResult{
			SQL:    "SELECT * FROM (" + definition + ") AS " + tableKey,
			IsRead: true,
			Masks:  map[string]store.MaskSpec{},
		}, nil
	}
	wrapped := "SELECT * FROM (" + definition + ") AS " + tableKey
	stmt, err := sqlparser.Parse(wrapped)
	if err != nil {
		return nil, fmt.Errorf("parse dataset definition: %w", err)
	}
	sel, ok := stmt.(*sqlparser.Select)
	if !ok {
		return nil, fmt.Errorf("dataset definition must be a SELECT statement")
	}
	key := strings.ToLower(tableKey)
	eff, ok := perms[key]
	if !ok {
		return nil, fmt.Errorf("access denied to dataset %q (no permission granted)", tableKey)
	}
	if !eff.Ops["SELECT"] {
		return nil, fmt.Errorf("SELECT denied on dataset %q", tableKey)
	}
	if len(eff.RowPolicies) > 0 {
		combined, err := combinePredicates(eff.RowPolicies, attrs)
		if err != nil {
			return nil, err
		}
		for _, ate := range collectTopLevelTables(sel.From[0]) {
			sub, ok := ate.Expr.(*sqlparser.Subquery)
			if !ok {
				continue
			}
			inner, ok := sub.Select.(*sqlparser.Select)
			if !ok {
				continue
			}
			if inner.Where == nil {
				inner.Where = &sqlparser.Where{Type: sqlparser.WhereStr, Expr: combined}
			} else {
				inner.Where.Expr = &sqlparser.AndExpr{Left: inner.Where.Expr, Right: combined}
			}
		}
	}
	res := &RewriteResult{SQL: sqlparser.String(stmt), IsRead: true, Masks: map[string]store.MaskSpec{}}
	computeColumnMask([]string{key}, perms, res)
	return res, nil
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

// targetSet builds a pointer identity set of the DML's direct target tables so
// that enforceTableRefs can skip exactly those (governing them via the
// top-level WHERE) while still hardening any nested subquery that references the
// same table under a different node.
func targetSet(targets []*sqlparser.AliasedTableExpr) map[*sqlparser.AliasedTableExpr]bool {
	m := make(map[*sqlparser.AliasedTableExpr]bool, len(targets))
	for _, t := range targets {
		m[t] = true
	}
	return m
}

// computeColumnMask aggregates denied/allowed columns and value-masks from
// referenced tables.
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
		for _, m := range eff.Masks {
			res.Masks[strings.ToLower(m.Column)] = m
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
