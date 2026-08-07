package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Workspace isolation (ADR-001)
//
// Aegis is single-binary and multi-tenant via a shared schema: every governed
// control-plane object carries a `workspace_id` discriminator column, and the
// repository layer is the single injection point that scopes all reads/writes
// to the active workspace resolved from the request context. Platform admins
// may opt into a cross-workspace view using the WorkspaceAll sentinel.
//
// Threading model: workspace_id flows through context.Context only. Store
// methods that touch governed tables take ctx as their first argument and read
// WorkspaceID(ctx). This keeps the scope impossible to forget at the call site
// and centralizes the "WHERE workspace_id = ?" rule in one place.

type ctxKey int

const wsCtxKey ctxKey = iota

// WorkspaceAll is the sentinel workspace id a platform admin passes (via
// X-Workspace-Id: *) to read across every workspace. It is never persisted as a
// real workspace id.
const WorkspaceAll = "*"

// DefaultWorkspaceID is the workspace every pre-ADR single-tenant deployment is
// transparently upgraded into. Behavior is unchanged for such deployments.
const DefaultWorkspaceID = "default"

// WithWorkspace returns a context carrying the active workspace id.
func WithWorkspace(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, wsCtxKey, id)
}

// WorkspaceID extracts the active workspace id from context. When unset it
// returns DefaultWorkspaceID so that un-upgraded paths keep working.
func WorkspaceID(ctx context.Context) string {
	if v, ok := ctx.Value(wsCtxKey).(string); ok && v != "" {
		return v
	}
	return DefaultWorkspaceID
}

// CrossesWorkspaces reports whether the context requests the platform-admin
// cross-workspace (all workspaces) view.
func CrossesWorkspaces(ctx context.Context) bool {
	v, ok := ctx.Value(wsCtxKey).(string)
	return ok && v == WorkspaceAll
}

// WriteWorkspace returns the workspace id to stamp on a created or updated
// governed object. It is identical to WorkspaceID except it never returns the
// cross-workspace sentinel "*": an admin writing without an explicit target
// workspace (i.e. resolved to WorkspaceAll) falls back to the default
// workspace rather than persisting the impossible "*" discriminator, which
// would make the object invisible to every scoped reader (ADR-001).
func WriteWorkspace(ctx context.Context) string {
	if CrossesWorkspaces(ctx) {
		return DefaultWorkspaceID
	}
	return WorkspaceID(ctx)
}

// BoundContext pins ctx to the datasource's own workspace.
//
// This is the anchor rule of ADR-0007: every governance object (table
// permission, row policy, column mask, semantic, classification, dataset)
// belongs to the workspace of the datasource it governs — NOT to whichever
// workspace the operator happens to be viewing. Without this, an admin working
// in the cross-workspace view would stamp "default" on governance for a
// datasource that lives in another tenant, silently orphaning the rule.
//
// The result is always a concrete workspace id, never "*".
func (d *DataSource) BoundContext(ctx context.Context) context.Context {
	ws := d.WorkspaceID
	if ws == "" || ws == WorkspaceAll {
		ws = DefaultWorkspaceID
	}
	return WithWorkspace(ctx, ws)
}

// Workspace is a tenant boundary within a single Aegis deployment.
type Workspace struct {
	ID        string
	Name      string
	Slug      string
	Settings  string // JSON
	CreatedAt time.Time
}

// WorkspaceMember links a (global) user to a workspace with a workspace-scoped
// role.
type WorkspaceMember struct {
	WorkspaceID string
	UserID      string
	Role        string // workspace_admin | member | viewer
	IsDefault   bool
	CreatedAt   time.Time
}

// Workspace roles (distinct from platform roles like admin).
const (
	WsRoleAdmin  = "workspace_admin"
	WsRoleMember = "member"
	WsRoleViewer = "viewer"
)

// wsRoleIDPrefix namespaces the ids of the three seeded workspace-role rows.
//
// roles.name carries a UNIQUE constraint, so workspace roles cannot reuse the
// platform name space: an IdP that maps a group to a platform role called
// "viewer" would otherwise collide with the workspace "viewer" membership
// level. Workspace roles are therefore addressed by their deterministic id
// (ws-role:<level>) and their roles.name is prefixed "ws:" purely to satisfy
// the unique index — the display value is always derived from the id.
const wsRoleIDPrefix = "ws-role:"

// wsRoleNamePrefix keeps the seeded rows out of the platform name space.
const wsRoleNamePrefix = "ws:"

// workspaceRoleID resolves a workspace-role level (one of WsRole*) to the
// roles.id of its seeded workspace-scoped row (ADR-0006). This is the bridge
// that lets the unified role_assignments table store workspace roles by id
// instead of by the free-text string the legacy workspace_members used.
func (s *Store) workspaceRoleID(level string) (string, error) {
	switch level {
	case WsRoleAdmin, WsRoleMember, WsRoleViewer:
	default:
		return "", fmt.Errorf("store: unknown workspace role %q", level)
	}
	id := wsRoleIDPrefix + level
	var got string
	err := s.db.QueryRow(`SELECT id FROM roles WHERE id=? AND scope='workspace'`, id).Scan(&got)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("store: workspace role %q not seeded", level)
	}
	if err != nil {
		return "", err
	}
	return got, nil
}

// workspaceRoleLevel is the inverse: it turns a seeded role id back into the
// level string the API and UI speak (workspace_admin | member | viewer).
func workspaceRoleLevel(roleID string) string {
	return strings.TrimPrefix(roleID, wsRoleIDPrefix)
}

func (s *Store) CreateWorkspace(w *Workspace) error {
	if w.ID == "" {
		w.ID = uid()
	}
	if w.CreatedAt.IsZero() {
		w.CreatedAt = time.Now()
	}
	_, err := s.db.Exec(
		`INSERT INTO workspaces (id,name,slug,settings,created_at) VALUES (?,?,?,?,?)`,
		w.ID, w.Name, w.Slug, w.Settings, w.CreatedAt)
	return err
}

func (s *Store) GetWorkspace(id string) (*Workspace, error) {
	w := &Workspace{}
	err := s.db.QueryRow(
		`SELECT id,name,slug,settings,created_at FROM workspaces WHERE id=?`, id).
		Scan(&w.ID, &w.Name, &w.Slug, &w.Settings, &w.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return w, nil
}

// DeleteWorkspace removes a workspace and its membership links. Governed
// objects that carry workspace_id are NOT cascaded here (they are scoped by
// the id, which simply becomes unresolvable) — callers must not delete the
// default workspace.
func (s *Store) DeleteWorkspace(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM workspace_members WHERE workspace_id=?`, id); err != nil {
		tx.Rollback()
		return err
	}
	if _, err := tx.Exec(`DELETE FROM role_assignments WHERE workspace_id=?`, id); err != nil {
		tx.Rollback()
		return err
	}
	if _, err := tx.Exec(`DELETE FROM workspaces WHERE id=?`, id); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *Store) GetWorkspaceBySlug(slug string) (*Workspace, error) {
	w := &Workspace{}
	err := s.db.QueryRow(
		`SELECT id,name,slug,settings,created_at FROM workspaces WHERE slug=?`, slug).
		Scan(&w.ID, &w.Name, &w.Slug, &w.Settings, &w.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return w, nil
}

func (s *Store) ListWorkspaces() ([]*Workspace, error) {
	rows, err := s.db.Query(`SELECT id,name,slug,settings,created_at FROM workspaces ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Workspace
	for rows.Next() {
		w := &Workspace{}
		if err := rows.Scan(&w.ID, &w.Name, &w.Slug, &w.Settings, &w.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, nil
}

// AddWorkspaceMember links a user to a workspace with exactly one membership
// level. isDefault marks the user's primary workspace (used when no explicit
// X-Workspace-Id is provided).
//
// Set-semantics, not append: the legacy workspace_members table was keyed on
// (workspace_id, user_id), so a user could only ever hold one level. The
// unified role_assignments key includes role_id, which would silently let a
// "change role" call stack workspace_admin AND member on the same user — the
// levels are a hierarchy, not a set, so the previous level is replaced.
func (s *Store) AddWorkspaceMember(wsID, userID, role string, isDefault bool) error {
	if wsID == "" || userID == "" {
		return errors.New("store: workspace and user id required")
	}
	if role == "" {
		role = WsRoleMember
	}
	roleID, err := s.workspaceRoleID(role)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Preserve the original join time when this is a level change, not a new
	// membership — the member list is ordered by it.
	// ORDER BY + LIMIT rather than MIN(): the SQLite driver loses the column's
	// declared type through an aggregate and hands back a string.
	var created sql.NullTime
	if err := tx.QueryRow(
		`SELECT created_at FROM role_assignments
		 WHERE workspace_id=? AND principal_id=? ORDER BY created_at LIMIT 1`,
		wsID, userID).Scan(&created); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	joinedAt := time.Now()
	if created.Valid && !created.Time.IsZero() {
		joinedAt = created.Time
	}
	if _, err := tx.Exec(
		`DELETE FROM role_assignments WHERE workspace_id=? AND principal_id=?`, wsID, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`INSERT INTO role_assignments (principal_id,role_id,workspace_id,is_default,created_at) VALUES (?,?,?,?,?)`,
		userID, roleID, wsID, isDefault, joinedAt); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListWorkspaceMembers(wsID string) ([]*WorkspaceMember, error) {
	// The membership level is derived from role_id, not roles.name: name is
	// prefixed to dodge the platform name space (see wsRoleIDPrefix), so the
	// id is the authoritative source for the value the API exposes.
	rows, err := s.db.Query(
		`SELECT ra.workspace_id, ra.principal_id, ra.role_id, ra.is_default, ra.created_at
		 FROM role_assignments ra
		 WHERE ra.workspace_id=? ORDER BY ra.created_at`,
		wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*WorkspaceMember
	for rows.Next() {
		m := &WorkspaceMember{}
		// created_at is nullable in role_assignments (rows backfilled by
		// migration 0002 predate the timestamp). Scan defensively so a NULL
		// degrades to the zero time instead of failing the whole listing.
		var roleID string
		var created sql.NullTime
		if err := rows.Scan(&m.WorkspaceID, &m.UserID, &roleID, &m.IsDefault, &created); err != nil {
			return nil, err
		}
		m.Role = workspaceRoleLevel(roleID)
		if created.Valid {
			m.CreatedAt = created.Time
		}
		out = append(out, m)
	}
	return out, nil
}

func (s *Store) RemoveWorkspaceMember(wsID, userID string) error {
	_, err := s.db.Exec(
		`DELETE FROM role_assignments WHERE workspace_id=? AND principal_id=?`, wsID, userID)
	return err
}

// DefaultWorkspaceForUser returns the user's default workspace id, or the
// platform default when the user has no membership (e.g. a freshly provisioned
// SSO user before group mapping runs).
func (s *Store) DefaultWorkspaceForUser(userID string) (string, error) {
	var id string
	err := s.db.QueryRow(
		`SELECT workspace_id FROM role_assignments WHERE principal_id=? AND is_default=1 LIMIT 1`, userID).
		Scan(&id)
	if err == sql.ErrNoRows {
		err = s.db.QueryRow(
			`SELECT workspace_id FROM role_assignments WHERE principal_id=? LIMIT 1`, userID).
			Scan(&id)
	}
	if err == sql.ErrNoRows {
		return DefaultWorkspaceID, nil
	}
	if err != nil {
		return "", err
	}
	return id, nil
}

// UserWorkspaces returns the workspaces a user is a member of.
func (s *Store) UserWorkspaces(userID string) ([]*Workspace, error) {
	// DISTINCT is defensive: AddWorkspaceMember enforces one level per user per
	// workspace, but a stray duplicate must never make a workspace appear twice
	// in the user's switcher.
	rows, err := s.db.Query(
		`SELECT DISTINCT w.id,w.name,w.slug,w.settings,w.created_at
		 FROM workspaces w JOIN role_assignments ra ON ra.workspace_id=w.id
		 WHERE ra.principal_id=? ORDER BY w.name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Workspace
	for rows.Next() {
		w := &Workspace{}
		if err := rows.Scan(&w.ID, &w.Name, &w.Slug, &w.Settings, &w.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, nil
}

// IsWorkspaceMember reports whether userID belongs to wsID.
func (s *Store) IsWorkspaceMember(wsID, userID string) (bool, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM role_assignments WHERE workspace_id=? AND principal_id=?`, wsID, userID).
		Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// EnsureDefaultMembership gives a user membership in the default workspace if
// they have none yet. Used during login / SSO provisioning so every principal
// resolves to a workspace (fail-closed resolver depends on this).
func (s *Store) EnsureDefaultMembership(userID string) error {
	ok, err := s.IsWorkspaceMember(DefaultWorkspaceID, userID)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	return s.AddWorkspaceMember(DefaultWorkspaceID, userID, WsRoleMember, true)
}

// ResolveWorkspaceID maps a request's workspace hint plus the caller's identity
// to the concrete workspace id that should scope the request (ADR-001). It is
// the single decision point shared by the HTTP WorkspaceResolver and the MCP
// handler so both surfaces enforce identical isolation rules.
//
// Rules:
//   - isAdmin: an explicit "*" (or empty hint) means the cross-workspace view;
//     an explicit id is allowed (admin may reach any workspace).
//   - non-admin: "*" is rejected; empty falls back to the user's default
//     workspace; a specific id is allowed only with confirmed membership.
//
// Any rejection returns an error so the caller can respond fail-closed (403).
func (s *Store) ResolveWorkspaceID(userID string, isAdmin bool, headerWS string) (string, error) {
	if isAdmin {
		if headerWS == "" || headerWS == WorkspaceAll {
			return WorkspaceAll, nil
		}
		return headerWS, nil
	}
	if headerWS == WorkspaceAll {
		return "", fmt.Errorf("cross-workspace access requires admin")
	}
	if headerWS == "" {
		if def, err := s.DefaultWorkspaceForUser(userID); err == nil && def != "" {
			return def, nil
		}
		return DefaultWorkspaceID, nil
	}
	ok, err := s.IsWorkspaceMember(headerWS, userID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("not a member of workspace %s", headerWS)
	}
	return headerWS, nil
}
