package nl2sql

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// LLMConfig tunes the OpenAI-compatible chat-completions backend.
type LLMConfig struct {
	BaseURL    string // e.g. https://api.openai.com/v1 or https://ark.cn-beijing.volces.com/api/v3
	APIKey     string
	Model      string
	Timeout    time.Duration
	MaxRetries int
}

// LLMGenerator produces SQL via an OpenAI-compatible /chat/completions API.
// It forces JSON output and a read-only system prompt, then validates the
// returned SQL so a misbehaving model cannot smuggle a write past governance.
type LLMGenerator struct {
	cfg    LLMConfig
	client *http.Client
}

// NewLLMGenerator constructs an LLMGenerator with sane defaults.
func NewLLMGenerator(cfg LLMConfig) *LLMGenerator {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 2
	}
	return &LLMGenerator{cfg: cfg, client: &http.Client{Timeout: cfg.Timeout}}
}

// systemPrompt is the contract the model must obey. It is deliberately
// restrictive: read-only, schema-bound, governance-aware.
func systemPrompt(dialect, schema string) string {
	var b strings.Builder
	b.WriteString("You are a precise SQL generator for a strictly governed data source. ")
	b.WriteString("Follow these rules exactly:\n")
	b.WriteString("1. Generate ONLY a single read-only SQL statement (SELECT or WITH). ")
	b.WriteString("Never produce INSERT/UPDATE/DELETE/DDL or any administrative command.\n")
	b.WriteString("2. Reference ONLY the tables and columns listed in the schema below. ")
	b.WriteString("Columns the caller may not access are already omitted — do not invent them.\n")
	b.WriteString("3. Do not attempt to circumvent row/column policies; the platform enforces them after you return SQL.\n")
	b.WriteString("4. Use ")
	b.WriteString(dialect)
	b.WriteString(" dialect syntax.\n")
	b.WriteString("5. Return STRICT JSON of the form {\"sql\": \"<statement>\", \"explanation\": \"<one short sentence>\"}.\n")
	b.WriteString("6. Do not wrap the SQL in markdown fences. Do not add a trailing semicolon unless the dialect requires it.\n\n")
	b.WriteString("## Governed schema\n")
	b.WriteString(schema)
	return b.String()
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model          string        `json:"model"`
	Messages       []chatMessage `json:"messages"`
	Temperature    float64       `json:"temperature"`
	ResponseFormat struct {
		Type string `json:"type"`
	} `json:"response_format"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Generate implements Generator.
func (g *LLMGenerator) Generate(ctx context.Context, req *Request) (*Result, error) {
	if strings.TrimSpace(req.SQLHint) != "" {
		if err := ValidateReadOnly(req.SQLHint); err != nil {
			return nil, err
		}
		return &Result{SQL: strings.TrimSpace(req.SQLHint), Explanation: "used supplied sql_hint"}, nil
	}
	if strings.TrimSpace(req.Question) == "" {
		return nil, fmt.Errorf("question is required")
	}
	if strings.TrimSpace(req.SchemaMarkdown) == "" {
		return nil, fmt.Errorf("no accessible schema to generate SQL from")
	}

	dialect := req.Dialect
	if dialect == "" {
		dialect = "standard SQL"
	}
	userContent := "Question: " + req.Question + "\n\nGenerate the SQL now."

	var lastErr error
	for attempt := 0; attempt <= g.cfg.MaxRetries; attempt++ {
		res, err := g.callOnce(ctx, dialect, req.SchemaMarkdown, userContent)
		if err == nil {
			return res, nil
		}
		lastErr = err
		// Retry only on transient transport errors; bad JSON/validation
		// failures are not worth retrying.
		if !isTransient(err) {
			return nil, err
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("nl2sql generation failed")
	}
	return nil, lastErr
}

func (g *LLMGenerator) callOnce(ctx context.Context, dialect, schema, userContent string) (*Result, error) {
	body := chatRequest{
		Model: g.cfg.Model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt(dialect, schema)},
			{Role: "user", Content: userContent},
		},
		Temperature: 0,
	}
	body.ResponseFormat.Type = "json_object"

	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	url := strings.TrimRight(g.cfg.BaseURL, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+g.cfg.APIKey)

	resp, err := g.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("provider returned %d: %s", resp.StatusCode, firstLine(respBody))
	}

	var cr chatResponse
	if err := json.Unmarshal(respBody, &cr); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if cr.Error != nil {
		return nil, fmt.Errorf("provider error: %s", cr.Error.Message)
	}
	if len(cr.Choices) == 0 {
		return nil, fmt.Errorf("provider returned no choices")
	}
	content := strings.TrimSpace(cr.Choices[0].Message.Content)
	if content == "" {
		return nil, fmt.Errorf("provider returned empty content")
	}

	// The model should return JSON; if it wrapped in fences, strip them.
	content = stripFences(content)
	var parsed struct {
		SQL         string `json:"sql"`
		Explanation string `json:"explanation"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		// Some models return bare SQL instead of JSON; tolerate that.
		parsed.SQL = content
		parsed.Explanation = "parsed as bare SQL"
	}
	parsed.SQL = strings.TrimSpace(parsed.SQL)
	if parsed.SQL == "" {
		return nil, fmt.Errorf("provider returned no sql field")
	}
	if err := ValidateReadOnly(parsed.SQL); err != nil {
		return nil, fmt.Errorf("generated SQL rejected: %w", err)
	}
	return &Result{SQL: parsed.SQL, Explanation: parsed.Explanation}, nil
}

func stripFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		// Drop the opening fence (``` or ```json) and the closing fence.
		if i := strings.Index(s, "\n"); i >= 0 {
			s = s[i+1:]
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	}
	return strings.TrimSpace(s)
}

func isTransient(err error) bool {
	msg := err.Error()
	for _, frag := range []string{"http:", "connection", "timeout", "i/o timeout", "EOF", "reset by peer"} {
		if strings.Contains(msg, frag) {
			return true
		}
	}
	return false
}

func firstLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}
