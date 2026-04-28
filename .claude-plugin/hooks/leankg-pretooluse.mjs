#!/usr/bin/env node
/**
 * LeanKG PreToolUse Hook
 * ENFORCES LeanKG usage by denying raw code search tools when LeanKG is available.
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

// ─── Detect code search tools ───
const CODE_SEARCH_TOOLS = ["Grep", "Glob", "Read", "Bash", "Search"];
const SEARCH_COMMANDS = ["grep", "rg", "find", "fd", "fzf", "cat", "head", "tail", "less"];

function isCodeSearchTool(toolName, toolInput) {
  if (!CODE_SEARCH_TOOLS.includes(toolName)) return false;

  if (toolName === "Bash") {
    const cmd = (toolInput.command || "").toLowerCase();
    // Allow cargo/npm/etc build commands through - they're not code search
    const isBuildCmd = /^(cargo|npm|pnpm|yarn|go|make|cmake|rustc)/.test(cmd);
    if (isBuildCmd) return false;
    const isSearch = SEARCH_COMMANDS.some(c => cmd.includes(c));
    const isLeankgCmd = cmd.includes("leankg");
    return isSearch && !isLeankgCmd;
  }

  return true;
}

function buildGuidance(toolName, toolInput) {
  const toolsList = Object.entries(LEANKG_TOOLS)
    .map(([name, desc]) => `  - mcp__leankg__${name}: ${desc}`)
    .join("\n");

  let query = "";
  if (toolName === "Grep") query = toolInput?.pattern || "";
  else if (toolName === "Glob") query = toolInput?.pattern || "";
  else if (toolName === "Read") query = toolInput?.file_path || "";
  else if (toolName === "Bash") {
    const cmd = toolInput?.command || "";
    const match = cmd.match(/['"]([^'"]+)['"]/);
    query = match ? match[1] : cmd.split(" ").pop() || "";
  }

  return `LEANKG ENFORCEMENT: Raw tool ${toolName} is BLOCKED for code search.

Use LeanKG MCP tools INSTEAD:
${toolsList}

REQUIRED WORKFLOW:
1. mcp__leankg__mcp_status → confirm LeanKG is ready
2. For code search: mcp__leankg__search_code("${query}")
   or mcp__leankg__find_function("${query}")
3. For file content: mcp__leankg__get_context("<file_path>")
4. For tests: mcp__leankg__get_tested_by("<file_path>")

The original tool call: ${toolName}(${JSON.stringify(toolInput)})`;
}

async function main() {
  try {
    const raw = await readStdin();
    if (!raw.trim()) {
      process.exit(0);
    }

    const input = JSON.parse(raw);
    const toolName = input.tool_name || "";
    const toolInput = input.tool_input || {};

    // Check if this is a code search tool
    if (!isCodeSearchTool(toolName, toolInput)) {
      process.exit(0);
    }

    // Check if LeanKG is ready
    const leanKGReady = isLeanKGMCPReady();

    if (!leanKGReady) {
      // LeanKG not ready - allow with guidance
      console.log(JSON.stringify({
        hookSpecificOutput: {
          hookEventName: "PreToolUse",
          permissionDecision: "allow",
          additionalContext: "LeanKG is not ready. Initialize with: mcp__leankg__mcp_init path:\"./.leankg\"",
        },
      }) + "\n");
      process.exit(0);
    }

    // LeanKG ready - DENY raw tool
    const guidance = buildGuidance(toolName, toolInput);
    console.log(JSON.stringify({
      hookSpecificOutput: {
        hookEventName: "PreToolUse",
        permissionDecision: "deny",
        permissionDecisionReason: guidance,
      },
    }) + "\n");
    process.exit(0);
  } catch {
    process.exit(0);
  }
}

main();