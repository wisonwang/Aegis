package datasource

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"time"

	"github.com/wisonwang/aegis/internal/store"
)

// esConnector serves Elasticsearch. Queries arrive as a JSON document describing
// the index, query DSL, _source filter and size. As with Mongo, governance is
// applied by the proxy before the connector runs the query.
type esConnector struct{}

func (c *esConnector) Kind() string { return "es" }

func (c *esConnector) Open(ds *store.DataSource) (Session, error) {
	base, user, pass, err := parseESDSN(ds.DSN)
	if err != nil {
		return nil, err
	}
	return &esSession{base: base, user: user, pass: pass, client: &http.Client{Timeout: 30 * time.Second}}, nil
}

func parseESDSN(dsn string) (base, user, pass string, err error) {
	u, e := url.Parse(dsn)
	if e != nil {
		return "", "", "", fmt.Errorf("invalid elasticsearch DSN %q: %w", dsn, e)
	}
	if u.Host == "" {
		return "", "", "", fmt.Errorf("elasticsearch DSN must be a URL, e.g. http://host:9200")
	}
	scheme := u.Scheme
	if scheme == "" {
		scheme = "http"
	}
	return scheme + "://" + u.Host, u.User.Username(), func() string { p, _ := u.User.Password(); return p }(), nil
}

type esSession struct {
	base   string
	user   string
	pass   string
	client *http.Client
}

type esQuery struct {
	Index   string          `json:"index"`
	Query   json.RawMessage `json:"query"`
	Source  json.RawMessage `json:"_source"`
	Size    *int            `json:"size"`
	Aggs    json.RawMessage `json:"aggs"`
}

// esWriteQuery carries a mutating operation. op is one of
// index|updateByQuery|deleteByQuery.
type esWriteQuery struct {
	Op       string          `json:"op"`
	Index    string          `json:"index"`
	ID       string          `json:"id"`
	Document json.RawMessage `json:"document"`
	Query    json.RawMessage `json:"query"`
	Script   json.RawMessage `json:"script"`
}

type esWriteResp struct {
	Error *struct {
		Reason string `json:"reason"`
	} `json:"error"`
}

type esBulkResp struct {
	Updated int64 `json:"updated"`
	Deleted int64 `json:"deleted"`
	Total   int64 `json:"total"`
	Error   *struct {
		Reason string `json:"reason"`
	} `json:"error"`
}

func (r esBulkResp) affected(verb string) int64 {
	if verb == "deleted" {
		return r.Deleted
	}
	return r.Updated
}

type esCountResp struct {
	Count int64 `json:"count"`
	Error *struct {
		Reason string `json:"reason"`
	} `json:"error"`
}

func (s *esSession) Exec(ctx context.Context, payload QueryPayload) (*RawResult, int64, error) {
	var q esQuery
	if err := json.Unmarshal(payload.Raw, &q); err != nil {
		return nil, 0, fmt.Errorf("invalid elasticsearch query: %w", err)
	}
	if q.Index == "" {
		return nil, 0, fmt.Errorf("elasticsearch query requires an 'index' field")
	}

	body := map[string]interface{}{}
	if len(q.Query) > 0 {
		var bq interface{}
		if err := json.Unmarshal(q.Query, &bq); err != nil {
			return nil, 0, fmt.Errorf("invalid elasticsearch query DSL: %w", err)
		}
		body["query"] = bq
	}
	if len(q.Source) > 0 {
		var src interface{}
		if err := json.Unmarshal(q.Source, &src); err != nil {
			return nil, 0, fmt.Errorf("invalid elasticsearch _source: %w", err)
		}
		body["_source"] = src
	}
	if len(q.Aggs) > 0 {
		var aggs interface{}
		if err := json.Unmarshal(q.Aggs, &aggs); err != nil {
			return nil, 0, fmt.Errorf("invalid elasticsearch aggs: %w", err)
		}
		body["aggs"] = aggs
	}
	if q.Size != nil {
		body["size"] = *q.Size
	}

	var resp esSearchResp
	if err := s.post(ctx, "/"+q.Index+"/_search", body, &resp); err != nil {
		return nil, 0, err
	}
	if resp.Error != nil {
		return nil, 0, fmt.Errorf("elasticsearch error: %s", resp.Error.Reason)
	}

	colSet := map[string]bool{}
	docs := make([]map[string]interface{}, 0, len(resp.Hits.Hits))
	for _, h := range resp.Hits.Hits {
		for k := range h.Source {
			colSet[k] = true
		}
		docs = append(docs, h.Source)
	}
	cols := make([]string, 0, len(colSet))
	for k := range colSet {
		cols = append(cols, k)
	}
	sort.Strings(cols)

	raw := &RawResult{Columns: cols, Rows: make([]map[string]interface{}, 0, len(docs))}
	for _, d := range docs {
		row := make(map[string]interface{}, len(cols))
		for _, c := range cols {
			row[c] = normalizeCell(d[c])
		}
		raw.Rows = append(raw.Rows, row)
	}
	return raw, 0, nil
}

func (s *esSession) ListCollections(ctx context.Context) ([]string, error) {
	var indices []map[string]interface{}
	if err := s.get(ctx, "/_cat/indices?h=index&format=json", &indices); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(indices))
	for _, idx := range indices {
		if name, ok := idx["index"].(string); ok {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (s *esSession) DescribeCollection(ctx context.Context, name string) ([]ColumnMeta, error) {
	if !safeIdent(name) {
		return nil, fmt.Errorf("invalid index name %q", name)
	}
	var mapping map[string]struct {
		Mappings struct {
			Properties map[string]struct {
				Type string `json:"type"`
			} `json:"properties"`
		} `json:"mappings"`
	}
	if err := s.get(ctx, "/"+name+"/_mapping", &mapping); err != nil {
		return nil, err
	}
	out := []ColumnMeta{}
	if m, ok := mapping[name]; ok {
		for field, prop := range m.Mappings.Properties {
			out = append(out, ColumnMeta{Name: field, Type: prop.Type})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *esSession) req(ctx context.Context, method, path string, body interface{}, out interface{}) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.base+path, rdr)
	if err != nil {
		return err
	}
	return s.do(req, out)
}

func (s *esSession) post(ctx context.Context, path string, body interface{}, out interface{}) error {
	return s.req(ctx, http.MethodPost, path, body, out)
}

func (s *esSession) get(ctx context.Context, path string, out interface{}) error {
	return s.req(ctx, http.MethodGet, path, nil, out)
}

func (s *esSession) do(req *http.Request, out interface{}) error {
	req.Header.Set("Content-Type", "application/json")
	if s.user != "" {
		enc := base64.StdEncoding.EncodeToString([]byte(s.user + ":" + s.pass))
		req.Header.Set("Authorization", "Basic "+enc)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("elasticsearch %s %s: %d %s", req.Method, req.URL.Path, resp.StatusCode, string(data))
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode elasticsearch response: %w", err)
		}
	}
	return nil
}

func (s *esSession) Close() error { return nil }

// Write runs a governed mutating operation. op is index|updateByQuery|deleteByQuery.
func (s *esSession) Write(ctx context.Context, payload WritePayload) (int64, error) {
	var w esWriteQuery
	if err := json.Unmarshal(payload.Raw, &w); err != nil {
		return 0, fmt.Errorf("invalid elasticsearch write: %w", err)
	}
	if w.Index == "" {
		return 0, fmt.Errorf("elasticsearch write requires 'index'")
	}
	switch w.Op {
	case "index":
		if len(w.Document) == 0 {
			return 0, fmt.Errorf("elasticsearch index requires 'document'")
		}
		var doc interface{}
		if err := json.Unmarshal(w.Document, &doc); err != nil {
			return 0, fmt.Errorf("invalid elasticsearch document: %w", err)
		}
		path := "/" + w.Index + "/_doc"
		method := http.MethodPost
		if w.ID != "" {
			path = "/" + w.Index + "/_doc/" + w.ID
			method = http.MethodPut
		}
		var resp esWriteResp
		if err := s.req(ctx, method, path, doc, &resp); err != nil {
			return 0, err
		}
		if resp.Error != nil {
			return 0, fmt.Errorf("elasticsearch error: %s", resp.Error.Reason)
		}
		return 1, nil
	case "updateByQuery", "deleteByQuery":
		body := map[string]interface{}{}
		if len(w.Query) > 0 {
			var q interface{}
			if err := json.Unmarshal(w.Query, &q); err != nil {
				return 0, fmt.Errorf("invalid elasticsearch query: %w", err)
			}
			body["query"] = q
		}
		if w.Op == "updateByQuery" && len(w.Script) > 0 {
			var sc interface{}
			if err := json.Unmarshal(w.Script, &sc); err != nil {
				return 0, fmt.Errorf("invalid elasticsearch script: %w", err)
			}
			body["script"] = sc
		}
		verb := "updated"
		if w.Op == "deleteByQuery" {
			verb = "deleted"
		}
		var resp esBulkResp
		if err := s.req(ctx, http.MethodPost, "/"+w.Index+"/_"+w.Op, body, &resp); err != nil {
			return 0, err
		}
		if resp.Error != nil {
			return 0, fmt.Errorf("elasticsearch error: %s", resp.Error.Reason)
		}
		return resp.affected(verb), nil
	default:
		return 0, fmt.Errorf("unsupported elasticsearch op %q", w.Op)
	}
}

// Count returns the number of documents matching a governed query. Used by the
// proxy to enforce the affected-rows guard before an update/delete.
func (s *esSession) Count(ctx context.Context, payload QueryPayload) (int64, error) {
	var q esQuery
	if err := json.Unmarshal(payload.Raw, &q); err != nil {
		return 0, fmt.Errorf("invalid elasticsearch count query: %w", err)
	}
	if q.Index == "" {
		return 0, fmt.Errorf("elasticsearch count requires 'index'")
	}
	body := map[string]interface{}{}
	if len(q.Query) > 0 {
		var bq interface{}
		if err := json.Unmarshal(q.Query, &bq); err != nil {
			return 0, fmt.Errorf("invalid elasticsearch query: %w", err)
		}
		body["query"] = bq
	}
	var resp esCountResp
	if err := s.req(ctx, http.MethodPost, "/"+q.Index+"/_count", body, &resp); err != nil {
		return 0, err
	}
	if resp.Error != nil {
		return 0, fmt.Errorf("elasticsearch error: %s", resp.Error.Reason)
	}
	return resp.Count, nil
}

type esSearchResp struct {
	Hits struct {
		Hits []struct {
			Source map[string]interface{} `json:"_source"`
		} `json:"hits"`
	} `json:"hits"`
	Error *struct {
		Reason string `json:"reason"`
	} `json:"error"`
}
