// Package logging provides structured, leveled logging for Aegis built on
// the standard library log/slog package. It keeps the project dependency-free
// (no zap / zerolog) so the single-binary promise holds, while giving
// operators JSON-or-text logs that ship cleanly to Loki / ELK / cloud
// log aggregation.
//
// The default logger is installed once at start-up via Init. Per-request
// context loggers (carrying a correlation id, principal, channel, ...) are
// produced with WithCtx / NewCtx so downstream handlers can extend the same
// record without threading a logger through every signature.
package logging

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

// Config selects the slog output format and minimum level. It is loaded from
// the top-level "logging" config block and overridable via AEGIS_LOG_FORMAT /
// AEGIS_LOG_LEVEL environment variables.
type Config struct {
	Format string `json:"format"` // "json" (default) or "text"
	Level  string `json:"level"`  // "debug" | "info" (default) | "warn" | "error"
}

// ctxKey carries a request-scoped logger through context.
type ctxKey struct{}

// Init installs a process-wide default slog.Logger. Call it once at start-up
// before any logging happens; later calls replace the default (mainly useful
// for tests that want a buffer-backed handler).
func Init(cfg Config) {
	level := parseLevel(cfg.Level)
	opts := &slog.HandlerOptions{Level: level}
	var h slog.Handler
	if strings.EqualFold(cfg.Format, "text") {
		h = slog.NewTextHandler(os.Stderr, opts)
	} else {
		h = slog.NewJSONHandler(os.Stderr, opts)
	}
	slog.SetDefault(slog.New(h))
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// With returns the default logger pre-loaded with the given structured
// attributes (key/value pairs, as accepted by slog). Use for one-off records
// that should always carry a fixed set of fields.
func With(args ...any) *slog.Logger {
	return slog.Default().With(args...)
}

// WithCtx returns a logger bound to ctx. If the request middleware stored a
// logger in ctx (e.g. carrying the request id), that one is used and the
// supplied attributes are merged on top; otherwise the default logger is used.
func WithCtx(ctx context.Context, args ...any) *slog.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok && l != nil {
		return l.With(args...)
	}
	return slog.Default().With(args...)
}

// NewCtx returns a context carrying a logger pre-loaded with args. Downstream
// callers retrieve it via WithCtx and keep extending the same record.
func NewCtx(ctx context.Context, args ...any) context.Context {
	l := slog.Default().With(args...)
	return context.WithValue(ctx, ctxKey{}, l)
}
