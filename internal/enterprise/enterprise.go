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
func Register(mux *http.ServeMux, cfg *config.Config, st *store.Store, h *api.Handler, caps *capabilities.Capabilities) {
	require := func(cap capabilities.Capability) func(http.HandlerFunc) http.HandlerFunc {
		return requireCap(caps, cap)
	}
	auth := func(fn http.HandlerFunc) http.HandlerFunc { return api.Authenticate(cfg, api.WorkspaceResolver(st, fn)) }
	admin := func(fn http.HandlerFunc) http.HandlerFunc {
		return api.Authenticate(cfg, api.WorkspaceResolver(st, api.RequireAdmin(fn)))
	}

	// ---- Datasets (data products) : CapDataProducts ----
	mux.HandleFunc("GET /api/v1/datasets", auth(require(capabilities.CapDataProducts)(h.ListDatasets)))
	mux.HandleFunc("GET /api/v1/datasets/{id}", auth(require(capabilities.CapDataProducts)(h.GetDataset)))
	mux.HandleFunc("POST /api/v1/datasets/{id}/query", auth(require(capabilities.CapDataProducts)(h.QueryDataset)))

	mux.HandleFunc("GET /admin/api/datasets", admin(require(capabilities.CapDataProducts)(h.AdminListDatasets)))
	mux.HandleFunc("POST /admin/api/datasets", admin(require(capabilities.CapDataProducts)(h.AdminCreateDataset)))
	mux.HandleFunc("GET /admin/api/datasets/{id}", admin(require(capabilities.CapDataProducts)(h.AdminGetDataset)))
	mux.HandleFunc("PUT /admin/api/datasets/{id}", admin(require(capabilities.CapDataProducts)(h.AdminUpdateDataset)))
	mux.HandleFunc("DELETE /admin/api/datasets/{id}", admin(require(capabilities.CapDataProducts)(h.AdminDeleteDataset)))
	mux.HandleFunc("POST /admin/api/datasets/{id}/publish", admin(require(capabilities.CapDataProducts)(h.AdminPublishDataset)))
	mux.HandleFunc("POST /admin/api/datasets/{id}/unpublish", admin(require(capabilities.CapDataProducts)(h.AdminUnpublishDataset)))
	mux.HandleFunc("GET /admin/api/datasets/{id}/permissions", admin(require(capabilities.CapDataProducts)(h.AdminListDatasetPermissions)))
	mux.HandleFunc("POST /admin/api/datasets/{id}/permissions", admin(require(capabilities.CapDataProducts)(h.AdminCreateDatasetPermission)))
	mux.HandleFunc("DELETE /admin/api/datasets/{id}/permissions/{perm}", admin(require(capabilities.CapDataProducts)(h.AdminDeleteDatasetPermission)))
	mux.HandleFunc("GET /admin/api/datasets/{id}/policies", admin(require(capabilities.CapDataProducts)(h.AdminListDatasetPolicies)))
	mux.HandleFunc("POST /admin/api/datasets/{id}/policies", admin(require(capabilities.CapDataProducts)(h.AdminCreateDatasetPolicy)))
	mux.HandleFunc("DELETE /admin/api/datasets/{id}/policies/{policy}", admin(require(capabilities.CapDataProducts)(h.AdminDeleteDatasetPolicy)))
	mux.HandleFunc("GET /admin/api/datasets/{id}/masks", admin(require(capabilities.CapDataProducts)(h.AdminListDatasetMasks)))
	mux.HandleFunc("POST /admin/api/datasets/{id}/masks", admin(require(capabilities.CapDataProducts)(h.AdminUpsertDatasetMask)))
	mux.HandleFunc("DELETE /admin/api/datasets/{id}/masks/{mask}", admin(require(capabilities.CapDataProducts)(h.AdminDeleteDatasetMask)))
	mux.HandleFunc("GET /admin/api/datasets/{id}/semantics", admin(require(capabilities.CapDataProducts)(h.AdminListDatasetSemantics)))
	mux.HandleFunc("POST /admin/api/datasets/{id}/semantics", admin(require(capabilities.CapDataProducts)(h.AdminUpsertDatasetSemantic)))
	mux.HandleFunc("DELETE /admin/api/datasets/{id}/semantics/{sem}", admin(require(capabilities.CapDataProducts)(h.AdminDeleteDatasetSemantic)))

	// ---- Semantic metric layer (part of data products) : CapDataProducts ----
	mux.HandleFunc("GET /api/v1/datasources/{id}/metrics", auth(require(capabilities.CapDataProducts)(h.ListMetrics)))
	mux.HandleFunc("POST /api/v1/datasources/{id}/metrics/{name}/run", auth(require(capabilities.CapDataProducts)(h.RunMetric)))
	mux.HandleFunc("GET /admin/api/datasources/{id}/metrics", admin(require(capabilities.CapDataProducts)(h.AdminListMetrics)))
	mux.HandleFunc("POST /admin/api/datasources/{id}/metrics", admin(require(capabilities.CapDataProducts)(h.AdminUpsertMetric)))
	mux.HandleFunc("DELETE /admin/api/datasources/{id}/metrics/{mid}", admin(require(capabilities.CapDataProducts)(h.AdminDeleteMetric)))

	// ---- Access approval workflow : CapApprovalWorkflow ----
	mux.HandleFunc("GET /api/v1/me/approvals", auth(require(capabilities.CapApprovalWorkflow)(h.UserListMyApprovals)))
	mux.HandleFunc("POST /admin/api/approvals", auth(require(capabilities.CapApprovalWorkflow)(h.UserSubmitApproval)))
	mux.HandleFunc("GET /admin/api/approvals", admin(require(capabilities.CapApprovalWorkflow)(h.AdminListApprovals)))
	mux.HandleFunc("POST /admin/api/approvals/{id}/approve", admin(require(capabilities.CapApprovalWorkflow)(h.AdminApproveApproval)))
	mux.HandleFunc("POST /admin/api/approvals/{id}/reject", admin(require(capabilities.CapApprovalWorkflow)(h.AdminRejectApproval)))
	mux.HandleFunc("POST /admin/api/approvals/{id}/revoke", admin(require(capabilities.CapApprovalWorkflow)(h.AdminRevokeApproval)))
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
