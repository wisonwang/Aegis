#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Dataset catalog management (D7): nested folder tree CRUD, dataset placement,
recursive filtering, move, delete-protection, and cycle guard.

``folder_id`` is purely organizational metadata; creating folders never touches
the governance key (``dataset.name``), so these operations are fully reversible.
"""
from conftest import http_json, list_of

H = lambda t: {"Authorization": f"Bearer {t}"}


def _datasource_id(base, token, name):
    """Admin/create-dataset lookups key on the datasource UUID (id), not its
    human name, so resolve it from the discovery list."""
    st, b = http_json("GET", base + "/api/v1/datasources",
                      headers={"Authorization": f"Bearer {token}"})
    assert st == 200, b
    for d in list_of(b, "datasources", "data"):
        if d["name"] == name:
            return d["id"]
    raise AssertionError(f"datasource {name!r} not found in list")


def test_folder_tree_crud_and_filtering(aegis, admin_token):
    hdr = H(admin_token)
    ds_id = _datasource_id(aegis, admin_token, "demo")

    # 1. root folder (create returns 201 Created)
    st, b = http_json("POST", aegis + "/admin/api/dataset-folders",
                      {"name": "酒店域"}, hdr)
    assert st in (200, 201), b
    root = b["id"]

    # 2. child folder
    st, b = http_json("POST", aegis + "/admin/api/dataset-folders",
                      {"name": "会员", "parent_id": root}, hdr)
    assert st in (200, 201), b
    child = b["id"]

    # 3. tree lists both
    st, b = http_json("GET", aegis + "/admin/api/dataset-folders", headers=hdr)
    assert st == 200
    folder_ids = {f["id"] for f in list_of(b, "folders", "data")}
    assert {root, child} <= folder_ids

    # 4. create a dataset inside the child folder
    st, b = http_json("POST", aegis + "/admin/api/datasets",
                      {"name": "vip_members", "display_name": "VIP会员",
                       "datasource_id": ds_id,
                       "definition": "SELECT id, guest_name FROM guest_profiles",
                       "folder_id": child}, hdr)
    assert st in (200, 201), b
    ds = b["id"]
    # placement is confirmed by reading the dataset back (create only returns id)
    st, b = http_json("GET", f"{aegis}/admin/api/datasets/{ds}", headers=hdr)
    assert b["folder_id"] == child

    # 5. recursive filter from root surfaces the nested dataset
    st, b = http_json("GET", f"{aegis}/admin/api/datasets?folder_id={root}&recursive=1",
                      headers=hdr)
    assert st == 200
    assert "vip_members" in [d["name"] for d in list_of(b, "datasets", "data")]

    # 6. non-recursive filter on root excludes the child's dataset
    st, b = http_json("GET", f"{aegis}/admin/api/datasets?folder_id={root}&recursive=0",
                      headers=hdr)
    assert "vip_members" not in [d["name"] for d in list_of(b, "datasets", "data")]

    # 7. move dataset back to uncategorized
    st, b = http_json("POST", f"{aegis}/admin/api/datasets/{ds}/move",
                      {"folder_id": ""}, hdr)
    assert st == 200, b
    st, b = http_json("GET", f"{aegis}/admin/api/datasets/{ds}", headers=hdr)
    assert b["folder_id"] == ""


def test_delete_non_empty_folder_rejected(aegis, admin_token):
    hdr = H(admin_token)
    ds_id = _datasource_id(aegis, admin_token, "demo")
    st, b = http_json("POST", aegis + "/admin/api/dataset-folders", {"name": "临时域"}, hdr)
    assert st in (200, 201), b
    nf = b["id"]
    st, b = http_json("POST", aegis + "/admin/api/datasets",
                      {"name": "tmp_ds", "display_name": "临时", "datasource_id": ds_id,
                       "definition": "SELECT id FROM guest_profiles", "folder_id": nf}, hdr)
    assert st in (200, 201), b
    tmp_id = b["id"]

    st, b = http_json("DELETE", f"{aegis}/admin/api/dataset-folders/{nf}", headers=hdr)
    assert st == 409, b

    # cleanup so the folder can be removed
    http_json("DELETE", f"{aegis}/admin/api/datasets/{tmp_id}", headers=hdr)
    st, b = http_json("DELETE", f"{aegis}/admin/api/dataset-folders/{nf}", headers=hdr)
    assert st in (200, 204), b


def test_folder_move_cycle_guard(aegis, admin_token):
    hdr = H(admin_token)
    st, b = http_json("POST", aegis + "/admin/api/dataset-folders", {"name": "P"}, hdr)
    assert st in (200, 201), b
    pid = b["id"]
    st, b = http_json("POST", aegis + "/admin/api/dataset-folders",
                      {"name": "C", "parent_id": pid}, hdr)
    assert st in (200, 201), b
    cid = b["id"]
    # moving a parent into its own descendant must be rejected
    st, b = http_json("PUT", f"{aegis}/admin/api/dataset-folders/{pid}",
                      {"name": "P", "parent_id": cid}, hdr)
    assert st == 400, b
