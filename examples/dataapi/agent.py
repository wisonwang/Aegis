#!/usr/bin/env python3
"""
A tiny "Text-to-Query" Agent backed by Aegis.

The point of this example is the *wedge*: the Agent never touches the
database directly. It always goes through Aegis, which applies table/row/column
governance + masking, and the Agent estimates cost/risk BEFORE executing.

Flow:
    question
      -> (optional) Aegis NL2SQL  -> governed SQL
      -> Aegis estimate_query     -> cost / risk preview (no execution)
      -> Aegis query              -> governed, masked result

Works with zero LLM config: if the server has no NL2SQL key configured, the
script falls back to a tiny local stub that maps demo questions to SQL.

Run:
    pip install httpx
    python3 agent.py "How many customers do we have?"
"""
import os
import sys

import httpx

BASE = os.environ.get("AEGIS_BASE", "http://localhost:8080")
USER = os.environ.get("AEGIS_USER", "analyst")
PASS = os.environ.get("AEGIS_PASS", "analyst123")
DATASOURCE = os.environ.get("AEGIS_DS", "demo")


def login() -> str:
    r = httpx.post(f"{BASE}/api/v1/auth/login",
                   json={"username": USER, "password": PASS}, timeout=10)
    r.raise_for_status()
    return r.json()["token"]


def stub_nl2sql(question: str) -> str:
    """Last-resort local mapping for the seeded demo schema."""
    q = question.lower()
    if "how many" in q and "customer" in q:
        return "SELECT COUNT(*) AS cnt FROM customers"
    if "customer" in q and ("name" in q or "list" in q):
        return "SELECT id, name, phone FROM customers"
    if "order" in q:
        return "SELECT * FROM orders"
    return "SELECT * FROM customers"


def nl2sql(token: str, question: str) -> str:
    """Try the server's NL2SQL; fall back to the stub if unconfigured."""
    r = httpx.post(f"{BASE}/api/v1/datasources/{DATASOURCE}/nl2sql",
                   headers={"Authorization": f"Bearer {token}"},
                   json={"question": question}, timeout=30)
    if r.status_code >= 400:
        # Most likely NL2SQL is disabled on the server -> use the stub.
        return stub_nl2sql(question)
    return r.json().get("generated_sql") or stub_nl2sql(question)


def estimate(token: str, sql: str) -> dict:
    r = httpx.post(f"{BASE}/api/v1/datasources/{DATASOURCE}/query/estimate",
                   headers={"Authorization": f"Bearer {token}"},
                   json={"sql": sql}, timeout=10)
    r.raise_for_status()
    return r.json()


def run_query(token: str, sql: str) -> dict:
    r = httpx.post(f"{BASE}/api/v1/datasources/{DATASOURCE}/query",
                   headers={"Authorization": f"Bearer {token}"},
                   json={"sql": sql}, timeout=10)
    r.raise_for_status()
    return r.json()


def main():
    question = sys.argv[1] if len(sys.argv) > 1 else "How many customers do we have?"
    print(f"Q: {question}")

    token = login()

    sql = nl2sql(token, question)
    print(f"generated SQL: {sql}")

    est = estimate(token, sql)
    print(f"estimate: risk={est['risk_level']} rows={est['estimated_rows']} "
          f"pii={est['has_pii']} sensitivity={est['max_sensitivity']}")
    for w in est.get("warnings", []):
        print(f"  ! {w}")

    if est["risk_level"] == "unknown":
        print("aborting: governance denied the estimate (table not authorized).")
        return

    result = run_query(token, sql)
    print("governed result:")
    print(result)


if __name__ == "__main__":
    main()
