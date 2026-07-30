package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/wisonwang/aegis/internal/auth"
	"github.com/wisonwang/aegis/internal/datasource"
	"github.com/wisonwang/aegis/internal/logging"
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
	ID           string            `json:"id"`
	Username     string            `json:"username"`
	DisplayName  string            `json:"display_name"`
	Email        string            `json:"email"`
	Type         string            `json:"type"`
	Roles        []string          `json:"roles"`
	Attributes   map[string]string `json:"attributes"`
	LastLoginAt  string            `json:"last_login_at"`
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
// @Summary Authenticate and obtain a JWT
// @Tags auth
// @Accept json
// @Produce json
// @Param request body loginRequest true "login credentials"
// @Success 200 {object} loginResponse
// @Router /api/v1/login [post]
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
	// Service accounts authenticate via API key only — never via password.
	if u == nil || u.Status != "active" || u.Type == "service" || !auth.CheckPassword(u.PasswordHash, req.Password) {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	if err := h.Store.UpdateLastLogin(u.ID); err != nil {
		logging.With("error", err.Error()).Warn("update last login failed")
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
// @Summary Get the current principal's profile
// @Tags auth
// @Produce json
// @Security BearerAuth
// @Success 200 {object} meResponse
// @Router /api/v1/me [get]
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
// @Summary Execute a governed query
// @Tags dataapi
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body queryRequest true "query request"
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/query [post]
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
	dsID, err := h.resolveDS(r.Context(), req.DataSource)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ds, derr := h.Store.GetDataSource(r.Context(), dsID)
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
// @Summary Natural-language question to governed SQL
// @Tags dataapi
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "datasource id or name"
// @Param request body nl2sqlRequest true "nl2sql request"
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/datasources/{id}/nl2sql [post]
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
	target := pathParam(r, "id")
	if target == "" {
		target = req.DataSource
	}
	dsID, err := h.resolveDS(r.Context(), target)
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
// @Summary List registered datasources
// @Tags dataapi
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/datasources [get]
func (h *Handler) ListDataSources(w http.ResponseWriter, r *http.Request) {
	ds, err := h.Store.ListDataSources(r.Context())
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
// @Summary List accessible tables on a datasource
// @Tags dataapi
// @Produce json
// @Security BearerAuth
// @Param id path string true "datasource id or name"
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/datasources/{id}/tables [get]
func (h *Handler) ListTables(w http.ResponseWriter, r *http.Request) {
	dsID, err := h.resolveDS(r.Context(), pathParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
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
// @Summary Describe table columns (governed)
// @Tags dataapi
// @Produce json
// @Security BearerAuth
// @Param id path string true "datasource id or name"
// @Param table path string true "table name"
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/datasources/{id}/tables/{table} [get]
func (h *Handler) DescribeTable(w http.ResponseWriter, r *http.Request) {
	dsID, err := h.resolveDS(r.Context(), pathParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	table := pathParam(r, "table")
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
// @Summary Governed, semantically enriched schema catalog
// @Tags dataapi
// @Produce json
// @Security BearerAuth
// @Param id path string true "datasource id or name"
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/datasources/{id}/catalog [get]
func (h *Handler) Catalog(w http.ResponseWriter, r *http.Request) {
	dsID, err := h.resolveDS(r.Context(), pathParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
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

// metricRunRequest is the body for running a curated metric.
type metricRunRequest struct {
	Params    map[string]interface{} `json:"params"`     // parameter name -> value
	SessionID string                 `json:"session_id"` // optional; links queries from one AI conversation
}

// ListMetrics returns the curated metric definitions registered on a
// datasource. Any authenticated principal may list them; executing a metric is
// still subject to table/row/column governance like any other query.
// @Summary List curated metrics on a datasource
// @Tags dataapi
// @Produce json
// @Security BearerAuth
// @Param id path string true "datasource id or name"
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/datasources/{id}/metrics [get]
func (h *Handler) ListMetrics(w http.ResponseWriter, r *http.Request) {
	dsID, err := h.resolveDS(r.Context(), pathParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "datasource not found")
		return
	}
	metrics, err := h.Store.ListMetrics(r.Context(), dsID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"metrics": metrics})
}

// RunMetric resolves a curated metric with caller-supplied parameters and
// returns the governed result plus lineage. The metric's SQL template is
// rendered with SQL-safe literals and executed through the same governed path
// as Query/NL2SQL, so governance and audit all still apply.
// @Summary Run a curated metric with parameters
// @Tags dataapi
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "datasource id or name"
// @Param name path string true "metric name"
// @Param request body metricRunRequest true "metric run request"
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/datasources/{id}/metrics/{name}/run [post]
func (h *Handler) RunMetric(w http.ResponseWriter, r *http.Request) {
	dsID, err := h.resolveDS(r.Context(), pathParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "datasource not found")
		return
	}
	metricName := pathParam(r, "name")
	if metricName == "" {
		writeError(w, http.StatusBadRequest, "metric name is required")
		return
	}
	var req metricRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	c := claimsFromContext(r.Context())
	sessionID := req.SessionID
	if sessionID == "" {
		sessionID = uuid.NewString()
	}
	ctx := proxy.WithSession(proxy.WithChannel(r.Context(), "dataapi"), sessionID)
	res, err := h.Proxy.ResolveMetric(ctx, dsID, c, metricName, req.Params)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"sql":           res.SQL,
		"lineage":      res.Lineage,
		"query_result": res.QueryResult,
		"session_id":   sessionID,
	})
}

// estimateRequest is the body for a pre-run cost/risk estimate.
type estimateRequest struct {
	DataSource string `json:"datasource"` // id or name
	SQL        string `json:"sql"`        // SQL statement to estimate
	SessionID  string `json:"session_id"` // optional; links queries from one AI conversation
}

// EstimateQuery returns a cost/risk preview of a SQL statement WITHOUT
// executing it. The statement is run through the same governance rewrite as
// Query, so row/column policies and masking are already reflected in the
// EXPLAIN plan, but no data is read or written. Agents use it to decide
// whether to run a query — tighten a filter, avoid a large scan, or
// handle PII carefully.
// @Summary Estimate query cost/risk without executing
// @Tags dataapi
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "datasource id or name"
// @Param request body estimateRequest true "estimate request"
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/datasources/{id}/query/estimate [post]
func (h *Handler) EstimateQuery(w http.ResponseWriter, r *http.Request) {
	var req estimateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if strings.TrimSpace(req.SQL) == "" {
		writeError(w, http.StatusBadRequest, "sql is required")
		return
	}
	// Prefer the datasource id/name from the URL path; allow an explicit
	// body field for callers that POST to a fixed endpoint.
	target := pathParam(r, "id")
	if target == "" {
		target = req.DataSource
	}
	dsID, err := h.resolveDS(r.Context(), target)
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
	est, err := h.Proxy.Estimate(ctx, dsID, c, req.SQL)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, est)
}

// resolveDS resolves a datasource id or name to its id.
func (h *Handler) resolveDS(ctx context.Context, idOrName string) (string, error) {
	if idOrName == "" {
		return "", errStr("datasource is required")
	}
	ds, err := h.Store.GetDataSource(ctx, idOrName)
	if err == nil && ds != nil {
		return ds.ID, nil
	}
	all, err := h.Store.ListDataSources(ctx)
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
	lastLogin := ""
	if u.LastLoginAt.Valid {
		lastLogin = u.LastLoginAt.Time.Format(time.RFC3339)
	}
	return &meResponse{
		ID:           u.ID,
		Username:     u.Username,
		DisplayName:  u.DisplayName,
		Email:        u.Email,
		Type:         u.Type,
		Roles:        roles,
		Attributes:   attrs,
		LastLoginAt:  lastLogin,
	}
}

type errStr string

func (e errStr) Error() string { return string(e) }
