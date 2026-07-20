#!/usr/bin/env python3
"""MCP tools smoke harness for LeanKG HTTP MCP (default: http://localhost:9699/mcp).

Improvements vs ad-hoc /tmp harnesses:
  - Discovers tools from tools/list (no stale static-only catalog)
  - Labels skips as mutating vs mega-graph-heavy (honest reasons)
  - Includes query_graph (US-GF-03) with a small token_budget
  - Defaults project=/workspace (LeanKG itself); set LEANKG_SMOKE_PROJECT for others

Usage:
  python3 scripts/mcp-smoke-tools.py
  LEANKG_SMOKE_PROJECT=/workspace python3 scripts/mcp-smoke-tools.py
  LEANKG_SMOKE_INCLUDE_HEAVY=1 python3 scripts/mcp-smoke-tools.py   # needs mem_limit >= 10g on mega-graphs
"""

from __future__ import annotations

import json
import os
import subprocess
import sys
import time
import urllib.request
from typing import Any

MCP_URL = os.environ.get("LEANKG_SMOKE_URL", "http://localhost:9699/mcp")
PROJECT = os.environ.get("LEANKG_SMOKE_PROJECT", "/workspace")
INCLUDE_HEAVY = os.environ.get("LEANKG_SMOKE_INCLUDE_HEAVY", "0") == "1"
SAMPLE_FILE = os.environ.get("LEANKG_SMOKE_FILE", "src/main.rs")

MUTATING = {
    "mcp_init",
    "mcp_index",
    "mcp_install",
    "add_knowledge",
    "update_knowledge",
    "delete_knowledge",
    "promote_environment",
    "add_annotation",
    "link_element",
    "add_documentation",
    "agent_diary_write",
    "report_query_outcome",
    "export_graph_snapshot",
    "wake_up",
}

# Full-graph / heavy tools — safe on small projects; skip on mega-graphs unless opted in.
MEGA_GRAPH_HEAVY = {
    "find_dead_code",
    "find_large_functions",
    "find_tunnels",
    "find_related_docs",
    "check_consistency",
    "get_cluster_skill",
    "get_clusters",
    "get_cluster_context",
    "get_god_nodes",
    "get_overview_context",
    "get_team_map",
    "get_service_graph",
    "kg_self_test",
    "kg_context",
    "kg_concept_map",
    "kg_trace_workflow",
    "kg_semantic_context",
    "semantic_search",
    "shortest_path",
    "run_raw_query",
    "load_layer",
    "temporal_query",
    "timeline",
    "search_by_environment",
    "search_by_requirement",
    "query_incidents",
    "find_env_conflicts",
    "get_service_context",
    "get_upcoming_changes",
    "search_annotations",
}

# Minimal args. Always inject project= unless the tool already has it.
DEFAULT_ARGS: dict[str, dict[str, Any]] = {
    "mcp_status": {},
    "mcp_index_docs": {"path": "docs"},
    "query_file": {"pattern": "*.rs", "limit": 5},
    "get_dependencies": {"file": SAMPLE_FILE},
    "get_dependents": {"file": SAMPLE_FILE},
    "get_impact_radius": {"file": SAMPLE_FILE, "depth": 1},
    "detect_changes": {"scope": "all"},
    "get_review_context": {"files": [SAMPLE_FILE]},
    "get_context": {"file": SAMPLE_FILE, "signature_only": True, "max_tokens": 500},
    "orchestrate": {"intent": f"show context for {SAMPLE_FILE}", "mode": "adaptive"},
    "ctx_read": {"file": SAMPLE_FILE, "mode": "signatures"},
    "explain_node": {"name": "main"},
    "get_pr_impact": {"files": [SAMPLE_FILE]},
    "resolve_with_lsp": {
        "file_path": SAMPLE_FILE,
        "language": "rust",
        "line": 1,
        "character": 1,
        "request": "definition",
    },
    "agent_focus": {"name": "smoke-tester"},
    "agent_diary_read": {"name": "smoke-tester", "limit": 5},
    "get_graph_report": {"format": "markdown", "project_name": "smoke"},
    "get_god_nodes": {"limit": 5},
    "shortest_path": {"source": "main", "target": "init", "max_hops": 3},
    "query_graph": {
        "question": "what connects main to init?",
        "token_budget": 800,
        "max_depth": 2,
    },
    "find_function": {"name": "main"},
    "get_callers": {"function": "main"},
    "get_call_graph": {"function": "main", "depth": 1, "max_results": 5},
    "search_code": {"query": "main", "limit": 5},
    "concept_search": {"query": "main", "limit": 5},
    "generate_doc": {"file": SAMPLE_FILE},
    "find_large_functions": {"limit": 5, "min_lines": 100},
    "get_tested_by": {"file": SAMPLE_FILE},
    "get_files_for_doc": {"doc": "README.md"},
    "get_doc_structure": {},
    "get_traceability": {"element": SAMPLE_FILE},
    "get_doc_tree": {},
    "get_code_tree": {"limit": 20},
    "get_nav_graph": {},
    "find_route": {"from": "main", "to": "init"},
    "get_screen_args": {"screen": "main"},
    "get_nav_callers": {"screen": "main"},
    "kg_ontology_status": {},
    "get_architecture": {},
    "get_graph_schema": {},
    "find_dead_code": {"min_lines": 100},
    "find_tunnels": {"limit": 5},
    "check_consistency": {},
    "get_clusters": {"limit": 5},
    "semantic_search": {"query": "main", "limit": 5},
    "search_knowledge": {"query": "main", "limit": 5},
}


def rpc(method: str, params: dict[str, Any] | None = None) -> Any:
    body = {
        "jsonrpc": "2.0",
        "id": 1,
        "method": method,
        "params": params or {},
    }
    req = urllib.request.Request(
        MCP_URL,
        data=json.dumps(body).encode(),
        headers={"Content-Type": "application/json", "Accept": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=180) as resp:
        payload = json.loads(resp.read().decode())
    if "error" in payload:
        raise RuntimeError(json.dumps(payload["error"])[:300])
    return payload.get("result")


def list_tools() -> list[str]:
    result = rpc("tools/list")
    tools = result.get("tools") if isinstance(result, dict) else result
    if not isinstance(tools, list):
        raise RuntimeError(f"unexpected tools/list shape: {result!r}"[:200])
    names = []
    for t in tools:
        if isinstance(t, dict) and "name" in t:
            names.append(t["name"])
    return sorted(set(names))


def call_tool(name: str, args: dict[str, Any]) -> str:
    merged = dict(args)
    if "project" not in merged:
        merged["project"] = PROJECT
    result = rpc("tools/call", {"name": name, "arguments": merged})
    # MCP content wrappers vary; stringify briefly.
    return json.dumps(result)[:160]


def main() -> int:
    try:
        tools = list_tools()
    except Exception as exc:
        print(f"FAILED to tools/list from {MCP_URL}: {exc}", file=sys.stderr)
        return 2

    print(f"MCP URL     : {MCP_URL}")
    print(f"project     : {PROJECT}")
    print(f"tools/list  : {len(tools)}")
    print(f"include_heavy: {INCLUDE_HEAVY}")
    print()

    results: list[tuple[str, str, str]] = []
    for name in tools:
        if name in MUTATING:
            results.append((name, "SKIP", "mutating"))
            continue
        if name in MEGA_GRAPH_HEAVY and not INCLUDE_HEAVY:
            results.append((name, "SKIP", "mega-graph-heavy (set LEANKG_SMOKE_INCLUDE_HEAVY=1)"))
            continue

        args = DEFAULT_ARGS.get(name, {})
        # Tools without curated args still get a best-effort empty call.
        t0 = time.time()
        try:
            info = call_tool(name, args)
            results.append((name, "PASS", f"{info} ({time.time() - t0:.1f}s)"))
        except Exception as exc:
            # agent_focus needs a persona fixture — treat missing persona as soft fail note
            msg = str(exc)
            if name == "agent_focus" and "not found" in msg:
                results.append(
                    (
                        name,
                        "FAIL",
                        f"fixture missing (create .leankg/agents/<name>.json): {msg[:120]}",
                    )
                )
            else:
                results.append((name, "FAIL", f"{msg[:180]} ({time.time() - t0:.1f}s)"))

    passed = sum(1 for _, s, _ in results if s == "PASS")
    failed = sum(1 for _, s, _ in results if s == "FAIL")
    skipped = sum(1 for _, s, _ in results if s == "SKIP")
    print(f"Passed : {passed}")
    print(f"Failed : {failed}")
    print(f"Skipped: {skipped}")
    print()
    for name, status, info in results:
        print(f"[{status:4}] {name:28} {info}")

    # Registry drift: tools in DEFAULT_ARGS but not listed
    unknown = sorted(set(DEFAULT_ARGS) - set(tools))
    if unknown:
        print()
        print(f"NOTE: DEFAULT_ARGS has tools not in tools/list: {unknown}")

    return 0 if failed == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
