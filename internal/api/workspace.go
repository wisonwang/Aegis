package api

import (
	"net/http"

	"github.com/wisonwang/aegis/internal/store"
)

// WorkspaceResolver enforces workspace isolation. It MUST run after
// Authenticate (so claims are on the context) and before the handler, and is
// the single place where the active workspace is bound to the request context.
//
// Resolution rules (ADR-001):
//   - The workspace hint comes from the X-Workspace-Id header, falling back to
//     the workspace_id query parameter.
//   - Platform admin: an explicit "*" (or no hint) means the cross-workspace
//     view; an explicit workspace id scopes to that workspace (admin may reach
//     any). This makes admin listing/reads see everything.
//   - Regular user: "*" is forbidden; an empty hint falls back to the user's
//     default workspace; a specific value is allowed ONLY when the user is a
//     member of it. Anything else is a fail-closed 403 — there is no path to
//     accidentally read another tenant's data.
func WorkspaceResolver(st *store.Store, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := ClaimsFromContext(r.Context())

		wsID := r.Header.Get("X-Workspace-Id")
		if wsID == "" {
			wsID = r.URL.Query().Get("workspace_id")
		}

		effective := store.DefaultWorkspaceID
		if claims != nil {
			resolved, err := st.ResolveWorkspaceID(claims.UserID, claims.IsAdmin(), wsID)
			if err != nil {
				writeError(w, http.StatusForbidden, err.Error())
				return
			}
			effective = resolved
		} else if wsID != "" {
			effective = wsID
		}

		ctx := store.WithWorkspace(r.Context(), effective)
		next(w, r.WithContext(ctx))
	}
}
