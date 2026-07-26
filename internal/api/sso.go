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
// logins via the unique externalID. Roles are derived from the caller-resolved
// role names (already mapped from the identity provider); missing roles are
// auto-created so a freshly mapped group just works without manual setup.
func provisionOrLinkExternalUser(st *store.Store, externalID, username, displayName string, attrs map[string]string, roleNames []string) (*store.User, error) {
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
		return u, nil
	}
	// Existing external user: keep the display name in sync.
	if displayName != "" && u.DisplayName != displayName {
		u.DisplayName = displayName
		_ = st.UpdateUser(u)
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
