#!/usr/bin/env python3
"""Clone each repo in repos.yaml with --depth 1 at the pinned ref.

Skips clones that already exist (idempotent), so re-running `make setup` is a
no-op after the first run. If the existing clone's HEAD doesn't match the
pinned ref, prints a warning so the user can `make clean && make setup` if a
re-pin is needed.
"""
from __future__ import annotations

import argparse
import shutil
import subprocess
import sys
from pathlib import Path

try:
    import yaml
except ImportError:
    print("ERROR: PyYAML is required for clone_repos.py. Install with: pip install pyyaml", file=sys.stderr)
    sys.exit(2)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repos", type=Path, required=True, help="Path to repos.yaml")
    parser.add_argument("--target", type=Path, required=True, help="Directory to clone into")
    return parser.parse_args()


def git_clone_shallow(url: str, ref: str, target: Path) -> None:
    if target.exists():
        # Already cloned — check HEAD matches pinned ref.
        try:
            head = subprocess.run(
                ["git", "-C", str(target), "rev-parse", "HEAD"],
                check=True, capture_output=True, text=True,
            ).stdout.strip()
            wanted = subprocess.run(
                ["git", "ls-remote", url, ref],
                check=True, capture_output=True, text=True,
            ).stdout.split()[0]
            if head == wanted:
                print(f"  [skip] {target.name} already at {ref[:10]}")
                return
            else:
                print(f"  [warn] {target.name} HEAD={head[:10]} != {ref}={wanted[:10]}; delete and re-clone")
                shutil.rmtree(target)
        except subprocess.CalledProcessError as exc:
            print(f"  [warn] could not inspect {target.name}: {exc}", file=sys.stderr)

    target.parent.mkdir(parents=True, exist_ok=True)
    print(f"  [clone] {url} -> {target} @ {ref}")
    subprocess.run(
        ["git", "clone", "--depth", "1", "--branch", ref, url, str(target)],
        check=True,
    )


def main() -> int:
    args = parse_args()
    data = yaml.safe_load(args.repos.read_text(encoding="utf-8"))
    repos = data.get("repos", [])
    if not repos:
        print("ERROR: repos.yaml has no 'repos' list", file=sys.stderr)
        return 2
    args.target.mkdir(parents=True, exist_ok=True)

    failures: list[tuple[str, str]] = []
    for entry in repos:
        slug = entry["slug"]
        url = entry["url"]
        ref = entry["ref"]
        target = args.target / slug
        try:
            git_clone_shallow(url, ref, target)
        except subprocess.CalledProcessError as exc:
            failures.append((slug, str(exc)))
            # Clean up partial clone so a retry is possible
            if target.exists():
                shutil.rmtree(target, ignore_errors=True)

    if failures:
        print("\nFAILED clones (continuing so other repos still get cloned):", file=sys.stderr)
        for slug, err in failures:
            print(f"  - {slug}: {err}", file=sys.stderr)
        return 1
    print(f"\ncloned {len(repos)} repos into {args.target}")
    return 0


if __name__ == "__main__":
    sys.exit(main())