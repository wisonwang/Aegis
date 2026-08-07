package api

import (
	"net/http"

	"github.com/wisonwang/aegis/internal/metrics"
)

// AdminStats returns a privacy-safe, point-in-time snapshot of this Aegis
// instance. It backs ADR-0005 (Phase 1) and is the local half of the adoption
// metrics story.
//
// Hard constraints (do not relax): it never reports PII, SQL text, tenant
// names, table/column names, hostnames or IPs — only aggregate counts and
// build identity. This keeps the "self-hosted + private" value proposition
// intact even though the data lives on the operator's own instance.
//
// @Summary admin Instance Stats
// @Tags admin
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/stats [get]
func (h *Handler) AdminStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	dss, err := h.Store.ListDataSources(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	types := map[string]struct{}{}
	for _, ds := range dss {
		if ds.Type != "" {
			types[ds.Type] = struct{}{}
		}
	}
	typeList := make([]string, 0, len(types))
	for t := range types {
		typeList = append(typeList, t)
	}

	wss, err := h.Store.ListWorkspaces()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	users, err := h.Store.ListUsers("")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	dts, err := h.Store.ListDatasets(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	ver, commit := metrics.BuildVersion()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"version": ver,
		"commit":  commit,
		"edition": h.Cfg.Edition,
		"uptime_seconds": metrics.UptimeSeconds(),
		"counts": map[string]interface{}{
			"datasources":       len(dss),
			"datasource_types":  typeList,
			"datasets":          len(dts),
			"workspaces":        len(wss),
			"users":             len(users),
			"queries_served":    metrics.QueriesServed(),
			"queries_denied":    metrics.QueriesDenied(),
			"mcp_sessions":      metrics.MCPSessions(),
		},
	})
}
