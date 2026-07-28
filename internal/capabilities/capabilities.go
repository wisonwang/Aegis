// Package capabilities implements the open-core tiering decision point
// (ADR-002). It is the single place that answers "is feature X enabled?".
//
// Design notes:
//   - A single Capabilities value is built once at startup from config
//     (edition shortcut or a signed license) and passed down to the router.
//   - Enterprise code lives in package internal/enterprise and depends on this
//     package (enterprise -> core). Core never imports enterprise.
//   - The license is a speed-bump, not a vault: it is an HMAC-signed token
//     verifiable with the same JWTSecret used for auth. That is intentional
//     for a solo project validating adoption — a determined operator can patch
//     it out; real license hardening is a later concern (see ADR-002).
package capabilities

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/wisonwang/aegis/internal/config"
)

// Edition is the distribution tier.
type Edition string

const (
	EditionCommunity  Edition = "community"
	EditionEnterprise Edition = "enterprise"
)

// Capability names the isolable enterprise features.
type Capability string

const (
	CapMultiTenant      Capability = "multi_tenant"       // ADR-001 workspaces
	CapSSOFederation    Capability = "sso_federation"     // OIDC/LDAP/AD multi-IdP + SCIM
	CapApprovalWorkflow Capability = "approval_workflow"  // data-access approval flow
	CapSIEMExport       Capability = "siem_export"        // audit stream forwarding
	CapDataProducts     Capability = "data_products"      // datasets + semantic metric layer
	CapHAControlPlane   Capability = "ha_control_plane"   // external PG control plane / clustering
)

// allEnterprise is the full enterprise set granted by edition=enterprise.
var allEnterprise = []Capability{
	CapMultiTenant, CapSSOFederation, CapApprovalWorkflow,
	CapSIEMExport, CapDataProducts, CapHAControlPlane,
}

type licenseClaims struct {
	Edition   string   `json:"edition"`
	Caps      []string `json:"caps"`
	ExpiresAt int64    `json:"exp"`
}

// Capabilities is the resolved feature set for this running instance.
type Capabilities struct {
	edition Edition
	caps    map[Capability]bool
	exp     time.Time
}

// Community returns a community-edition (no enterprise features) resolver.
func Community() *Capabilities {
	return &Capabilities{edition: EditionCommunity, caps: map[Capability]bool{}}
}

func enterpriseAll() *Capabilities {
	c := &Capabilities{edition: EditionEnterprise, caps: map[Capability]bool{}}
	for _, cp := range allEnterprise {
		c.caps[cp] = true
	}
	return c
}

// New resolves capabilities from config. Precedence:
//  1. edition == "enterprise"  -> all enterprise caps (dev/demo shortcut)
//  2. license_key / license_file -> signed, scoped, expiring caps
//  3. otherwise                  -> community
//
// A malformed or expired license degrades to community rather than failing
// closed, so a bad key never bricks the free tier (the operator is told via
// the returned error and the caller logs it).
func New(cfg *config.Config) (*Capabilities, error) {
	if cfg.Edition == string(EditionEnterprise) {
		return enterpriseAll(), nil
	}
	key := strings.TrimSpace(cfg.LicenseKey)
	if key == "" && cfg.LicenseFile != "" {
		if b, err := os.ReadFile(cfg.LicenseFile); err == nil {
			key = strings.TrimSpace(string(b))
		}
	}
	if key == "" {
		return Community(), nil
	}
	lc, err := parseLicense(key, cfg.JWTSecret)
	if err != nil {
		// Degrade to community but surface the error so the caller can log it.
		// caps is never nil, so it is always safe to inspect.
		return Community(), fmt.Errorf("invalid license: %w", err)
	}
	if lc.Edition != string(EditionEnterprise) {
		return Community(), nil
	}
	c := &Capabilities{edition: EditionEnterprise, caps: map[Capability]bool{}, exp: time.Unix(lc.ExpiresAt, 0)}
	for _, cp := range lc.Caps {
		c.caps[Capability(cp)] = true
	}
	return c, nil
}

// Has reports whether cap is entitled in the current edition.
func (c *Capabilities) Has(cap Capability) bool {
	if c.edition != EditionEnterprise {
		return false
	}
	if !c.exp.IsZero() && time.Now().After(c.exp) {
		return false
	}
	return c.caps[cap]
}

// Edition returns the resolved distribution tier.
func (c *Capabilities) Edition() Edition { return c.edition }

// List returns the entitled enterprise capabilities.
func (c *Capabilities) List() []Capability {
	out := make([]Capability, 0, len(allEnterprise))
	for _, cp := range allEnterprise {
		if c.Has(cp) {
			out = append(out, cp)
		}
	}
	return out
}

// Strings returns List as plain strings (for JSON / UI consumption).
func (c *Capabilities) Strings() []string {
	out := make([]string, 0, len(allEnterprise))
	for _, cp := range c.List() {
		out = append(out, string(cp))
	}
	return out
}

// SignLicense produces a signed enterprise license token. Useful for tests
// and for issuing dev/demo licenses; production issuance should happen in a
// separate, secret-protected flow.
func SignLicense(secret, edition string, caps []Capability, ttl time.Duration) (string, error) {
	claims := licenseClaims{
		Edition:   edition,
		Caps:      make([]string, 0, len(caps)),
		ExpiresAt: time.Now().Add(ttl).Unix(),
	}
	for _, cp := range caps {
		claims.Caps = append(claims.Caps, string(cp))
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	sig := sign(secret, payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + sig, nil
}

func parseLicense(token, secret string) (*licenseClaims, error) {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("malformed license token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("bad payload: %w", err)
	}
	if !verify(secret, payload, parts[1]) {
		return nil, fmt.Errorf("signature verification failed")
	}
	var lc licenseClaims
	if err := json.Unmarshal(payload, &lc); err != nil {
		return nil, fmt.Errorf("bad claims: %w", err)
	}
	if lc.ExpiresAt != 0 && time.Now().Unix() > lc.ExpiresAt {
		return nil, fmt.Errorf("license expired")
	}
	return &lc, nil
}

func sign(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func verify(secret string, payload []byte, sig string) bool {
	return hmac.Equal([]byte(sign(secret, payload)), []byte(sig))
}
