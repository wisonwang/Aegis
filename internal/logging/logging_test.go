package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// withStderr swaps os.Stderr for a pipe so we can capture the default
// slog handler output, then restores it. Returns a function that closes the
// write end and returns the captured bytes. Call it BEFORE Init so the
// installed handler targets the pipe.
func withStderr(t *testing.T) func() []byte {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	old := os.Stderr
	os.Stderr = w
	return func() []byte {
		_ = w.Close()
		os.Stderr = old
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		return buf.Bytes()
	}
}

func TestInitJSONOutput(t *testing.T) {
	drain := withStderr(t)
	Init(Config{Format: "json", Level: "info"})
	With("k", "v").Info("hello")
	out := drain()

	var m map[string]interface{}
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, out)
	}
	if m["msg"] != "hello" {
		t.Errorf("msg = %v, want hello", m["msg"])
	}
	if m["k"] != "v" {
		t.Errorf("attr k = %v, want v", m["k"])
	}
	if m["level"] != "INFO" {
		t.Errorf("level = %v, want INFO", m["level"])
	}
}

func TestInitTextOutput(t *testing.T) {
	drain := withStderr(t)
	Init(Config{Format: "text", Level: "info"})
	With("k", "v").Info("hello")
	out := string(drain())

	if !strings.Contains(out, "msg=hello") || !strings.Contains(out, "k=v") {
		t.Errorf("unexpected text output: %q", out)
	}
}

func TestLevelFiltering(t *testing.T) {
	drain := withStderr(t)
	Init(Config{Format: "json", Level: "error"})
	With().Info("should be dropped")
	With().Error("keep me")
	out := drain()

	var count int
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		count++
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("bad json: %v", line)
		}
		if m["msg"] != "keep me" {
			t.Errorf("unexpected record: %s", line)
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 record after level filter, got %d", count)
	}
}

func TestParseLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"debug":   slog.LevelDebug,
		"INFO":    slog.LevelInfo,
		"warn":     slog.LevelWarn,
		"warning":  slog.LevelWarn,
		"error":    slog.LevelError,
		"":         slog.LevelInfo,
	}
	for in, want := range cases {
		if got := ParseLevel(in); got != want {
			t.Errorf("ParseLevel(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestContextLoggerPropagates(t *testing.T) {
	drain := withStderr(t)
	Init(Config{Format: "json", Level: "debug"})
	ctx := NewCtx(context.Background(), "req_id", "abc123")
	WithCtx(ctx, "extra", "x").Info("ctx event")
	out := drain()

	var m map[string]interface{}
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if m["req_id"] != "abc123" {
		t.Errorf("req_id = %v, want abc123", m["req_id"])
	}
	if m["extra"] != "x" {
		t.Errorf("extra = %v, want x", m["extra"])
	}
}

func TestMiddlewareEmitsAccessLog(t *testing.T) {
	drain := withStderr(t)
	Init(Config{Format: "json", Level: "info"})
	probe := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	srv := httptest.NewServer(probe)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/query")
	if err != nil {
		t.Fatalf("probe request: %v", err)
	}
	resp.Body.Close()
	out := drain()

	var found bool
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("bad json: %v", line)
		}
		if m["msg"] == "http request rejected" && m["path"] == "/api/v1/query" {
			if m["status"] != float64(http.StatusForbidden) {
				t.Errorf("status = %v, want 403", m["status"])
			}
			if m["req_id"] == "" {
				t.Errorf("req_id missing from access log: %s", line)
			}
			if m["level"] != "WARN" {
				t.Errorf("level = %v, want WARN", m["level"])
			}
			found = true
		}
	}
	if !found {
		t.Errorf("access-log line not found in: %s", out)
	}
}

func TestMiddlewareSkipsProbes(t *testing.T) {
	drain := withStderr(t)
	Init(Config{Format: "json", Level: "info"})
	probe := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv := httptest.NewServer(probe)
	defer srv.Close()

	for _, p := range []string{"/api/v1/health", "/api/v1/ready", "/metrics"} {
		resp, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatalf("request %s: %v", p, err)
		}
		resp.Body.Close()
	}
	out := drain()
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("probe paths should not be access-logged, got: %s", out)
	}
}
