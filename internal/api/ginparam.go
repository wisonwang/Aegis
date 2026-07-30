package api

import (
	"context"
	"net/http"

	"github.com/wisonwang/aegis/internal/requestctx"
)

// WithPathParam records a route path parameter into the context so that the
// net/http handlers keep working under the gin router. It is called by the
// ginParamMiddleware installed in the server package.
func WithPathParam(ctx context.Context, name, value string) context.Context {
	return requestctx.WithPathParam(ctx, name, value)
}

// pathParam returns a route path parameter. It first tries the stdlib
// r.PathValue (populated when served by the net/http ServeMux) and falls back
// to the value mirrored into the context by the gin router middleware. This
// keeps the handlers router-agnostic, so the governance logic is identical
// regardless of which HTTP engine serves the request.
func pathParam(r *http.Request, name string) string {
	return requestctx.PathParam(r, name)
}
