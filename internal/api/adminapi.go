package api

import (
	"encoding/json"
	"net/http"

	"github.com/wisonwang/aegis/internal/datasource"
	"github.com/wisonwang/aegis/internal/store"
	"context"
)

// ---- Users ----

// @Summary admin List Users
// @Tags admin
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/users [get]
func (h *Handler) AdminListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.Store.ListUsers()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	roles, _ := h.Store.ListRoles()
	roleByID := map[string]string{}
	for _, role := range roles {
		roleByID[role.ID] = role.Name
	}
	out := make([]map[string]interface{}, 0, len(users))
	for _, u := range users {
		ur, _ := h.Store.ListRolesForUser(u.ID)
		names := []string{}
		for _, role := range ur {
			names = append(names, role.Name)
		}
		out = append(out, map[string]interface{}{
			"id":           u.ID,
			"username":     u.Username,
			"display_name": u.DisplayName,
			"status":       u.Status,
			"attributes":   json.RawMessage(orEmpty(u.Attributes)),
			"roles":        names,
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"users": out})
}

type createUserRequest struct {
	Username    string            `json:"username"`
	DisplayName string            `json:"display_name"`
	Password    string            `json:"password"`
	Attributes  map[string]string `json:"attributes"`
	Roles       []string          `json:"roles"`
}

// @Summary admin Create User
// @Tags admin
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/users [post]
func (h *Handler) AdminCreateUser(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.Username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "username and password are required")
		return
	}
	hash, err := hashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	attrs := ""
	if len(req.Attributes) > 0 {
		b, _ := json.Marshal(req.Attributes)
		attrs = string(b)
	}
	u := &store.User{
		Username:     req.Username,
		DisplayName:  req.DisplayName,
		PasswordHash: hash,
		Attributes:   attrs,
	}
	if err := h.Store.CreateUser(u); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, rn := range req.Roles {
		role, err := h.Store.GetRole(rn)
		if err != nil || role == nil {
			continue
		}
		_ = h.Store.AddUserRole(u.ID, role.ID)
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": u.ID})
}

type updateUserRequest struct {
	DisplayName string            `json:"display_name"`
	Status      string            `json:"status"`
	Attributes  map[string]string `json:"attributes"`
}

// @Summary admin Update User
// @Tags admin
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "id"
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/users/{id} [put]
func (h *Handler) AdminUpdateUser(w http.ResponseWriter, r *http.Request) {
	id := pathParam(r, "id")
	u, err := h.Store.GetUser(id)
	if err != nil || u == nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	var req updateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.DisplayName != "" {
		u.DisplayName = req.DisplayName
	}
	if req.Status != "" {
		u.Status = req.Status
	}
	if req.Attributes != nil {
		b, _ := json.Marshal(req.Attributes)
		u.Attributes = string(b)
	}
	if err := h.Store.UpdateUser(u); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type passwordRequest struct {
	Password string `json:"password"`
}

// @Summary admin Set Password
// @Tags admin
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "id"
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/users/{id}/password [post]
func (h *Handler) AdminSetPassword(w http.ResponseWriter, r *http.Request) {
	id := pathParam(r, "id")
	var req passwordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Password == "" {
		writeError(w, http.StatusBadRequest, "password required")
		return
	}
	hash, err := hashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := h.Store.SetUserPassword(id, hash); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// @Summary admin Delete User
// @Tags admin
// @Produce json
// @Security BearerAuth
// @Param id path string true "id"
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/users/{id} [delete]
func (h *Handler) AdminDeleteUser(w http.ResponseWriter, r *http.Request) {
	id := pathParam(r, "id")
	if err := h.Store.DeleteUser(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type roleRef struct {
	Role string `json:"role"`
}

// @Summary admin Add User Role
// @Tags admin
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "id"
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/users/{id}/roles [post]
func (h *Handler) AdminAddUserRole(w http.ResponseWriter, r *http.Request) {
	id := pathParam(r, "id")
	var req roleRef
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Role == "" {
		writeError(w, http.StatusBadRequest, "role required")
		return
	}
	role, err := h.Store.GetRole(req.Role)
	if err != nil || role == nil {
		writeError(w, http.StatusNotFound, "role not found")
		return
	}
	if err := h.Store.AddUserRole(id, role.ID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// @Summary admin Remove User Role
// @Tags admin
// @Produce json
// @Security BearerAuth
// @Param id path string true "id"
// @Param role path string true "role"
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/users/{id}/roles/{role} [delete]
func (h *Handler) AdminRemoveUserRole(w http.ResponseWriter, r *http.Request) {
	id := pathParam(r, "id")
	role, err := h.Store.GetRole(pathParam(r, "role"))
	if err != nil || role == nil {
		writeError(w, http.StatusNotFound, "role not found")
		return
	}
	if err := h.Store.RemoveUserRole(id, role.ID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---- Roles ----

// @Summary admin List Roles
// @Tags admin
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/roles [get]
func (h *Handler) AdminListRoles(w http.ResponseWriter, r *http.Request) {
	roles, err := h.Store.ListRoles()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"roles": roles})
}

type createRoleRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// @Summary admin Create Role
// @Tags admin
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/roles [post]
func (h *Handler) AdminCreateRole(w http.ResponseWriter, r *http.Request) {
	var req createRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}
	if err := h.Store.CreateRole(&store.Role{Name: req.Name, Description: req.Description}); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"status": "ok"})
}

// @Summary admin Delete Role
// @Tags admin
// @Produce json
// @Security BearerAuth
// @Param id path string true "id"
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/roles/{id} [delete]
func (h *Handler) AdminDeleteRole(w http.ResponseWriter, r *http.Request) {
	id := pathParam(r, "id")
	if err := h.Store.DeleteRole(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---- DataSources ----

// @Summary admin List Data Sources
// @Tags admin
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/datasources [get]
func (h *Handler) AdminListDataSources(w http.ResponseWriter, r *http.Request) {
	ds, err := h.Store.ListDataSources(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"datasources": ds})
}

type createDSRequest struct {
	Name string `json:"name"`
	Type string `json:"type"`
	DSN  string `json:"dsn"`
}

// @Summary admin Create Data Source
// @Tags admin
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/datasources [post]
func (h *Handler) AdminCreateDataSource(w http.ResponseWriter, r *http.Request) {
	var req createDSRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" || req.Type == "" {
		writeError(w, http.StatusBadRequest, "name and type required")
		return
	}
	if !datasource.IsKnownType(req.Type) {
		writeError(w, http.StatusBadRequest, "unsupported datasource type "+req.Type)
		return
	}
	d := &store.DataSource{Name: req.Name, Type: datasource.NormalizeType(req.Type), DSN: req.DSN}
	if err := h.Store.CreateDataSource(r.Context(), d); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": d.ID})
}

// @Summary admin Update Data Source
// @Tags admin
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "id"
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/datasources/{id} [put]
func (h *Handler) AdminUpdateDataSource(w http.ResponseWriter, r *http.Request) {
	dsID, rerr := h.resolveDS(r.Context(), pathParam(r, "id"))
	if rerr != nil {
		writeError(w, http.StatusNotFound, rerr.Error())
		return
	}
	d, err := h.Store.GetDataSource(r.Context(), dsID)
	if err != nil || d == nil {
		writeError(w, http.StatusNotFound, "datasource not found")
		return
	}
	var req createDSRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.Type != "" && !datasource.IsKnownType(req.Type) {
		writeError(w, http.StatusBadRequest, "unsupported datasource type "+req.Type)
		return
	}
	if req.Name != "" {
		d.Name = req.Name
	}
	if req.Type != "" {
		d.Type = datasource.NormalizeType(req.Type)
	}
	if req.DSN != "" {
		d.DSN = req.DSN
	}
	if err := h.Store.UpdateDataSource(d); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// @Summary admin Delete Data Source
// @Tags admin
// @Produce json
// @Security BearerAuth
// @Param id path string true "id"
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/datasources/{id} [delete]
func (h *Handler) AdminDeleteDataSource(w http.ResponseWriter, r *http.Request) {
	dsID, rerr := h.resolveDS(r.Context(), pathParam(r, "id"))
	if rerr != nil {
		writeError(w, http.StatusNotFound, rerr.Error())
		return
	}
	if err := h.Store.DeleteDataSource(dsID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---- Table permissions ----

// @Summary admin List Table Permissions
// @Tags admin
// @Produce json
// @Security BearerAuth
// @Param id path string true "id"
// @Param table path string true "table"
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/datasources/{id}/tables/{table}/permissions [get]
func (h *Handler) AdminListTablePermissions(w http.ResponseWriter, r *http.Request) {
	dsID, rerr := h.resolveDS(r.Context(), pathParam(r, "id"))
	if rerr != nil {
		writeError(w, http.StatusNotFound, rerr.Error())
		return
	}
	table := pathParam(r, "table")
	out, err := h.listPermView(dsID, table)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"permissions": out})
}

func (h *Handler) listPermView(dsID, table string) ([]map[string]interface{}, error) {
	roles, _ := h.Store.ListRoles()
	nameByID := map[string]string{}
	for _, role := range roles {
		nameByID[role.ID] = role.Name
	}
	perms, err := h.Store.ListTablePermissions(context.Background(), "", dsID, table)
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

// @Summary admin Create Table Permission
// @Tags admin
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "id"
// @Param table path string true "table"
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/datasources/{id}/tables/{table}/permissions [post]
func (h *Handler) AdminCreateTablePermission(w http.ResponseWriter, r *http.Request) {
	dsID, rerr := h.resolveDS(r.Context(), pathParam(r, "id"))
	if rerr != nil {
		writeError(w, http.StatusNotFound, rerr.Error())
		return
	}
	table := pathParam(r, "table")
	var req createPermRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Role == "" || req.Ops == "" {
		writeError(w, http.StatusBadRequest, "role and ops required")
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
		DataSourceID: dsID,
		TableName:    table,
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

// @Summary admin Delete Table Permission
// @Tags admin
// @Produce json
// @Security BearerAuth
// @Param id path string true "id"
// @Param perm path string true "perm"
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/datasources/{id}/permissions/{perm} [delete]
func (h *Handler) AdminDeleteTablePermission(w http.ResponseWriter, r *http.Request) {
	id := pathParam(r, "perm")
	if err := h.Store.DeleteTablePermission(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---- Row policies ----

// @Summary admin List Row Policies
// @Tags admin
// @Produce json
// @Security BearerAuth
// @Param id path string true "id"
// @Param table path string true "table"
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/datasources/{id}/tables/{table}/policies [get]
func (h *Handler) AdminListRowPolicies(w http.ResponseWriter, r *http.Request) {
	dsID, rerr := h.resolveDS(r.Context(), pathParam(r, "id"))
	if rerr != nil {
		writeError(w, http.StatusNotFound, rerr.Error())
		return
	}
	table := pathParam(r, "table")
	roles, _ := h.Store.ListRoles()
	nameByID := map[string]string{}
	for _, role := range roles {
		nameByID[role.ID] = role.Name
	}
	pols, err := h.Store.ListRowPolicies(r.Context(), "", dsID, table)
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
			"table_name": p.TableName,
			"predicate":  p.Predicate,
			"priority":   p.Priority,
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"policies": out})
}

type createPolicyRequest struct {
	Role      string `json:"role"`
	Predicate string `json:"predicate"`
	Priority  int    `json:"priority"`
}

// @Summary admin Create Row Policy
// @Tags admin
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "id"
// @Param table path string true "table"
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/datasources/{id}/tables/{table}/policies [post]
func (h *Handler) AdminCreateRowPolicy(w http.ResponseWriter, r *http.Request) {
	dsID, rerr := h.resolveDS(r.Context(), pathParam(r, "id"))
	if rerr != nil {
		writeError(w, http.StatusNotFound, rerr.Error())
		return
	}
	table := pathParam(r, "table")
	var req createPolicyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Role == "" || req.Predicate == "" {
		writeError(w, http.StatusBadRequest, "role and predicate required")
		return
	}
	role, err := h.Store.GetRole(req.Role)
	if err != nil || role == nil {
		writeError(w, http.StatusNotFound, "role not found")
		return
	}
	p := &store.RowPolicy{
		RoleID:       role.ID,
		DataSourceID: dsID,
		TableName:    table,
		Predicate:    req.Predicate,
		Priority:     req.Priority,
	}
	if err := h.Store.CreateRowPolicy(r.Context(), p); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": p.ID})
}

// @Summary admin Delete Row Policy
// @Tags admin
// @Produce json
// @Security BearerAuth
// @Param id path string true "id"
// @Param policy path string true "policy"
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/datasources/{id}/policies/{policy} [delete]
func (h *Handler) AdminDeleteRowPolicy(w http.ResponseWriter, r *http.Request) {
	id := pathParam(r, "policy")
	if err := h.Store.DeleteRowPolicy(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---- Audit logs ----

// AdminListAudits returns governed-query audit entries, newest first.
// Query params: user, datasource, status, channel, session_id, limit, offset.
// @Summary admin List Audits
// @Tags admin
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/audit [get]
func (h *Handler) AdminListAudits(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.AuditFilter{
		Username:   q.Get("user"),
		DataSource: q.Get("datasource"),
		Status:     q.Get("status"),
		Channel:    q.Get("channel"),
		SessionID:  q.Get("session_id"),
		Limit:      atoiDefault(q.Get("limit"), 50),
		Offset:     atoiDefault(q.Get("offset"), 0),
	}
	logs, total, err := h.Store.ListAudits(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"logs":  logs,
		"total": total,
	})
}

// AdminAuditStats returns aggregate counters (total/ok/denied/error).
// @Summary admin Audit Stats
// @Tags admin
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/audit/stats [get]
func (h *Handler) AdminAuditStats(w http.ResponseWriter, r *http.Request) {
	stats, err := h.Store.AuditStats(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// ---- Security alerts (anomaly detection) ----

// AdminListAlerts returns raised security alerts, newest first.
// Query params: level, resolved (open|resolved), limit, offset.
// @Summary admin List Alerts
// @Tags admin
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/alerts [get]
func (h *Handler) AdminListAlerts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.SecurityAlertFilter{
		Level:    q.Get("level"),
		Resolved: q.Get("resolved"),
		Limit:    atoiDefault(q.Get("limit"), 50),
		Offset:   atoiDefault(q.Get("offset"), 0),
	}
	alerts, total, err := h.Store.ListSecurityAlerts(f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"alerts": alerts,
		"total":  total,
	})
}

// AdminResolveAlert marks an alert as handled.
// @Summary admin Resolve Alert
// @Tags admin
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "id"
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/alerts/{id}/resolve [post]
func (h *Handler) AdminResolveAlert(w http.ResponseWriter, r *http.Request) {
	id := pathParam(r, "id")
	if err := h.Store.ResolveSecurityAlert(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// AdminAlertStats returns aggregate counters for the alert dashboard.
// @Summary admin Alert Stats
// @Tags admin
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/alerts/stats [get]
func (h *Handler) AdminAlertStats(w http.ResponseWriter, r *http.Request) {
	stats, err := h.Store.SecurityAlertStats()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// helpers --------------------------------------------------------------------

func orEmpty(s string) string {
	if s == "" {
		return "null"
	}
	return s
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n := 0
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return def
		}
		n = n*10 + int(ch-'0')
	}
	return n
}
