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

    mkdir -p "$config_dir"

    if [ -f "$config_file" ]; then
        local content
        content=$(cat "$config_file")
        if echo "$content" | grep -q "leankg"; then
            echo "LeanKG already configured in Cursor"
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
    local config_file="$HOME/.claude/mcp_settings.json"
    local leankg_path="${INSTALL_DIR}/${BINARY_NAME}"

    mkdir -p "$(dirname "$config_file")"

    # Check if file exists and has content
    local has_content=false
    if [ -f "$config_file" ] && [ -s "$config_file" ]; then
        local content
        content=$(cat "$config_file")
        if echo "$content" | grep -q "leankg"; then
            echo "LeanKG already configured in Claude Code"
            return
        fi
        has_content=true
    fi

    # Initialize or ensure valid JSON structure
    if [ "$has_content" = false ]; then
        cat > "$config_file" <<EOF
{
  "mcpServers": {}
}
EOF
    fi

    local tmp_file
    tmp_file=$(mktemp)
    cat "$config_file" | jq --arg leankg "$leankg_path" '.mcpServers.leankg = {"command": $leankg, "args": ["mcp-stdio", "--watch"]}' > "$tmp_file"
    mv "$tmp_file" "$config_file"

    echo "Configured LeanKG for Claude Code at $config_file"
}

setup_claude_hooks() {
    local plugin_dir="$HOME/.claude/plugins/leankg"
    local hooks_installed=false
    
    if [ ! -d "$plugin_dir/hooks" ]; then
        mkdir -p "$plugin_dir/hooks"
        
        cat > "$plugin_dir/hooks/hooks.json" <<'EOF'
{
  "hooks": {
    "SessionStart": [
      {
        "matcher": "startup|clear|compact",
        "hooks": [
          {
            "type": "command",
            "command": "\"${CLAUDE_PLUGIN_ROOT}/hooks/run-hook.cmd\" session-start",
            "async": false
          }
        ]
      }
    ]
  }
}
EOF
         
        cat > "$plugin_dir/hooks/run-hook.cmd" <<'CMDEOF'
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
if [ -n "${CLAUDE_PLUGIN_ROOT:-}" ]; then
  printf '{\n  "hookSpecificOutput": {\n    "hookEventName": "SessionStart",\n    "additionalContext": "%s"\n  }\n}\n' "$session_context"
else
  printf '{\n  "additional_context": "%s"\n}\n' "$session_context"
fi
exit 0
CMDEOF

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
if [ -n "${CLAUDE_PLUGIN_ROOT:-}" ]; then
  printf '{\n  "hookSpecificOutput": {\n    "hookEventName": "SessionStart",\n    "additionalContext": "%s"\n  }\n}\n' "$session_context"
else
  printf '{\n  "additional_context": "%s"\n}\n' "$session_context"
fi
exit 0
HOOKEOF

        chmod +x "$plugin_dir/hooks/run-hook.cmd" "$plugin_dir/hooks/session-start"
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
    
    if [ -d "$plugin_dir/hooks" ]; then
        echo "LeanKG hooks already configured for Cursor"
        return
    fi
    
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
    
    echo "Configured LeanKG hooks for Cursor"
}

configure_kilo() {
    local config_dir="${XDG_CONFIG_HOME:-$HOME/.config}/kilo"
    local config_file="$config_dir/kilo.json"
    local leankg_path="${INSTALL_DIR}/${BINARY_NAME}"

    mkdir -p "$config_dir"

    if [ -f "$config_file" ]; then
        local content
        content=$(cat "$config_file")
        if echo "$content" | grep -q "leankg"; then
            echo "LeanKG already configured in Kilo"
            return
        fi
    fi

    local tmp_file
    tmp_file=$(mktemp)
    if [ -f "$config_file" ]; then
        cat "$config_file" | jq --arg leankg "$leankg_path" '.mcp.leankg = {"type": "local", "command": [$leankg_path, "mcp-stdio", "--watch"], "enabled": true}' > "$tmp_file"
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
    
    if command -v gemini >/dev/null 2>&1; then
        echo "Configuring LeanKG for Gemini CLI using 'gemini mcp add'..."
        gemini mcp add leankg "$leankg_path" mcp-stdio --watch --scope user || true
        echo "Configured LeanKG for Gemini CLI"
    else
        local config_file="$HOME/.gemini/settings.json"
        mkdir -p "$HOME/.gemini"

        if [ -f "$config_file" ]; then
            local content
            content=$(cat "$config_file")
            if echo "$content" | grep -q "leankg"; then
                echo "LeanKG already configured in Gemini CLI"
                return
            fi
        else
            echo "{}" > "$config_file"
        fi

        local tmp_file
        tmp_file=$(mktemp)
        cat "$config_file" | jq --arg leankg "$leankg_path" '.mcpServers.leankg = {"command": $leankg, "args": ["mcp-stdio", "--watch"]}' > "$tmp_file"
        mv "$tmp_file" "$config_file"
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
                install_leankg_skill "$HOME/.claude/skills" "claude"
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
