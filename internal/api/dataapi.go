package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/wisonwang/aegis/internal/auth"
	"github.com/wisonwang/aegis/internal/datasource"
	"github.com/wisonwang/aegis/internal/proxy"
	"github.com/wisonwang/aegis/internal/store"
)

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginResponse struct {
	Token string      `json:"token"`
	User  *meResponse `json:"user"`
}

type meResponse struct {
	ID          string            `json:"id"`
	Username    string            `json:"username"`
	DisplayName string            `json:"display_name"`
	Roles       []string          `json:"roles"`
	Attributes  map[string]string `json:"attributes"`
}

type queryRequest struct {
	DataSource string          `json:"datasource"` // id or name
	SQL        string          `json:"sql"`        // SQL for SQL-family backends
	Query      json.RawMessage `json:"query"`      // backend-specific JSON for NoSQL (mongo/es)
	Params     []interface{}   `json:"params"`
	SessionID  string          `json:"session_id"` // optional; links queries from one AI conversation
}

// queryResponse wraps the governed result with the session id used for this
// call, so clients can thread it across subsequent queries in the same chat.
type queryResponse struct {
	*proxy.QueryResult
	SessionID string `json:"session_id"`
}

// Login authenticates a principal and returns a JWT.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	u, err := h.Store.GetUserByUsername(req.Username)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if u == nil || u.Status != "active" || !auth.CheckPassword(u.PasswordHash, req.Password) {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	roles, err := h.Store.ListRolesForUser(u.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	roleNames := make([]string, 0, len(roles))
	for _, role := range roles {
		roleNames = append(roleNames, role.Name)
	}
	attrs, _ := h.Store.UserAttributes(u.ID)
	claims := &auth.Claims{
		UserID:      u.ID,
		Username:    u.Username,
		DisplayName: u.DisplayName,
		Roles:       roleNames,
		Attributes:  attrs,
	}
	tok, err := auth.GenerateToken(claims, h.Cfg.JWTSecret, h.Cfg.JWTExpiry)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "token generation failed")
		return
	}
	writeJSON(w, http.StatusOK, loginResponse{
		Token: tok,
		User:  toMe(u, roleNames, attrs),
	})
}

// Me returns the authenticated principal's profile.
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	c := claimsFromContext(r.Context())
	u, err := h.Store.GetUser(c.UserID)
	if err != nil || u == nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	writeJSON(w, http.StatusOK, toMe(u, c.Roles, c.Attributes))
}

// Query executes a governed statement. For SQL-family backends it takes `sql`;
// for NoSQL backends (mongo/es) it takes `query` (a backend-specific JSON
// document, e.g. {"collection":...,"filter":...} for Mongo).
func (h *Handler) Query(w http.ResponseWriter, r *http.Request) {
	var req queryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.SQL == "" && len(req.Query) == 0 {
		writeError(w, http.StatusBadRequest, "sql or query is required")
		return
	}
	dsID, err := h.resolveDS(req.DataSource)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ds, derr := h.Store.GetDataSource(dsID)
	if derr != nil || ds == nil {
		writeError(w, http.StatusBadRequest, "datasource not found")
		return
	}
	c := claimsFromContext(r.Context())
	sessionID := req.SessionID
	if sessionID == "" {
		sessionID = uuid.NewString()
	}
	ctx := proxy.WithSession(proxy.WithChannel(r.Context(), "dataapi"), sessionID)
	var res *proxy.QueryResult
	if datasource.IsNoSQL(ds.Type) {
		if len(req.Query) == 0 {
			writeError(w, http.StatusBadRequest, "query (JSON) is required for NoSQL datasource")
			return
		}
		res, err = h.Proxy.Execute(ctx, dsID, c, string(req.Query))
	} else {
		if req.SQL == "" {
			writeError(w, http.StatusBadRequest, "sql is required for SQL datasource")
			return
		}
		res, err = h.Proxy.Execute(ctx, dsID, c, req.SQL, req.Params...)
	}
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, queryResponse{QueryResult: res, SessionID: sessionID})
}

// nl2sqlRequest is the body for the NL2SQL gateway endpoint.
type nl2sqlRequest struct {
	DataSource string `json:"datasource"` // id or name
	Question   string `json:"question"`   // natural-language question
	SQLHint    string `json:"sql_hint"`   // optional hand-written SQL to prefer over generation
	SessionID  string `json:"session_id"` // optional; links queries from one AI conversation
}

// NL2SQL turns a natural-language question into a governed SQL query and runs
// it. The generated SQL is executed through the same governed path as Query,
// so table/row/column governance, masking and the audit trail all still apply.
func (h *Handler) NL2SQL(w http.ResponseWriter, r *http.Request) {
	var req nl2sqlRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if strings.TrimSpace(req.Question) == "" && strings.TrimSpace(req.SQLHint) == "" {
		writeError(w, http.StatusBadRequest, "question or sql_hint is required")
		return
	}
	// Prefer the datasource id/name from the URL path; allow an explicit body
	// field for callers that POST to a fixed endpoint. resolveDS accepts an
	// id or a name.
	target := r.PathValue("id")
	if target == "" {
		target = req.DataSource
	}
	dsID, err := h.resolveDS(target)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	c := claimsFromContext(r.Context())
	sessionID := req.SessionID
	if sessionID == "" {
		sessionID = uuid.NewString()
	}
	ctx := proxy.WithSession(proxy.WithChannel(r.Context(), "dataapi"), sessionID)
	res, gen, err := h.Proxy.NL2SQL(ctx, dsID, c, req.Question, req.SQLHint)
	if err != nil {
		if gen == nil {
			// Generation failure / not configured: server-side issue.
			writeError(w, http.StatusBadGateway, err.Error())
		} else {
			// Generation succeeded but governance denied execution.
			writeError(w, http.StatusForbidden, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"generated_sql": gen.SQL,
		"explanation":   gen.Explanation,
		"query_result":  res,
		"session_id":    sessionID,
	})
}

// ListDataSources returns the registered datasources (id, name, type).
func (h *Handler) ListDataSources(w http.ResponseWriter, r *http.Request) {
	ds, err := h.Store.ListDataSources()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]string, 0, len(ds))
	for _, d := range ds {
		out = append(out, map[string]string{"id": d.ID, "name": d.Name, "type": d.Type})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"datasources": out})
}

// ListTables returns tables the principal may access on a datasource.
func (h *Handler) ListTables(w http.ResponseWriter, r *http.Request) {
	dsID := r.PathValue("id")
	if _, err := h.Store.GetDataSource(dsID); err != nil || dsID == "" {
		writeError(w, http.StatusNotFound, "datasource not found")
		return
	}
	c := claimsFromContext(r.Context())
	tables, err := h.Proxy.ListTables(r.Context(), dsID, c)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"tables": tables})
}

// DescribeTable returns column metadata for a table (governed).
func (h *Handler) DescribeTable(w http.ResponseWriter, r *http.Request) {
	dsID := r.PathValue("id")
	table := r.PathValue("table")
	c := claimsFromContext(r.Context())
	cols, err := h.Proxy.DescribeTable(r.Context(), dsID, table, c)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"table": table, "columns": cols})
}

// Catalog returns the governed, semantically enriched schema of a datasource
// for the caller: the tables/columns they may access, with business
// descriptions, synonyms and example values. This is exactly what the NL2SQL
// gateway feeds to the model.
func (h *Handler) Catalog(w http.ResponseWriter, r *http.Request) {
	dsID := r.PathValue("id")
	if _, err := h.Store.GetDataSource(dsID); err != nil || dsID == "" {
		writeError(w, http.StatusNotFound, "datasource not found")
		return
	}
	c := claimsFromContext(r.Context())
	schema, err := h.Proxy.Catalog(r.Context(), dsID, c)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, schema)
}

// resolveDS resolves a datasource id or name to its id.
func (h *Handler) resolveDS(idOrName string) (string, error) {
	if idOrName == "" {
		return "", errStr("datasource is required")
	}
	ds, err := h.Store.GetDataSource(idOrName)
	if err == nil && ds != nil {
		return ds.ID, nil
	}
	all, err := h.Store.ListDataSources()
	if err != nil {
		return "", err
	}
	for _, d := range all {
		if d.Name == idOrName {
			return d.ID, nil
		}
	}
	return "", errStr("datasource not found: " + idOrName)
}

func toMe(u *store.User, roles []string, attrs map[string]string) *meResponse {
	return &meResponse{
		ID:          u.ID,
		Username:    u.Username,
		DisplayName: u.DisplayName,
		Roles:       roles,
		Attributes:  attrs,
	}
}

type errStr string

func (e errStr) Error() string { return string(e) }
