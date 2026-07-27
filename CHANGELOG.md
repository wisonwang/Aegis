# Changelog

Aegis is a self-hosted, governance-by-default **AI Data Supply Gateway**. The
single binary turns your internal databases into *controlled Agent tools*: the
LLM/Agent never sees a database credential, and every query is forced through
table / row / column governance, masking, behavior limits, and an audit trail.

This changelog is organized as a **capability timeline** following the "wedge"
strategy — start as a thin, unavoidable governance layer, then expand AI-native
capabilities on top of the same enforcement core. Each milestone is shipped on
`wisonwang/Aegis` `main`.

---

## Milestone: AI-native governance (current)

> Commit `0571258` — *Query Lineage & Cost* completes the "risk visibility"
> narrative of the wedge.

The Agent now gets, **before executing anything**, a cost/risk preview of the
SQL it is about to run:

- `POST /api/v1/datasources/{id}/query/estimate` (DataAPI) and the
  `estimate_query` MCP tool.
- Returns the **governed** SQL (after row/column policy rewrite), estimated scan
  rows, involved tables, max data sensitivity, PII flag, and a synthesized
  `risk_level` (low / medium / high / unknown) with human-readable warnings.
- Reuses the exact same `permission.Rewrite` path as execution — so the estimate
  can never show a less-restricted view than what will actually run. A governance
  denial *is* a useful estimate.
- Dialect-aware: MySQL / PostgreSQL use `EXPLAIN`; SQLite falls back to a
  read-only `COUNT(*)` so it works out-of-the-box on the dev backend.

**Full AI-native surface (all governed, all audited):**

| Capability | DataAPI | MCP |
|------------|---------|-----|
| Ad-hoc governed query | `POST /query` | `query` |
| NL2SQL secure gateway | `POST /nl2sql` | `nl2sql` |
| Curated metrics (governed templates + lineage) | `POST /metrics/{name}/run` | `run_metric` / `list_metrics` |
| Pre-execution cost/risk estimate | `POST /query/estimate` | `estimate_query` |
| Governed semantic schema | `GET /catalog` | `get_catalog` (resource) |
| Data products (datasets) | `POST /datasets/{id}/query` | `list_datasets` |

---

## Earlier milestones

### Data classification + approval workflow
> `de3e097` (approval workflow), `bdc0820` (auto-recommend masks), `0e2f33e`
> (RLS double-layer for nested subqueries)

- **Data classification & auto-recommend masks**: scan columns, recommend a
  default masking strategy per sensitivity, one-click apply per role.
- **Access approval workflow**: users request table access → admin approves →
  permission takes effect → revocable. Closes the "who can see what" loop.
- **RLS double-layer**: row policies are recursively injected into nested
  subqueries, hardening the boundary.

### NL2SQL secure gateway
> `3193f38`

Natural-language questions become SQL, but the generated SQL is fed back through
the **same** `Proxy.Execute` path as hand-written SQL — NL2SQL widens *who can
ask*, never *what they can see*. Read-only enforcement, schema-restricted
columns, and JSON hardening included.

### Curated metrics (semantic metric layer)
> `1e0923c`

Administrators predefine governed SQL templates (with typed, whitelisted
parameters). Agents consume metrics by name instead of inventing SQL at runtime —
kills metric drift. Runtime **lineage** (tables + sensitivity + PII) is returned
alongside the masked result.

### Enterprise identity + observability
> SSO/OIDC, LDAP/AD, structured logging (`slog`), PostgreSQL end-to-end,
> FPE / tokenize masking strategies

- **OIDC** (Auth Code + PKCE + nonce) and **LDAP/AD** login with group→role
  mapping and auto-provisioning.
- **Structured logging** (`log/slog`, JSON/text) with request-ID correlation and
  governance-decision events.
- **PostgreSQL** verified end-to-end; **FPE** (format-preserving, keyed) and
  **tokenize** (deterministic HMAC pseudonym) masking for reversible / joinable
  de-identification.

### Core data-proxy governance (the wedge)
> Initial commit + audit (`audit_logs`), MCP + DataAPI, three-level governance

- Centralized auth; apps/agents connect to Aegis, **not** the database.
- Three-level governance: **table** (role × datasource × table), **row**
  (predicate policies with `:attr` injection from JWT), **column** (hide or
  dynamic mask: phone / email / card / partial / hash / redact / tokenize / fpe).
- Default-deny; full audit trail (`ok` / `denied` / `error`) tagged by channel
  (dataapi / mcp) and session.
- Behavior limits: max rows, query timeout, per-principal rate limit; write
  protection (no-WHERE writes blocked, affected-row cap).

---

## Upgrade / deployment notes

- **Single binary.** `go build ./cmd/aegis` or `docker compose up -d`.
- **Config**: `config.json` (flag `-config`) or environment
  (`AEGIS_JWT_SECRET`, `AEGIS_MCP_API_KEY`, `AEGIS_MASK_SECRET`, …).
- **Change the demo credentials before any real deployment** — `admin/admin123`,
  `analyst/analyst123`, `mcp-agent/mcp123` and `mcp.api_key = mcp-demo-key` are
  seed defaults. See `SECURITY.md`.
- **Control plane**: SQLite by default (`aegis.db`); data sources are separate
  connections you register.
