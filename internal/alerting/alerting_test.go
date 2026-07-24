package alerting

import (
	"testing"
	"time"

	"github.com/wisonwang/aegis/internal/store"
)

// captureSink records every persisted alert in order.
func captureSink(out *[]store.SecurityAlert) Sink {
	return func(a store.SecurityAlert) error {
		*out = append(*out, a)
		return nil
	}
}

func TestRepeatedDenied(t *testing.T) {
	var got []store.SecurityAlert
	d := New(Config{DeniedCount: 3, DeniedWindow: time.Minute, Cooldown: 10 * time.Minute}, captureSink(&got))
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	// 3 denied within the window -> 1 alert.
	for i := 0; i < 3; i++ {
		d.Observe("analyst", false, "dataapi", "denied", 0, base.Add(time.Duration(i)*time.Second))
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(got))
	}
	if got[0].Rule != RuleRepeatedDenied || got[0].Level != "warning" {
		t.Fatalf("unexpected alert: %+v", got[0])
	}
	if got[0].Principal != "analyst" {
		t.Fatalf("principal mismatch: %q", got[0].Principal)
	}
}

func TestRepeatedDeniedCooldown(t *testing.T) {
	var got []store.SecurityAlert
	d := New(Config{DeniedCount: 3, DeniedWindow: time.Minute, Cooldown: 10 * time.Minute}, captureSink(&got))
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		d.Observe("analyst", false, "dataapi", "denied", 0, base.Add(time.Duration(i)*time.Second))
	}
	// 3 more denied right after, within cooldown -> no new alert yet.
	for i := 3; i < 6; i++ {
		d.Observe("analyst", false, "dataapi", "denied", 0, base.Add(time.Duration(i)*time.Second))
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 alert (cooldown), got %d", len(got))
	}
	// After the cooldown elapses, the same pattern raises again.
	for i := 0; i < 3; i++ {
		d.Observe("analyst", false, "dataapi", "denied", 0, base.Add(11*time.Minute+time.Duration(i)*time.Second))
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 alerts after cooldown, got %d", len(got))
	}
}

func TestBulkExport(t *testing.T) {
	var got []store.SecurityAlert
	d := New(Config{BulkRows: 100, Cooldown: time.Minute}, captureSink(&got))
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	d.Observe("analyst", false, "mcp", "ok", 5000, at)  // large result -> critical
	d.Observe("analyst", false, "mcp", "ok", 50, at)    // small result -> no alert
	if len(got) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(got))
	}
	if got[0].Rule != RuleBulkExport || got[0].Level != "critical" {
		t.Fatalf("unexpected alert: %+v", got[0])
	}
}

func TestOffHoursExcludesAdmin(t *testing.T) {
	var got []store.SecurityAlert
	d := New(Config{OffHoursOn: true, OffHoursStart: 0, OffHoursEnd: 6, Cooldown: time.Minute}, captureSink(&got))
	at := time.Date(2026, 1, 1, 3, 0, 0, 0, time.UTC) // 03:00 local
	d.Observe("analyst", false, "dataapi", "ok", 0, at) // non-admin off-hours -> alert
	d.Observe("admin", true, "dataapi", "ok", 0, at)    // admin excluded
	if len(got) != 1 {
		t.Fatalf("expected 1 alert (admin excluded), got %d", len(got))
	}
	if got[0].Rule != RuleOffHours {
		t.Fatalf("unexpected rule: %q", got[0].Rule)
	}
}

func TestOffHoursDisabled(t *testing.T) {
	var got []store.SecurityAlert
	d := New(Config{OffHoursOn: false, OffHoursStart: 0, OffHoursEnd: 6, Cooldown: time.Minute}, captureSink(&got))
	at := time.Date(2026, 1, 1, 3, 0, 0, 0, time.UTC)
	d.Observe("analyst", false, "dataapi", "ok", 0, at)
	if len(got) != 0 {
		t.Fatalf("expected 0 alerts when off-hours disabled, got %d", len(got))
	}
}

func TestNilSinkNoPanic(t *testing.T) {
	d := New(Config{}, nil)
	d.Observe("analyst", false, "dataapi", "denied", 0, time.Now())
}
