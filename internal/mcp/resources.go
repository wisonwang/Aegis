package mcp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// uriPrefix is the scheme Aegis uses for its MCP resources.
const uriPrefix = "aegis://"

// listResources exposes one "schema" resource per data source the caller may
// access. Reading a resource returns the governed, semantically enriched
// schema (see proxy.Catalog) that helps an agent write correct SQL.
func (s *Server) listResources(r *http.Request) ([]map[string]interface{}, error) {
	claims, err := s.principal(r)
	if err != nil {
		return nil, err
	}
	dss, err := s.store.ListDataSources()
	if err != nil {
		return nil, err
	}
	out := []map[string]interface{}{}
	for _, ds := range dss {
		// Only surface data sources the caller has at least one accessible table in.
		schema, err := s.proxy.Catalog(r.Context(), ds.ID, claims)
		if err != nil || schema == nil || len(schema.Tables) == 0 {
			continue
		}
		out = append(out, map[string]interface{}{
			"uri":         uriPrefix + ds.Name + "/schema",
			"name":        ds.Name + " schema",
			"description": fmt.Sprintf("Governed semantic schema for data source %q (%d accessible tables).", ds.Name, len(schema.Tables)),
			"mimeType":    "text/markdown",
		})
	}
	return out, nil
}

// resourceTemplates advertises the URI shape so agents can construct reads.
func resourceTemplates() []map[string]interface{} {
	return []map[string]interface{}{
		{
			"uriTemplate": uriPrefix + "{datasource}/schema",
			"name":        "Data source semantic schema",
			"description": "Governed schema with business descriptions for a data source.",
			"mimeType":    "text/markdown",
		},
	}
}

// readResource resolves a aegis:// URI and returns the governed semantic
// schema for the referenced data source, as both markdown and JSON.
func (s *Server) readResource(r *http.Request, params json.RawMessage) (interface{}, error) {
	claims, err := s.principal(r)
	if err != nil {
		return nil, err
	}
	var p struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params")
	}
	dsName, kind, err := parseURI(p.URI)
	if err != nil {
		return nil, err
	}
	if kind != "schema" {
		return nil, fmt.Errorf("unsupported resource kind %q", kind)
	}
	dsID, err := resolveDatasource(s.store, dsName)
	if err != nil {
		return nil, err
	}
	schema, err := s.proxy.Catalog(r.Context(), dsID, claims)
	if err != nil {
		return nil, err
	}
	jsonText, _ := json.MarshalIndent(schema, "", "  ")
	return map[string]interface{}{
		"contents": []map[string]interface{}{
			{"uri": p.URI, "mimeType": "text/markdown", "text": schema.CatalogMarkdown()},
			{"uri": p.URI + "?format=json", "mimeType": "application/json", "text": string(jsonText)},
		},
	}, nil
}

// parseURI splits aegis://<datasource>/<kind>.
func parseURI(uri string) (datasource, kind string, err error) {
	if !strings.HasPrefix(uri, uriPrefix) {
		return "", "", fmt.Errorf("unknown resource uri %q", uri)
	}
	rest := strings.TrimPrefix(uri, uriPrefix)
	// tolerate a trailing ?format=json etc.
	if i := strings.IndexByte(rest, '?'); i >= 0 {
		rest = rest[:i]
	}
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("malformed resource uri %q", uri)
	}
	return parts[0], parts[1], nil
}
