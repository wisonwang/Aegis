#!/usr/bin/env python3

import argparse
import json
import socket
import subprocess
import sys
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib import error, request


ROOT = Path(__file__).resolve().parent.parent


def free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


class FakeLLMHandler(BaseHTTPRequestHandler):
    def do_POST(self):
        if self.path != "/chat/completions":
            self.send_error(404)
            return
        length = int(self.headers.get("Content-Length", "0"))
        body = self.rfile.read(length)
        try:
            payload = json.loads(body.decode("utf-8"))
        except json.JSONDecodeError:
            self.send_error(400, "invalid json")
            return
        messages = payload.get("messages", [])
        question = ""
        if messages:
            question = str(messages[-1].get("content", ""))
        lower_question = question.lower()
        sql = (
            "SELECT sum(room_revenue) AS confirmed_room_revenue "
            "FROM hotel_bookings WHERE booking_status IN ('confirmed', 'checked_in')"
        )
        explanation = "sum confirmed room revenue"
        if "客人" in question or "guest" in lower_question or "会员" in question:
            sql = (
                "SELECT sum(guest_count) AS arrival_guests "
                "FROM hotel_bookings WHERE booking_status IN ('confirmed', 'checked_in')"
            )
            explanation = "sum arrival guests"
        content = json.dumps({"sql": sql, "explanation": explanation}, ensure_ascii=False)
        raw = json.dumps({"choices": [{"message": {"content": content}}]}).encode("utf-8")
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def log_message(self, fmt, *args):
        return


def start_fake_llm():
    port = free_port()
    server = ThreadingHTTPServer(("127.0.0.1", port), FakeLLMHandler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    return server, f"http://127.0.0.1:{port}"


def http_json(method: str, url: str, body=None, headers=None):
    raw = None
    req_headers = {"Accept": "application/json"}
    if headers:
        req_headers.update(headers)
    if body is not None:
        raw = json.dumps(body).encode("utf-8")
        req_headers.setdefault("Content-Type", "application/json")
    req = request.Request(url, data=raw, headers=req_headers, method=method)
    try:
        with request.urlopen(req, timeout=10) as resp:
            data = resp.read()
            text = data.decode("utf-8") if data else ""
            parsed = json.loads(text) if text else None
            return resp.status, parsed, dict(resp.headers)
    except error.HTTPError as exc:
        text = exc.read().decode("utf-8")
        parsed = None
        if text:
            try:
                parsed = json.loads(text)
            except json.JSONDecodeError:
                parsed = text
        return exc.code, parsed, dict(exc.headers)


def assert_true(condition, message):
    if not condition:
        raise AssertionError(message)


def wait_ready(base_url: str, proc: subprocess.Popen, log_path: Path, timeout_sec: int = 40):
    deadline = time.time() + timeout_sec
    last_error = None
    while time.time() < deadline:
        if proc.poll() is not None:
            logs = log_path.read_text(encoding="utf-8", errors="ignore")
            raise RuntimeError(f"Aegis exited early with code {proc.returncode}\n{logs}")
        try:
            status, body, _ = http_json("GET", f"{base_url}/api/v1/ready")
            if status == 200 and body and body.get("status") == "ready":
                return
        except Exception as exc:
            last_error = exc
        time.sleep(0.5)
    logs = log_path.read_text(encoding="utf-8", errors="ignore")
    raise RuntimeError(f"Aegis did not become ready in time: {last_error}\n{logs}")


class MCPClient:
    def __init__(self, base_url: str, headers: dict):
        self.base_url = base_url.rstrip("/") + "/mcp"
        self.headers = {
            "Accept": "application/json",
            "Content-Type": "application/json",
        }
        self.headers.update(headers)
        self.next_id = 1

    def call(self, method: str, params=None, notification: bool = False):
        payload = {"jsonrpc": "2.0", "method": method}
        if not notification:
            payload["id"] = self.next_id
            self.next_id += 1
        if params is not None:
            payload["params"] = params
        status, body, headers = http_json("POST", self.base_url, payload, self.headers)
        if notification:
            assert_true(status == 202, f"notification {method} should return 202, got {status}")
            return None
        sid = headers.get("Mcp-Session-Id")
        if sid:
            self.headers["Mcp-Session-Id"] = sid
        assert_true(status == 200, f"MCP {method} failed with status {status}: {body}")
        assert_true(body.get("error") is None, f"MCP {method} returned error: {body}")
        return body["result"]

    def tool(self, name: str, arguments=None):
        result = self.call("tools/call", {"name": name, "arguments": arguments or {}})
        text = result["content"][0]["text"]
        return json.loads(text)


def login_user(base_url: str, username: str, password: str) -> str:
    login_status, login_body, _ = http_json(
        "POST",
        f"{base_url}/api/v1/login",
        {"username": username, "password": password},
    )
    assert_true(login_status == 200 and login_body.get("token"), f"login failed for {username}: {login_body}")
    return login_body["token"]


def create_metric(base_url: str):
    token = login_user(base_url, "admin", "admin123")
    metric_bodies = [
        {
            "name": "arrival_guest_count",
            "description": "已确认及在住订单的入住人数",
            "sql_template": "SELECT sum(guest_count) AS arrival_guests FROM hotel_bookings WHERE booking_status IN ('confirmed', 'checked_in')",
            "params": [],
            "unit": "count",
        },
        {
            "name": "confirmed_room_revenue",
            "description": "已确认及在住订单的房费收入",
            "sql_template": "SELECT sum(room_revenue) AS confirmed_room_revenue FROM hotel_bookings WHERE booking_status IN ('confirmed', 'checked_in')",
            "params": [],
            "unit": "cny",
        },
    ]
    for metric_body in metric_bodies:
        status, body, _ = http_json(
            "POST",
            f"{base_url}/admin/api/datasources/demo/metrics",
            metric_body,
            {"Authorization": f"Bearer {token}"},
        )
        assert_true(status == 200, f"create metric failed: {body}")


def build_client(base_url: str, mode: str) -> MCPClient:
    if mode == "admin":
        token = login_user(base_url, "admin", "admin123")
        return MCPClient(base_url, {"Authorization": f"Bearer {token}"})
    return MCPClient(base_url, {"X-MCP-API-Key": "mcp-demo-key"})


def write_config(path: Path, data_dir: Path, llm_base_url: str, port: int):
    config = {
        "environment": "development",
        "listen_addr": f":{port}",
        "jwt_secret": "mcp-e2e-secret",
        "mask_secret": "mcp-e2e-mask-secret",
        "jwt_expiry": "24h",
        "db_type": "sqlite",
        "db_path": str(path.parent / "control.db"),
        "data_dir": str(data_dir),
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
            "base_url": llm_base_url,
            "api_key": "fake-key",
            "model": "fake-sql-model",
            "timeout_sec": 10,
            "max_retries": 1,
        },
        "logging": {"format": "text", "level": "debug"},
    }
    path.write_text(json.dumps(config, ensure_ascii=False, indent=2), encoding="utf-8")


def run_scenario(base_url: str, mode: str):
    create_metric(base_url)
    client = build_client(base_url, mode)
    init = client.call("initialize", {
        "protocolVersion": "2024-11-05",
        "capabilities": {},
        "clientInfo": {"name": f"mcp-e2e-{mode}", "version": "1.0"},
    })
    assert_true(init["serverInfo"]["name"] == "aegis-mcp", "unexpected mcp server name")
    client.call("notifications/initialized", notification=True)

    tools = client.call("tools/list")["tools"]
    tool_names = {tool["name"] for tool in tools}
    for name in [
        "list_datasources",
        "list_tables",
        "describe_table",
        "get_catalog",
        "query",
        "estimate_query",
        "list_metrics",
        "run_metric",
        "list_datasets",
        "get_dataset_catalog",
        "nl2sql",
    ]:
        assert_true(name in tool_names, f"missing tool: {name}")

    datasources = client.tool("list_datasources")
    assert_true(any(ds["name"] == "demo" for ds in datasources), "demo datasource not found")

    tables = client.tool("list_tables", {"datasource": "demo"})
    table_names = {t["name"] if isinstance(t, dict) else t for t in tables["tables"]}
    assert_true("hotel_bookings" in table_names, "hotel_bookings table missing")
    assert_true("guest_profiles" in table_names, "guest_profiles table missing")

    columns = client.tool("describe_table", {"datasource": "demo", "table": "guest_profiles"})
    column_names = {c["name"] for c in columns["columns"]}
    assert_true({"guest_name", "member_tier", "phone", "email", "tenant_id"}.issubset(column_names), "unexpected guest_profiles columns")

    catalog = client.tool("get_catalog", {"datasource": "demo"})
    assert_true(len(catalog["tables"]) >= 2, "catalog should expose governed tables")

    query = client.tool(
        "query",
        {"datasource": "demo", "sql": "SELECT guest_name, member_tier, phone, email FROM guest_profiles ORDER BY id"},
    )
    rows = query["queryResult"]["rows"]
    assert_true(len(rows) == 2 if mode != "admin" else len(rows) == 4, f"unexpected governed guest rows: {len(rows)}")
    first = rows[0]
    if mode == "admin":
        assert_true(first["guest_name"] == "Alice Zhang", f"admin should see raw guest name: {first}")
        assert_true(first["phone"] == "13812345678", f"admin should see raw phone: {first}")
        assert_true(first["email"] == "alice.zhang@demo.com", f"admin should see raw email: {first}")
    else:
        assert_true(first["guest_name"] == "A*********g", f"guest name should be masked: {first}")
        assert_true(first["phone"] == "138****5678", f"phone should be masked: {first}")
        assert_true(first["email"] == "a***@demo.com", f"email should be masked: {first}")

    estimate = client.tool(
        "estimate_query",
        {"datasource": "demo", "sql": "SELECT hotel_name, room_revenue FROM hotel_bookings"},
    )
    assert_true(estimate["estimated_rows"] >= 1, f"estimate rows invalid: {estimate}")
    assert_true("hotel_bookings" in estimate["tables"], f"estimate should mention hotel_bookings: {estimate}")

    metrics = client.tool("list_metrics", {"datasource": "demo"})
    assert_true(any(m["name"] == "arrival_guest_count" for m in metrics["metrics"]), "arrival_guest_count metric missing")
    assert_true(any(m["name"] == "confirmed_room_revenue" for m in metrics["metrics"]), "confirmed_room_revenue metric missing")

    metric = client.tool("run_metric", {"datasource": "demo", "metric": "arrival_guest_count", "params": {}})
    metric_rows = metric["queryResult"]["rows"]
    expected_arrival_guests = 4 if mode == "admin" else 3
    assert_true(metric_rows and metric_rows[0]["arrival_guests"] == expected_arrival_guests, f"metric result unexpected: {metric_rows}")

    confirmed_room_revenue = client.tool("run_metric", {"datasource": "demo", "metric": "confirmed_room_revenue", "params": {}})
    confirmed_room_revenue_rows = confirmed_room_revenue["queryResult"]["rows"]
    expected_room_revenue = 6040.0 if mode == "admin" else 4160.0
    assert_true(
        confirmed_room_revenue_rows and abs(float(confirmed_room_revenue_rows[0]["confirmed_room_revenue"]) - expected_room_revenue) < 0.0001,
        f"confirmed_room_revenue result unexpected: {confirmed_room_revenue_rows}",
    )

    datasets = client.tool("list_datasets")
    assert_true(any(d["name"] == "hotel_confirmed_bookings" for d in datasets["datasets"]), "hotel_confirmed_bookings dataset missing")

    dataset_catalog = client.tool("get_dataset_catalog", {"name": "hotel_confirmed_bookings"})
    dataset_field_names = {field["name"] for field in dataset_catalog["fields"]}
    assert_true({"hotel_name", "channel", "room_type", "room_revenue", "booking_status"}.issubset(dataset_field_names), f"dataset catalog unexpected: {dataset_catalog}")

    resources = client.call("resources/list")["resources"]
    uris = {res["uri"] for res in resources}
    assert_true("aegis://demo/schema" in uris, "demo schema resource missing")
    assert_true("aegis://dataset/hotel_confirmed_bookings/schema" in uris, "dataset schema resource missing")

    ds_resource = client.call("resources/read", {"uri": "aegis://demo/schema"})
    contents = ds_resource["contents"]
    assert_true(any(item["mimeType"] == "text/markdown" for item in contents), "missing markdown resource")

    dataset_resource = client.call("resources/read", {"uri": "aegis://dataset/hotel_confirmed_bookings/schema"})
    assert_true(len(dataset_resource["contents"]) == 2, "dataset resource should contain markdown+json")

    prompts = client.call("prompts/list")["prompts"]
    assert_true(any(p["name"] == "nl2sql" for p in prompts), "nl2sql prompt missing")

    prompt = client.call(
        "prompts/get",
        {"name": "nl2sql", "arguments": {"datasource": "demo", "question": "已确认订单的房费收入是多少？"}},
    )
    assert_true(len(prompt["messages"]) == 2, f"prompt should return 2 messages: {prompt}")

    nl2sql = client.tool(
        "nl2sql",
        {"datasource": "demo", "question": "已确认订单的房费收入是多少？"},
    )
    nl_rows = nl2sql["queryResult"]["rows"]
    expected_nl2sql_room_revenue = 6040.0 if mode == "admin" else 4160.0
    assert_true(
        nl_rows and abs(float(nl_rows[0]["confirmed_room_revenue"]) - expected_nl2sql_room_revenue) < 0.0001,
        f"nl2sql result unexpected: {nl_rows}",
    )
    assert_true("booking_status IN ('confirmed', 'checked_in')" in nl2sql["generated_sql"], f"unexpected nl2sql sql: {nl2sql}")

    return {
        "mode": mode,
        "datasource": "demo",
        "scenario": "酒店运营晨会备数",
        "query_rows": rows,
        "metric_rows": metric_rows,
        "confirmed_room_revenue_rows": confirmed_room_revenue_rows,
        "nl2sql_rows": nl_rows,
        "resource_uris": sorted(uris),
    }


def main():
    parser = argparse.ArgumentParser(description="Run the scenario-driven Aegis MCP E2E test")
    parser.add_argument("--mode", choices=["analyst", "admin"], default="analyst")
    args = parser.parse_args()

    fake_llm, llm_base = start_fake_llm()
    with tempfile.TemporaryDirectory(prefix="aegis-mcp-e2e-") as tmp:
        tmpdir = Path(tmp)
        cfg_path = tmpdir / "config.json"
        data_dir = tmpdir / "data"
        port = free_port()
        write_config(cfg_path, data_dir, llm_base, port)
        log_path = tmpdir / "aegis.log"
        with log_path.open("w", encoding="utf-8") as logs:
            proc = subprocess.Popen(
                ["go", "run", "./cmd/aegis", "-config", str(cfg_path)],
                cwd=ROOT,
                stdout=logs,
                stderr=subprocess.STDOUT,
            )
            try:
                base_url = f"http://127.0.0.1:{port}"
                wait_ready(base_url, proc, log_path)
                result = run_scenario(base_url, args.mode)
                print(f"MCP E2E scenario passed ({args.mode})")
                print(json.dumps(result, ensure_ascii=False, indent=2))
            finally:
                proc.terminate()
                try:
                    proc.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    proc.kill()
                fake_llm.shutdown()


if __name__ == "__main__":
    try:
        main()
    except Exception as exc:
        print(f"MCP E2E scenario failed: {exc}", file=sys.stderr)
        sys.exit(1)
