package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/wisonwang/aegis/internal/proxy"
	"github.com/wisonwang/aegis/internal/requestctx"
	"github.com/wisonwang/aegis/internal/store"
)

type Handler struct {
	Store *store.Store
	Proxy *proxy.Proxy
}

func New(st *store.Store, px *proxy.Proxy) *Handler {
	return &Handler{Store: st, Proxy: px}
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]interface{}{"error": msg})
}

// writeMutationError maps a store mutation error onto an HTTP status.
// store.ErrNotFound covers both "no such id" and "that id lives in another
// workspace" — deliberately the same 404 so a caller cannot use delete as an
// oracle to enumerate another tenant's object ids (ADR-0007).
func writeMutationError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found in the active workspace")
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}

func pathParam(r *http.Request, name string) string {
	return requestctx.PathParam(r, name)
}

// resolveDS resolves a datasource id or name to its canonical id.
func (h *Handler) resolveDS(ctx context.Context, idOrName string) (string, error) {
	if idOrName == "" {
		return "", errStr("datasource is required")
	}
	ds, err := h.Store.GetDataSource(ctx, idOrName)
	if err == nil && ds != nil {
		return ds.ID, nil
	}
	all, err := h.Store.ListDataSources(ctx)
	if err != nil {
		return "", err
	}
	for _, d := range all {
		if d.Name == idOrName {
			return d.ID, nil
		}
	}
	return "", errStr("datasource not found: " + idOrName)
}

func orEmpty(s string) string {
	if s == "" {
		return "null"
	}
	return s
}

func (h *Handler) listPermView(ctx context.Context, dsID, table string) ([]map[string]interface{}, error) {
	roles, _ := h.Store.ListRoles()
	nameByID := map[string]string{}
	for _, role := range roles {
		nameByID[role.ID] = role.Name
	}
	perms, err := h.Store.ListTablePermissions(ctx, "", dsID, table)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]interface{}, 0, len(perms))
	for _, p := range perms {
		out = append(out, map[string]interface{}{
			"id":           p.ID,
			"role":         nameByID[p.RoleID],
			"role_id":      p.RoleID,
			"table_name":   p.TableName,
			"ops":          p.Ops,
			"allowed_cols": json.RawMessage(orEmpty(p.AllowedCols)),
			"denied_cols":  json.RawMessage(orEmpty(p.DeniedCols)),
		})
	}
	return out, nil
}

type createPermRequest struct {
	Role        string   `json:"role"`
	Ops         string   `json:"ops"`
	AllowedCols []string `json:"allowed_cols"`
	DeniedCols  []string `json:"denied_cols"`
}

type createPolicyRequest struct {
	Role      string `json:"role"`
	Predicate string `json:"predicate"`
	Priority  int    `json:"priority"`
}

type upsertMaskRequest struct {
	Role     string `json:"role"`
	Table    string `json:"table"`
	Column   string `json:"column"`
	Strategy string `json:"strategy"`
	Keep     int    `json:"keep"`
}

type upsertSemanticRequest struct {
	TableName   string   `json:"table_name"`
	ColumnName  string   `json:"column_name"`
	Description string   `json:"description"`
	Synonyms    []string `json:"synonyms"`
	Examples    []string `json:"examples"`
}

type createDatasetRequest struct {
	Name         string `json:"name"`
	DisplayName  string `json:"display_name"`
	Description  string `json:"description"`
	DataSourceID string `json:"datasource_id"`
	Definition   string `json:"definition"`
	Status       string `json:"status"`
	Fields       string `json:"fields"`
	FolderID     string `json:"folder_id"`
	WorkspaceID  string `json:"workspace_id"` // ADR-0007: where the dataset lands
}

type updateDatasetRequest struct {
	DisplayName string  `json:"display_name"`
	Description string  `json:"description"`
	Definition  string  `json:"definition"`
	Status      string  `json:"status"`
	Fields      string  `json:"fields"`
	FolderID    *string `json:"folder_id"` // nil = unchanged; "" = uncategorized
}

type upsertMetricRequest struct {
	Name        string              `json:"name"`
	Description string              `json:"description"`
	SQLTemplate string              `json:"sql_template"`
	Params      []store.MetricParam `json:"params"`
	Unit        string              `json:"unit"`
}

type metricRunRequest struct {
	Params    map[string]interface{} `json:"params"`
	SessionID string                 `json:"session_id"`
}

func validMaskStrategy(s string) bool {
	switch s {
	case "phone", "email", "card", "hash", "redact", "partial", "tokenize", "fpe":
		return true
	}
	return false
}

type simpleErr struct{ msg string }

func (e *simpleErr) Error() string { return e.msg }

func errNotFound(what string) error { return &simpleErr{msg: "not found: " + what} }

type errStr string

func (e errStr) Error() string { return string(e) }

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

func (h *Handler) AdminListDatasets(w http.ResponseWriter, r *http.Request) {
	folderID := r.URL.Query().Get("folder_id")
	recursive := r.URL.Query().Get("recursive") == "1" || r.URL.Query().Get("recursive") == "true"
	ds, err := h.Store.ListDatasetsByFolder(r.Context(), folderID, recursive)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"datasets": ds})
}

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
	if req.Fields != "" && !json.Valid([]byte(req.Fields)) {
		writeError(w, http.StatusBadRequest, "fields must be valid JSON")
		return
	}
	if req.FolderID != "" {
		if _, err := h.Store.GetFolder(r.Context(), req.FolderID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	status := strings.ToLower(req.Status)
	if status == "" {
		status = store.DatasetDraft
	}
	// ADR-0007: stamp the dataset into the chosen workspace. An explicit
	// workspace_id wins; otherwise the caller's active scope is used (and in the
	// cross-workspace "*" view the admin must name a concrete workspace).
	writeCtx := r.Context()
	if req.WorkspaceID != "" && req.WorkspaceID != store.WorkspaceAll {
		ws, werr := h.Store.GetWorkspace(req.WorkspaceID)
		if werr != nil {
			writeError(w, http.StatusInternalServerError, werr.Error())
			return
		}
		if ws == nil {
			writeError(w, http.StatusBadRequest, "workspace not found: "+req.WorkspaceID)
			return
		}
		writeCtx = store.WithWorkspace(writeCtx, ws.ID)
	}
	d := &store.Dataset{
		Name:         req.Name,
		DisplayName:  req.DisplayName,
		Description:  req.Description,
		DataSourceID: req.DataSourceID,
		Definition:   req.Definition,
		Status:       status,
		Fields:       req.Fields,
		FolderID:     req.FolderID,
	}
	if d.Fields == "" {
		d.Fields = "[]"
	}
	if err := h.Store.CreateDataset(writeCtx, d); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": d.ID, "workspace_id": store.WriteWorkspace(writeCtx)})
}

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
	if req.FolderID != nil {
		d.FolderID = *req.FolderID
	}
	if err := h.Store.UpdateDataset(r.Context(), d); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) AdminDeleteDataset(w http.ResponseWriter, r *http.Request) {
	if err := h.Store.DeleteDataset(r.Context(), pathParam(r, "id")); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---- Dataset catalog folders (hierarchical organization of datasets) ----

type folderRequest struct {
	Name        string `json:"name"`
	ParentID    string `json:"parent_id"` // "" or absent = root
	WorkspaceID string `json:"workspace_id"`
}

type moveDatasetRequest struct {
	FolderID string `json:"folder_id"` // "" = uncategorized
}

// @Summary list catalog folders (workspace-scoped)
// @Tags catalog
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/dataset-folders [get]
// ListFolders is the consumer-side read of the catalog tree (workspace-scoped),
// used to render the collapsible data catalog.
func (h *Handler) ListFolders(w http.ResponseWriter, r *http.Request) {
	folders, err := h.Store.ListFolders(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"folders": folders})
}

// @Summary admin list catalog folders
// @Tags catalog
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/dataset-folders [get]
// AdminListFolders returns the workspace's flat folder list for the admin tree.
func (h *Handler) AdminListFolders(w http.ResponseWriter, r *http.Request) {
	folders, err := h.Store.ListFolders(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"folders": folders})
}

// @Summary admin create catalog folder
// @Tags catalog
// @Accept json
// @Produce json
// @Param body body folderRequest true "folder"
// @Security BearerAuth
// @Success 201 {object} map[string]interface{}
// @Router /admin/api/dataset-folders [post]
// AdminCreateFolder creates a catalog folder, optionally under a parent.
func (h *Handler) AdminCreateFolder(w http.ResponseWriter, r *http.Request) {
	var req folderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	writeCtx := r.Context()
	if req.WorkspaceID != "" && req.WorkspaceID != store.WorkspaceAll {
		ws, werr := h.Store.GetWorkspace(req.WorkspaceID)
		if werr != nil {
			writeError(w, http.StatusInternalServerError, werr.Error())
			return
		}
		if ws == nil {
			writeError(w, http.StatusBadRequest, "workspace not found: "+req.WorkspaceID)
			return
		}
		writeCtx = store.WithWorkspace(writeCtx, ws.ID)
	}
	if req.ParentID != "" {
		if _, err := h.Store.GetFolder(writeCtx, req.ParentID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	f := &store.DatasetFolder{Name: req.Name, ParentID: req.ParentID}
	if err := h.Store.CreateFolder(writeCtx, f); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": f.ID, "workspace_id": store.WriteWorkspace(writeCtx)})
}

// @Summary admin update catalog folder
// @Tags catalog
// @Accept json
// @Produce json
// @Param id path string true "folder id"
// @Param body body folderRequest true "folder"
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/dataset-folders/{id} [put]
// AdminUpdateFolder renames and/or reparents a folder.
func (h *Handler) AdminUpdateFolder(w http.ResponseWriter, r *http.Request) {
	id := pathParam(r, "id")
	var req folderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if err := h.Store.UpdateFolder(r.Context(), id, req.Name, req.ParentID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// @Summary admin delete catalog folder
// @Tags catalog
// @Param id path string true "folder id"
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/dataset-folders/{id} [delete]
// AdminDeleteFolder removes a folder; refuses non-empty folders.
func (h *Handler) AdminDeleteFolder(w http.ResponseWriter, r *http.Request) {
	if err := h.Store.DeleteFolder(r.Context(), pathParam(r, "id")); err != nil {
		if strings.Contains(err.Error(), "not empty") {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// @Summary admin move dataset to a catalog folder
// @Tags datasets
// @Accept json
// @Produce json
// @Param id path string true "dataset id"
// @Param body body moveDatasetRequest true "target folder"
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/datasets/{id}/move [post]
// AdminMoveDataset assigns a dataset to a catalog folder ("" = uncategorized).
func (h *Handler) AdminMoveDataset(w http.ResponseWriter, r *http.Request) {
	id := pathParam(r, "id")
	var req moveDatasetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if err := h.Store.MoveDataset(r.Context(), id, req.FolderID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

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

func (h *Handler) AdminListDatasetPermissions(w http.ResponseWriter, r *http.Request) {
	d, _, err := h.datasetCtx(r.Context(), pathParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	out, err := h.listPermView(r.Context(), d.DataSourceID, d.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"permissions": out})
}

func (h *Handler) AdminCreateDatasetPermission(w http.ResponseWriter, r *http.Request) {
	d, ds, err := h.datasetCtx(r.Context(), pathParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	// Governance belongs to the datasource's workspace, not the admin's
	// current view (ADR-0007).
	ctx := ds.BoundContext(r.Context())
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
	if err := h.Store.CreateTablePermission(ctx, p); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": p.ID})
}

func (h *Handler) AdminDeleteDatasetPermission(w http.ResponseWriter, r *http.Request) {
	_, ds, err := h.datasetCtx(r.Context(), pathParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err := h.Store.DeleteTablePermission(ds.BoundContext(r.Context()), pathParam(r, "perm")); err != nil {
		writeMutationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

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
			"id":        p.ID,
			"role":      nameByID[p.RoleID],
			"role_id":   p.RoleID,
			"predicate": p.Predicate,
			"priority":  p.Priority,
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"policies": out})
}

func (h *Handler) AdminCreateDatasetPolicy(w http.ResponseWriter, r *http.Request) {
	d, ds, err := h.datasetCtx(r.Context(), pathParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	// Governance belongs to the datasource's workspace, not the admin's
	// current view (ADR-0007).
	ctx := ds.BoundContext(r.Context())
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
	if err := h.Store.CreateRowPolicy(ctx, p); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": p.ID})
}

func (h *Handler) AdminDeleteDatasetPolicy(w http.ResponseWriter, r *http.Request) {
	_, ds, err := h.datasetCtx(r.Context(), pathParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err := h.Store.DeleteRowPolicy(ds.BoundContext(r.Context()), pathParam(r, "policy")); err != nil {
		writeMutationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

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

func (h *Handler) AdminUpsertDatasetMask(w http.ResponseWriter, r *http.Request) {
	d, ds, err := h.datasetCtx(r.Context(), pathParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	// Governance belongs to the datasource's workspace, not the admin's
	// current view (ADR-0007).
	ctx := ds.BoundContext(r.Context())
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
	if err := h.Store.UpsertColumnMask(ctx, m); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": m.ID})
}

func (h *Handler) AdminDeleteDatasetMask(w http.ResponseWriter, r *http.Request) {
	_, ds, err := h.datasetCtx(r.Context(), pathParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err := h.Store.DeleteColumnMask(ds.BoundContext(r.Context()), pathParam(r, "mask")); err != nil {
		writeMutationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

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

func (h *Handler) AdminUpsertDatasetSemantic(w http.ResponseWriter, r *http.Request) {
	d, ds, err := h.datasetCtx(r.Context(), pathParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	// Governance belongs to the datasource's workspace, not the admin's
	// current view (ADR-0007).
	ctx := ds.BoundContext(r.Context())
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
	if err := h.Store.UpsertSemantic(ctx, sem); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": sem.ID})
}

func (h *Handler) AdminDeleteDatasetSemantic(w http.ResponseWriter, r *http.Request) {
	_, ds, err := h.datasetCtx(r.Context(), pathParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err := h.Store.DeleteSemantic(ds.BoundContext(r.Context()), pathParam(r, "sem")); err != nil {
		writeMutationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) ListDatasets(w http.ResponseWriter, r *http.Request) {
	c := requestctx.Claims(r.Context())
	datasets, err := h.Proxy.ListDatasets(r.Context(), c)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"datasets": datasets})
}

func (h *Handler) GetDataset(w http.ResponseWriter, r *http.Request) {
	c := requestctx.Claims(r.Context())
	schema, err := h.Proxy.DatasetCatalog(r.Context(), pathParam(r, "id"), c)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, schema)
}

func (h *Handler) QueryDataset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Params []interface{} `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	c := requestctx.Claims(r.Context())
	res, err := h.Proxy.ExecuteDataset(proxy.WithChannel(r.Context(), "dataapi"), pathParam(r, "id"), c, req.Params)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// @Summary list metrics for a datasource
// @Tags metrics
// @Produce json
// @Param id path string true "datasource id"
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/datasources/{id}/metrics [get]
func (h *Handler) ListMetrics(w http.ResponseWriter, r *http.Request) {
	dsID, err := h.resolveDS(r.Context(), pathParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "datasource not found")
		return
	}
	metrics, err := h.Store.ListMetrics(r.Context(), dsID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"metrics": metrics})
}

// @Summary run a metric (resolve SQL + execute)
// @Tags metrics
// @Accept json
// @Produce json
// @Param id path string true "datasource id"
// @Param name path string true "metric name"
// @Param body body metricRunRequest true "run params"
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/datasources/{id}/metrics/{name}/run [post]
func (h *Handler) RunMetric(w http.ResponseWriter, r *http.Request) {
	dsID, err := h.resolveDS(r.Context(), pathParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "datasource not found")
		return
	}
	metricName := pathParam(r, "name")
	if metricName == "" {
		writeError(w, http.StatusBadRequest, "metric name is required")
		return
	}
	var req metricRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	c := requestctx.Claims(r.Context())
	sessionID := req.SessionID
	if sessionID == "" {
		sessionID = uuid.NewString()
	}
	ctx := proxy.WithSession(proxy.WithChannel(r.Context(), "dataapi"), sessionID)
	res, err := h.Proxy.ResolveMetric(ctx, dsID, c, metricName, req.Params)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"sql":          res.SQL,
		"lineage":      res.Lineage,
		"query_result": res.QueryResult,
		"session_id":   sessionID,
	})
}

// @Summary admin list metrics for a datasource
// @Tags metrics
// @Produce json
// @Param id path string true "datasource id"
// @Security BearerAuth
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

// @Summary admin create or update a metric
// @Tags metrics
// @Accept json
// @Produce json
// @Param id path string true "datasource id"
// @Param body body upsertMetricRequest true "metric"
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/datasources/{id}/metrics [post]
func (h *Handler) AdminUpsertMetric(w http.ResponseWriter, r *http.Request) {
	dsID, err := h.resolveDS(r.Context(), pathParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "datasource not found")
		return
	}
	ds, err := h.Store.GetDataSource(r.Context(), dsID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if ds == nil {
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
		DataSourceID: dsID,
		Name:         req.Name,
		Description:  req.Description,
		SQLTemplate:  req.SQLTemplate,
		Params:       req.Params,
		Unit:         req.Unit,
	}
	upsCtx := ds.BoundContext(r.Context())
	if err := h.Store.UpsertMetric(upsCtx, m); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": m.ID, "name": m.Name, "workspace_id": store.WriteWorkspace(upsCtx)})
}

// @Summary admin delete a metric
// @Tags metrics
// @Param id path string true "datasource id"
// @Param mid path string true "metric id"
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/datasources/{id}/metrics/{mid} [delete]
func (h *Handler) AdminDeleteMetric(w http.ResponseWriter, r *http.Request) {
	mid := pathParam(r, "mid")
	if err := h.Store.DeleteMetric(r.Context(), mid); err != nil {
		writeMutationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

var validApprovalOps = map[string]bool{
	"SELECT": true, "INSERT": true, "UPDATE": true, "DELETE": true,
}

func normalizeOps(raw string) (string, bool) {
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' })
	out := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, p := range parts {
		op := strings.ToUpper(strings.TrimSpace(p))
		if op == "" || !validApprovalOps[op] {
			return "", false
		}
		if !seen[op] {
			seen[op] = true
			out = append(out, op)
		}
	}
	if len(out) == 0 {
		return "", false
	}
	return strings.Join(out, ","), true
}

type createApprovalRequest struct {
	DataSourceID  string `json:"datasource_id"`
	TableName     string `json:"table_name"`
	Role          string `json:"role"`
	Ops           string `json:"ops"`
	Justification string `json:"justification"`
}

func (h *Handler) UserSubmitApproval(w http.ResponseWriter, r *http.Request) {
	c := requestctx.Claims(r.Context())
	var req createApprovalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.DataSourceID == "" || req.TableName == "" || req.Role == "" || req.Ops == "" {
		writeError(w, http.StatusBadRequest, "datasource_id, table_name, role and ops are required")
		return
	}
	ops, ok := normalizeOps(req.Ops)
	if !ok {
		writeError(w, http.StatusBadRequest, "ops must be a subset of SELECT,INSERT,UPDATE,DELETE")
		return
	}
	dsID, rerr := h.resolveDS(r.Context(), req.DataSourceID)
	if rerr != nil {
		writeError(w, http.StatusNotFound, rerr.Error())
		return
	}
	ds, err := h.Store.GetDataSource(r.Context(), dsID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if ds == nil {
		writeError(w, http.StatusNotFound, "datasource not found")
		return
	}
	role, err := h.Store.GetRole(req.Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if role == nil {
		writeError(w, http.StatusNotFound, "role not found")
		return
	}
	name := c.DisplayName
	if name == "" {
		name = c.Username
	}
	ar := &store.ApprovalRequest{
		ApplicantID:    c.UserID,
		ApplicantName:  name,
		DataSourceID:   ds.ID,
		DataSourceName: ds.Name,
		TableName:      req.TableName,
		RoleName:       role.Name,
		Ops:            ops,
		Justification:  req.Justification,
	}
	if err := h.Store.CreateApprovalRequest(ds.BoundContext(r.Context()), ar); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": ar.ID, "status": ar.Status})
}

func (h *Handler) UserListMyApprovals(w http.ResponseWriter, r *http.Request) {
	c := requestctx.Claims(r.Context())
	list, err := h.Store.ListApprovalRequests(r.Context(), "", "", c.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"approvals": list})
}

func (h *Handler) AdminListApprovals(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	list, err := h.Store.ListApprovalRequests(r.Context(), q.Get("status"), q.Get("datasource_id"), "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"approvals": list})
}

func (h *Handler) AdminApproveApproval(w http.ResponseWriter, r *http.Request) {
	id := pathParam(r, "id")
	ar, err := h.Store.GetApprovalRequest(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if ar == nil {
		writeError(w, http.StatusNotFound, "request not found")
		return
	}
	if ar.Status != store.ApprovalPending {
		writeError(w, http.StatusConflict, "request already resolved (status="+ar.Status+")")
		return
	}
	role, err := h.Store.GetRole(ar.RoleName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if role == nil {
		writeError(w, http.StatusConflict, "target role no longer exists")
		return
	}
	ds, err := h.Store.GetDataSource(r.Context(), ar.DataSourceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if ds == nil {
		writeError(w, http.StatusConflict, "target datasource no longer exists")
		return
	}
	perm := &store.TablePermission{
		RoleID:       role.ID,
		DataSourceID: ds.ID,
		TableName:    ar.TableName,
		Ops:          ar.Ops,
	}
	if err := h.Store.CreateTablePermission(ds.BoundContext(r.Context()), perm); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	c := requestctx.Claims(r.Context())
	approverName := c.DisplayName
	if approverName == "" {
		approverName = c.Username
	}
	if err := h.Store.ResolveApproval(id, store.ApprovalApproved, c.UserID, approverName, perm.ID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": store.ApprovalApproved, "granted_perm_id": perm.ID})
}

func (h *Handler) AdminRejectApproval(w http.ResponseWriter, r *http.Request) {
	id := pathParam(r, "id")
	ar, err := h.Store.GetApprovalRequest(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if ar == nil {
		writeError(w, http.StatusNotFound, "request not found")
		return
	}
	if ar.Status != store.ApprovalPending {
		writeError(w, http.StatusConflict, "request already resolved (status="+ar.Status+")")
		return
	}
	c := requestctx.Claims(r.Context())
	approverName := c.DisplayName
	if approverName == "" {
		approverName = c.Username
	}
	if err := h.Store.ResolveApproval(id, store.ApprovalRejected, c.UserID, approverName, ""); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": store.ApprovalRejected})
}

func (h *Handler) AdminRevokeApproval(w http.ResponseWriter, r *http.Request) {
	id := pathParam(r, "id")
	ar, err := h.Store.GetApprovalRequest(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if ar == nil {
		writeError(w, http.StatusNotFound, "request not found")
		return
	}
	if ar.Status != store.ApprovalApproved {
		writeError(w, http.StatusConflict, "only approved requests can be revoked (status="+ar.Status+")")
		return
	}
	if ar.GrantedPermID != "" {
		// Revocation must always land, whatever workspace the admin is in.
		_ = h.Store.DeleteTablePermission(store.WithWorkspace(r.Context(), store.WorkspaceAll), ar.GrantedPermID)
	}
	c := requestctx.Claims(r.Context())
	approverName := c.DisplayName
	if approverName == "" {
		approverName = c.Username
	}
	if err := h.Store.ResolveApproval(id, store.ApprovalRevoked, c.UserID, approverName, ar.GrantedPermID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": store.ApprovalRevoked})
}
