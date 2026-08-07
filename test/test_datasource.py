#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Datasource discovery surface (DataAPI): list sources, tables, columns, catalog."""
from conftest import http_json, list_of


def test_list_datasources(aegis, analyst_token):
    st, b = http_json("GET", aegis + "/api/v1/datasources",
                      headers={"Authorization": f"Bearer {analyst_token}"})
    assert st == 200
    names = [d["name"] for d in list_of(b, "datasources", "data")]
    assert "demo" in names


def test_list_tables(aegis, analyst_token):
    st, b = http_json("GET", aegis + "/api/v1/datasources/demo/tables",
                      headers={"Authorization": f"Bearer {analyst_token}"})
    assert st == 200
    names = [t["name"] for t in list_of(b, "tables", "data")]
    assert {"hotel_bookings", "guest_profiles"} <= set(names)


def test_describe_table(aegis, analyst_token):
    st, b = http_json("GET", aegis + "/api/v1/datasources/demo/tables/guest_profiles",
                      headers={"Authorization": f"Bearer {analyst_token}"})
    assert st == 200
    cols = {c["name"] for c in list_of(b, "columns", "data")}
    assert {"guest_name", "phone", "email", "tenant_id"} <= cols


def test_catalog(aegis, analyst_token):
    st, b = http_json("GET", aegis + "/api/v1/datasources/demo/catalog",
                      headers={"Authorization": f"Bearer {analyst_token}"})
    assert st == 200
    assert len(list_of(b, "tables", "data")) >= 2
