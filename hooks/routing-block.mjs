/**
 * Shared routing block for LeanKG Claude Code hooks.
 * Single source of truth - imported by sessionstart.mjs and pretooluse.mjs.
 */

import { createToolNamer } from "./core/tool-naming.mjs";

// Factory function accepting tool namer
export function createRoutingBlock(t) {
  return `
<tool_selection_hierarchy>
  1. ORCHESTRATE: mcp__leankg__orchestrate(intent)
     - Natural language: "show me impact of changing function X"
     - Smart caching and flow optimization.

  2. CODE DISCOVERY: mcp__leankg__search_code(query, element_type)
     - Primary search. ONE call replaces many Grep/Bash find commands.
     - Searches functions, files, structs, classes by name/type.

  3. IMPACT ANALYSIS: mcp__leankg__get_impact_radius(file, depth)
     - Calculate blast radius BEFORE making changes.
     - depth=2 recommended for LLM context budgets.

  4. CONTEXT: mcp__leankg__get_context(file)
     - Get minimal token-optimized context for a file.

  5. DEPENDENCIES: mcp__leankg__get_dependencies(file) | mcp__leankg__get_dependents(file)
     - Direct imports (what this file uses / what uses this file).

  6. CALLERS: mcp__leankg__get_callers(function) | mcp__leankg__find_function(name)
     - Find who calls a function / locate function by name.

  7. DOCUMENTATION: mcp__leankg__get_doc_for_file(file) | mcp__leankg__get_traceability(element)
     - Link code to business requirements and docs.

  8. TESTING: mcp__leankg__get_tested_by(file) | mcp__leankg__detect_changes(scope)
     - Get test coverage / pre-commit risk analysis.
</tool_selection_hierarchy>

<forbidden_actions>
  - DO NOT use Grep for code search (use mcp__leankg__search_code instead)
  - DO NOT use Bash find/grep for file search (use mcp__leankg__query_file instead)
  - DO NOT manually trace dependencies (use mcp__leankg__get_impact_radius instead)
</forbidden_actions>
`;
}

// Backward compat - static export
const _t = createToolNamer("claude-code");
export const ROUTING_BLOCK = createRoutingBlock(_t);