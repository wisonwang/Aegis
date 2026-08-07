#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Dataset product management: catalog, schema contract, and tenant-scoped query."""
import json

from conftest import http_json, list_of, rows_of


def _dataset_id(base, token, name):
    """The DataAPI dataset routes key on the dataset UUID (id), not its name,
    so resolve the id from the discovery list first."""
    st, b = http_json("GET", base + "/api/v1/datasets",
                      headers={"Authorization": f"Bearer {token}"})
    assert st == 200, b
    for d in list_of(b, "datasets", "data"):
        if d["name"] == name:
            return d["id"]
    raise AssertionError(f"dataset {name!r} not found in list")


def test_list_datasets(aegis, analyst_token):
    st, b = http_json("GET", aegis + "/api/v1/datasets",
                      headers={"Authorization": f"Bearer {analyst_token}"})
    assert st == 200
    names = [d["name"] for d in list_of(b, "datasets", "data")]
    assert "hotel_confirmed_bookings" in names


def test_dataset_detail_contract(aegis, analyst_token):
    ds_id = _dataset_id(aegis, analyst_token, "hotel_confirmed_bookings")
    st, b = http_json("GET", aegis + f"/api/v1/datasets/{ds_id}",
                      headers={"Authorization": f"Bearer {analyst_token}"})
    assert st == 200
    fields = b.get("fields")
    if isinstance(fields, str):
        fields = json.loads(fields)
    fnames = {f["name"] for f in (fields or [])}
    assert {"hotel_name", "channel", "room_revenue", "booking_status"} <= fnames


def test_query_dataset_tenant_scoping(aegis, analyst_token, admin_token):
    ds_id = _dataset_id(aegis, analyst_token, "hotel_confirmed_bookings")
    st, b = http_json("POST", aegis + f"/api/v1/datasets/{ds_id}/query",
                      {"params": []},
                      headers={"Authorization": f"Bearer {analyst_token}"})
    assert st == 200
    rows = rows_of(b)
    assert len(rows) == 2                      # acme confirmed/checked_in only
    assert all(r["tenant_id"] == "acme" for r in rows)

    st2, b2 = http_json("POST", aegis + f"/api/v1/datasets/{ds_id}/query",
                        {"params": []},
                        headers={"Authorization": f"Bearer {admin_token}"})
    assert len(rows_of(b2)) == 3               # acme 2 + globex 1
