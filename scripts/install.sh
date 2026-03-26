#!/bin/bash
set -e

REPO="FreePeak/LeanKG"
BINARY_NAME="leanKG"
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

    mkdir -p "$config_dir"

    if [ -f "$config_file" ]; then
        local content
        content=$(cat "$config_file")
        if echo "$content" | grep -q "leankg"; then
            echo "LeanKG already configured in OpenCode"
            return
        fi
    else
        echo "{}" > "$config_file"
    fi

    local tmp_file
    tmp_file=$(mktemp)
    cat "$config_file" | jq '.mcp.leankg_dev = {"type": "local", "command": ["leanKG", "mcp-stdio", "--watch"], "enabled": true}' > "$tmp_file"
    mv "$tmp_file" "$config_file"
    echo "Configured LeanKG for OpenCode at $config_file"
}

configure_cursor() {
    local config_dir="${XDG_CONFIG_HOME:-$HOME/.config}/cursor"
    local config_file="$config_dir/mcp.json"

    mkdir -p "$config_dir"

    if [ -f "$config_file" ]; then
        local content
        content=$(cat "$config_file")
        if echo "$content" | grep -q "leankg"; then
            echo "LeanKG already configured in Cursor"
            return
        fi
    else
        echo '{"mcpServers": {}}' > "$config_file"
    fi

    local tmp_file
    tmp_file=$(mktemp)
    cat "$config_file" | jq '.mcpServers.leankg = {"command": "leanKG", "args": ["mcp-stdio", "--watch"]}' > "$tmp_file"
    mv "$tmp_file" "$config_file"
    echo "Configured LeanKG for Cursor at $config_file"
}

configure_claude() {
    local config_dir="$HOME/.config/claude"
    local config_file="$config_dir/settings.json"

    mkdir -p "$config_dir"

    if [ -f "$config_file" ]; then
        local content
        content=$(cat "$config_file")
        if echo "$content" | grep -q "leankg"; then
            echo "LeanKG already configured in Claude Code"
            return
        fi
    else
        cat > "$config_file" <<EOF
{
  "mcpServers": {}
}
EOF
    fi

    local tmp_file
    tmp_file=$(mktemp)
    cat "$config_file" | jq '.mcpServers.leankg = {"command": "leanKG", "args": ["mcp-stdio", "--watch"]}' > "$tmp_file"
    mv "$tmp_file" "$config_file"

    echo "Configured LeanKG for Claude Code at $config_file"
}

configure_gemini() {
    local config_dir="${XDG_CONFIG_HOME:-$HOME/.config}/gemini-cli"
    local config_file="$config_dir/mcp.json"

    mkdir -p "$config_dir"

    if [ -f "$config_file" ]; then
        local content
        content=$(cat "$config_file")
        if echo "$content" | grep -q "leankg"; then
            echo "LeanKG already configured in Gemini CLI"
            return
        fi
    else
        echo '{"mcpServers": {}}' > "$config_file"
    fi

    local tmp_file
    tmp_file=$(mktemp)
    cat "$config_file" | jq '.mcpServers.leankg = {"command": "leanKG", "args": ["mcp-stdio", "--watch"]}' > "$tmp_file"
    mv "$tmp_file" "$config_file"
    echo "Configured LeanKG for Gemini CLI at $config_file"
}

configure_antigravity() {
    local config_dir="$HOME/.gemini/antigravity"
    local config_file="$config_dir/mcp_config.json"

    mkdir -p "$config_dir"

    if [ -f "$config_file" ]; then
        local content
        content=$(cat "$config_file")
        if echo "$content" | grep -q "leankg"; then
            echo "LeanKG already configured in Anti Gravity"
            return
        fi
    else
        echo '{"mcpServers": {}}' > "$config_file"
    fi

    local tmp_file
    tmp_file=$(mktemp)
    cat "$config_file" | jq '.mcpServers.leankg = {"command": "leanKG", "args": ["mcp-stdio", "--watch"]}' > "$tmp_file"
    mv "$tmp_file" "$config_file"
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
        if grep -q "LEANKG" "$agents_file" 2>/dev/null; then
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
        opencode|cursor|claude|gemini|antigravity)
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
                install_agents_instructions "$HOME/.config/opencode/AGENTS.md"
                ;;
            cursor)
                configure_cursor
                install_agents_instructions "$HOME/.config/cursor/AGENTS.md"
                ;;
            claude)
                configure_claude
                install_claude_instructions
                ;;
            gemini)
                configure_gemini
                install_agents_instructions "$HOME/.gemini/GEMINI.md"
                ;;
            antigravity)
                configure_antigravity
                install_agents_instructions "$HOME/.gemini/GEMINI.md"
                ;;
        esac
    fi

    echo ""
    echo "Run 'leanKG --help' to get started."
    echo "To update later: curl -fsSL $GITHUB_RAW/scripts/install.sh | bash -s -- update"
}

main "$@"
