package server

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"time"

	"github.com/wisonwang/aegis/internal/alerting"
	"github.com/wisonwang/aegis/internal/api"
	"github.com/wisonwang/aegis/internal/capabilities"
	"github.com/wisonwang/aegis/internal/config"
	"github.com/wisonwang/aegis/internal/datasource"
	"github.com/wisonwang/aegis/internal/enterprise"
	"github.com/wisonwang/aegis/internal/logging"
	"github.com/wisonwang/aegis/internal/mcp"
	"github.com/wisonwang/aegis/internal/metrics"
	"github.com/wisonwang/aegis/internal/nl2sql"
	"github.com/wisonwang/aegis/internal/proxy"
	"github.com/wisonwang/aegis/internal/store"
	"github.com/wisonwang/aegis/internal/version"
)

//go:embed all:web
var webFS embed.FS

// Run starts the Aegis platform: it opens the control-plane store, seeds a
// demo tenant when empty, and serves the DataAPI, admin API/UI and MCP endpoint.
func Run(cfg *config.Config) error {
	// Install the structured logger first so every subsequent record (store
	// open, seed, route serving) is emitted in the chosen format/level.
	logging.Init(logging.Config{Format: cfg.Logging.Format, Level: cfg.Logging.Level})

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	if cfg.SeedDemo {
		if err := seedIfEmpty(st, cfg); err != nil {
			logging.With("error", err.Error()).Warn("demo seed failed")
		}
	}

	// Observability: advertise build identity and live counts at start-up.
	metrics.SetBuildInfo(version.Version, version.Commit)
	if dss, err := st.ListDataSources(context.Background()); err == nil {
		metrics.SetDatasources(len(dss))
	}
	if dsets, err := st.ListDatasets(context.Background()); err == nil {
		n := 0
		for _, d := range dsets {
			if d.Status == store.DatasetPublished {
				n++
			}
		}
		metrics.SetDatasetsPublished(n)
	}

	dm := datasource.NewManager(st)
	px := proxy.New(st, dm)
	px.SetGuard(proxy.NewGuard(cfg.Limits))

	// Install the masking key for keyed strategies (tokenize / fpe). Without a
	// configured secret the proxy uses an insecure development default, which
	// would let anyone with the binary reverse masked values.
	proxy.SetMaskKey(cfg.MaskSecret)
	if cfg.MaskSecret == "" {
		logging.With("component", "mask").Warn("AEGIS_MASK_SECRET not set; keyed masking (tokenize/fpe) uses an insecure development default")
	}
	// Anomaly detection: observe the audit stream and persist security alerts.
	det := alerting.New(alerting.Config{
		DeniedCount:   cfg.Alerting.DeniedCount,
		DeniedWindow:  time.Duration(cfg.Alerting.DeniedWindow) * time.Second,
		BulkRows:      cfg.Alerting.BulkRows,
		OffHoursOn:    cfg.Alerting.OffHoursOn,
		OffHoursStart: cfg.Alerting.OffHoursStart,
		OffHoursEnd:   cfg.Alerting.OffHoursEnd,
		Cooldown:      time.Duration(cfg.Alerting.Cooldown) * time.Second,
		Webhook:       cfg.Alerting.Webhook,
	}, func(a store.SecurityAlert) error {
		return st.InsertSecurityAlert(&a)
	})
	px.SetDetector(det)

	// NL2SQL gateway: install the configured generator (nil when disabled or
	// misconfigured). When set, natural-language questions are turned into
	// governed SQL and executed through the normal path.
	if gen, err := nl2sql.NewGenerator(cfg.NL2SQL); err != nil {
		logging.With("error", err.Error()).Warn("nl2sql disabled")
	} else if gen != nil {
		px.SetNL2SQL(gen)
	}

	h := &api.Handler{Store: st, Proxy: px, DS: dm, Cfg: cfg}

	// OIDC handler (nil when disabled)
	oidcH, err := api.NewOIDCHandler(context.Background(), st, cfg)
	if err != nil {
		logging.With("error", err.Error()).Warn("oidc init failed")
	}

	// LDAP handler (nil when disabled)
	ldapH, err := api.NewLDAPHandler(st, cfg)
	if err != nil {
		logging.With("error", err.Error()).Warn("ldap init failed")
	}

	// Resolve the open-core tier (community by default). A bad license degrades
	// to community and is logged; it never bricks the free tier (ADR-002).
	caps, err := capabilities.New(cfg)
	if err != nil {
		logging.With("error", err.Error()).Warn("license invalid; running community edition")
		caps = capabilities.Community()
	}
	logging.With("edition", string(caps.Edition())).Info("edition resolved")

	mux := http.NewServeMux()
	registerRoutes(mux, h, st, px, cfg, oidcH, ldapH, caps)

	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		return err
	}
	mux.Handle("GET /admin/", http.StripPrefix("/admin/", http.FileServer(http.FS(sub))))
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/", http.StatusFound)
	})

	logging.With("addr", cfg.ListenAddr, "admin", "/admin/", "mcp", cfg.MCP.Path).
		Info("aegis listening")
	srv := &http.Server{Addr: cfg.ListenAddr, Handler: logging.Middleware(mux)}
	return srv.ListenAndServe()
}

func registerRoutes(mux *http.ServeMux, h *api.Handler, st *store.Store, px *proxy.Proxy, cfg *config.Config, oidcH *api.OIDCHandler, ldapH *api.LDAPHandler, caps *capabilities.Capabilities) {
	// ---- Health probes ----
	mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		api.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/v1/ready", func(w http.ResponseWriter, r *http.Request) {
		// Readiness: ensure the control-plane store is reachable.
		if _, err := st.ListDataSources(context.Background()); err != nil {
			api.WriteJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not ready", "reason": err.Error()})
			return
		}
		api.WriteJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	mux.HandleFunc("GET /api/v1/capabilities", func(w http.ResponseWriter, r *http.Request) {
		api.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"edition":      caps.Edition(),
			"capabilities": caps.Strings(),
		})
	})
	mux.Handle("GET /metrics", metrics.Handler())

	// ---- OIDC login flow (optional) ----
	if oidcH != nil {
		mux.HandleFunc("GET /api/v1/auth/oidc/login", oidcH.OIDCLogin)
		mux.HandleFunc("GET /api/v1/auth/oidc/callback", oidcH.OIDCCallback)
	}

	// ---- LDAP login flow (optional, password-based directory SSO) ----
	if ldapH != nil {
		mux.HandleFunc("POST /api/v1/auth/ldap/login", ldapH.LDAPLogin)
	}

	// ---- DataAPI (requires authentication + workspace scoping) ----
	mux.HandleFunc("POST /api/v1/login", h.Login)
	mux.HandleFunc("GET /api/v1/me", api.Authenticate(cfg, h.Me))
	mux.HandleFunc("POST /api/v1/query", api.Authenticate(cfg, api.WorkspaceResolver(st, h.Query)))
	mux.HandleFunc("POST /api/v1/datasources/{id}/query/estimate", api.Authenticate(cfg, api.WorkspaceResolver(st, h.EstimateQuery)))
	mux.HandleFunc("GET /api/v1/datasources", api.Authenticate(cfg, api.WorkspaceResolver(st, h.ListDataSources)))
	mux.HandleFunc("GET /api/v1/datasources/{id}/tables", api.Authenticate(cfg, api.WorkspaceResolver(st, h.ListTables)))
	mux.HandleFunc("GET /api/v1/datasources/{id}/tables/{table}", api.Authenticate(cfg, api.WorkspaceResolver(st, h.DescribeTable)))
	mux.HandleFunc("GET /api/v1/datasources/{id}/catalog", api.Authenticate(cfg, api.WorkspaceResolver(st, h.Catalog)))
	mux.HandleFunc("POST /api/v1/datasources/{id}/nl2sql", api.Authenticate(cfg, api.WorkspaceResolver(st, h.NL2SQL)))

	// The caller's own workspaces (authenticated, no admin required).
	mux.HandleFunc("GET /api/v1/workspaces", api.Authenticate(cfg, h.ListMyWorkspaces))

	// ---- Admin API (admin role only) ----
	// Admin routes also resolve the workspace so platform admins get the
	// cross-workspace ("*") view by default (ADR-001).
	a := func(fn http.HandlerFunc) http.HandlerFunc {
		return api.Authenticate(cfg, api.WorkspaceResolver(st, api.RequireAdmin(fn)))
	}

	mux.HandleFunc("GET /admin/api/users", a(h.AdminListUsers))
	mux.HandleFunc("POST /admin/api/users", a(h.AdminCreateUser))
	mux.HandleFunc("PUT /admin/api/users/{id}", a(h.AdminUpdateUser))
	mux.HandleFunc("POST /admin/api/users/{id}/password", a(h.AdminSetPassword))
	mux.HandleFunc("DELETE /admin/api/users/{id}", a(h.AdminDeleteUser))
	mux.HandleFunc("POST /admin/api/users/{id}/roles", a(h.AdminAddUserRole))
	mux.HandleFunc("DELETE /admin/api/users/{id}/roles/{role}", a(h.AdminRemoveUserRole))

	mux.HandleFunc("GET /admin/api/roles", a(h.AdminListRoles))
	mux.HandleFunc("POST /admin/api/roles", a(h.AdminCreateRole))
	mux.HandleFunc("DELETE /admin/api/roles/{id}", a(h.AdminDeleteRole))

	mux.HandleFunc("GET /admin/api/datasources", a(h.AdminListDataSources))
	mux.HandleFunc("POST /admin/api/datasources", a(h.AdminCreateDataSource))
	mux.HandleFunc("PUT /admin/api/datasources/{id}", a(h.AdminUpdateDataSource))
	mux.HandleFunc("DELETE /admin/api/datasources/{id}", a(h.AdminDeleteDataSource))

	mux.HandleFunc("GET /admin/api/datasources/{id}/tables/{table}/permissions", a(h.AdminListTablePermissions))
	mux.HandleFunc("POST /admin/api/datasources/{id}/tables/{table}/permissions", a(h.AdminCreateTablePermission))
	mux.HandleFunc("DELETE /admin/api/datasources/{id}/permissions/{perm}", a(h.AdminDeleteTablePermission))

	// ---- Workspaces (multi-tenant boundaries, ADR-001) ----
	mux.HandleFunc("POST /admin/api/workspaces", a(h.AdminCreateWorkspace))
	mux.HandleFunc("GET /admin/api/workspaces/{id}", a(h.AdminGetWorkspace))
	mux.HandleFunc("DELETE /admin/api/workspaces/{id}", a(h.AdminDeleteWorkspace))
	mux.HandleFunc("GET /admin/api/workspaces/{id}/members", a(h.AdminListWorkspaceMembers))
	mux.HandleFunc("POST /admin/api/workspaces/{id}/members", a(h.AdminAddWorkspaceMember))
	mux.HandleFunc("DELETE /admin/api/workspaces/{id}/members/{user_id}", a(h.AdminRemoveWorkspaceMember))

	mux.HandleFunc("GET /admin/api/datasources/{id}/tables/{table}/policies", a(h.AdminListRowPolicies))
	mux.HandleFunc("POST /admin/api/datasources/{id}/tables/{table}/policies", a(h.AdminCreateRowPolicy))
	mux.HandleFunc("DELETE /admin/api/datasources/{id}/policies/{policy}", a(h.AdminDeleteRowPolicy))

	mux.HandleFunc("GET /admin/api/audit", a(h.AdminListAudits))
	mux.HandleFunc("GET /admin/api/audit/stats", a(h.AdminAuditStats))

	// ---- Security alerts (anomaly detection) ----
	mux.HandleFunc("GET /admin/api/alerts", a(h.AdminListAlerts))
	mux.HandleFunc("GET /admin/api/alerts/stats", a(h.AdminAlertStats))
	mux.HandleFunc("POST /admin/api/alerts/{id}/resolve", a(h.AdminResolveAlert))

	// ---- Schema semantics (AI data-supply layer) ----
	mux.HandleFunc("GET /admin/api/datasources/{id}/semantics", a(h.AdminListSemantics))
	mux.HandleFunc("POST /admin/api/datasources/{id}/semantics", a(h.AdminUpsertSemantic))
	mux.HandleFunc("DELETE /admin/api/datasources/{id}/semantics/{sem}", a(h.AdminDeleteSemantic))

	// ---- Column masks (dynamic masking / PII supply) ----
	mux.HandleFunc("GET /admin/api/datasources/{id}/masks", a(h.AdminListMasks))
	mux.HandleFunc("POST /admin/api/datasources/{id}/masks", a(h.AdminUpsertMask))
	mux.HandleFunc("DELETE /admin/api/datasources/{id}/masks/{mask}", a(h.AdminDeleteMask))
	mux.HandleFunc("POST /admin/api/datasources/{id}/masks/recommend", a(h.AdminRecommendMasks))

	// ---- Data classification (PII / sensitivity tags) ----
	mux.HandleFunc("GET /admin/api/datasources/{id}/classifications", a(h.AdminListClassifications))
	mux.HandleFunc("POST /admin/api/datasources/{id}/classifications", a(h.AdminUpsertClassification))
	mux.HandleFunc("DELETE /admin/api/datasources/{id}/classifications/{cls}", a(h.AdminDeleteClassification))

	// ---- Enterprise-only routes (gated by capability, ADR-002) ----
	enterprise.Register(mux, cfg, h, caps)

	// ---- MCP endpoint for AI agents ----
	if cfg.MCP.Enabled {
		mcpSrv := mcp.New(px, st, cfg)
		mux.HandleFunc("POST "+cfg.MCP.Path, mcpSrv.Handle)
	}
}
