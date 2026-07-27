package nl2sql

import (
	"context"
	"strings"
	"testing"
)

func TestValidateReadOnly(t *testing.T) {
	ok := []string{
		"SELECT * FROM customers",
		"  select id from users",
		"WITH t AS (SELECT 1) SELECT * FROM t",
		"SELECT a.id, count(*) FROM a JOIN b ON a.id=b.aid GROUP BY a.id",
	}
	for _, s := range ok {
		if err := ValidateReadOnly(s); err != nil {
			t.Fatalf("expected read-only allowed: %q -> %v", s, err)
		}
	}
	bad := []string{
		"INSERT INTO customers (name) VALUES ('x')",
		"UPDATE customers SET name='y'",
		"DELETE FROM customers",
		"DROP TABLE customers",
		"SELECT * FROM t; CREATE TABLE evil(x int)", // contains CREATE as a word
		"SELECT * FROM t INTO OUTFILE '/tmp/x'",
		"",
	}
	for _, s := range bad {
		if err := ValidateReadOnly(s); err == nil {
			t.Fatalf("expected rejection of: %q", s)
		}
	}
}

func TestStubGeneratorHint(t *testing.T) {
	g := &StubGenerator{DefaultSQL: "SELECT 1"}
	res, err := g.Generate(context.Background(), &Request{
		Question: "anything",
		SQLHint:  "SELECT id FROM customers",
	})
	if err != nil {
		t.Fatalf("stub hint: %v", err)
	}
	if res.SQL != "SELECT id FROM customers" {
		t.Fatalf("hint not used: %q", res.SQL)
	}
	// A mutating hint must be rejected even via stub.
	if _, err := g.Generate(context.Background(), &Request{SQLHint: "DELETE FROM customers"}); err == nil {
		t.Fatal("stub must validate read-only even for hints")
	}
}

func TestStubGeneratorKeyword(t *testing.T) {
	g := &StubGenerator{
		ByKeyword: map[string]string{"count orders": "SELECT count(*) FROM orders"},
	}
	res, err := g.Generate(context.Background(), &Request{Question: "How many count orders do we have?"})
	if err != nil {
		t.Fatalf("keyword: %v", err)
	}
	if res.SQL != "SELECT count(*) FROM orders" {
		t.Fatalf("keyword miss: %q", res.SQL)
	}
}

func TestStubGeneratorDefault(t *testing.T) {
	g := &StubGenerator{DefaultSQL: "SELECT 1"}
	res, err := g.Generate(context.Background(), &Request{Question: "??"})
	if err != nil || res.SQL != "SELECT 1" {
		t.Fatalf("default: %v %q", err, res.SQL)
	}
}

// minimalFakeServer is a tiny in-process OpenAI-compatible stub used to verify
// LLMGenerator's request/response handling and validation without the network.
type minimalFakeServer struct {
	lastBody string
	respond  string // raw body to return
	code     int
}

func TestLLMGeneratorParsesJSON(t *testing.T) {
	srv := &minimalFakeServer{
		respond: `{"choices":[{"message":{"content":"{\"sql\":\"SELECT id FROM customers\",\"explanation\":\"get ids\"}"}}]}`,
		code:    200,
	}
	ts := newTestServer(t, srv)
	g := NewLLMGenerator(LLMConfig{BaseURL: ts.URL, APIKey: "k", Model: "m"})
	res, err := g.Generate(context.Background(), &Request{
		SchemaMarkdown: "# schema",
		Question:       "list customer ids",
		Dialect:        "sqlite",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if res.SQL != "SELECT id FROM customers" {
		t.Fatalf("sql: %q", res.SQL)
	}
	if !strings.Contains(srv.lastBody, "/chat/completions") {
		t.Fatalf("wrong endpoint hit: %s", srv.lastBody)
	}
	if !strings.Contains(srv.lastBody, "Bearer k") {
		t.Fatalf("auth header missing: %s", srv.lastBody)
	}
}

func TestLLMGeneratorRejectsWrite(t *testing.T) {
	srv := &minimalFakeServer{
		respond: `{"choices":[{"message":{"content":"{\"sql\":\"DELETE FROM customers\",\"explanation\":\"oops\"}"}}]}`,
		code:    200,
	}
	ts := newTestServer(t, srv)
	g := NewLLMGenerator(LLMConfig{BaseURL: ts.URL, APIKey: "k", Model: "m"})
	if _, err := g.Generate(context.Background(), &Request{SchemaMarkdown: "x", Question: "q"}); err == nil {
		t.Fatal("expected write to be rejected")
	}
}

func TestLLMGeneratorProviderError(t *testing.T) {
	srv := &minimalFakeServer{respond: `{"error":{"message":"rate limited"}}`, code: 200}
	ts := newTestServer(t, srv)
	g := NewLLMGenerator(LLMConfig{BaseURL: ts.URL, APIKey: "k", Model: "m"})
	if _, err := g.Generate(context.Background(), &Request{SchemaMarkdown: "x", Question: "q"}); err == nil {
		t.Fatal("expected provider error to surface")
	}
}
