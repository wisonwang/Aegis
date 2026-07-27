package permission

import (
	"strings"
	"testing"

	"github.com/wisonwang/aegis/internal/store"
)

func permsFor(table string, ops ...string) map[string]*store.TableEffective {
	opMap := map[string]bool{}
	for _, o := range ops {
		opMap[o] = true
	}
	return map[string]*store.TableEffective{
		table: {Ops: opMap},
	}
}

func TestWriteHasWhere_NoPolicy(t *testing.T) {
	// UPDATE without WHERE, no row policy -> must be flagged unsafe.
	rr, err := Rewrite("UPDATE orders SET status='x'", permsFor("orders", "UPDATE"), nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rr.WriteHasWhere {
		t.Errorf("expected WriteHasWhere=false for UPDATE without WHERE")
	}
	if rr.CountCheckSQL == "" {
		t.Errorf("expected CountCheckSQL for single-table UPDATE")
	}
	if !strings.Contains(rr.CountCheckSQL, "SELECT COUNT(*) FROM orders") {
		t.Errorf("unexpected CountCheckSQL: %s", rr.CountCheckSQL)
	}

	// UPDATE with WHERE -> safe.
	rr, err = Rewrite("UPDATE orders SET status='x' WHERE id=1", permsFor("orders", "UPDATE"), nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !rr.WriteHasWhere {
		t.Errorf("expected WriteHasWhere=true for UPDATE with WHERE")
	}

	// DELETE without WHERE -> unsafe.
	rr, err = Rewrite("DELETE FROM orders", permsFor("orders", "DELETE"), nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rr.WriteHasWhere {
		t.Errorf("expected WriteHasWhere=false for DELETE without WHERE")
	}
}

func TestWriteHasWhere_RowPolicyInjected(t *testing.T) {
	// No user WHERE, but an injected row policy bounds the write -> safe.
	p := permsFor("orders", "UPDATE")
	p["orders"].RowPolicies = []string{"tenant_id = :tenant"}
	rr, err := Rewrite("UPDATE orders SET status='x'", p, map[string]string{"tenant": "acme"}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !rr.WriteHasWhere {
		t.Errorf("row-policy-injected write should be treated as having a WHERE")
	}
	if !strings.Contains(rr.CountCheckSQL, "tenant_id") {
		t.Errorf("count check should include injected policy: %s", rr.CountCheckSQL)
	}
}

func TestWriteProtection_InsertSafe(t *testing.T) {
	rr, err := Rewrite("INSERT INTO orders (tenant_id, status) VALUES ('acme','open')", permsFor("orders", "INSERT"), nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !rr.WriteHasWhere {
		t.Errorf("INSERT must not trip the no-WHERE guard")
	}
}

func TestWriteProtection_SuperuserBypasses(t *testing.T) {
	rr, err := Rewrite("DELETE FROM orders", permsFor("orders", "DELETE"), nil, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !rr.WriteHasWhere {
		t.Errorf("superuser write should be marked safe (guard exempts admin)")
	}
}

// withRowPolicy builds a perms map where every listed table has SELECT granted
// and (optionally) the given row policies.
func withRowPolicy(policies map[string][]string) map[string]*store.TableEffective {
	m := map[string]*store.TableEffective{}
	for t, ps := range policies {
		m[t] = &store.TableEffective{Ops: map[string]bool{"SELECT": true}, RowPolicies: ps}
	}
	return m
}

// TestRowPolicy_NestedSubqueryInWhere verifies the second hardening layer: a
// governed table referenced only inside a nested subquery still receives its
// row policy. Before this change the inner table was silently bypassed.
func TestRowPolicy_NestedSubqueryInWhere(t *testing.T) {
	p := withRowPolicy(map[string][]string{
		"orders":     {"tenant_id = :tenant"},
		"line_items": nil,
	})
	rr, err := Rewrite("SELECT * FROM orders WHERE id IN (SELECT order_id FROM line_items)", p, map[string]string{"tenant": "acme"}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	low := strings.ToLower(rr.SQL)
	if !strings.Contains(low, "tenant_id = 'acme'") {
		t.Errorf("row policy not injected: %s", rr.SQL)
	}
	if !strings.Contains(low, "(select * from orders where tenant_id = 'acme')") {
		t.Errorf("nested orders reference not governed: %s", rr.SQL)
	}
}

// TestRowPolicy_NestedSubqueryBothGoverned checks that BOTH tables in a
// top-level + subquery pair are wrapped when both carry policies.
func TestRowPolicy_NestedSubqueryBothGoverned(t *testing.T) {
	p := withRowPolicy(map[string][]string{
		"orders":     {"tenant_id = :tenant"},
		"line_items": {"tenant_id = :tenant"},
	})
	rr, err := Rewrite("SELECT * FROM orders WHERE id IN (SELECT order_id FROM line_items)", p, map[string]string{"tenant": "acme"}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	low := strings.ToLower(rr.SQL)
	if !strings.Contains(low, "(select * from orders where tenant_id = 'acme')") {
		t.Errorf("orders not governed in nested query: %s", rr.SQL)
	}
	if !strings.Contains(low, "(select * from line_items where tenant_id = 'acme')") {
		t.Errorf("line_items not governed in nested subquery: %s", rr.SQL)
	}
}

// TestRowPolicy_ScalarSubqueryInSelect ensures a scalar subquery in the SELECT
// list is governed independently of the outer query.
func TestRowPolicy_ScalarSubqueryInSelect(t *testing.T) {
	p := withRowPolicy(map[string][]string{"orders": {"tenant_id = :tenant"}})
	rr, err := Rewrite("SELECT (SELECT count(*) FROM orders) AS c FROM orders", p, map[string]string{"tenant": "acme"}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	low := strings.ToLower(rr.SQL)
	if cnt := strings.Count(low, "(select * from orders where tenant_id = 'acme')"); cnt != 2 {
		t.Errorf("expected both orders refs governed (count=2), got %d: %s", cnt, rr.SQL)
	}
}

// TestRowPolicy_ExistsSubquery checks correlated EXISTS subqueries govern both
// the outer and inner tables (with their aliases preserved).
func TestRowPolicy_ExistsSubquery(t *testing.T) {
	p := withRowPolicy(map[string][]string{
		"orders":     {"tenant_id = :tenant"},
		"line_items": {"tenant_id = :tenant"},
	})
	rr, err := Rewrite("SELECT * FROM orders o WHERE EXISTS (SELECT 1 FROM line_items li WHERE li.order_id = o.id)", p, map[string]string{"tenant": "acme"}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	low := strings.ToLower(rr.SQL)
	if !strings.Contains(low, "(select * from orders where tenant_id = 'acme') as o") {
		t.Errorf("orders (alias o) not governed: %s", rr.SQL)
	}
	if !strings.Contains(low, "(select * from line_items where tenant_id = 'acme') as li") {
		t.Errorf("line_items (alias li) not governed: %s", rr.SQL)
	}
}

// TestRowPolicy_DerivedTableInFrom ensures a table hidden behind a derived
// table in the FROM clause is still governed in its own scope.
func TestRowPolicy_DerivedTableInFrom(t *testing.T) {
	p := withRowPolicy(map[string][]string{"orders": {"tenant_id = :tenant"}})
	rr, err := Rewrite("SELECT * FROM (SELECT * FROM orders) x", p, map[string]string{"tenant": "acme"}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	low := strings.ToLower(rr.SQL)
	if !strings.Contains(low, "(select * from orders where tenant_id = 'acme') as orders") {
		t.Errorf("inner orders in derived table not governed: %s", rr.SQL)
	}
}

// TestRowPolicy_DeniedInSubquery confirms a table referenced only inside a
// subquery is still subject to the default-deny permission check.
func TestRowPolicy_DeniedInSubquery(t *testing.T) {
	p := withRowPolicy(map[string][]string{"orders": {"tenant_id = :tenant"}}) // secret absent
	_, err := Rewrite("SELECT * FROM orders WHERE id IN (SELECT id FROM secret)", p, map[string]string{"tenant": "acme"}, false)
	if err == nil {
		t.Fatalf("expected access denied for secret referenced only in a subquery")
	}
}

// TestRowPolicy_SelfJoinGoverned verifies a self-join wraps each table
// reference once (no double-wrapping, no missed reference).
func TestRowPolicy_SelfJoinGoverned(t *testing.T) {
	p := withRowPolicy(map[string][]string{"orders": {"tenant_id = :tenant"}})
	rr, err := Rewrite("SELECT * FROM orders a JOIN orders b ON a.id = b.id", p, map[string]string{"tenant": "acme"}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	low := strings.ToLower(rr.SQL)
	if cnt := strings.Count(low, "(select * from orders where tenant_id = 'acme')"); cnt != 2 {
		t.Errorf("expected both self-join refs governed (count=2), got %d: %s", cnt, rr.SQL)
	}
}

// TestRowPolicy_NoDoubleWrap guards against the walk re-descending into a
// freshly wrapped derived table (which would wrap it again).
func TestRowPolicy_NoDoubleWrap(t *testing.T) {
	p := withRowPolicy(map[string][]string{"orders": {"tenant_id = :tenant"}})
	rr, err := Rewrite("SELECT * FROM orders", p, map[string]string{"tenant": "acme"}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	low := strings.ToLower(rr.SQL)
	// A legitimate single wrap renders as `select * from (select * from orders ...)`.
	// A double wrap would nest a third level: flag that as the failure mode.
	if strings.Contains(low, "select * from (select * from (select * from orders") {
		t.Errorf("table was double-wrapped: %s", rr.SQL)
	}
}

// TestRowPolicy_ColumnMaskFromNestedTable confirms column masks from a table
// referenced only inside a subquery are still aggregated for result masking.
func TestRowPolicy_ColumnMaskFromNestedTable(t *testing.T) {
	p := map[string]*store.TableEffective{
		"orders":     {Ops: map[string]bool{"SELECT": true}, RowPolicies: []string{"tenant_id = :tenant"}},
		"line_items": {Ops: map[string]bool{"SELECT": true}, DeniedCols: []string{"amount"}},
	}
	rr, err := Rewrite("SELECT * FROM orders WHERE id IN (SELECT order_id FROM line_items)", p, map[string]string{"tenant": "acme"}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, c := range rr.DeniedCols {
		if c == "amount" {
			found = true
		}
	}
	if !found {
		t.Errorf("denied column from nested table not aggregated: %v", rr.DeniedCols)
	}
}

// TestRowPolicy_DMLNestedSubquery verifies the write path also hardens nested
// subqueries: the DML target keeps its policy in the top-level WHERE, while a
// governed table inside a SET subquery is wrapped in place.
func TestRowPolicy_DMLNestedSubquery(t *testing.T) {
	p := map[string]*store.TableEffective{
		"orders":     {Ops: map[string]bool{"UPDATE": true}, RowPolicies: []string{"tenant_id = :tenant"}},
		"line_items": {Ops: map[string]bool{"SELECT": true}, RowPolicies: []string{"tenant_id = :tenant"}},
	}
	rr, err := Rewrite("UPDATE orders SET total = (SELECT sum(amount) FROM line_items) WHERE id = 1", p, map[string]string{"tenant": "acme"}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	low := strings.ToLower(rr.SQL)
	if !strings.Contains(low, "where id = 1 and tenant_id = 'acme'") {
		t.Errorf("target orders policy not appended to WHERE: %s", rr.SQL)
	}
	if !strings.Contains(low, "(select * from line_items where tenant_id = 'acme')") {
		t.Errorf("nested line_items in DML not governed: %s", rr.SQL)
	}
}
