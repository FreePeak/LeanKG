#!/usr/bin/env python3
"""Print the prompt for a given repo slug from repos.yaml (one line on stdout)."""
from __future__ import annotations

import argparse
import sys
from pathlib import Path

try:
    import yaml
except ImportError:
    print("ERROR: PyYAML is required for get_prompt.py", file=sys.stderr)
    sys.exit(2)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repos", type=Path, required=True)
    parser.add_argument("--slug", required=True)
    args = parser.parse_args()

    data = yaml.safe_load(args.repos.read_text(encoding="utf-8"))
    for entry in data.get("repos", []):
        if entry["slug"] == args.slug:
            print(entry["prompt"])
            return 0
    print(f"ERROR: slug '{args.slug}' not found in {args.repos}", file=sys.stderr)
    return 2


if __name__ == "__main__":
    sys.exit(main())