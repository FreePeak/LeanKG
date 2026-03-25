#!/bin/bash
set -e

REPO="FreePeak/LeanKG"
BINARY_NAME="leanKG"
INSTALL_DIR="$HOME/.local/bin"
GITHUB_RAW="https://raw.githubusercontent.com/$REPO/main"
GITHUB_API="https://api.github.com/repos/$REPO/releases/latest"

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
    local config_dir="${XDG_CONFIG_HOME:-$HOME/.config}/antigravity"
    local config_file="$config_dir/mcp.json"

    mkdir -p "$config_dir"

    if [ -f "$config_file" ]; then
        local content
        content=$(cat "$config_file")
        if echo "$content" | grep -q "leankg"; then
            echo "LeanKG already configured in Anti Gravity"
            return
        fi
    else
        echo '{"servers": {}}' > "$config_file"
    fi

    local tmp_file
    tmp_file=$(mktemp)
    cat "$config_file" | jq '.servers.leankg = {"command": "leanKG", "args": ["mcp-stdio", "--watch"]}' > "$tmp_file"
    mv "$tmp_file" "$config_file"
    echo "Configured LeanKG for Anti Gravity at $config_file"
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
                ;;
            cursor)
                configure_cursor
                ;;
            claude)
                configure_claude
                ;;
            gemini)
                configure_gemini
                ;;
            antigravity)
                configure_antigravity
                ;;
        esac
    fi

    echo ""
    echo "Run 'leanKG --help' to get started."
    echo "To update later: curl -fsSL $GITHUB_RAW/scripts/install.sh | bash -s -- update"
}

main "$@"
