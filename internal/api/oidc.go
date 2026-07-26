package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/wisonwang/aegis/internal/auth"
	"github.com/wisonwang/aegis/internal/config"
	"github.com/wisonwang/aegis/internal/store"
)

// OIDCHandler bundles OIDC-specific dependencies.
type OIDCHandler struct {
	Client *auth.OIDCClient
	Store  *store.Store
	Cfg    *config.Config
}

// NewOIDCHandler creates the handler; returns nil if OIDC is disabled.
func NewOIDCHandler(ctx context.Context, st *store.Store, cfg *config.Config) (*OIDCHandler, error) {
	if !cfg.OIDC.Enabled {
		return nil, nil
	}
	if cfg.OIDC.Issuer == "" || cfg.OIDC.ClientID == "" || cfg.OIDC.RedirectURL == "" {
		return nil, fmt.Errorf("oidc enabled but missing required config (issuer, client_id, redirect_url)")
	}
	client, err := auth.NewOIDCClient(ctx, cfg.OIDC)
	if err != nil {
		return nil, err
	}
	return &OIDCHandler{Client: client, Store: st, Cfg: cfg}, nil
}

// OIDCLogin initiates the authorization-code flow.
func (h *OIDCHandler) OIDCLogin(w http.ResponseWriter, r *http.Request) {
	sess, err := auth.NewSessionState()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create session")
		return
	}
	encoded, err := sess.Encode()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode session")
		return
	}
	auth.SetOIDCCookie(w, auth.SessionCookieName, encoded, 600)
	url, _, err := h.Client.AuthURL(sess.State, sess.Nonce)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build auth URL")
		return
	}
	auth.Redirect(w, r, url)
}

// OIDCCallback handles the IdP redirect back to the platform.
func (h *OIDCHandler) OIDCCallback(w http.ResponseWriter, r *http.Request) {
	// Validate session cookie
	ck, err := r.Cookie(auth.SessionCookieName)
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing session cookie")
		return
	}
	sess, err := auth.DecodeSessionState(ck.Value)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid session cookie")
		return
	}
	auth.ClearOIDCCookie(w, auth.SessionCookieName)
	if time.Now().Unix() > sess.ExpiresAt {
		writeError(w, http.StatusBadRequest, "session expired")
		return
	}
	// Validate state parameter
	state := r.URL.Query().Get("state")
	if state == "" || state != sess.State {
		writeError(w, http.StatusBadRequest, "state mismatch")
		return
	}
	// Handle error from IdP
	if errMsg := r.URL.Query().Get("error"); errMsg != "" {
		desc := r.URL.Query().Get("error_description")
		writeError(w, http.StatusBadRequest, fmt.Sprintf("oidc error: %s: %s", errMsg, desc))
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		writeError(w, http.StatusBadRequest, "missing authorization code")
		return
	}

	// Exchange code for identity
	identity, err := h.Client.Exchange(r.Context(), code, sess.Nonce, sess.CodeVerifier)
	if err != nil {
		writeError(w, http.StatusUnauthorized, fmt.Sprintf("oidc exchange failed: %v", err))
		return
	}

	// Auto-provision or link user
	u, err := h.Store.GetUserByExternalID(identity.Subject)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if u == nil {
		// First time: create user
		attrs := map[string]string{"oidc_issuer": h.Cfg.OIDC.Issuer}
		if identity.Email != "" {
			attrs["email"] = identity.Email
		}
		u = &store.User{
			ID:           uuid.NewString(),
			Username:     identity.Username(),
			DisplayName:  identity.DisplayName(),
			ExternalID:   identity.Subject,
			Status:       "active",
			Attributes:   mustJSON(attrs),
			CreatedAt:    time.Now(),
		}
		if err := h.Store.CreateUser(u); err != nil {
			writeError(w, http.StatusInternalServerError, "user provisioning failed")
			return
		}
		// Map claims to roles
		mappedRoles := identity.ResolveRoles(h.Cfg.OIDC.ClaimMappings)
		for _, roleName := range mappedRoles {
			role, err := h.Store.GetRole(roleName)
			if err != nil {
				continue
			}
			if role == nil {
				// Auto-create role if mapped but not present
				role = &store.Role{ID: uuid.NewString(), Name: roleName}
				_ = h.Store.CreateRole(role)
			}
			_ = h.Store.AddUserRole(u.ID, role.ID)
		}
	} else {
		// Existing user: sync display name if changed
		if identity.DisplayName() != "" && u.DisplayName != identity.DisplayName() {
			u.DisplayName = identity.DisplayName()
			_ = h.Store.UpdateUser(u)
		}
	}

	// Resolve roles
	roles, err := h.Store.ListRolesForUser(u.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	roleNames := make([]string, 0, len(roles))
	for _, r := range roles {
		roleNames = append(roleNames, r.Name)
	}
	userAttrs, _ := h.Store.UserAttributes(u.ID)
	claims := &auth.Claims{
		UserID:      u.ID,
		Username:    u.Username,
		DisplayName: u.DisplayName,
		Roles:       roleNames,
		Attributes:  userAttrs,
	}
	tok, err := auth.GenerateToken(claims, h.Cfg.JWTSecret, h.Cfg.JWTExpiry)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "token generation failed")
		return
	}

	writeJSON(w, http.StatusOK, loginResponse{
		Token: tok,
		User:  toMe(u, roleNames, userAttrs),
	})
}

func mustJSON(v map[string]string) string {
	b, _ := json.Marshal(v)
	return string(b)
}
