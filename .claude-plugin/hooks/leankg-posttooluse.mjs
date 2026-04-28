#!/usr/bin/env node
/**
 * LeanKG PostToolUse Hook
 * Logs tool usage for analytics and can provide follow-up LeanKG suggestions.
 */

import { readFileSync, appendFileSync, existsSync, mkdirSync } from "node:fs";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { homedir } from "node:os";

// ─── Configuration ───
const LOG_DIR = resolve(homedir(), ".cache", "leankg-hooks");
const LOG_FILE = resolve(LOG_DIR, "posttooluse.log");

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

// ─── Log tool usage ───
function logToolUsage(toolName, toolInput, toolResult, durationMs) {
  try {
    if (!existsSync(LOG_DIR)) {
      mkdirSync(LOG_DIR, { recursive: true });
    }

    const entry = {
      timestamp: new Date().toISOString(),
      tool: toolName,
      input: toolInput,
      durationMs,
      hadResult: !!toolResult,
      resultLength: toolResult ? JSON.stringify(toolResult).length : 0,
    };

    appendFileSync(LOG_FILE, JSON.stringify(entry) + "\n");
  } catch {
    // Silently ignore logging errors
  }
}

// ─── Main ───
async function main() {
  try {
    const raw = await readStdin();
    if (!raw.trim()) {
      process.exit(0);
    }

    const input = JSON.parse(raw);
    const toolName = input.tool_name || "";
    const toolInput = input.tool_input || {};
    const toolResult = input.result || null;
    const durationMs = input.duration_ms || 0;

    // Log for analytics
    logToolUsage(toolName, toolInput, toolResult, durationMs);

    process.exit(0);
  } catch {
    // Graceful degradation
    process.exit(0);
  }
}

main();
