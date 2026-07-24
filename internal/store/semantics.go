package store

import (
	"database/sql"
	"strings"
	"time"
)

// Semantic is a human/business description attached to a table or column of a
// datasource. It is the "semantic layer" that turns a raw physical schema into
// something an LLM can reason about accurately (business meaning, synonyms,
// example values). When ColumnName is empty the row describes the table itself.
type Semantic struct {
	ID           string    `json:"id"`
	DataSourceID string    `json:"datasource_id"`
	TableName    string    `json:"table_name"`
	ColumnName   string    `json:"column_name"` // "" => table-level description
	Description  string    `json:"description"`
	Synonyms     string    `json:"synonyms"` // JSON array of business terms/aliases
	Examples     string    `json:"examples"` // JSON array of example values or phrasings
	UpdatedAt    time.Time `json:"updated_at"`
}

// UpsertSemantic inserts or updates a semantic entry, keyed by
// (datasource_id, table_name, column_name).
func (s *Store) UpsertSemantic(sem *Semantic) error {
	if sem.ID == "" {
		sem.ID = uid()
	}
	sem.UpdatedAt = time.Now()
	_, err := s.db.Exec(
		`INSERT INTO schema_semantics
			(id,datasource_id,table_name,column_name,description,synonyms,examples,updated_at)
		 VALUES (?,?,?,?,?,?,?,?)
		 ON CONFLICT(datasource_id,table_name,column_name) DO UPDATE SET
			description=excluded.description,
			synonyms=excluded.synonyms,
			examples=excluded.examples,
			updated_at=excluded.updated_at`,
		sem.ID, sem.DataSourceID, sem.TableName, sem.ColumnName,
		sem.Description, sem.Synonyms, sem.Examples, sem.UpdatedAt)
	return err
}

// ListSemantics returns all semantic entries for a datasource. When table is
// non-empty it is scoped to that table (both table-level and column-level rows).
func (s *Store) ListSemantics(dsID, table string) ([]*Semantic, error) {
	q := `SELECT id,datasource_id,table_name,column_name,description,synonyms,examples,updated_at
	      FROM schema_semantics WHERE datasource_id=?`
	args := []interface{}{dsID}
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
	var out []*Semantic
	for rows.Next() {
		sem := &Semantic{}
		if err := rows.Scan(&sem.ID, &sem.DataSourceID, &sem.TableName, &sem.ColumnName,
			&sem.Description, &sem.Synonyms, &sem.Examples, &sem.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, sem)
	}
	return out, nil
}

// SemanticIndex is a fast lookup of a datasource's semantics, keyed by
// lower-cased table name -> lower-cased column name ("" = table level).
type SemanticIndex map[string]map[string]*Semantic

// Table returns the table-level semantic, or nil.
func (ix SemanticIndex) Table(table string) *Semantic {
	if m, ok := ix[strings.ToLower(table)]; ok {
		return m[""]
	}
	return nil
}

// Column returns the semantic for a column, or nil.
func (ix SemanticIndex) Column(table, col string) *Semantic {
	if m, ok := ix[strings.ToLower(table)]; ok {
		return m[strings.ToLower(col)]
	}
	return nil
}

// SemanticIndexFor builds a SemanticIndex for a datasource in one query.
func (s *Store) SemanticIndexFor(dsID string) (SemanticIndex, error) {
	all, err := s.ListSemantics(dsID, "")
	if err != nil {
		return nil, err
	}
	ix := SemanticIndex{}
	for _, sem := range all {
		t := strings.ToLower(sem.TableName)
		if ix[t] == nil {
			ix[t] = map[string]*Semantic{}
		}
		ix[t][strings.ToLower(sem.ColumnName)] = sem
	}
	return ix, nil
}

// DeleteSemantic removes one entry by id.
func (s *Store) DeleteSemantic(id string) error {
	_, err := s.db.Exec(`DELETE FROM schema_semantics WHERE id=?`, id)
	return err
}

// GetSemantic returns the entry for a specific table/column, or nil.
func (s *Store) GetSemantic(dsID, table, column string) (*Semantic, error) {
	sem := &Semantic{}
	err := s.db.QueryRow(
		`SELECT id,datasource_id,table_name,column_name,description,synonyms,examples,updated_at
		 FROM schema_semantics WHERE datasource_id=? AND table_name=? AND column_name=?`,
		dsID, table, column).
		Scan(&sem.ID, &sem.DataSourceID, &sem.TableName, &sem.ColumnName,
			&sem.Description, &sem.Synonyms, &sem.Examples, &sem.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return sem, nil
}
