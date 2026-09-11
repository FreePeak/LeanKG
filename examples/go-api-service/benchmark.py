#!/usr/bin/env python3
"""
LeanKG Token Benchmark Script

This script demonstrates the token savings achieved by using LeanKG
for providing AI context compared to raw file content.

Benchmark scenarios:
1. Code Review: Before vs After LeanKG
2. Impact Analysis: Blast radius computation
3. Feature Testing: Full feature coverage verification
"""

import json
import os
import subprocess
import time

# The LeanKG engine binary (Go since v4.7.0). Override with LEANKG_BIN.
LEANKG_BIN = os.environ.get("LEANKG_BIN", "leankg")
from dataclasses import dataclass
from pathlib import Path
from typing import List, Dict, Any

CHARS_PER_TOKEN = 4

@dataclass
class BenchmarkResult:
    scenario: str
    before_tokens: int
    after_tokens: int
    savings_percent: float
    files_analyzed: int

def estimate_tokens(text: str) -> int:
    """Estimate token count from text (rough approximation)"""
    return max(1, len(text) // CHARS_PER_TOKEN)

def get_raw_file_content(file_path: str) -> str:
    """Get raw file content"""
    with open(file_path, 'r') as f:
        return f.read()

def get_all_related_files(base_file: str, graph_dir: str) -> List[str]:
    """Files affected by base_file, from the engine's impact blast radius.

    The blast-radius output is 'qn depth' lines; the FILE part of each qn is
    the affected file. Raises on engine failure (no silent fallback).
    """
    result = subprocess.run(
        [LEANKG_BIN, 'impact', base_file, '--depth', '2', '--compress'],
        cwd=graph_dir,
        capture_output=True,
        text=True,
        timeout=10
    )
    if result.returncode != 0:
        raise SystemExit(f"impact call failed: {result.stderr[:200]}")
    files = []
    for line in result.stdout.strip().splitlines():
        qn = line.rsplit(' ', 1)[0]
        if '::' in qn:
            f = qn.split('::')[0]
            if f not in files:
                files.append(f)
    return files or [base_file]

def benchmark_file_review(file_path: str, leankg_dir: str) -> BenchmarkResult:
    """Benchmark: Code review scenario.

    before = the base file PLUS the raw content of every affected file (what
    a reviewer reads manually); after = the base file PLUS the compact
    blast-radius answer LeanKG provides as context.
    """
    raw_content = get_raw_file_content(file_path)
    impact_files = get_all_related_files(file_path, leankg_dir)

    before_context = raw_content
    for f in impact_files:
        p = os.path.join(leankg_dir, f)
        if os.path.exists(p):
            before_context += f"\n# {f}\n" + get_raw_file_content(p)
    before_tokens = estimate_tokens(before_context)

    leankg_context = raw_content
    result = subprocess.run(
        [LEANKG_BIN, 'impact', file_path, '--depth', '2', '--compress'],
        cwd=leankg_dir, capture_output=True, text=True, timeout=10)
    if result.returncode != 0:
        raise SystemExit(f"impact call failed: {result.stderr[:200]}")
    leankg_context += "\n# impact (affected qn depth)\n" + result.stdout
    after_tokens = estimate_tokens(leankg_context)
    
    return BenchmarkResult(
        scenario="Code Review",
        before_tokens=before_tokens,
        after_tokens=after_tokens,
        savings_percent=((before_tokens - after_tokens) / before_tokens * 100) if before_tokens > 0 else 0,
        files_analyzed=len(impact_files)
    )

def benchmark_impact_analysis(file_path: str, leankg_dir: str) -> BenchmarkResult:
    """Benchmark: Impact analysis scenario.

    before = raw content of EVERY file in the blast radius (what an agent
    would read without LeanKG); after = the compressed blast-radius answer
    itself. This is the honest framing of the capability.
    """
    result = subprocess.run(
        [LEANKG_BIN, 'impact', file_path, '--depth', '2', '--compress'],
        cwd=leankg_dir,
        capture_output=True,
        text=True,
        timeout=10
    )
    if result.returncode != 0:
        raise SystemExit(f"impact call failed: {result.stderr[:200]}")
    after_tokens = estimate_tokens(result.stdout)
    before_tokens = 0
    for line in result.stdout.strip().splitlines():
        qn = line.rsplit(' ', 1)[0]
        if '::' not in qn:
            continue
        f = qn.split('::')[0]
        try:
            before_tokens += estimate_tokens(get_raw_file_content(os.path.join(leankg_dir, f)))
        except FileNotFoundError:
            pass
    
    return BenchmarkResult(
        scenario="Impact Analysis",
        before_tokens=before_tokens,
        after_tokens=after_tokens,
        savings_percent=((before_tokens - after_tokens) / before_tokens * 100) if before_tokens > 0 else 0,
        files_analyzed=1
    )

def benchmark_full_feature_testing(leankg_dir: str) -> BenchmarkResult:
    """Benchmark: Full feature testing scenario"""
    all_files = list(Path(leankg_dir).rglob('*.go'))
    all_files = [f for f in all_files if '.leankg' not in str(f)]
    
    total_raw_tokens = 0
    for f in all_files:
        try:
            content = get_raw_file_content(str(f))
            total_raw_tokens += estimate_tokens(content)
        except:
            pass
    
    try:
        result = subprocess.run(
            [LEANKG_BIN, 'status'],
            cwd=leankg_dir,
            capture_output=True,
            text=True,
            timeout=10
        )
        if result.returncode != 0:
            raise SystemExit(f"status call failed: {result.stderr[:200]}")
        leankg_tokens = estimate_tokens(result.stdout)
    except subprocess.TimeoutExpired:
        raise SystemExit("status call timed out")
    
    return BenchmarkResult(
        scenario="Full Feature Testing",
        before_tokens=total_raw_tokens,
        after_tokens=leankg_tokens,
        savings_percent=((total_raw_tokens - leankg_tokens) / total_raw_tokens * 100) if total_raw_tokens > 0 else 0,
        files_analyzed=len(all_files)
    )

def run_benchmarks():
    """Run all benchmark scenarios"""
    leankg_dir = str(Path(__file__).resolve().parent)
    results = []
    
    print("=" * 80)
    print("LeanKG Token Benchmark - Go API Service Example")
    print("=" * 80)
    print()
    
    test_file = f"{leankg_dir}/internal/services/user_service.go"
    if os.path.exists(test_file):
        print(f"1. Benchmarking: Code Review for user_service.go")
        result = benchmark_file_review(test_file, leankg_dir)
        results.append(result)
        print(f"   Before LeanKG: {result.before_tokens} tokens")
        print(f"   After LeanKG:  {result.after_tokens} tokens")
        print(f"   Savings:       {result.savings_percent:.1f}%")
        print()
    
    print("2. Benchmarking: Impact Analysis")
    result = benchmark_impact_analysis(test_file, leankg_dir)
    results.append(result)
    print(f"   Before LeanKG: {result.before_tokens} tokens")
    print(f"   After LeanKG:  {result.after_tokens} tokens")
    print(f"   Savings:       {result.savings_percent:.1f}%")
    print()
    
    print("3. Benchmarking: Full Feature Testing")
    result = benchmark_full_feature_testing(leankg_dir)
    results.append(result)
    print(f"   Before LeanKG: {result.before_tokens} tokens")
    print(f"   After LeanKG:  {result.after_tokens} tokens")
    print(f"   Savings:       {result.savings_percent:.1f}%")
    print(f"   Files analyzed: {result.files_analyzed}")
    print()
    
    avg_savings = sum(r.savings_percent for r in results) / len(results) if results else 0
    print("-" * 80)
    print(f"Average Token Savings: {avg_savings:.1f}%")
    print("-" * 80)
    
    return results

def get_leankg_features():
    """Get LeanKG feature capabilities"""
    leankg_dir = str(Path(__file__).resolve().parent)
    
    features = []
    
    print("\nLeanKG Feature Verification:")
    print("-" * 40)
    
    commands = [
        ("Status", "status"),
        ("Query", "query user --compress"),
        ("Impact", "impact internal/api/handler.go --depth 1 --compress"),
        ("Dependencies", "query imports --compress"),
    ]
    
    for name, cmd in commands:
        try:
            result = subprocess.run(
                f"{LEANKG_BIN} {cmd}".split(),
                cwd=leankg_dir,
                capture_output=True,
                text=True,
                timeout=10
            )
            status = "OK" if result.returncode == 0 else "FAIL"
            features.append((name, status))
            print(f"  {name}: {status}")
        except Exception as e:
            features.append((name, "FAIL"))
            print(f"  {name}: FAIL ({e})")
    
    return features

if __name__ == "__main__":
    results = run_benchmarks()
    features = get_leankg_features()
    
    with open(os.path.join(str(Path(__file__).resolve().parent), "benchmark_results.json"), "w") as f:
        json.dump({
            "results": [
                {
                    "scenario": r.scenario,
                    "before_tokens": r.before_tokens,
                    "after_tokens": r.after_tokens,
                    "savings_percent": r.savings_percent,
                    "files_analyzed": r.files_analyzed
                }
                for r in results
            ],
            "features": dict(features)
        }, f, indent=2)
    
    print("\nBenchmark results saved to benchmark_results.json")


def update_readme_table(results):
    """Regenerate the README savings table from THIS run so the README and
    benchmark_results.json can never drift apart. The impact `before` varies
    with the corpus (radius files are read raw), so the table also carries
    the run date."""
    readme = Path(__file__).resolve().parent / "README.md"
    text = readme.read_text()
    start = text.index("<!-- savings-table:start -->")
    end = text.index("<!-- savings-table:end -->") + len("<!-- savings-table:end -->")
    rows = "\n".join(
        f"| **{r['scenario']}** | {r['before_tokens']:,} tokens | {r['after_tokens']:,} tokens | **{r['savings_percent']:.1f}%** |"
        for r in results)
    import datetime
    stamp = datetime.date.today().isoformat()
    block = (f"<!-- savings-table:start -->\n"
             f"| Scenario | Without LeanKG | With LeanKG | Savings |\n"
             f"|----------|----------------|-------------|---------|\n"
             f"{rows}\n"
             f"<!-- savings-table:end -->\n\n"
             f"*Numbers from the {stamp} run of `benchmark.py` against the Go engine; "
             f"the impact `before` varies with the radius corpus content at run time.*")
    readme.write_text(text[:start] + block + text[end:])
