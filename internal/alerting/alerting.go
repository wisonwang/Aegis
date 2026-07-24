// Package alerting implements a lightweight anomaly-detection engine that
// watches the governed-query audit stream and raises security alerts for risky
// agent/user behavior. It is intentionally simple and self-contained: in-memory
// sliding windows, a few hard rules, cooldown-based de-duplication, optional
// persistence via a Sink, and an optional webhook for alert delivery.
package alerting

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/wisonwang/aegis/internal/store"
)

// Detection rule identifiers.
const (
	RuleRepeatedDenied = "repeated_denied" // many denied queries in a short window (probing / misconfig)
	RuleBulkExport     = "bulk_export"     // a single query returned an unusually large number of rows
	RuleOffHours       = "off_hours"       // access outside the configured business hours
)

// Config controls the detector thresholds. Zero values fall back to safe
// defaults inside New.
type Config struct {
	DeniedCount   int           // denied queries within DeniedWindow to trip repeated_denied
	DeniedWindow  time.Duration // sliding window for repeated_denied
	BulkRows      int           // a single ok query returning >= BulkRows trips bulk_export
	OffHoursOn    bool          // enable the off-hours rule
	OffHoursStart int           // inclusive local hour (e.g. 0)
	OffHoursEnd   int           // exclusive local hour (e.g. 6)
	Cooldown      time.Duration // min gap between repeated alerts of the same (principal, rule)
	Webhook       string        // optional URL to POST raised alerts to
}

// Sink persists a raised alert. The server provides a closure that writes to
// the control-plane store; tests may use an in-memory capturing sink.
type Sink func(store.SecurityAlert) error

// Detector observes audit events and raises alerts.
type Detector struct {
	cfg  Config
	sink Sink

	mu     sync.Mutex
	denied map[string][]time.Time // principal -> recent denied timestamps
	last   map[string]time.Time   // principal|rule -> last raised time (cooldown)
}

// New builds a Detector with normalized defaults.
func New(cfg Config, sink Sink) *Detector {
	if cfg.DeniedCount <= 0 {
		cfg.DeniedCount = 10
	}
	if cfg.DeniedWindow <= 0 {
		cfg.DeniedWindow = time.Minute
	}
	if cfg.BulkRows <= 0 {
		cfg.BulkRows = 5000
	}
	if cfg.Cooldown <= 0 {
		cfg.Cooldown = 5 * time.Minute
	}
	return &Detector{
		cfg:    cfg,
		sink:   sink,
		denied: map[string][]time.Time{},
		last:   map[string]time.Time{},
	}
}

// Observe feeds one governed outcome into the detector. principal is the
// username, isAdmin excludes the principal from the off-hours rule (ops access
// is expected), channel is dataapi/mcp, status is ok/denied/error, rows is the
// number of rows returned, and at is the observation time.
func (d *Detector) Observe(principal string, isAdmin bool, channel, status string, rows int, at time.Time) {
	if d == nil || d.sink == nil || principal == "" {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	// Rule 1: repeated denied queries within a sliding window.
	if status == "denied" {
		w := append(d.denied[principal], at)
		cutoff := at.Add(-d.cfg.DeniedWindow)
		i := 0
		for ; i < len(w); i++ {
			if w[i].After(cutoff) {
				break
			}
		}
		w = w[i:]
		d.denied[principal] = w
		if len(w) >= d.cfg.DeniedCount {
			d.raise(principal, channel, RuleRepeatedDenied, "warning",
				fmt.Sprintf("%d 次查询在 %s 内被拒绝，疑似探测或越权尝试", len(w), d.cfg.DeniedWindow), at)
			// Reset the window so we don't re-fire on every subsequent denial
			// until the cooldown elapses and the pattern recurs.
			d.denied[principal] = nil
		}
	}

	// Rule 2: bulk export — a single query returned an unexpectedly large set.
	if status == "ok" && rows >= d.cfg.BulkRows {
		d.raise(principal, channel, RuleBulkExport, "critical",
			fmt.Sprintf("单次查询返回 %d 行（超过阈值 %d，疑似批量数据导出）", rows, d.cfg.BulkRows), at)
	}

	// Rule 3: off-hours access (admins excluded — ops access is expected).
	if d.cfg.OffHoursOn && !isAdmin {
		h := at.Hour()
		if h >= d.cfg.OffHoursStart && h < d.cfg.OffHoursEnd {
			d.raise(principal, channel, RuleOffHours, "warning",
				fmt.Sprintf("非工作时段 %02d:00 访问（%02d:00-%02d:00 视为离岗）", h, d.cfg.OffHoursStart, d.cfg.OffHoursEnd), at)
		}
	}
}

// raise persists (and optionally delivers) an alert, honoring the cooldown so
// the same principal+rule does not spam alerts. Must be called with d.mu held.
func (d *Detector) raise(principal, channel, rule, level, detail string, at time.Time) {
	key := principal + "|" + rule
	if last, ok := d.last[key]; ok && at.Sub(last) < d.cfg.Cooldown {
		return
	}
	d.last[key] = at
	alert := store.SecurityAlert{
		TS:        at,
		Level:     level,
		Rule:      rule,
		Principal: principal,
		Channel:   channel,
		Detail:    detail,
	}
	if err := d.sink(alert); err != nil {
		return
	}
	if d.cfg.Webhook != "" {
		go d.fireWebhook(alert)
	}
}

// fireWebhook posts the alert JSON to the configured webhook (best-effort).
func (d *Detector) fireWebhook(a store.SecurityAlert) {
	body, err := json.Marshal(a)
	if err != nil {
		return
	}
	req, err := http.NewRequest(http.MethodPost, d.cfg.Webhook, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
}
