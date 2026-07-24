package permission

import (
	"strings"
	"testing"

	"github.com/fosun/aegis/internal/store"
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
