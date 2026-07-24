package mcp

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// promptsList advertises the reusable prompt templates Aegis offers to
// agents. The nl2sql prompt injects the governed semantic schema so the model
// writes SQL grounded in real, permitted columns.
func promptsList() []map[string]interface{} {
	return []map[string]interface{}{
		{
			"name":        "nl2sql",
			"description": "Translate a natural-language question into a safe SQL query for a governed data source. The governed semantic schema (accessible tables/columns with business meaning) is injected automatically.",
			"arguments": []map[string]interface{}{
				{"name": "datasource", "description": "data source id or name", "required": true},
				{"name": "question", "description": "the natural-language question", "required": true},
				{"name": "dialect", "description": "SQL dialect hint (mysql|postgres|sqlite)", "required": false},
			},
		},
	}
}

// getPrompt renders a prompt template into concrete messages. For nl2sql it
// builds a system message containing the caller's governed schema plus rules,
// and a user message carrying the question.
func (s *Server) getPrompt(r *http.Request, params json.RawMessage) (interface{}, error) {
	var p struct {
		Name      string            `json:"name"`
		Arguments map[string]string `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params")
	}
	switch p.Name {
	case "nl2sql":
		return s.nl2sqlPrompt(r, p.Arguments)
	default:
		return nil, fmt.Errorf("unknown prompt: %s", p.Name)
	}
}

func (s *Server) nl2sqlPrompt(r *http.Request, args map[string]string) (interface{}, error) {
	claims, err := s.principal(r)
	if err != nil {
		return nil, err
	}
	dsName := args["datasource"]
	question := args["question"]
	if dsName == "" || question == "" {
		return nil, fmt.Errorf("datasource and question are required")
	}
	dsID, err := resolveDatasource(s.store, dsName)
	if err != nil {
		return nil, err
	}
	schema, err := s.proxy.Catalog(r.Context(), dsID, claims)
	if err != nil {
		return nil, err
	}
	dialect := args["dialect"]
	if dialect == "" {
		if ds, err := s.store.GetDataSource(dsID); err == nil && ds != nil {
			dialect = ds.Type
		}
	}

	system := fmt.Sprintf(`You are a SQL generation assistant for the governed data platform "Aegis".
Write a single valid %s SELECT statement that answers the user's question, using ONLY the tables and columns in the schema below.

Rules:
- Use only tables/columns present in the schema. Never invent columns; if a needed column is missing, say so instead of guessing.
- Prefer the business meaning described for each column; match user terms against column descriptions and synonyms.
- Row-level filtering (e.g. tenant/owner scoping) is enforced automatically by the platform — do NOT add such filters yourself.
- A column marked "[class: pii]" or "[class: restricted]" carries personal or sensitive data, and "[class: confidential]" marks financial/commercially sensitive data. Treat these columns with care: prefer aggregates (COUNT, GROUP BY, sums) over listing individual records, and never echo raw PII values back in free-text answers. Masks shown as "[masked: ...]" are already applied by the platform — keep them masked.
- Return only the SQL, then a one-line explanation. Do not include DROP/DELETE/UPDATE/INSERT.

%s`, dialectName(dialect), schema.CatalogMarkdown())

	return map[string]interface{}{
		"description": fmt.Sprintf("NL2SQL for data source %q", dsName),
		"messages": []map[string]interface{}{
			{"role": "system", "content": map[string]interface{}{"type": "text", "text": system}},
			{"role": "user", "content": map[string]interface{}{"type": "text", "text": question}},
		},
	}, nil
}

func dialectName(t string) string {
	switch t {
	case "postgres", "postgresql":
		return "PostgreSQL"
	case "sqlite":
		return "SQLite"
	case "mysql":
		return "MySQL"
	case "":
		return "SQL"
	default:
		return t
	}
}
