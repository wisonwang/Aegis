#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST_DIR="${ROOT_DIR}/dist"
OUTPUT_FILE="${1:-${DIST_DIR}/checksums.txt}"

mkdir -p "${DIST_DIR}"

if command -v sha256sum >/dev/null 2>&1; then
  HASH_CMD=(sha256sum)
elif command -v shasum >/dev/null 2>&1; then
  HASH_CMD=(shasum -a 256)
else
  echo "Neither sha256sum nor shasum is available." >&2
  exit 1
fi

artifacts="$(find "${DIST_DIR}" -maxdepth 1 -type f \( -name 'aegis_*.tar.gz' -o -name 'aegis-*.json' -o -name 'aegis-vulns*.json' \) | sort)"

if [ -z "${artifacts}" ]; then
  echo "No release artifacts found in ${DIST_DIR}" >&2
  exit 1
fi

(
  cd "${DIST_DIR}"
  : > "${OUTPUT_FILE##*/}"
  while IFS= read -r artifact; do
    [ -n "${artifact}" ] || continue
    "${HASH_CMD[@]}" "$(basename "${artifact}")" >> "${OUTPUT_FILE##*/}"
  done <<EOF
${artifacts}
EOF
)

echo "Checksums written to ${OUTPUT_FILE}"
