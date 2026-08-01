#!/usr/bin/env python3
"""MCP tools smoke harness for LeanKG HTTP MCP (default: http://localhost:9699/mcp).

Improvements vs ad-hoc /tmp harnesses:
  - Discovers tools from tools/list (no stale static-only catalog)
  - Labels skips as mutating vs mega-graph-heavy (honest reasons)
  - Includes query_graph (US-GF-03) with a small token_budget
  - Defaults project=/workspace (LeanKG itself); set LEANKG_SMOKE_PROJECT for others
  - Ontology gates (FR-A03): kg_self_test / kg_ontology_status / kg_trace_workflow
    / ontology_control(action=status) must PASS before Phase 1 can be called done
  - Routing gates (FR-A06): find_route / get_screen_args / get_nav_callers must
    either PASS or refuse predictably (mega-graph guard) — a hard tool error fails
  - run_raw_query recipe fixtures (FR-B50): a validated Datalog recipe per
    relation/schema; each must return a result (count or rows)
  - --check-only-ontology exits after the ontology+routing gates (CI-friendly,
    no full-registry walk, no heavy tools)

Usage:
  python3 scripts/mcp-smoke-tools.py
  python3 scripts/mcp-smoke-tools.py --check-only-ontology   # FR-A03/A06 gates only
  LEANKG_SMOKE_PROJECT=/workspace python3 scripts/mcp-smoke-tools.py
  LEANKG_SMOKE_INCLUDE_HEAVY=1 python3 scripts/mcp-smoke-tools.py   # needs mem_limit >= 10g on mega-graphs
"""

from __future__ import annotations

import json
import os
import sys
import time
import urllib.request
from typing import Any

MCP_URL = os.environ.get("LEANKG_SMOKE_URL", "http://localhost:9699/mcp")
PROJECT = os.environ.get("LEANKG_SMOKE_PROJECT", "/workspace")
INCLUDE_HEAVY = os.environ.get("LEANKG_SMOKE_INCLUDE_HEAVY", "0") == "1"
SAMPLE_FILE = os.environ.get("LEANKG_SMOKE_FILE", "src/main.rs")

# FR-A03: ontology must stay healthy after a sync. A failed kg_* gate means
# agents will see -32603 errors on the ontology layer, so these are hard gates.
# kg_trace_workflow uses a real workflow id from the repo's ontology YAML;
# override via LEANKG_SMOKE_WORKFLOW for other projects.
WORKFLOW_ID = os.environ.get("LEANKG_SMOKE_WORKFLOW", "leankg-index-and-query")

# FR-B50: >= 10 validated `run_raw_query` Datalog recipes. Each maps a
# project's CozoDB relation to a useful probe. See
# docs/guides/run-raw-query-recipes.md for the full catalogue with explanations.
RAW_QUERY_RECIPES: list[tuple[str, str]] = [
    # (recipe name, Datalog query)
    ("count_elements", "?[count(qualified_name)] := *code_elements{qualified_name}"),
    ("count_relationships", "?[count(source_qualified)] := *relationships{source_qualified}"),
    (
        "by_language",
        "?[language, count(qualified_name)] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer] :limit 15",
    ),
    (
        "by_element_type",
        "?[element_type, count(qualified_name)] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer] :limit 20",
    ),
    (
        "calls_edges",
        '?[source_qualified, target_qualified] := *relationships{source_qualified, target_qualified, rel_type}, rel_type = "calls" :limit 5',
    ),
    (
        "imports_edges",
        '?[source_qualified, target_qualified] := *relationships{source_qualified, target_qualified, rel_type}, rel_type = "imports" :limit 5',
    ),
    (
        "tested_by_edges",
        '?[count(source_qualified)] := *relationships{source_qualified, rel_type}, rel_type = "tested_by"',
    ),
    (
        "docs_elements",
        '?[qualified_name, name, file_path] := *code_elements{qualified_name, name, file_path, language}, language = "markdown" :limit 5',
    ),
    (
        "ontology_nodes",
        '?[count(qualified_name)] := *code_elements{qualified_name, file_path}, regex_matches(file_path, "ontology://")',
    ),
    (
        "knowledge_count",
        "?[count(id)] := *knowledge_entries{id}",
    ),
    (
        "vector_count",
        "?[count(qualified_name)] := *embedding_vectors{qualified_name, vector}",
    ),
    (
        "orphan_elements",
        "?[qualified_name] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer], not *relationships[source_qualified, target_qualified, rel_type, confidence, metadata, env] :limit 5",
    ),
    (
        "longest_functions",
        '?[qualified_name, name, line_end, lines] := *code_elements{qualified_name, name, line_start, line_end, element_type}, element_type = "function", lines = line_end - line_start, lines > 200 :limit 5',
    ),
    (
        "incident_count",
        "?[count(id)] := *incidents{id}",
    ),
]

# FR-A06: routing tools must either answer or refuse with a *predictable*
# guard (mega-graph full-scan cap). Anything else (schema error, missing
# required arg, DB error) fails the routing gate.
ROUTING_PROBES: list[tuple[str, dict[str, Any], str]] = [
    ("find_route", {"route": "/login"}, "route lookup"),
    ("get_screen_args", {"destination": "login"}, "screen args"),
    ("get_nav_callers", {"destination": "login"}, "nav callers"),
]

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
    "add_ontology_concept",
    "add_ontology_workflow",
    "delete_ontology_concept",
    "index_prd",
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
    "temporal_query",
    "timeline",
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
    # --- tools that require non-empty args (missing-required-param fails) ---
    "add_ontology_concept": {
        "name": "smoke_concept",
        "type_": "domain_entity",
        "description": "smoke test concept",
    },
    "add_ontology_workflow": {
        "name": "smoke_workflow",
        "description": "smoke test workflow",
        "steps": [{"name": "step_one"}],
    },
    "delete_ontology_concept": {"gid": "smoke-non-existent-gid"},
    "embed_control": {"action": "status"},
    "ontology_control": {"action": "status"},
    "find_route": {"route": "/login"},
    "get_screen_args": {"destination": "login"},
    "get_nav_callers": {"destination": "login"},
    "get_feature_flow": {"feature_id": "FR-A01"},
    "load_layer": {"layer": "L0"},
    "index_prd": {"source_doc": "README.md"},
    "ctx_read": {"file": "AGENTS.md", "mode": "signatures"},
    "orchestrate": {"intent": "show AGENTS.md context", "mode": "adaptive"},
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


def call_tool_raw(name: str, args: dict[str, Any]) -> Any:
    """Call a tool and return the full JSON-RPC payload (dict or error)."""
    merged = dict(args)
    if "project" not in merged:
        merged["project"] = PROJECT
    return rpc("tools/call", {"name": name, "arguments": merged})


def _payload_text(payload: Any) -> str:
    try:
        content = payload.get("content", [])
        if isinstance(content, list) and content:
            return str(content[0].get("text", ""))
    except Exception:
        pass
    return json.dumps(payload)[:300]


# ---------------------------------------------------------------------------
# FR-A03 / FR-A06 gates: ontology + routing must pass before Phase 1 done.
# These run independently of the full-registry walk so CI can gate on them
# alone (--check-only-ontology) without paying for heavy tools.
# ---------------------------------------------------------------------------

def run_ontology_gates() -> list[tuple[str, str, str]]:
    results: list[tuple[str, str, str]] = []

    # Hard gate: every kg_* tool self-tests clean (all_ok: true).
    try:
        payload = call_tool_raw("kg_self_test", {})
        text = _payload_text(payload)
        if "error" in payload:
            results.append(("kg_self_test", "FAIL", f"rpc error: {payload['error']}"))
        elif '"all_ok": true' in text or "all_ok: true" in text:
            results.append(("kg_self_test", "PASS", "all_ok: true"))
        else:
            results.append(("kg_self_test", "FAIL", f"all_ok missing/false: {text[:200]}"))
    except Exception as exc:
        results.append(("kg_self_test", "FAIL", str(exc)[:160]))

    # Ontology status: concept + procedural counts present and >= 0.
    try:
        payload = call_tool_raw("kg_ontology_status", {})
        text = _payload_text(payload)
        if "error" in payload:
            results.append(("kg_ontology_status", "FAIL", f"rpc error: {payload['error']}"))
        elif "procedural_counts" in text and "concept_counts" in text:
            results.append(("kg_ontology_status", "PASS", "concept+procedural counts present"))
        else:
            results.append(("kg_ontology_status", "FAIL", f"missing counts: {text[:200]}"))
    except Exception as exc:
        results.append(("kg_ontology_status", "FAIL", str(exc)[:160]))

    # Workflow trace: real workflow id -> ordered steps (FR-A03 post-sync).
    try:
        payload = call_tool_raw(
            "kg_trace_workflow", {"workflow_id_or_query": WORKFLOW_ID}
        )
        text = _payload_text(payload)
        if "error" in payload:
            results.append(("kg_trace_workflow", "FAIL", f"rpc error: {payload['error']}"))
        elif "step_count" in text and "steps" in text:
            results.append(
                ("kg_trace_workflow", "PASS", f"workflow={WORKFLOW_ID} traceable")
            )
        else:
            results.append(("kg_trace_workflow", "FAIL", f"no steps: {text[:200]}"))
    except Exception as exc:
        results.append(("kg_trace_workflow", "FAIL", str(exc)[:160]))

    # ontology_control(status): YAML mtimes + marker + counts (FR-ONT-PROC-03).
    try:
        payload = call_tool_raw("ontology_control", {"action": "status"})
        text = _payload_text(payload)
        if "error" in payload:
            results.append(("ontology_control(status)", "FAIL", f"rpc error: {payload['error']}"))
        elif "concepts_yaml" in text or "concept_counts" in text:
            results.append(("ontology_control(status)", "PASS", "sync status readable"))
        else:
            results.append(("ontology_control(status)", "FAIL", f"odd status: {text[:200]}"))
    except Exception as exc:
        results.append(("ontology_control(status)", "FAIL", str(exc)[:160]))

    return results


def run_routing_gates() -> list[tuple[str, str, str]]:
    results: list[tuple[str, str, str]] = []
    for name, args, label in ROUTING_PROBES:
        try:
            payload = call_tool_raw(name, args)
            text = _payload_text(payload)
            if "error" in payload:
                results.append((name, "FAIL", f"rpc error: {payload['error']}"))
            elif "refused" in text and "element_count" in text:
                # Mega-graph full-scan guard is a *predictable* refuse — the
                # tool answers, it does not error. Acceptable for Phase 1.
                results.append((name, "PASS", f"guard-refuse ({label}): {text[:120]}"))
            elif "status: ok" in text:
                results.append((name, "PASS", f"{label} answered"))
            else:
                results.append((name, "FAIL", f"unexpected: {text[:160]}"))
        except Exception as exc:
            results.append((name, "FAIL", str(exc)[:160]))
    return results


def run_raw_query_recipes() -> list[tuple[str, str, str]]:
    results: list[tuple[str, str, str]] = []
    for name, query in RAW_QUERY_RECIPES:
        try:
            payload = call_tool_raw("run_raw_query", {"query": query})
            text = _payload_text(payload)
            if "error" in payload:
                results.append((name, "FAIL", f"rpc error: {payload['error']}"))
            elif "rows" in text or "count(" in text or "headers" in text:
                results.append((name, "PASS", "returned rows/count"))
            else:
                results.append((name, "FAIL", f"no rows: {text[:160]}"))
        except Exception as exc:
            results.append((name, "FAIL", str(exc)[:160]))
    return results


def print_gate_section(title: str, results: list[tuple[str, str, str]]) -> None:
    passed = sum(1 for _, s, _ in results if s == "PASS")
    print(f"\n--- {title} ({passed}/{len(results)} passed) ---")
    for name, status, info in results:
        print(f"[{status:4}] {name:28} {info}")
    return passed == len(results)


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

    # FR-A03 + FR-A06: ontology + routing gates (hard gates, always run).
    ontology_results = run_ontology_gates()
    routing_results = run_routing_gates()
    ontology_ok = print_gate_section(
        "FR-A03 ontology gates (kg_* after sync)", ontology_results
    )
    routing_ok = print_gate_section("FR-A06 routing gates", routing_results)

    # FR-B50: validated run_raw_query recipes (>= 10).
    recipe_results = run_raw_query_recipes()
    recipes_ok = print_gate_section(
        f"FR-B50 run_raw_query recipes ({len(recipe_results)} >= 10 required)",
        recipe_results,
    )
    if len(recipe_results) < 10:
        print(
            f"[FAIL] FR-B50 requires >= 10 recipes, only {len(recipe_results)} defined",
            file=sys.stderr,
        )
        recipes_ok = False

    if not (ontology_ok and routing_ok and recipes_ok):
        return 1

    if "--check-only-ontology" in sys.argv:
        print(
            "\nOntology + routing + recipes gates PASS — full registry walk skipped "
            "(--check-only-ontology)."
        )
        return 0

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
    print(f"\nRegistry walk  : {passed} passed, {failed} failed, {skipped} skipped")
    print(f"FR-A03 ontology: {'PASS' if ontology_ok else 'FAIL'}")
    print(f"FR-A06 routing : {'PASS' if routing_ok else 'FAIL'}")
    print(f"FR-B50 recipes : {'PASS' if recipes_ok else 'FAIL'} ({len(recipe_results)})")
    print()
    for name, status, info in results:
        print(f"[{status:4}] {name:28} {info}")

    # Registry drift: tools in DEFAULT_ARGS but not listed
    unknown = sorted(set(DEFAULT_ARGS) - set(tools))
    if unknown:
        print()
        print(f"NOTE: DEFAULT_ARGS has tools not in tools/list: {unknown}")

    return 0 if (failed == 0 and ontology_ok and routing_ok and recipes_ok) else 1


if __name__ == "__main__":
    sys.exit(main())
