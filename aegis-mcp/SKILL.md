---
name: aegis-mcp
description: This skill should be used when a user asks data-related questions that require querying databases, running SQL, exploring schemas, discovering datasets, or computing business metrics through the Aegis governed data gateway. It covers invoking the Aegis MCP server (registered in TRAE as the `aegis` MCP server) — its tools (query, nl2sql, estimate_query, list_datasources, list_tables, describe_table, get_catalog, list_datasets, get_dataset_catalog, list_metrics, run_metric), resources, and prompts — so TRAE can answer data questions safely under table/row/column governance and value masking.
agent_created: true
---

# Aegis MCP — Governed Data Gateway Invocation

## Overview
Aegis is a self-hosted, governance-by-default AI data gateway. Through its MCP server (registered in TRAE as the `aegis` MCP server), TRAE gains tool-based access to query governed data sources, translate natural language to SQL, estimate query risk, and run curated metrics/datasets. Every call is rewritten and masked by the governance engine (default-deny), so the agent never bypasses table/row/column policies.

## When to use this skill
- The user asks a question answerable from a database (e.g. "上个月 GMV 是多少", "查一下订单表结构").
- The user wants SQL executed, NL→SQL, or a schema / data product discovered.
- The user references "Aegis", "数据网关", "受治理查询", or a specific datasource / dataset / metric name.
- Do NOT use for: writing to databases through uncontrolled paths, or anything outside the connected data sources.

## Prerequisites
- The `aegis` MCP server must be configured in TRAE (Settings → MCP → Create → Manual configuration) and point to `http://localhost:8080/mcp`.
- For project-local usage, import this skill into the current project's `.trae/skills/aegis-mcp/` directory, or install it globally under `~/.trae-cn/skills/aegis-mcp/`.
- The Aegis server must be running (default `:8080`). If the MCP tools are unavailable, verify the server is up: `curl -s -o /dev/null -w '%{http_code}' localhost:8080/metrics` (expect 200).
- Auth is handled by the MCP server configuration. The server accepts `Authorization: Bearer <JWT>` (admin / any role; get via `POST /api/v1/login`) and a static `X-MCP-API-Key` header — but the static key **only works when the server's `mcp.api_key` is set**; in the local `conf/config.local.json` it is empty, so configure a JWT. No per-call auth is needed once connected.

## Core capabilities (MCP tools)
All tools accept a `datasource` argument as the data source id or name. Full input schemas and raw HTTP examples are in `references/mcp-tools.md`.

**Discovery**
- `list_datasources` — list registered data sources.
- `list_tables` — list accessible tables on a source (table permissions applied).
- `describe_table` — columns of a table (denied columns removed).
- `get_catalog` — semantically enriched schema (business descriptions, synonyms, examples); call BEFORE writing SQL / NL2SQL.
- `list_datasets` / `get_dataset_catalog` — discover curated data products.

**Execution**
- `query` — run governed SQL (returns rewritten SQL + result).
- `nl2sql` — natural-language question → governed SQL + result.
- `estimate_query` — preview cost/risk (estimated rows, sensitive columns, risk level) BEFORE running.
- `list_metrics` / `run_metric` — curated governed metrics (prefer over hand-written SQL for KPIs).

**Resources / Prompts**
- Resource `aegis://<datasource>/schema` — permission-filtered semantic schema card for a source.
- Resource `aegis://dataset/<name>/schema` — governed contract for a curated dataset (e.g. `aegis://dataset/paid_orders/schema`).
- Prompt `nl2sql` — "how to query safely" template. Accepts `datasource` (required), `question` (required), and optional `dialect` (mysql|postgres|sqlite).

## Standard workflow
1. **Discover** what is available: `list_datasources`, then `list_tables` / `get_catalog` for the target source. Use `get_catalog` to learn column meaning before forming SQL.
2. **For a natural-language question**: call `nl2sql` (optionally after `get_catalog`). It returns `generated_sql`, `explanation`, and `queryResult`.
3. **For a known SQL**: call `query`. It returns the rewritten (governed) SQL and the result — surface the rewritten SQL for transparency.
4. **De-risk large / PII queries**: before `query`, call `estimate_query` to see estimated row scan, touched sensitive columns, and risk level (low/medium/high). If high, tighten the `WHERE` filter or narrow scope.
5. **For a KPI**: `list_metrics` then `run_metric` with params. Prefer metrics over ad-hoc SQL when one fits.
6. **For curated data products**: `list_datasets` then `get_dataset_catalog`, then query through the dataset.

## Governance notes
- All execution paths enforce table/row/column permissions and value masking; the caller (analyst) sees only what is granted. `admin` sees more.
- `query` / `nl2sql` run **read-only** SQL only. Write operations go through the DataAPI, not MCP.
- `estimate_query` and `query` are themselves governed (row/column policy reflected in output).
- Large result sets are truncated by `MaxRows`; the `rewritten_sql` field is the actually executed statement.

## References
- `references/mcp-tools.md` — full tool catalog with exact input schemas, resource/prompt definitions, and raw HTTP JSON-RPC examples (useful when the TRAE MCP server entry is unavailable).
