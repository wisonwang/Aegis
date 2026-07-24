package permission

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wisonwang/aegis/internal/datasource"
	"github.com/wisonwang/aegis/internal/store"
)

// NoSQLGov is the governed form of a backend-specific (Mongo / Elasticsearch)
// query. The proxy executes Payload and then applies Masks to cell values.
// Allowed/denied columns are enforced at query time (projection / _source), so
// they are not re-applied at the result layer.
type NoSQLGov struct {
	Payload datasource.QueryPayload
	Masks   map[string]store.MaskSpec
}

// GovernNoSQL applies table/row/column governance to a NoSQL query expressed in
// the backend's native JSON dialect. dsType selects the enforcement rules.
// superuser bypasses all enforcement.
func GovernNoSQL(dsType string, raw json.RawMessage, perms map[string]*store.TableEffective, superuser bool) (*NoSQLGov, error) {
	if superuser {
		return &NoSQLGov{Payload: datasource.QueryPayload{Raw: raw}}, nil
	}
	switch datasource.NormalizeType(dsType) {
	case "mongo":
		return governMongo(raw, perms)
	case "es":
		return governES(raw, perms)
	}
	return nil, fmt.Errorf("unsupported NoSQL datasource type %q", dsType)
}

func masksFor(eff *store.TableEffective) map[string]store.MaskSpec {
	m := map[string]store.MaskSpec{}
	if eff == nil {
		return m
	}
	for _, x := range eff.Masks {
		m[strings.ToLower(x.Column)] = x
	}
	return m
}

// GovernNoSQLVirtual applies dataset-level governance to a NoSQL query. Unlike
// GovernNoSQL (which keys governance on the query's own collection/index), this
// keys governance on an explicit govKey (the dataset name) while still executing
// the query against the real collection/index named inside the definition. This
// lets a dataset be a curated, separately-governed view over a physical
// collection. superuser bypasses all enforcement.
func GovernNoSQLVirtual(dsType string, raw json.RawMessage, govKey string, perms map[string]*store.TableEffective, superuser bool) (*NoSQLGov, error) {
	if superuser {
		return &NoSQLGov{Payload: datasource.QueryPayload{Raw: raw}}, nil
	}
	switch datasource.NormalizeType(dsType) {
	case "mongo":
		return governMongoVirtual(raw, govKey, perms)
	case "es":
		return governESVirtual(raw, govKey, perms)
	}
	return nil, fmt.Errorf("unsupported NoSQL datasource type %q", dsType)
}

func governMongoVirtual(raw json.RawMessage, govKey string, perms map[string]*store.TableEffective) (*NoSQLGov, error) {
	var q mongoIn
	if err := json.Unmarshal(raw, &q); err != nil {
		return nil, fmt.Errorf("invalid mongo query: %w", err)
	}
	if q.Collection == "" {
		return nil, fmt.Errorf("mongo query requires 'collection'")
	}
	eff := perms[strings.ToLower(govKey)]
	if eff == nil {
		return nil, fmt.Errorf("access denied to dataset %q (no permission granted)", govKey)
	}
	if !eff.Ops["SELECT"] {
		return nil, fmt.Errorf("SELECT denied on dataset %q", govKey)
	}
	filter, err := mergeMongoFilter(q.Filter, eff.RowPolicies)
	if err != nil {
		return nil, err
	}
	proj, err := mongoProjection(eff, q.Projection)
	if err != nil {
		return nil, err
	}
	out := map[string]interface{}{"collection": q.Collection, "filter": filter}
	if proj != nil {
		out["projection"] = proj
	}
	if len(q.Sort) > 0 {
		out["sort"] = q.Sort
	}
	if q.Limit != nil {
		out["limit"] = *q.Limit
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	return &NoSQLGov{Payload: datasource.QueryPayload{Raw: b}, Masks: masksFor(eff)}, nil
}

func governESVirtual(raw json.RawMessage, govKey string, perms map[string]*store.TableEffective) (*NoSQLGov, error) {
	var q esIn
	if err := json.Unmarshal(raw, &q); err != nil {
		return nil, fmt.Errorf("invalid elasticsearch query: %w", err)
	}
	if q.Index == "" {
		return nil, fmt.Errorf("elasticsearch query requires 'index'")
	}
	eff := perms[strings.ToLower(govKey)]
	if eff == nil {
		return nil, fmt.Errorf("access denied to dataset %q (no permission granted)", govKey)
	}
	if !eff.Ops["SELECT"] {
		return nil, fmt.Errorf("SELECT denied on dataset %q", govKey)
	}
	query, err := mergeESQuery(q.Query, eff.RowPolicies)
	if err != nil {
		return nil, err
	}
	src, err := esSource(eff, q.Source)
	if err != nil {
		return nil, err
	}
	out := map[string]interface{}{"index": q.Index, "query": query}
	if src != nil {
		out["_source"] = src
	}
	if q.Size != nil {
		out["size"] = *q.Size
	}
	if len(q.Aggs) > 0 {
		out["aggs"] = q.Aggs
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	return &NoSQLGov{Payload: datasource.QueryPayload{Raw: b}, Masks: masksFor(eff)}, nil
}

// ---- MongoDB ---------------------------------------------------------------

type mongoIn struct {
	Collection string          `json:"collection"`
	Filter     json.RawMessage `json:"filter"`
	Projection json.RawMessage `json:"projection"`
	Sort       json.RawMessage `json:"sort"`
	Limit      *int64          `json:"limit"`
}

func governMongo(raw json.RawMessage, perms map[string]*store.TableEffective) (*NoSQLGov, error) {
	var q mongoIn
	if err := json.Unmarshal(raw, &q); err != nil {
		return nil, fmt.Errorf("invalid mongo query: %w", err)
	}
	if q.Collection == "" {
		return nil, fmt.Errorf("mongo query requires 'collection'")
	}
	eff := perms[strings.ToLower(q.Collection)]
	if eff == nil {
		return nil, fmt.Errorf("access denied to collection %q (no permission granted)", q.Collection)
	}
	if !eff.Ops["SELECT"] {
		return nil, fmt.Errorf("SELECT denied on collection %q", q.Collection)
	}

	filter, err := mergeMongoFilter(q.Filter, eff.RowPolicies)
	if err != nil {
		return nil, err
	}
	proj, err := mongoProjection(eff, q.Projection)
	if err != nil {
		return nil, err
	}

	out := map[string]interface{}{"collection": q.Collection, "filter": filter}
	if proj != nil {
		out["projection"] = proj
	}
	if len(q.Sort) > 0 {
		out["sort"] = q.Sort
	}
	if q.Limit != nil {
		out["limit"] = *q.Limit
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	return &NoSQLGov{Payload: datasource.QueryPayload{Raw: b}, Masks: masksFor(eff)}, nil
}

func mergeMongoFilter(userFilter json.RawMessage, policies []string) (json.RawMessage, error) {
	var uf map[string]interface{}
	if len(userFilter) > 0 {
		if err := json.Unmarshal(userFilter, &uf); err != nil {
			return nil, fmt.Errorf("invalid mongo filter: %w", err)
		}
	}
	clauses := []interface{}{}
	if len(uf) > 0 {
		clauses = append(clauses, uf)
	}
	for _, p := range policies {
		var pf interface{}
		if err := json.Unmarshal([]byte(p), &pf); err != nil {
			return nil, fmt.Errorf("invalid mongo row policy %q: %w", p, err)
		}
		clauses = append(clauses, pf)
	}
	var final interface{}
	switch len(clauses) {
	case 0:
		final = map[string]interface{}{}
	case 1:
		final = clauses[0]
	default:
		final = map[string]interface{}{"$and": clauses}
	}
	return json.Marshal(final)
}

func mongoProjection(eff *store.TableEffective, userProj json.RawMessage) (json.RawMessage, error) {
	if len(eff.AllowedCols) > 0 {
		proj := map[string]int{}
		for _, c := range eff.AllowedCols {
			proj[c] = 1
		}
		return json.Marshal(proj)
	}
	if len(eff.DeniedCols) > 0 {
		proj := map[string]int{}
		for _, c := range eff.DeniedCols {
			proj[c] = 0
		}
		return json.Marshal(proj)
	}
	if len(userProj) > 0 {
		return userProj, nil
	}
	return nil, nil
}

// ---- Elasticsearch ---------------------------------------------------------

type esIn struct {
	Index  string          `json:"index"`
	Query  json.RawMessage `json:"query"`
	Source json.RawMessage `json:"_source"`
	Size   *int            `json:"size"`
	Aggs   json.RawMessage `json:"aggs"`
}

func governES(raw json.RawMessage, perms map[string]*store.TableEffective) (*NoSQLGov, error) {
	var q esIn
	if err := json.Unmarshal(raw, &q); err != nil {
		return nil, fmt.Errorf("invalid elasticsearch query: %w", err)
	}
	if q.Index == "" {
		return nil, fmt.Errorf("elasticsearch query requires 'index'")
	}
	eff := perms[strings.ToLower(q.Index)]
	if eff == nil {
		return nil, fmt.Errorf("access denied to index %q (no permission granted)", q.Index)
	}
	if !eff.Ops["SELECT"] {
		return nil, fmt.Errorf("SELECT denied on index %q", q.Index)
	}

	query, err := mergeESQuery(q.Query, eff.RowPolicies)
	if err != nil {
		return nil, err
	}
	src, err := esSource(eff, q.Source)
	if err != nil {
		return nil, err
	}

	out := map[string]interface{}{"index": q.Index, "query": query}
	if src != nil {
		out["_source"] = src
	}
	if q.Size != nil {
		out["size"] = *q.Size
	}
	if len(q.Aggs) > 0 {
		out["aggs"] = q.Aggs
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	return &NoSQLGov{Payload: datasource.QueryPayload{Raw: b}, Masks: masksFor(eff)}, nil
}

func mergeESQuery(userQuery json.RawMessage, policies []string) (json.RawMessage, error) {
	var uq map[string]interface{}
	if len(userQuery) > 0 {
		if err := json.Unmarshal(userQuery, &uq); err != nil {
			return nil, fmt.Errorf("invalid elasticsearch query DSL: %w", err)
		}
	}
	must := []interface{}{}
	if len(uq) > 0 {
		must = append(must, uq)
	}
	for _, p := range policies {
		var pq interface{}
		if err := json.Unmarshal([]byte(p), &pq); err != nil {
			return nil, fmt.Errorf("invalid elasticsearch row policy %q: %w", p, err)
		}
		must = append(must, pq)
	}
	var final interface{}
	switch len(must) {
	case 0:
		final = map[string]interface{}{"match_all": map[string]interface{}{}}
	case 1:
		final = must[0]
	default:
		final = map[string]interface{}{"bool": map[string]interface{}{"must": must}}
	}
	return json.Marshal(final)
}

func esSource(eff *store.TableEffective, userSrc json.RawMessage) (json.RawMessage, error) {
	if len(eff.AllowedCols) > 0 {
		return json.Marshal(map[string]interface{}{"includes": eff.AllowedCols})
	}
	if len(eff.DeniedCols) > 0 {
		return json.Marshal(map[string]interface{}{"excludes": eff.DeniedCols})
	}
	if len(userSrc) > 0 {
		return userSrc, nil
	}
	return nil, nil
}
