// Package enterprise owns the routing surface and access gate for all
// enterprise-only features (ADR-002). It depends on core packages
// (api, capabilities, config) and never the other way around.
//
// Phase 1 (this file): the enterprise boundary is the *route registration*
// plus the capability gate. Handler method bodies still live in internal/api
// (e.g. api.Handler.AdminListDatasets) and are wired in here; Phase 2 will
// physically relocate those bodies into this package. The gate is the hard
// enforcement; the single decision point is capabilities.Capabilities.Has.
package enterprise

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/wisonwang/aegis/internal/api"
	"github.com/wisonwang/aegis/internal/capabilities"
	"github.com/wisonwang/aegis/internal/config"
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
	require := func(cap capabilities.Capability) func(http.HandlerFunc) http.HandlerFunc {
		return requireCap(caps, cap)
	}
	auth := func(fn http.HandlerFunc) http.HandlerFunc { return api.Authenticate(cfg, api.WorkspaceResolver(st, fn)) }
	admin := func(fn http.HandlerFunc) http.HandlerFunc {
		return api.Authenticate(cfg, api.WorkspaceResolver(st, api.RequireAdmin(fn)))
	}

	// ---- Datasets (data products) : CapDataProducts ----
	engine.GET("/api/v1/datasets", gin.WrapF(auth(require(capabilities.CapDataProducts)(h.ListDatasets))))
	engine.GET("/api/v1/datasets/:id", gin.WrapF(auth(require(capabilities.CapDataProducts)(h.GetDataset))))
	engine.POST("/api/v1/datasets/:id/query", gin.WrapF(auth(require(capabilities.CapDataProducts)(h.QueryDataset))))

	engine.GET("/admin/api/datasets", gin.WrapF(admin(require(capabilities.CapDataProducts)(h.AdminListDatasets))))
	engine.POST("/admin/api/datasets", gin.WrapF(admin(require(capabilities.CapDataProducts)(h.AdminCreateDataset))))
	engine.GET("/admin/api/datasets/:id", gin.WrapF(admin(require(capabilities.CapDataProducts)(h.AdminGetDataset))))
	engine.PUT("/admin/api/datasets/:id", gin.WrapF(admin(require(capabilities.CapDataProducts)(h.AdminUpdateDataset))))
	engine.DELETE("/admin/api/datasets/:id", gin.WrapF(admin(require(capabilities.CapDataProducts)(h.AdminDeleteDataset))))
	engine.POST("/admin/api/datasets/:id/publish", gin.WrapF(admin(require(capabilities.CapDataProducts)(h.AdminPublishDataset))))
	engine.POST("/admin/api/datasets/:id/unpublish", gin.WrapF(admin(require(capabilities.CapDataProducts)(h.AdminUnpublishDataset))))
	engine.GET("/admin/api/datasets/:id/permissions", gin.WrapF(admin(require(capabilities.CapDataProducts)(h.AdminListDatasetPermissions))))
	engine.POST("/admin/api/datasets/:id/permissions", gin.WrapF(admin(require(capabilities.CapDataProducts)(h.AdminCreateDatasetPermission))))
	engine.DELETE("/admin/api/datasets/:id/permissions/:perm", gin.WrapF(admin(require(capabilities.CapDataProducts)(h.AdminDeleteDatasetPermission))))
	engine.GET("/admin/api/datasets/:id/policies", gin.WrapF(admin(require(capabilities.CapDataProducts)(h.AdminListDatasetPolicies))))
	engine.POST("/admin/api/datasets/:id/policies", gin.WrapF(admin(require(capabilities.CapDataProducts)(h.AdminCreateDatasetPolicy))))
	engine.DELETE("/admin/api/datasets/:id/policies/:policy", gin.WrapF(admin(require(capabilities.CapDataProducts)(h.AdminDeleteDatasetPolicy))))
	engine.GET("/admin/api/datasets/:id/masks", gin.WrapF(admin(require(capabilities.CapDataProducts)(h.AdminListDatasetMasks))))
	engine.POST("/admin/api/datasets/:id/masks", gin.WrapF(admin(require(capabilities.CapDataProducts)(h.AdminUpsertDatasetMask))))
	engine.DELETE("/admin/api/datasets/:id/masks/:mask", gin.WrapF(admin(require(capabilities.CapDataProducts)(h.AdminDeleteDatasetMask))))
	engine.GET("/admin/api/datasets/:id/semantics", gin.WrapF(admin(require(capabilities.CapDataProducts)(h.AdminListDatasetSemantics))))
	engine.POST("/admin/api/datasets/:id/semantics", gin.WrapF(admin(require(capabilities.CapDataProducts)(h.AdminUpsertDatasetSemantic))))
	engine.DELETE("/admin/api/datasets/:id/semantics/:sem", gin.WrapF(admin(require(capabilities.CapDataProducts)(h.AdminDeleteDatasetSemantic))))

	// ---- Semantic metric layer (part of data products) : CapDataProducts ----
	engine.GET("/api/v1/datasources/:id/metrics", gin.WrapF(auth(require(capabilities.CapDataProducts)(h.ListMetrics))))
	engine.POST("/api/v1/datasources/:id/metrics/:name/run", gin.WrapF(auth(require(capabilities.CapDataProducts)(h.RunMetric))))
	engine.GET("/admin/api/datasources/:id/metrics", gin.WrapF(admin(require(capabilities.CapDataProducts)(h.AdminListMetrics))))
	engine.POST("/admin/api/datasources/:id/metrics", gin.WrapF(admin(require(capabilities.CapDataProducts)(h.AdminUpsertMetric))))
	engine.DELETE("/admin/api/datasources/:id/metrics/:mid", gin.WrapF(admin(require(capabilities.CapDataProducts)(h.AdminDeleteMetric))))

	// ---- Access approval workflow : CapApprovalWorkflow ----
	engine.GET("/api/v1/me/approvals", gin.WrapF(auth(require(capabilities.CapApprovalWorkflow)(h.UserListMyApprovals))))
	engine.POST("/admin/api/approvals", gin.WrapF(auth(require(capabilities.CapApprovalWorkflow)(h.UserSubmitApproval))))
	engine.GET("/admin/api/approvals", gin.WrapF(admin(require(capabilities.CapApprovalWorkflow)(h.AdminListApprovals))))
	engine.POST("/admin/api/approvals/:id/approve", gin.WrapF(admin(require(capabilities.CapApprovalWorkflow)(h.AdminApproveApproval))))
	engine.POST("/admin/api/approvals/:id/reject", gin.WrapF(admin(require(capabilities.CapApprovalWorkflow)(h.AdminRejectApproval))))
	engine.POST("/admin/api/approvals/:id/revoke", gin.WrapF(admin(require(capabilities.CapApprovalWorkflow)(h.AdminRevokeApproval))))
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
