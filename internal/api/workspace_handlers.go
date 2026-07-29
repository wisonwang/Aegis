package api

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/wisonwang/aegis/internal/store"
)

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = slugRe.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

// ListMyWorkspaces returns the workspaces the caller is a member of.
// @Summary list My Workspaces
// @Tags workspaces
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/workspaces [get]
func (h *Handler) ListMyWorkspaces(w http.ResponseWriter, r *http.Request) {
	claims := ClaimsFromContext(r.Context())
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	ws, err := h.Store.UserWorkspaces(claims.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if ws == nil {
		ws = []*store.Workspace{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"workspaces": ws})
}

type createWorkspaceRequest struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// AdminCreateWorkspace creates a workspace and makes the caller its admin.
// @Summary admin Create Workspace
// @Tags workspaces
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/workspaces [post]
func (h *Handler) AdminCreateWorkspace(w http.ResponseWriter, r *http.Request) {
	claims := ClaimsFromContext(r.Context())
	var req createWorkspaceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}
	if req.Slug == "" {
		req.Slug = slugify(req.Name)
	}
	ws := &store.Workspace{Name: req.Name, Slug: req.Slug, Settings: "{}"}
	if err := h.Store.CreateWorkspace(ws); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// The creator becomes workspace_admin and it is their default workspace.
	if err := h.Store.AddWorkspaceMember(ws.ID, claims.UserID, store.WsRoleAdmin, true); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, ws)
}

// AdminListWorkspaces returns all workspaces (admin only).
// @Summary admin List Workspaces
// @Tags workspaces
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/workspaces [get]
func (h *Handler) AdminListWorkspaces(w http.ResponseWriter, r *http.Request) {
	ws, err := h.Store.ListWorkspaces()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if ws == nil {
		ws = []*store.Workspace{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"workspaces": ws})
}

// AdminGetWorkspace returns a single workspace (admin may read any).
// @Summary admin Get Workspace
// @Tags workspaces
// @Produce json
// @Security BearerAuth
// @Param id path string true "id"
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/workspaces/{id} [get]
func (h *Handler) AdminGetWorkspace(w http.ResponseWriter, r *http.Request) {
	id := pathParam(r, "id")
	ws, err := h.Store.GetWorkspace(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if ws == nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	writeJSON(w, http.StatusOK, ws)
}

// AdminDeleteWorkspace deletes a workspace. The platform default workspace is
// protected — it can never be removed (it is the upgrade target for
// single-tenant deployments).
// @Summary admin Delete Workspace
// @Tags workspaces
// @Produce json
// @Security BearerAuth
// @Param id path string true "id"
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/workspaces/{id} [delete]
func (h *Handler) AdminDeleteWorkspace(w http.ResponseWriter, r *http.Request) {
	id := pathParam(r, "id")
	if id == store.DefaultWorkspaceID {
		writeError(w, http.StatusBadRequest, "cannot delete the default workspace")
		return
	}
	if err := h.Store.DeleteWorkspace(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted", "id": id})
}

type memberRequest struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
}

// AdminAddWorkspaceMember invites a user into a workspace with a workspace role.
// @Summary admin Add Workspace Member
// @Tags workspaces
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "id"
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/workspaces/{id}/members [post]
func (h *Handler) AdminAddWorkspaceMember(w http.ResponseWriter, r *http.Request) {
	wsID := pathParam(r, "id")
	var req memberRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.UserID == "" {
		writeError(w, http.StatusBadRequest, "user_id required")
		return
	}
	role := req.Role
	if role == "" {
		role = store.WsRoleMember
	}
	if role != store.WsRoleAdmin && role != store.WsRoleMember && role != store.WsRoleViewer {
		writeError(w, http.StatusBadRequest, "invalid workspace role")
		return
	}
	if err := h.Store.AddWorkspaceMember(wsID, req.UserID, role, false); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"status": "added", "workspace_id": wsID, "user_id": req.UserID, "role": role})
}

// AdminListWorkspaceMembers lists the members of a workspace.
// @Summary admin List Workspace Members
// @Tags workspaces
// @Produce json
// @Security BearerAuth
// @Param id path string true "id"
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/workspaces/{id}/members [get]
func (h *Handler) AdminListWorkspaceMembers(w http.ResponseWriter, r *http.Request) {
	wsID := pathParam(r, "id")
	members, err := h.Store.ListWorkspaceMembers(wsID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if members == nil {
		members = []*store.WorkspaceMember{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"members": members})
}

// AdminRemoveWorkspaceMember removes a user from a workspace.
// @Summary admin Remove Workspace Member
// @Tags workspaces
// @Produce json
// @Security BearerAuth
// @Param id path string true "id"
// @Param user_id path string true "user_id"
// @Success 200 {object} map[string]interface{}
// @Router /admin/api/workspaces/{id}/members/{user_id} [delete]
func (h *Handler) AdminRemoveWorkspaceMember(w http.ResponseWriter, r *http.Request) {
	wsID := pathParam(r, "id")
	userID := pathParam(r, "user_id")
	if err := h.Store.RemoveWorkspaceMember(wsID, userID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed", "workspace_id": wsID, "user_id": userID})
}
