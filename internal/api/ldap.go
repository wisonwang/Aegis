package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/wisonwang/aegis/internal/auth"
	"github.com/wisonwang/aegis/internal/config"
	"github.com/wisonwang/aegis/internal/store"
)

// LDAPHandler bundles LDAP-specific dependencies.
type LDAPHandler struct {
	Dir   auth.Directory // Directory abstraction (mockable for tests)
	Store *store.Store
	Cfg   *config.Config
}

// NewLDAPHandler creates the handler; returns nil if LDAP is disabled or
// required configuration is missing.
func NewLDAPHandler(st *store.Store, cfg *config.Config) (*LDAPHandler, error) {
	if !cfg.LDAP.Enabled {
		return nil, nil
	}
	if cfg.LDAP.URL == "" || cfg.LDAP.BaseDN == "" || cfg.LDAP.UserFilter == "" {
		return nil, fmt.Errorf("ldap enabled but missing required config (url, base_dn, user_filter)")
	}
	return &LDAPHandler{Dir: auth.NewLDAPDirectory(cfg.LDAP), Store: st, Cfg: cfg}, nil
}

// LDAPLogin authenticates a user against the directory and returns a JWT.
// It mirrors the OIDC callback: authenticate -> map groups to roles ->
// auto-provision (first login) -> issue token.
func (h *LDAPHandler) LDAPLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.Username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "username and password required")
		return
	}

	id, err := h.Dir.Authenticate(r.Context(), req.Username, req.Password)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "ldap authentication failed")
		return
	}

	// External id is the directory DN, prefixed to avoid collisions with other
	// identity providers that might reuse the same subject value.
	externalID := "ldap:" + id.DN
	attrs := map[string]string{"ldap_dn": id.DN, "ldap_source": h.Cfg.LDAP.URL}
	if id.Email != "" {
		attrs["email"] = id.Email
	}
	roleNames := id.ResolveRoles(h.Cfg.LDAP.ClaimMappings)
	roleNames = append(roleNames, h.Cfg.LDAP.DefaultRoles...)
	wsBindings := id.ResolveWorkspaces(h.Cfg.LDAP.ClaimMappings)

	u, err := provisionOrLinkExternalUser(h.Store, externalID, id.Username, id.DisplayName, attrs, roleNames, wsBindings)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "user provisioning failed")
		return
	}
	issueTokenForUser(w, h.Store, h.Cfg, u)
}
