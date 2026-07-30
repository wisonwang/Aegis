package requestctx

import (
	"context"
	"net/http"

	"github.com/wisonwang/aegis/internal/auth"
)

type claimsKey struct{}
type pathParamKey string

func pathKey(name string) pathParamKey { return pathParamKey("aegis.path." + name) }

// WithClaims stores parsed auth claims on the request context.
func WithClaims(ctx context.Context, claims *auth.Claims) context.Context {
	return context.WithValue(ctx, claimsKey{}, claims)
}

// Claims returns auth claims from the request context.
func Claims(ctx context.Context) *auth.Claims {
	c, _ := ctx.Value(claimsKey{}).(*auth.Claims)
	return c
}

// WithPathParam stores a route path parameter on the request context so
// handlers remain agnostic to the HTTP router implementation.
func WithPathParam(ctx context.Context, name, value string) context.Context {
	return context.WithValue(ctx, pathKey(name), value)
}

// PathParam returns a route path parameter from either the stdlib path value
// or the context mirror used by the gin adapter.
func PathParam(r *http.Request, name string) string {
	if v := r.PathValue(name); v != "" {
		return v
	}
	if v, ok := r.Context().Value(pathKey(name)).(string); ok {
		return v
	}
	return ""
}
