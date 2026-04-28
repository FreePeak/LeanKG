#!/usr/bin/env node
/**
 * LeanKG PreToolUse Hook
 * Provides LeanKG context when code search is detected.
 * Only blocks Bash commands that use raw grep/find.
 */
import { spawnSync } from "node:child_process";

// ─── LeanKG Tools Mapping ───
const LEANKG_TOOLS = {
  search_code: "Search code by name/type",
  find_function: "Locate function definitions",
  query_file: "Find files by name/pattern",
  get_impact_radius: "Calculate blast radius",
  get_dependencies: "Get direct imports",
  get_dependents: "Get files depending on target",
  get_context: "Get AI-optimized file context",
  get_tested_by: "Get test coverage",
  get_call_graph: "Get function call graph",
  get_callers: "Get who calls a function",
};

// ─── Read stdin ───
function readStdin() {
  return new Promise((resolve, reject) => {
    let data = "";
    process.stdin.on("readable", () => {
      let chunk;
      while ((chunk = process.stdin.read()) !== null) {
        data += chunk;
      }
    });
    process.stdin.on("end", () => resolve(data));
    process.stdin.on("error", reject);
  });
}

// ─── Check if LeanKG is available ───
function isLeanKGMCPReady() {
  try {
    const result = spawnSync("cargo", ["run", "--release", "--", "status"], {
      cwd: process.cwd(),
      timeout: 5000,
    });
    return result.status === 0;
  } catch {
    return false;
  }
}

// ─── Only block Bash with grep/find - allow other tools ───
function shouldBlockTool(toolName, toolInput) {
  if (toolName !== "Bash") return false;

  const cmd = (toolInput.command || "").toLowerCase();

  // Build commands always allowed
  const isBuildCmd = /^(cargo|npm|pnpm|yarn|go|make|cmake|rustc)/.test(cmd);
  if (isBuildCmd) return false;

  // Only block if using raw grep/find in bash
  const hasRawSearch = /\b(grep|rg|ag|ack|find|fd|fzf)\b/.test(cmd);
  const isLeankgCmd = cmd.includes("leankg");

  return hasRawSearch && !isLeankgCmd;
}

function buildGuidance(toolInput) {
  const cmd = toolInput.command || "";
  const match = cmd.match(/['"]([^'"]+)['"]/);
  const query = match ? match[1] : "";

  const toolsList = Object.entries(LEANKG_TOOLS)
    .map(([name, desc]) => `  - mcp__leankg__${name}: ${desc}`)
    .join("\n");

  return `LEANKG ENFORCEMENT: Raw search via Bash is blocked.

Use LeanKG MCP tools instead:
${toolsList}

REQUIRED WORKFLOW:
1. mcp__leankg__mcp_status → confirm LeanKG is ready
2. For code search: mcp__leankg__search_code("${query}") or mcp__leankg__find_function("${query}")

The original tool call: Bash(${JSON.stringify(toolInput)})`;
}

async function main() {
  try {
    const raw = await readStdin();
    if (!raw.trim()) process.exit(0);

    const input = JSON.parse(raw);
    const toolName = input.tool_name || "";
    const toolInput = input.tool_input || {};

    if (!shouldBlockTool(toolName, toolInput)) {
      process.exit(0);
    }

    const leanKGReady = isLeanKGMCPReady();
    if (!leanKGReady) process.exit(0);

    // LeanKG ready - block Bash search commands
    console.log(JSON.stringify({
      hookSpecificOutput: {
        hookEventName: "PreToolUse",
        permissionDecision: "deny",
        permissionDecisionReason: buildGuidance(toolInput),
      },
    }) + "\n");
    process.exit(0);
  } catch {
    process.exit(0);
  }
}

main();