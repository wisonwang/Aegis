package server

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
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

	ginSwagger "github.com/swaggo/gin-swagger"
	swaggerFiles "github.com/swaggo/files"

	_ "github.com/wisonwang/aegis/internal/apidoc" // swag-generated OpenAPI spec (swag init)
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

	engine := gin.New()
	engine.Use(gin.Recovery())

	registerRoutes(engine, h, st, px, cfg, oidcH, ldapH, caps)

	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		return err
	}
	// Serve the embedded admin UI from /admin/ for real files, falling
	// through to the registered API routes otherwise (so /admin/api/* and
	// /admin/api/docs/* are never shadowed by a catch-all static route).
	engine.Use(staticWebMiddleware(http.FS(sub)))
	engine.GET("/", func(c *gin.Context) {
		c.Redirect(http.StatusFound, "/admin/")
	})

	logging.With("addr", cfg.ListenAddr, "admin", "/admin/", "mcp", cfg.MCP.Path).
		Info("aegis listening")
	srv := &http.Server{Addr: cfg.ListenAddr, Handler: logging.Middleware(engine)}
	return srv.ListenAndServe()
}

func registerRoutes(engine *gin.Engine, h *api.Handler, st *store.Store, px *proxy.Proxy, cfg *config.Config, oidcH *api.OIDCHandler, ldapH *api.LDAPHandler, caps *capabilities.Capabilities) {
	// Mirror every gin path parameter into the request context so the
	// net/http handlers (which still read pathParam(r, "id")) keep working
	// unchanged under the gin router.
	engine.Use(withPathParams())

	// ---- Health probes ----
	engine.GET("/api/v1/health", gin.WrapF(func(w http.ResponseWriter, r *http.Request) {
		api.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}))
	engine.GET("/api/v1/ready", gin.WrapF(func(w http.ResponseWriter, r *http.Request) {
		// Readiness: ensure the control-plane store is reachable.
		if _, err := st.ListDataSources(context.Background()); err != nil {
			api.WriteJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not ready", "reason": err.Error()})
			return
		}
		api.WriteJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	}))
	engine.GET("/api/v1/capabilities", gin.WrapF(func(w http.ResponseWriter, r *http.Request) {
		api.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"edition":      caps.Edition(),
			"capabilities": caps.Strings(),
		})
	}))
	engine.GET("/metrics", gin.WrapH(metrics.Handler()))

	// ---- Auto-extracted API docs (swaggo; spec generated by `swag init`) ----
	// GIN's catch-all *any conflicts with a trailing-slash literal, so the
	// bare /admin/api/docs (no slash) issues the redirect and the catch-all
	// additionally forwards a bare "/admin/api/docs/" to the UI entry point.
	engine.GET("/admin/api/docs", func(c *gin.Context) {
		c.Redirect(http.StatusFound, "/admin/api/docs/index.html")
	})
	engine.GET("/admin/api/docs/*any", func(c *gin.Context) {
		if c.Param("any") == "/" {
			c.Redirect(http.StatusFound, "/admin/api/docs/index.html")
			return
		}
		ginSwagger.WrapHandler(swaggerFiles.Handler)(c)
	})

	// ---- OIDC login flow (optional) ----
	if oidcH != nil {
		engine.GET("/api/v1/auth/oidc/login", gin.WrapF(oidcH.OIDCLogin))
		engine.GET("/api/v1/auth/oidc/callback", gin.WrapF(oidcH.OIDCCallback))
	}

	// ---- LDAP login flow (optional, password-based directory SSO) ----
	if ldapH != nil {
		engine.POST("/api/v1/auth/ldap/login", gin.WrapF(ldapH.LDAPLogin))
	}

	// ---- DataAPI (requires authentication + workspace scoping) ----
	engine.POST("/api/v1/login", gin.WrapF(h.Login))
	engine.GET("/api/v1/me", gin.WrapF(api.Authenticate(cfg, h.Me)))
	engine.POST("/api/v1/query", gin.WrapF(api.Authenticate(cfg, api.WorkspaceResolver(st, h.Query))))
	engine.POST("/api/v1/datasources/:id/query/estimate", gin.WrapF(api.Authenticate(cfg, api.WorkspaceResolver(st, h.EstimateQuery))))
	engine.GET("/api/v1/datasources", gin.WrapF(api.Authenticate(cfg, api.WorkspaceResolver(st, h.ListDataSources))))
	engine.GET("/api/v1/datasources/:id/tables", gin.WrapF(api.Authenticate(cfg, api.WorkspaceResolver(st, h.ListTables))))
	engine.GET("/api/v1/datasources/:id/tables/:table", gin.WrapF(api.Authenticate(cfg, api.WorkspaceResolver(st, h.DescribeTable))))
	engine.GET("/api/v1/datasources/:id/catalog", gin.WrapF(api.Authenticate(cfg, api.WorkspaceResolver(st, h.Catalog))))
	engine.POST("/api/v1/datasources/:id/nl2sql", gin.WrapF(api.Authenticate(cfg, api.WorkspaceResolver(st, h.NL2SQL))))

	// The caller's own workspaces (authenticated, no admin required).
	engine.GET("/api/v1/workspaces", gin.WrapF(api.Authenticate(cfg, h.ListMyWorkspaces)))

	// ---- Admin API (admin role only) ----
	// Admin routes also resolve the workspace so platform admins get the
	// cross-workspace ("*") view by default (ADR-001).
	a := func(fn http.HandlerFunc) http.HandlerFunc {
		return api.Authenticate(cfg, api.WorkspaceResolver(st, api.RequireAdmin(fn)))
	}

	engine.GET("/admin/api/users", gin.WrapF(a(h.AdminListUsers)))
	engine.POST("/admin/api/users", gin.WrapF(a(h.AdminCreateUser)))
	engine.PUT("/admin/api/users/:id", gin.WrapF(a(h.AdminUpdateUser)))
	engine.POST("/admin/api/users/:id/password", gin.WrapF(a(h.AdminSetPassword)))
	engine.DELETE("/admin/api/users/:id", gin.WrapF(a(h.AdminDeleteUser)))
	engine.POST("/admin/api/users/:id/roles", gin.WrapF(a(h.AdminAddUserRole)))
	engine.DELETE("/admin/api/users/:id/roles/:role", gin.WrapF(a(h.AdminRemoveUserRole)))

	engine.GET("/admin/api/roles", gin.WrapF(a(h.AdminListRoles)))
	engine.POST("/admin/api/roles", gin.WrapF(a(h.AdminCreateRole)))
	engine.DELETE("/admin/api/roles/:id", gin.WrapF(a(h.AdminDeleteRole)))

	engine.GET("/admin/api/datasources", gin.WrapF(a(h.AdminListDataSources)))
	engine.POST("/admin/api/datasources", gin.WrapF(a(h.AdminCreateDataSource)))
	engine.PUT("/admin/api/datasources/:id", gin.WrapF(a(h.AdminUpdateDataSource)))
	engine.DELETE("/admin/api/datasources/:id", gin.WrapF(a(h.AdminDeleteDataSource)))

	engine.GET("/admin/api/datasources/:id/tables/:table/permissions", gin.WrapF(a(h.AdminListTablePermissions)))
	engine.POST("/admin/api/datasources/:id/tables/:table/permissions", gin.WrapF(a(h.AdminCreateTablePermission)))
	engine.DELETE("/admin/api/datasources/:id/permissions/:perm", gin.WrapF(a(h.AdminDeleteTablePermission)))

	// ---- Workspaces (multi-tenant boundaries, ADR-001) ----
	engine.POST("/admin/api/workspaces", gin.WrapF(a(h.AdminCreateWorkspace)))
	engine.GET("/admin/api/workspaces/:id", gin.WrapF(a(h.AdminGetWorkspace)))
	engine.DELETE("/admin/api/workspaces/:id", gin.WrapF(a(h.AdminDeleteWorkspace)))
	engine.GET("/admin/api/workspaces/:id/members", gin.WrapF(a(h.AdminListWorkspaceMembers)))
	engine.POST("/admin/api/workspaces/:id/members", gin.WrapF(a(h.AdminAddWorkspaceMember)))
	engine.DELETE("/admin/api/workspaces/:id/members/:user_id", gin.WrapF(a(h.AdminRemoveWorkspaceMember)))

	engine.GET("/admin/api/datasources/:id/tables/:table/policies", gin.WrapF(a(h.AdminListRowPolicies)))
	engine.POST("/admin/api/datasources/:id/tables/:table/policies", gin.WrapF(a(h.AdminCreateRowPolicy)))
	engine.DELETE("/admin/api/datasources/:id/policies/:policy", gin.WrapF(a(h.AdminDeleteRowPolicy)))

	engine.GET("/admin/api/audit", gin.WrapF(a(h.AdminListAudits)))
	engine.GET("/admin/api/audit/stats", gin.WrapF(a(h.AdminAuditStats)))

	// ---- Security alerts (anomaly detection) ----
	engine.GET("/admin/api/alerts", gin.WrapF(a(h.AdminListAlerts)))
	engine.GET("/admin/api/alerts/stats", gin.WrapF(a(h.AdminAlertStats)))
	engine.POST("/admin/api/alerts/:id/resolve", gin.WrapF(a(h.AdminResolveAlert)))

	// ---- Schema semantics (AI data-supply layer) ----
	engine.GET("/admin/api/datasources/:id/semantics", gin.WrapF(a(h.AdminListSemantics)))
	engine.POST("/admin/api/datasources/:id/semantics", gin.WrapF(a(h.AdminUpsertSemantic)))
	engine.DELETE("/admin/api/datasources/:id/semantics/:sem", gin.WrapF(a(h.AdminDeleteSemantic)))

	// ---- Column masks (dynamic masking / PII supply) ----
	engine.GET("/admin/api/datasources/:id/masks", gin.WrapF(a(h.AdminListMasks)))
	engine.POST("/admin/api/datasources/:id/masks", gin.WrapF(a(h.AdminUpsertMask)))
	engine.DELETE("/admin/api/datasources/:id/masks/:mask", gin.WrapF(a(h.AdminDeleteMask)))
	engine.POST("/admin/api/datasources/:id/masks/recommend", gin.WrapF(a(h.AdminRecommendMasks)))

	// ---- Data classification (PII / sensitivity tags) ----
	engine.GET("/admin/api/datasources/:id/classifications", gin.WrapF(a(h.AdminListClassifications)))
	engine.POST("/admin/api/datasources/:id/classifications", gin.WrapF(a(h.AdminUpsertClassification)))
	engine.DELETE("/admin/api/datasources/:id/classifications/:cls", gin.WrapF(a(h.AdminDeleteClassification)))

	// ---- Enterprise-only routes (gated by capability, ADR-002) ----
	enterprise.Register(engine, cfg, st, h, caps)

	// ---- MCP endpoint for AI agents ----
	if cfg.MCP.Enabled {
		mcpSrv := mcp.New(px, st, cfg)
		engine.POST(cfg.MCP.Path, gin.WrapF(mcpSrv.Handle))
	}
}

// withPathParams mirrors gin's route parameters into the request context so
// the existing net/http handlers (which read pathParam(r, "id")) keep working
// without modification under the gin router.
func withPathParams() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		for _, p := range c.Params {
			ctx = api.WithPathParam(ctx, p.Key, p.Value)
		}
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

// staticWebMiddleware serves the embedded admin UI from /admin/ when the
// requested path maps to a real file, and otherwise falls through to the
// registered API routes. This avoids the catch-all static route that would
// otherwise shadow /admin/api/* and /admin/api/docs/* (ADR-001 swagger UI).
func staticWebMiddleware(fs http.FileSystem) gin.HandlerFunc {
	strip := http.StripPrefix("/admin/", http.FileServer(fs))
	return func(c *gin.Context) {
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			c.Next()
			return
		}
		if !strings.HasPrefix(c.Request.URL.Path, "/admin/") {
			c.Next()
			return
		}
		rel := strings.TrimPrefix(c.Request.URL.Path, "/admin/")
		if rel == "" {
			rel = "index.html"
		}
		f, err := fs.Open(rel)
		if err != nil {
			c.Next()
			return
		}
		f.Close()
		strip.ServeHTTP(c.Writer, c.Request)
		c.Abort()
	}
}
