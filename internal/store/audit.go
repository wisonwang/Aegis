package store

import (
	"context"
	"time"
)

// AuditLog is a single governed-query trace: who, through which channel,
// against which datasource, the original and rewritten SQL, and the outcome.
type AuditLog struct {
	ID           string    `json:"id"`
	TS           time.Time `json:"ts"`
	UserID       string    `json:"user_id"`
	Username     string    `json:"username"`
	Channel      string    `json:"channel"` // dataapi | mcp
	DataSourceID string    `json:"datasource_id"`
	DataSource   string    `json:"datasource"`
	SessionID    string    `json:"session_id"` // links queries from one AI conversation
	SQLText      string    `json:"sql"`
	RewrittenSQL string    `json:"rewritten_sql"`
	Status       string    `json:"status"` // ok | denied | error
	Error        string    `json:"error"`
	RowCount     int       `json:"row_count"`
	DurationMS   int64     `json:"duration_ms"`
}

// AuditFilter narrows ListAudits results. Zero values mean "no filter".
type AuditFilter struct {
	Username   string
	DataSource string
	Status     string
	Channel    string
	SessionID  string
	Limit      int
	Offset     int
}

func (s *Store) InsertAudit(ctx context.Context, a *AuditLog) error {
	if a.ID == "" {
		a.ID = uid()
	}
	if a.TS.IsZero() {
		a.TS = time.Now()
	}
	_, err := s.db.Exec(
		`INSERT INTO audit_logs
		 (id,ts,user_id,username,channel,datasource_id,datasource,session_id,sql_text,rewritten_sql,status,error,row_count,duration_ms,workspace_id)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.TS, a.UserID, a.Username, a.Channel, a.DataSourceID, a.DataSource, a.SessionID,
		a.SQLText, a.RewrittenSQL, a.Status, a.Error, a.RowCount, a.DurationMS, WorkspaceID(ctx))
	return err
}

// ListAudits returns the newest-first audit entries matching the filter,
// plus the total match count for pagination. Scoped to the active workspace
// from ctx unless the caller is a platform admin requesting WorkspaceAll.
func (s *Store) ListAudits(ctx context.Context, f AuditFilter) ([]*AuditLog, int, error) {
	where := ` WHERE 1=1`
	args := []interface{}{}
	if !CrossesWorkspaces(ctx) {
		where += ` AND workspace_id=?`
		args = append(args, WorkspaceID(ctx))
	}
	if f.Username != "" {
		where += ` AND username=?`
		args = append(args, f.Username)
	}
	if f.DataSource != "" {
		where += ` AND datasource=?`
		args = append(args, f.DataSource)
	}
	if f.Status != "" {
		where += ` AND status=?`
		args = append(args, f.Status)
	}
	if f.Channel != "" {
		where += ` AND channel=?`
		args = append(args, f.Channel)
	}
	if f.SessionID != "" {
		where += ` AND session_id=?`
		args = append(args, f.SessionID)
	}

	total := 0
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM audit_logs`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	q := `SELECT id,ts,user_id,username,channel,datasource_id,datasource,session_id,sql_text,rewritten_sql,status,error,row_count,duration_ms
	      FROM audit_logs` + where + ` ORDER BY ts DESC LIMIT ? OFFSET ?`
	args = append(args, limit, f.Offset)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []*AuditLog{}
	for rows.Next() {
		a := &AuditLog{}
		if err := rows.Scan(&a.ID, &a.TS, &a.UserID, &a.Username, &a.Channel,
			&a.DataSourceID, &a.DataSource, &a.SessionID, &a.SQLText, &a.RewrittenSQL,
			&a.Status, &a.Error, &a.RowCount, &a.DurationMS); err != nil {
			return nil, 0, err
		}
		out = append(out, a)
	}
	return out, total, nil
}

// AuditStats returns quick aggregate counters for the dashboard, scoped to the
// active workspace from ctx unless WorkspaceAll is requested.
func (s *Store) AuditStats(ctx context.Context) (map[string]int, error) {
	q := `SELECT status, COUNT(*) FROM audit_logs`
	args := []interface{}{}
	if !CrossesWorkspaces(ctx) {
		q += ` WHERE workspace_id=?`
		args = append(args, WorkspaceID(ctx))
	}
	q += ` GROUP BY status`
	stats := map[string]int{"total": 0, "ok": 0, "denied": 0, "error": 0}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		stats[st] = n
		stats["total"] += n
	}
	return stats, nil
}
