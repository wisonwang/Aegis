#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Governance enforcement through the DataAPI (default-deny moat).

These tests prove the core thesis of Aegis: every query is rewritten through a
single governance engine, so table/row/column rules are *unavoidable* — the
analyst is scoped to the ``acme`` tenant, PII is masked, and DDL is rejected.
"""
from conftest import http_json, rows_of


def _query(base, token, sql):
    st, b = http_json("POST", base + "/api/v1/query",
                      {"datasource": "demo", "sql": sql},
                      headers={"Authorization": f"Bearer {token}"})
    return st, b


def test_analyst_row_policy_scoping(aegis, analyst_token, admin_token):
    st, b = _query(aegis, analyst_token, "SELECT count(*) AS c FROM hotel_bookings")
    assert st == 200
    assert rows_of(b)[0]["c"] == 3          # acme only (Alice, Daisy, Cathy)

    st2, b2 = _query(aegis, admin_token, "SELECT count(*) AS c FROM hotel_bookings")
    assert rows_of(b2)[0]["c"] == 5        # all tenants, admin bypasses policy


def test_analyst_column_masking(aegis, analyst_token):
    st, b = _query(aegis, analyst_token,
                   "SELECT guest_name, phone, email FROM guest_profiles ORDER BY id")
    assert st == 200
    rows = rows_of(b)
    assert len(rows) == 2                   # acme only
    first = rows[0]
    assert first["guest_name"] != "Alice Zhang"          # masked name
    assert "*" in first["phone"] and first["phone"] != "13812345678"
    # the row policy must be physically injected into the executed SQL
    assert "tenant_id" in (b.get("rewritten_sql") or "")


def test_admin_sees_raw_pii(aegis, admin_token):
    st, b = _query(aegis, admin_token,
                   "SELECT guest_name, phone, email FROM guest_profiles ORDER BY id")
    assert st == 200
    rows = rows_of(b)
    assert len(rows) == 4
    alice = [r for r in rows if r.get("guest_name") == "Alice Zhang"]
    assert alice and alice[0]["phone"] == "13812345678"


def test_ddl_is_blocked_for_analyst(aegis, analyst_token):
    st, b = _query(aegis, analyst_token, "CREATE TABLE attacker (id int)")
    assert st == 403
