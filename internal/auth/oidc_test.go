package auth

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/wisonwang/aegis/internal/config"
)

func TestSessionStateRoundTrip(t *testing.T) {
	sess, err := NewSessionState()
	if err != nil {
		t.Fatalf("NewSessionState: %v", err)
	}
	if sess.State == "" || sess.Nonce == "" || sess.CodeVerifier == "" {
		t.Fatal("expected non-empty state/nonce/verifier")
	}
	if sess.ExpiresAt <= time.Now().Unix() {
		t.Fatal("expected future expiry")
	}

	encoded, err := sess.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	decoded, err := DecodeSessionState(encoded)
	if err != nil {
		t.Fatalf("DecodeSessionState: %v", err)
	}
	if decoded.State != sess.State {
		t.Errorf("state mismatch: got %q, want %q", decoded.State, sess.State)
	}
	if decoded.Nonce != sess.Nonce {
		t.Errorf("nonce mismatch")
	}
	if decoded.CodeVerifier != sess.CodeVerifier {
		t.Errorf("code_verifier mismatch")
	}
}

func TestOIDCIdentityUsername(t *testing.T) {
	id := &OIDCIdentity{Subject: "sub123", Email: "alice@example.com"}
	if got := id.Username(); got != "alice@example.com" {
		t.Errorf("Username with email: got %q, want %q", got, "alice@example.com")
	}
	id.Email = ""
	if got := id.Username(); got != "sub123" {
		t.Errorf("Username without email: got %q, want %q", got, "sub123")
	}
}

func TestOIDCIdentityDisplayName(t *testing.T) {
	tests := []struct {
		name   string
		id     *OIDCIdentity
		expect string
	}{
		{"name", &OIDCIdentity{Name: "Alice Smith"}, "Alice Smith"},
		{"given+family", &OIDCIdentity{GivenName: "Alice", FamilyName: "Smith"}, "Alice Smith"},
		{"given only", &OIDCIdentity{GivenName: "Alice"}, "Alice"},
		{"email", &OIDCIdentity{Email: "alice@example.com"}, "alice@example.com"},
		{"sub", &OIDCIdentity{Subject: "sub123"}, "sub123"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.id.DisplayName(); got != tc.expect {
				t.Errorf("DisplayName: got %q, want %q", got, tc.expect)
			}
		})
	}
}

func TestResolveRoles(t *testing.T) {
	mappings := map[string]config.ClaimMapping{
		"admins":   {Role: "admin"},
		"analysts": {Role: "analyst"},
	}
	id := &OIDCIdentity{
		RawClaims: map[string]interface{}{
			"groups": []interface{}{"admins", "analysts", "unknown"},
		},
	}
	roles := id.ResolveRoles(mappings)
	set := map[string]bool{}
	for _, r := range roles {
		set[r] = true
	}
	if !set["admin"] || !set["analyst"] {
		t.Errorf("expected admin and analyst, got %v", roles)
	}
	if set["unknown"] {
		t.Error("unexpected unknown role")
	}
}

func TestResolveRolesStringClaim(t *testing.T) {
	mappings := map[string]config.ClaimMapping{"staff": {Role: "analyst"}}
	id := &OIDCIdentity{RawClaims: map[string]interface{}{"roles": "staff"}}
	roles := id.ResolveRoles(mappings)
	if len(roles) != 1 || roles[0] != "analyst" {
		t.Errorf("expected [analyst], got %v", roles)
	}
}

func TestResolveWorkspaces(t *testing.T) {
	mappings := map[string]config.ClaimMapping{
		"acme-admins": {
			Role: "admin",
			Workspaces: []config.WorkspaceBinding{
				{Slug: "acme", Role: "workspace_admin"},
				{Slug: "globex", Role: "member"},
			},
		},
		"shared": {Role: "viewer", Workspaces: []config.WorkspaceBinding{{Slug: "acme", Role: "member"}}},
	}
	id := &OIDCIdentity{
		RawClaims: map[string]interface{}{
			"groups": []interface{}{"acme-admins", "shared"},
		},
	}
	got := id.ResolveWorkspaces(mappings)
	set := map[string]bool{}
	for _, wb := range got {
		set[wb.Slug+"/"+wb.Role] = true
	}
	// acme/member is contributed by both groups but must collapse to one entry.
	if len(got) != 3 {
		t.Fatalf("expected 3 unique bindings (collapsed), got %v", got)
	}
	if !set["acme/workspace_admin"] || !set["globex/member"] || !set["acme/member"] {
		t.Errorf("missing expected bindings: %v", got)
	}
}

func TestClaimMappingUnmarshalLegacyString(t *testing.T) {
	var m map[string]config.ClaimMapping
	if err := json.Unmarshal([]byte(`{"admins":"admin","analysts":"analyst"}`), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["admins"].Role != "admin" || m["analysts"].Role != "analyst" {
		t.Fatalf("legacy string form not parsed: %+v", m)
	}
	if len(m["admins"].Workspaces) != 0 {
		t.Errorf("legacy form should have no workspaces")
	}
}

func TestClaimMappingUnmarshalStructured(t *testing.T) {
	var m map[string]config.ClaimMapping
	raw := `{"acme-admins":{"role":"admin","workspaces":[{"slug":"acme","role":"workspace_admin"}]}}`
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	cm := m["acme-admins"]
	if cm.Role != "admin" || len(cm.Workspaces) != 1 || cm.Workspaces[0].Slug != "acme" {
		t.Fatalf("structured form not parsed: %+v", cm)
	}
}

func TestPKCEChallenge(t *testing.T) {
	v := []byte("test-verifier-123")
	ch1 := pkceChallenge(v)
	ch2 := pkceChallenge(v)
	if ch1 == "" {
		t.Fatal("expected non-empty challenge")
	}
	if ch1 != ch2 {
		t.Error("challenge not deterministic")
	}
	if ch1 == string(v) {
		t.Error("challenge should be hashed")
	}
}
