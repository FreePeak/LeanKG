#!/usr/bin/env node
/**
 * PreToolUse hook for LeanKG - Guidance Only
 *
 * Shows once-per-session nudges to use LeanKG tools.
 * Does NOT block or redirect - only provides guidance.
 */

import { readFileSync } from "node:fs";
import { dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { tmpdir } from "node:os";
import { resolve } from "node:path";
import { mkdirSync, openSync, closeSync, constants } from "node:fs";

const HOOK_DIR = dirname(fileURLToPath(import.meta.url));

async function readStdin() {
  return new Promise((resolve) => {
    let data = "";
    process.stdin.on("data", (chunk) => (data += chunk));
    process.stdin.on("end", () => resolve(data));
  });
}

// Guidance throttle: show each advisory type at most once per session
const _guidanceShown = new Set();
const _guidanceId = String(process.ppid);
const _guidanceDir = resolve(tmpdir(), `leankg-guidance-${_guidanceId}`);

function guidanceOnce(type, content) {
  if (_guidanceShown.has(type)) return null;

  try { mkdirSync(_guidanceDir, { recursive: true }); } catch {}

  const marker = resolve(_guidanceDir, type);
  try {
    const fd = openSync(marker, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY);
    closeSync(fd);
  } catch {
    _guidanceShown.add(type);
    return null;
  }

  _guidanceShown.add(type);
  return { action: "context", additionalContext: content };
}

const READ_GUIDANCE = `<context_guidance>
  <tip>
    For code analysis, use mcp__leankg__query_file or mcp__leankg__get_context instead of Read.
    LeanKG tools are token-optimized and track relationships.
  </tip>
</context_guidance>`;

const GREP_GUIDANCE = `<context_guidance>
  <tip>
    Use mcp__leankg__search_code instead of Grep for code search.
    Example: mcp__leankg__search_code(query: "function_name", element_type: "function")
  </tip>
</context_guidance>`;

const BASH_GUIDANCE = `<context_guidance>
  <tip>
    For file search, use mcp__leankg__query_file(pattern: "*.rs")
    For code search, use mcp__leankg__search_code(query: "pattern")
    For impact analysis, use mcp__leankg__get_impact_radius(file: "path", depth: 2)
  </tip>
</context_guidance>`;

const raw = await readStdin();
const input = JSON.parse(raw);
const tool = input.tool_name ?? "";
const toolInput = input.tool_input ?? {};

// Route based on tool
if (tool === "Read") {
  const response = guidanceOnce("read", READ_GUIDANCE);
  if (response) process.stdout.write(JSON.stringify(response) + "\n");
} else if (tool === "Grep") {
  const response = guidanceOnce("grep", GREP_GUIDANCE);
  if (response) process.stdout.write(JSON.stringify(response) + "\n");
} else if (tool === "Bash") {
  const command = toolInput.command ?? "";
  // Only nudge for find/grep patterns
  if (/\b(find|grep|rg|ag)\b/.test(command)) {
    const response = guidanceOnce("bash", BASH_GUIDANCE);
    if (response) process.stdout.write(JSON.stringify(response) + "\n");
  }
}
// All other tools - passthrough (no output)
