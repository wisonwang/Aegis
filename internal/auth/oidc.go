package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/wisonwang/aegis/internal/config"
	"golang.org/x/oauth2"
)

// OIDCClient wraps an OpenID Connect provider and OAuth2 config.
type OIDCClient struct {
	provider *oidc.Provider
	oauth2   oauth2.Config
	verifier *oidc.IDTokenVerifier
	cfg      config.OIDCConfig
}

// NewOIDCClient discovers the IdP metadata and builds a verifier.
func NewOIDCClient(ctx context.Context, cfg config.OIDCConfig) (*OIDCClient, error) {
	provider, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	scopes := []string{oidc.ScopeOpenID}
	scopes = append(scopes, cfg.Scopes...)
	oauth2 := oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		RedirectURL:  cfg.RedirectURL,
		Endpoint:     provider.Endpoint(),
		Scopes:       scopes,
	}
	verifier := provider.Verifier(&oidc.Config{ClientID: cfg.ClientID})
	return &OIDCClient{provider: provider, oauth2: oauth2, verifier: verifier, cfg: cfg}, nil
}

// AuthURL builds the OIDC authorization URL with PKCE and nonce.
func (c *OIDCClient) AuthURL(state, nonce string) (url string, codeChallenge string, err error) {
	pkce, err := randomBytes(32)
	if err != nil {
		return "", "", err
	}
	challenge := pkceChallenge(pkce)
	url = c.oauth2.AuthCodeURL(state,
		oauth2.SetAuthURLParam("nonce", nonce),
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
	return url, challenge, nil
}

// Exchange turns the authorization code into verified claims.
func (c *OIDCClient) Exchange(ctx context.Context, code, nonce, verifier string) (*OIDCIdentity, error) {
	tok, err := c.oauth2.Exchange(ctx, code,
		oauth2.SetAuthURLParam("code_verifier", verifier),
	)
	if err != nil {
		return nil, fmt.Errorf("token exchange: %w", err)
	}
	rawIDToken, ok := tok.Extra("id_token").(string)
	if !ok {
		return nil, fmt.Errorf("id_token missing")
	}
	idToken, err := c.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, fmt.Errorf("id token verify: %w", err)
	}
	if idToken.Nonce != nonce {
		return nil, fmt.Errorf("nonce mismatch")
	}

	var claims map[string]interface{}
	if err := idToken.Claims(&claims); err != nil {
		return nil, fmt.Errorf("claims decode: %w", err)
	}

	return &OIDCIdentity{
		Subject:     idToken.Subject,
		Email:       stringClaim(claims, "email"),
		Name:        stringClaim(claims, "name"),
		GivenName:   stringClaim(claims, "given_name"),
		FamilyName:  stringClaim(claims, "family_name"),
		Picture:     stringClaim(claims, "picture"),
		RawClaims:   claims,
	}, nil
}

// OIDCIdentity is the normalized user profile from an IdP.
type OIDCIdentity struct {
	Subject    string
	Email      string
	Name       string
	GivenName  string
	FamilyName string
	Picture    string
	RawClaims  map[string]interface{}
}

// Username returns a stable local username derived from the identity.
func (id *OIDCIdentity) Username() string {
	if id.Email != "" {
		return id.Email
	}
	return id.Subject
}

// DisplayName returns a human-readable name.
func (id *OIDCIdentity) DisplayName() string {
	if id.Name != "" {
		return id.Name
	}
	if id.GivenName != "" && id.FamilyName != "" {
		return id.GivenName + " " + id.FamilyName
	}
	if id.GivenName != "" {
		return id.GivenName
	}
	if id.Email != "" {
		return id.Email
	}
	return id.Subject
}

// ResolveRoles maps OIDC claims to platform roles using the configured
// claim_mappings. It inspects the "groups" claim (array of strings) first,
// then falls back to a single string claim named "groups" or "roles".
func (id *OIDCIdentity) ResolveRoles(mappings map[string]string) []string {
	var sources []string
	if g, ok := id.RawClaims["groups"]; ok {
		switch v := g.(type) {
		case []string:
			sources = v
		case []interface{}:
			for _, e := range v {
				if s, ok := e.(string); ok {
					sources = append(sources, s)
				}
			}
		case string:
			sources = append(sources, v)
		}
	}
	if r, ok := id.RawClaims["roles"]; ok {
		switch v := r.(type) {
		case []string:
			sources = append(sources, v...)
		case []interface{}:
			for _, e := range v {
				if s, ok := e.(string); ok {
					sources = append(sources, s)
				}
			}
		case string:
			sources = append(sources, v)
		}
	}
	set := map[string]bool{}
	for _, src := range sources {
		if mapped, ok := mappings[src]; ok {
			set[mapped] = true
		}
	}
	out := make([]string, 0, len(set))
	for r := range set {
		out = append(out, r)
	}
	return out
}

// SessionState holds the ephemeral PKCE/state/nonce for one OIDC flow.
type SessionState struct {
	State        string `json:"state"`
	Nonce        string `json:"nonce"`
	CodeVerifier string `json:"code_verifier"`
	ExpiresAt    int64  `json:"expires_at"`
}

// Encode serializes the session to a base64 JSON blob suitable for a cookie.
func (s *SessionState) Encode() (string, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

// DecodeSessionState parses a cookie value back into state.
func DecodeSessionState(v string) (*SessionState, error) {
	b, err := base64.URLEncoding.DecodeString(v)
	if err != nil {
		return nil, err
	}
	var s SessionState
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// NewSessionState generates a fresh OIDC flow session.
func NewSessionState() (*SessionState, error) {
	state, err := randomBytes(24)
	if err != nil {
		return nil, err
	}
	nonce, err := randomBytes(24)
	if err != nil {
		return nil, err
	}
	pkce, err := randomBytes(32)
	if err != nil {
		return nil, err
	}
	return &SessionState{
		State:        base64.URLEncoding.EncodeToString(state),
		Nonce:        base64.URLEncoding.EncodeToString(nonce),
		CodeVerifier: base64.URLEncoding.EncodeToString(pkce),
		ExpiresAt:    time.Now().Add(10 * time.Minute).Unix(),
	}, nil
}

func randomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}

func pkceChallenge(verifier []byte) string {
	h := sha256.Sum256(verifier)
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func stringClaim(m map[string]interface{}, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// SetOIDCCookie writes the OIDC session cookie.
func SetOIDCCookie(w http.ResponseWriter, name, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   false, // set to true when served over HTTPS
	})
}

// ClearOIDCCookie deletes the OIDC session cookie.
func ClearOIDCCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{
		Name:   name,
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})
}

// Redirect writes a 302 redirect.
func Redirect(w http.ResponseWriter, r *http.Request, url string) {
	http.Redirect(w, r, url, http.StatusFound)
}

// SessionCookieName is the name of the cookie that carries the OIDC flow state.
const SessionCookieName = "aegis_oidc_session"
