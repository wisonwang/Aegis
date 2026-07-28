package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/wisonwang/aegis/internal/proxy"
	"github.com/wisonwang/aegis/internal/store"
)

// validMaskStrategy reports whether s is a supported dynamic-masking strategy.
func validMaskStrategy(s string) bool {
	switch s {
	case "phone", "email", "card", "hash", "redact", "partial", "tokenize", "fpe":
		return true
	}
	return false
}

// AdminListMasks returns all column-masking rules for a data source (across
// roles), with the role name resolved for readability.
func (h *Handler) AdminListMasks(w http.ResponseWriter, r *http.Request) {
	dsID, rerr := h.resolveDS(r.PathValue("id"))
	if rerr != nil {
		writeError(w, http.StatusNotFound, rerr.Error())
		return
	}
	table := r.URL.Query().Get("table")
	roles, _ := h.Store.ListRoles()
	nameByID := map[string]string{}
	for _, role := range roles {
		nameByID[role.ID] = role.Name
	}
	masks, err := h.Store.ListColumnMasks("", dsID, table)
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
	dsID, rerr := h.resolveDS(r.PathValue("id"))
	if rerr != nil {
		writeError(w, http.StatusNotFound, rerr.Error())
		return
	}
	var req upsertMaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil ||
		req.Role == "" || req.Table == "" || req.Column == "" || req.Strategy == "" {
		writeError(w, http.StatusBadRequest, "role, table, column and strategy are required")
		return
	}
	if !validMaskStrategy(req.Strategy) {
		writeError(w, http.StatusBadRequest, "unsupported mask strategy (phone|email|card|hash|redact|partial|tokenize|fpe)")
		return
	}
	role, err := h.Store.GetRole(req.Role)
	if err != nil || role == nil {
		writeError(w, http.StatusNotFound, "role not found")
		return
	}
	m := &store.ColumnMask{
		RoleID:       role.ID,
		DataSourceID: dsID,
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

type recommendMasksRequest struct {
	Role            string `json:"role"`             // target role name; empty + apply_to_all_roles => all non-admin roles
	Table           string `json:"table"`            // optional table scope
	ApplyToAllRoles bool   `json:"apply_to_all_roles"` // apply to every non-admin role
	Apply           bool   `json:"apply"`            // persist recommendations; default false (dry-run)
}

type maskRecommendation struct {
	Table       string   `json:"table_name"`
	Column      string   `json:"column_name"`
	Level       string   `json:"level"`
	Tags        string   `json:"tags"`
	Strategy    string   `json:"recommended_strategy"`
	Keep        int      `json:"keep"`
	Reason      string   `json:"reason"`
	Applied     bool     `json:"applied"`
	TargetRoles []string `json:"target_roles,omitempty"`
}

// AdminRecommendMasks proposes default masking rules from a data source's
// column classifications (see proxy.RecommendMask). With apply=false (the safe
// default) it only returns proposals; with apply=true it persists ColumnMask
// rows for the chosen role(s). admin bypasses masking, so the meaningful
// default when apply_to_all_roles is set is every non-admin role.
func (h *Handler) AdminRecommendMasks(w http.ResponseWriter, r *http.Request) {
	dsID, rerr := h.resolveDS(r.PathValue("id"))
	if rerr != nil {
		writeError(w, http.StatusNotFound, rerr.Error())
		return
	}
	var req recommendMasksRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	cls, err := h.Store.ListClassifications(dsID, req.Table)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var targetRoles []*store.Role
	if req.Apply {
		if req.Role != "" {
			role, e := h.Store.GetRole(req.Role)
			if e != nil || role == nil {
				writeError(w, http.StatusNotFound, "role not found")
				return
			}
			targetRoles = []*store.Role{role}
		} else if req.ApplyToAllRoles {
			all, e := h.Store.ListRoles()
			if e != nil {
				writeError(w, http.StatusInternalServerError, e.Error())
				return
			}
			for _, role := range all {
				if strings.EqualFold(role.Name, "admin") {
					continue
				}
				targetRoles = append(targetRoles, role)
			}
		} else {
			writeError(w, http.StatusBadRequest, "apply requires role or apply_to_all_roles")
			return
		}
	}

	recs := make([]maskRecommendation, 0, len(cls))
	for _, c := range cls {
		if c.ColumnName == "" {
			continue // table-level labels don't map to a column mask
		}
		strategy, keep, reason, ok := proxy.RecommendMask(*c)
		rec := maskRecommendation{
			Table: c.TableName, Column: c.ColumnName, Level: c.Level,
			Tags: c.Tags, Strategy: strategy, Keep: keep, Reason: reason,
		}
		if !ok {
			recs = append(recs, rec)
			continue
		}
		if req.Apply && len(targetRoles) > 0 {
			names := make([]string, 0, len(targetRoles))
			for _, role := range targetRoles {
				names = append(names, role.Name)
				m := &store.ColumnMask{
					RoleID: role.ID, DataSourceID: dsID, TableName: c.TableName,
					ColumnName: c.ColumnName, Strategy: strategy, Keep: keep,
				}
				if e := h.Store.UpsertColumnMask(m); e != nil {
					writeError(w, http.StatusInternalServerError, e.Error())
					return
				}
			}
			rec.Applied = true
			rec.TargetRoles = names
		}
		recs = append(recs, rec)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"applied":         req.Apply,
		"recommendations": recs,
	})
}
