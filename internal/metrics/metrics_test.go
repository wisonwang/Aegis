package metrics

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecordReason_ClassifiesFreeText(t *testing.T) {
	cases := []struct {
		name   string
		errMsg string
		want   string
	}{
		{"no_where_write", "UPDATE/DELETE without WHERE is not permitted (row-policy-bounded writes are allowed; set allow_no_where_writes to override)", ReasonNoWhereWrite},
		{"affected_rows_cap_low", "write would affect 1234 rows, exceeding max_affected_rows=1000", ReasonAffectedRowsCap},
		{"affected_rows_cap_high", "write would affect 9999 rows, exceeding max_affected_rows=5000", ReasonAffectedRowsCap},
		{"rate_limit", "rate limit exceeded: max 60 queries/min per principal", ReasonRateLimit},
		{"query_timeout_deadline", "context deadline exceeded", ReasonQueryTimeout},
		{"query_timeout_explicit", "query timeout: 30s", ReasonQueryTimeout},
		{"access_denied", "SELECT denied on table \"orders\"", ReasonAccessDenied},
		{"access_denied_msg", "no permission granted for table users", ReasonAccessDenied},
		{"rls_no_match", "row policy rejects request (no rows match)", ReasonRLSNoMatch},
		{"dataset_not_found", "dataset \"paid_orders\" not found", ReasonDatasetNotFound},
		{"dataset_denied", "SELECT denied on dataset \"revenue_pii\"", ReasonDatasetDenied},
		{"dataset_not_ready", "dataset is not published", ReasonDatasetNotReady},
		{"method_not_allowed", "statement type not permitted by the proxy", ReasonMethodNotAllowed},
		{"unknown", "backend exploded", ReasonUnknown},
		{"empty", "", ReasonUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyReason(tc.errMsg); got != tc.want {
				t.Errorf("ClassifyReason(%q) = %q, want %q", tc.errMsg, got, tc.want)
			}
		})
	}
}

func TestRecordReason_OkIsIgnored(t *testing.T) {
	// Status "ok" must never produce a reason series — RecordReason is a
	// no-op for it. We pin the channel to a unique value so we don't
	// collide with counters from other tests in the same process.
	// First, force the metric to appear in /metrics by recording a non-ok
	// outcome on a *different* channel; otherwise the CounterVec has no
	// series and the HELP/TYPE lines are absent.
	RecordReason("metrics-test-warmup", "denied", "rate limit exceeded")

	const ch = "dataapi-ok-guard"
	RecordReason(ch, "ok", "should-be-ignored")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, "aegis_audit_reasons_total") {
		t.Fatalf("metric aegis_audit_reasons_total not exposed: %s", body)
	}
	if strings.Contains(body, `aegis_audit_reasons_total{channel="`+ch+`"`) {
		t.Errorf("ok status should not produce a reason series, got:\n%s", body)
	}
}

func TestRecordReason_ExposesReasons(t *testing.T) {
	// Two distinct reasons on the same channel must yield two distinct
	// series with different reason labels — this is the cardinality that
	// SREs rely on for alert rules.
	RecordReason("dataapi", "denied", "UPDATE without WHERE clause is not allowed")
	RecordReason("dataapi", "denied", "rate limit exceeded: max 60 queries/min per principal")
	RecordReason("mcp", "error", "context deadline exceeded")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	for _, want := range []string{
		`aegis_audit_reasons_total{channel="dataapi",reason="no_where_write",`,
		`aegis_audit_reasons_total{channel="dataapi",reason="rate_limit",`,
		`aegis_audit_reasons_total{channel="mcp",reason="query_timeout",`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing expected series %q in /metrics:\n%s", want, body)
		}
	}
}
