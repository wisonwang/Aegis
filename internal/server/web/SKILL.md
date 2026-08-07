---
name: aegis-mcp-integration
description: >-
  Connect an AI agent to Aegis (self-hosted, governance-first AI Data Supply
  Gateway) over MCP to run governed SQL / NL2SQL queries, list data sources,
  tables, datasets and business metrics. Use when the agent needs tenant-isolated,
  column-masked, non-bypassable access to enterprise data through Aegis's /mcp
  endpoint, or when the user asks to "query the data via Aegis / 用 Aegis 查数据".
---

# Aegis MCP Integration Skill

Aegis is a self-hosted, **governance-first AI Data Supply Gateway**. It exposes an
MCP (Model Context Protocol) server so any MCP-capable agent (WorkBuddy, Claude
Desktop, etc.) can query enterprise data through a single, **non-bypassable**
governance layer. This skill tells the agent how to discover data and run governed
queries against Aegis.

## Endpoint

- Base URL: `http://<aegis-host>:<port>` (default port `8080`)
- MCP path: `POST /mcp` (configurable via `mcp.path`; JSON-RPC 2.0 over HTTP, SSE supported)
- Live API docs (Swagger UI): `/admin/api/docs/`
- Downloadable copy of this skill: `/admin/api/skill`

## Authentication

MCP accepts **one** of two auth modes (the operator provisions the credential):

1. **Service account (recommended for agents)** — static API key header:
   `X-MCP-API-Key: <your-mcp-api-key>`
   Provisioned as the `mcp-agent` service account; assign the `analyst` role for
   least-privilege, governed access.
2. **User JWT** — `Authorization: Bearer <jwt>` obtained from `POST /api/v1/login`.

## MCP handshake order (mandatory)

1. `initialize` → capture `Mcp-Session-Id` from the response header.
2. send `notifications/initialized` (notification; expect `202`).
3. only now call `tools/list`, `tools/call`, `resources/read`, `prompts/get`.

## Tools (11)

| Tool | Arguments | Purpose |
|------|-----------|---------|
| `list_datasources` | — | List registered data sources |
| `list_tables` | `datasource` | List tables of a data source |
| `describe_table` | `datasource`, `table` | Columns + types of a table |
| `get_catalog` | `datasource` | Full schema catalog of a data source |
| `list_datasets` | — | List published datasets |
| `get_dataset_catalog` | `dataset` | Schema of a dataset |
| `query` | `sql` | Run a governed SQL query → returns `rewritten_sql` + masked `query_result.rows` |
| `nl2sql` | `question`, `datasource?` | Natural-language → SQL via the configured LLM |
| `estimate_query` | `sql` | Row-count / cost estimate without executing |
| `list_metrics` | `datasource` | List seeded business metrics |
| `run_metric` | `name`, `params?`, `session_id?` | Execute a metric (tenant-isolated) |

## Resources & Prompts

- **Resources**: `aegis://<name>/schema` — read a catalog / schema as a resource.
- **Prompts**: `nl2sql` — scaffold an NL→SQL prompt for the user's question.

## Governance (always on, cannot be bypassed)

Every query passes through one rewrite engine before hitting the database:

- **Default-deny** — no table access without an explicit grant.
- **Row policy** — `:attr` predicates from the caller's JWT are AND-merged and
  injected as a derived table (tenant isolation).
- **Column masks** — `phone / email / card / partial / hash / redact / tokenize / fpe`.
- **SQL LIMIT injection** + DDL (`CREATE / DROP / ALTER / TRUNCATE`) blocked.
- The `admin` role bypasses governance; every other role is constrained.

The `query` tool's `rewritten_sql` field shows the *actual* SQL executed (with the
tenant predicate and LIMIT injected) — surface it when debugging.

## Minimal MCP client config

Claude Desktop / WorkBuddy style `mcpServers` entry:

```json
{
  "mcpServers": {
    "aegis": {
      "url": "http://localhost:8080/mcp",
      "headers": { "X-MCP-API-Key": "<your-mcp-api-key>" }
    }
  }
}
```

## Quickstart (thinking in tool calls)

1. `initialize` → open a session, capture `Mcp-Session-Id`.
2. `tools/call` → `list_datasources` to discover what's available.
3. `tools/call` → `describe_table` to learn column names/types.
4. `tools/call` → `query` with `{"sql": "SELECT * FROM hotel_bookings LIMIT 10"}`
   → returns `rewritten_sql` (tenant predicate injected) and masked `rows`.
5. When the user asks in natural language, use `nl2sql` first, then `query` the result.

> The host, port and API key are instance-specific. Ask the Aegis operator for the
> `/mcp` URL and an `mcp-agent` API key. Never embed a real key in shared prompts.
