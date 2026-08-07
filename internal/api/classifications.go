package api

import (
	"encoding/json"
	"net/http"

	"github.com/wisonwang/aegis/internal/store"
)

// AdminListClassifications returns all classification (PII / sensitivity) labels
// for a data source. An optional ?table= filter scopes the result.
// @Summary admin List Classifications
// @Tags classifications
// @Produce json
// @Security BearerAuth
// @Param id path string true "id"
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/datasources/{id}/classifications [get]
func (h *Handler) AdminListClassifications(w http.ResponseWriter, r *http.Request) {
	dsID, rerr := h.resolveDS(r.Context(), pathParam(r, "id"))
	if rerr != nil {
		writeError(w, http.StatusNotFound, rerr.Error())
		return
	}
	table := r.URL.Query().Get("table")
	cls, err := h.Store.ListClassifications(r.Context(), dsID, table)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]interface{}, 0, len(cls))
	for _, c := range cls {
		out = append(out, map[string]interface{}{
			"id":           c.ID,
			"table_name":   c.TableName,
			"column_name":  c.ColumnName,
			"level":        c.Level,
			"tags":         json.RawMessage(orEmpty(c.Tags)),
			"updated_at":   c.UpdatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"classifications": out})
}

type upsertClassificationRequest struct {
	TableName  string   `json:"table_name"`
	ColumnName string   `json:"column_name"` // "" => table-level
	Level      string   `json:"level"`        // public|internal|confidential|restricted|pii
	Tags       []string `json:"tags"`
}

// AdminUpsertClassification inserts or updates a classification label for a
// table or column, keyed by (table_name, column_name).
// @Summary admin Upsert Classification
// @Tags classifications
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "id"
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/datasources/{id}/classifications [post]
func (h *Handler) AdminUpsertClassification(w http.ResponseWriter, r *http.Request) {
	dsID, ctx, rerr := h.resolveDSBound(r.Context(), pathParam(r, "id"))
	if rerr != nil {
		writeError(w, http.StatusNotFound, rerr.Error())
		return
	}
	var req upsertClassificationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.TableName == "" || req.Level == "" {
		writeError(w, http.StatusBadRequest, "table_name and level are required")
		return
	}
	tags, _ := json.Marshal(req.Tags)
	dc := &store.DataClassification{
		DataSourceID: dsID,
		TableName:    req.TableName,
		ColumnName:   req.ColumnName,
		Level:        req.Level,
		Tags:         string(tags),
	}
	if err := h.Store.UpsertClassification(ctx, dc); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": dc.ID})
}

// AdminDeleteClassification removes a classification entry by id.
// @Summary admin Delete Classification
// @Tags classifications
// @Produce json
// @Security BearerAuth
// @Param id path string true "id"
// @Param cls path string true "cls"
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/datasources/{id}/classifications/{cls} [delete]
func (h *Handler) AdminDeleteClassification(w http.ResponseWriter, r *http.Request) {
	_, ctx, rerr := h.resolveDSBound(r.Context(), pathParam(r, "id"))
	if rerr != nil {
		writeError(w, http.StatusNotFound, rerr.Error())
		return
	}
	if err := h.Store.DeleteClassification(ctx, pathParam(r, "cls")); err != nil {
		writeMutationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
