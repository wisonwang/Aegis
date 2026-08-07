"""DSN password masking + DSN docs link.

Covers the "hide password in the data source list" requirement:
  * admin list echoes a *masked* DSN (password redacted) plus a dsn_masked flag
    and a top-level dsn_docs link to the DSN format reference;
  * the consumer-facing /api/v1/datasources endpoint never exposes a dsn field;
  * MCP list_datasources also masks the DSN.
"""
import time

from conftest import http_json

DSN_RAW = "root:s3cr3tP@ss@tcp(127.0.0.1:3306)/masktest?parseTime=true"


def _create_ds(aegis, admin_token, name):
    st, b = http_json(
        "POST", aegis + "/admin/api/datasources",
        {"name": name, "type": "mysql", "dsn": DSN_RAW},
        headers={"Authorization": f"Bearer {admin_token}"},
    )
    assert st in (200, 201), f"create ds failed: {b}"
    return b["id"]


def _delete_ds(aegis, admin_token, ds_id):
    http_json("DELETE", aegis + f"/admin/api/datasources/{ds_id}",
              headers={"Authorization": f"Bearer {admin_token}"})


def test_admin_list_masks_dsn_and_links_docs(aegis, admin_token):
    name = f"masktest_{int(time.time()*1000)}"
    ds_id = _create_ds(aegis, admin_token, name)
    try:
        st, b = http_json("GET", aegis + "/admin/api/datasources",
                          headers={"Authorization": f"Bearer {admin_token}"})
        assert st == 200
        entries = [d for d in b.get("datasources", []) if d.get("id") == ds_id]
        assert entries, "created datasource missing from admin list"
        d = entries[0]
        # password must be redacted
        assert "****" in d["dsn"], f"DSN not masked: {d['dsn']}"
        assert "s3cr3tP@ss" not in d["dsn"], "raw password leaked in list"
        assert d["dsn_masked"] is True
        # docs link must be present at the top level
        assert isinstance(b.get("dsn_docs"), str) and b["dsn_docs"].startswith("http")
    finally:
        _delete_ds(aegis, admin_token, ds_id)


def test_consumer_list_never_exposes_dsn(aegis, admin_token, analyst_token):
    st, b = http_json("GET", aegis + "/api/v1/datasources",
                      headers={"Authorization": f"Bearer {analyst_token}"})
    assert st == 200
    for d in b.get("datasources", []):
        assert set(d.keys()) == {"id", "name", "type"}, f"unexpected keys: {d.keys()}"
        assert "dsn" not in d


def test_mcp_list_datasources_masks_dsn(aegis, admin_token, mcp_admin):
    name = f"masktest_{int(time.time()*1000)}"
    ds_id = _create_ds(aegis, admin_token, name)
    try:
        out = mcp_admin.tool("list_datasources")
        entries = [d for d in out if d.get("id") == ds_id]
        assert entries, "created datasource missing from MCP list"
        d = entries[0]
        assert "****" in d["dsn"], f"MCP DSN not masked: {d['dsn']}"
        assert "s3cr3tP@ss" not in d["dsn"], "raw password leaked via MCP"
        assert d["dsn_masked"] is True
    finally:
        _delete_ds(aegis, admin_token, ds_id)


def test_update_accepts_masked_dsn_as_noop(aegis, admin_token):
    """Pasting the masked DSN (echoed from the list) back on update must be
    accepted (200) and treated as 'no change' rather than corrupting the stored
    secret with the placeholder. This exercises the backend guard branch."""
    name = f"masktest_{int(time.time()*1000)}"
    ds_id = _create_ds(aegis, admin_token, name)
    try:
        _, b = http_json("GET", aegis + "/admin/api/datasources",
                         headers={"Authorization": f"Bearer {admin_token}"})
        masked = next(d["dsn"] for d in b["datasources"] if d["id"] == ds_id)

        st, _ = http_json("PUT", aegis + f"/admin/api/datasources/{ds_id}",
                          {"name": name, "type": "mysql", "dsn": masked},
                          headers={"Authorization": f"Bearer {admin_token}"})
        assert st == 200, f"update with masked dsn should be a no-op, got {st}"

        # after the no-op update, the list still returns a masked, well-formed
        # DSN (no error, no double-masking artifact)
        _, b2 = http_json("GET", aegis + "/admin/api/datasources",
                          headers={"Authorization": f"Bearer {admin_token}"})
        stored = next(d["dsn"] for d in b2["datasources"] if d["id"] == ds_id)
        assert stored.startswith("root:****@tcp("), f"unexpected stored dsn: {stored}"
    finally:
        _delete_ds(aegis, admin_token, ds_id)
