package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

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

	// Auto-provision or link user, then issue a JWT.
	attrs := map[string]string{"oidc_issuer": h.Cfg.OIDC.Issuer}
	if identity.Email != "" {
		attrs["email"] = identity.Email
	}
	mappedRoles := identity.ResolveRoles(h.Cfg.OIDC.ClaimMappings)
	u, err := provisionOrLinkExternalUser(h.Store, identity.Subject, identity.Username(), identity.DisplayName(), attrs, mappedRoles)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "user provisioning failed")
		return
	}
	issueTokenForUser(w, h.Store, h.Cfg, u)
}

func mustJSON(v map[string]string) string {
	b, _ := json.Marshal(v)
	return string(b)
}
