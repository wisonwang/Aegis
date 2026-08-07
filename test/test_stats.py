from conftest import http_json

# ADR-0005, Phase 1: local adoption snapshot. These tests pin the contract so
# the endpoint can never silently start leaking PII.

EXPECTED_COUNT_KEYS = {
    "datasources", "datasource_types", "datasets", "workspaces",
    "users", "queries_served", "queries_denied", "mcp_sessions",
}

# The entire response schema is this allow-list. Anything else (tenant names,
# table/column names, SQL text) would be a privacy regression.
ALLOWED_TOP_KEYS = {"version", "commit", "edition", "uptime_seconds", "counts"}


def test_stats_requires_admin(aegis, analyst_token):
    st, _ = http_json("GET", aegis + "/admin/api/stats",
                      headers={"Authorization": f"Bearer {analyst_token}"})
    assert st in (401, 403)


def test_stats_snapshot_shape(aegis, admin_token):
    st, b = http_json("GET", aegis + "/admin/api/stats",
                      headers={"Authorization": f"Bearer {admin_token}"})
    assert st == 200
    assert set(b.keys()) <= ALLOWED_TOP_KEYS
    assert b.get("edition") in ("community", "enterprise")
    counts = b.get("counts")
    assert isinstance(counts, dict)
    assert set(counts.keys()) == EXPECTED_COUNT_KEYS
    for k, v in counts.items():
        if k == "datasource_types":
            assert isinstance(v, list), f"{k} must be list, got {type(v)}"
        else:
            assert isinstance(v, int), f"{k} must be int, got {type(v)}"
    assert counts["datasources"] >= 1
    assert isinstance(counts["datasource_types"], list)


def test_stats_queries_accumulate(aegis, admin_token, analyst_token):
    # Run a governed query as analyst, then confirm it shows up in the snapshot.
    q = {
        "datasource": "demo",
        "sql": "SELECT 1",
    }
    http_json("POST", aegis + "/api/v1/query", q,
              headers={"Authorization": f"Bearer {analyst_token}"})
    st, b = http_json("GET", aegis + "/admin/api/stats",
                      headers={"Authorization": f"Bearer {admin_token}"})
    assert st == 200
    assert b["counts"]["queries_served"] >= 1
