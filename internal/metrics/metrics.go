// Package metrics provides a self-contained Prometheus registry and the
// instrumentation surface for Aegis. Every governed query (DataAPI, MCP, and
// dataset queries) funnels through the proxy audit chokepoints, which call
// RecordQuery / RecordRows; start-up wiring reports build identity and the
// live datasource / dataset counts. The registry is exposed via Handler, which
// the server mounts on GET /metrics.
//
// In-process atomic mirrors of the gauges/counters are kept so that
// GET /admin/api/stats can return a current snapshot without scraping the
// Prometheus exposition format.
package metrics

import (
	"net/http"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// reg is a dedicated registry so the default Go/process collectors are added
// explicitly (the global default registry is intentionally not used, keeping
// the exposition limited to Aegis-relevant metrics).
var reg = prometheus.NewRegistry()

var (
	queriesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "aegis_queries_total",
		Help: "Total governed queries by access channel and outcome (ok/denied/error).",
	}, []string{"channel", "status"})

	queryDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "aegis_query_duration_seconds",
		Help:    "Latency of governed queries by access channel and outcome.",
		Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
	}, []string{"channel", "status"})

	rowsReturned = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "aegis_rows_returned_total",
		Help: "Total rows returned to callers by access channel and outcome.",
	}, []string{"channel", "status"})

	datasourcesTotal = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "aegis_datasources_total",
		Help: "Number of configured datasources.",
	})

	datasetsPublishedTotal = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "aegis_datasets_published_total",
		Help: "Number of published datasets.",
	})

	mcpSessionsTotal = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "aegis_mcp_sessions_total",
		Help: "Current number of active MCP sessions.",
	})

	buildInfo = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "aegis_build_info",
		Help: "Build information for this Aegis instance (value is always 1).",
	}, []string{"version", "commit"})
)

// In-process mirrors so /admin/api/stats can read current values cheaply.
var (
	queriesServed   int64
	queriesDenied   int64
	datasourcesCnt int64
	datasetsCnt     int64
	mcpSessionsCnt  int64
	startTime       = time.Now()
	buildVersion    string
	buildCommit     string
)

func init() {
	reg.MustRegister(
		queriesTotal,
		queryDuration,
		rowsReturned,
		datasourcesTotal,
		datasetsPublishedTotal,
		mcpSessionsTotal,
		buildInfo,
	)
	// Runtime visibility (goroutines, GC, process CPU/mem, start time).
	reg.MustRegister(prometheus.NewGoCollector())
	reg.MustRegister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
}

// RecordQuery records one governed query outcome with its latency.
func RecordQuery(channel, status string, dur time.Duration) {
	queriesTotal.WithLabelValues(channel, status).Inc()
	queryDuration.WithLabelValues(channel, status).Observe(dur.Seconds())
	switch status {
	case "ok":
		atomic.AddInt64(&queriesServed, 1)
	case "denied":
		atomic.AddInt64(&queriesDenied, 1)
	}
}

// RecordRows adds to the running total of rows returned to callers.
func RecordRows(channel, status string, n int) {
	rowsReturned.WithLabelValues(channel, status).Add(float64(n))
}

// SetDatasources reports the current configured-datasource count.
func SetDatasources(n int) {
	datasourcesTotal.Set(float64(n))
	atomic.StoreInt64(&datasourcesCnt, int64(n))
}

// SetDatasetsPublished reports the current published-dataset count.
func SetDatasetsPublished(n int) {
	datasetsPublishedTotal.Set(float64(n))
	atomic.StoreInt64(&datasetsCnt, int64(n))
}

// SetMCPSessions reports the current active MCP session count.
func SetMCPSessions(n int) {
	mcpSessionsTotal.Set(float64(n))
	atomic.StoreInt64(&mcpSessionsCnt, int64(n))
}

// SetBuildInfo advertises the running version and commit.
func SetBuildInfo(version, commit string) {
	buildVersion, buildCommit = version, commit
	buildInfo.Reset()
	buildInfo.WithLabelValues(version, commit).Set(1)
}

// Handler returns an http.Handler that renders the Prometheus exposition
// format for the registry. Mount it on /metrics.
func Handler() http.Handler {
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
}

// ---- Snapshot getters for GET /admin/api/stats (ADR-0005, Phase 1) ----

// UptimeSeconds returns how long this process has been running.
func UptimeSeconds() int64 { return int64(time.Since(startTime).Seconds()) }

// BuildVersion returns the running version and commit stamped at build time.
func BuildVersion() (string, string) { return buildVersion, buildCommit }

// QueriesServed returns the count of governed queries that passed governance.
func QueriesServed() int64 { return atomic.LoadInt64(&queriesServed) }

// QueriesDenied returns the count of governed queries blocked by governance.
func QueriesDenied() int64 { return atomic.LoadInt64(&queriesDenied) }

// Datasources returns the configured-datasource count.
func Datasources() int { return int(atomic.LoadInt64(&datasourcesCnt)) }

// DatasetsPublished returns the published-dataset count.
func DatasetsPublished() int { return int(atomic.LoadInt64(&datasetsCnt)) }

// MCPSessions returns the current active MCP session count.
func MCPSessions() int { return int(atomic.LoadInt64(&mcpSessionsCnt)) }
