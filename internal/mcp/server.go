package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/wisonwang/aegis/internal/auth"
	"github.com/wisonwang/aegis/internal/config"
	"github.com/wisonwang/aegis/internal/proxy"
	"github.com/wisonwang/aegis/internal/store"
	"github.com/google/uuid"
)

// Server implements the Model Context Protocol over Streamable HTTP so that
// AI agents can discover and query governed data sources.
type Server struct {
	proxy    *proxy.Proxy
	store    *store.Store
	cfg      *config.Config
	mu       sync.Mutex
	sessions map[string]*session
}

type session struct {
	id      string
	init    bool
	info    map[string]interface{}
}

func New(p *proxy.Proxy, st *store.Store, cfg *config.Config) *Server {
	return &Server{
		proxy:    p,
		store:    st,
		cfg:      cfg,
		sessions: map[string]*session{},
	}
}

type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *Server) Handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeRPC(w, r, "", nil, jsonrpcResponse{
			JSONRPC: "2.0",
			Error:   &jsonrpcError{Code: -32700, Message: "read error"},
		})
		return
	}

	// Single request or batch.
	var reqs []jsonrpcRequest
	if err := json.Unmarshal(body, &reqs); err != nil {
		// maybe a single object
		var single jsonrpcRequest
		if err2 := json.Unmarshal(body, &single); err2 != nil {
			writeRPC(w, r, "", nil, jsonrpcResponse{
				JSONRPC: "2.0",
				Error:   &jsonrpcError{Code: -32700, Message: "parse error"},
			})
			return
		}
		reqs = []jsonrpcRequest{single}
	}

	var responses []jsonrpcResponse
	var sessID string
	for _, req := range reqs {
		resp, sid := s.dispatch(r, req)
		if sid != "" {
			sessID = sid
		}
		if req.ID == nil && resp == nil {
			// notification: no response
			continue
		}
		if resp != nil {
			responses = append(responses, *resp)
		}
	}

	if len(responses) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if len(responses) == 1 {
		writeRPC(w, r, sessID, nil, responses[0])
		return
	}
	writeRPC(w, r, sessID, nil, responses)
}

func (s *Server) dispatch(r *http.Request, req jsonrpcRequest) (*jsonrpcResponse, string) {
	resp := &jsonrpcResponse{JSONRPC: "2.0", ID: req.ID}

	// Session bookkeeping for initialize / subsequent calls.
	if req.Method == "initialize" {
		s.mu.Lock()
		sid := uuid.NewString()
		s.sessions[sid] = &session{id: sid, init: true}
		s.mu.Unlock()
		resp.Result = map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]interface{}{
				"tools":     map[string]interface{}{},
				"resources": map[string]interface{}{},
				"prompts":   map[string]interface{}{},
			},
			"serverInfo": map[string]interface{}{"name": "aegis-mcp", "version": "0.2.0"},
		}
		return resp, sid
	}

	// notifications/initialized and other notifications: accept, no reply.
	if req.Method == "notifications/initialized" || strings.HasPrefix(req.Method, "notifications/") {
		return nil, ""
	}

	switch req.Method {
	case "ping":
		resp.Result = map[string]interface{}{}
	case "tools/list":
		resp.Result = map[string]interface{}{"tools": toolsList()}
	case "tools/call":
		res, err := s.callTool(r, req.Params)
		if err != nil {
			resp.Error = &jsonrpcError{Code: -32000, Message: err.Error()}
		} else {
			resp.Result = res
		}
	case "resources/list":
		res, err := s.listResources(r)
		if err != nil {
			resp.Error = &jsonrpcError{Code: -32000, Message: err.Error()}
		} else {
			resp.Result = map[string]interface{}{"resources": res}
		}
	case "resources/templates/list":
		resp.Result = map[string]interface{}{"resourceTemplates": resourceTemplates()}
	case "resources/read":
		res, err := s.readResource(r, req.Params)
		if err != nil {
			resp.Error = &jsonrpcError{Code: -32000, Message: err.Error()}
		} else {
			resp.Result = res
		}
	case "prompts/list":
		resp.Result = map[string]interface{}{"prompts": promptsList()}
	case "prompts/get":
		res, err := s.getPrompt(r, req.Params)
		if err != nil {
			resp.Error = &jsonrpcError{Code: -32000, Message: err.Error()}
		} else {
			resp.Result = res
		}
	default:
		resp.Error = &jsonrpcError{Code: -32601, Message: "method not found: " + req.Method}
	}
	return resp, ""
}

// callTool resolves the principal, dispatches to a tool, and returns an MCP
// tool result (content array).
func (s *Server) callTool(r *http.Request, params json.RawMessage) (interface{}, error) {
	claims, err := s.principal(r)
	if err != nil {
		return nil, err
	}
	var p struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params")
	}
	args := p.Arguments
	if args == nil {
		args = map[string]interface{}{}
	}

	var payload interface{}
	switch p.Name {
	case "list_datasources":
		ds, err := s.store.ListDataSources()
		if err != nil {
			return nil, err
		}
		payload = ds
	case "list_tables":
		dsName, _ := args["datasource"].(string)
		dsID, err := resolveDatasource(s.store, dsName)
		if err != nil {
			return nil, err
		}
		tables, err := s.proxy.ListTables(r.Context(), dsID, claims)
		if err != nil {
			return nil, err
		}
		payload = map[string]interface{}{"tables": tables}
	case "describe_table":
		dsName, _ := args["datasource"].(string)
		table, _ := args["table"].(string)
		dsID, err := resolveDatasource(s.store, dsName)
		if err != nil {
			return nil, err
		}
		cols, err := s.proxy.DescribeTable(r.Context(), dsID, table, claims)
		if err != nil {
			return nil, err
		}
		payload = map[string]interface{}{"table": table, "columns": cols}
	case "query":
		dsName, _ := args["datasource"].(string)
		sql, _ := args["sql"].(string)
		rawParams, _ := args["params"].([]interface{})
		dsID, err := resolveDatasource(s.store, dsName)
		if err != nil {
			return nil, err
		}
		// Link every query an agent issues in one conversation. Prefer a
		// client-supplied session_id, then the MCP transport session header,
		// then a freshly generated id.
		sessionID, _ := args["session_id"].(string)
		if sessionID == "" {
			if h := r.Header.Get("Mcp-Session-Id"); h != "" {
				sessionID = h
			} else {
				sessionID = uuid.NewString()
			}
		}
		ctx := proxy.WithSession(proxy.WithChannel(r.Context(), "mcp"), sessionID)
		res, err := s.proxy.Execute(ctx, dsID, claims, sql, rawParams...)
		if err != nil {
			return nil, err
		}
		payload = map[string]interface{}{
			"session_id":   sessionID,
			"queryResult":  res,
		}
	case "nl2sql":
		dsName, _ := args["datasource"].(string)
		question, _ := args["question"].(string)
		sqlHint, _ := args["sql_hint"].(string)
		dsID, err := resolveDatasource(s.store, dsName)
		if err != nil {
			return nil, err
		}
		// Link every query an agent issues in one conversation (same as query).
		sessionID, _ := args["session_id"].(string)
		if sessionID == "" {
			if h := r.Header.Get("Mcp-Session-Id"); h != "" {
				sessionID = h
			} else {
				sessionID = uuid.NewString()
			}
		}
		ctx := proxy.WithSession(proxy.WithChannel(r.Context(), "mcp"), sessionID)
		res, gen, err := s.proxy.NL2SQL(ctx, dsID, claims, question, sqlHint)
		if err != nil {
			return nil, err
		}
		payload = map[string]interface{}{
			"session_id":    sessionID,
			"generated_sql": gen.SQL,
			"explanation":   gen.Explanation,
			"queryResult":   res,
		}
	case "get_catalog":
		dsName, _ := args["datasource"].(string)
		dsID, err := resolveDatasource(s.store, dsName)
		if err != nil {
			return nil, err
		}
		schema, err := s.proxy.Catalog(r.Context(), dsID, claims)
		if err != nil {
			return nil, err
		}
		payload = schema
	case "list_datasets":
		datasets, err := s.proxy.ListDatasets(r.Context(), claims)
		if err != nil {
			return nil, err
		}
		payload = map[string]interface{}{"datasets": datasets}
	case "get_dataset_catalog":
		name, _ := args["name"].(string)
		if name == "" {
			return nil, fmt.Errorf("name is required")
		}
		d, err := s.store.GetDatasetByName(name)
		if err != nil {
			return nil, err
		}
		if d == nil {
			return nil, fmt.Errorf("dataset %q not found", name)
		}
		schema, err := s.proxy.DatasetCatalog(r.Context(), d.ID, claims)
		if err != nil {
			return nil, err
		}
		payload = schema
	default:
		return nil, fmt.Errorf("unknown tool: %s", p.Name)
	}

	text, _ := json.MarshalIndent(payload, "", "  ")
	return map[string]interface{}{
		"content": []map[string]interface{}{
			{"type": "text", "text": string(text)},
		},
	}, nil
}

// principal resolves the caller from a Bearer JWT or the static MCP API key.
func (s *Server) principal(r *http.Request) (*auth.Claims, error) {
	if az := r.Header.Get("Authorization"); strings.HasPrefix(az, "Bearer ") {
		return auth.ParseToken(strings.TrimPrefix(az, "Bearer "), s.cfg.JWTSecret)
	}
	apiKey := r.Header.Get("X-MCP-API-Key")
	if apiKey == "" {
		apiKey = r.Header.Get("Mcp-Api-Key")
	}
	if apiKey != "" && apiKey == s.cfg.MCP.APIKey && s.cfg.MCP.APIKey != "" {
		u, err := s.store.GetUserByUsername("mcp-agent")
		if err != nil {
			return nil, err
		}
		if u == nil {
			return nil, fmt.Errorf("mcp service account (mcp-agent) is not configured")
		}
		roles, _ := s.store.ListRolesForUser(u.ID)
		names := make([]string, 0, len(roles))
		for _, role := range roles {
			names = append(names, role.Name)
		}
		attrs, _ := s.store.UserAttributes(u.ID)
		return &auth.Claims{
			UserID:      u.ID,
			Username:    u.Username,
			DisplayName: u.DisplayName,
			Roles:       names,
			Attributes:  attrs,
		}, nil
	}
	return nil, fmt.Errorf("unauthorized: provide a Bearer token or MCP API key")
}

func resolveDatasource(st *store.Store, idOrName string) (string, error) {
	if idOrName == "" {
		return "", fmt.Errorf("datasource is required")
	}
	ds, err := st.GetDataSource(idOrName)
	if err == nil && ds != nil {
		return ds.ID, nil
	}
	all, err := st.ListDataSources()
	if err != nil {
		return "", err
	}
	for _, d := range all {
		if d.Name == idOrName {
			return d.ID, nil
		}
	}
	return "", fmt.Errorf("datasource not found: %s", idOrName)
}

func writeRPC(w http.ResponseWriter, r *http.Request, sessID string, _ interface{}, v interface{}) {
	if sessID != "" {
		w.Header().Set("Mcp-Session-Id", sessID)
	}
	accept := r.Header.Get("Accept")
	if strings.Contains(accept, "text/event-stream") {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		b, _ := json.Marshal(v)
		_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", b)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(v)
}

// toolsList returns the MCP tool catalog exposed to agents.
func toolsList() []map[string]interface{} {
	return []map[string]interface{}{
		{
			"name":        "list_datasources",
			"description": "List the data sources registered in the platform.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "list_tables",
			"description": "List the tables a caller may access on a data source (respecting table permissions).",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"datasource": map[string]interface{}{"type": "string", "description": "data source id or name"},
				},
				"required": []string{"datasource"},
			},
		},
		{
			"name":        "describe_table",
			"description": "Describe a table's columns, with denied/non-allowed columns removed per the caller's column permissions.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"datasource": map[string]interface{}{"type": "string"},
					"table":      map[string]interface{}{"type": "string"},
				},
				"required": []string{"datasource", "table"},
			},
		},
		{
			"name":        "get_catalog",
			"description": "Return the governed, semantically enriched schema of a data source: accessible tables and columns with business descriptions, synonyms and example values. Use this before writing SQL to understand column meaning and improve NL2SQL accuracy. Columns you may not access are already omitted.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"datasource": map[string]interface{}{"type": "string", "description": "data source id or name"},
				},
				"required": []string{"datasource"},
			},
		},
		{
			"name":        "list_datasets",
			"description": "List the curated datasets the caller may consume (published, access-granted data products). Use this to discover governed data products before querying them.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "get_dataset_catalog",
			"description": "Return the governed, semantically enriched contract of a dataset: its stable fields with business descriptions, synonyms and example values, plus any value masking. Use this before querying a dataset to understand its columns.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{"type": "string", "description": "dataset name"},
				},
				"required": []string{"name"},
			},
		},
		{
			"name":        "query",
			"description": "Run a governed SQL query against a data source. Table, row, and column permissions are enforced and the rewritten SQL is returned for transparency.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"datasource": map[string]interface{}{"type": "string"},
					"sql":        map[string]interface{}{"type": "string", "description": "SQL statement"},
					"params":     map[string]interface{}{"type": "array", "description": "optional query parameters"},
					"session_id": map[string]interface{}{"type": "string", "description": "optional id linking queries from one AI conversation; echoed back for threading"},
				},
				"required": []string{"datasource", "sql"},
			},
		},
		{
			"name":        "nl2sql",
			"description": "Translate a natural-language question into a governed SQL query and run it. Only read-only SQL is ever executed; table/row/column governance and value masking still apply. Use this to let a user ask in plain language. Prefer get_catalog first so the model sees column meanings.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"datasource": map[string]interface{}{"type": "string", "description": "data source id or name"},
					"question":   map[string]interface{}{"type": "string", "description": "the natural-language question"},
					"sql_hint":   map[string]interface{}{"type": "string", "description": "optional hand-written SQL to prefer over free generation"},
					"session_id": map[string]interface{}{"type": "string", "description": "optional id linking queries from one AI conversation; echoed back for threading"},
				},
				"required": []string{"datasource", "question"},
			},
		},
	}
}
