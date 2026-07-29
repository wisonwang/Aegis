package proxy

import (
	"sync"
	"time"

	"github.com/wisonwang/aegis/internal/config"
)

// Guard enforces AI-behavior governance on top of permission governance:
//   - MaxRows:  hard cap on rows returned per query (AI agents cannot dump tables)
//   - Timeout:  per-query execution deadline (runaway SQL is cancelled)
//   - RatePerMin: sliding-window per-principal rate limit
//   - MaxAffectedRows: hard cap on rows a single UPDATE/DELETE may touch
//   - AllowNoWhere: when false (default), UPDATE/DELETE without a WHERE is rejected
//   - MaxBytes: hard cap on serialized response body size (prevents wide-row bypass)
//
// A zero-value Guard (nil limiter) disables all behavior limits.
type Guard struct {
	MaxRows         int
	Timeout         time.Duration
	RatePerMin      int
	AdminExempt     bool
	MaxAffectedRows int
	AllowNoWhere    bool
	MaxBytes        int // response body size cap in bytes; 0 = no cap

	mu      sync.Mutex
	windows map[string][]time.Time // principal ID -> timestamps within last minute
}

// NewGuard builds a Guard from config, normalizing zero values to safe defaults.
func NewGuard(l config.Limits) *Guard {
	g := &Guard{
		MaxRows:         l.MaxRows,
		RatePerMin:      l.RatePerMin,
		AdminExempt:     l.AdminExempt,
		MaxAffectedRows: l.MaxAffectedRows,
		AllowNoWhere:    l.AllowNoWhere,
		MaxBytes:        l.MaxBytes,
		windows:         map[string][]time.Time{},
	}
	if g.MaxRows <= 0 {
		g.MaxRows = 10000
	}
	if g.MaxAffectedRows <= 0 {
		g.MaxAffectedRows = 10000
	}
	if g.MaxBytes <= 0 {
		g.MaxBytes = 4194304 // 4MB
	}
	if g.RatePerMin <= 0 {
		g.RatePerMin = 60
	}
	g.Timeout = 30 * time.Second
	if l.QueryTimeout != "" {
		if d, err := time.ParseDuration(l.QueryTimeout); err == nil && d > 0 {
			g.Timeout = d
		}
	}
	return g
}

// Allow records one query attempt for the principal and reports whether it is
// within the per-minute budget. Sliding one-minute window, O(requests/min).
func (g *Guard) Allow(principalID string) bool {
	if g == nil {
		return true
	}
	now := time.Now()
	cutoff := now.Add(-time.Minute)

	g.mu.Lock()
	defer g.mu.Unlock()

	w := g.windows[principalID]
	// drop entries older than the window
	i := 0
	for ; i < len(w); i++ {
		if w[i].After(cutoff) {
			break
		}
	}
	w = w[i:]
	if len(w) >= g.RatePerMin {
		g.windows[principalID] = w
		return false
	}
	g.windows[principalID] = append(w, now)
	return true
}
