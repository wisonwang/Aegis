package api

import (
	"encoding/json"
	"net/http"

	"github.com/wisonwang/aegis/internal/store"
)

// validMaskStrategy reports whether s is a supported dynamic-masking strategy.
func validMaskStrategy(s string) bool {
	switch s {
	case "phone", "email", "card", "hash", "redact", "partial":
		return true
	}
	return false
}

// AdminListMasks returns all column-masking rules for a data source (across
// roles), with the role name resolved for readability.
func (h *Handler) AdminListMasks(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	table := r.URL.Query().Get("table")
	roles, _ := h.Store.ListRoles()
	nameByID := map[string]string{}
	for _, role := range roles {
		nameByID[role.ID] = role.Name
	}
	masks, err := h.Store.ListColumnMasks("", id, table)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]interface{}, 0, len(masks))
	for _, m := range masks {
		out = append(out, map[string]interface{}{
			"id":          m.ID,
			"role":        nameByID[m.RoleID],
			"role_id":     m.RoleID,
			"table_name":  m.TableName,
			"column_name": m.ColumnName,
			"strategy":    m.Strategy,
			"keep":        m.Keep,
			"updated_at":  m.UpdatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"masks": out})
}

type upsertMaskRequest struct {
	Role     string `json:"role"`
	Table    string `json:"table"`
	Column   string `json:"column"`
	Strategy string `json:"strategy"`
	Keep     int    `json:"keep"`
}

// AdminUpsertMask inserts or updates a column-masking rule for a role.
func (h *Handler) AdminUpsertMask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req upsertMaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil ||
		req.Role == "" || req.Table == "" || req.Column == "" || req.Strategy == "" {
		writeError(w, http.StatusBadRequest, "role, table, column and strategy are required")
		return
	}
	if !validMaskStrategy(req.Strategy) {
		writeError(w, http.StatusBadRequest, "unsupported mask strategy (phone|email|card|hash|redact|partial)")
		return
	}
	role, err := h.Store.GetRole(req.Role)
	if err != nil || role == nil {
		writeError(w, http.StatusNotFound, "role not found")
		return
	}
	m := &store.ColumnMask{
		RoleID:       role.ID,
		DataSourceID: id,
		TableName:    req.Table,
		ColumnName:   req.Column,
		Strategy:     req.Strategy,
		Keep:         req.Keep,
	}
	if err := h.Store.UpsertColumnMask(m); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": m.ID})
}

// AdminDeleteMask removes a column-masking rule by id.
func (h *Handler) AdminDeleteMask(w http.ResponseWriter, r *http.Request) {
	mask := r.PathValue("mask")
	if err := h.Store.DeleteColumnMask(mask); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
