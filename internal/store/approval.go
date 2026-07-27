package store

import (
	"database/sql"
	"time"
)

// Approval status values. The workflow is a closed loop:
// pending -> approved | rejected, and approved -> revoked.
const (
	ApprovalPending  = "pending"
	ApprovalApproved = "approved"
	ApprovalRejected = "rejected"
	ApprovalRevoked  = "revoked"
)

// ApprovalRequest is an access-grant request raised against the RBAC model.
// Governance permissions in Aegis are keyed by *role* (not by user), so the
// request asks to grant a chosen role access to a table on a datasource with a
// set of operations. On approval the platform creates (or re-points) the
// corresponding table_permissions row; on revoke it deletes that row. This
// keeps the loop reversible without touching the user<->role membership graph.
type ApprovalRequest struct {
	ID             string    `json:"id"`
	ApplicantID    string    `json:"applicant_id"`
	ApplicantName  string    `json:"applicant_name"`
	DataSourceID   string    `json:"datasource_id"`
	DataSourceName string    `json:"datasource_name"`
	TableName      string    `json:"table_name"`
	RoleName       string    `json:"role_name"`
	Ops            string    `json:"ops"` // comma separated: SELECT,INSERT,UPDATE,DELETE
	Justification  string    `json:"justification"`
	Status         string    `json:"status"` // pending|approved|rejected|revoked
	ApproverID     string    `json:"approver_id"`
	ApproverName   string    `json:"approver_name"`
	GrantedPermID  string    `json:"granted_perm_id"` // linked table_permissions.id once approved
	CreatedAt      time.Time `json:"created_at"`
	ResolvedAt     time.Time `json:"resolved_at"`
}

// CreateApprovalRequest persists a new pending request.
func (s *Store) CreateApprovalRequest(req *ApprovalRequest) error {
	if req.ID == "" {
		req.ID = uid()
	}
	if req.Status == "" {
		req.Status = ApprovalPending
	}
	if req.CreatedAt.IsZero() {
		req.CreatedAt = time.Now()
	}
	_, err := s.db.Exec(
		`INSERT INTO approval_requests
		 (id, applicant_id, applicant_name, datasource_id, datasource_name,
		  table_name, role_name, ops, justification, status, created_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		req.ID, req.ApplicantID, req.ApplicantName, req.DataSourceID, req.DataSourceName,
		req.TableName, req.RoleName, req.Ops, req.Justification, req.Status, req.CreatedAt)
	return err
}

// GetApprovalRequest fetches a single request by ID, or nil if absent.
func (s *Store) GetApprovalRequest(id string) (*ApprovalRequest, error) {
	q := `SELECT id, applicant_id, applicant_name, datasource_id, datasource_name,
	       table_name, role_name, ops, justification, status, approver_id,
	       approver_name, granted_perm_id, created_at, resolved_at
	      FROM approval_requests WHERE id=?`
	r := &ApprovalRequest{}
	var apprID, apprName, grantID sql.NullString
	var resolvedAt sql.NullTime
	err := s.db.QueryRow(q, id).Scan(
		&r.ID, &r.ApplicantID, &r.ApplicantName, &r.DataSourceID, &r.DataSourceName,
		&r.TableName, &r.RoleName, &r.Ops, &r.Justification, &r.Status,
		&apprID, &apprName, &grantID, &r.CreatedAt, &resolvedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// approver_* / granted_perm_id / resolved_at are NULL until the request is
	// resolved; scan into NULL-safe holders so a pending request doesn't crash
	// the row decoder (same NULL-safety pattern as users.external_id).
	r.ApproverID = apprID.String
	r.ApproverName = apprName.String
	r.GrantedPermID = grantID.String
	r.ResolvedAt = resolvedAt.Time
	return r, nil
}

// ListApprovalRequests returns requests filtered by optional status,
// datasource, and applicant. Empty strings mean "no filter" for that field.
func (s *Store) ListApprovalRequests(status, dsID, applicantID string) ([]*ApprovalRequest, error) {
	q := `SELECT id, applicant_id, applicant_name, datasource_id, datasource_name,
	       table_name, role_name, ops, justification, status, approver_id,
	       approver_name, granted_perm_id, created_at, resolved_at
	      FROM approval_requests WHERE 1=1`
	args := []interface{}{}
	if status != "" {
		q += ` AND status=?`
		args = append(args, status)
	}
	if dsID != "" {
		q += ` AND datasource_id=?`
		args = append(args, dsID)
	}
	if applicantID != "" {
		q += ` AND applicant_id=?`
		args = append(args, applicantID)
	}
	q += ` ORDER BY created_at DESC`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ApprovalRequest
	for rows.Next() {
		r := &ApprovalRequest{}
		var apprID, apprName, grantID sql.NullString
		var resolvedAt sql.NullTime
		if err := rows.Scan(
			&r.ID, &r.ApplicantID, &r.ApplicantName, &r.DataSourceID, &r.DataSourceName,
			&r.TableName, &r.RoleName, &r.Ops, &r.Justification, &r.Status,
			&apprID, &apprName, &grantID, &r.CreatedAt, &resolvedAt); err != nil {
			return nil, err
		}
		r.ApproverID = apprID.String
		r.ApproverName = apprName.String
		r.GrantedPermID = grantID.String
		r.ResolvedAt = resolvedAt.Time
		out = append(out, r)
	}
	return out, nil
}

// ResolveApproval transitions a request to a terminal (approved/rejected/revoked)
// state, recording who acted and (for approval) the created grant ID.
func (s *Store) ResolveApproval(id, status, approverID, approverName, grantedPermID string) error {
	_, err := s.db.Exec(
		`UPDATE approval_requests
		 SET status=?, approver_id=?, approver_name=?, granted_perm_id=?, resolved_at=?
		 WHERE id=?`,
		status, approverID, approverName, grantedPermID, time.Now(), id)
	return err
}
