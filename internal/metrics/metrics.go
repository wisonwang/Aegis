// Package metrics provides a self-contained Prometheus registry and the
// instrumentation surface for Aegis. Every governed query (DataAPI, MCP, and
// dataset queries) funnels through the proxy audit chokepoints, which call
// RecordQuery / RecordRows; start-up wiring reports build identity and the
// live datasource / dataset counts. The registry is exposed via Handler, which
// the server mounts on GET /metrics.
package metrics

import (
	"net/http"
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

	buildInfo = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "aegis_build_info",
		Help: "Build information for this Aegis instance (value is always 1).",
	}, []string{"version", "commit"})
)

func init() {
	reg.MustRegister(
		queriesTotal,
		queryDuration,
		rowsReturned,
		datasourcesTotal,
		datasetsPublishedTotal,
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
}

// RecordRows adds to the running total of rows returned to callers.
func RecordRows(channel, status string, n int) {
	rowsReturned.WithLabelValues(channel, status).Add(float64(n))
}

// SetDatasources reports the current configured-datasource count.
func SetDatasources(n int) { datasourcesTotal.Set(float64(n)) }

// SetDatasetsPublished reports the current published-dataset count.
func SetDatasetsPublished(n int) { datasetsPublishedTotal.Set(float64(n)) }

// SetBuildInfo advertises the running version and commit.
func SetBuildInfo(version, commit string) {
	buildInfo.Reset()
	buildInfo.WithLabelValues(version, commit).Set(1)
}

// Handler returns an http.Handler that renders the Prometheus exposition
// format for the registry. Mount it on /metrics.
func Handler() http.Handler {
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
}
