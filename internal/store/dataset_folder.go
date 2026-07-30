package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// DatasetFolder is a node in the dataset catalog tree. The tree is a free-form
// hierarchy (parent_id self-reference, "" = root) scoped per workspace. A
// dataset references at most one folder via datasets.folder_id, so folders are
// purely organizational metadata — moving a dataset between folders never
// touches its name (the stable consumption handle) or its governance rows.
type DatasetFolder struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Name        string    `json:"name"`
	ParentID    string    `json:"parent_id"` // "" = root
	SortOrder   int       `json:"sort_order"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// CreateFolder inserts a new catalog folder. The (workspace, parent, name)
// triple is unique, so two siblings cannot share a name.
func (s *Store) CreateFolder(ctx context.Context, f *DatasetFolder) error {
	if f.ID == "" {
		f.ID = uid()
	}
	now := time.Now()
	if f.CreatedAt.IsZero() {
		f.CreatedAt = now
	}
	if f.UpdatedAt.IsZero() {
		f.UpdatedAt = now
	}
	if f.Name == "" {
		return fmt.Errorf("folder name required")
	}
	ws := WriteWorkspace(ctx)
	var cnt int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM dataset_folders WHERE workspace_id=? AND parent_id=? AND name=?`,
		ws, f.ParentID, f.Name).Scan(&cnt); err != nil {
		return err
	}
	if cnt > 0 {
		return fmt.Errorf("a folder with this name already exists here")
	}
	_, err := s.db.Exec(
		`INSERT INTO dataset_folders (id, workspace_id, name, parent_id, sort_order, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,?)`,
		f.ID, ws, f.Name, f.ParentID, f.SortOrder, f.CreatedAt, f.UpdatedAt)
	return err
}

// GetFolder returns a folder by id (workspace-scoped unless the caller crosses
// workspaces), or nil if not found.
func (s *Store) GetFolder(ctx context.Context, id string) (*DatasetFolder, error) {
	q := `SELECT id, workspace_id, name, parent_id, sort_order, created_at, updated_at
		 FROM dataset_folders WHERE id=?`
	args := []interface{}{id}
	if !CrossesWorkspaces(ctx) {
		q += ` AND workspace_id=?`
		args = append(args, WorkspaceID(ctx))
	}
	row := s.db.QueryRow(q, args...)
	f := &DatasetFolder{}
	err := row.Scan(&f.ID, &f.WorkspaceID, &f.Name, &f.ParentID, &f.SortOrder, &f.CreatedAt, &f.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return f, nil
}

// ListFolders returns every folder in the active workspace, ordered for stable
// tree construction (parent-first, then sort order, then name).
func (s *Store) ListFolders(ctx context.Context) ([]*DatasetFolder, error) {
	q := `SELECT id, workspace_id, name, parent_id, sort_order, created_at, updated_at FROM dataset_folders`
	args := []interface{}{}
	if !CrossesWorkspaces(ctx) {
		q += ` WHERE workspace_id=?`
		args = append(args, WorkspaceID(ctx))
	}
	q += ` ORDER BY parent_id, sort_order, name`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*DatasetFolder{}
	for rows.Next() {
		f := &DatasetFolder{}
		if err := rows.Scan(&f.ID, &f.WorkspaceID, &f.Name, &f.ParentID, &f.SortOrder, &f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// UpdateFolder renames and/or reparents a folder. Reparenting rejects cycles
// (a folder cannot be moved into its own descendant) and duplicate sibling names.
func (s *Store) UpdateFolder(ctx context.Context, id, name, parentID string) error {
	existing, err := s.GetFolder(ctx, id)
	if err != nil {
		return err
	}
	if existing == nil {
		return fmt.Errorf("folder not found")
	}
	newParent := existing.ParentID
	if parentID != "" && parentID != existing.ParentID {
		p, err := s.GetFolder(ctx, parentID)
		if err != nil {
			return err
		}
		if p == nil {
			return fmt.Errorf("parent folder not found")
		}
		sub, err := s.folderSubtreeIDs(ctx, id)
		if err != nil {
			return err
		}
		if sub[parentID] {
			return fmt.Errorf("cannot move a folder into its own descendant")
		}
		newParent = parentID
	}
	newName := name
	if newName == "" {
		newName = existing.Name
	}
	var cnt int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM dataset_folders WHERE workspace_id=? AND parent_id=? AND name=? AND id<>?`,
		existing.WorkspaceID, newParent, newName, id).Scan(&cnt); err != nil {
		return err
	}
	if cnt > 0 {
		return fmt.Errorf("a folder with this name already exists here")
	}
	_, err = s.db.Exec(`UPDATE dataset_folders SET name=?, parent_id=?, updated_at=? WHERE id=?`,
		newName, newParent, time.Now(), id)
	return err
}

// DeleteFolder removes a folder. It refuses to delete a non-empty folder
// (containing subfolders or datasets) so datasets are never orphaned silently.
func (s *Store) DeleteFolder(ctx context.Context, id string) error {
	existing, err := s.GetFolder(ctx, id)
	if err != nil {
		return err
	}
	if existing == nil {
		return fmt.Errorf("folder not found")
	}
	var childCnt int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM dataset_folders WHERE parent_id=?`, id).Scan(&childCnt); err != nil {
		return err
	}
	if childCnt > 0 {
		return fmt.Errorf("folder is not empty: it contains subfolders")
	}
	var dsCnt int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM datasets WHERE folder_id=?`, id).Scan(&dsCnt); err != nil {
		return err
	}
	if dsCnt > 0 {
		return fmt.Errorf("folder is not empty: it contains datasets")
	}
	_, err = s.db.Exec(`DELETE FROM dataset_folders WHERE id=?`, id)
	return err
}

// MoveDataset assigns a dataset to a catalog folder ("" = uncategorized). The
// target folder must belong to the same workspace as the dataset.
func (s *Store) MoveDataset(ctx context.Context, datasetID, folderID string) error {
	d, err := s.GetDataset(ctx, datasetID)
	if err != nil {
		return err
	}
	if d == nil {
		return fmt.Errorf("dataset not found")
	}
	if folderID != "" {
		f, err := s.GetFolder(ctx, folderID)
		if err != nil {
			return err
		}
		if f == nil {
			return fmt.Errorf("folder not found")
		}
	}
	_, err = s.db.Exec(`UPDATE datasets SET folder_id=?, updated_at=? WHERE id=?`, folderID, time.Now(), datasetID)
	return err
}

// folderSubtreeIDs returns the set of folder ids rooted at rootID (inclusive),
// computed from the workspace's flat folder list.
func (s *Store) folderSubtreeIDs(ctx context.Context, rootID string) (map[string]bool, error) {
	folders, err := s.ListFolders(ctx)
	if err != nil {
		return nil, err
	}
	children := map[string][]string{}
	for _, f := range folders {
		children[f.ParentID] = append(children[f.ParentID], f.ID)
	}
	set := map[string]bool{rootID: true}
	stack := []string{rootID}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, c := range children[cur] {
			if !set[c] {
				set[c] = true
				stack = append(stack, c)
			}
		}
	}
	return set, nil
}

// ListDatasetsByFolder returns datasets filtered by folder. An empty folderID
// returns all datasets; recursive expands the folder's subtree.
func (s *Store) ListDatasetsByFolder(ctx context.Context, folderID string, recursive bool) ([]*Dataset, error) {
	if folderID == "" {
		return s.ListDatasets(ctx)
	}
	all, err := s.ListDatasets(ctx)
	if err != nil {
		return nil, err
	}
	if !recursive {
		out := []*Dataset{}
		for _, d := range all {
			if d.FolderID == folderID {
				out = append(out, d)
			}
		}
		return out, nil
	}
	sub, err := s.folderSubtreeIDs(ctx, folderID)
	if err != nil {
		return nil, err
	}
	out := []*Dataset{}
	for _, d := range all {
		if sub[d.FolderID] {
			out = append(out, d)
		}
	}
	return out, nil
}
