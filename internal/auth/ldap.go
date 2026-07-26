package auth

import (
	"context"
	"crypto/tls"
	"fmt"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
	"github.com/wisonwang/aegis/internal/config"
)

// Directory abstracts an external identity directory used for password-based
// SSO (LDAP / Active Directory). It is an interface so the HTTP layer and the
// test suite can substitute an in-memory mock without a live server.
type Directory interface {
	// Authenticate verifies the credentials against the directory and returns
	// the normalized identity. A non-nil error means authentication failed
	// (bad credentials, user not found, or directory unreachable).
	Authenticate(ctx context.Context, username, password string) (*LDAPIdentity, error)
}

// LDAPIdentity is the normalized profile resolved from a directory entry.
type LDAPIdentity struct {
	DN          string            // distinguished name, used as the stable external id
	Username    string            // local username (from UserAttr or the DN)
	DisplayName string            // human-readable name
	Email       string            // email if the directory exposes one
	Attributes  map[string]string // raw attributes carried onto the platform user
	Groups      []string          // group membership values (GroupNameAttr)
}

// ResolveRoles maps directory group memberships to platform roles using the
// configured claim_mappings (group value -> role name). Each group value is
// tried verbatim; a configured mapping contributes its target role.
func (id *LDAPIdentity) ResolveRoles(mappings map[string]string) []string {
	set := map[string]bool{}
	for _, g := range id.Groups {
		if mapped, ok := mappings[g]; ok {
			set[mapped] = true
		}
	}
	out := make([]string, 0, len(set))
	for r := range set {
		out = append(out, r)
	}
	return out
}

// goLDAPDirectory is the production Directory backed by github.com/go-ldap.
type goLDAPDirectory struct {
	cfg config.LDAPConfig
}

// NewLDAPDirectory builds a Directory from configuration.
func NewLDAPDirectory(cfg config.LDAPConfig) Directory {
	return &goLDAPDirectory{cfg: cfg}
}

// Authenticate performs a three-step bind against the directory:
//  1. (optional) bind a service account so we may search for the user
//  2. search the user entry by UserFilter, resolving the user DN
//  3. bind as the user DN with the supplied password — this is the real
//     credential check; a failure here means invalid credentials
//
// Group membership is then resolved best-effort to drive role mapping.
func (d *goLDAPDirectory) Authenticate(ctx context.Context, username, password string) (*LDAPIdentity, error) {
	l, err := ldap.DialURL(d.cfg.URL, ldap.DialWithTLSConfig(&tls.Config{InsecureSkipVerify: d.cfg.SkipTLSVerify}))
	if err != nil {
		return nil, fmt.Errorf("ldap dial %s: %w", d.cfg.URL, err)
	}
	defer l.Close()
	l.SetTimeout(15 * time.Second)

	// Optional service-account bind for the search phase.
	if d.cfg.BindDN != "" {
		if err := l.Bind(d.cfg.BindDN, d.cfg.BindPassword); err != nil {
			return nil, fmt.Errorf("ldap bind (service account): %w", err)
		}
	}

	// Resolve the user entry (and its DN) by the configured filter.
	searchAttrs := []string{"dn"}
	if d.cfg.UserAttr != "" {
		searchAttrs = append(searchAttrs, d.cfg.UserAttr)
	}
	if d.cfg.DisplayAttr != "" {
		searchAttrs = append(searchAttrs, d.cfg.DisplayAttr)
	}
	if d.cfg.EmailAttr != "" {
		searchAttrs = append(searchAttrs, d.cfg.EmailAttr)
	}
	filter := replaceAll(d.cfg.UserFilter, "%s", ldap.EscapeFilter(username))
	search := ldap.NewSearchRequest(
		d.cfg.BaseDN,
		ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false,
		filter, searchAttrs, nil,
	)
	res, err := l.Search(search)
	if err != nil {
		return nil, fmt.Errorf("ldap search: %w", err)
	}
	if len(res.Entries) == 0 {
		return nil, fmt.Errorf("user %q not found in directory", username)
	}
	if len(res.Entries) > 1 {
		return nil, fmt.Errorf("ambiguous user %q: %d matches", username, len(res.Entries))
	}
	entry := res.Entries[0]
	userDN := entry.DN

	// The authoritative credential check: bind as the user.
	if err := l.Bind(userDN, password); err != nil {
		return nil, fmt.Errorf("ldap bind (user): %w", err)
	}

	// Best-effort group resolution (drives role mapping).
	groups := d.resolveGroups(l, userDN)

	uname := entry.GetAttributeValue(d.cfg.UserAttr)
	if uname == "" {
		uname = userDN
	}
	disp := entry.GetAttributeValue(d.cfg.DisplayAttr)
	if disp == "" {
		disp = uname
	}
	email := entry.GetAttributeValue(d.cfg.EmailAttr)

	return &LDAPIdentity{
		DN:          userDN,
		Username:    uname,
		DisplayName: disp,
		Email:       email,
		Attributes:  map[string]string{"ldap_dn": userDN, "ldap_source": d.cfg.URL},
		Groups:      groups,
	}, nil
}

// resolveGroups searches for groups that contain the user. It is best-effort:
// any error is swallowed and an empty group list is returned so that auth still
// succeeds (the user simply gets no group-derived roles).
func (d *goLDAPDirectory) resolveGroups(l *ldap.Conn, userDN string) []string {
	if d.cfg.GroupBaseDN == "" || d.cfg.GroupFilter == "" || d.cfg.GroupNameAttr == "" {
		return nil
	}
	gfilter := replaceAll(d.cfg.GroupFilter, "%d", ldap.EscapeFilter(userDN))
	gsearch := ldap.NewSearchRequest(
		d.cfg.GroupBaseDN,
		ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false,
		gfilter, []string{d.cfg.GroupNameAttr}, nil,
	)
	gres, err := l.Search(gsearch)
	if err != nil {
		return nil
	}
	var groups []string
	for _, e := range gres.Entries {
		for _, a := range e.Attributes {
			if a.Name == d.cfg.GroupNameAttr {
				groups = append(groups, a.Values...)
			}
		}
	}
	return groups
}

// replaceAll is a small helper so we do not depend on strings.ReplaceAll
// semantics in two call sites; it replaces every occurrence of old with new.
func replaceAll(s, old, new string) string {
	return strings.ReplaceAll(s, old, new)
}
