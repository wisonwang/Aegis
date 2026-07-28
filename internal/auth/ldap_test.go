package auth

import (
	"context"
	"testing"

	"github.com/wisonwang/aegis/internal/config"
)

func TestLDAPIdentity_ResolveRoles(t *testing.T) {
	id := &LDAPIdentity{Groups: []string{"aegis-admins", "aegis-analysts", "other"}}
	mappings := map[string]config.ClaimMapping{
		"aegis-admins":   {Role: "admin"},
		"aegis-analysts": {Role: "analyst"},
	}
	roles := id.ResolveRoles(mappings)
	if !contains(roles, "admin") {
		t.Fatalf("expected admin role in %v", roles)
	}
	if !contains(roles, "analyst") {
		t.Fatalf("expected analyst role in %v", roles)
	}
	if contains(roles, "other") {
		t.Fatalf("unmapped group leaked into roles: %v", roles)
	}
}

func TestLDAPIdentity_ResolveRoles_NoMapping(t *testing.T) {
	id := &LDAPIdentity{Groups: []string{"nobody"}}
	if roles := id.ResolveRoles(nil); len(roles) != 0 {
		t.Fatalf("expected no roles, got %v", roles)
	}
}

func TestLDAPIdentity_ResolveWorkspaces(t *testing.T) {
	id := &LDAPIdentity{Groups: []string{"aegis-acme", "aegis-shared"}}
	mappings := map[string]config.ClaimMapping{
		"aegis-acme": {
			Role:        "member",
			Workspaces:  []config.WorkspaceBinding{{Slug: "acme", Role: "member"}},
		},
		"aegis-shared": {
			Role:       "viewer",
			Workspaces: []config.WorkspaceBinding{{Slug: "acme", Role: "member"}, {Slug: "globex", Role: "viewer"}},
		},
	}
	got := id.ResolveWorkspaces(mappings)
	set := map[string]bool{}
	for _, wb := range got {
		set[wb.Slug+"/"+wb.Role] = true
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 unique bindings (acme/member collapsed), got %v", got)
	}
	if !set["acme/member"] || !set["globex/viewer"] {
		t.Errorf("missing expected bindings: %v", got)
	}
}

func TestNewLDAPDirectory(t *testing.T) {
	d := NewLDAPDirectory(config.LDAPConfig{URL: "ldap://127.0.0.1:389"})
	if d == nil {
		t.Fatal("expected non-nil directory")
	}
	// A dial against an unreachable directory must return an error, not panic.
	if _, err := d.Authenticate(context.Background(), "u", "p"); err == nil {
		t.Fatal("expected dial error for unreachable directory")
	}
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
