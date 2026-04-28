#!/usr/bin/env python3
"""
Compare A/B test results between baseline and LeanKG.
Usage: python3 compare_results.py <results_dir>
"""

import json
import sys
from pathlib import Path
from datetime import datetime


def compare_results(results_dir: str):
    """Compare results and generate report."""
    results_path = Path(results_dir)

    print(f"# LeanKG Benchmark Report - {datetime.now().strftime('%Y-%m-%d')}\n")
    print("## Summary\n")

    result_files = list(results_path.glob("**/*.json"))

    if not result_files:
        print("No results found. Run benchmarks first.\n")
        print("Steps to run benchmarks:")
        print("1. Ensure LeanKG is indexed: cargo run -- index ./src")
        print("2. Start Kilo with LeanKG MCP: kilo")
        print(
            "3. Run queries and export: kilo export <session_id> > results/<query_id>.json"
        )
        print("4. Re-run without LeanKG MCP for baseline comparison")
        return

    print(f"Total result files: {len(result_files)}\n")


def main():
    if len(sys.argv) < 2:
        print("Usage: compare_results.py <results_dir>")
        sys.exit(1)

    compare_results(sys.argv[1])


if __name__ == "__main__":
    main()
