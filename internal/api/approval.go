package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/wisonwang/aegis/internal/store"
)

var validApprovalOps = map[string]bool{
	"SELECT": true, "INSERT": true, "UPDATE": true, "DELETE": true,
}

// normalizeOps validates and uppercases a comma/space separated op list.
// Returns ("", false) if any op is unknown or the list is empty.
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
	DataSourceID string `json:"datasource_id"`
	TableName    string `json:"table_name"`
	Role         string `json:"role"`
	Ops          string `json:"ops"`
	Justification string `json:"justification"`
}

// UserSubmitApproval lets any authenticated user raise an access-grant request
// (grant a chosen role access to a table on a datasource). The request sits in
// pending until an admin approves/rejects it.
func (h *Handler) UserSubmitApproval(w http.ResponseWriter, r *http.Request) {
	c := claimsFromContext(r.Context())
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
	if err := h.Store.CreateApprovalRequest(r.Context(), ar); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": ar.ID, "status": ar.Status})
}

// UserListMyApprovals returns the current user's own requests.
func (h *Handler) UserListMyApprovals(w http.ResponseWriter, r *http.Request) {
	c := claimsFromContext(r.Context())
	list, err := h.Store.ListApprovalRequests(r.Context(), "", "", c.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"approvals": list})
}

// AdminListApprovals lists all requests (admin). Optional status / datasource filter.
func (h *Handler) AdminListApprovals(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	list, err := h.Store.ListApprovalRequests(r.Context(), q.Get("status"), q.Get("datasource_id"), "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"approvals": list})
}

// AdminApproveApproval approves a pending request and creates the actual
// role->table grant, recording it for later revocation.
func (h *Handler) AdminApproveApproval(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
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
	if err := h.Store.CreateTablePermission(r.Context(), perm); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	c := claimsFromContext(r.Context())
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

// AdminRejectApproval rejects a pending request (no grant created).
func (h *Handler) AdminRejectApproval(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
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
	c := claimsFromContext(r.Context())
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

// AdminRevokeApproval revokes an approved request and removes the grant it
// created, closing the governance loop.
func (h *Handler) AdminRevokeApproval(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
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
	// Remove the grant this approval created, if it still exists.
	if ar.GrantedPermID != "" {
		_ = h.Store.DeleteTablePermission(ar.GrantedPermID)
	}
	c := claimsFromContext(r.Context())
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
