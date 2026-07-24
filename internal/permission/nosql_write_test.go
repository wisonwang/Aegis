package permission

import (
	"encoding/json"
	"testing"

	"github.com/wisonwang/aegis/internal/store"
)

func effWrite(ops map[string]bool, policies []string, allowed, denied []string) map[string]*store.TableEffective {
	return map[string]*store.TableEffective{
		"orders": {TableName: "orders", Ops: ops, RowPolicies: policies, AllowedCols: allowed, DeniedCols: denied},
		"logs":   {TableName: "logs", Ops: ops, RowPolicies: policies, AllowedCols: allowed, DeniedCols: denied},
	}
}

func unmarshalMap(t *testing.T, raw json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("payload not json: %v", err)
	}
	return m
}

func TestGovernMongoWriteInsertDeniedCol(t *testing.T) {
	perms := effWrite(map[string]bool{"INSERT": true}, nil, []string{"status", "amount", "customer"}, []string{"secret"})
	_, err := GovernNoSQLWrite("mongo",
		json.RawMessage(`{"op":"insert","collection":"orders","document":{"status":"open","amount":1,"customer":"a","secret":"x"}}`),
		perms, false, false)
	if err == nil {
		t.Fatal("expected denial for write to denied column 'secret'")
	}
}

func TestGovernMongoWriteInsertProjection(t *testing.T) {
	perms := effWrite(map[string]bool{"INSERT": true}, nil, []string{"status", "amount"}, nil)
	gov, err := GovernNoSQLWrite("mongo",
		json.RawMessage(`{"op":"insert","collection":"orders","document":{"status":"open","amount":1,"extra":"y"}}`),
		perms, false, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	w := unmarshalMap(t, gov.Payload.Raw)
	var doc map[string]interface{}
	if err := json.Unmarshal(w["document"], &doc); err != nil {
		t.Fatalf("document: %v", err)
	}
	if _, ok := doc["extra"]; ok {
		t.Fatalf("non-allowed column 'extra' should be dropped: %v", doc)
	}
	if _, ok := doc["status"]; !ok {
		t.Fatalf("allowed column 'status' dropped: %v", doc)
	}
}

func TestGovernMongoWriteUpdateNoWhereDenied(t *testing.T) {
	perms := effWrite(map[string]bool{"UPDATE": true}, nil, nil, nil)
	_, err := GovernNoSQLWrite("mongo",
		json.RawMessage(`{"op":"update","collection":"orders","filter":{},"update":{"$set":{"amount":1}}}`),
		perms, false, false)
	if err == nil {
		t.Fatal("expected denial for no-where update")
	}
}

func TestGovernMongoWriteUpdateRowPolicy(t *testing.T) {
	perms := effWrite(map[string]bool{"UPDATE": true}, []string{`{"tenant":"acme"}`}, nil, nil)
	gov, err := GovernNoSQLWrite("mongo",
		json.RawMessage(`{"op":"update","collection":"orders","filter":{"status":"open"},"update":{"$set":{"amount":1}}}`),
		perms, false, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	w := unmarshalMap(t, gov.Payload.Raw)
	var filter map[string]interface{}
	if err := json.Unmarshal(w["filter"], &filter); err != nil {
		t.Fatalf("filter: %v", err)
	}
	and, ok := filter["$and"].([]interface{})
	if !ok || len(and) != 2 {
		t.Fatalf("expected $and with 2 clauses, got %v", filter)
	}
	if gov.CountPayload.Raw == nil {
		t.Fatal("expected CountPayload to be set for update")
	}
}

func TestGovernMongoWriteDeleteNoWhereAllowed(t *testing.T) {
	perms := effWrite(map[string]bool{"DELETE": true}, nil, nil, nil)
	gov, err := GovernNoSQLWrite("mongo",
		json.RawMessage(`{"op":"delete","collection":"orders","filter":{}}`),
		perms, false, true) // allowNoWhere = true
	if err != nil {
		t.Fatalf("no-where delete should be allowed when allowNoWhere=true: %v", err)
	}
	if gov.CountPayload.Raw == nil {
		t.Fatal("expected CountPayload for delete")
	}
}

func TestGovernNoSQLWriteSuperuserBypass(t *testing.T) {
	raw := json.RawMessage(`{"op":"delete","collection":"orders","filter":{}}`)
	gov, err := GovernNoSQLWrite("mongo", raw, nil, true, false)
	if err != nil {
		t.Fatalf("superuser should bypass: %v", err)
	}
	if string(gov.Payload.Raw) != string(raw) {
		t.Fatalf("superuser payload changed: %s", gov.Payload.Raw)
	}
}

func TestGovernESWriteIndexProjection(t *testing.T) {
	perms := effWrite(map[string]bool{"INSERT": true, "UPDATE": true}, nil, []string{"ts", "msg"}, []string{"secret"})
	// denied column should be rejected
	if _, err := GovernNoSQLWrite("es",
		json.RawMessage(`{"op":"index","index":"logs","document":{"ts":1,"msg":"a","secret":"x"}}`),
		perms, false, false); err == nil {
		t.Fatal("expected denial for write to denied column 'secret'")
	}
	// non-allowed column should be dropped
	gov, err := GovernNoSQLWrite("es",
		json.RawMessage(`{"op":"index","index":"logs","document":{"ts":1,"msg":"a","extra":"y"}}`),
		perms, false, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	w := unmarshalMap(t, gov.Payload.Raw)
	var doc map[string]interface{}
	if err := json.Unmarshal(w["document"], &doc); err != nil {
		t.Fatalf("document: %v", err)
	}
	if _, ok := doc["extra"]; ok {
		t.Fatalf("non-allowed column 'extra' should be dropped: %v", doc)
	}
}

func TestGovernESWriteUpdateByQueryRowPolicy(t *testing.T) {
	perms := effWrite(map[string]bool{"UPDATE": true}, []string{`{"range":{"ts":{"gte":"now-1d"}}}`}, nil, nil)
	gov, err := GovernNoSQLWrite("es",
		json.RawMessage(`{"op":"updateByQuery","index":"logs","query":{"term":{"x":1}}}`),
		perms, false, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	w := unmarshalMap(t, gov.Payload.Raw)
	var query map[string]interface{}
	if err := json.Unmarshal(w["query"], &query); err != nil {
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
	if gov.CountPayload.Raw == nil {
		t.Fatal("expected CountPayload for updateByQuery")
	}
}
