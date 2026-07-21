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
  docker        Docker-only setup: index + embed + MCP (no Rust)
  update        Update LeanKG to the latest version
  version       Show installed and latest available version

Examples:
  curl -fsSL $GITHUB_RAW/scripts/install.sh | bash -s -- opencode
  curl -fsSL $GITHUB_RAW/scripts/install.sh | bash -s -- docker
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
    local mcp_port="${MCP_HTTP_PORT:-9699}"

    mkdir -p "$config_dir"

    # HTTP MCP: project is routed via ?project=. For Docker RocksDB the value
    # must be the *container* mount (e.g. /workspace), not a Mac host path.
    # Override with LEANKG_MCP_PROJECT when using a secondary mount.
    local mcp_project="${LEANKG_MCP_PROJECT:-/workspace}"

    # Remove leankg from global config (per-project config is used instead)
    if [ -f "$config_file" ]; then
        local has_leankg
        has_leankg=$(jq -r '.mcpServers.leankg // empty' "$config_file" 2>/dev/null)
        if [ -n "$has_leankg" ]; then
            echo "Removing LeanKG from global Cursor config (per-project config preferred)"
            local tmp_file
            tmp_file=$(mktemp)
            cat "$config_file" | jq 'del(.mcpServers.leankg)' > "$tmp_file"
            mv "$tmp_file" "$config_file"
        fi
    fi

    local project_mcp_dir=".cursor"
    local project_mcp_file="$project_mcp_dir/mcp.json"
    local mcp_url="http://localhost:${mcp_port}/mcp?project=${mcp_project}"

    if [ -d ".cursor" ] || [ -f ".cursor/mcp.json" ]; then
        mkdir -p "$project_mcp_dir"
        if [ -f "$project_mcp_file" ]; then
            local current_url
            current_url=$(jq -r '.mcpServers.leankg.url // empty' "$project_mcp_file" 2>/dev/null)
            if [ "$current_url" = "$mcp_url" ]; then
                echo "LeanKG already configured in project .cursor/mcp.json"
                echo "  project=$mcp_project (Docker container path; override LEANKG_MCP_PROJECT)"
                return
            fi
            local tmp_file
            tmp_file=$(mktemp)
            cat "$project_mcp_file" | jq --arg url "$mcp_url" \
                '.mcpServers.leankg = {"url": $url}' > "$tmp_file"
            mv "$tmp_file" "$project_mcp_file"
        else
            cat > "$project_mcp_file" <<EOF
{
  "mcpServers": {
    "leankg": {
      "url": "$mcp_url"
    }
  }
}
EOF
        fi
        echo "Configured LeanKG for Cursor in project .cursor/mcp.json"
        echo "  URL: $mcp_url"
        echo "  project=$mcp_project (container mount for Docker MCP)"
        echo "  Override: LEANKG_MCP_PROJECT=/workspace-other"
    else
        echo "No .cursor directory in project. Run this from a Cursor project root."
        echo "Or add to .cursor/mcp.json manually:"
        echo "{\"mcpServers\": {\"leankg\": {\"url\": \"$mcp_url\"}}}"
    fi
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
  Gate: curl -sf http://localhost:9699/health — if fail, use Grep/Glob/Read (no LeanKG).

  Prefer-order (HTTP healthy):
  0. get_overview_context(project=…) — session start; not load_layer(L0) alone
  1. mcp_status(project=…) — container path e.g. /workspace for Docker
  2. DISCOVER: concept_search → semantic_search → search_code / find_function
  3. EXACT: get_context / get_impact_radius / get_dependencies / get_dependents
  4. CALLERS: get_callers / get_call_graph
  5. DOCS: get_traceability / find_related_docs / get_files_for_doc
  6. TESTING: get_tested_by / detect_changes
  7. ORCHESTRATE: orchestrate(intent) when unsure which tool
  8. ENV: env= on search/kg_* (never search_by_environment — removed)
</tool_selection_hierarchy>

<forbidden_actions>
  - Do NOT call LeanKG when :9699 health failed
  - Do NOT pass host Mac paths as project= against Docker MCP (use /workspace)
  - Do NOT use Grep for code search when LeanKG is healthy and returns hits
  - Do NOT call removed tools (get_doc_for_file, mcp_hello, mcp_impact, find_clones, wake_up, search_by_environment)
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
  "mcp__leankg__concept_search",
  "mcp__leankg__semantic_search",
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
  Gate: curl -sf http://localhost:9699/health — if fail, use Grep/Glob/Read (no LeanKG).
  1. mcp_status(project=…) — Docker: /workspace (not host Mac path)
  2. DISCOVER: concept_search → semantic_search → search_code / find_function
  3. EXACT: get_context / get_impact_radius / get_dependencies / get_dependents
  4. CALLERS: get_callers / get_call_graph
  5. DOCS: get_traceability / find_related_docs / get_files_for_doc
  6. TESTING: get_tested_by / detect_changes
  7. ORCHESTRATE: orchestrate(intent) when unsure
</tool_selection_hierarchy>

<forbidden_actions>
  - Do NOT call LeanKG when :9699 health failed
  - Do NOT pass host Mac paths as project= against Docker MCP
  - Do NOT use Grep when LeanKG is healthy and returns hits
  - Do NOT call removed tools (get_doc_for_file, mcp_hello, mcp_impact, find_clones, wake_up, search_by_environment)
</forbidden_actions>

<project_context>
Project type detected: ${projectType}
</project_context>

<leankg_reminder>
- Health-gate :9699 before LeanKG tools
- Prefer concept_search / semantic_search before search_code for NL queries
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

**Multi-Project Support:**
LeanKG uses a single HTTP server that supports multiple projects. Each tool accepts a `project` parameter to specify the target project directory. The server routes queries to the correct `.leankg` database automatically.

**IMPORTANT:** Always pass `project="/path/to/project/root"` when calling LeanKG tools. This ensures the server queries the correct project database.

Example: `search_code(query="main", project="/workspace")`  # Docker container mount

**Auto-Activated Tools (all accept `project` parameter):**
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
- `get_traceability` - Get full traceability chain
- `get_code_tree` - Get codebase structure
- `get_clusters` - Get functional clusters
- `detect_changes` - Pre-commit risk analysis

**MANDATORY WORKFLOW: LeanKG First, Grep Fallback**

Before ANY codebase search/navigation, you MUST:

1. Check if LeanKG is available via `mcp_status(project="/project/root")`
2. If LeanKG is not initialized, run `mcp_init(path="/project/root/.leankg")` first
3. Use LeanKG tools with `project="/project/root"`: `search_code`, `find_function`, `query_file`, etc.
4. **ONLY after LeanKG is exhausted (returns empty) may you fall back to grep/ripgrep**

| Instead of | Use LeanKG |
|------------|------------|
| grep/ripgrep for "where is X?" | `search_code(query="X", project="/path")` or `find_function(name="X", project="/path")` |
| glob + content search for tests | `get_tested_by(file="X", project="/path")` |
| Manual dependency tracing | `get_impact_radius(file="X", project="/path")` or `get_dependencies(file="X", project="/path")` |
| Reading entire files | `get_context(file="X", project="/path")` (token-optimized) |
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

**Multi-Project Support:**
LeanKG uses a single HTTP server that supports multiple projects. Each tool accepts a `project` parameter to specify the target project directory. The server routes queries to the correct `.leankg` database automatically.

**IMPORTANT:** Always pass `project="/path/to/project/root"` when calling LeanKG tools. This ensures the server queries the correct project database.

Example: `search_code(query="main", project="/workspace")`  # Docker container mount

**Auto-Activated Tools (all accept `project` parameter):**
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
- `get_traceability` - Get full traceability chain
- `get_code_tree` - Get codebase structure
- `get_clusters` - Get functional clusters
- `detect_changes` - Pre-commit risk analysis

**MANDATORY WORKFLOW: LeanKG First, Grep Fallback**

Before ANY codebase search/navigation, you MUST:

1. Check if LeanKG is available via `mcp_status(project="/project/root")`
2. If LeanKG is not initialized, run `mcp_init(path="/project/root/.leankg")` first
3. Use LeanKG tools with `project="/project/root"`: `search_code`, `find_function`, `query_file`, etc.
4. **ONLY after LeanKG is exhausted (returns empty) may you fall back to grep/ripgrep**

| Instead of | Use LeanKG |
|------------|------------|
| grep/ripgrep for "where is X?" | `search_code(query="X", project="/path")` or `find_function(name="X", project="/path")` |
| glob + content search for tests | `get_tested_by(file="X", project="/path")` |
| Manual dependency tracing | `get_impact_radius(file="X", project="/path")` or `get_dependencies(file="X", project="/path")` |
| Reading entire files | `get_context(file="X", project="/path")` (token-optimized) |
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
    local skill_url="${INSTRUCTIONS_DIR}/using-leankg/SKILL.md"

    mkdir -p "$leankg_skill_dir"

    # Prefer on-disk copy when install.sh is run from a git checkout.
    local repo_skill=""
    if [ -n "${LEANKG_REPO_ROOT:-}" ] && [ -f "${LEANKG_REPO_ROOT}/instructions/using-leankg/SKILL.md" ]; then
        repo_skill="${LEANKG_REPO_ROOT}/instructions/using-leankg/SKILL.md"
    elif [ -n "${BASH_SOURCE[0]:-}" ] && [ -f "${BASH_SOURCE[0]}" ]; then
        local _root
        _root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." 2>/dev/null && pwd || true)"
        if [ -n "$_root" ] && [ -f "$_root/instructions/using-leankg/SKILL.md" ]; then
            repo_skill="$_root/instructions/using-leankg/SKILL.md"
        fi
    elif [ -f "$(pwd)/instructions/using-leankg/SKILL.md" ]; then
        repo_skill="$(pwd)/instructions/using-leankg/SKILL.md"
    fi

    local dest="$leankg_skill_dir/SKILL.md"
    local resolved_skills
    resolved_skills="$(cd "$skills_dir" 2>/dev/null && pwd -P || echo "$skills_dir")"

    if [ -L "$skills_dir" ] || [[ "$resolved_skills" == *'/.ai-tools/skills'* ]]; then
        echo "Note: $skills_dir resolves to shared skills ($resolved_skills)"
        echo "      Updating using-leankg from LeanKG canonical skill (HTTP-gated + prefer-order)"
    fi

    # Refresh when missing or still on the old mandatory / RTK template.
    local needs_update=1
    if [ -f "$dest" ] && grep -q 'prefer-order' "$dest" 2>/dev/null \
        && grep -q 'HTTP-gated\|:9699/health' "$dest" 2>/dev/null \
        && ! grep -q 'STRICT ENFORCEMENT' "$dest" 2>/dev/null; then
        needs_update=0
    fi

    if [ "$needs_update" = "0" ]; then
        echo "LeanKG skill already up to date at $leankg_skill_dir"
        return
    fi

    if [ -n "$repo_skill" ]; then
        cp "$repo_skill" "$dest"
    else
        if ! curl -fsSL "$skill_url" -o "$dest"; then
            echo "WARN: could not download $skill_url — writing embedded fallback" >&2
            cat > "$dest" <<'EOF'
---
name: using-leankg
description: >-
  Code search via LeanKG MCP when HTTP :9699 is healthy; otherwise skip LeanKG
  and use default Grep/Glob/Read.
---

# LeanKG Code Search (HTTP-gated)

Gate: `curl -sf --max-time 2 http://localhost:9699/health`
- Fail → Grep/Glob/Read only (no MCP, no mcp_init)
- OK → mcp_status(project=…) then prefer-order:
  concept_search → semantic_search → search_code / find_function → get_context

Docker MCP: pass container project= (`/workspace`), not a host Mac path.
EOF
        fi
    fi

    echo "Installed LeanKG skill to $leankg_skill_dir for $agent_name"
}

install_agents_instructions() {
    local agents_file="$1"
    local agents_dir="$(dirname "$agents_file")"
    mkdir -p "$agents_dir"

    local agents_content
    agents_content=$(cat <<'EOF'

## LeanKG Tools Usage (HTTP-gated)

### Gate first

```bash
curl -sf --max-time 2 http://localhost:9699/health
```

| Health | Mode |
|--------|------|
| OK | Prefer LeanKG MCP |
| Fail / timeout / refused | Grep / Glob / Read — do **not** call LeanKG, `mcp_init`, or leankg CLI |

### When HTTP is healthy

0. `get_overview_context(project=…)` — session start (not `load_layer(L0)` alone)
1. `mcp_status(project=…)` — Docker: container mount (`/workspace`), not a host Mac path
2. Prefer-order discover: `concept_search` → `semantic_search` → `search_code` / `find_function`
3. Exact follow-up: `get_context` / `get_impact_radius` / `get_dependencies` / `get_dependents` / `get_tested_by`
4. Environment filter: `env=` on search / `kg_*` (never `search_by_environment` — removed)
5. If LeanKG returns empty → fall back to Grep/Glob/Read

See [MCP tools reference](mcp-tools.md) for the full prefer-order and hard-removed tool list (~81 tools with embeddings).

| Instead of | Use LeanKG (HTTP up only) |
|------------|---------------------------|
| grep for "where is X?" (NL/domain) | `concept_search` then `semantic_search` |
| grep for exact symbol name | `search_code` / `find_function` |
| Manual dependency tracing | `get_impact_radius` / `get_dependencies` |
| Reading entire files | `get_context` |

### Example

**User: "Where is authentication handled?"**
```
1. curl :9699/health → OK
2. mcp_status(project="/workspace")
3. concept_search("authentication") or semantic_search("authentication")
4. get_context(file=…) on a hit
```

EOF
)

    if [ -f "$agents_file" ]; then
        if grep -q 'prefer-order\|HTTP-gated\|:9699/health' "$agents_file" 2>/dev/null \
            && ! grep -q 'MANDATORY RULE - ALWAYS USE LEANKG FIRST\|mcp_init first' "$agents_file" 2>/dev/null; then
            echo "LeanKG instructions already up to date in $agents_file"
        elif grep -qi "LEANKG" "$agents_file" 2>/dev/null; then
            # Replace stale LeanKG section markers by appending refreshed block once.
            if ! grep -q 'concept_search → semantic_search' "$agents_file" 2>/dev/null; then
                echo "$agents_content" >> "$agents_file"
                echo "Appended updated LeanKG instructions to $agents_file"
            else
                echo "LeanKG instructions already present in $agents_file"
            fi
        else
            echo "$agents_content" >> "$agents_file"
            echo "Added LeanKG instructions to $agents_file"
        fi
    else
        cat > "$agents_file" <<'EOF'
# LeanKG Agent Instructions

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

    # When run from a git checkout (not curl|bash), expose repo root for skill copy.
    if [ -z "${LEANKG_REPO_ROOT:-}" ] && [ -n "${BASH_SOURCE[0]:-}" ] && [ -f "${BASH_SOURCE[0]}" ]; then
        LEANKG_REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." 2>/dev/null && pwd || true)"
        export LEANKG_REPO_ROOT
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
        docker)
            # No binary install — pull Hub image, offline embed, start MCP.
            echo "Docker-only setup (no Rust). Fetching scripts/docker-up.sh..."
            curl -fsSL "$GITHUB_RAW/scripts/docker-up.sh" | bash
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
