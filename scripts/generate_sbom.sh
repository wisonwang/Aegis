#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SOURCE_PATH="${1:-${ROOT_DIR}}"
OUTPUT_FILE="${2:-${ROOT_DIR}/dist/aegis-sbom.spdx.json}"

mkdir -p "$(dirname "${OUTPUT_FILE}")"

run_syft() {
  local syft_cmd=("$@")
  "${syft_cmd[@]}" "dir:${SOURCE_PATH}" -o "spdx-json=${OUTPUT_FILE}"
}

if command -v syft >/dev/null 2>&1; then
  run_syft syft
elif command -v docker >/dev/null 2>&1; then
  if ! docker info >/dev/null 2>&1; then
    echo "Docker is installed but the daemon is not reachable. Start Docker Desktop or install syft locally." >&2
    exit 1
  fi
  docker run --rm \
    -v "${ROOT_DIR}:/workspace" \
    anchore/syft:latest \
    "dir:/workspace" -o "spdx-json=/workspace/${OUTPUT_FILE#${ROOT_DIR}/}"
else
  echo "syft is not available. Install syft or run with Docker to generate SBOM." >&2
  exit 1
fi

echo "SBOM written to ${OUTPUT_FILE}"
