#!/bin/bash
# Usage: ./scripts/sign_artifacts.sh <version>
# Signs release artifacts and checksums using cosign

set -euo pipefail

VERSION=${1:-dev}
cd "$(git rev-parse --show-toplevel 2>/dev/null || pwd)" || exit 1

DIST_DIR="$(pwd)/dist"
mkdir -p "${DIST_DIR}"

echo "=== Aegis Supply Chain Artifact Signing ==="
echo "Version: ${VERSION}"

if ! command -v cosign >/dev/null 2>&1; then
  echo "Error: 'cosign' is not available locally."
  echo "Please install 'cosign' (https://docs.sigstore.dev/cosign/installation)."
  exit 1
fi

cd "${DIST_DIR}" || exit 1

for file in aegis_v*.tar.gz aegis-sbom*.json checksums.txt; do
  if [ -f "$file" ]; then
    echo "Signing: $file"
    cosign sign-blob "$file" --output-signature "$file.sig" --output-certificate "$file.pem" || true
  fi
done

echo ""
echo "=== Signing complete ==="
echo "Signatures and certificates saved to ${DIST_DIR}/"
echo ""
echo "Note: For verification, use:"
echo "  cosign verify-blob <artifact> --signature <artifact>.sig --certificate <artifact>.pem"
