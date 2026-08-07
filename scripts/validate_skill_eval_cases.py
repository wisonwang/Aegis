#!/usr/bin/env python3

import json
import sys
from pathlib import Path


def fail(message: str) -> None:
    print(f"eval cases invalid: {message}", file=sys.stderr)
    sys.exit(1)


def main() -> None:
    if len(sys.argv) != 2:
        fail("usage: validate_skill_eval_cases.py <cases.json>")

    path = Path(sys.argv[1])
    if not path.exists():
        fail(f"file not found: {path}")

    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except json.JSONDecodeError as exc:
        fail(f"invalid json: {exc}")

    if not isinstance(data, list) or not data:
        fail("top-level value must be a non-empty array")

    seen_ids = set()
    for index, item in enumerate(data, start=1):
        if not isinstance(item, dict):
            fail(f"item #{index} must be an object")

        required = ["id", "title", "user_input", "should_trigger_skill", "recommended_tools", "must_not", "acceptance"]
        for field in required:
            if field not in item:
                fail(f"item #{index} missing field: {field}")

        case_id = item["id"]
        if not isinstance(case_id, str) or not case_id:
            fail(f"item #{index} id must be a non-empty string")
        if case_id in seen_ids:
            fail(f"duplicate id: {case_id}")
        seen_ids.add(case_id)

        if not isinstance(item["title"], str) or not item["title"].strip():
            fail(f"{case_id} title must be a non-empty string")
        if not isinstance(item["user_input"], str) or not item["user_input"].strip():
            fail(f"{case_id} user_input must be a non-empty string")
        if not isinstance(item["should_trigger_skill"], bool):
            fail(f"{case_id} should_trigger_skill must be a boolean")

        for field in ["recommended_tools", "must_not", "acceptance"]:
            value = item[field]
            if not isinstance(value, list):
                fail(f"{case_id} {field} must be an array")
            for sub_index, sub_item in enumerate(value, start=1):
                if not isinstance(sub_item, str) or not sub_item.strip():
                    fail(f"{case_id} {field}[{sub_index}] must be a non-empty string")

        if item["should_trigger_skill"] and not item["acceptance"]:
            fail(f"{case_id} must have acceptance criteria")

    print(f"eval cases valid: {len(data)} cases")


if __name__ == "__main__":
    main()
