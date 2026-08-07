#!/usr/bin/env python3
"""
Minimal Aegis MCP client (Streamable HTTP transport).

Shows the three calls an Agent needs against Aegis:
  1. initialize               -> opens a session, server returns `Mcp-Session-Id`
  2. notifications/initialized -> handshake ack (server replies 202, no body)
  3. tools/list               -> discover governed tools
  4. tools/call               -> run a governed query / pre-execution estimate

Auth (pick one):
  * `X-MCP-API-Key`            -> maps to the seeded `mcp-agent` (analyst) account.
                                  No login needed. Matches config.json `mcp.api_key`.
  * `Authorization: Bearer JWT`-> from `POST /api/v1/login` (e.g. analyst/analyst123).

Run:
    pip install httpx
    python3 client.py
"""
import json

import httpx

BASE = "http://localhost:8080/mcp"
API_KEY = "mcp-demo-key"          # == config.json mcp.api_key / AEGIS_MCP_API_KEY
# JWT = "eyJ..."                  # alternative: pass Authorization: Bearer <JWT>
DATASOURCE = "demo"               # seeded demo datasource

HEADERS = {
    "Content-Type": "application/json",
    "Accept": "application/json",   # ask for plain JSON (not text/event-stream)
    "X-MCP-API-Key": API_KEY,
}


def rpc(session: httpx.Client, method: str, *, params=None, msg_id=1, notification=False):
    payload = {"jsonrpc": "2.0", "method": method}
    if not notification:
        payload["id"] = msg_id
    if params is not None:
        payload["params"] = params

    r = session.post(BASE, headers=HEADERS, json=payload)
    if notification:
        # servers reply 202 Accepted with no body to notifications
        return None

    r.raise_for_status()
    sid = r.headers.get("Mcp-Session-Id")
    if sid:
        HEADERS["Mcp-Session-Id"] = sid   # echo back on subsequent calls (spec-compliant)
    return r.json()


def main():
    with httpx.Client() as s:
        # 1. initialize
        init = rpc(s, "initialize", params={
            "protocolVersion": "2024-11-05",
            "capabilities": {},
            "clientInfo": {"name": "aegis-example", "version": "0.1"},
        })
        print("initialize ->", init["result"]["serverInfo"])

        # 2. handshake ack (notification, ignored)
        rpc(s, "notifications/initialized", notification=True)

        # 3. list tools
        tools = rpc(s, "tools/list", msg_id=2)
        print("tools:", [t["name"] for t in tools["result"]["tools"]])

        # 4a. governed query (row/column policies + masking applied server-side)
        q = rpc(s, "tools/call", params={
            "name": "query",
            "arguments": {"datasource": DATASOURCE, "sql": "SELECT guest_name, member_tier, phone FROM guest_profiles"},
        }, msg_id=3)
        print("\n[query] result:")
        print(json.dumps(q["result"], ensure_ascii=False, indent=2))

        # 4b. pre-execution cost / risk estimate (the wedge's risk-visibility)
        est = rpc(s, "tools/call", params={
            "name": "estimate_query",
            "arguments": {"datasource": DATASOURCE, "sql": "SELECT hotel_name, room_revenue FROM hotel_bookings"},
        }, msg_id=4)
        print("\n[estimate_query] result:")
        print(json.dumps(est["result"], ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
