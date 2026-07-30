package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Dataset is a curated, governed, publishable data product built over a
// datasource. Unlike a raw table, a dataset has a stable name (its consumption
// handle), an authored definition (the query that produces it), and a lifecycle
// (draft -> published). Agents consume datasets; the underlying table/row/column
// governance is reused by keying every governance row on table_name = dataset.Name.
type Dataset struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`          // unique, used as the governance key and consumption handle
	DisplayName  string    `json:"display_name"`  // human label
	Description  string    `json:"description"`
	DataSourceID string    `json:"datasource_id"` // which backend the definition queries
	Definition   string    `json:"definition"`    // SQL for SQL-family; JSON query for mongo/es
	Status       string    `json:"status"`        // draft | published
	Fields       string    `json:"fields"`        // JSON array of {name,type,description} — the stable output contract
	FolderID     string    `json:"folder_id"`     // catalog folder id ("" = uncategorized / root)
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// DatasetField is one column of a dataset's output contract.
type DatasetField struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

// DatasetStatus values.
const (
	DatasetDraft     = "draft"
	DatasetPublished = "published"
)

// migrateDatasets adds the datasets table to the control plane.
func migrateDatasets(s *Store) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS datasets (
			id VARCHAR(64) PRIMARY KEY, name VARCHAR(191) UNIQUE NOT NULL, display_name TEXT,
			description TEXT, datasource_id TEXT, definition TEXT, status TEXT,
			fields TEXT, created_at DATETIME, updated_at DATETIME)`,
		`CREATE TABLE IF NOT EXISTS dataset_folders (
			id VARCHAR(64) PRIMARY KEY, workspace_id VARCHAR(191) NOT NULL DEFAULT '',
			name VARCHAR(191) NOT NULL, parent_id VARCHAR(191) NOT NULL DEFAULT '',
			sort_order INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME, updated_at DATETIME,
			UNIQUE(workspace_id, parent_id, name))`,
	}
	for _, st := range stmts {
		if _, err := s.db.Exec(st); err != nil {
			return fmt.Errorf("migrate datasets: %w", err)
		}
	}
	if !columnExists(s, "datasets", "folder_id") {
		if _, err := s.db.Exec(`ALTER TABLE datasets ADD COLUMN folder_id VARCHAR(191) NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("migrate datasets folder_id: %w", err)
		}
	}
	return nil
}

// columnExists reports whether a table has the named column (for idempotent
// ALTER migrations). SQLite uses PRAGMA table_info; MySQL uses
// information_schema.columns.
func columnExists(s *Store, table, col string) bool {
	if s.isMySQL() {
		rows, err := s.db.Query(
			`SELECT COLUMN_NAME FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name=?`, table)
		if err != nil {
			return false
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				continue
			}
			if name == col {
				return true
			}
		}
		return false
	}
	rows, err := s.db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			continue
		}
		if name == col {
			return true
		}
	}
	return false
}

// CreateDataset inserts a new dataset. Scoped to the active workspace from ctx.
func (s *Store) CreateDataset(ctx context.Context, d *Dataset) error {
	if d.ID == "" {
		d.ID = uid()
	}
	now := time.Now()
	if d.CreatedAt.IsZero() {
		d.CreatedAt = now
	}
	if d.UpdatedAt.IsZero() {
		d.UpdatedAt = now
	}
	if d.Status == "" {
		d.Status = DatasetDraft
	}
	if d.Fields == "" {
		d.Fields = "[]"
	}
	_, err := s.db.Exec(
		`INSERT INTO datasets (id,name,display_name,description,datasource_id,definition,status,fields,folder_id,created_at,updated_at,workspace_id)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		d.ID, d.Name, d.DisplayName, d.Description, d.DataSourceID, d.Definition, d.Status, d.Fields, d.FolderID, d.CreatedAt, d.UpdatedAt, WriteWorkspace(ctx))
	return err
}

// GetDataset returns a dataset by id, or nil if not found. Scoped to the active
// workspace from ctx (platform admin may pass WorkspaceAll).
func (s *Store) GetDataset(ctx context.Context, id string) (*Dataset, error) {
	q := `SELECT id,name,display_name,description,datasource_id,definition,status,fields,folder_id,created_at,updated_at
		 FROM datasets WHERE id=?`
	args := []interface{}{id}
	if !CrossesWorkspaces(ctx) {
		q += ` AND workspace_id=?`
		args = append(args, WorkspaceID(ctx))
	}
	return scanDataset(s.db.QueryRow(q, args...))
}

// GetDatasetByName returns a dataset by unique name, or nil if not found.
// Scoped to the active workspace from ctx.
func (s *Store) GetDatasetByName(ctx context.Context, name string) (*Dataset, error) {
	q := `SELECT id,name,display_name,description,datasource_id,definition,status,fields,folder_id,created_at,updated_at
		 FROM datasets WHERE name=?`
	args := []interface{}{name}
	if !CrossesWorkspaces(ctx) {
		q += ` AND workspace_id=?`
		args = append(args, WorkspaceID(ctx))
	}
	return scanDataset(s.db.QueryRow(q, args...))
}

func scanDataset(row *sql.Row) (*Dataset, error) {
	d := &Dataset{}
	err := row.Scan(&d.ID, &d.Name, &d.DisplayName, &d.Description, &d.DataSourceID,
		&d.Definition, &d.Status, &d.Fields, &d.FolderID, &d.CreatedAt, &d.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return d, nil
}

// ListDatasets returns every dataset, ordered by name. Scoped to the active
// workspace from ctx (platform admin may pass WorkspaceAll).
func (s *Store) ListDatasets(ctx context.Context) ([]*Dataset, error) {
	q := `SELECT id,name,display_name,description,datasource_id,definition,status,fields,folder_id,created_at,updated_at
		 FROM datasets`
	args := []interface{}{}
	if !CrossesWorkspaces(ctx) {
		q += ` WHERE workspace_id=?`
		args = append(args, WorkspaceID(ctx))
	}
	q += ` ORDER BY name`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Dataset{}
	for rows.Next() {
		d := &Dataset{}
		if err := rows.Scan(&d.ID, &d.Name, &d.DisplayName, &d.Description, &d.DataSourceID,
			&d.Definition, &d.Status, &d.Fields, &d.FolderID, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// UpdateDataset patches the mutable fields of a dataset. Empty fields are left
// unchanged; only id is required.
func (s *Store) UpdateDataset(ctx context.Context, d *Dataset) error {
	existing, err := s.GetDataset(ctx, d.ID)
	if err != nil {
		return err
	}
	if existing == nil {
		return fmt.Errorf("dataset not found")
	}
	if d.DisplayName == "" {
		d.DisplayName = existing.DisplayName
	}
	if d.Description == "" {
		d.Description = existing.Description
	}
	if d.Definition == "" {
		d.Definition = existing.Definition
	}
	if d.Status == "" {
		d.Status = existing.Status
	}
	if d.Fields == "" {
		d.Fields = existing.Fields
	}
	_, err = s.db.Exec(
		`UPDATE datasets SET display_name=?, description=?, definition=?, status=?, fields=?, folder_id=?, updated_at=? WHERE id=?`,
		d.DisplayName, d.Description, d.Definition, d.Status, d.Fields, d.FolderID, time.Now(), d.ID)
	return err
}

// SetDatasetStatus flips a dataset's lifecycle state.
func (s *Store) SetDatasetStatus(id, status string) error {
	_, err := s.db.Exec(`UPDATE datasets SET status=?, updated_at=? WHERE id=?`, status, time.Now(), id)
	return err
}

// DeleteDataset removes a dataset and cascades all governance keyed on
// (datasource_id, table_name=dataset.Name): permissions, row policies, column
// masks, and semantic descriptions.
func (s *Store) DeleteDataset(ctx context.Context, id string) error {
	d, err := s.GetDataset(ctx, id)
	if err != nil {
		return err
	}
	if d == nil {
		return fmt.Errorf("dataset not found")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM table_permissions WHERE datasource_id=? AND table_name=?`,
		`DELETE FROM row_policies WHERE datasource_id=? AND table_name=?`,
		`DELETE FROM column_masks WHERE datasource_id=? AND table_name=?`,
		`DELETE FROM schema_semantics WHERE datasource_id=? AND table_name=?`,
		`DELETE FROM data_classifications WHERE datasource_id=? AND table_name=?`,
	} {
		if _, err := tx.Exec(q, d.DataSourceID, d.Name); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM datasets WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// DatasetFields parses the dataset's field contract JSON.
func (d *Dataset) DatasetFields() []DatasetField {
	if strings.TrimSpace(d.Fields) == "" {
		return nil
	}
	var out []DatasetField
	if err := json.Unmarshal([]byte(d.Fields), &out); err != nil {
		return nil
	}
	return out
}

// SetDatasetFields serialises a field contract for storage.
func (d *Dataset) SetDatasetFields(fields []DatasetField) error {
	b, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	d.Fields = string(b)
	return nil
}
