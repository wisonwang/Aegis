package api

import (
	"encoding/json"
	"net/http"

	"github.com/wisonwang/aegis/internal/store"
)

// AdminListMetrics returns all curated metric definitions for a datasource.
// @Summary admin List Metrics
// @Tags metrics
// @Produce json
// @Security BearerAuth
// @Param id path string true "id"
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/datasources/{id}/metrics [get]
func (h *Handler) AdminListMetrics(w http.ResponseWriter, r *http.Request) {
	id, err := h.resolveDS(r.Context(), pathParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "datasource not found")
		return
	}
	metrics, err := h.Store.ListMetrics(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"metrics": metrics})
}

type upsertMetricRequest struct {
	Name        string               `json:"name"`
	Description string               `json:"description"`
	SQLTemplate string               `json:"sql_template"`
	Params      []store.MetricParam `json:"params"`
	Unit        string               `json:"unit"`
}

// AdminUpsertMetric inserts or updates a curated metric definition. The metric
// is only ever executed through the governed path, so this is a safe curated
// surface — admins define the SQL, agents supply typed parameters.
// @Summary admin Upsert Metric
// @Tags metrics
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "id"
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/datasources/{id}/metrics [post]
func (h *Handler) AdminUpsertMetric(w http.ResponseWriter, r *http.Request) {
	id, err := h.resolveDS(r.Context(), pathParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "datasource not found")
		return
	}
	var req upsertMetricRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" || req.SQLTemplate == "" {
		writeError(w, http.StatusBadRequest, "name and sql_template are required")
		return
	}
	for _, p := range req.Params {
		if p.Name == "" {
			writeError(w, http.StatusBadRequest, "each param must have a name")
			return
		}
	}
	m := &store.MetricDefinition{
		DataSourceID: id,
		Name:         req.Name,
		Description:  req.Description,
		SQLTemplate:  req.SQLTemplate,
		Params:       req.Params,
		Unit:         req.Unit,
	}
	if err := h.Store.UpsertMetric(r.Context(), m); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": m.ID, "name": m.Name})
}

// AdminDeleteMetric removes a metric by id.
// @Summary admin Delete Metric
// @Tags metrics
// @Produce json
// @Security BearerAuth
// @Param id path string true "id"
// @Param mid path string true "mid"
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/datasources/{id}/metrics/{mid} [delete]
func (h *Handler) AdminDeleteMetric(w http.ResponseWriter, r *http.Request) {
	mid := pathParam(r, "mid")
	if err := h.Store.DeleteMetric(mid); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
