package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/wisonwang/aegis/internal/auth"
	"github.com/wisonwang/aegis/internal/datasource"
	"github.com/wisonwang/aegis/internal/metrics"
	"github.com/wisonwang/aegis/internal/permission"
	"github.com/wisonwang/aegis/internal/store"
)

// DatasetInfo is a lightweight, governance-filtered view of a dataset that the
// platform exposes for discovery (e.g. in the dataset list).
type DatasetInfo struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	DisplayName    string `json:"display_name"`
	Description    string `json:"description"`
	Type           string `json:"type"` // datasource backend type (mysql/sqlite/mongo/...)
	DataSourceID   string `json:"datasource_id"`
	DataSourceName string `json:"datasource_name"`
	Status         string `json:"status"`
}

// DatasetSchema is the governed, semantically enriched contract of a dataset —
// the stable columns an agent may consume, with business meaning and masking.
type DatasetSchema struct {
	DatasetID      string            `json:"dataset_id"`
	Name           string            `json:"name"`
	DisplayName    string            `json:"display_name"`
	Description    string            `json:"description"`
	DataSourceID   string            `json:"datasource_id"`
	DataSourceName string            `json:"datasource_name"`
	Type           string            `json:"type"`
	Status         string            `json:"status"`
	Fields         []SemanticColumn  `json:"fields"`
}

// ExecuteDataset runs a governed dataset query and returns safe, masked rows.
// A dataset is a read-only curated view, so write-protection is not applicable.
// The dataset must be published (or the caller is admin) and the caller must
// hold a SELECT grant on it.
func (p *Proxy) ExecuteDataset(ctx context.Context, datasetID string, claims *auth.Claims, params []interface{}) (*QueryResult, error) {
	started := time.Now()

	dsMeta, err := p.store.GetDataset(ctx, datasetID)
	if err != nil {
		return nil, err
	}
	if dsMeta == nil {
		return nil, fmt.Errorf("dataset not found")
	}
	if dsMeta.Status != store.DatasetPublished && !claims.IsAdmin() {
		return nil, fmt.Errorf("dataset %q is not published", dsMeta.Name)
	}
	ds, err := p.store.GetDataSource(ctx, dsMeta.DataSourceID)
	if err != nil || ds == nil {
		return nil, fmt.Errorf("dataset's datasource not found")
	}

	// Behavior governance: rate limit + per-query timeout (admin may be exempt).
	limited := p.guard != nil && !(p.guard.AdminExempt && claims.IsAdmin())
	if limited {
		if !p.guard.Allow(claims.UserID) {
			err := fmt.Errorf("rate limit exceeded: max %d queries/min per principal", p.guard.RatePerMin)
			p.auditDataset(ctx, dsMeta, claims, "rate-limit", "", "denied", err.Error(), 0, started)
			return nil, err
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.guard.Timeout)
		defer cancel()
	}

	if datasource.IsNoSQL(ds.Type) {
		return p.executeDatasetNoSQL(ctx, dsMeta, ds, claims, started, limited)
	}
	return p.executeDatasetSQL(ctx, dsMeta, ds, claims, params, started, limited)
}

func (p *Proxy) executeDatasetSQL(ctx context.Context, dsMeta *store.Dataset, ds *store.DataSource, claims *auth.Claims, params []interface{}, started time.Time, limited bool) (*QueryResult, error) {
	perms, err := p.store.ResolvePermissions(ctx, claims.UserID, dsMeta.DataSourceID)
	if err != nil {
		p.auditDataset(ctx, dsMeta, claims, dsMeta.Definition, "", "error", err.Error(), 0, started)
		return nil, err
	}
	rr, err := permission.RewriteVirtual(dsMeta.Name, dsMeta.Definition, perms, claims.Attributes, claims.IsAdmin())
	if err != nil {
		p.auditDataset(ctx, dsMeta, claims, dsMeta.Definition, "", "denied", err.Error(), 0, started)
		return nil, err
	}
	// For read queries under behavior governance, inject a LIMIT clause
	// so the database engine stops scanning at max_rows (primary defense
	// against table-dumping via datasets).
	execSQL := rr.SQL
	if limited && p.guard.MaxRows > 0 {
		execSQL = injectLimit(rr.SQL, p.guard.MaxRows)
	}
	raw, _, err := p.ds.ExecSQL(ctx, ds, execSQL, params, true)
	if err != nil {
		p.auditDataset(ctx, dsMeta, claims, dsMeta.Definition, rr.SQL, "error", err.Error(), 0, started)
		return nil, fmt.Errorf("execute dataset: %w", err)
	}
	maxRows := 0
	maxBytes := 0
	if limited {
		maxRows = p.guard.MaxRows
		maxBytes = p.guard.MaxBytes
	}
	res, truncated, oversized := p.maskRaw(raw, rr.DeniedCols, rr.AllowedCols, rr.Masks, maxRows, maxBytes)
	res.RewrittenSQL = rr.SQL
	note := ""
	if truncated {
		note = fmt.Sprintf("result truncated at max_rows=%d", maxRows)
	}
	if oversized {
		note = "result body exceeds max_bytes limit"
	}
	p.auditDataset(ctx, dsMeta, claims, dsMeta.Definition, rr.SQL, "ok", note, len(res.Rows), started)
	return res, nil
}

func (p *Proxy) executeDatasetNoSQL(ctx context.Context, dsMeta *store.Dataset, ds *store.DataSource, claims *auth.Claims, started time.Time, limited bool) (*QueryResult, error) {
	perms, err := p.store.ResolvePermissions(ctx, claims.UserID, dsMeta.DataSourceID)
	if err != nil {
		p.auditDataset(ctx, dsMeta, claims, dsMeta.Definition, "", "error", err.Error(), 0, started)
		return nil, err
	}
	gov, err := permission.GovernNoSQLVirtual(ds.Type, json.RawMessage(dsMeta.Definition), dsMeta.Name, perms, claims.IsAdmin())
	if err != nil {
		p.auditDataset(ctx, dsMeta, claims, dsMeta.Definition, "", "denied", err.Error(), 0, started)
		return nil, err
	}
	raw, _, err := p.ds.NoSQLExec(ctx, ds, gov.Payload)
	if err != nil {
		p.auditDataset(ctx, dsMeta, claims, dsMeta.Definition, string(gov.Payload.Raw), "error", err.Error(), 0, started)
		return nil, fmt.Errorf("execute dataset: %w", err)
	}
	maxRows := 0
	maxBytes := 0
	if limited {
		maxRows = p.guard.MaxRows
		maxBytes = p.guard.MaxBytes
	}
	res, truncated, oversized := p.maskRaw(raw, nil, nil, gov.Masks, maxRows, maxBytes)
	res.RewrittenSQL = string(gov.Payload.Raw)
	note := ""
	if truncated {
		note = fmt.Sprintf("result truncated at max_rows=%d", maxRows)
	}
	if oversized {
		note = "result body exceeds max_bytes limit"
	}
	p.auditDataset(ctx, dsMeta, claims, dsMeta.Definition, string(gov.Payload.Raw), "ok", note, len(res.Rows), started)
	return res, nil
}

// ListDatasets returns the datasets a principal may consume: published datasets
// for which the principal (or, for admin, all published) holds a SELECT grant.
func (p *Proxy) ListDatasets(ctx context.Context, claims *auth.Claims) ([]DatasetInfo, error) {
	all, err := p.store.ListDatasets(ctx)
	if err != nil {
		return nil, err
	}
	permCache := map[string]map[string]*store.TableEffective{}
	var out []DatasetInfo
	for _, d := range all {
		if d.Status != store.DatasetPublished {
			continue
		}
		ds, err := p.store.GetDataSource(ctx, d.DataSourceID)
		if err != nil || ds == nil {
			continue
		}
		if !claims.IsAdmin() {
			perms, ok := permCache[d.DataSourceID]
			if !ok {
				perms, err = p.store.ResolvePermissions(ctx, claims.UserID, d.DataSourceID)
				if err != nil {
					return nil, err
				}
				permCache[d.DataSourceID] = perms
			}
			eff := perms[strings.ToLower(d.Name)]
			if eff == nil || !eff.Ops["SELECT"] {
				continue
			}
		}
		out = append(out, DatasetInfo{
			ID:             d.ID,
			Name:           d.Name,
			DisplayName:    d.DisplayName,
			Description:    d.Description,
			Type:           ds.Type,
			DataSourceID:   d.DataSourceID,
			DataSourceName: ds.Name,
			Status:         d.Status,
		})
	}
	return out, nil
}

// DatasetCatalog builds the governed, semantically enriched contract of a
// dataset. Field-level governance (denied/allowed/masked) is applied for
// non-admin callers; business descriptions come from the semantic layer keyed
// on the dataset name.
func (p *Proxy) DatasetCatalog(ctx context.Context, datasetID string, claims *auth.Claims) (*DatasetSchema, error) {
	d, err := p.store.GetDataset(ctx, datasetID)
	if err != nil {
		return nil, err
	}
	if d == nil {
		return nil, fmt.Errorf("dataset not found")
	}
	if d.Status != store.DatasetPublished && !claims.IsAdmin() {
		return nil, fmt.Errorf("dataset %q is not published", d.Name)
	}
	ds, err := p.store.GetDataSource(ctx, d.DataSourceID)
	if err != nil || ds == nil {
		return nil, fmt.Errorf("dataset's datasource not found")
	}

	// Determine the field contract: explicit fields first, else derive from
	// the semantic layer (column-level entries keyed on the dataset name).
	fields := d.DatasetFields()
	semantics, err := p.store.SemanticIndexFor(ctx, d.DataSourceID)
	if err != nil {
		return nil, err
	}
	classes, err := p.store.ClassificationIndexFor(ctx, d.DataSourceID)
	if err != nil {
		return nil, err
	}
	if len(fields) == 0 {
		for _, sem := range semantics[d.Name] {
			if sem.ColumnName == "" {
				continue
			}
			fields = append(fields, store.DatasetField{
				Name:        sem.ColumnName,
				Description: sem.Description,
			})
		}
	}

	schema := &DatasetSchema{
		DatasetID:      d.ID,
		Name:           d.Name,
		DisplayName:    d.DisplayName,
		Description:    d.Description,
		DataSourceID:   d.DataSourceID,
		DataSourceName: ds.Name,
		Type:           ds.Type,
		Status:         d.Status,
	}

	eff := (*store.TableEffective)(nil)
	denied := map[string]bool{}
	allowSet := map[string]bool{}
	allowActive := false
	masked := map[string]string{}
	if !claims.IsAdmin() {
		perms, err := p.store.ResolvePermissions(ctx, claims.UserID, d.DataSourceID)
		if err != nil {
			return nil, err
		}
		eff = perms[strings.ToLower(d.Name)]
		if eff == nil || !eff.Ops["SELECT"] {
			return nil, fmt.Errorf("access denied to dataset %q", d.Name)
		}
		for _, c := range eff.DeniedCols {
			denied[strings.ToLower(c)] = true
		}
		allowActive = len(eff.AllowedCols) > 0
		for _, c := range eff.AllowedCols {
			allowSet[strings.ToLower(c)] = true
		}
		for _, m := range eff.Masks {
			masked[strings.ToLower(m.Column)] = m.Strategy
		}
	}

	for _, f := range fields {
		lc := strings.ToLower(f.Name)
		if !claims.IsAdmin() {
			if denied[lc] {
				continue
			}
			if allowActive && !allowSet[lc] {
				continue
			}
		}
		sc := SemanticColumn{Name: f.Name, Type: f.Type}
		if f.Description == "" {
			if sem := semantics.Column(d.Name, f.Name); sem != nil {
				sc.Description = sem.Description
				sc.Synonyms = jsonStringSlice(sem.Synonyms)
				sc.Examples = jsonStringSlice(sem.Examples)
			}
		} else {
			sc.Description = f.Description
		}
		if ms, ok := masked[lc]; ok {
			sc.Masked = ms
		}
		if cls := classes.Column(d.Name, f.Name); cls != nil {
			sc.Classification = &ClassificationInfo{Level: cls.Level, Tags: jsonStringSlice(cls.Tags)}
		}
		schema.Fields = append(schema.Fields, sc)
	}
	return schema, nil
}

// CatalogMarkdown renders the dataset contract as a compact markdown document
// for an LLM: field names, types, business meaning and masking notes.
func (s *DatasetSchema) CatalogMarkdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Dataset: %s\n", s.Name)
	if s.DisplayName != "" {
		fmt.Fprintf(&b, "Display name: %s\n", s.DisplayName)
	}
	if s.Description != "" {
		fmt.Fprintf(&b, "%s\n", s.Description)
	}
	fmt.Fprintf(&b, "Source: %s (%s)\n\n", s.DataSourceName, s.Type)
	if len(s.Fields) == 0 {
		b.WriteString("(no accessible fields)\n")
		return b.String()
	}
	b.WriteString("Fields:\n")
	for _, c := range s.Fields {
		fmt.Fprintf(&b, "- `%s` (%s)", c.Name, c.Type)
		if c.Description != "" {
			fmt.Fprintf(&b, ": %s", c.Description)
		}
		if len(c.Synonyms) > 0 {
			fmt.Fprintf(&b, " (aka: %s)", strings.Join(c.Synonyms, ", "))
		}
		if c.Masked != "" {
			fmt.Fprintf(&b, " [masked: %s]", c.Masked)
		}
		if c.Classification != nil {
			if c.Classification.Level != "" {
				fmt.Fprintf(&b, " [class: %s]", c.Classification.Level)
			}
			if len(c.Classification.Tags) > 0 {
				fmt.Fprintf(&b, " [tags: %s]", strings.Join(c.Classification.Tags, ", "))
			}
		}
		b.WriteString("\n")
		if len(c.Examples) > 0 {
			fmt.Fprintf(&b, "  - examples: %s\n", strings.Join(c.Examples, ", "))
		}
	}
	return b.String()
}

// auditDataset persists a governed-dataset trace. It is a thin wrapper that
// stamps the dataset name so audit entries are attributable to the dataset.
func (p *Proxy) auditDataset(ctx context.Context, dsMeta *store.Dataset, claims *auth.Claims, sqlText, rewritten, status, errMsg string, rowCount int, started time.Time) {
	dsName := ""
	if ds, err := p.store.GetDataSource(ctx, dsMeta.DataSourceID); err == nil && ds != nil {
		dsName = ds.Name
	}
	_ = p.store.InsertAudit(ctx, &store.AuditLog{
		UserID:       claims.UserID,
		Username:     claims.Username,
		Channel:      channelFrom(ctx),
		DataSourceID: dsMeta.DataSourceID,
		DataSource:   dsName,
		SQLText:      "dataset:" + dsMeta.Name + " | " + sqlText,
		RewrittenSQL: rewritten,
		Status:       status,
		Error:        errMsg,
		RowCount:     rowCount,
		DurationMS:   time.Since(started).Milliseconds(),
	})
	ch := channelFrom(ctx)
	metrics.RecordQuery(ch, status, time.Since(started))
	metrics.RecordRows(ch, status, rowCount)
	if p.detector != nil {
		p.detector.Observe(claims.Username, claims.IsAdmin(), ch, status, rowCount, time.Now())
	}
}

// IsValidDatasetName reports whether name is a safe identifier usable as a
// governance key and SQL alias (letters, digits, underscores only).
func IsValidDatasetName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if !(r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

// ValidateDatasetDefinition checks that a definition is well-formed for its
// datasource type. For SQL-family backends it must parse as a SELECT; for NoSQL
// it must be valid JSON naming a collection/index.
func (p *Proxy) ValidateDatasetDefinition(dsType, def string) error {
	if strings.TrimSpace(def) == "" {
		return fmt.Errorf("definition is required")
	}
	if datasource.IsNoSQL(dsType) {
		var probe map[string]interface{}
		if err := json.Unmarshal([]byte(def), &probe); err != nil {
			return fmt.Errorf("invalid %s definition (must be JSON): %w", dsType, err)
		}
		if datasource.NormalizeType(dsType) == "mongo" {
			if _, ok := probe["collection"]; !ok {
				return fmt.Errorf("mongo dataset definition requires 'collection'")
			}
		} else {
			if _, ok := probe["index"]; !ok {
				return fmt.Errorf("elasticsearch dataset definition requires 'index'")
			}
		}
		return nil
	}
	// SQL-family: parse the wrapped form to validate it is a SELECT.
	if _, err := permission.RewriteVirtual("__v", def, map[string]*store.TableEffective{}, nil, true); err != nil {
		return fmt.Errorf("invalid SQL dataset definition: %w", err)
	}
	return nil
}
