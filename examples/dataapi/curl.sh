#!/usr/bin/env bash
# Aegis DataAPI quick-start cheat sheet (copy-paste).
# Assumes Aegis is running on :8080 with the seeded demo tenant.
set -euo pipefail

BASE="http://localhost:8080"
echo "== 1. Login as analyst (returns a JWT) =="
TOKEN=$(curl -s -X POST "$BASE/api/v1/login" \
  -H 'Content-Type: application/json' \
  -d '{"username":"analyst","password":"analyst123"}' | python3 -c 'import sys,json;print(json.load(sys.stdin)["token"])')
echo "token=$TOKEN"

AUTH="Authorization: Bearer $TOKEN"

echo; echo "== 2. List accessible tables =="
curl -s "$BASE/api/v1/datasources/demo/tables" -H "$AUTH" | python3 -m json.tool

echo; echo "== 3. Pre-execution cost/risk estimate (does NOT run the SQL) =="
curl -s -X POST "$BASE/api/v1/datasources/demo/query/estimate" \
  -H "$AUTH" -H 'Content-Type: application/json' \
  -d '{"sql":"SELECT * FROM guest_profiles"}' | python3 -m json.tool

echo; echo "== 4. Run a governed query (row policy + column masking applied) =="
curl -s -X POST "$BASE/api/v1/query" \
  -H "$AUTH" -H 'Content-Type: application/json' \
  -d '{"datasource":"demo","sql":"SELECT guest_name,phone FROM guest_profiles"}' | python3 -m json.tool

echo; echo "== 5. NL2SQL (needs config.nl2sql.api_key set; returns governed SQL + masked result) =="
curl -s -X POST "$BASE/api/v1/datasources/demo/nl2sql" \
  -H "$AUTH" -H 'Content-Type: application/json' \
  -d '{"question":"How many customers do we have?"}' | python3 -m json.tool

echo; echo "== 6. Run a curated metric (pre-defined, governed template) =="
curl -s -X POST "$BASE/api/v1/datasources/demo/metrics/arrival_guest_count/run" \
  -H "$AUTH" -H 'Content-Type: application/json' -d '{}' | python3 -m json.tool
