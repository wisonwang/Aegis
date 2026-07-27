package api

import (
	"encoding/json"
	"net/http"

	"github.com/wisonwang/aegis/internal/store"
)

// AdminListMetrics returns all curated metric definitions for a datasource.
func (h *Handler) AdminListMetrics(w http.ResponseWriter, r *http.Request) {
	id, err := h.resolveDS(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "datasource not found")
		return
	}
	metrics, err := h.Store.ListMetrics(id)
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
func (h *Handler) AdminUpsertMetric(w http.ResponseWriter, r *http.Request) {
	id, err := h.resolveDS(r.PathValue("id"))
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
	if err := h.Store.UpsertMetric(m); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": m.ID, "name": m.Name})
}

// AdminDeleteMetric removes a metric by id.
func (h *Handler) AdminDeleteMetric(w http.ResponseWriter, r *http.Request) {
	mid := r.PathValue("mid")
	if err := h.Store.DeleteMetric(mid); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
