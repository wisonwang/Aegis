package store

import (
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
			id TEXT PRIMARY KEY, name TEXT UNIQUE NOT NULL, display_name TEXT,
			description TEXT, datasource_id TEXT, definition TEXT, status TEXT,
			fields TEXT, created_at DATETIME, updated_at DATETIME)`,
	}
	for _, st := range stmts {
		if _, err := s.db.Exec(st); err != nil {
			return fmt.Errorf("migrate datasets: %w", err)
		}
	}
	return nil
}

// CreateDataset inserts a new dataset.
func (s *Store) CreateDataset(d *Dataset) error {
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
		`INSERT INTO datasets (id,name,display_name,description,datasource_id,definition,status,fields,created_at,updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?)`,
		d.ID, d.Name, d.DisplayName, d.Description, d.DataSourceID, d.Definition, d.Status, d.Fields, d.CreatedAt, d.UpdatedAt)
	return err
}

// GetDataset returns a dataset by id, or nil if not found.
func (s *Store) GetDataset(id string) (*Dataset, error) {
	return scanDataset(s.db.QueryRow(
		`SELECT id,name,display_name,description,datasource_id,definition,status,fields,created_at,updated_at
		 FROM datasets WHERE id=?`, id))
}

// GetDatasetByName returns a dataset by unique name, or nil if not found.
func (s *Store) GetDatasetByName(name string) (*Dataset, error) {
	return scanDataset(s.db.QueryRow(
		`SELECT id,name,display_name,description,datasource_id,definition,status,fields,created_at,updated_at
		 FROM datasets WHERE name=?`, name))
}

func scanDataset(row *sql.Row) (*Dataset, error) {
	d := &Dataset{}
	err := row.Scan(&d.ID, &d.Name, &d.DisplayName, &d.Description, &d.DataSourceID,
		&d.Definition, &d.Status, &d.Fields, &d.CreatedAt, &d.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return d, nil
}

// ListDatasets returns every dataset, ordered by name.
func (s *Store) ListDatasets() ([]*Dataset, error) {
	rows, err := s.db.Query(
		`SELECT id,name,display_name,description,datasource_id,definition,status,fields,created_at,updated_at
		 FROM datasets ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Dataset
	for rows.Next() {
		d := &Dataset{}
		if err := rows.Scan(&d.ID, &d.Name, &d.DisplayName, &d.Description, &d.DataSourceID,
			&d.Definition, &d.Status, &d.Fields, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// UpdateDataset patches the mutable fields of a dataset. Empty fields are left
// unchanged; only id is required.
func (s *Store) UpdateDataset(d *Dataset) error {
	existing, err := s.GetDataset(d.ID)
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
		`UPDATE datasets SET display_name=?, description=?, definition=?, status=?, fields=?, updated_at=? WHERE id=?`,
		d.DisplayName, d.Description, d.Definition, d.Status, d.Fields, time.Now(), d.ID)
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
func (s *Store) DeleteDataset(id string) error {
	d, err := s.GetDataset(id)
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
