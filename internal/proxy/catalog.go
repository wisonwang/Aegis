package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/wisonwang/aegis/internal/auth"
	"github.com/wisonwang/aegis/internal/store"
)

// SemanticSchema is the governance-filtered, semantically enriched view of a
// datasource that Aegis exposes to AI agents. It pairs the physical schema
// (columns, types) with human-authored business descriptions so an LLM can
// generate correct SQL without guessing at column meaning. Columns denied or
// not allowed by the principal's governance are already removed.
type SemanticSchema struct {
	DataSourceID   string             `json:"datasource_id"`
	DataSourceName string             `json:"datasource_name"`
	Tables         []SemanticTable    `json:"tables"`
}

// SemanticTable describes one table the principal may access.
type SemanticTable struct {
	Name        string            `json:"name"`
	Ops         []string          `json:"ops"`
	Description string            `json:"description,omitempty"`
	RowScoped   bool              `json:"row_scoped,omitempty"` // a row policy is in effect for this principal
	Columns     []SemanticColumn  `json:"columns"`
}

// ClassificationInfo carries the sensitivity label attached to a column or
// table so AI agents can treat PII / sensitive columns with appropriate care.
type ClassificationInfo struct {
	Level string   `json:"level,omitempty"` // public|internal|confidential|restricted|pii
	Tags  []string `json:"tags,omitempty"`
}

// SemanticColumn pairs physical metadata with optional business semantics.
type SemanticColumn struct {
	Name        string              `json:"name"`
	Type        string              `json:"type"`
	Nullable    string              `json:"nullable,omitempty"`
	Key         string              `json:"key,omitempty"`
	Description string              `json:"description,omitempty"`
	Synonyms    []string            `json:"synonyms,omitempty"`
	Examples    []string            `json:"examples,omitempty"`
	Masked      string              `json:"masked,omitempty"` // masking strategy applied to this column's values (e.g. "phone")
	Classification *ClassificationInfo `json:"classification,omitempty"`
}

// Catalog builds the governed, semantically enriched schema for a principal.
// It reuses the same table/column governance as Execute/DescribeTable, then
// layers on any human-authored descriptions, synonyms and example values.
func (p *Proxy) Catalog(ctx context.Context, dsID string, claims *auth.Claims) (*SemanticSchema, error) {
	ds, err := p.store.GetDataSource(dsID)
	if err != nil {
		return nil, err
	}
	if ds == nil {
		return nil, fmt.Errorf("datasource not found")
	}
	physical, err := p.physicalTables(ctx, ds)
	if err != nil {
		return nil, err
	}
	perms, err := p.store.ResolvePermissions(claims.UserID, dsID)
	if err != nil {
		return nil, err
	}
	semantics, err := p.store.SemanticIndexFor(dsID)
	if err != nil {
		return nil, err
	}
	classes, err := p.store.ClassificationIndexFor(dsID)
	if err != nil {
		return nil, err
	}

	schema := &SemanticSchema{DataSourceID: ds.ID, DataSourceName: ds.Name}
	for _, t := range physical {
		if !claims.IsAdmin() {
			eff, ok := perms[strings.ToLower(t)]
			if !ok {
				continue
			}
			if !eff.Ops["SELECT"] {
				// Non-SELECT tables are still listed, but without row exposure.
				// For the schema catalog we only surface SELECT-able tables.
				continue
			}
		}
		cols, err := p.describeColumns(ctx, ds, t)
		if err != nil {
			return nil, err
		}
		denied, allowSet, allowActive := columnGovernance(perms, claims, t)
		masked := map[string]string{}
		if !claims.IsAdmin() {
			if eff := perms[strings.ToLower(t)]; eff != nil {
				for _, m := range eff.Masks {
					masked[strings.ToLower(m.Column)] = m.Strategy
				}
			}
		}
		out := make([]SemanticColumn, 0, len(cols))
		for _, c := range cols {
			lc := strings.ToLower(c.Name)
			if !claims.IsAdmin() {
				if denied[lc] {
					continue
				}
				if allowActive && !allowSet[lc] {
					continue
				}
			}
			sem := semantics.Column(t, c.Name)
			sc := SemanticColumn{Name: c.Name, Type: c.Type, Nullable: c.Nullable, Key: c.Key}
			if sem != nil {
				sc.Description = sem.Description
				sc.Synonyms = jsonStringSlice(sem.Synonyms)
				sc.Examples = jsonStringSlice(sem.Examples)
			}
		if ms, ok := masked[lc]; ok {
			sc.Masked = ms
		}
		if cls := classes.Column(t, c.Name); cls != nil {
			sc.Classification = &ClassificationInfo{
				Level: cls.Level,
				Tags:  jsonStringSlice(cls.Tags),
			}
		}
		out = append(out, sc)
		}
		st := SemanticTable{Name: t, Columns: out}
		if claims.IsAdmin() {
			st.Ops = allOps()
		} else {
			st.Ops = opsList(perms[strings.ToLower(t)].Ops)
			st.RowScoped = len(perms[strings.ToLower(t)].RowPolicies) > 0
		}
		if tsem := semantics.Table(t); tsem != nil {
			st.Description = tsem.Description
		}
		schema.Tables = append(schema.Tables, st)
	}
	sort.Slice(schema.Tables, func(i, j int) bool {
		return schema.Tables[i].Name < schema.Tables[j].Name
	})
	return schema, nil
}

// columnGovernance resolves the principal's denied/allowed column sets for a
// table. Admin callers get empty filters (no masking).
func columnGovernance(perms map[string]*store.TableEffective, claims *auth.Claims, table string) (denied, allowSet map[string]bool, allowActive bool) {
	denied = map[string]bool{}
	allowSet = map[string]bool{}
	if claims.IsAdmin() {
		return
	}
	eff := perms[strings.ToLower(table)]
	if eff == nil {
		return
	}
	for _, c := range eff.DeniedCols {
		denied[strings.ToLower(c)] = true
	}
	allowActive = len(eff.AllowedCols) > 0
	for _, c := range eff.AllowedCols {
		allowSet[strings.ToLower(c)] = true
	}
	return
}

func jsonStringSlice(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	return out
}

// CatalogMarkdown renders the semantic schema as a compact markdown document
// suitable for an LLM prompt: table names, business meanings, and column
// descriptions with synonyms/examples. Denied columns are already absent.
func (s *SemanticSchema) CatalogMarkdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Data source: %s\n\n", s.DataSourceName)
	if len(s.Tables) == 0 {
		b.WriteString("(no accessible tables)\n")
		return b.String()
	}
	for _, t := range s.Tables {
		fmt.Fprintf(&b, "## Table: %s\n", t.Name)
		if t.Description != "" {
			fmt.Fprintf(&b, "%s\n", t.Description)
		}
		if t.RowScoped {
			b.WriteString("- Note: rows are automatically scoped to the caller; do not add tenant/owner filters.\n")
		}
		b.WriteString("Columns:\n")
		for _, c := range t.Columns {
			fmt.Fprintf(&b, "- `%s` (%s)", c.Name, c.Type)
			if c.Key != "" {
				fmt.Fprintf(&b, " [%s]", c.Key)
			}
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
		b.WriteString("\n")
	}
	return b.String()
}
