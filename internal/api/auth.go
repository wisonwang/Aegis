package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/wisonwang/aegis/internal/auth"
	"github.com/wisonwang/aegis/internal/config"
	"github.com/wisonwang/aegis/internal/datasource"
	"github.com/wisonwang/aegis/internal/proxy"
	"github.com/wisonwang/aegis/internal/store"
)

type ctxKeyClaims struct{}

func claimsFromContext(ctx context.Context) *auth.Claims {
	c, _ := ctx.Value(ctxKeyClaims{}).(*auth.Claims)
	return c
}

// Authenticate validates the Bearer JWT and stores the claims in context.
func Authenticate(cfg *config.Config, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		if !strings.HasPrefix(h, "Bearer ") {
			writeError(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		claims, err := auth.ParseToken(strings.TrimPrefix(h, "Bearer "), cfg.JWTSecret)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid token")
			return
		}
		ctx := context.WithValue(r.Context(), ctxKeyClaims{}, claims)
		next(w, r.WithContext(ctx))
	}
}

// RequireAdmin restricts a route to the built-in admin role.
func RequireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := claimsFromContext(r.Context())
		if c == nil || !c.IsAdmin() {
			writeError(w, http.StatusForbidden, "admin role required")
			return
		}
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteJSON is the exported JSON writer used by route handlers.
func WriteJSON(w http.ResponseWriter, status int, v interface{}) { writeJSON(w, status, v) }

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]interface{}{"error": msg})
}

// ClaimsFromContext is the exported form of claimsFromContext, for use by the
// enterprise route package (which cannot reach the unexported helper).
func ClaimsFromContext(ctx context.Context) *auth.Claims { return claimsFromContext(ctx) }

// WriteError is the exported form of writeError, for use by the enterprise route package.
func WriteError(w http.ResponseWriter, status int, msg string) { writeError(w, status, msg) }

// Handler bundles dependencies shared by all HTTP handlers.
type Handler struct {
	Store *store.Store
	Proxy *proxy.Proxy
	DS    *datasource.Manager
	Cfg   *config.Config
}

// hashPassword wraps the auth helper for admin user creation.
func hashPassword(p string) (string, error) { return auth.HashPassword(p) }
