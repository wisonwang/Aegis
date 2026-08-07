#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Full MCP surface (JSON-RPC over HTTP) — the AI-Agent consumption path.

Covers the protocol handshake, all 11 tools, resources and prompts, and the
governance behaviour that is *shared* with the DataAPI (so MCP cannot bypass it).
Both the governed ``analyst`` identity (static API key) and the ``admin`` Bearer
identity are exercised.
"""
EXPECTED_TOOLS = {
    "list_datasources", "list_tables", "describe_table", "get_catalog",
    "list_datasets", "get_dataset_catalog", "query", "nl2sql",
    "estimate_query", "list_metrics", "run_metric",
}


def _rows(payload):
    """Extract result rows from an MCP tool payload, tolerating both the
    wrapped ``{"queryResult": {"rows": [...]}}`` shape and a bare list."""
    if isinstance(payload, list):
        return payload
    if isinstance(payload, dict):
        qr = payload.get("queryResult", payload)
        if isinstance(qr, dict):
            return qr.get("rows", [])
    return []


def test_mcp_protocol_and_tool_inventory(aegis, mcp_analyst):
    tools = mcp_analyst.call("tools/list")["tools"]
    names = {t["name"] for t in tools}
    assert EXPECTED_TOOLS <= names


def test_mcp_resources_and_prompts(aegis, mcp_analyst):
    res = mcp_analyst.call("resources/list")["resources"]
    uris = {r["uri"] for r in res}
    assert "aegis://demo/schema" in uris
    prompts = mcp_analyst.call("prompts/list")["prompts"]
    assert any(p["name"] == "nl2sql" for p in prompts)


def test_mcp_analyst_query_masked(aegis, mcp_analyst):
    # list_datasources returns a bare list; list_tables wraps under "tables"
    out = mcp_analyst.tool("list_datasources")
    sources = out if isinstance(out, list) else out.get("datasources", [])
    assert any(d["name"] == "demo" for d in sources)

    tables = mcp_analyst.tool("list_tables", {"datasource": "demo"})
    tnames = {t["name"] if isinstance(t, dict) else t for t in tables.get("tables", [])}
    assert {"hotel_bookings", "guest_profiles"} <= tnames

    q = mcp_analyst.tool("query",
                         {"datasource": "demo",
                          "sql": "SELECT guest_name, phone, email FROM guest_profiles ORDER BY id"})
    rows = _rows(q)
    assert len(rows) == 2
    assert rows[0]["guest_name"] != "Alice Zhang"
    assert "*" in rows[0]["phone"]


def test_mcp_analyst_nl2sql(aegis, mcp_analyst, seeded_metrics):
    out = mcp_analyst.tool("nl2sql",
                          {"datasource": "demo",
                           "question": "已确认订单的房费收入是多少？"})
    rows = _rows(out)
    assert rows and abs(float(rows[0]["confirmed_room_revenue"]) - 4160.0) < 0.0001


def test_mcp_analyst_metrics(aegis, mcp_analyst, seeded_metrics):
    m = mcp_analyst.tool("list_metrics", {"datasource": "demo"})
    assert any(x["name"] == "arrival_guest_count" for x in m.get("metrics", []))
    r = mcp_analyst.tool("run_metric",
                         {"datasource": "demo", "metric": "arrival_guest_count", "params": {}})
    rows = _rows(r)
    assert rows and rows[0]["arrival_guests"] == 3


def test_mcp_admin_raw(aegis, mcp_admin):
    q = mcp_admin.tool("query",
                       {"datasource": "demo",
                        "sql": "SELECT guest_name, phone FROM guest_profiles ORDER BY id"})
    rows = _rows(q)
    assert len(rows) == 4
    assert rows[0]["phone"] == "13812345678"


def test_mcp_admin_nl2sql(aegis, mcp_admin, seeded_metrics):
    out = mcp_admin.tool("nl2sql",
                         {"datasource": "demo",
                          "question": "已确认订单的房费收入是多少？"})
    rows = _rows(out)
    assert rows and abs(float(rows[0]["confirmed_room_revenue"]) - 6040.0) < 0.0001
