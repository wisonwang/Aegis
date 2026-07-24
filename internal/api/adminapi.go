package api

import (
	"encoding/json"
	"net/http"

	"github.com/wisonwang/aegis/internal/datasource"
	"github.com/wisonwang/aegis/internal/store"
)

// ---- Users ----

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

func (h *Handler) AdminUpdateUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
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

func (h *Handler) AdminSetPassword(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
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

func (h *Handler) AdminDeleteUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.Store.DeleteUser(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type roleRef struct {
	Role string `json:"role"`
}

func (h *Handler) AdminAddUserRole(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
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

func (h *Handler) AdminRemoveUserRole(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	role, err := h.Store.GetRole(r.PathValue("role"))
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

func (h *Handler) AdminDeleteRole(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.Store.DeleteRole(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---- DataSources ----

func (h *Handler) AdminListDataSources(w http.ResponseWriter, r *http.Request) {
	ds, err := h.Store.ListDataSources()
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
	if err := h.Store.CreateDataSource(d); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": d.ID})
}

func (h *Handler) AdminUpdateDataSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d, err := h.Store.GetDataSource(id)
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

func (h *Handler) AdminDeleteDataSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.Store.DeleteDataSource(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---- Table permissions ----

func (h *Handler) AdminListTablePermissions(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	table := r.PathValue("table")
	out, err := h.listPermView(id, table)
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
	perms, err := h.Store.ListTablePermissions("", dsID, table)
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

func (h *Handler) AdminCreateTablePermission(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	table := r.PathValue("table")
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
		DataSourceID: id,
		TableName:    table,
		Ops:          req.Ops,
		AllowedCols:  string(allowed),
		DeniedCols:   string(denied),
	}
	if err := h.Store.CreateTablePermission(p); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": p.ID})
}

func (h *Handler) AdminDeleteTablePermission(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("perm")
	if err := h.Store.DeleteTablePermission(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---- Row policies ----

func (h *Handler) AdminListRowPolicies(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	table := r.PathValue("table")
	roles, _ := h.Store.ListRoles()
	nameByID := map[string]string{}
	for _, role := range roles {
		nameByID[role.ID] = role.Name
	}
	pols, err := h.Store.ListRowPolicies("", id, table)
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

func (h *Handler) AdminCreateRowPolicy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	table := r.PathValue("table")
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
		DataSourceID: id,
		TableName:    table,
		Predicate:    req.Predicate,
		Priority:     req.Priority,
	}
	if err := h.Store.CreateRowPolicy(p); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": p.ID})
}

func (h *Handler) AdminDeleteRowPolicy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("policy")
	if err := h.Store.DeleteRowPolicy(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---- Audit logs ----

// AdminListAudits returns governed-query audit entries, newest first.
// Query params: user, datasource, status, channel, limit, offset.
func (h *Handler) AdminListAudits(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.AuditFilter{
		Username:   q.Get("user"),
		DataSource: q.Get("datasource"),
		Status:     q.Get("status"),
		Channel:    q.Get("channel"),
		Limit:      atoiDefault(q.Get("limit"), 50),
		Offset:     atoiDefault(q.Get("offset"), 0),
	}
	logs, total, err := h.Store.ListAudits(f)
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
func (h *Handler) AdminAuditStats(w http.ResponseWriter, r *http.Request) {
	stats, err := h.Store.AuditStats()
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
