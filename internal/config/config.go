package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
)

// Config is the top-level platform configuration.
// It is loaded from a JSON file (default ./config.json) with optional
// environment overrides (AEGIS_*).
type Config struct {
	ListenAddr string         `json:"listen_addr"` // HTTP listen address, e.g. ":8080"
	JWTSecret  string         `json:"jwt_secret"`  // HMAC secret for token signing
	JWTExpiry  string         `json:"jwt_expiry"`  // token TTL, e.g. "24h"
	DBPath     string         `json:"db_path"`     // SQLite path for the control plane store
	DataDir    string         `json:"data_dir"`    // directory for demo/seeded database files
	SeedDemo   bool           `json:"seed_demo"`   // seed demo datasource + users on first run
	MCP        MCPConfig      `json:"mcp"`
	Limits     Limits         `json:"limits"`
	Alerting   AlertingConfig `json:"alerting"`
	OIDC       OIDCConfig     `json:"oidc"`
	LDAP       LDAPConfig     `json:"ldap"`
	NL2SQL     NL2SQLConfig   `json:"nl2sql"`
	Logging    LoggingConfig  `json:"logging"`
	MaskSecret string         `json:"mask_secret"` // server key for keyed masking (tokenize/fpe); source from KMS in prod
	Edition    string         `json:"edition"`      // "community" (default) | "enterprise"
	LicenseKey string         `json:"license_key"`  // signed enterprise license token (alternative to edition=enterprise)
	LicenseFile string        `json:"license_file"` // path to a file containing the license token
}

// LoggingConfig selects the structured-log output format and minimum level.
// Zero values fall back to safe defaults; both fields can also be set via
// the AEGIS_LOG_FORMAT / AEGIS_LOG_LEVEL environment variables.
type LoggingConfig struct {
	Format string `json:"format"` // "json" (default) or "text"
	Level  string `json:"level"`  // "debug" | "info" (default) | "warn" | "error"
}

// WorkspaceBinding links an identity-provider group to a workspace with a
// workspace-scoped role. It is the workspace half of a ClaimMapping.
type WorkspaceBinding struct {
	Slug string `json:"slug"` // target workspace slug (must already exist)
	Role string `json:"role"` // workspace role: workspace_admin | member | viewer (empty => member)
}

// ClaimMapping binds an identity-provider group/claim value to platform
// entitlements. It supports two JSON shapes for backward compatibility:
//
//	legacy string form:  "admins": "admin"
//	structured form:     "admins": {"role": "admin", "workspaces": [{"slug":"acme","role":"workspace_admin"}]}
//
// The structured form lets a single IdP group grant both a platform role and
// membership in one or more workspaces (ADR-001 Phase 1: group -> workspace).
type ClaimMapping struct {
	Role      string            `json:"role"`      // platform role granted by this group
	Workspaces []WorkspaceBinding `json:"workspaces"` // workspaces the user joins on login
}

// UnmarshalJSON accepts both the legacy string form and the structured form so
// existing config files keep working after this type was extended.
func (m *ClaimMapping) UnmarshalJSON(b []byte) error {
	// Try the legacy string form first.
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		m.Role = s
		return nil
	}
	// Structured form.
	type alias ClaimMapping
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*m = ClaimMapping(a)
	return nil
}

// OIDCConfig configures OpenID Connect single sign-on. When Enabled, the
// platform delegates authentication to an external IdP and auto-provisions
// users on first login.
type OIDCConfig struct {
	Enabled       bool              `json:"enabled"`        // enable OIDC login flow
	Issuer        string            `json:"issuer"`         // IdP issuer URL, e.g. "https://accounts.google.com"
	ClientID      string            `json:"client_id"`      // OAuth2 client id
	ClientSecret  string            `json:"client_secret"`  // OAuth2 client secret
	RedirectURL   string            `json:"redirect_url"`   // callback URL registered with the IdP, e.g. "http://localhost:8080/api/v1/auth/oidc/callback"
	Scopes        []string          `json:"scopes"`         // additional scopes beyond openid,profile,email
	ClaimMappings map[string]ClaimMapping `json:"claim_mappings"` // map OIDC claim values to platform roles (+ optional workspaces). Legacy string form still supported.
}

// NL2SQLConfig configures the natural-language-to-SQL gateway. When Enabled
// and an APIKey is present, Aegis can translate a natural-language question
// into a governed SQL query and execute it through the normal governed path
// (so table/row/column governance + masking + audit still apply). The LLM
// endpoint is OpenAI-compatible (works with OpenAI, Volcengine Ark/doubao,
// Azure OpenAI, local vLLM, etc.). Leave Enabled=false to keep the feature
// off; the platform then returns a clear "not configured" error.
type NL2SQLConfig struct {
	Enabled    bool   `json:"enabled"`     // enable the NL2SQL gateway
	Provider   string `json:"provider"`    // "openai" (OpenAI-compatible chat completions)
	BaseURL    string `json:"base_url"`    // e.g. https://api.openai.com/v1 or https://ark.cn-beijing.volces.com/api/v3
	APIKey     string `json:"api_key"`     // LLM provider key (source from secret manager in prod)
	Model      string `json:"model"`       // model id, e.g. gpt-4o-mini
	TimeoutSec int    `json:"timeout_sec"` // per-request timeout (0 = default 30)
	MaxRetries int    `json:"max_retries"` // retry count on transient errors (0 = default 2)
}

// limiting. Zero values fall back to platform defaults; admin can be exempted.
type Limits struct {
	MaxRows         int    `json:"max_rows"`              // max result rows per query (0 = default 1000)
	QueryTimeout    string `json:"query_timeout"`         // per-query timeout, e.g. "30s" (0/"" = default 30s)
	RatePerMin      int    `json:"rate_per_min"`          // max queries per principal per minute (0 = default 120)
	AdminExempt     bool   `json:"admin_exempt"`          // admin bypasses limits (default true via Default())
	MaxAffectedRows int    `json:"max_affected_rows"`     // max rows a single UPDATE/DELETE may touch (0 = no cap)
	AllowNoWhere    bool   `json:"allow_no_where_writes"` // permit UPDATE/DELETE without WHERE (unsafe; default false => blocked)
}

// AlertingConfig tunes the anomaly-detection engine that watches the audit
// stream for risky agent/user behavior (probing, bulk export, off-hours).
// Zero values fall back to safe defaults; thresholds can also be set via the
// AEGIS_ALERT_* environment variables.
type AlertingConfig struct {
	DeniedCount   int    `json:"denied_count"`      // denied queries within window to trip (0 = default 10)
	DeniedWindow  int    `json:"denied_window_sec"` // sliding window in seconds (0 = default 60)
	BulkRows      int    `json:"bulk_rows"`         // single-query rows >= this trips bulk_export (0 = default 5000)
	OffHoursOn    bool   `json:"off_hours_on"`      // enable the off-hours access rule (default false)
	OffHoursStart int    `json:"off_hours_start"`   // inclusive local hour, e.g. 0
	OffHoursEnd   int    `json:"off_hours_end"`     // exclusive local hour, e.g. 6
	Cooldown      int    `json:"cooldown_sec"`      // min seconds between repeated alerts of the same rule (0 = default 300)
	Webhook       string `json:"webhook"`           // optional URL to POST raised alerts to
}

// LDAPConfig configures password-based directory (LDAP / Active Directory)
// single sign-on. When Enabled, the platform authenticates users against an
// external directory and auto-provisions them on first login, mapping group
// memberships to platform roles via ClaimMappings.
type LDAPConfig struct {
	Enabled       bool              `json:"enabled"`         // enable LDAP login flow
	URL           string            `json:"url"`             // directory URL, e.g. "ldap://dc1.example.com:389" or "ldaps://dc1.example.com:636"
	BindDN        string            `json:"bind_dn"`         // service account DN used to search (leave empty for anonymous bind)
	BindPassword  string            `json:"bind_password"`   // service account password
	BaseDN        string            `json:"base_dn"`         // search base, e.g. "dc=example,dc=com"
	UserFilter    string            `json:"user_filter"`     // search filter with %s for the login name, e.g. "(uid=%s)" or "(sAMAccountName=%s)"
	UserAttr      string            `json:"user_attr"`       // attribute used as local username, e.g. "uid" (falls back to user DN)
	DisplayAttr   string            `json:"display_attr"`    // attribute used as display name, e.g. "displayName"
	EmailAttr     string            `json:"email_attr"`      // attribute carrying the email, e.g. "mail"
	GroupBaseDN   string            `json:"group_base_dn"`   // search base for groups, e.g. "ou=groups,dc=example,dc=com"
	GroupFilter   string            `json:"group_filter"`    // group filter with %d for the user DN, e.g. "(member=%d)"
	GroupNameAttr string            `json:"group_name_attr"` // attribute holding the group name, e.g. "cn"
	ClaimMappings map[string]ClaimMapping `json:"claim_mappings"`  // map directory group values to platform roles (+ optional workspaces). Legacy string form still supported.
	DefaultRoles  []string          `json:"default_roles"`   // roles granted to every LDAP-authenticated user
	SkipTLSVerify bool              `json:"skip_tls_verify"` // insecure: skip TLS cert verification (dev only)
}

// MCPConfig configures the Model Context Protocol endpoint used by AI agents.
type MCPConfig struct {
	Enabled     bool   `json:"enabled"`      // enable the /mcp endpoint
	Path        string `json:"path"`         // mount path, default "/mcp"
	APIKey      string `json:"api_key"`      // static service-account key for agents
	RequireAuth bool   `json:"require_auth"` // require Bearer token or API key
}

// Load reads configuration from a JSON file, then applies environment overrides.
func Load(path string) (*Config, error) {
	cfg := Default()
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read config %s: %w", path, err)
		}
		if err := json.Unmarshal(b, cfg); err != nil {
			return nil, fmt.Errorf("parse config %s: %w", path, err)
		}
	}
	applyEnv(cfg)
	if cfg.JWTSecret == "" {
		cfg.JWTSecret = "change-me-in-production"
	}
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ":8080"
	}
	if cfg.MCP.Path == "" {
		cfg.MCP.Path = "/mcp"
	}
	return cfg, nil
}

// Default returns a sane default configuration.
func Default() *Config {
	return &Config{
		ListenAddr: ":8080",
		JWTExpiry:  "24h",
		DBPath:     "aegis.db",
		DataDir:    "./data",
		SeedDemo:   true,
		MCP: MCPConfig{
			Enabled:     true,
			Path:        "/mcp",
			RequireAuth: true,
		},
		Limits: Limits{
			MaxRows:         1000,
			QueryTimeout:    "30s",
			RatePerMin:      120,
			AdminExempt:     true,
			MaxAffectedRows: 0,
			AllowNoWhere:    false,
		},
		Alerting: AlertingConfig{
			DeniedCount:   10,
			DeniedWindow:  60,
			BulkRows:      5000,
			OffHoursOn:    false,
			OffHoursStart: 0,
			OffHoursEnd:   6,
			Cooldown:      300,
		},
		OIDC: OIDCConfig{
			Scopes: []string{"profile", "email"},
		},
		NL2SQL: NL2SQLConfig{
			Provider:   "openai",
			BaseURL:    "https://api.openai.com/v1",
			Model:      "gpt-4o-mini",
			TimeoutSec: 30,
			MaxRetries: 2,
		},
		Logging: LoggingConfig{
			Format: "json",
			Level:  "info",
		},
	}
}

func applyEnv(cfg *Config) {
	if v := os.Getenv("AEGIS_LISTEN_ADDR"); v != "" {
		cfg.ListenAddr = v
	}
	if v := os.Getenv("AEGIS_JWT_SECRET"); v != "" {
		cfg.JWTSecret = v
	}
	if v := os.Getenv("AEGIS_JWT_EXPIRY"); v != "" {
		cfg.JWTExpiry = v
	}
	if v := os.Getenv("AEGIS_DB_PATH"); v != "" {
		cfg.DBPath = v
	}
	if v := os.Getenv("AEGIS_DATA_DIR"); v != "" {
		cfg.DataDir = v
	}
	if v := os.Getenv("AEGIS_MCP_API_KEY"); v != "" {
		cfg.MCP.APIKey = v
	}
	if v := os.Getenv("AEGIS_MAX_ROWS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Limits.MaxRows = n
		}
	}
	if v := os.Getenv("AEGIS_QUERY_TIMEOUT"); v != "" {
		cfg.Limits.QueryTimeout = v
	}
	if v := os.Getenv("AEGIS_RATE_PER_MIN"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Limits.RatePerMin = n
		}
	}
	if v := os.Getenv("AEGIS_MAX_AFFECTED_ROWS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Limits.MaxAffectedRows = n
		}
	}
	if v := os.Getenv("AEGIS_ALLOW_NO_WHERE_WRITES"); v != "" {
		cfg.Limits.AllowNoWhere = v == "true" || v == "1"
	}
	if v := os.Getenv("AEGIS_ALERT_DENIED_COUNT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Alerting.DeniedCount = n
		}
	}
	if v := os.Getenv("AEGIS_ALERT_DENIED_WINDOW_SEC"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Alerting.DeniedWindow = n
		}
	}
	if v := os.Getenv("AEGIS_ALERT_BULK_ROWS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Alerting.BulkRows = n
		}
	}
	if v := os.Getenv("AEGIS_ALERT_OFFHOURS"); v != "" {
		cfg.Alerting.OffHoursOn = v == "true" || v == "1"
	}
	if v := os.Getenv("AEGIS_ALERT_OFFHOURS_START"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Alerting.OffHoursStart = n
		}
	}
	if v := os.Getenv("AEGIS_ALERT_OFFHOURS_END"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Alerting.OffHoursEnd = n
		}
	}
	if v := os.Getenv("AEGIS_ALERT_COOLDOWN_SEC"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Alerting.Cooldown = n
		}
	}
	if v := os.Getenv("AEGIS_ALERT_WEBHOOK"); v != "" {
		cfg.Alerting.Webhook = v
	}
	// OIDC environment overrides
	if v := os.Getenv("AEGIS_OIDC_ENABLED"); v != "" {
		cfg.OIDC.Enabled = v == "true" || v == "1"
	}
	if v := os.Getenv("AEGIS_OIDC_ISSUER"); v != "" {
		cfg.OIDC.Issuer = v
	}
	if v := os.Getenv("AEGIS_OIDC_CLIENT_ID"); v != "" {
		cfg.OIDC.ClientID = v
	}
	if v := os.Getenv("AEGIS_OIDC_CLIENT_SECRET"); v != "" {
		cfg.OIDC.ClientSecret = v
	}
	if v := os.Getenv("AEGIS_OIDC_REDIRECT_URL"); v != "" {
		cfg.OIDC.RedirectURL = v
	}
	// NL2SQL environment overrides
	if v := os.Getenv("AEGIS_NL2SQL_ENABLED"); v != "" {
		cfg.NL2SQL.Enabled = v == "true" || v == "1"
	}
	if v := os.Getenv("AEGIS_NL2SQL_BASE_URL"); v != "" {
		cfg.NL2SQL.BaseURL = v
	}
	if v := os.Getenv("AEGIS_NL2SQL_API_KEY"); v != "" {
		cfg.NL2SQL.APIKey = v
	}
	if v := os.Getenv("AEGIS_NL2SQL_MODEL"); v != "" {
		cfg.NL2SQL.Model = v
	}
	if v := os.Getenv("AEGIS_NL2SQL_TIMEOUT_SEC"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.NL2SQL.TimeoutSec = n
		}
	}
	if v := os.Getenv("AEGIS_NL2SQL_MAX_RETRIES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.NL2SQL.MaxRetries = n
		}
	}
	// Logging overrides
	if v := os.Getenv("AEGIS_LOG_FORMAT"); v != "" {
		cfg.Logging.Format = v
	}
	if v := os.Getenv("AEGIS_LOG_LEVEL"); v != "" {
		cfg.Logging.Level = v
	}
	if v := os.Getenv("AEGIS_MASK_SECRET"); v != "" {
		cfg.MaskSecret = v
	}
	// LDAP environment overrides
	if v := os.Getenv("AEGIS_LDAP_ENABLED"); v != "" {
		cfg.LDAP.Enabled = v == "true" || v == "1"
	}
	if v := os.Getenv("AEGIS_LDAP_URL"); v != "" {
		cfg.LDAP.URL = v
	}
	if v := os.Getenv("AEGIS_LDAP_BIND_DN"); v != "" {
		cfg.LDAP.BindDN = v
	}
	if v := os.Getenv("AEGIS_LDAP_BIND_PASSWORD"); v != "" {
		cfg.LDAP.BindPassword = v
	}
	if v := os.Getenv("AEGIS_LDAP_BASE_DN"); v != "" {
		cfg.LDAP.BaseDN = v
	}
	if v := os.Getenv("AEGIS_LDAP_USER_FILTER"); v != "" {
		cfg.LDAP.UserFilter = v
	}
	if v := os.Getenv("AEGIS_LDAP_USER_ATTR"); v != "" {
		cfg.LDAP.UserAttr = v
	}
	if v := os.Getenv("AEGIS_LDAP_DISPLAY_ATTR"); v != "" {
		cfg.LDAP.DisplayAttr = v
	}
	if v := os.Getenv("AEGIS_LDAP_EMAIL_ATTR"); v != "" {
		cfg.LDAP.EmailAttr = v
	}
	if v := os.Getenv("AEGIS_LDAP_GROUP_BASE_DN"); v != "" {
		cfg.LDAP.GroupBaseDN = v
	}
	if v := os.Getenv("AEGIS_LDAP_GROUP_FILTER"); v != "" {
		cfg.LDAP.GroupFilter = v
	}
	if v := os.Getenv("AEGIS_LDAP_GROUP_NAME_ATTR"); v != "" {
		cfg.LDAP.GroupNameAttr = v
	}
	if v := os.Getenv("AEGIS_LDAP_SKIP_TLS_VERIFY"); v != "" {
		cfg.LDAP.SkipTLSVerify = v == "true" || v == "1"
	}
	// Edition / license (open-core tiering, ADR-002)
	if v := os.Getenv("AEGIS_EDITION"); v != "" {
		cfg.Edition = v
	}
	if v := os.Getenv("AEGIS_LICENSE_KEY"); v != "" {
		cfg.LicenseKey = v
	}
	if v := os.Getenv("AEGIS_LICENSE_FILE"); v != "" {
		cfg.LicenseFile = v
	}
}
