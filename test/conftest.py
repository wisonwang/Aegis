#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Shared pytest fixtures for the Aegis end-to-end test suite.

Strategy
--------
Each test session spins up ONE fresh, fully-isolated Aegis instance:

  * a temp directory holds the control-plane SQLite DB + the seeded demo
    datasource (``seed_demo: true``), so nothing touches the developer's
    running instance;
  * a tiny in-process FakeLLM serves the ``/chat/completions`` endpoint so the
    ``nl2sql`` path is exercised end-to-end without a real LLM;
  * ``go run ./cmd/aegis`` boots the binary, mirroring the proven
    ``scripts/mcp_e2e_scenario.py`` pattern.

The seeded "hotel operations" scenario is the realistic test data: it contains
a multi-tenant ``hotel_bookings`` / ``guest_profiles`` schema, an ``analyst``
role scoped to the ``acme`` tenant via row policy + column masks, an ``admin``
role that bypasses governance, and a published ``hotel_confirmed_bookings``
dataset.
"""
import json
import os
import socket
import subprocess
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib import error, request

import pytest

ROOT = Path(__file__).resolve().parent.parent
GO = os.environ.get("GO_BIN", "/usr/local/go/bin/go")


# --------------------------------------------------------------------------- #
# Helpers
# --------------------------------------------------------------------------- #
def free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def http_json(method, url, body=None, headers=None, timeout=30):
    """Minimal JSON HTTP client (stdlib only). Returns (status, parsed_body)."""
    h = {"Accept": "application/json"}
    if headers:
        h.update(headers)
    raw = json.dumps(body).encode("utf-8") if body is not None else None
    if raw is not None:
        h.setdefault("Content-Type", "application/json")
    req = request.Request(url, data=raw, headers=h, method=method)
    try:
        with request.urlopen(req, timeout=timeout) as r:
            text = r.read().decode("utf-8")
            return r.status, (json.loads(text) if text else None)
    except error.HTTPError as exc:
        text = exc.read().decode("utf-8", "ignore")
        try:
            parsed = json.loads(text)
        except json.JSONDecodeError:
            parsed = text
        return exc.code, parsed


def list_of(resp, *keys):
    """Robustly pull a list out of a response that may be a list or a dict
    wrapping a list under one of ``keys`` (or any list value)."""
    if isinstance(resp, list):
        return resp
    if isinstance(resp, dict):
        for k in keys:
            if isinstance(resp.get(k), list):
                return resp[k]
        for v in resp.values():
            if isinstance(v, list):
                return v
    return []


def rows_of(resp):
    """Extract result rows from either a flattened QueryResult (DataAPI) or a
    nested ``query_result`` envelope (MCP / run_metric)."""
    if isinstance(resp, dict):
        if isinstance(resp.get("rows"), list):
            return resp["rows"]
        qr = resp.get("query_result")
        if isinstance(qr, dict) and isinstance(qr.get("rows"), list):
            return qr["rows"]
    return []


def login(base, user, pwd):
    st, b = http_json("POST", base + "/api/v1/login",
                      {"username": user, "password": pwd})
    assert st == 200 and b.get("token"), f"login {user} failed: {b}"
    return b["token"]


# --------------------------------------------------------------------------- #
# FakeLLM — serves deterministic SQL for the nl2sql path
# --------------------------------------------------------------------------- #
class FakeLLMHandler(BaseHTTPRequestHandler):
    def do_POST(self):
        if self.path != "/chat/completions":
            self.send_error(404)
            return
        n = int(self.headers.get("Content-Length", "0"))
        payload = json.loads(self.rfile.read(n) or b"{}")
        msgs = payload.get("messages", [])
        q = str(msgs[-1].get("content", "")).lower() if msgs else ""
        if "客人" in q or "guest" in q or "会员" in q:
            sql = ("SELECT sum(guest_count) AS arrival_guests FROM hotel_bookings "
                   "WHERE booking_status IN ('confirmed', 'checked_in')")
        else:
            sql = ("SELECT sum(room_revenue) AS confirmed_room_revenue FROM hotel_bookings "
                   "WHERE booking_status IN ('confirmed', 'checked_in')")
        content = json.dumps({"sql": sql, "explanation": "auto"}, ensure_ascii=False)
        raw = json.dumps({"choices": [{"message": {"content": content}}]}).encode("utf-8")
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def log_message(self, *args):
        return


def start_fake_llm():
    port = free_port()
    server = ThreadingHTTPServer(("127.0.0.1", port), FakeLLMHandler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    return server, f"http://127.0.0.1:{port}"


# --------------------------------------------------------------------------- #
# MCP client (JSON-RPC over HTTP)
# --------------------------------------------------------------------------- #
class MCPClient:
    def __init__(self, base, headers):
        self.url = base.rstrip("/") + "/mcp"
        self.h = {"Accept": "application/json", "Content-Type": "application/json"}
        self.h.update(headers)
        self.n = 1

    def call(self, method, params=None, notification=False):
        payload = {"jsonrpc": "2.0", "method": method}
        if not notification:
            payload["id"] = self.n
            self.n += 1
        if params is not None:
            payload["params"] = params
        st, body = http_json("POST", self.url, payload, self.h)
        if notification:
            assert st in (200, 202), f"notif {method} -> {st}"
            return None
        assert st == 200, f"mcp {method} -> {st}: {body}"
        assert body.get("error") is None, f"mcp {method} error: {body}"
        return body["result"]

    def tool(self, name, args=None):
        res = self.call("tools/call", {"name": name, "arguments": args or {}})
        return json.loads(res["content"][0]["text"])


# --------------------------------------------------------------------------- #
# Session-scoped fixtures
# --------------------------------------------------------------------------- #
@pytest.fixture(scope="session")
def fake_llm():
    srv, url = start_fake_llm()
    yield url
    srv.shutdown()


@pytest.fixture(scope="session")
def aegis(fake_llm):
    tmp = Path(tempfile.mkdtemp(prefix="aegis-pytest-"))
    port = free_port()
    cfg = {
        "environment": "test",
        "listen_addr": f":{port}",
        "jwt_secret": "pytest-secret",
        "mask_secret": "pytest-mask-secret",
        "jwt_expiry": "24h",
        "db_type": "sqlite",
        "db_path": str(tmp / "control.db"),
        "data_dir": str(tmp),
        "edition": "enterprise",
        "seed_demo": True,
        "mcp": {
            "enabled": True,
            "path": "/mcp",
            "api_key": "mcp-demo-key",
            "require_auth": True,
        },
        "limits": {
            "max_rows": 10000,
            "max_affected_rows": 10000,
            "max_bytes": 4194304,
            "query_timeout": "30s",
            "rate_per_min": 60,
            "admin_exempt": False,
        },
        "nl2sql": {
            "enabled": True,
            "provider": "openai",
            # NOTE: llm.go appends "/chat/completions" to BaseURL itself
            # (see internal/nl2sql/llm.go), so we point at the root only.
            "base_url": fake_llm,
            "api_key": "fake",
            "model": "fake",
            "timeout_sec": 10,
            "max_retries": 1,
        },
        "logging": {"format": "text", "level": "info"},
    }
    (tmp / "config.json").write_text(json.dumps(cfg, ensure_ascii=False, indent=2),
                                     encoding="utf-8")
    log = (tmp / "aegis.log").open("w", encoding="utf-8")
    env = dict(os.environ,
               GOPROXY="https://goproxy.cn",
               GOSUMDB="sum.golang.google.cn",
               GOFLAGS="-mod=mod")
    proc = subprocess.Popen(
        [GO, "run", "./cmd/aegis", "-config", str(tmp / "config.json")],
        cwd=str(ROOT), stdout=log, stderr=subprocess.STDOUT, env=env,
    )
    base = f"http://127.0.0.1:{port}"

    deadline = time.time() + 240
    ready = False
    while time.time() < deadline:
        if proc.poll() is not None:
            raise RuntimeError("Aegis exited early:\n" + (tmp / "aegis.log").read_text(encoding="utf-8", errors="ignore"))
        try:
            st, b = http_json("GET", base + "/api/v1/ready")
            if st == 200 and b and b.get("status") == "ready":
                ready = True
                break
        except Exception:
            pass
        time.sleep(0.7)
    if not ready:
        raise RuntimeError("Aegis did not become ready:\n" + (tmp / "aegis.log").read_text(encoding="utf-8", errors="ignore"))

    yield base

    proc.terminate()
    try:
        proc.wait(timeout=10)
    except subprocess.TimeoutExpired:
        proc.kill()


@pytest.fixture(scope="session")
def admin_token(aegis):
    return login(aegis, "admin", "admin123")


@pytest.fixture(scope="session")
def analyst_token(aegis):
    return login(aegis, "analyst", "analyst123")


@pytest.fixture(scope="session")
def seeded_metrics(aegis, admin_token):
    """Seed the two demo metrics so list_metrics / run_metric are exercisable."""
    hdr = {"Authorization": f"Bearer {admin_token}"}
    metrics = [
        {
            "name": "arrival_guest_count",
            "description": "已确认及在住订单的入住人数",
            "sql_template": ("SELECT sum(guest_count) AS arrival_guests FROM hotel_bookings "
                             "WHERE booking_status IN ('confirmed', 'checked_in')"),
            "params": [],
            "unit": "count",
        },
        {
            "name": "confirmed_room_revenue",
            "description": "已确认及在住房费收入",
            "sql_template": ("SELECT sum(room_revenue) AS confirmed_room_revenue FROM hotel_bookings "
                             "WHERE booking_status IN ('confirmed', 'checked_in')"),
            "params": [],
            "unit": "cny",
        },
    ]
    for m in metrics:
        st, b = http_json("POST", aegis + "/admin/api/datasources/demo/metrics", m, hdr)
        assert st in (200, 409), f"seed metric failed: {b}"
    return metrics


@pytest.fixture(scope="session")
def mcp_analyst(aegis):
    c = MCPClient(aegis, {"X-MCP-API-Key": "mcp-demo-key"})
    c.call("initialize", {
        "protocolVersion": "2024-11-05",
        "capabilities": {},
        "clientInfo": {"name": "pytest-analyst", "version": "1"},
    })
    c.call("notifications/initialized", notification=True)
    return c


@pytest.fixture(scope="session")
def mcp_admin(aegis, admin_token):
    c = MCPClient(aegis, {"Authorization": f"Bearer {admin_token}"})
    c.call("initialize", {
        "protocolVersion": "2024-11-05",
        "capabilities": {},
        "clientInfo": {"name": "pytest-admin", "version": "1"},
    })
    c.call("notifications/initialized", notification=True)
    return c
