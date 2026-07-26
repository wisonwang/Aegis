package logging

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// statusRecorder captures the HTTP status code and byte count written by the
// downstream handler, since http.ResponseWriter does not expose them.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// skipPaths are high-frequency, low-signal endpoints we do not emit at the
// access-log level (they are still served normally). Probes and the metrics
// scrape would otherwise drown out the real governance traffic.
var skipPaths = map[string]bool{
	"/api/v1/health": true,
	"/api/v1/ready":  true,
	"/metrics":       true,
}

// Middleware records one structured access-log line per HTTP request, tagging
// each request with a correlation id (from the X-Request-Id header or a fresh
// UUID) that downstream handlers can pick up via WithCtx. Health / readiness /
// metrics probes are skipped to keep the stream clean. The log level follows
// the HTTP status: 5xx -> error, 4xx -> warn, everything else -> info.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if skipPaths[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		reqID := r.Header.Get("X-Request-Id")
		if reqID == "" {
			reqID = uuid.NewString()
		}
		ctx := NewCtx(r.Context(), "req_id", reqID)
		r = r.WithContext(ctx)

		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)

		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		level := slog.LevelInfo
		msg := "http request"
		switch {
		case status >= 500:
			level = slog.LevelError
			msg = "http request error"
		case status >= 400:
			level = slog.LevelWarn
			msg = "http request rejected"
		}
		WithCtx(ctx).Log(ctx, level, msg,
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"bytes", rec.bytes,
			"duration_ms", time.Since(start).Milliseconds(),
			"remote", r.RemoteAddr,
			"req_id", reqID,
		)
	})
}

// ParseLevel exposes the level parser for callers that want to re-use the same
// "debug"/"info"/"warn"/"error" vocabulary elsewhere.
func ParseLevel(s string) slog.Level { return parseLevel(s) }
