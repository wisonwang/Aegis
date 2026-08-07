#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Semantic metrics: seeding and governed execution with tenant scoping."""
from conftest import http_json, list_of, rows_of


def test_list_metrics(aegis, analyst_token, seeded_metrics):
    st, b = http_json("GET", aegis + "/api/v1/datasources/demo/metrics",
                      headers={"Authorization": f"Bearer {analyst_token}"})
    assert st == 200
    names = [m["name"] for m in list_of(b, "metrics", "data")]
    assert {"arrival_guest_count", "confirmed_room_revenue"} <= set(names)


def test_run_metric_tenant_scoping(aegis, analyst_token, admin_token, seeded_metrics):
    st, b = http_json("POST", aegis + "/api/v1/datasources/demo/metrics/arrival_guest_count/run",
                      {"params": {}},
                      headers={"Authorization": f"Bearer {analyst_token}"})
    assert st == 200
    rows = rows_of(b)
    assert rows and rows[0]["arrival_guests"] == 3      # acme: Alice 2 + Daisy 1

    st2, b2 = http_json("POST", aegis + "/api/v1/datasources/demo/metrics/arrival_guest_count/run",
                        {"params": {}},
                        headers={"Authorization": f"Bearer {admin_token}"})
    assert rows_of(b2)[0]["arrival_guests"] == 4        # + globex Brian
