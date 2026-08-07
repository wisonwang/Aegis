package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/wisonwang/aegis/internal/datasource"
	"github.com/wisonwang/aegis/internal/store"
)

// ---- Users ----

// @Summary admin List Users
// @Tags admin
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/users [get]
func (h *Handler) AdminListUsers(w http.ResponseWriter, r *http.Request) {
	ws := r.URL.Query().Get("workspace")
	users, err := h.Store.ListUsers(ws)
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
		source := "local"
		if u.ExternalID.Valid && u.ExternalID.String != "" {
			source = "sso"
		}
		lastLogin := ""
		if u.LastLoginAt.Valid {
			lastLogin = u.LastLoginAt.Time.Format(time.RFC3339)
		}
		out = append(out, map[string]interface{}{
			"id":            u.ID,
			"username":      u.Username,
			"display_name":  u.DisplayName,
			"email":         u.Email,
			"type":          u.Type,
			"source":        source,
			"external_id":   u.ExternalID.String,
			"status":        u.Status,
			"last_login_at": lastLogin,
			"attributes":    json.RawMessage(orEmpty(u.Attributes)),
			"roles":         names,
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"users": out})
}

type createUserRequest struct {
	Username    string            `json:"username"`
	DisplayName string            `json:"display_name"`
	Email       string            `json:"email"`
	Type        string            `json:"type"` // human | service (default human)
	Password    string            `json:"password"`
	Workspace   string            `json:"workspace"` // optional: join the user to this workspace on creation
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
	if req.Username == "" {
		writeError(w, http.StatusBadRequest, "username is required")
		return
	}
	userType := req.Type
	if userType == "" {
		userType = "human"
	}
	// Service accounts authenticate via API key only, so a password is not
	// required (and is deliberately left empty so password login fails).
	if userType == "human" && req.Password == "" {
		writeError(w, http.StatusBadRequest, "password is required for human users")
		return
	}
	var hash string
	if req.Password != "" {
		var err error
		hash, err = hashPassword(req.Password)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	attrs := ""
	if len(req.Attributes) > 0 {
		b, _ := json.Marshal(req.Attributes)
		attrs = string(b)
	}
	u := &store.User{
		Username:     req.Username,
		DisplayName:  req.DisplayName,
		Email:        req.Email,
		Type:         userType,
		PasswordHash: hash,
		Attributes:   attrs,
	}
	if err := h.Store.CreateUser(u); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if req.Workspace != "" {
		// Accept either a workspace id or a slug from the client and resolve
		// it to the canonical id before linking — AddWorkspaceMember stores the
		// raw workspace id, so a slug would otherwise create a dangling member.
		ws, werr := h.Store.GetWorkspace(req.Workspace)
		if werr != nil || ws == nil {
			ws, _ = h.Store.GetWorkspaceBySlug(req.Workspace)
		}
		if ws != nil {
			if err := h.Store.AddWorkspaceMember(ws.ID, u.ID, store.WsRoleMember, false); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
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
	Email       string            `json:"email"`
	Type        string            `json:"type"`
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
	if req.Email != "" {
		u.Email = req.Email
	}
	if req.Type != "" {
		u.Type = req.Type
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

// ---- API keys (per-user bearer credentials) ----

type createKeyRequest struct {
	Name      string `json:"name"`
	ExpiresIn string `json:"expires_in"` // optional duration, e.g. "720h"; empty = no expiry
}

// AdminCreateUserAPIKey mints a new API key for any user (admin only). The
// plaintext is returned exactly once in the response.
// @Router /admin/api/users/{id}/apikeys [post]
func (h *Handler) AdminCreateUserAPIKey(w http.ResponseWriter, r *http.Request) {
	id := pathParam(r, "id")
	u, err := h.Store.GetUser(id)
	if err != nil || u == nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	var req createKeyRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	name := req.Name
	if name == "" {
		name = "key"
	}
	var exp time.Time
	if req.ExpiresIn != "" {
		d, derr := time.ParseDuration(req.ExpiresIn)
		if derr != nil {
			writeError(w, http.StatusBadRequest, "invalid expires_in")
			return
		}
		exp = time.Now().Add(d)
	}
	plain, keyID, err := h.Store.CreateAPIKey(id, name, exp)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": keyID, "key": plain, "prefix": plain[:12]})
}

// AdminListUserAPIKeys lists a user's keys (no secret material).
// @Router /admin/api/users/{id}/apikeys [get]
func (h *Handler) AdminListUserAPIKeys(w http.ResponseWriter, r *http.Request) {
	id := pathParam(r, "id")
	keys, err := h.Store.ListAPIKeys(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if keys == nil {
		keys = []*store.APIKey{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"api_keys": keys})
}

// AdminRevokeUserAPIKey revokes one of a user's keys.
// @Router /admin/api/users/{id}/apikeys/{keyId} [delete]
func (h *Handler) AdminRevokeUserAPIKey(w http.ResponseWriter, r *http.Request) {
	id := pathParam(r, "id")
	keyID := pathParam(r, "keyId")
	if err := h.Store.RevokeAPIKey(id, keyID); err != nil {
		if err == store.ErrNotFound {
			writeError(w, http.StatusNotFound, "key not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// MeListAPIKeys lists the caller's own keys.
// @Router /api/v1/me/apikeys [get]
func (h *Handler) MeListAPIKeys(w http.ResponseWriter, r *http.Request) {
	c := claimsFromContext(r.Context())
	keys, err := h.Store.ListAPIKeys(c.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if keys == nil {
		keys = []*store.APIKey{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"api_keys": keys})
}

// MeCreateAPIKey mints a new key for the caller.
// @Router /api/v1/me/apikeys [post]
func (h *Handler) MeCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	c := claimsFromContext(r.Context())
	var req createKeyRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	name := req.Name
	if name == "" {
		name = "key"
	}
	var exp time.Time
	if req.ExpiresIn != "" {
		d, derr := time.ParseDuration(req.ExpiresIn)
		if derr != nil {
			writeError(w, http.StatusBadRequest, "invalid expires_in")
			return
		}
		exp = time.Now().Add(d)
	}
	plain, keyID, err := h.Store.CreateAPIKey(c.UserID, name, exp)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": keyID, "key": plain, "prefix": plain[:12]})
}

// MeRevokeAPIKey revokes one of the caller's own keys.
// @Router /api/v1/me/apikeys/{keyId} [delete]
func (h *Handler) MeRevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	c := claimsFromContext(r.Context())
	keyID := pathParam(r, "keyId")
	if err := h.Store.RevokeAPIKey(c.UserID, keyID); err != nil {
		if err == store.ErrNotFound {
			writeError(w, http.StatusNotFound, "key not found")
			return
		}
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
	if role.System {
		writeError(w, http.StatusForbidden, "cannot grant a system role")
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
	if role.System {
		writeError(w, http.StatusForbidden, "cannot revoke a system role")
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

type updateRoleRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// @Summary admin Update Role
// @Tags admin
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "id"
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/roles/{id} [put]
func (h *Handler) AdminUpdateRole(w http.ResponseWriter, r *http.Request) {
	id := pathParam(r, "id")
	role, err := h.Store.GetRoleByID(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if role == nil {
		writeError(w, http.StatusNotFound, "role not found")
		return
	}
	if role.System {
		writeError(w, http.StatusForbidden, "cannot edit a system role")
		return
	}
	var req updateRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}
	if err := h.Store.UpdateRole(id, req.Name, req.Description); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
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
	role, err := h.Store.GetRoleByID(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if role == nil {
		writeError(w, http.StatusNotFound, "role not found")
		return
	}
	if role.System {
		writeError(w, http.StatusForbidden, "cannot delete a system role")
		return
	}
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
	// Resolve workspace ids to names so the "all workspaces" view can label
	// each row with its owner instead of showing opaque ids.
	wsName := map[string]string{}
	if all, werr := h.Store.ListWorkspaces(); werr == nil {
		for _, ws := range all {
			wsName[ws.ID] = ws.Name
		}
	}
	out := make([]map[string]interface{}, 0, len(ds))
	for _, d := range ds {
		masked := datasource.MaskDSN(d.DSN)
		wsID := d.WorkspaceID
		if wsID == "" {
			wsID = store.DefaultWorkspaceID
		}
		out = append(out, map[string]interface{}{
			"id":             d.ID,
			"name":           d.Name,
			"type":           d.Type,
			"dsn":            masked,
			"dsn_masked":     masked != d.DSN, // true only when a secret was actually redacted
			"created_at":     d.CreatedAt,
			"workspace_id":   wsID,
			"workspace_name": wsName[wsID],
		})
	}
	// dsn_docs points operators to the canonical DSN format reference so a
	// misconfigured connection string is self-serviceable.
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"datasources": out,
		"dsn_docs":    datasource.DSNDocsURL,
	})
}

type createDSRequest struct {
	Name string `json:"name"`
	Type string `json:"type"`
	DSN  string `json:"dsn"`
	// WorkspaceID is the owning workspace. Required when the caller is in the
	// cross-workspace ("all") view, because there is no sane default there —
	// silently falling back to "default" is how governance ends up orphaned.
	WorkspaceID string `json:"workspace_id"`
}

// @Summary admin Create Data Source
// @Tags admin
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/datasources [post]
// @Description The `dsn` is a driver-specific connection string. See the DSN
// format reference (returned as `dsn_docs` from GET /admin/api/datasources) for
// per-type examples. Passwords in `dsn` are never echoed back on list endpoints.
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
	if req.DSN != "" {
		if err := datasource.ValidateDSN(req.Type, req.DSN); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	ctx, werr := h.datasourceWriteCtx(r.Context(), req.WorkspaceID)
	if werr != nil {
		writeError(w, http.StatusBadRequest, werr.Error())
		return
	}
	d := &store.DataSource{Name: req.Name, Type: datasource.NormalizeType(req.Type), DSN: req.DSN}
	if err := h.Store.CreateDataSource(ctx, d); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": d.ID, "workspace_id": store.WriteWorkspace(ctx)})
}

// datasourceWriteCtx decides which workspace a new datasource lands in.
//
//   - explicit workspace_id in the body wins (must exist);
//   - otherwise the caller's active workspace is used;
//   - in the cross-workspace ("all") view there is no active workspace, so an
//     explicit choice is REQUIRED. Defaulting to "default" there is what made
//     every object collapse into a single tenant before ADR-0007.
func (h *Handler) datasourceWriteCtx(ctx context.Context, want string) (context.Context, error) {
	if want == "" || want == store.WorkspaceAll {
		if store.CrossesWorkspaces(ctx) {
			return ctx, errStr("workspace_id is required when creating from the all-workspaces view")
		}
		return ctx, nil
	}
	ws, err := h.Store.GetWorkspace(want)
	if err != nil {
		return ctx, err
	}
	if ws == nil {
		return ctx, errStr("workspace not found: " + want)
	}
	return store.WithWorkspace(ctx, ws.ID), nil
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
	if req.DSN != "" {
		// The admin list returns a masked DSN. If that masked value is pasted
		// straight back into an update we must NOT overwrite the real secret
		// with the placeholder — treat it as "no change".
		if datasource.IsMasked(req.DSN) {
			req.DSN = ""
		} else if err := datasource.ValidateDSN(req.Type, req.DSN); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
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
	// Re-parenting a datasource must drag its whole governance set with it,
	// otherwise the permissions stay behind in the old tenant and silently
	// stop applying to the datasource that still references them.
	if req.WorkspaceID != "" && req.WorkspaceID != store.WorkspaceAll && req.WorkspaceID != d.WorkspaceID {
		ws, werr := h.Store.GetWorkspace(req.WorkspaceID)
		if werr != nil || ws == nil {
			writeError(w, http.StatusBadRequest, "workspace not found: "+req.WorkspaceID)
			return
		}
		if err := h.Store.MoveDataSource(d.ID, ws.ID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
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
	out, err := h.listPermView(r.Context(), dsID, table)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"permissions": out})
}

// listPermView renders table permissions for a datasource. It MUST take the
// request context: using context.Background() here silently pinned the view to
// the "default" workspace, so grants created in any other workspace were
// invisible in the admin UI even though they were live in the engine.
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
			"workspace_id": p.WorkspaceID,
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
	// Bind the write to the datasource's own workspace, not the admin's active
	// view: a grant on a "globex" datasource must never be stamped "default"
	// just because the admin was browsing in the cross-workspace view.
	dsID, ctx, rerr := h.resolveDSBound(r.Context(), pathParam(r, "id"))
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
	if err := h.Store.CreateTablePermission(ctx, p); err != nil {
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
	_, ctx, rerr := h.resolveDSBound(r.Context(), pathParam(r, "id"))
	if rerr != nil {
		writeError(w, http.StatusNotFound, rerr.Error())
		return
	}
	if err := h.Store.DeleteTablePermission(ctx, pathParam(r, "perm")); err != nil {
		writeMutationError(w, err)
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
			"workspace_id": p.WorkspaceID,
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
	dsID, ctx, rerr := h.resolveDSBound(r.Context(), pathParam(r, "id"))
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
	if err := h.Store.CreateRowPolicy(ctx, p); err != nil {
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
	_, ctx, rerr := h.resolveDSBound(r.Context(), pathParam(r, "id"))
	if rerr != nil {
		writeError(w, http.StatusNotFound, rerr.Error())
		return
	}
	if err := h.Store.DeleteRowPolicy(ctx, pathParam(r, "policy")); err != nil {
		writeMutationError(w, err)
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
