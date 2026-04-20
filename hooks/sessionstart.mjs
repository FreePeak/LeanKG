#!/usr/bin/env node
/**
 * SessionStart hook for LeanKG
 *
 * Injects <tool_selection_hierarchy> at session start.
 */

import { readFileSync } from "node:fs";
import { dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { ROUTING_BLOCK } from "./routing-block.mjs";

const HOOK_DIR = dirname(fileURLToPath(import.meta.url));

async function readStdin() {
  return new Promise((resolve) => {
    let data = "";
    process.stdin.on("data", (chunk) => (data += chunk));
    process.stdin.on("end", () => resolve(data));
  });
}

const raw = await readStdin();
const input = JSON.parse(raw);
const source = input.source ?? "startup";

// Build output based on source
let additionalContext = ROUTING_BLOCK;

console.log(JSON.stringify({
  hookSpecificOutput: {
    hookEventName: "SessionStart",
    additionalContext,
  },
}));
