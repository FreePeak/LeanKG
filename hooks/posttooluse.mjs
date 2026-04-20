#!/usr/bin/env node
/**
 * PostToolUse hook for LeanKG session continuity.
 *
 * Captures LeanKG MCP tool calls for session continuity.
 * For minimal prototype - logs to file (can be extended to SQLite later).
 */

import { readFileSync, appendFileSync, existsSync, mkdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { homedir } from "node:os";

const HOOK_DIR = dirname(fileURLToPath(import.meta.url));

async function readStdin() {
  return new Promise((resolve) => {
    let data = "";
    process.stdin.on("data", (chunk) => (data += chunk));
    process.stdin.on("end", () => resolve(data));
  });
}

// LeanKG tool prefix
const LEANKG_TOOLS = [
  "mcp__leankg__orchestrate",
  "mcp__leankg__search_code",
  "mcp__leankg__find_function",
  "mcp__leankg__query_file",
  "mcp__leankg__get_impact_radius",
  "mcp__leankg__get_dependencies",
  "mcp__leankg__get_dependents",
  "mcp__leankg__get_context",
  "mcp__leankg__get_callers",
  "mcp__leankg__get_call_graph",
  "mcp__leankg__get_clusters",
  "mcp__leankg__get_doc_for_file",
  "mcp__leankg__get_traceability",
  "mcp__leankg__get_tested_by",
  "mcp__leankg__detect_changes",
  "mcp__leankg__mcp_status",
  "mcp__leankg__mcp_index",
];

const SESSION_LOG_DIR = join(homedir(), ".leankg", "sessions");
const SESSION_LOG_FILE = join(SESSION_LOG_DIR, "posttooluse.log");

function getSessionId() {
  return process.env.CLAUDE_SESSION_ID || "unknown";
}

try {
  const raw = await readStdin();
  const input = JSON.parse(raw);

  const toolName = input.tool_name ?? "";
  const toolInput = input.tool_input ?? {};

  // Check if it's a LeanKG tool
  const isLeankgTool = LEANKG_TOOLS.some(t => toolName.includes(t));

  if (isLeankgTool) {
    // Ensure log directory exists
    if (!existsSync(SESSION_LOG_DIR)) {
      mkdirSync(SESSION_LOG_DIR, { recursive: true });
    }

    // Log the event
    const sessionId = getSessionId();
    const timestamp = new Date().toISOString();
    const logEntry = JSON.stringify({
      timestamp,
      sessionId,
      tool: toolName,
      input: toolInput,
    }) + "\n";

    appendFileSync(SESSION_LOG_FILE, logEntry);
  }
} catch {
  // Silent failure - never block the session
}

// PostToolUse hooks don't need hookSpecificOutput
