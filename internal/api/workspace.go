package api

import (
	"context"
	"errors"
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

// resolveDSBound resolves a datasource id-or-name AND returns a context bound
// to the datasource's own workspace.
//
// This is the pivot of ADR-0007. Governance objects (table permissions, row
// policies, column masks, semantics, classifications, datasets) belong to the
// datasource they govern, not to whichever workspace the admin happens to be
// looking at. Writing them with the raw request context would stamp them with
// "default" whenever an admin is in the cross-workspace view — permanently
// orphaning the governance from its datasource.
//
// The returned context is always concrete (never "*"), so WriteWorkspace can
// never fall back to "default" for an object whose parent lives elsewhere.
func (h *Handler) resolveDSBound(ctx context.Context, idOrName string) (string, context.Context, error) {
	dsID, err := h.resolveDS(ctx, idOrName)
	if err != nil {
		return "", ctx, err
	}
	ds, err := h.Store.GetDataSourceByID(dsID)
	if err != nil {
		return "", ctx, err
	}
	if ds == nil {
		return "", ctx, errStr("datasource not found: " + idOrName)
	}
	return dsID, ds.BoundContext(ctx), nil
}

// writeMutationError maps a store mutation error onto an HTTP status.
// store.ErrNotFound covers both "no such id" and "that id lives in another
// workspace" — deliberately the same 404 so a caller cannot use delete as an
// oracle to enumerate another tenant's object ids.
func writeMutationError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found in the active workspace")
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}

// canMutateWorkspaceObject reports whether the caller, in its current
// workspace context, is allowed to mutate an object stamped with objWS.
// Cross-workspace (admin "all") context may touch anything; otherwise the
// object must live in the active workspace. Empty objWS is legacy data and is
// treated as "default".
func canMutateWorkspaceObject(ctx context.Context, objWS string) bool {
	if store.CrossesWorkspaces(ctx) {
		return true
	}
	if objWS == "" {
		objWS = store.DefaultWorkspaceID
	}
	return objWS == store.WorkspaceID(ctx)
}
