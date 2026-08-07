#!/usr/bin/env bash

set -euo pipefail

SKILL_DIR="${1:-aegis-mcp}"
TARGET_ROOT="${TRAE_SKILLS_HOME:-$HOME/.trae-cn/skills}"
SKILL_NAME="$(basename "$SKILL_DIR")"
TARGET_DIR="$TARGET_ROOT/$SKILL_NAME"

if [[ ! -d "$SKILL_DIR" ]]; then
  echo "skill directory not found: $SKILL_DIR" >&2
  exit 1
fi

if [[ ! -f "$SKILL_DIR/SKILL.md" ]]; then
  echo "SKILL.md not found under: $SKILL_DIR" >&2
  exit 1
fi

mkdir -p "$TARGET_ROOT"
rm -rf "$TARGET_DIR"
cp -R "$SKILL_DIR" "$TARGET_DIR"

echo "installed skill to: $TARGET_DIR"
