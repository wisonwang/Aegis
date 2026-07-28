package api

import (
	"encoding/json"
	"net/http"

	"github.com/wisonwang/aegis/internal/store"
)

// AdminListSemantics returns all semantic entries (table & column business
// descriptions) for a data source.
func (h *Handler) AdminListSemantics(w http.ResponseWriter, r *http.Request) {
	dsID, rerr := h.resolveDS(r.PathValue("id"))
	if rerr != nil {
		writeError(w, http.StatusNotFound, rerr.Error())
		return
	}
	table := r.URL.Query().Get("table")
	sems, err := h.Store.ListSemantics(dsID, table)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]interface{}, 0, len(sems))
	for _, s := range sems {
		out = append(out, map[string]interface{}{
			"id":            s.ID,
			"table_name":    s.TableName,
			"column_name":   s.ColumnName,
			"description":   s.Description,
			"synonyms":      json.RawMessage(orEmpty(s.Synonyms)),
			"examples":      json.RawMessage(orEmpty(s.Examples)),
			"updated_at":    s.UpdatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"semantics": out})
}

type upsertSemanticRequest struct {
	TableName   string   `json:"table_name"`
	ColumnName  string   `json:"column_name"` // "" => table-level
	Description string   `json:"description"`
	Synonyms    []string `json:"synonyms"`
	Examples    []string `json:"examples"`
}

// AdminUpsertSemantic inserts or updates a table/column semantic description.
func (h *Handler) AdminUpsertSemantic(w http.ResponseWriter, r *http.Request) {
	dsID, rerr := h.resolveDS(r.PathValue("id"))
	if rerr != nil {
		writeError(w, http.StatusNotFound, rerr.Error())
		return
	}
	var req upsertSemanticRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.TableName == "" {
		writeError(w, http.StatusBadRequest, "table_name is required")
		return
	}
	syn, _ := json.Marshal(req.Synonyms)
	ex, _ := json.Marshal(req.Examples)
	sem := &store.Semantic{
		DataSourceID: dsID,
		TableName:    req.TableName,
		ColumnName:   req.ColumnName,
		Description:  req.Description,
		Synonyms:     string(syn),
		Examples:     string(ex),
	}
	if err := h.Store.UpsertSemantic(sem); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": sem.ID})
}

// AdminDeleteSemantic removes a semantic entry by id.
func (h *Handler) AdminDeleteSemantic(w http.ResponseWriter, r *http.Request) {
	sem := r.PathValue("sem")
	if err := h.Store.DeleteSemantic(sem); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
