#!/bin/bash
# Usage: ./scripts/scan_vulnerabilities.sh
# Scans for vulnerabilities using Trivy (preferred) or Grype (fallback)

set -euo pipefail

cd "$(git rev-parse --show-toplevel 2>/dev/null || pwd)" || exit 1

DIST_DIR="$(pwd)/dist"
mkdir -p "${DIST_DIR}"

echo "=== Aegis Supply Chain Vulnerability Scan ==="

if command -v trivy >/dev/null 2>&1; then
  echo "Using Trivy for scanning..."
  trivy fs --format json --output "${DIST_DIR}/aegis-vulns-trivy.json" . || true
  trivy fs --format table . || true
elif command -v docker >/dev/null 2>&1; then
  echo "Using Trivy via Docker for scanning..."
  docker run --rm -v "$(pwd):/workspace" -w /workspace aquasec/trivy:latest fs --format json --output "/workspace/dist/aegis-vulns-trivy.json" . || true
  docker run --rm -v "$(pwd):/workspace" -w /workspace aquasec/trivy:latest fs --format table . || true
elif command -v grype >/dev/null 2>&1; then
  echo "Using Grype for scanning..."
  grype dir:. --output json > "${DIST_DIR}/aegis-vulns-grype.json" || true
  grype dir:. --output table || true
elif command -v docker >/dev/null 2>&1; then
  echo "Using Grype via Docker for scanning..."
  docker run --rm -v "$(pwd):/workspace" -w /workspace anchore/grype:latest dir:. --output json > "/workspace/dist/aegis-vulns-grype.json" || true
  docker run --rm -v "$(pwd):/workspace" -w /workspace anchore/grype:latest dir:. --output table || true
else
  echo "Error: neither 'trivy' nor 'grype' nor 'docker' is available locally."
  echo "Please install 'trivy' (https://aquasecurity.github.io/trivy) or start Docker."
  exit 1
fi

echo ""
echo "=== Scan complete ==="
echo "Results saved to ${DIST_DIR}/"
