package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/wisonwang/aegis/internal/proxy"
	"github.com/wisonwang/aegis/internal/store"
	"context"
)

// ---- Admin: dataset management -------------------------------------------

// @Summary admin List Datasets
// @Tags datasets
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/datasets [get]
func (h *Handler) AdminListDatasets(w http.ResponseWriter, r *http.Request) {
	ds, err := h.Store.ListDatasets(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"datasets": ds})
}

type createDatasetRequest struct {
	Name         string `json:"name"`
	DisplayName  string `json:"display_name"`
	Description  string `json:"description"`
	DataSourceID string `json:"datasource_id"`
	Definition   string `json:"definition"`
	Status       string `json:"status"` // optional: draft (default) | published
	Fields       string `json:"fields"` // optional JSON array of {name,type,description}
}

// @Summary admin Create Dataset
// @Tags datasets
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/datasets [post]
func (h *Handler) AdminCreateDataset(w http.ResponseWriter, r *http.Request) {
	var req createDatasetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil ||
		req.Name == "" || req.DataSourceID == "" || req.Definition == "" {
		writeError(w, http.StatusBadRequest, "name, datasource_id and definition are required")
		return
	}
	if !proxy.IsValidDatasetName(req.Name) {
		writeError(w, http.StatusBadRequest, "name must be a valid identifier (letters, digits, underscores)")
		return
	}
	if existing, _ := h.Store.GetDatasetByName(r.Context(), req.Name); existing != nil {
		writeError(w, http.StatusConflict, "a dataset with this name already exists")
		return
	}
	ds, err := h.Store.GetDataSource(r.Context(), req.DataSourceID)
	if err != nil || ds == nil {
		writeError(w, http.StatusBadRequest, "datasource not found")
		return
	}
	if err := h.Proxy.ValidateDatasetDefinition(ds.Type, req.Definition); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Fields != "" {
		if !json.Valid([]byte(req.Fields)) {
			writeError(w, http.StatusBadRequest, "fields must be valid JSON")
			return
		}
	}
	status := strings.ToLower(req.Status)
	if status == "" {
		status = store.DatasetDraft
	}
	d := &store.Dataset{
		Name:         req.Name,
		DisplayName:  req.DisplayName,
		Description:  req.Description,
		DataSourceID: req.DataSourceID,
		Definition:   req.Definition,
		Status:       status,
		Fields:       req.Fields,
	}
	if d.Fields == "" {
		d.Fields = "[]"
	}
	if err := h.Store.CreateDataset(r.Context(), d); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": d.ID})
}

// @Summary admin Get Dataset
// @Tags datasets
// @Produce json
// @Security BearerAuth
// @Param id path string true "id"
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/datasets/{id} [get]
func (h *Handler) AdminGetDataset(w http.ResponseWriter, r *http.Request) {
	d, err := h.Store.GetDataset(r.Context(), pathParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if d == nil {
		writeError(w, http.StatusNotFound, "dataset not found")
		return
	}
	writeJSON(w, http.StatusOK, d)
}

type updateDatasetRequest struct {
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	Definition  string `json:"definition"`
	Status      string `json:"status"`
	Fields      string `json:"fields"`
}

// @Summary admin Update Dataset
// @Tags datasets
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "id"
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/datasets/{id} [put]
func (h *Handler) AdminUpdateDataset(w http.ResponseWriter, r *http.Request) {
	id := pathParam(r, "id")
	d, err := h.Store.GetDataset(r.Context(), id)
	if err != nil || d == nil {
		writeError(w, http.StatusNotFound, "dataset not found")
		return
	}
	var req updateDatasetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.DisplayName != "" {
		d.DisplayName = req.DisplayName
	}
	if req.Description != "" {
		d.Description = req.Description
	}
	if req.Definition != "" {
		ds, err := h.Store.GetDataSource(r.Context(), d.DataSourceID)
		if err != nil || ds == nil {
			writeError(w, http.StatusBadRequest, "dataset's datasource not found")
			return
		}
		if err := h.Proxy.ValidateDatasetDefinition(ds.Type, req.Definition); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		d.Definition = req.Definition
	}
	if req.Fields != "" {
		if !json.Valid([]byte(req.Fields)) {
			writeError(w, http.StatusBadRequest, "fields must be valid JSON")
			return
		}
		d.Fields = req.Fields
	}
	if req.Status != "" {
		d.Status = strings.ToLower(req.Status)
	}
	if err := h.Store.UpdateDataset(r.Context(), d); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// @Summary admin Delete Dataset
// @Tags datasets
// @Produce json
// @Security BearerAuth
// @Param id path string true "id"
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/datasets/{id} [delete]
func (h *Handler) AdminDeleteDataset(w http.ResponseWriter, r *http.Request) {
	if err := h.Store.DeleteDataset(r.Context(), pathParam(r, "id")); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// @Summary admin Publish Dataset
// @Tags datasets
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "id"
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/datasets/{id}/publish [post]
func (h *Handler) AdminPublishDataset(w http.ResponseWriter, r *http.Request) {
	id := pathParam(r, "id")
	d, err := h.Store.GetDataset(r.Context(), id)
	if err != nil || d == nil {
		writeError(w, http.StatusNotFound, "dataset not found")
		return
	}
	ds, err := h.Store.GetDataSource(r.Context(), d.DataSourceID)
	if err != nil || ds == nil {
		writeError(w, http.StatusBadRequest, "dataset's datasource not found")
		return
	}
	if err := h.Proxy.ValidateDatasetDefinition(ds.Type, d.Definition); err != nil {
		writeError(w, http.StatusBadRequest, "cannot publish: "+err.Error())
		return
	}
	if err := h.Store.SetDatasetStatus(id, store.DatasetPublished); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": store.DatasetPublished})
}

// @Summary admin Unpublish Dataset
// @Tags datasets
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "id"
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/datasets/{id}/unpublish [post]
func (h *Handler) AdminUnpublishDataset(w http.ResponseWriter, r *http.Request) {
	id := pathParam(r, "id")
	if _, err := h.Store.GetDataset(r.Context(), id); err != nil {
		writeError(w, http.StatusNotFound, "dataset not found")
		return
	}
	if err := h.Store.SetDatasetStatus(id, store.DatasetDraft); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": store.DatasetDraft})
}

// ---- Admin: dataset governance (reuses the table-level stores, keyed by
// datasource_id + table_name = dataset.Name) ----

func (h *Handler) datasetCtx(ctx context.Context, id string) (*store.Dataset, *store.DataSource, error) {
	d, err := h.Store.GetDataset(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if d == nil {
		return nil, nil, errNotFound("dataset")
	}
	ds, err := h.Store.GetDataSource(ctx, d.DataSourceID)
	if err != nil || ds == nil {
		return nil, nil, errNotFound("datasource")
	}
	return d, ds, nil
}

func errNotFound(what string) error { return &simpleErr{"not found: " + what} }

type simpleErr struct{ msg string }

func (e *simpleErr) Error() string { return e.msg }

// @Summary admin List Dataset Permissions
// @Tags datasets
// @Produce json
// @Security BearerAuth
// @Param id path string true "id"
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/datasets/{id}/permissions [get]
func (h *Handler) AdminListDatasetPermissions(w http.ResponseWriter, r *http.Request) {
	d, _, err := h.datasetCtx(r.Context(), pathParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	out, err := h.listPermView(d.DataSourceID, d.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"permissions": out})
}

// @Summary admin Create Dataset Permission
// @Tags datasets
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "id"
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/datasets/{id}/permissions [post]
func (h *Handler) AdminCreateDatasetPermission(w http.ResponseWriter, r *http.Request) {
	d, _, err := h.datasetCtx(r.Context(), pathParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	var req createPermRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Role == "" || req.Ops == "" {
		writeError(w, http.StatusBadRequest, "role and ops are required")
		return
	}
	role, err := h.Store.GetRole(req.Role)
	if err != nil || role == nil {
		writeError(w, http.StatusNotFound, "role not found")
		return
	}
	allowed, _ := json.Marshal(req.AllowedCols)
	denied, _ := json.Marshal(req.DeniedCols)
	p := &store.TablePermission{
		RoleID:       role.ID,
		DataSourceID: d.DataSourceID,
		TableName:    d.Name,
		Ops:          req.Ops,
		AllowedCols:  string(allowed),
		DeniedCols:   string(denied),
	}
	if err := h.Store.CreateTablePermission(r.Context(), p); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": p.ID})
}

// @Summary admin Delete Dataset Permission
// @Tags datasets
// @Produce json
// @Security BearerAuth
// @Param id path string true "id"
// @Param perm path string true "perm"
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/datasets/{id}/permissions/{perm} [delete]
func (h *Handler) AdminDeleteDatasetPermission(w http.ResponseWriter, r *http.Request) {
	if err := h.Store.DeleteTablePermission(pathParam(r, "perm")); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// @Summary admin List Dataset Policies
// @Tags datasets
// @Produce json
// @Security BearerAuth
// @Param id path string true "id"
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/datasets/{id}/policies [get]
func (h *Handler) AdminListDatasetPolicies(w http.ResponseWriter, r *http.Request) {
	d, _, err := h.datasetCtx(r.Context(), pathParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	roles, _ := h.Store.ListRoles()
	nameByID := map[string]string{}
	for _, role := range roles {
		nameByID[role.ID] = role.Name
	}
	pols, err := h.Store.ListRowPolicies(r.Context(), "", d.DataSourceID, d.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]interface{}, 0, len(pols))
	for _, p := range pols {
		out = append(out, map[string]interface{}{
			"id":         p.ID,
			"role":       nameByID[p.RoleID],
			"role_id":    p.RoleID,
			"predicate":  p.Predicate,
			"priority":   p.Priority,
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"policies": out})
}

// @Summary admin Create Dataset Policy
// @Tags datasets
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "id"
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/datasets/{id}/policies [post]
func (h *Handler) AdminCreateDatasetPolicy(w http.ResponseWriter, r *http.Request) {
	d, _, err := h.datasetCtx(r.Context(), pathParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	var req createPolicyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Role == "" || req.Predicate == "" {
		writeError(w, http.StatusBadRequest, "role and predicate are required")
		return
	}
	role, err := h.Store.GetRole(req.Role)
	if err != nil || role == nil {
		writeError(w, http.StatusNotFound, "role not found")
		return
	}
	p := &store.RowPolicy{
		RoleID:       role.ID,
		DataSourceID: d.DataSourceID,
		TableName:    d.Name,
		Predicate:    req.Predicate,
		Priority:     req.Priority,
	}
	if err := h.Store.CreateRowPolicy(r.Context(), p); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": p.ID})
}

// @Summary admin Delete Dataset Policy
// @Tags datasets
// @Produce json
// @Security BearerAuth
// @Param id path string true "id"
// @Param policy path string true "policy"
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/datasets/{id}/policies/{policy} [delete]
func (h *Handler) AdminDeleteDatasetPolicy(w http.ResponseWriter, r *http.Request) {
	if err := h.Store.DeleteRowPolicy(pathParam(r, "policy")); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// @Summary admin List Dataset Masks
// @Tags datasets
// @Produce json
// @Security BearerAuth
// @Param id path string true "id"
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/datasets/{id}/masks [get]
func (h *Handler) AdminListDatasetMasks(w http.ResponseWriter, r *http.Request) {
	d, _, err := h.datasetCtx(r.Context(), pathParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	roles, _ := h.Store.ListRoles()
	nameByID := map[string]string{}
	for _, role := range roles {
		nameByID[role.ID] = role.Name
	}
	masks, err := h.Store.ListColumnMasks(r.Context(), "", d.DataSourceID, d.Name)
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
			"column_name": m.ColumnName,
			"strategy":    m.Strategy,
			"keep":        m.Keep,
			"updated_at":  m.UpdatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"masks": out})
}

// @Summary admin Upsert Dataset Mask
// @Tags datasets
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "id"
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/datasets/{id}/masks [post]
func (h *Handler) AdminUpsertDatasetMask(w http.ResponseWriter, r *http.Request) {
	d, _, err := h.datasetCtx(r.Context(), pathParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	var req upsertMaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if req.Role == "" || req.Column == "" || req.Strategy == "" {
		writeError(w, http.StatusBadRequest, "role, column and strategy are required")
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
		DataSourceID: d.DataSourceID,
		TableName:    d.Name,
		ColumnName:   req.Column,
		Strategy:     req.Strategy,
		Keep:         req.Keep,
	}
	if err := h.Store.UpsertColumnMask(r.Context(), m); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": m.ID})
}

// @Summary admin Delete Dataset Mask
// @Tags datasets
// @Produce json
// @Security BearerAuth
// @Param id path string true "id"
// @Param mask path string true "mask"
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/datasets/{id}/masks/{mask} [delete]
func (h *Handler) AdminDeleteDatasetMask(w http.ResponseWriter, r *http.Request) {
	if err := h.Store.DeleteColumnMask(pathParam(r, "mask")); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// @Summary admin List Dataset Semantics
// @Tags datasets
// @Produce json
// @Security BearerAuth
// @Param id path string true "id"
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/datasets/{id}/semantics [get]
func (h *Handler) AdminListDatasetSemantics(w http.ResponseWriter, r *http.Request) {
	d, _, err := h.datasetCtx(r.Context(), pathParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	all, err := h.Store.ListSemantics(r.Context(), d.DataSourceID, d.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"semantics": all})
}

// @Summary admin Upsert Dataset Semantic
// @Tags datasets
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "id"
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/datasets/{id}/semantics [post]
func (h *Handler) AdminUpsertDatasetSemantic(w http.ResponseWriter, r *http.Request) {
	d, _, err := h.datasetCtx(r.Context(), pathParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	var req upsertSemanticRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	syn, _ := json.Marshal(req.Synonyms)
	ex, _ := json.Marshal(req.Examples)
	sem := &store.Semantic{
		DataSourceID: d.DataSourceID,
		TableName:    d.Name,
		ColumnName:   req.ColumnName,
		Description:  req.Description,
		Synonyms:     string(syn),
		Examples:     string(ex),
	}
	if err := h.Store.UpsertSemantic(r.Context(), sem); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": sem.ID})
}

// @Summary admin Delete Dataset Semantic
// @Tags datasets
// @Produce json
// @Security BearerAuth
// @Param id path string true "id"
// @Param sem path string true "sem"
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/datasets/{id}/semantics/{sem} [delete]
func (h *Handler) AdminDeleteDatasetSemantic(w http.ResponseWriter, r *http.Request) {
	if err := h.Store.DeleteSemantic(pathParam(r, "sem")); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---- Agent/user: dataset consumption --------------------------------------

// ListDatasets returns the datasets the caller may consume.
// @Summary list Datasets
// @Tags datasets
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/datasets [get]
func (h *Handler) ListDatasets(w http.ResponseWriter, r *http.Request) {
	c := claimsFromContext(r.Context())
	datasets, err := h.Proxy.ListDatasets(r.Context(), c)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"datasets": datasets})
}

// GetDataset returns the governed schema contract of a dataset.
// @Summary get Dataset
// @Tags datasets
// @Produce json
// @Security BearerAuth
// @Param id path string true "id"
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/datasets/{id} [get]
func (h *Handler) GetDataset(w http.ResponseWriter, r *http.Request) {
	c := claimsFromContext(r.Context())
	schema, err := h.Proxy.DatasetCatalog(r.Context(), pathParam(r, "id"), c)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, schema)
}

// QueryDataset executes a governed dataset query.
// @Summary query Dataset
// @Tags datasets
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "id"
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/datasets/{id}/query [post]
func (h *Handler) QueryDataset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Params []interface{} `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	c := claimsFromContext(r.Context())
	res, err := h.Proxy.ExecuteDataset(proxy.WithChannel(r.Context(), "dataapi"), pathParam(r, "id"), c, req.Params)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}
