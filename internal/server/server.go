package server

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"

	"github.com/fosun/aegis/internal/api"
	"github.com/fosun/aegis/internal/config"
	"github.com/fosun/aegis/internal/datasource"
	"github.com/fosun/aegis/internal/mcp"
	"github.com/fosun/aegis/internal/proxy"
	"github.com/fosun/aegis/internal/store"
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

	dm := datasource.NewManager(st)
	px := proxy.New(st, dm)
	px.SetGuard(proxy.NewGuard(cfg.Limits))
	h := &api.Handler{Store: st, Proxy: px, DS: dm, Cfg: cfg}

	mux := http.NewServeMux()
	registerRoutes(mux, h, st, px, cfg)

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

func registerRoutes(mux *http.ServeMux, h *api.Handler, st *store.Store, px *proxy.Proxy, cfg *config.Config) {
	// ---- DataAPI (requires authentication) ----
	mux.HandleFunc("POST /api/v1/login", h.Login)
	mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		api.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/v1/me", api.Authenticate(cfg, h.Me))
	mux.HandleFunc("POST /api/v1/query", api.Authenticate(cfg, h.Query))
	mux.HandleFunc("GET /api/v1/datasources", api.Authenticate(cfg, h.ListDataSources))
	mux.HandleFunc("GET /api/v1/datasources/{id}/tables", api.Authenticate(cfg, h.ListTables))
	mux.HandleFunc("GET /api/v1/datasources/{id}/tables/{table}", api.Authenticate(cfg, h.DescribeTable))

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

	// ---- Schema semantics (AI data-supply layer) ----
	mux.HandleFunc("GET /admin/api/datasources/{id}/semantics", a(h.AdminListSemantics))
	mux.HandleFunc("POST /admin/api/datasources/{id}/semantics", a(h.AdminUpsertSemantic))
	mux.HandleFunc("DELETE /admin/api/datasources/{id}/semantics/{sem}", a(h.AdminDeleteSemantic))

	// ---- MCP endpoint for AI agents ----
	if cfg.MCP.Enabled {
		mcpSrv := mcp.New(px, st, cfg)
		mux.HandleFunc("POST "+cfg.MCP.Path, mcpSrv.Handle)
	}
}
