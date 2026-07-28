package store

import (
	"context"
	"testing"
)

func TestApprovalRequest_Lifecycle(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	req := &ApprovalRequest{
		ApplicantID:    "u1",
		ApplicantName:  "Alice",
		DataSourceID:   "ds1",
		DataSourceName: "demo",
		TableName:      "orders",
		RoleName:       "analyst",
		Ops:            "SELECT",
		Justification:  "need read access",
	}
	if err := st.CreateApprovalRequest(context.Background(), req); err != nil {
		t.Fatalf("create: %v", err)
	}
	if req.ID == "" {
		t.Fatal("expected generated id")
	}
	if req.Status != ApprovalPending {
		t.Fatalf("status = %q", req.Status)
	}

	got, err := st.GetApprovalRequest(req.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil || got.TableName != "orders" || got.ApplicantName != "Alice" {
		t.Fatalf("get mismatch: %+v", got)
	}

	pending, err := st.ListApprovalRequests(context.Background(), ApprovalPending, "", "")
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending count = %d", len(pending))
	}

	mine, err := st.ListApprovalRequests(context.Background(), "", "", "u1")
	if err != nil {
		t.Fatalf("list mine: %v", err)
	}
	if len(mine) != 1 {
		t.Fatalf("mine count = %d", len(mine))
	}

	if err := st.ResolveApproval(req.ID, ApprovalApproved, "admin", "Admin", "perm-1"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	got, _ = st.GetApprovalRequest(req.ID)
	if got.Status != ApprovalApproved {
		t.Fatalf("status = %q", got.Status)
	}
	if got.GrantedPermID != "perm-1" {
		t.Fatalf("granted_perm_id = %q", got.GrantedPermID)
	}
	if got.ApproverName != "Admin" {
		t.Fatalf("approver_name = %q", got.ApproverName)
	}
	if got.ResolvedAt.IsZero() {
		t.Fatal("expected resolved_at to be set")
	}

	approved, err := st.ListApprovalRequests(context.Background(), ApprovalApproved, "", "")
	if err != nil {
		t.Fatalf("list approved: %v", err)
	}
	if len(approved) != 1 {
		t.Fatalf("approved count = %d", len(approved))
	}
}

func TestApprovalRequest_FilterByDataSource(t *testing.T) {
	st, _ := Open(":memory:")
	defer st.Close()
	_ = st.CreateApprovalRequest(context.Background(), &ApprovalRequest{ID: "a1", DataSourceID: "ds1", Status: ApprovalPending})
	_ = st.CreateApprovalRequest(context.Background(), &ApprovalRequest{ID: "a2", DataSourceID: "ds2", Status: ApprovalPending})

	list, err := st.ListApprovalRequests(context.Background(), "", "ds1", "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].ID != "a1" {
		t.Fatalf("filtered list = %+v", list)
	}
}
