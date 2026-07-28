package api

import (
	"context"
	"net/http"
)

// pathParamKeyT is the context key under which GIN route parameters are
// mirrored. GIN does not populate the stdlib http.Request.PathValue (it does
// not use the net/http ServeMux), so after the router migrated to gin,
// handlers that previously called r.PathValue("id") must read the value from
// the request context instead.
type pathParamKeyT string

func pathParamKey(name string) pathParamKeyT { return pathParamKeyT("aegis.path." + name) }

// WithPathParam records a route path parameter into the context so that the
// net/http handlers keep working under the gin router. It is called by the
// ginParamMiddleware installed in the server package.
func WithPathParam(ctx context.Context, name, value string) context.Context {
	return context.WithValue(ctx, pathParamKey(name), value)
}

// pathParam returns a route path parameter. It first tries the stdlib
// r.PathValue (populated when served by the net/http ServeMux) and falls back
// to the value mirrored into the context by the gin router middleware. This
// keeps the handlers router-agnostic, so the governance logic is identical
// regardless of which HTTP engine serves the request.
func pathParam(r *http.Request, name string) string {
	if v := r.PathValue(name); v != "" {
		return v
	}
	if v, ok := r.Context().Value(pathParamKey(name)).(string); ok {
		return v
	}
	return ""
}
