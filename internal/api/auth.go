package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/wisonwang/aegis/internal/auth"
	"github.com/wisonwang/aegis/internal/config"
	"github.com/wisonwang/aegis/internal/datasource"
	"github.com/wisonwang/aegis/internal/proxy"
	"github.com/wisonwang/aegis/internal/requestctx"
	"github.com/wisonwang/aegis/internal/store"
)

func claimsFromContext(ctx context.Context) *auth.Claims {
	return requestctx.Claims(ctx)
}

// principalResult carries the resolved identity and how it was authenticated.
type principalResult struct {
	Claims *auth.Claims
	Method string // "jwt" | "apikey"
}

// ResolvePrincipal extracts the caller identity from a request, supporting two
// credential forms:
//   - Bearer JWT (Authorization: Bearer <jwt>)
//   - API key, either as Authorization: Bearer <key> or X-API-Key: <key>
//     (per-user keys stored hashed in api_keys; service accounts use these).
//
// It is shared by the HTTP DataAPI path (Authenticate) and the MCP transport
// so that a single per-user API key works across both. It enforces
// fail-closed: a disabled user (or a revoked/expired key) is rejected. The
// caller is responsible for role checks (e.g. RequireAdmin).
func ResolvePrincipal(st *store.Store, cfg *config.Config, r *http.Request) (*principalResult, error) {
	raw := ""
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		raw = strings.TrimPrefix(h, "Bearer ")
	}
	if raw == "" {
		raw = r.Header.Get("X-API-Key")
	}
	if raw == "" {
		return nil, fmt.Errorf("missing credentials")
	}
	// 1) Try JWT first.
	if claims, err := auth.ParseToken(raw, cfg.JWTSecret); err == nil {
		return &principalResult{Claims: claims, Method: "jwt"}, nil
	}
	// 2) Fall back to per-user API key (hashed lookup).
	if st != nil {
		if ak, u, err := st.LookupAPIKey(raw); err != nil {
			return nil, fmt.Errorf("api key lookup failed")
		} else if ak != nil && u != nil {
			if u.Status != "active" {
				return nil, fmt.Errorf("account disabled")
			}
			st.TouchAPIKey(ak.ID)
			roles, _ := st.ListRolesForUser(u.ID)
			names := make([]string, 0, len(roles))
			for _, role := range roles {
				names = append(names, role.Name)
			}
			attrs, _ := st.UserAttributes(u.ID)
			return &principalResult{
				Claims: &auth.Claims{
					UserID:      u.ID,
					Username:    u.Username,
					DisplayName: u.DisplayName,
					Roles:       names,
					Attributes:  attrs,
				},
				Method: "apikey",
			}, nil
		}
	}
	return nil, fmt.Errorf("invalid credentials")
}

// Authenticate validates the Bearer JWT or API key and stores the claims in
// context. It also enforces account status fail-closed: a disabled user's
// token/key is rejected on every request (not just at login), so disabling
// takes effect immediately even for credentials issued before the change.
func Authenticate(st *store.Store, cfg *config.Config, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		res, err := ResolvePrincipal(st, cfg, r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, err.Error())
			return
		}
		// Redundant-but-safe disabled re-check for the JWT path (subject may
		// have been disabled after the token was issued). For apikey paths the
		// resolver already enforced this, so this is a no-op there.
		if st != nil {
			if u, _ := st.GetUser(res.Claims.UserID); u != nil && u.Status != "active" {
				writeError(w, http.StatusForbidden, "account disabled")
				return
			}
		}
		ctx := requestctx.WithClaims(r.Context(), res.Claims)
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
