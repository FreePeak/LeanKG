#!/bin/bash
set -e

REPO="FreePeak/LeanKG"
BINARY_NAME="leankg"
INSTALL_DIR="$HOME/.local/bin"
GITHUB_RAW="https://raw.githubusercontent.com/$REPO/main"
GITHUB_API="https://api.github.com/repos/$REPO/releases/latest"

INSTRUCTIONS_DIR="${GITHUB_RAW}/instructions"

CLAUDE_TEMPLATE_URL="${INSTRUCTIONS_DIR}/claude-template.md"
AGENTS_TEMPLATE_URL="${INSTRUCTIONS_DIR}/agents-template.md"

usage() {
    cat <<EOF
LeanKG Installer/Updater

Usage: curl -fsSL $GITHUB_RAW/scripts/install.sh | bash -s -- <command>

Commands:
  opencode      Install and configure LeanKG for OpenCode AI
  cursor        Install and configure LeanKG for Cursor AI
  claude        Install and configure LeanKG for Claude Code/Desktop
  gemini        Install and configure LeanKG for Gemini CLI
  kilo          Install and configure LeanKG for Kilo Code
  antigravity   Install and configure LeanKG for Anti Gravity
  update        Update LeanKG to the latest version
  version       Show installed and latest available version

Examples:
  curl -fsSL $GITHUB_RAW/scripts/install.sh | bash -s -- opencode
  curl -fsSL $GITHUB_RAW/scripts/install.sh | bash -s -- update
  curl -fsSL $GITHUB_RAW/scripts/install.sh | bash -s -- version
EOF
}

detect_platform() {
    local platform
    local arch

    case "$(uname -s)" in
        Darwin*)
            platform="macos"
            ;;
        Linux*)
            platform="linux"
            ;;
        *)
            echo "Unsupported platform: $(uname -s)" >&2
            exit 1
            ;;
    esac

    case "$(uname -m)" in
        x86_64)
            arch="x64"
            ;;
        arm64|aarch64)
            arch="arm64"
            ;;
        *)
            echo "Unsupported architecture: $(uname -m)" >&2
            exit 1
            ;;
    esac

    echo "${platform}-${arch}"
}

get_download_url() {
    local platform="$1"
    local version="$2"
    echo "https://github.com/$REPO/releases/download/v${version}/${BINARY_NAME}-${platform}.tar.gz"
}

get_installed_version() {
    local binary_path="${INSTALL_DIR}/${BINARY_NAME}"
    if [ -x "$binary_path" ]; then
        "$binary_path" --version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1 || echo "unknown"
    else
        echo "not installed"
    fi
}

get_latest_version() {
    curl -fsSL "$GITHUB_API" | grep -o '"tag_name": "[^"]*' | cut -d'"' -f4 | sed 's/v//'
}

check_for_updates() {
    local installed="$1"
    local latest="$2"

    if [ "$installed" = "not installed" ]; then
        echo "not installed"
        return 1
    fi

    if [ "$installed" != "$latest" ]; then
        echo "update available: $installed -> $latest"
        return 1
    else
        echo "up to date ($installed)"
        return 0
    fi
}

show_version() {
    local installed latest
    installed=$(get_installed_version)
    latest=$(get_latest_version)

    echo "LeanKG Version Check"
    echo "-------------------"
    echo "Installed: $installed"
    echo "Latest:    $latest"

    if [ "$installed" != "$latest" ] && [ "$installed" != "not installed" ]; then
        echo ""
        echo "A new version is available!"
        echo "Run 'curl -fsSL $GITHUB_RAW/scripts/install.sh | bash -s -- update' to upgrade."
    fi
}

update_binary() {
    local platform="$1"
    local installed latest

    installed=$(get_installed_version)
    latest=$(get_latest_version)

    echo "Checking for updates..."
    echo "Current: $installed"
    echo "Latest:  $latest"

    if [ "$installed" = "$latest" ]; then
        echo ""
        echo "You already have the latest version ($latest)."
        return 0
    fi

    echo ""
    echo "Stopping any running LeanKG processes..."
    pkill -f "leankg" 2>/dev/null || true
    sleep 1

    echo "Updating LeanKG for ${platform}..."

    local url
    url=$(get_download_url "$platform" "$latest")

    echo "Downloading from $url..."

    local tmp_dir
    tmp_dir=$(mktemp -d)
    local tar_path="$tmp_dir/binary.tar.gz"

    cleanup() {
        rm -rf "$tmp_dir"
    }
    trap cleanup EXIT

    curl -fsSL -o "$tar_path" "$url"

    mkdir -p "$INSTALL_DIR"

    # Remove existing binary first to avoid APFS metadata corruption issues
    # (overwriting in place can leave corrupted metadata, causing SIGKILL on exec)
    rm -f "${INSTALL_DIR}/${BINARY_NAME}"

    tar -xzf "$tar_path" -C "$INSTALL_DIR"
    chmod +x "${INSTALL_DIR}/${BINARY_NAME}"

    echo ""
    echo "Updated to v$latest"
    echo "Installed to ${INSTALL_DIR}/${BINARY_NAME}"
}

install_binary() {
    local platform="$1"
    local install_type="$2"

    local installed latest
    installed=$(get_installed_version)
    latest=$(get_latest_version)

    if [ "$installed" = "$latest" ]; then
        echo "LeanKG v$latest is already installed."
        return 0
    fi

    echo "Installing LeanKG for ${platform}..."

    local url
    url=$(get_download_url "$platform" "$latest")

    echo "Downloading v$latest from $url..."

    local tmp_dir
    tmp_dir=$(mktemp -d)
    local tar_path="$tmp_dir/binary.tar.gz"

    cleanup() {
        rm -rf "$tmp_dir"
    }
    trap cleanup EXIT

    curl -fsSL -o "$tar_path" "$url"

    mkdir -p "$INSTALL_DIR"

    # Remove existing binary first to avoid APFS metadata corruption issues
    rm -f "${INSTALL_DIR}/${BINARY_NAME}"

    tar -xzf "$tar_path" -C "$INSTALL_DIR"
    chmod +x "${INSTALL_DIR}/${BINARY_NAME}"

    echo "Installed v$latest to ${INSTALL_DIR}/${BINARY_NAME}"

    if [ "$install_type" = "full" ]; then
        echo "Adding ${INSTALL_DIR} to PATH..."
        if [ -d "$INSTALL_DIR" ] && [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
            echo "Add this to your shell profile if needed:"
            echo "  export PATH=\"\$HOME/.local/bin:\$PATH\""
        fi
    fi
}

configure_opencode() {
    local config_dir="${XDG_CONFIG_HOME:-$HOME/.config}/opencode"
    local config_file="$config_dir/opencode.json"
    local leankg_path="${INSTALL_DIR}/${BINARY_NAME}"

    mkdir -p "$config_dir"

    local has_mcp=false
    local has_plugin=false

    if [ -f "$config_file" ]; then
        if jq -e '.mcp.leankg' "$config_file" > /dev/null 2>&1; then
            echo "LeanKG MCP already configured in OpenCode"
            has_mcp=true
        fi
        if jq -e '.plugin | contains(["leankg@git"])' "$config_file" > /dev/null 2>&1; then
            echo "LeanKG plugin already in OpenCode"
            has_plugin=true
        fi
    else
        echo '{"$schema":"https://opencode.ai/config.json","plugin":[],"mcp":{}}' > "$config_file"
    fi

    local tmp_file
    tmp_file=$(mktemp)

    if [ "$has_mcp" = false ]; then
        jq --arg leankg "$leankg_path" '.mcp.leankg = {"type": "local", "command": [$leankg, "mcp-stdio", "--watch"], "enabled": true}' "$config_file" > "$tmp_file" && mv "$tmp_file" "$config_file"
    fi

    if [ "$has_plugin" = false ]; then
        jq '.plugin += ["leankg@git+https://github.com/FreePeak/LeanKG.git"]' "$config_file" > "$tmp_file" && mv "$tmp_file" "$config_file"
    fi

    echo "Configured LeanKG plugin and MCP for OpenCode at $config_file"
}

configure_cursor() {
    local config_dir="$HOME/.cursor"
    local config_file="$config_dir/mcp.json"
    local leankg_path="${INSTALL_DIR}/${BINARY_NAME}"
    local needs_update=false

    mkdir -p "$config_dir"

    if [ -f "$config_file" ]; then
        local current_path
        current_path=$(jq -r '.mcpServers.leankg.command // empty' "$config_file" 2>/dev/null)
        local current_args
        current_args=$(jq -r '.mcpServers.leankg.args // [] | join(" ")' "$config_file" 2>/dev/null)
        
        if [ -n "$current_path" ]; then
            if [ "$current_path" != "$leankg_path" ]; then
                echo "Updating LeanKG binary path for Cursor: $current_path -> $leankg_path"
                needs_update=true
            fi
            if ! echo "$current_args" | grep -q "\-\-watch"; then
                echo "Adding --watch flag to LeanKG for Cursor"
                needs_update=true
            fi
        fi
        
        if [ "$needs_update" = false ]; then
            echo "LeanKG already properly configured in Cursor"
            return
        fi
        
        local tmp_file
        tmp_file=$(mktemp)
        cat "$config_file" | jq --arg leankg "$leankg_path" '.mcpServers.leankg = {"command": $leankg, "args": ["mcp-stdio", "--watch"]}' > "$tmp_file"
        mv "$tmp_file" "$config_file"
    else
        echo "{\"mcpServers\": {\"leankg\": {\"command\": \"$leankg_path\", \"args\": [\"mcp-stdio\", \"--watch\"]}}}" > "$config_file"
    fi
    echo "Configured LeanKG for Cursor at $config_file"
}

configure_claude() {
    local config_file="$HOME/.claude.json"
    local leankg_path="${INSTALL_DIR}/${BINARY_NAME}"
    local needs_update=false

    if [ -f "$config_file" ] && [ -s "$config_file" ]; then
        local current_path
        current_path=$(jq -r '.mcpServers.leankg.command // empty' "$config_file" 2>/dev/null)
        local current_args
        current_args=$(jq -r '.mcpServers.leankg.args // [] | join(" ")' "$config_file" 2>/dev/null)
        
        if [ -z "$current_path" ]; then
            needs_update=true
        else
            if [ "$current_path" != "$leankg_path" ]; then
                echo "Updating LeanKG binary path for Claude Code: $current_path -> $leankg_path"
                needs_update=true
            fi
            if ! echo "$current_args" | grep -q "\-\-watch"; then
                echo "Adding --watch flag to LeanKG for Claude Code"
                needs_update=true
            fi
        fi
        
        if [ "$needs_update" = false ]; then
            echo "LeanKG already properly configured in Claude Code"
            return
        fi
    else
        needs_update=true
    fi

    local tmp_file
    tmp_file=$(mktemp)
    if [ -f "$config_file" ] && [ "$needs_update" = true ]; then
        cat "$config_file" | jq --arg leankg "$leankg_path" '.mcpServers.leankg = {"type": "stdio", "command": $leankg, "args": ["mcp-stdio", "--watch"]}' > "$tmp_file" && mv "$tmp_file" "$config_file"
    else
        cat > "$tmp_file" <<EOF
{
  "mcpServers": {
    "leankg": {
      "type": "stdio",
      "command": "$leankg_path",
      "args": ["mcp-stdio", "--watch"]
    }
  }
}
EOF
        mv "$tmp_file" "$config_file"
    fi
    echo "Configured LeanKG for Claude Code at $config_file"
}

remove_old_skill() {
    local skill_dir="$HOME/.claude/skills/using-leankg"
    if [ -d "$skill_dir" ]; then
        rm -rf "$skill_dir"
        echo "Removed old LeanKG skill from $skill_dir"
    fi
}

setup_claude_hooks() {
    local plugin_dir="$HOME/.claude/plugins/leankg"
    local hooks_installed=false

    mkdir -p "$plugin_dir/hooks"

    # Write hooks.json with all Claude-Mem-equivalent hooks
    # Setup, SessionStart, UserPromptSubmit, PreToolUse, PostToolUse, Stop
    cat > "$plugin_dir/hooks/hooks.json" <<'EOF'
{
  "description": "LeanKG hooks - enforces LeanKG usage and provides Claude-Mem-like session management",
  "hooks": {
    "Setup": [
      {
        "matcher": "*",
        "hooks": [
          {
            "type": "command",
            "command": "node \"${CLAUDE_PLUGIN_ROOT}/hooks/version-check.mjs\""
          }
        ]
      }
    ],
    "SessionStart": [
      {
        "matcher": "*",
        "hooks": [
          {
            "type": "command",
            "command": "node \"${CLAUDE_PLUGIN_ROOT}/hooks/sessionstart.mjs\""
          }
        ]
      }
    ],
    "UserPromptSubmit": [
      {
        "matcher": "*",
        "hooks": [
          {
            "type": "command",
            "command": "node \"${CLAUDE_PLUGIN_ROOT}/hooks/session-init.mjs\""
          }
        ]
      }
    ],
    "PreToolUse": [
      {
        "matcher": "*",
        "hooks": [
          {
            "type": "command",
            "command": "node \"${CLAUDE_PLUGIN_ROOT}/hooks/leankg-pretooluse.mjs\""
          }
        ]
      }
    ],
    "PostToolUse": [
      {
        "matcher": "mcp__leankg__*",
        "hooks": [
          {
            "type": "command",
            "command": "node \"${CLAUDE_PLUGIN_ROOT}/hooks/leankg-posttooluse.mjs\""
          }
        ]
      }
    ],
    "Stop": [
      {
        "matcher": "*",
        "hooks": [
          {
            "type": "command",
            "command": "node \"${CLAUDE_PLUGIN_ROOT}/hooks/summarize.mjs\""
          }
        ]
      }
    ]
  }
}
EOF
    hooks_installed=true

    # Write hook scripts
    cat > "$plugin_dir/hooks/sessionstart.mjs" <<'HOOKEOF'
#!/usr/bin/env node
/**
 * SessionStart hook for LeanKG
 * Injects <tool_selection_hierarchy> at session start.
 */
import { dirname } from "node:path";
import { fileURLToPath } from "node:url";

async function readStdin() {
  return new Promise((resolve) => {
    let data = "";
    process.stdin.on("data", (chunk) => (data += chunk));
    process.stdin.on("end", () => resolve(data));
  });
}

const raw = await readStdin();
const input = JSON.parse(raw);

const ROUTING_BLOCK = `
<tool_selection_hierarchy>
  1. ORCHESTRATE: mcp__leankg__orchestrate(intent)
     - Natural language: "show me impact of changing function X"

  2. CODE DISCOVERY: mcp__leankg__search_code(query, element_type)
     - Primary search. ONE call replaces many Grep/Bash commands.

  3. IMPACT ANALYSIS: mcp__leankg__get_impact_radius(file, depth)
     - Calculate blast radius BEFORE making changes.

  4. CONTEXT: mcp__leankg__get_context(file)
     - Get minimal token-optimized context for a file.

  5. DEPENDENCIES: mcp__leankg__get_dependencies(file) | mcp__leankg__get_dependents(file)

  6. CALLERS: mcp__leankg__get_callers(function) | mcp__leankg__find_function(name)

  7. DOCUMENTATION: mcp__leankg__get_doc_for_file(file) | mcp__leankg__get_traceability(element)

  8. TESTING: mcp__leankg__get_tested_by(file) | mcp__leankg__detect_changes(scope)
</tool_selection_hierarchy>

<forbidden_actions>
  - DO NOT use Grep for code search (use mcp__leankg__search_code instead)
  - DO NOT use Bash find/grep for file search (use mcp__leankg__query_file instead)
</forbidden_actions>
`;

console.log(JSON.stringify({
  hookSpecificOutput: {
    hookEventName: "SessionStart",
    additionalContext: ROUTING_BLOCK,
  },
}));
HOOKEOF

    cat > "$plugin_dir/hooks/pretooluse.mjs" <<'HOOKEOF'
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
HOOKEOF

    cat > "$plugin_dir/hooks/posttooluse.mjs" <<'HOOKEOF'
#!/usr/bin/env node
/**
 * PostToolUse hook for LeanKG
 * Tracks LeanKG MCP tool usage and logs for analytics.
 */
import { appendFileSync, existsSync, mkdirSync } from "node:fs";
import { join } from "node:path";
import { homedir } from "node:os";

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
const SESSION_LOG_FILE = join(SESSION_LOG_DIR, "tool-usage.log");

async function readStdin() {
  return new Promise((resolve) => {
    let data = "";
    process.stdin.on("data", (chunk) => (data += chunk));
    process.stdin.on("end", () => resolve(data));
  });
}

async function main() {
  try {
    const raw = await readStdin();
    if (!raw.trim()) {
      process.exit(0);
    }

    const input = JSON.parse(raw);
    const toolName = input.tool_name ?? "";
    const toolInput = input.tool_input ?? {};

    const isLeankgTool = LEANKG_TOOLS.some(t => toolName.includes(t));

    if (isLeankgTool) {
      if (!existsSync(SESSION_LOG_DIR)) {
        mkdirSync(SESSION_LOG_DIR, { recursive: true });
      }
      const sessionId = process.env.CLAUDE_SESSION_ID || "unknown";
      const timestamp = new Date().toISOString();
      const logEntry = JSON.stringify({
        timestamp,
        sessionId,
        tool: toolName,
        input: toolInput,
      }) + "\n";
      appendFileSync(SESSION_LOG_FILE, logEntry);
    }
  } catch { /* silent */ }
}

main();
HOOKEOF

    cat > "$plugin_dir/hooks/version-check.mjs" <<'HOOKEOF'
#!/usr/bin/env node
/**
 * Setup hook for LeanKG - version check
 * Runs at plugin startup to verify version requirements
 */
import { readFileSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

const __dirname = dirname(fileURLToPath(import.meta.url));
const PLUGIN_ROOT = process.env.CLAUDE_PLUGIN_ROOT || __dirname;
const MIN_VERSION = "0.16.0";

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

function getInstalledVersion() {
  try {
    const cargoPath = join(PLUGIN_ROOT, "..", "..", "Cargo.toml");
    const content = readFileSync(cargoPath, "utf-8");
    const match = content.match(/version\s*=\s*"([^"]+)"/);
    return match ? match[1] : null;
  } catch {
    return null;
  }
}

function compareVersions(a, b) {
  const partsA = a.split(".").map(Number);
  const partsB = b.split(".").map(Number);
  for (let i = 0; i < Math.max(partsA.length, partsB.length); i++) {
    const pA = partsA[i] || 0;
    const pB = partsB[i] || 0;
    if (pA > pB) return 1;
    if (pA < pB) return -1;
  }
  return 0;
}

async function main() {
  try {
    const raw = await readStdin();
    if (!raw.trim()) process.exit(0);

    const input = JSON.parse(raw);
    const hookName = input.hookName || input.hook_event_name || "Setup";
    if (hookName !== "Setup") process.exit(0);

    const installedVersion = getInstalledVersion();
    if (!installedVersion) {
      console.log(JSON.stringify({
        hookSpecificOutput: {
          hookEventName: "Setup",
          versionCheck: "warning",
          message: "Could not determine LeanKG version. Version gating skipped.",
        },
      }));
      process.exit(0);
    }

    const versionOk = compareVersions(installedVersion, MIN_VERSION) >= 0;
    console.log(JSON.stringify({
      hookSpecificOutput: {
        hookEventName: "Setup",
        versionCheck: versionOk ? "pass" : "fail",
        installedVersion,
        minimumVersion: MIN_VERSION,
        message: versionOk ? `LeanKG v${installedVersion} ready` : `LeanKG v${installedVersion} does not meet minimum v${MIN_VERSION}`,
      },
    }) + "\n");
  } catch (err) {
    console.error("Version check error:", err.message);
    process.exit(0);
  }
}

main();
HOOKEOF

    cat > "$plugin_dir/hooks/session-init.mjs" <<'HOOKEOF'
#!/usr/bin/env node
/**
 * UserPromptSubmit hook for LeanKG (session-init)
 * Initializes session context and injects LeanKG usage patterns
 */
import { existsSync } from "node:fs";
import { join } from "node:path";

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

function detectProjectType(cwd) {
  const indicators = [
    { pattern: "Cargo.toml", type: "rust", weight: 10 },
    { pattern: "package.json", type: "node", weight: 8 },
    { pattern: "go.mod", type: "go", weight: 9 },
    { pattern: "pyproject.toml", type: "python", weight: 7 },
    { pattern: "pom.xml", type: "java", weight: 7 },
  ];
  let bestType = "unknown";
  let bestScore = 0;
  for (const { pattern, type, weight } of indicators) {
    if (existsSync(join(cwd, pattern))) {
      if (weight > bestScore) {
        bestScore = weight;
        bestType = type;
      }
    }
  }
  return bestType;
}

function buildSessionContext(input) {
  const cwd = input.cwd || process.cwd();
  const projectType = detectProjectType(cwd);

  const context = `<tool_selection_hierarchy>
  1. ORCHESTRATE: mcp__leankg__orchestrate(intent)
  2. CODE DISCOVERY: mcp__leankg__search_code(query, element_type)
  3. IMPACT ANALYSIS: mcp__leankg__get_impact_radius(file, depth)
  4. CONTEXT: mcp__leankg__get_context(file)
  5. DEPENDENCIES: mcp__leankg__get_dependencies(file) | mcp__leankg__get_dependents(file)
  6. CALLERS: mcp__leankg__get_callers(function) | mcp__leankg__find_function(name)
  7. DOCUMENTATION: mcp__leankg__get_doc_for_file(file) | mcp__leankg__get_traceability(element)
  8. TESTING: mcp__leankg__get_tested_by(file) | mcp__leankg__detect_changes(scope)
</tool_selection_hierarchy>

<forbidden_actions>
  - DO NOT use Grep for code search (use mcp__leankg__search_code instead)
  - DO NOT use Bash find/grep for file search (use mcp__leankg__query_file instead)
</forbidden_actions>

<project_context>
Project type detected: ${projectType}
</project_context>

<leankg_reminder>
- Run: mcp__leankg__mcp_status to verify LeanKG is ready
- Run: mcp__leankg__mcp_index to index code if needed
</leankg_reminder>`;

  return context;
}

async function main() {
  try {
    const raw = await readStdin();
    if (!raw.trim()) process.exit(0);

    const input = JSON.parse(raw);
    if (!input.prompt && !input.user_prompt) process.exit(0);

    const sessionContext = buildSessionContext(input);
    console.log(JSON.stringify({
      hookSpecificOutput: {
        hookEventName: "UserPromptSubmit",
        additionalContext: sessionContext,
      },
    }) + "\n");
  } catch (err) {
    console.error("SessionInit error:", err.message);
    process.exit(0);
  }
}

main();
HOOKEOF

    cat > "$plugin_dir/hooks/summarize.mjs" <<'HOOKEOF'
#!/usr/bin/env node
/**
 * Stop hook for LeanKG (summarize)
 * Captures session summary and stores for future context injection
 */
import { writeFileSync, existsSync, mkdirSync } from "node:fs";
import { join } from "node:path";

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

function extractToolUsage(input) {
  const tools = [];
  if (input.tool_calls) {
    for (const call of input.tool_calls) {
      tools.push({ name: call.name || call.tool_name || "unknown" });
    }
  }
  if (input.tools_used) tools.push(...input.tools_used);
  if (input.mcp_tools) tools.push(...input.mcp_tools.map(t => ({ name: t })));
  return tools;
}

function generateSummary(input) {
  const timestamp = new Date().toISOString();
  const cwd = input.cwd || process.cwd() || "unknown";
  const tools = extractToolUsage(input);
  const mcpTools = tools.filter(t => t.name.startsWith("mcp__leankg__")).map(t => t.name.replace("mcp__leankg__", ""));
  const bashCommands = tools.filter(t => t.name === "Bash").length;

  return {
    timestamp,
    cwd,
    tools_used: { total: tools.length, mcp_leankg: mcpTools.length, bash_commands: bashCommands },
    leankg_tools: [...new Set(mcpTools)],
    session_duration_ms: input.duration_ms || 0,
  };
}

function saveSessionSummary(summary) {
  try {
    const SESSION_DIR = join(process.env.HOME || "~", ".cache", "leankg-hooks", "sessions");
    if (!existsSync(SESSION_DIR)) mkdirSync(SESSION_DIR, { recursive: true });
    const ts = new Date().getTime();
    const filepath = join(SESSION_DIR, `session-${ts}.json`);
    writeFileSync(filepath, JSON.stringify(summary, null, 2));
    const latestPath = join(SESSION_DIR, "latest.json");
    writeFileSync(latestPath, JSON.stringify(summary, null, 2));
    return filepath;
  } catch (err) {
    console.error("Failed to save session summary:", err.message);
    return null;
  }
}

async function main() {
  try {
    const raw = await readStdin();
    if (!raw.trim()) process.exit(0);

    const input = JSON.parse(raw);
    const hookName = input.hook_name || input.hook_event_name || "";
    if (hookName !== "Stop") process.exit(0);

    const summary = generateSummary(input);
    const savedPath = saveSessionSummary(summary);

    const toolList = summary.leankg_tools.length > 0
      ? summary.leankg_tools.map(t => `  - ${t}`).join("\n")
      : "none";

    const summaryText = `Session Summary:
- Working directory: ${summary.cwd}
- Total tools used: ${summary.tools_used.total}
- LeanKG MCP calls: ${summary.tools_used.mcp_leankg}
- Bash commands: ${summary.tools_used.bash_commands}
- LeanKG tools used: ${summary.leankg_tools.join(", ") || "none"}
- Saved to: ${savedPath}`;

    console.log(JSON.stringify({
      hookSpecificOutput: {
        hookEventName: "Stop",
        sessionSummary: summary,
        summaryText,
        savedTo: savedPath,
      },
    }) + "\n");
  } catch (err) {
    console.error("Summarize error:", err.message);
    process.exit(0);
  }
}

main();
HOOKEOF

    chmod +x "$plugin_dir/hooks/sessionstart.mjs" "$plugin_dir/hooks/pretooluse.mjs" "$plugin_dir/hooks/posttooluse.mjs" "$plugin_dir/hooks/version-check.mjs" "$plugin_dir/hooks/session-init.mjs" "$plugin_dir/hooks/summarize.mjs"
    
    if [ ! -f "$plugin_dir/leankg-bootstrap.md" ]; then
        cat > "$plugin_dir/leankg-bootstrap.md" <<'BOOTSTRAPEOF'
# LeanKG Bootstrap

LeanKG is a lightweight knowledge graph for codebase understanding.

**Auto-Activated Tools:**
- `mcp_status` - Check if LeanKG is initialized
- `mcp_init` - Initialize LeanKG for a project
- `mcp_index` - Index codebase
- `search_code` - Search code elements by name/type
- `find_function` - Locate function definitions
- `get_impact_radius` - Calculate blast radius of changes
- `get_dependencies` - Get direct imports of a file
- `get_dependents` - Get files depending on target
- `get_context` - Get AI-optimized context for a file
- `get_tested_by` - Get test coverage info
- `query_file` - Find files by name/pattern
- `get_call_graph` - Get function call chains
- `find_large_functions` - Find oversized functions
- `get_doc_for_file` - Get documentation for a file
- `get_traceability` - Get full traceability chain
- `get_code_tree` - Get codebase structure
- `get_clusters` - Get functional clusters
- `detect_changes` - Pre-commit risk analysis

**MANDATORY WORKFLOW: LeanKG First, Grep Fallback**

Before ANY codebase search/navigation, you MUST:

1. Check if LeanKG is available via `mcp_status`
2. If LeanKG is not initialized, run `mcp_init` first
3. Use LeanKG tools: `search_code`, `find_function`, `query_file`, `get_impact_radius`, etc.
4. **ONLY after LeanKG is exhausted (returns empty) may you fall back to grep/ripgrep**

| Instead of | Use LeanKG |
|------------|------------|
| grep/ripgrep for "where is X?" | `search_code` or `find_function` |
| glob + content search for tests | `get_tested_by` |
| Manual dependency tracing | `get_impact_radius` or `get_dependencies` |
| Reading entire files | `get_context` (token-optimized) |
BOOTSTRAPEOF
        echo "Created leankg-bootstrap.md for Claude Code"
    fi
    
    if [ "$hooks_installed" = true ]; then
        echo "Configured LeanKG hooks for Claude Code"
    else
        echo "LeanKG hooks already configured for Claude Code"
    fi
}

setup_cursor_hooks() {
    local plugin_dir="$HOME/.cursor/plugins/leankg"
    local hooks_installed=false
    
    if [ ! -d "$plugin_dir/hooks" ]; then
        mkdir -p "$plugin_dir/hooks"
        
        cat > "$plugin_dir/hooks/hooks.json" <<'EOF'
{
  "version": 1,
  "hooks": {
    "sessionStart": [
      {
        "command": "./hooks/session-start"
      }
    ]
  }
}
EOF

        cat > "$plugin_dir/hooks/session-start" <<'HOOKEOF'
#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PLUGIN_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
leankg_bootstrap_content=$(cat "${PLUGIN_ROOT}/leankg-bootstrap.md" 2>&1 || echo "")
escape_for_json() {
    local s="$1"
    s="${s//\\/\\\\}"
    s="${s//\"/\\\"}"
    s="${s//$'\n'/\\n}"
    s="${s//$'\r'/\\r}"
    s="${s//$'\t'/\\t}"
    printf '%s' "$s"
}
leankg_bootstrap_escaped=$(escape_for_json "$leankg_bootstrap_content")
session_context="<LEANKG_BOOTSTRAP>\n${leankg_bootstrap_escaped}\n</LEANKG_BOOTSTRAP>"
printf '{\n  "additional_context": "%s"\n}\n' "$session_context"
exit 0
HOOKEOF

        chmod +x "$plugin_dir/hooks/session-start"
        hooks_installed=true
    fi
    
    if [ ! -f "$plugin_dir/leankg-bootstrap.md" ]; then
        cat > "$plugin_dir/leankg-bootstrap.md" <<'BOOTSTRAPEOF'
# LeanKG Bootstrap

LeanKG is a lightweight knowledge graph for codebase understanding.

**Auto-Activated Tools:**
- `mcp_status` - Check if LeanKG is initialized
- `mcp_init` - Initialize LeanKG for a project
- `mcp_index` - Index codebase
- `search_code` - Search code elements by name/type
- `find_function` - Locate function definitions
- `get_impact_radius` - Calculate blast radius of changes
- `get_dependencies` - Get direct imports of a file
- `get_dependents` - Get files depending on target
- `get_context` - Get AI-optimized context for a file
- `get_tested_by` - Get test coverage info
- `query_file` - Find files by name/pattern
- `get_call_graph` - Get function call chains
- `find_large_functions` - Find oversized functions
- `get_doc_for_file` - Get documentation for a file
- `get_traceability` - Get full traceability chain
- `get_code_tree` - Get codebase structure
- `get_clusters` - Get functional clusters
- `detect_changes` - Pre-commit risk analysis

**MANDATORY WORKFLOW: LeanKG First, Grep Fallback**

Before ANY codebase search/navigation, you MUST:

1. Check if LeanKG is available via `mcp_status`
2. If LeanKG is not initialized, run `mcp_init` first
3. Use LeanKG tools: `search_code`, `find_function`, `query_file`, `get_impact_radius`, etc.
4. **ONLY after LeanKG is exhausted (returns empty) may you fall back to grep/ripgrep**

| Instead of | Use LeanKG |
|------------|------------|
| grep/ripgrep for "where is X?" | `search_code` or `find_function` |
| glob + content search for tests | `get_tested_by` |
| Manual dependency tracing | `get_impact_radius` or `get_dependencies` |
| Reading entire files | `get_context` (token-optimized) |
BOOTSTRAPEOF
        echo "Created leankg-bootstrap.md for Cursor"
    fi
    
    if [ "$hooks_installed" = true ]; then
        echo "Configured LeanKG hooks for Cursor"
    else
        echo "LeanKG hooks already configured for Cursor"
    fi
}

configure_kilo() {
    local config_dir="${XDG_CONFIG_HOME:-$HOME/.config}/kilo"
    local config_file="$config_dir/kilo.json"
    local leankg_path="${INSTALL_DIR}/${BINARY_NAME}"
    local needs_update=false

    mkdir -p "$config_dir"

    if [ -f "$config_file" ]; then
        local current_path
        current_path=$(jq -r '.mcp.leankg.command[0] // empty' "$config_file" 2>/dev/null)
        local current_args
        current_args=$(jq -r '.mcp.leankg.command[1:] | join(" ")' "$config_file" 2>/dev/null)
        
        if [ -n "$current_path" ]; then
            if [ "$current_path" != "$leankg_path" ]; then
                echo "Updating LeanKG binary path for Kilo: $current_path -> $leankg_path"
                needs_update=true
            fi
            if ! echo "$current_args" | grep -q "\-\-watch"; then
                echo "Adding --watch flag to LeanKG for Kilo"
                needs_update=true
            fi
        fi
        
        if [ "$needs_update" = false ]; then
            echo "LeanKG already properly configured in Kilo"
            return
        fi
    else
        needs_update=true
    fi

    local tmp_file
    tmp_file=$(mktemp)
    if [ -f "$config_file" ] && [ "$needs_update" = true ]; then
        cat "$config_file" | jq --arg leankg "$leankg_path" '.mcp.leankg = {"type": "local", "command": [$leankg, "mcp-stdio", "--watch"], "enabled": true}' > "$tmp_file"
    else
        cat > "$tmp_file" <<EOF
{
  "\$schema": "https://kilo.ai/config.json",
  "mcp": {
    "leankg": {
      "type": "local",
      "command": ["$leankg_path", "mcp-stdio", "--watch"],
      "enabled": true
    }
  }
}
EOF
    fi
    mv "$tmp_file" "$config_file"
    echo "Configured LeanKG for Kilo at $config_file"
}

configure_gemini() {
    local leankg_path="${INSTALL_DIR}/${BINARY_NAME}"
    local needs_update=false
    
    if command -v gemini >/dev/null 2>&1; then
        echo "Configuring LeanKG for Gemini CLI using 'gemini mcp add'..."
        gemini mcp add leankg "$leankg_path" mcp-stdio --watch --scope user || true
        echo "Configured LeanKG for Gemini CLI"
    else
        local config_file="$HOME/.gemini/settings.json"
        mkdir -p "$HOME/.gemini"

        if [ -f "$config_file" ] && [ -s "$config_file" ]; then
            local current_path
            current_path=$(jq -r '.mcpServers.leankg.command // empty' "$config_file" 2>/dev/null)
            local current_args
            current_args=$(jq -r '.mcpServers.leankg.args // [] | join(" ")' "$config_file" 2>/dev/null)
            
            if [ -n "$current_path" ]; then
                if [ "$current_path" != "$leankg_path" ]; then
                    echo "Updating LeanKG binary path for Gemini CLI: $current_path -> $leankg_path"
                    needs_update=true
                fi
                if ! echo "$current_args" | grep -q "\-\-watch"; then
                    echo "Adding --watch flag to LeanKG for Gemini CLI"
                    needs_update=true
                fi
            fi
            
            if [ "$needs_update" = false ]; then
                echo "LeanKG already properly configured in Gemini CLI"
                return
            fi
        else
            needs_update=true
        fi

        local tmp_file
        tmp_file=$(mktemp)
        if [ -f "$config_file" ] && [ "$needs_update" = true ]; then
            cat "$config_file" | jq --arg leankg "$leankg_path" '.mcpServers.leankg = {"command": $leankg, "args": ["mcp-stdio", "--watch"]}' > "$tmp_file" && mv "$tmp_file" "$config_file"
        else
            cat > "$tmp_file" <<EOF
{
  "mcpServers": {
    "leankg": {
      "command": "$leankg_path",
      "args": ["mcp-stdio", "--watch"]
    }
  }
}
EOF
            mv "$tmp_file" "$config_file"
        fi
        echo "Configured LeanKG for Gemini CLI at $config_file"
    fi
}

configure_antigravity() {
    local config_dir="$HOME/.gemini/antigravity"
    local config_file="$config_dir/mcp_config.json"
    local leankg_path="${INSTALL_DIR}/${BINARY_NAME}"
    local srv_json="{\"name\": \"leankg\", \"transport\": \"stdio\", \"command\": \"$leankg_path\", \"args\": [\"mcp-stdio\", \"--watch\"], \"enabled\": true}"

    mkdir -p "$config_dir"

    if [ -f "$config_file" ]; then
        local content
        content=$(cat "$config_file")
        if echo "$content" | jq -e '(.mcpServers | type == "array") and (.mcpServers[] | select(.name == "leankg"))' > /dev/null 2>&1; then
            echo "LeanKG already configured in Anti Gravity"
            return
        fi
        local tmp_file
        tmp_file=$(mktemp)
        if echo "$content" | jq -e '.mcpServers | type == "array"' > /dev/null 2>&1; then
            cat "$config_file" | jq --argjson srv "$srv_json" '.mcpServers += [$srv]' > "$tmp_file"
        else
            cat "$config_file" | jq --argjson srv "$srv_json" '.mcpServers = [$srv]' > "$tmp_file"
        fi
        mv "$tmp_file" "$config_file"
    else
        echo "{\"mcpServers\": [$srv_json]}" > "$config_file"
    fi
    echo "Configured LeanKG for Anti Gravity at $config_file"
}

install_claude_instructions() {
    local claude_md="$HOME/.config/claude/CLAUDE.md"
    mkdir -p "$(dirname "$claude_md")"
    
    if [ -f "$claude_md" ]; then
        if grep -q "MANDATORY" "$claude_md" 2>/dev/null; then
            echo "LeanKG instructions already exist in Claude Code CLAUDE.md"
        else
            echo "" >> "$claude_md"
            curl -fsSL "$CLAUDE_TEMPLATE_URL" >> "$claude_md" 2>/dev/null || cat >> "$claude_md" <<'EOF'

# LeanKG

## MANDATORY: Use LeanKG First

Before ANY codebase search/navigation, use LeanKG tools:
1. `mcp_status` - check if ready
2. Use tool: `search_code`, `find_function`, `query_file`, `get_impact_radius`, `get_dependencies`, `get_dependents`, `get_tested_by`, `get_context`
3. Only fallback to grep/read if LeanKG fails

| Task | Use |
|------|-----|
| Where is X? | `search_code` or `find_function` |
| What breaks if I change Y? | `get_impact_radius` |
| What tests cover Y? | `get_tested_by` |
| How does X work? | `get_context` |
EOF
            echo "Added LeanKG instructions to Claude Code CLAUDE.md"
        fi
    else
        curl -fsSL "$CLAUDE_TEMPLATE_URL" > "$claude_md" 2>/dev/null || cat > "$claude_md" <<'EOF'
# LeanKG

## MANDATORY: Use LeanKG First

Before ANY codebase search/navigation, use LeanKG tools:
1. `mcp_status` - check if ready
2. Use tool: `search_code`, `find_function`, `query_file`, `get_impact_radius`, `get_dependencies`, `get_dependents`, `get_tested_by`, `get_context`
3. Only fallback to grep/read if LeanKG fails

| Task | Use |
|------|-----|
| Where is X? | `search_code` or `find_function` |
| What breaks if I change Y? | `get_impact_radius` |
| What tests cover Y? | `get_tested_by` |
| How does X work? | `get_context` |
EOF
        echo "Created CLAUDE.md for Claude Code at $claude_md"
    fi
}

index_leankg_project() {
    local leankg_path="${INSTALL_DIR}/${BINARY_NAME}"
    local project_dir="${1:-$(pwd)}"
    
    echo "Indexing LeanKG project at $project_dir..."
    
    if [ ! -x "$leankg_path" ]; then
        echo "LeanKG binary not found at $leankg_path - skipping indexing"
        return 1
    fi
    
    if [ -d "$project_dir/.git" ]; then
        if [ -f "$project_dir/Cargo.toml" ]; then
            echo "Detected Rust project - indexing source code..."
            "$leankg_path" index "$project_dir/src" 2>/dev/null || echo "Indexing completed (or warnings are normal)"
            return 0
        fi
    fi
    
    echo "Not a recognized project structure - skipping indexing"
    return 1
}

install_opencode_skills() {
    local skills_dir="${XDG_CONFIG_HOME:-$HOME/.config}/opencode/skills"
    install_leankg_skill "$skills_dir" "opencode"
}

install_leankg_skill() {
    local skills_dir="$1"
    local agent_name="$2"
    local leankg_skill_dir="$skills_dir/using-leankg"
    
    mkdir -p "$leankg_skill_dir"
    
    local skill_content=$(cat <<'EOF'
---
name: using-leankg
description: Use for ANY code search, navigation, or finding code logic - "where is X", "find logic Y", "how does Z work", impact analysis, dependencies. LeanKG is MANDATORY first.
---

<EXTREMELY_IMPORTANT>
STRICT ENFORCEMENT: You MUST use LeanKG tools FIRST before ANY other method.

1. ALWAYS invoke `mcp_status` first to check LeanKG readiness
2. If NOT ready, invoke `mcp_init` or `mcp_index` to initialize
3. Use LeanKG tools for ALL searches
4. ONLY fall back to RTK or grep if LeanKG returns EMPTY results

**NO EXCEPTIONS. NO RATIONALIZATION. Use LeanKG first.**
</EXTREMELY_IMPORTANT>

## MANDATORY Search Flow: LeanKG -> RTK -> Grep

```
When user asks "where is X", "find logic Y", "how does Z work", etc:
           |
           v
1. mcp_status (ALWAYS check first)
           |
           v
2. search_code("X") or find_function("X") or query_file("X")
           |
           +-- Results returned --> Use get_context(file) to read content
           |
           v (EMPTY)
3. rtk grep "X" --path .
           |
           v (EMPTY)
4. grep -rn "X" --include="*.rs"
```

## LeanKG MCP Tools (Use in this order)

| Step | Tool | When to Use |
|------|------|-------------|
| 1 | `mcp_status` | ALWAYS check first |
| 2 | `search_code("X")` | Find code by name/type |
| 3 | `find_function("X")` | Locate function definitions |
| 4 | `query_file("*X*")` | Find files by name |
| 5 | `get_impact_radius(file)` | Blast radius for changes |
| 6 | `get_context(file)` | READ file content (token-optimized) |
| 7 | `get_dependencies(file)` | Get imports |
| 8 | `get_tested_by(file)` | Find tests |

## Critical: After search_code returns file paths

**IMPORTANT:** When `search_code` returns results with file paths:
1. Use `get_context(file_path)` to READ the actual file content
2. Do NOT just report the file paths - show the code

## RTK Fallback (Only if LeanKG EMPTY)

```bash
rtk grep "search term" --path .
rtk file "pattern" --path .
```

## Grep Fallback (LAST RESORT, only if RTK EMPTY too)

```bash
grep -rn "X" --include="*.rs"
```

## Common Triggers for LeanKG

| User says... | LeanKG tool |
|--------------|-------------|
| "where is X" | `search_code("X")` or `find_function("X")` |
| "find the logic" | `search_code("logic_name")` |
| "how does X work" | `get_context(file)` after search_code |
| "what calls X" | `get_call_graph("X")` |
| "what breaks if I change X" | `get_impact_radius("X")` |
| "find all files named X" | `query_file("X")` |
EOF
)

    if [ -f "$leankg_skill_dir/SKILL.md" ]; then
        if grep -q "STRICT ENFORCEMENT" "$leankg_skill_dir/SKILL.md" 2>/dev/null; then
            echo "LeanKG skill already installed at $leankg_skill_dir"
            return
        fi
    fi
    
    echo "$skill_content" > "$leankg_skill_dir/SKILL.md"
    echo "Installed LeanKG skill to $leankg_skill_dir for $agent_name"
}

install_agents_instructions() {
    local agents_file="$1"
    local agents_dir="$(dirname "$agents_file")"
    mkdir -p "$agents_dir"
    
    local agents_content
    agents_content=$(cat <<'EOF'

## LeanKG Tools Usage

### MANDATORY RULE - ALWAYS USE LEANKG FIRST

**Before doing ANY codebase search, navigation, or analysis, you MUST use LeanKG tools.**

### This Is Not Optional

When a user asks about the codebase, you MUST:
1. Check if LeanKG is available via `mcp_status`
2. If LeanKG is not initialized, run `mcp_init` first
3. Use the appropriate LeanKG tool for the task
4. **ONLY after LeanKG is exhausted** may you fall back to other methods

### Why This Matters

| Instead of | Use LeanKG |
|------------|------------|
| grep/ripgrep for "where is X?" | `search_code` or `find_function` |
| glob + content search for tests | `get_tested_by` |
| Manual dependency tracing | `get_impact_radius` or `get_dependencies` |
| Reading entire files | `get_context` (token-optimized) |

### LeanKG Tools Quick Reference

| Tool | Use Case |
|------|----------|
| `mcp_status` | Check if LeanKG is ready |
| `search_code` | Find code by name/type |
| `find_function` | Locate function definition |
| `query_file` | Find file by name/pattern |
| `get_impact_radius` | Blast radius before changes |
| `get_call_graph` | Understand function flow |
| `get_dependencies` | Get direct imports |
| `get_dependents` | Get files depending on target |
| `get_tested_by` | Find related tests |
| `get_context` | Minimal AI context (token-optimized) |
| `get_review_context` | Focused subgraph for reviews |

### Example Decision Flow

**User: "Where is the auth function?"**
```
1. mcp_status -> confirmed ready
2. search_code("auth") OR find_function("auth")
3. Return result from LeanKG
```

**User: "What would break if I change main.rs?"**
```
1. mcp_status -> confirmed ready
2. get_impact_radius("src/main.rs", depth=3)
3. Return affected elements
```

### Important Notes

- LeanKG maintains a **knowledge graph** of your codebase
- `get_impact_radius` calculates blast radius - always check before changes
- `get_context` returns token-optimized output (~99% token savings)
- Tools are pre-indexed and **much faster** than runtime grep/search
- If LeanKG returns empty/incomplete results, THEN you may use fallback methods
EOF
)

    if [ -f "$agents_file" ]; then
        if grep -qi "LEANKG" "$agents_file" 2>/dev/null; then
            echo "LeanKG instructions already exist in $agents_file"
        else
            echo "$agents_content" >> "$agents_file"
            echo "Added LeanKG instructions to $agents_file"
        fi
    else
        cat > "$agents_file" <<'EOF'
# LeanKG Agent Instructions

## MANDATORY: Use LeanKG First
EOF
        echo "$agents_content" >> "$agents_file"
        echo "Created $agents_file with LeanKG instructions"
    fi
}

main() {
    local target="${1:-}"

    if [ -z "$target" ]; then
        usage
        exit 1
    fi

    local platform
    platform=$(detect_platform)

    case "$target" in
        update)
            update_binary "$platform"
            # Reinstall hooks on update (they may have new features)
            setup_claude_hooks
            # Remove old skill (replaced by hooks)
            remove_old_skill
            echo "LeanKG hooks updated, old skill removed."
            exit 0
            ;;
        version)
            show_version
            exit 0
            ;;
        opencode|cursor|claude|gemini|kilo|antigravity)
            install_binary "$platform" "full"
            ;;
        *)
            echo "Unknown command: $target" >&2
            usage
            exit 1
            ;;
    esac

    if [ "$target" != "update" ]; then
        case "$target" in
            opencode)
                configure_opencode
                install_opencode_skills
                install_agents_instructions "$HOME/.config/opencode/AGENTS.md"
                index_leankg_project "$(pwd)"
                ;;
            cursor)
                configure_cursor
                setup_cursor_hooks
                install_leankg_skill "$HOME/.cursor/skills" "cursor"
                install_agents_instructions "$HOME/.cursor/AGENTS.md"
                ;;
            claude)
                configure_claude
                setup_claude_hooks
                install_claude_instructions
                ;;
            gemini)
                configure_gemini
                install_leankg_skill "$HOME/.gemini/skills" "gemini"
                install_agents_instructions "$HOME/.gemini/GEMINI.md"
                ;;
            kilo)
                configure_kilo
                install_leankg_skill "$HOME/.config/kilo/skills" "kilo"
                install_agents_instructions "$HOME/.config/kilo/AGENTS.md"
                ;;
            antigravity)
                configure_antigravity
                install_leankg_skill "$HOME/.gemini/antigravity/skills" "antigravity"
                install_agents_instructions "$HOME/.gemini/GEMINI.md"
                ;;
        esac
    fi

    echo ""
    echo "Run 'leankg --help' to get started."
    echo "To update later: curl -fsSL $GITHUB_RAW/scripts/install.sh | bash -s -- update"
}

main "$@"
