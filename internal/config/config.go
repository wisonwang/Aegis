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
	ListenAddr string    `json:"listen_addr"` // HTTP listen address, e.g. ":8080"
	JWTSecret  string    `json:"jwt_secret"`  // HMAC secret for token signing
	JWTExpiry  string    `json:"jwt_expiry"`  // token TTL, e.g. "24h"
	DBPath     string    `json:"db_path"`     // SQLite path for the control plane store
	DataDir    string    `json:"data_dir"`    // directory for demo/seeded database files
	SeedDemo   bool      `json:"seed_demo"`   // seed demo datasource + users on first run
	MCP        MCPConfig `json:"mcp"`
	Limits     Limits    `json:"limits"`
}

// Limits governs AI/agent query behavior: result caps, timeouts and rate
// limiting. Zero values fall back to platform defaults; admin can be exempted.
type Limits struct {
	MaxRows         int    `json:"max_rows"`          // max result rows per query (0 = default 1000)
	QueryTimeout    string `json:"query_timeout"`     // per-query timeout, e.g. "30s" (0/"" = default 30s)
	RatePerMin      int    `json:"rate_per_min"`      // max queries per principal per minute (0 = default 120)
	AdminExempt     bool   `json:"admin_exempt"`      // admin bypasses limits (default true via Default())
	MaxAffectedRows int    `json:"max_affected_rows"` // max rows a single UPDATE/DELETE may touch (0 = no cap)
	AllowNoWhere    bool   `json:"allow_no_where_writes"` // permit UPDATE/DELETE without WHERE (unsafe; default false => blocked)
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
}
