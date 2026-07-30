package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// MetricParam describes one bindable parameter of a curated metric. The type
// constrains what a caller may pass and the engine renders the value as a
// SQL-safe literal, so a metric template can never be SQL-injected through a
// parameter.
type MetricParam struct {
	Name        string      `json:"name"`                  // referenced in SQLTemplate as :name
	Type        string      `json:"type"`                  // string|number|date|bool|enum
	Description string      `json:"description,omitempty"` // human/business meaning for agents
	Enum        []string    `json:"enum,omitempty"`        // allowed values when type=enum
	Required    bool        `json:"required"`              // must be supplied on each run
	Default     interface{} `json:"default,omitempty"`     // used when omitted and not required
}

// MetricDefinition is a curated, governed business metric. It pairs a SQL
// template (with :param placeholders) and typed parameters so an AI agent can
// ask for "monthly_revenue" instead of hand-writing SQL. The template is always
// executed through the governed Execute path, so table/row/column governance,
// masking and audit all still apply.
type MetricDefinition struct {
	ID           string        `json:"id"`
	DataSourceID string        `json:"datasource_id"`
	Name         string        `json:"name"`        // unique per datasource, e.g. "monthly_revenue"
	Description  string        `json:"description"` // business meaning, surfaced to agents
	SQLTemplate  string        `json:"sql_template"` // SELECT ... with :param placeholders
	Params       []MetricParam `json:"params"`
	Unit         string        `json:"unit,omitempty"` // "CNY" | "count" | "%" ...
	CreatedAt    time.Time     `json:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at"`
}

// UpsertMetric inserts or updates a metric definition, keyed by
// (datasource_id, name). Params are persisted as a JSON column. Scoped to the
// active workspace.
func (s *Store) UpsertMetric(ctx context.Context, m *MetricDefinition) error {
	if m.ID == "" {
		m.ID = uid()
	}
	m.UpdatedAt = time.Now()
	if m.CreatedAt.IsZero() {
		m.CreatedAt = m.UpdatedAt
	}
	paramsJSON, err := json.Marshal(m.Params)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`INSERT INTO metric_definitions
			(id,datasource_id,name,description,sql_template,params,unit,created_at,updated_at,workspace_id)
		 VALUES (?,?,?,?,?,?,?,?,?,?)
		 ` + s.upsertSuffix("datasource_id,name", []string{"description","sql_template","params","unit","updated_at","workspace_id"}) ,
		m.ID, m.DataSourceID, m.Name, m.Description, m.SQLTemplate, string(paramsJSON),
		m.Unit, m.CreatedAt, m.UpdatedAt, WriteWorkspace(ctx))
	return err
}

// GetMetric returns the metric for a datasource + name, or nil when absent.
func (s *Store) GetMetric(ctx context.Context, dsID, name string) (*MetricDefinition, error) {
	m := &MetricDefinition{}
	var paramsJSON string
	q := `SELECT id,datasource_id,name,description,sql_template,params,unit,created_at,updated_at
		 FROM metric_definitions WHERE datasource_id=? AND name=?`
	args := []interface{}{dsID, name}
	if !CrossesWorkspaces(ctx) {
		q += ` AND workspace_id=?`
		args = append(args, WorkspaceID(ctx))
	}
	err := s.db.QueryRow(q, args...).
		Scan(&m.ID, &m.DataSourceID, &m.Name, &m.Description, &m.SQLTemplate,
			&paramsJSON, &m.Unit, &m.CreatedAt, &m.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if paramsJSON != "" {
		_ = json.Unmarshal([]byte(paramsJSON), &m.Params)
	}
	return m, nil
}

// ListMetrics returns all metric definitions for a datasource (order by name).
// Scoped to the active workspace from ctx.
func (s *Store) ListMetrics(ctx context.Context, dsID string) ([]*MetricDefinition, error) {
	q := `SELECT id,datasource_id,name,description,sql_template,params,unit,created_at,updated_at
		 FROM metric_definitions WHERE datasource_id=?`
	args := []interface{}{dsID}
	if !CrossesWorkspaces(ctx) {
		q += ` AND workspace_id=?`
		args = append(args, WorkspaceID(ctx))
	}
	q += ` ORDER BY name`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*MetricDefinition
	for rows.Next() {
		m := &MetricDefinition{}
		var paramsJSON string
		if err := rows.Scan(&m.ID, &m.DataSourceID, &m.Name, &m.Description, &m.SQLTemplate,
			&paramsJSON, &m.Unit, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		if paramsJSON != "" {
			_ = json.Unmarshal([]byte(paramsJSON), &m.Params)
		}
		out = append(out, m)
	}
	return out, nil
}

// DeleteMetric removes a metric by id.
func (s *Store) DeleteMetric(id string) error {
	_, err := s.db.Exec(`DELETE FROM metric_definitions WHERE id=?`, id)
	return err
}

// MetricNameFromID is a helper used by callers that receive a metric id in a
// URL path (the MCP/REST run endpoints accept a name, but the admin delete
// endpoint accepts an id). It returns the name for an id, or "" when unknown.
func (s *Store) MetricNameFromID(id string) (string, error) {
	var name string
	err := s.db.QueryRow(`SELECT name FROM metric_definitions WHERE id=?`, id).Scan(&name)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return name, nil
}
