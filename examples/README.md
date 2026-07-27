# Examples — plug Aegis into your Agent in 5 minutes

Aegis exposes two ways for an AI Agent to reach governed data:

| Surface | Best for | Auth |
|---------|----------|------|
| **MCP** (`POST /mcp`, Streamable HTTP) | Native MCP clients (Claude Desktop, custom agents) | `X-MCP-API-Key` or `Bearer <JWT>` |
| **DataAPI** (REST) | Traditional apps, custom agents, scripting | `Bearer <JWT>` |

The wedge: **your Agent never connects to the database.** Every query, every
NL2SQL output, every estimate goes through Aegis, which applies table/row/column
governance + masking and writes an audit trail — before any bytes reach the DB.

---

## 1. MCP (drop-in for Claude Desktop & any MCP client)

Copy [`mcp/claude_desktop_config.json`](mcp/claude_desktop_config.json) into your
Claude Desktop / MCP client config:

```json
{
  "mcpServers": {
    "aegis": {
      "type": "http",
      "url": "http://localhost:8080/mcp",
      "headers": { "X-MCP-API-Key": "mcp-demo-key" }
    }
  }
}
```

That's it — Claude (or any MCP client) can now call `query`, `estimate_query`,
`nl2sql`, `list_metrics` / `run_metric`, `list_tables`, `get_catalog`, …
all governed by Aegis. No database credentials ever leave the gateway.

Minimal Python client: [`mcp/client.py`](mcp/client.py)
(`pip install httpx && python3 client.py`).

---

## 2. DataAPI (REST)

A copy-paste cheat sheet: [`dataapi/curl.sh`](dataapi/curl.sh).

A tiny "Text-to-Query" Agent that always estimates cost/risk **before** running,
and falls back to a local stub if no LLM key is configured:
[`dataapi/agent.py`](dataapi/agent.py)

```bash
pip install httpx
python3 dataapi/agent.py "How many customers do we have?"
```

---

## Prerequisites

Start Aegis with the seeded demo tenant (auto-created on first boot):

```bash
docker compose up -d        # or: go run ./cmd/aegis -config config.json
```

Seeded demo accounts (change before any real deployment!):

| user       | password     | role     |
|------------|--------------|----------|
| `admin`    | `admin123`   | admin (superuser) |
| `analyst`  | `analyst123` | analyst (governed) |
| `mcp-agent`| `mcp123`     | analyst (used by `X-MCP-API-Key`) |

The MCP API key `mcp-demo-key` is set in `config.json` (`mcp.api_key`) and the
compose file (`AEGIS_MCP_API_KEY`).
