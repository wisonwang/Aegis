#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP_NAME="aegis"
VERSION="${1:-dev}"
DIST_DIR="${ROOT_DIR}/dist"
STAGE_DIR="${DIST_DIR}/release-stage"
COMMIT="${GIT_COMMIT:-$(git -C "${ROOT_DIR}" rev-parse --short HEAD 2>/dev/null || echo unknown)}"

TARGETS=(
  "linux amd64"
  "linux arm64"
  "darwin amd64"
  "darwin arm64"
)

mkdir -p "${DIST_DIR}"
rm -rf "${STAGE_DIR}"
mkdir -p "${STAGE_DIR}"

echo "Building release artifacts for version ${VERSION} (${COMMIT})"

for target in "${TARGETS[@]}"; do
  read -r goos goarch <<<"${target}"
  build_dir="${STAGE_DIR}/${APP_NAME}_${goos}_${goarch}"
  archive="${DIST_DIR}/${APP_NAME}_${VERSION}_${goos}_${goarch}.tar.gz"
  mkdir -p "${build_dir}"

  echo " -> ${goos}/${goarch}"
  (
    cd "${ROOT_DIR}"
    CGO_ENABLED=0 GOOS="${goos}" GOARCH="${goarch}" \
      go build -trimpath \
      -ldflags="-s -w -X github.com/wisonwang/aegis/internal/version.Version=${VERSION} -X github.com/wisonwang/aegis/internal/version.Commit=${COMMIT}" \
      -o "${build_dir}/${APP_NAME}" ./cmd/aegis
  )

  cp "${ROOT_DIR}/LICENSE" "${build_dir}/"
  cp "${ROOT_DIR}/NOTICE" "${build_dir}/"
  cp "${ROOT_DIR}/README.md" "${build_dir}/"

  tar -C "${build_dir}" -czf "${archive}" .
done

rm -rf "${STAGE_DIR}"
echo "Release archives written to ${DIST_DIR}"
