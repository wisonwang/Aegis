package capabilities

import (
	"testing"
	"time"

	"github.com/wisonwang/aegis/internal/config"
)

func TestCommunityRejectsEnterprise(t *testing.T) {
	cfg := config.Default()
	cfg.Edition = "community"
	caps, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if caps.Edition() != EditionCommunity {
		t.Fatalf("expected community edition, got %s", caps.Edition())
	}
	if caps.Has(CapDataProducts) {
		t.Fatal("community must NOT grant data_products")
	}
	if caps.Has(CapApprovalWorkflow) {
		t.Fatal("community must NOT grant approval_workflow")
	}
	if len(caps.List()) != 0 {
		t.Fatalf("community must expose no enterprise caps, got %v", caps.List())
	}
}

func TestEnterpriseGrantsAll(t *testing.T) {
	cfg := config.Default()
	cfg.Edition = "enterprise"
	caps, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, cp := range allEnterprise {
		if !caps.Has(cp) {
			t.Fatalf("enterprise must grant %s", cp)
		}
	}
}

func TestLicenseRoundtripScoped(t *testing.T) {
	cfg := config.Default()
	lic, err := SignLicense(cfg.JWTSecret, "enterprise", []Capability{CapApprovalWorkflow}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	cfg2 := config.Default()
	cfg2.LicenseKey = lic
	caps, err := New(cfg2)
	if err != nil {
		t.Fatal(err)
	}
	if !caps.Has(CapApprovalWorkflow) {
		t.Fatal("scoped license should grant approval_workflow")
	}
	if caps.Has(CapDataProducts) {
		t.Fatal("scoped license must NOT grant data_products")
	}
	if caps.Has(CapMultiTenant) {
		t.Fatal("scoped license must NOT grant multi_tenant")
	}
}

func TestExpiredLicenseDegradesToCommunity(t *testing.T) {
	cfg := config.Default()
	lic, err := SignLicense(cfg.JWTSecret, "enterprise", []Capability{CapDataProducts}, -time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	cfg.LicenseKey = lic
	caps, err := New(cfg)
	// An expired token is a degraded (not fatal) condition: New returns a
	// community resolver plus an error for the caller to log.
	if err == nil {
		t.Fatal("expected error for expired license")
	}
	if caps.Has(CapDataProducts) {
		t.Fatal("expired license must not grant anything")
	}
	if caps.Edition() != EditionCommunity {
		t.Fatalf("expired license should degrade to community, got %s", caps.Edition())
	}
}

func TestInvalidLicenseDegradesToCommunity(t *testing.T) {
	cfg := config.Default()
	cfg.LicenseKey = "not-a-valid-token"
	caps, err := New(cfg)
	if err == nil {
		t.Fatal("expected error for malformed license")
	}
	if caps.Edition() != EditionCommunity {
		t.Fatalf("invalid license must degrade to community, got %s", caps.Edition())
	}
	if caps.Has(CapDataProducts) {
		t.Fatal("invalid license must not grant enterprise caps")
	}
}
