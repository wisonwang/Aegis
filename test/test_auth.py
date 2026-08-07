#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Authentication, JWT issuance, role separation and privilege boundaries."""
from conftest import http_json


def test_admin_login(aegis, admin_token):
    assert admin_token


def test_analyst_login(aegis, analyst_token):
    assert analyst_token


def test_wrong_password_rejected(aegis):
    st, b = http_json("POST", aegis + "/api/v1/login",
                      {"username": "admin", "password": "wrong-password"})
    assert st == 401


def test_service_account_cannot_use_password_login(aegis):
    # mcp-agent is a service account: it authenticates via API key only.
    st, b = http_json("POST", aegis + "/api/v1/login",
                      {"username": "mcp-agent", "password": "anything"})
    assert st == 401


def test_analyst_blocked_from_admin_api(aegis, analyst_token):
    st, b = http_json("GET", aegis + "/admin/api/users",
                      headers={"Authorization": f"Bearer {analyst_token}"})
    assert st in (401, 403)


def test_me_returns_roles(aegis, admin_token):
    st, b = http_json("GET", aegis + "/api/v1/me",
                      headers={"Authorization": f"Bearer {admin_token}"})
    assert st == 200
    assert "admin" in (b.get("roles") or [])
