package permission

import (
	"encoding/json"
	"testing"

	"github.com/wisonwang/aegis/internal/store"
)

func effMongo(ops map[string]bool, policies []string, allowed, denied []string, masks []store.MaskSpec) map[string]*store.TableEffective {
	return map[string]*store.TableEffective{
		"orders": {TableName: "orders", Ops: ops, RowPolicies: policies, AllowedCols: allowed, DeniedCols: denied, Masks: masks},
	}
}

func TestGovernMongoBasic(t *testing.T) {
	perms := effMongo(
		map[string]bool{"SELECT": true},
		[]string{`{"tenant":"acme"}`},
		[]string{"status", "amount"},
		nil,
		[]store.MaskSpec{{Column: "amount", Strategy: "partial", Keep: 2}},
	)
	gov, err := GovernNoSQL("mongo", json.RawMessage(`{"collection":"orders","filter":{"status":"open"}}`), perms, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var q map[string]json.RawMessage
	if err := json.Unmarshal(gov.Payload.Raw, &q); err != nil {
		t.Fatalf("payload not json: %v", err)
	}
	if string(q["collection"]) != `"orders"` {
		t.Fatalf("collection: %s", q["collection"])
	}
	// filter should AND the user filter with the row policy.
	var filter map[string]interface{}
	if err := json.Unmarshal(q["filter"], &filter); err != nil {
		t.Fatalf("filter: %v", err)
	}
	and, ok := filter["$and"].([]interface{})
	if !ok || len(and) != 2 {
		t.Fatalf("expected $and with 2 clauses, got %v", filter)
	}
	// projection should allow only status+amount.
	var proj map[string]int
	if err := json.Unmarshal(q["projection"], &proj); err != nil {
		t.Fatalf("projection: %v", err)
	}
	if proj["status"] != 1 || proj["amount"] != 1 || len(proj) != 2 {
		t.Fatalf("projection wrong: %v", proj)
	}
	if _, ok := gov.Masks["amount"]; !ok {
		t.Fatalf("expected mask for amount")
	}
}

func TestGovernMongoDenied(t *testing.T) {
	perms := map[string]*store.TableEffective{} // no permission
	_, err := GovernNoSQL("mongo", json.RawMessage(`{"collection":"orders"}`), perms, false)
	if err == nil {
		t.Fatal("expected denial for ungranted collection")
	}
}

func TestGovernMongoNoSelect(t *testing.T) {
	perms := effMongo(map[string]bool{"SELECT": false}, nil, nil, nil, nil)
	_, err := GovernNoSQL("mongo", json.RawMessage(`{"collection":"orders"}`), perms, false)
	if err == nil {
		t.Fatal("expected SELECT denial")
	}
}

func TestGovernESSuperuserBypass(t *testing.T) {
	raw := json.RawMessage(`{"index":"logs","query":{"term":{"x":1}}}`)
	gov, err := GovernNoSQL("es", raw, nil, true)
	if err != nil {
		t.Fatalf("superuser should bypass: %v", err)
	}
	if string(gov.Payload.Raw) != string(raw) {
		t.Fatalf("superuser payload changed: %s", gov.Payload.Raw)
	}
}

func TestGovernESQueryAndSource(t *testing.T) {
	perms := map[string]*store.TableEffective{
		"logs": {
			Ops:         map[string]bool{"SELECT": true},
			RowPolicies: []string{`{"range":{"ts":{"gte":"now-1d"}}}`},
			AllowedCols: []string{"ts", "msg"},
		},
	}
	gov, err := GovernNoSQL("es", json.RawMessage(`{"index":"logs","query":{"term":{"x":1}}}`), perms, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var q map[string]json.RawMessage
	if err := json.Unmarshal(gov.Payload.Raw, &q); err != nil {
		t.Fatalf("payload: %v", err)
	}
	var query map[string]interface{}
	if err := json.Unmarshal(q["query"], &query); err != nil {
		t.Fatalf("query: %v", err)
	}
	boolq, ok := query["bool"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected bool wrapper, got %v", query)
	}
	must, ok := boolq["must"].([]interface{})
	if !ok || len(must) != 2 {
		t.Fatalf("expected 2 must clauses, got %v", boolq)
	}
	var src map[string]interface{}
	if err := json.Unmarshal(q["_source"], &src); err != nil {
		t.Fatalf("_source: %v", err)
	}
	includes, ok := src["includes"].([]interface{})
	if !ok || len(includes) != 2 {
		t.Fatalf("expected includes of 2, got %v", src)
	}
}
