package server

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"time"

	"github.com/wisonwang/aegis/internal/api"
	"github.com/wisonwang/aegis/internal/alerting"
	"github.com/wisonwang/aegis/internal/config"
	"github.com/wisonwang/aegis/internal/datasource"
	"github.com/wisonwang/aegis/internal/metrics"
	"github.com/wisonwang/aegis/internal/mcp"
	"github.com/wisonwang/aegis/internal/proxy"
	"github.com/wisonwang/aegis/internal/store"
	"github.com/wisonwang/aegis/internal/version"
)

//go:embed all:web
var webFS embed.FS

// Run starts the Aegis platform: it opens the control-plane store, seeds a
// demo tenant when empty, and serves the DataAPI, admin API/UI and MCP endpoint.
func Run(cfg *config.Config) error {
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	if cfg.SeedDemo {
		if err := seedIfEmpty(st, cfg); err != nil {
			log.Printf("warn: demo seed failed: %v", err)
		}
	}

	// Observability: advertise build identity and live counts at start-up.
	metrics.SetBuildInfo(version.Version, version.Commit)
	if dss, err := st.ListDataSources(); err == nil {
		metrics.SetDatasources(len(dss))
	}
	if dsets, err := st.ListDatasets(); err == nil {
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
	h := &api.Handler{Store: st, Proxy: px, DS: dm, Cfg: cfg}

	// OIDC handler (nil when disabled)
	oidcH, err := api.NewOIDCHandler(context.Background(), st, cfg)
	if err != nil {
		log.Printf("warn: oidc init failed: %v", err)
	}

	mux := http.NewServeMux()
	registerRoutes(mux, h, st, px, cfg, oidcH)

	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		return err
	}
	mux.Handle("GET /admin/", http.StripPrefix("/admin/", http.FileServer(http.FS(sub))))
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/", http.StatusFound)
	})

	log.Printf("aegis listening on %s  (admin UI: /admin/, mcp: %s)", cfg.ListenAddr, cfg.MCP.Path)
	srv := &http.Server{Addr: cfg.ListenAddr, Handler: mux}
	return srv.ListenAndServe()
}

func registerRoutes(mux *http.ServeMux, h *api.Handler, st *store.Store, px *proxy.Proxy, cfg *config.Config, oidcH *api.OIDCHandler) {
	// ---- Health probes ----
	mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		api.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/v1/ready", func(w http.ResponseWriter, r *http.Request) {
		// Readiness: ensure the control-plane store is reachable.
		if _, err := st.ListDataSources(); err != nil {
			api.WriteJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not ready", "reason": err.Error()})
			return
		}
		api.WriteJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	mux.Handle("GET /metrics", metrics.Handler())

	// ---- OIDC login flow (optional) ----
	if oidcH != nil {
		mux.HandleFunc("GET /api/v1/auth/oidc/login", oidcH.OIDCLogin)
		mux.HandleFunc("GET /api/v1/auth/oidc/callback", oidcH.OIDCCallback)
	}

	// ---- DataAPI (requires authentication) ----
	mux.HandleFunc("POST /api/v1/login", h.Login)
	mux.HandleFunc("GET /api/v1/me", api.Authenticate(cfg, h.Me))
	mux.HandleFunc("POST /api/v1/query", api.Authenticate(cfg, h.Query))
	mux.HandleFunc("GET /api/v1/datasources", api.Authenticate(cfg, h.ListDataSources))
	mux.HandleFunc("GET /api/v1/datasources/{id}/tables", api.Authenticate(cfg, h.ListTables))
	mux.HandleFunc("GET /api/v1/datasources/{id}/tables/{table}", api.Authenticate(cfg, h.DescribeTable))

	// ---- Datasets (agent-facing consumption) ----
	mux.HandleFunc("GET /api/v1/datasets", api.Authenticate(cfg, h.ListDatasets))
	mux.HandleFunc("GET /api/v1/datasets/{id}", api.Authenticate(cfg, h.GetDataset))
	mux.HandleFunc("POST /api/v1/datasets/{id}/query", api.Authenticate(cfg, h.QueryDataset))

	// ---- Admin API (admin role only) ----
	a := func(fn http.HandlerFunc) http.HandlerFunc { return api.Authenticate(cfg, api.RequireAdmin(fn)) }

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

	// ---- Data classification (PII / sensitivity tags) ----
	mux.HandleFunc("GET /admin/api/datasources/{id}/classifications", a(h.AdminListClassifications))
	mux.HandleFunc("POST /admin/api/datasources/{id}/classifications", a(h.AdminUpsertClassification))
	mux.HandleFunc("DELETE /admin/api/datasources/{id}/classifications/{cls}", a(h.AdminDeleteClassification))

	// ---- Dataset management (curated, governed data products) ----
	mux.HandleFunc("GET /admin/api/datasets", a(h.AdminListDatasets))
	mux.HandleFunc("POST /admin/api/datasets", a(h.AdminCreateDataset))
	mux.HandleFunc("GET /admin/api/datasets/{id}", a(h.AdminGetDataset))
	mux.HandleFunc("PUT /admin/api/datasets/{id}", a(h.AdminUpdateDataset))
	mux.HandleFunc("DELETE /admin/api/datasets/{id}", a(h.AdminDeleteDataset))
	mux.HandleFunc("POST /admin/api/datasets/{id}/publish", a(h.AdminPublishDataset))
	mux.HandleFunc("POST /admin/api/datasets/{id}/unpublish", a(h.AdminUnpublishDataset))
	mux.HandleFunc("GET /admin/api/datasets/{id}/permissions", a(h.AdminListDatasetPermissions))
	mux.HandleFunc("POST /admin/api/datasets/{id}/permissions", a(h.AdminCreateDatasetPermission))
	mux.HandleFunc("DELETE /admin/api/datasets/{id}/permissions/{perm}", a(h.AdminDeleteDatasetPermission))
	mux.HandleFunc("GET /admin/api/datasets/{id}/policies", a(h.AdminListDatasetPolicies))
	mux.HandleFunc("POST /admin/api/datasets/{id}/policies", a(h.AdminCreateDatasetPolicy))
	mux.HandleFunc("DELETE /admin/api/datasets/{id}/policies/{policy}", a(h.AdminDeleteDatasetPolicy))
	mux.HandleFunc("GET /admin/api/datasets/{id}/masks", a(h.AdminListDatasetMasks))
	mux.HandleFunc("POST /admin/api/datasets/{id}/masks", a(h.AdminUpsertDatasetMask))
	mux.HandleFunc("DELETE /admin/api/datasets/{id}/masks/{mask}", a(h.AdminDeleteDatasetMask))
	mux.HandleFunc("GET /admin/api/datasets/{id}/semantics", a(h.AdminListDatasetSemantics))
	mux.HandleFunc("POST /admin/api/datasets/{id}/semantics", a(h.AdminUpsertDatasetSemantic))
	mux.HandleFunc("DELETE /admin/api/datasets/{id}/semantics/{sem}", a(h.AdminDeleteDatasetSemantic))

	// ---- MCP endpoint for AI agents ----
	if cfg.MCP.Enabled {
		mcpSrv := mcp.New(px, st, cfg)
		mux.HandleFunc("POST "+cfg.MCP.Path, mcpSrv.Handle)
	}
}
