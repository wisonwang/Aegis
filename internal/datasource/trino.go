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
	"time"

	"github.com/wisonwang/aegis/internal/store"
)

// Trino/Presto expose an ANSI-SQL surface over the HTTP /v1/statement REST API.
// Aegis drives them with the standard library only (no JDBC/driver dependency):
// it POSTs the (already governed) SQL, then polls nextUri until the result set
// is complete. This lets Trino/Presto reuse the exact same permission.Rewrite
// governance pipeline as every other SQL backend.

const trinoPollTimeout = 60 * time.Second

type trinoResp struct {
	ID       string             `json:"id"`
	NextURI  string             `json:"nextUri"`
	Columns  []trinoColumn      `json:"columns"`
	Data     [][]interface{}    `json:"data"`
	Error    *trinoError        `json:"error"`
	Stats    json.RawMessage    `json:"stats"`
}

type trinoColumn struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type trinoError struct {
	Message string `json:"message"`
	Error   string `json:"error"`
}

type trinoDSN struct {
	base    string
	catalog string
	schema  string
	user    string
	pass    string
	presto  bool
}

func parseTrinoDSN(dsn, dsType string) (*trinoDSN, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return nil, fmt.Errorf("invalid trino/presto DSN %q: %w", dsn, err)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("trino/presto DSN must be a URL with host, e.g. http://host:8080?catalog=x&schema=y")
	}
	scheme := u.Scheme
	if scheme == "" {
		scheme = "http"
	}
	q := u.Query()
	user := u.User.Username()
	pass, _ := u.User.Password()
	return &trinoDSN{
		base:    scheme + "://" + u.Host,
		catalog: q.Get("catalog"),
		schema:  q.Get("schema"),
		user:    user,
		pass:    pass,
		presto:  NormalizeType(dsType) == "presto",
	}, nil
}

func (t *trinoDSN) userHeader() string {
	if t.presto {
		return "X-Presto-User"
	}
	return "X-Trino-User"
}

// runTrino executes a SQL statement and returns the normalised result set.
func runTrino(ctx context.Context, ds *store.DataSource, sqlText string, isRead bool) (*RawResult, int64, error) {
	t, err := parseTrinoDSN(ds.DSN, ds.Type)
	if err != nil {
		return nil, 0, err
	}
	client := &http.Client{Timeout: trinoPollTimeout}

	stmtURL := t.base + "/v1/statement"
	if t.catalog != "" || t.schema != "" {
		q := url.Values{}
		if t.catalog != "" {
			q.Set("catalog", t.catalog)
		}
		if t.schema != "" {
			q.Set("schema", t.schema)
		}
		stmtURL += "?" + q.Encode()
	}

	var columns []trinoColumn
	var data [][]interface{}

	body, err := postTrino(ctx, client, t, stmtURL, sqlText)
	if err != nil {
		return nil, 0, err
	}
	if body.Error != nil {
		return nil, 0, fmt.Errorf("trino error: %s", body.Error.Message)
	}
	columns = body.Columns
	data = append(data, body.Data...)

	// Poll nextUri until the result is fully materialised.
	pollCtx, cancel := context.WithTimeout(ctx, trinoPollTimeout)
	defer cancel()
	for body.NextURI != "" {
		next, err := getTrino(pollCtx, client, t, body.NextURI)
		if err != nil {
			return nil, 0, err
		}
		if next.Error != nil {
			return nil, 0, fmt.Errorf("trino error: %s", next.Error.Message)
		}
		if len(next.Columns) > 0 {
			columns = next.Columns
		}
		data = append(data, next.Data...)
		body = next
	}

	raw := &RawResult{}
	if len(columns) > 0 {
		raw.Columns = make([]string, len(columns))
		for i, c := range columns {
			raw.Columns[i] = c.Name
		}
	}
	for _, row := range data {
		m := make(map[string]interface{}, len(raw.Columns))
		for i, name := range raw.Columns {
			if i < len(row) {
				m[name] = normalizeCell(row[i])
			}
		}
		raw.Rows = append(raw.Rows, m)
	}
	return raw, 0, nil
}

func postTrino(ctx context.Context, client *http.Client, t *trinoDSN, urlStr, sqlText string) (*trinoResp, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, urlStr, bytes.NewBufferString(sqlText))
	if err != nil {
		return nil, err
	}
	applyTrinoHeaders(req, t)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("trino POST %s: %d %s", urlStr, resp.StatusCode, string(b))
	}
	return decodeTrino(resp.Body)
}

func getTrino(ctx context.Context, client *http.Client, t *trinoDSN, urlStr string) (*trinoResp, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, err
	}
	applyTrinoHeaders(req, t)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("trino GET %s: %d %s", urlStr, resp.StatusCode, string(b))
	}
	return decodeTrino(resp.Body)
}

func applyTrinoHeaders(req *http.Request, t *trinoDSN) {
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("User-Agent", "aegis")
	if t.catalog != "" {
		if t.presto {
			req.Header.Set("X-Presto-Catalog", t.catalog)
		} else {
			req.Header.Set("X-Trino-Catalog", t.catalog)
		}
	}
	if t.schema != "" {
		if t.presto {
			req.Header.Set("X-Presto-Schema", t.schema)
		} else {
			req.Header.Set("X-Trino-Schema", t.schema)
		}
	}
	user := t.user
	if user == "" {
		user = "aegis"
	}
	req.Header.Set(t.userHeader(), user)
	if t.pass != "" {
		enc := base64.StdEncoding.EncodeToString([]byte(t.user + ":" + t.pass))
		req.Header.Set("Authorization", "Basic "+enc)
	}
}

func decodeTrino(r io.Reader) (*trinoResp, error) {
	var out trinoResp
	if err := json.NewDecoder(r).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode trino response: %w", err)
	}
	return &out, nil
}

// execTrino is the entry point used by Manager.ExecSQL for Trino/Presto.
func execTrino(ctx context.Context, ds *store.DataSource, sqlText string, isRead bool) (*RawResult, int64, error) {
	return runTrino(ctx, ds, sqlText, isRead)
}

// trinoListTables runs `SHOW TABLES` and returns the single-column result.
func trinoListTables(ctx context.Context, ds *store.DataSource) ([]string, error) {
	raw, _, err := runTrino(ctx, ds, "SHOW TABLES", true)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(raw.Rows))
	for _, row := range raw.Rows {
		for _, v := range row {
			out = append(out, fmt.Sprintf("%v", v))
			break
		}
	}
	return out, nil
}

// trinoDescribeTable runs `DESCRIBE "<table>"` and maps the result columns.
func trinoDescribeTable(ctx context.Context, ds *store.DataSource, table string) ([]ColumnMeta, error) {
	if !safeIdent(table) {
		return nil, fmt.Errorf("invalid table identifier %q", table)
	}
	raw, _, err := runTrino(ctx, ds, `DESCRIBE "`+table+`"`, true)
	if err != nil {
		return nil, err
	}
	out := make([]ColumnMeta, 0, len(raw.Rows))
	for _, row := range raw.Rows {
		name, _ := row["Column"].(string)
		typ, _ := row["Type"].(string)
		if name == "" {
			// Fall back to positional if column names differ.
			for _, v := range row {
				name = fmt.Sprintf("%v", v)
				break
			}
		}
		out = append(out, ColumnMeta{Name: name, Type: typ})
	}
	return out, nil
}
