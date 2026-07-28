package api

import (
	"database/sql"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/wisonwang/aegis/internal/auth"
	"github.com/wisonwang/aegis/internal/config"
	"github.com/wisonwang/aegis/internal/store"
)

// provisionOrLinkExternalUser creates a platform user from an external identity
// (OIDC subject, LDAP DN, SAML nameID, ...) on first login, and links subsequent
// logins via the unique externalID. Platform roles and workspace memberships are
// derived from the caller-resolved mappings and applied on EVERY login so that
// changes in the identity provider's group membership propagate to the platform
// (idempotent: duplicate inserts are ignored). Missing platform roles are
// auto-created so a freshly mapped group just works without manual setup.
//
// Workspace bindings are resolved by slug; a binding whose workspace does not
// exist yet is skipped rather than auto-created (a fail-safe default for a
// governance product — we never spawn tenants as a side effect of a login).
func provisionOrLinkExternalUser(st *store.Store, externalID, username, displayName string, attrs map[string]string, roleNames []string, wsBindings []config.WorkspaceBinding) (*store.User, error) {
	u, err := st.GetUserByExternalID(externalID)
	if err != nil {
		return nil, err
	}
	if u == nil {
		u = &store.User{
			ID:          uuid.NewString(),
			Username:    username,
			DisplayName: displayName,
			ExternalID:  sql.NullString{String: externalID, Valid: true},
			Status:      "active",
			Attributes:  mustJSON(attrs),
			CreatedAt:   time.Now(),
		}
		if err := st.CreateUser(u); err != nil {
			return nil, err
		}
	}
	// Existing external user: keep the display name in sync.
	if displayName != "" && u.DisplayName != displayName {
		u.DisplayName = displayName
		_ = st.UpdateUser(u)
	}

	// Platform roles (idempotent).
	for _, roleName := range roleNames {
		role, err := st.GetRole(roleName)
		if err != nil {
			continue
		}
		if role == nil {
			role = &store.Role{ID: uuid.NewString(), Name: roleName}
			if err := st.CreateRole(role); err != nil {
				continue
			}
		}
		_ = st.AddUserRole(u.ID, role.ID)
	}

	// Workspace membership (idempotent). Every principal always gets the
	// default workspace so the fail-closed resolver has something to fall back
	// to; group-derived bindings add the tenant(s) the identity belongs to.
	if err := st.EnsureDefaultMembership(u.ID); err != nil {
		return nil, err
	}
	for _, wb := range wsBindings {
		ws, err := st.GetWorkspaceBySlug(wb.Slug)
		if err != nil || ws == nil {
			continue
		}
		role := wb.Role
		if role == "" {
			role = store.WsRoleMember
		}
		_ = st.AddWorkspaceMember(ws.ID, u.ID, role, false)
	}
	return u, nil
}

// issueTokenForUser resolves the user's roles/attributes and signs a JWT,
// writing the login response. It is the shared terminal step for every
// external-identity login flow.
func issueTokenForUser(w http.ResponseWriter, st *store.Store, cfg *config.Config, u *store.User) {
	roles, err := st.ListRolesForUser(u.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	roleNames := make([]string, 0, len(roles))
	for _, r := range roles {
		roleNames = append(roleNames, r.Name)
	}
	userAttrs, _ := st.UserAttributes(u.ID)
	claims := &auth.Claims{
		UserID:      u.ID,
		Username:    u.Username,
		DisplayName: u.DisplayName,
		Roles:       roleNames,
		Attributes:  userAttrs,
	}
	tok, err := auth.GenerateToken(claims, cfg.JWTSecret, cfg.JWTExpiry)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "token generation failed")
		return
	}
	writeJSON(w, http.StatusOK, loginResponse{
		Token: tok,
		User:  toMe(u, roleNames, userAttrs),
	})
}
