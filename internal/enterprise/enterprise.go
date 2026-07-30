// Package enterprise owns the routing surface and access gate for all
// enterprise-only features (ADR-002). It depends on core packages
// (api, capabilities, config) and never the other way around.
//
// Phase 2: the enterprise boundary owns both route registration and the
// runtime handler bodies for enterprise-gated endpoints. Metrics still reuse
// api.Handler for now; datasets and approvals are served by the enterprise
// handler package behind the same capability gate.
package enterprise

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/wisonwang/aegis/internal/api"
	"github.com/wisonwang/aegis/internal/capabilities"
	"github.com/wisonwang/aegis/internal/config"
	enterprisehandlers "github.com/wisonwang/aegis/internal/enterprise/handlers"
	"github.com/wisonwang/aegis/internal/store"
)

// Register wires enterprise-only routes behind the capability gate.
//
// The admin and auth wrappers apply api.WorkspaceResolver so enterprise
// routes obey the same multi-tenant scoping as the core DataAPI (ADR-001):
// a platform admin gets the cross-workspace ("*") view, while non-admins
// are scoped to their own workspace. Without this, dataset/metric/approval
// management would have been pinned to the default workspace.
func Register(engine *gin.Engine, cfg *config.Config, st *store.Store, h *api.Handler, caps *capabilities.Capabilities) {
	eh := enterprisehandlers.New(st, h.Proxy)
	require := func(cap capabilities.Capability) func(http.HandlerFunc) http.HandlerFunc {
		return requireCap(caps, cap)
	}
	auth := func(fn http.HandlerFunc) http.HandlerFunc {
		return api.Authenticate(st, cfg, api.WorkspaceResolver(st, fn))
	}
	admin := func(fn http.HandlerFunc) http.HandlerFunc {
		return api.Authenticate(st, cfg, api.WorkspaceResolver(st, api.RequireAdmin(fn)))
	}

	// ---- Datasets (data products) : CapDataProducts ----
	engine.GET("/api/v1/datasets", gin.WrapF(auth(require(capabilities.CapDataProducts)(eh.ListDatasets))))
	engine.GET("/api/v1/datasets/:id", gin.WrapF(auth(require(capabilities.CapDataProducts)(eh.GetDataset))))
	engine.POST("/api/v1/datasets/:id/query", gin.WrapF(auth(require(capabilities.CapDataProducts)(eh.QueryDataset))))

	engine.GET("/admin/api/datasets", gin.WrapF(admin(require(capabilities.CapDataProducts)(eh.AdminListDatasets))))
	engine.POST("/admin/api/datasets", gin.WrapF(admin(require(capabilities.CapDataProducts)(eh.AdminCreateDataset))))
	engine.GET("/admin/api/datasets/:id", gin.WrapF(admin(require(capabilities.CapDataProducts)(eh.AdminGetDataset))))
	engine.PUT("/admin/api/datasets/:id", gin.WrapF(admin(require(capabilities.CapDataProducts)(eh.AdminUpdateDataset))))
	engine.DELETE("/admin/api/datasets/:id", gin.WrapF(admin(require(capabilities.CapDataProducts)(eh.AdminDeleteDataset))))
	engine.POST("/admin/api/datasets/:id/publish", gin.WrapF(admin(require(capabilities.CapDataProducts)(eh.AdminPublishDataset))))
	engine.POST("/admin/api/datasets/:id/unpublish", gin.WrapF(admin(require(capabilities.CapDataProducts)(eh.AdminUnpublishDataset))))
	engine.GET("/admin/api/datasets/:id/permissions", gin.WrapF(admin(require(capabilities.CapDataProducts)(eh.AdminListDatasetPermissions))))
	engine.POST("/admin/api/datasets/:id/permissions", gin.WrapF(admin(require(capabilities.CapDataProducts)(eh.AdminCreateDatasetPermission))))
	engine.DELETE("/admin/api/datasets/:id/permissions/:perm", gin.WrapF(admin(require(capabilities.CapDataProducts)(eh.AdminDeleteDatasetPermission))))
	engine.GET("/admin/api/datasets/:id/policies", gin.WrapF(admin(require(capabilities.CapDataProducts)(eh.AdminListDatasetPolicies))))
	engine.POST("/admin/api/datasets/:id/policies", gin.WrapF(admin(require(capabilities.CapDataProducts)(eh.AdminCreateDatasetPolicy))))
	engine.DELETE("/admin/api/datasets/:id/policies/:policy", gin.WrapF(admin(require(capabilities.CapDataProducts)(eh.AdminDeleteDatasetPolicy))))
	engine.GET("/admin/api/datasets/:id/masks", gin.WrapF(admin(require(capabilities.CapDataProducts)(eh.AdminListDatasetMasks))))
	engine.POST("/admin/api/datasets/:id/masks", gin.WrapF(admin(require(capabilities.CapDataProducts)(eh.AdminUpsertDatasetMask))))
	engine.DELETE("/admin/api/datasets/:id/masks/:mask", gin.WrapF(admin(require(capabilities.CapDataProducts)(eh.AdminDeleteDatasetMask))))
	engine.GET("/admin/api/datasets/:id/semantics", gin.WrapF(admin(require(capabilities.CapDataProducts)(eh.AdminListDatasetSemantics))))
	engine.POST("/admin/api/datasets/:id/semantics", gin.WrapF(admin(require(capabilities.CapDataProducts)(eh.AdminUpsertDatasetSemantic))))
	engine.DELETE("/admin/api/datasets/:id/semantics/:sem", gin.WrapF(admin(require(capabilities.CapDataProducts)(eh.AdminDeleteDatasetSemantic))))

	// ---- Dataset catalog folders (hierarchical organization) : CapDataProducts ----
	// Consumer reads the folder tree to render the collapsible data catalog.
	engine.GET("/api/v1/dataset-folders", gin.WrapF(auth(require(capabilities.CapDataProducts)(eh.ListFolders))))
	// Admin manages the folder tree (CRUD) and moves datasets between folders.
	engine.GET("/admin/api/dataset-folders", gin.WrapF(admin(require(capabilities.CapDataProducts)(eh.AdminListFolders))))
	engine.POST("/admin/api/dataset-folders", gin.WrapF(admin(require(capabilities.CapDataProducts)(eh.AdminCreateFolder))))
	engine.PUT("/admin/api/dataset-folders/:id", gin.WrapF(admin(require(capabilities.CapDataProducts)(eh.AdminUpdateFolder))))
	engine.DELETE("/admin/api/dataset-folders/:id", gin.WrapF(admin(require(capabilities.CapDataProducts)(eh.AdminDeleteFolder))))
	engine.POST("/admin/api/datasets/:id/move", gin.WrapF(admin(require(capabilities.CapDataProducts)(eh.AdminMoveDataset))))

	// ---- Semantic metric layer (part of data products) : CapDataProducts ----
	engine.GET("/api/v1/datasources/:id/metrics", gin.WrapF(auth(require(capabilities.CapDataProducts)(h.ListMetrics))))
	engine.POST("/api/v1/datasources/:id/metrics/:name/run", gin.WrapF(auth(require(capabilities.CapDataProducts)(h.RunMetric))))
	engine.GET("/admin/api/datasources/:id/metrics", gin.WrapF(admin(require(capabilities.CapDataProducts)(h.AdminListMetrics))))
	engine.POST("/admin/api/datasources/:id/metrics", gin.WrapF(admin(require(capabilities.CapDataProducts)(h.AdminUpsertMetric))))
	engine.DELETE("/admin/api/datasources/:id/metrics/:mid", gin.WrapF(admin(require(capabilities.CapDataProducts)(h.AdminDeleteMetric))))

	// ---- Access approval workflow : CapApprovalWorkflow ----
	engine.GET("/api/v1/me/approvals", gin.WrapF(auth(require(capabilities.CapApprovalWorkflow)(eh.UserListMyApprovals))))
	engine.POST("/admin/api/approvals", gin.WrapF(auth(require(capabilities.CapApprovalWorkflow)(eh.UserSubmitApproval))))
	engine.GET("/admin/api/approvals", gin.WrapF(admin(require(capabilities.CapApprovalWorkflow)(eh.AdminListApprovals))))
	engine.POST("/admin/api/approvals/:id/approve", gin.WrapF(admin(require(capabilities.CapApprovalWorkflow)(eh.AdminApproveApproval))))
	engine.POST("/admin/api/approvals/:id/reject", gin.WrapF(admin(require(capabilities.CapApprovalWorkflow)(eh.AdminRejectApproval))))
	engine.POST("/admin/api/approvals/:id/revoke", gin.WrapF(admin(require(capabilities.CapApprovalWorkflow)(eh.AdminRevokeApproval))))
}

// requireCap wraps a handler so it only runs when the capability is entitled.
// Missing entitlement returns 402 Payment Required with a structured body.
func requireCap(caps *capabilities.Capabilities, cap capabilities.Capability) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !caps.Has(cap) {
				api.WriteJSON(w, http.StatusPaymentRequired, map[string]interface{}{
					"error":       "enterprise feature requires a valid license",
					"capability":  string(cap),
					"edition":     string(caps.Edition()),
					"upgrade_url": "https://github.com/wisonwang/Aegis#enterprise",
				})
				return
			}
			next(w, r)
		}
	}
}
