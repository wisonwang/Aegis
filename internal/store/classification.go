package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// DataClassification is a governance label on a table or column describing how
// sensitive the data is (e.g. pii, confidential, financial). Unlike masks it is
// a property of the data asset itself rather than a per-role rule, and its main
// job is to inform the semantic catalog so AI agents know which columns demand
// care. When ColumnName is empty the row labels the table as a whole.
type DataClassification struct {
	ID           string    `json:"id"`
	DataSourceID string    `json:"datasource_id"`
	TableName    string    `json:"table_name"`
	ColumnName   string    `json:"column_name"` // "" => table-level
	Level        string    `json:"level"`        // public|internal|confidential|restricted|pii
	Tags         string    `json:"tags"`         // JSON array of free-form tags, e.g. ["pii:phone","contact"]
	UpdatedAt    time.Time `json:"updated_at"`
}

// UpsertClassification inserts or updates a classification entry, keyed by
// (datasource_id, table_name, column_name). Scoped to the active workspace.
func (s *Store) UpsertClassification(ctx context.Context, dc *DataClassification) error {
	if dc.ID == "" {
		dc.ID = uid()
	}
	dc.UpdatedAt = time.Now()
	_, err := s.db.Exec(
		`INSERT INTO data_classifications
			(id,datasource_id,table_name,column_name,level,tags,updated_at,workspace_id)
		 VALUES (?,?,?,?,?,?,?,?)
		 ` + s.upsertSuffix("datasource_id,table_name,column_name", []string{"level","tags","updated_at","workspace_id"}) ,
		dc.ID, dc.DataSourceID, dc.TableName, dc.ColumnName,
		dc.Level, dc.Tags, dc.UpdatedAt, WriteWorkspace(ctx))
	return err
}

// ListClassifications returns classification entries for a datasource. When
// table is non-empty it is scoped to that table (both table- and column-level).
// Scoped to the active workspace from ctx.
func (s *Store) ListClassifications(ctx context.Context, dsID, table string) ([]*DataClassification, error) {
	q := `SELECT id,datasource_id,table_name,column_name,level,tags,updated_at
	      FROM data_classifications WHERE datasource_id=?`
	args := []interface{}{dsID}
	if !CrossesWorkspaces(ctx) {
		q += ` AND workspace_id=?`
		args = append(args, WorkspaceID(ctx))
	}
	if table != "" {
		q += ` AND table_name=?`
		args = append(args, table)
	}
	q += ` ORDER BY table_name, column_name`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*DataClassification
	for rows.Next() {
		dc := &DataClassification{}
		if err := rows.Scan(&dc.ID, &dc.DataSourceID, &dc.TableName, &dc.ColumnName,
			&dc.Level, &dc.Tags, &dc.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, dc)
	}
	return out, nil
}

// ClassificationIndex is a fast lookup of a datasource's classifications,
// keyed by lower-cased table name -> lower-cased column name ("" = table level).
type ClassificationIndex map[string]map[string]*DataClassification

// Table returns the table-level classification, or nil.
func (ix ClassificationIndex) Table(table string) *DataClassification {
	if m, ok := ix[strings.ToLower(table)]; ok {
		return m[""]
	}
	return nil
}

// Column returns the classification for a column, or nil.
func (ix ClassificationIndex) Column(table, col string) *DataClassification {
	if m, ok := ix[strings.ToLower(table)]; ok {
		return m[strings.ToLower(col)]
	}
	return nil
}

// ClassificationIndexFor builds a ClassificationIndex for a datasource in one query.
func (s *Store) ClassificationIndexFor(ctx context.Context, dsID string) (ClassificationIndex, error) {
	all, err := s.ListClassifications(ctx, dsID, "")
	if err != nil {
		return nil, err
	}
	ix := ClassificationIndex{}
	for _, dc := range all {
		t := strings.ToLower(dc.TableName)
		if ix[t] == nil {
			ix[t] = map[string]*DataClassification{}
		}
		ix[t][strings.ToLower(dc.ColumnName)] = dc
	}
	return ix, nil
}

// GetClassification returns the entry for a specific table/column, or nil.
func (s *Store) GetClassification(ctx context.Context, dsID, table, column string) (*DataClassification, error) {
	dc := &DataClassification{}
	q := `SELECT id,datasource_id,table_name,column_name,level,tags,updated_at
		 FROM data_classifications WHERE datasource_id=? AND table_name=? AND column_name=?`
	args := []interface{}{dsID, table, column}
	if !CrossesWorkspaces(ctx) {
		q += ` AND workspace_id=?`
		args = append(args, WorkspaceID(ctx))
	}
	err := s.db.QueryRow(q, args...).
		Scan(&dc.ID, &dc.DataSourceID, &dc.TableName, &dc.ColumnName,
			&dc.Level, &dc.Tags, &dc.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return dc, nil
}

// DeleteClassification removes one entry by id, scoped to the caller's workspace.
func (s *Store) DeleteClassification(ctx context.Context, id string) error {
	return s.deleteWorkspaceScoped(ctx, "data_classifications", id)
}

// DeleteClassificationsByTable removes all classifications for a table (used
// when a datasource or dataset is deleted). Scoped to the caller's workspace
// unless running in the cross-workspace admin context.
func (s *Store) DeleteClassificationsByTable(ctx context.Context, dsID, table string) error {
	q := `DELETE FROM data_classifications WHERE datasource_id=? AND table_name=?`
	args := []interface{}{dsID, table}
	if !CrossesWorkspaces(ctx) {
		q += ` AND COALESCE(NULLIF(workspace_id,''),'` + DefaultWorkspaceID + `')=?`
		args = append(args, WorkspaceID(ctx))
	}
	_, err := s.db.Exec(q, args...)
	return err
}
