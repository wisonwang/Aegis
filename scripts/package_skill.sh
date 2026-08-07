#!/usr/bin/env bash

set -euo pipefail

SKILL_DIR="${1:-aegis-mcp}"
OUTPUT_ZIP="${2:-dist/aegis-mcp-skill.zip}"

if [[ ! -d "$SKILL_DIR" ]]; then
  echo "skill directory not found: $SKILL_DIR" >&2
  exit 1
fi

if [[ ! -f "$SKILL_DIR/SKILL.md" ]]; then
  echo "SKILL.md not found under: $SKILL_DIR" >&2
  exit 1
fi

mkdir -p "$(dirname "$OUTPUT_ZIP")"
rm -f "$OUTPUT_ZIP"

(
  cd "$SKILL_DIR"
  zip -qr "../$OUTPUT_ZIP" . \
    -x "*.DS_Store" \
    -x "__MACOSX/*"
)

echo "packed skill: $OUTPUT_ZIP"
