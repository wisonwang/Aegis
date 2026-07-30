# Aegis MCP — Tool Catalog & Raw HTTP Reference

This reference documents the Aegis MCP server endpoint `http://localhost:8080/mcp`
(JSON-RPC 2.0 over Streamable HTTP; also speaks SSE via `Accept: text/event-stream`).
Use it when the WorkBuddy `aegis` connector is unavailable and you must call the
endpoint directly (e.g. via `curl`).

## Authentication
Send ONE of:
- `X-MCP-API-Key: mcp-demo-key`  (static key → `mcp-agent` / analyst role)
- `Authorization: Bearer <JWT>`  (admin / any role; 24h expiry, get via `POST /api/v1/login`)

Workspace scoping (optional): `X-Workspace-Id: <id>` — admin selects a concrete
workspace; members are limited to their own; absent → cross-workspace view for admin.

## Transport pattern
Every request is `POST /mcp` with `Content-Type: application/json`:
```json
{"jsonrpc":"2.0","id":1,"method":"<method>","params":{...}}
```
First call `initialize`, then `tools/list` / `tools/call`. `notifications/initialized`
is accepted with no reply.

---

## Tools

### list_datasources
List registered data sources. No arguments.
Returns: array of data source objects.

### list_tables
List accessible tables on a source (table permissions applied).
- `datasource` (string, required): data source id or name.
Returns: `{ "tables": [...] }`.

### describe_table
Describe a table's columns (denied columns removed per caller's column perms).
- `datasource` (string, required)
- `table` (string, required)
Returns: `{ "table": "...", "columns": [...] }`.

### get_catalog
Semantically enriched, governed schema: accessible tables + columns with business
descriptions, synonyms, example values. Columns you may not access are omitted.
**Call this before writing SQL / NL2SQL.**
- `datasource` (string, required)
Returns: catalog object (tables → columns with semantics).

### list_datasets
List curated datasets the caller may consume (published, access-granted data products).
No arguments.
Returns: `{ "datasets": [...] }`.

### get_dataset_catalog
Governed contract of a dataset: stable fields with descriptions, synonyms, examples,
and value masking. Call before querying a dataset.
- `name` (string, required): dataset name.
Returns: dataset catalog object.

### query
Run a governed SQL query. Table/row/column perms enforced; rewritten SQL returned.
- `datasource` (string, required)
- `sql` (string, required)
- `params` (array, optional): query parameters
- `session_id` (string, optional): links queries from one AI conversation; echoed back
Returns: `{ "session_id": "...", "queryResult": {...} }` (result includes `rewritten_sql`).

### nl2sql
Natural-language question → governed SQL → result. Read-only SQL only.
- `datasource` (string, required)
- `question` (string, required)
- `sql_hint` (string, optional): hand-written SQL to prefer over free generation
- `session_id` (string, optional)
Returns: `{ "session_id", "generated_sql", "explanation", "queryResult" }`.

### estimate_query
Preview cost/risk BEFORE running. Returns EXPLAIN-based estimated row scan, tables /
sensitive columns touched, max data sensitivity, and a low/medium/high risk level with
warnings. Governed (row/column policy reflected) and read-only.
- `datasource` (string, required)
- `sql` (string, required)
- `session_id` (string, optional)
Returns: estimate object (risk level, estimated rows, sensitive columns, warnings).

### list_metrics
List curated governed metrics on a source (each with params, unit, business description).
- `datasource` (string, required)
Returns: `{ "metrics": [...] }`.

### run_metric
Run a curated metric with parameters. Metric SQL template rendered with SQL-safe literals
from `params`; full governance path applies (so lineage/PII info is returned).
- `datasource` (string, required)
- `metric` (string, required)
- `params` (object, optional): name → value, matching the metric's declared params
- `session_id` (string, optional)
Returns: `{ "session_id", "sql", "lineage", "queryResult" }`.

---

## Resources
- `aegis://<datasource>/schema` — permission-filtered semantic schema card for a source.
  Read via `resources/read` with `{"uri":"aegis://<name>/schema"}`.

## Prompts
- `nl2sql` — "how to query safely" template. Get via `prompts/get` with
  `{"name":"nl2sql","arguments":{"datasource":"<name>","question":"<q>"}}`.

---

## Raw HTTP examples (curl)

Initialize:
```bash
curl -s -X POST localhost:8080/mcp \
  -H 'Content-Type: application/json' \
  -H 'X-MCP-API-Key: mcp-demo-key' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"wb","version":"1"}}}'
```

List tools:
```bash
curl -s -X POST localhost:8080/mcp -H 'Content-Type: application/json' \
  -H 'X-MCP-API-Key: mcp-demo-key' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
```

Call `query`:
```bash
curl -s -X POST localhost:8080/mcp -H 'Content-Type: application/json' \
  -H 'X-MCP-API-Key: mcp-demo-key' \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"query","arguments":{"datasource":"mysql-local","sql":"SELECT 1"}}}'
```

Call `nl2sql`:
```bash
curl -s -X POST localhost:8080/mcp -H 'Content-Type: application/json' \
  -H 'X-MCP-API-Key: mcp-demo-key' \
  -d '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"nl2sql","arguments":{"datasource":"mysql-local","question":"上个月 GMV 是多少"}}}'
```

Call `estimate_query` (de-risk before running):
```bash
curl -s -X POST localhost:8080/mcp -H 'Content-Type: application/json' \
  -H 'X-MCP-API-Key: mcp-demo-key' \
  -d '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"estimate_query","arguments":{"datasource":"mysql-local","sql":"SELECT * FROM orders"}}}'
```

> All `tools/call` responses wrap the payload in `{"content":[{"type":"text","text":"<json>"}]}`.
> Parse the `text` field as JSON to obtain the structured result.
