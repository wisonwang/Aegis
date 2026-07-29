package proxy

import (
	"strings"
	"testing"
)

func TestInjectLimit(t *testing.T) {
	tests := []struct {
		name    string
		sql     string
		maxRows int
		want    string // substring to check (LIMIT clause)
	}{
		{
			name:    "no limit -> inject",
			sql:     "SELECT * FROM orders",
			maxRows: 10000,
			want:    "limit 10000",
		},
		{
			name:    "existing limit below max -> keep",
			sql:     "SELECT * FROM orders LIMIT 100",
			maxRows: 10000,
			want:    "limit 100",
		},
		{
			name:    "existing limit exceeds max -> replace",
			sql:     "SELECT * FROM orders LIMIT 1000000",
			maxRows: 10000,
			want:    "limit 10000",
		},
		{
			name:    "existing limit equals max -> keep",
			sql:     "SELECT * FROM orders LIMIT 10000",
			maxRows: 10000,
			want:    "limit 10000",
		},
		{
			name:    "with WHERE clause -> inject",
			sql:     "SELECT id, name FROM customers WHERE status = 'active'",
			maxRows: 5000,
			want:    "limit 5000",
		},
		{
			name:    "with WHERE + existing lower limit -> keep",
			sql:     "SELECT id FROM customers WHERE status = 'active' LIMIT 50",
			maxRows: 5000,
			want:    "limit 50",
		},
		{
			name:    "non-SELECT (UPDATE) -> unchanged",
			sql:     "UPDATE orders SET status = 'done' WHERE id = 1",
			maxRows: 10000,
			want:    "UPDATE orders SET status = 'done' WHERE id = 1",
		},
		{
			name:    "malformed SQL -> unchanged",
			sql:     "NOT SQL AT ALL",
			maxRows: 10000,
			want:    "NOT SQL AT ALL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := injectLimit(tt.sql, tt.maxRows)
			if !strings.Contains(strings.ToLower(result), strings.ToLower(tt.want)) {
				t.Errorf("injectLimit(%q, %d) = %q, want containing %q", tt.sql, tt.maxRows, result, tt.want)
			}
		})
	}
}
