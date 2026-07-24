package permission

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/wisonwang/aegis/internal/store"
)

func dsPerms(eff *store.TableEffective) map[string]*store.TableEffective {
	return map[string]*store.TableEffective{strings.ToLower(eff.TableName): eff}
}

func TestRewriteVirtualSQL(t *testing.T) {
	def := "SELECT id, tenant_id, amount FROM orders WHERE status = 'paid'"

	// Analyst: SELECT on the dataset, scoped to a tenant, amount masked.
	eff := &store.TableEffective{
		TableName:   "paid_orders",
		Ops:         map[string]bool{"SELECT": true},
		RowPolicies: []string{"tenant_id = 'acme'"},
		Masks:       []store.MaskSpec{{Column: "amount", Strategy: "hash"}},
	}
	rr, err := RewriteVirtual("paid_orders", def, dsPerms(eff), nil, false)
	if err != nil {
		t.Fatalf("RewriteVirtual: %v", err)
	}
	if !rr.IsRead {
		t.Fatal("dataset must be read-only")
	}
	// Row policy must be injected INTO the definition body.
	if !strings.Contains(rr.SQL, "tenant_id = 'acme'") {
		t.Fatalf("row policy not injected: %s", rr.SQL)
	}
	// Column mask must be present.
	if ms, ok := rr.Masks["amount"]; !ok || ms.Strategy != "hash" {
		t.Fatalf("amount mask missing: %v", rr.Masks)
	}

	// Access denied when no SELECT grant.
	_, err = RewriteVirtual("paid_orders", def, map[string]*store.TableEffective{}, nil, false)
	if err == nil || !strings.Contains(err.Error(), "access denied") {
		t.Fatalf("expected access denied, got %v", err)
	}

	// Superuser bypasses governance but still wraps so columns resolve.
	rr2, err := RewriteVirtual("paid_orders", def, nil, nil, true)
	if err != nil {
		t.Fatalf("superuser RewriteVirtual: %v", err)
	}
	if len(rr2.Masks) != 0 {
		t.Fatalf("superuser should not mask: %v", rr2.Masks)
	}
}

func TestGovernNoSQLVirtual(t *testing.T) {
	def := `{"collection":"orders","filter":{"status":"paid"}}`

	eff := &store.TableEffective{
		TableName:   "paid_orders",
		Ops:         map[string]bool{"SELECT": true},
		RowPolicies: []string{`{"tenant_id":"acme"}`},
		Masks:       []store.MaskSpec{{Column: "amount", Strategy: "hash"}},
	}
	gov, err := GovernNoSQLVirtual("mongo", json.RawMessage(def), "paid_orders", dsPerms(eff), false)
	if err != nil {
		t.Fatalf("GovernNoSQLVirtual: %v", err)
	}
	var out map[string]interface{}
	if err := json.Unmarshal(gov.Payload.Raw, &out); err != nil {
		t.Fatalf("payload not json: %v", err)
	}
	// Executes against the REAL collection named in the definition.
	if out["collection"] != "orders" {
		t.Fatalf("should target real collection, got %v", out["collection"])
	}
	// Dataset-level row policy merged into the filter.
	filter, _ := out["filter"].(map[string]interface{})
	if filter == nil {
		t.Fatalf("filter missing: %v", out)
	}
	// Masks carried for result-layer application.
	if ms, ok := gov.Masks["amount"]; !ok || ms.Strategy != "hash" {
		t.Fatalf("amount mask missing: %v", gov.Masks)
	}

	// No grant -> denied.
	_, err = GovernNoSQLVirtual("mongo", json.RawMessage(def), "paid_orders", map[string]*store.TableEffective{}, false)
	if err == nil || !strings.Contains(err.Error(), "access denied") {
		t.Fatalf("expected access denied, got %v", err)
	}
}
