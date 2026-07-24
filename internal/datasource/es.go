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

func (s *esSession) post(ctx context.Context, path string, body interface{}, out interface{}) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.base+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	return s.do(req, out)
}

func (s *esSession) get(ctx context.Context, path string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base+path, nil)
	if err != nil {
		return err
	}
	return s.do(req, out)
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
