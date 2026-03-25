#!/bin/bash
set -e

REPO="FreePeak/LeanKG"
BINARY_NAME="leanKG"
INSTALL_DIR="$HOME/.local/bin"
GITHUB_RAW="https://raw.githubusercontent.com/$REPO/main"
GITHUB_API="https://api.github.com/repos/$REPO/releases/latest"

usage() {
    cat <<EOF
LeanKG Installer

Usage: curl -fsSL $GITHUB_RAW/scripts/install.sh | bash -s -- <target>

Targets:
  opencode      Configure LeanKG for OpenCode AI
  cursor        Configure LeanKG for Cursor AI
  claude        Configure LeanKG for Claude Code/Desktop
  gemini        Configure LeanKG for Gemini CLI
  antigravity   Configure LeanKG for Anti Gravity

Examples:
  curl -fsSL $GITHUB_RAW/scripts/install.sh | bash -s -- opencode
  curl -fsSL $GITHUB_RAW/scripts/install.sh | bash -s -- cursor
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

install_binary() {
    local platform="$1"
    local install_type="$2"

    echo "Installing LeanKG for ${platform}..."

    local version
    version=$(curl -fsSL "$GITHUB_API" | grep -o '"tag_name": "[^"]*' | cut -d'"' -f4 | sed 's/v//')

    if [ -z "$version" ]; then
        echo "Failed to fetch latest version" >&2
        exit 1
    fi

    local url
    url=$(get_download_url "$platform" "$version")

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

    echo "Installed to ${INSTALL_DIR}/${BINARY_NAME}"

    if [ "$install_type" = "full" ]; then
        echo "Adding ${INSTALL_DIR} to PATH..."
        if [ -d "$INSTALL_DIR" ] && [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
            echo "Add this to your shell profile if needed:"
            echo "  export PATH=\"\$HOME/.local/bin:\$PATH\""
        fi
    fi
}

configure_opencode() {
    local config_dir="$HOME/.opencode"
    local config_file="$config_dir/mcp.json"

    mkdir -p "$config_dir"

    if [ -f "$config_file" ]; then
        local content
        content=$(cat "$config_file")
        if echo "$content" | grep -q "leankg"; then
            echo "LeanKG already configured in OpenCode"
            return
        fi
    fi

    cat > "$config_file" <<EOF
{
  "mcpServers": {
    "leankg": {
      "command": "leanKG",
      "args": ["mcp-stdio", "--watch"]
    }
  }
}
EOF
    echo "Configured LeanKG for OpenCode at $config_file"
}

configure_cursor() {
    local config_dir="$HOME/.cursor"
    local config_file="$config_dir/mcp.json"

    mkdir -p "$config_dir"

    if [ -f "$config_file" ]; then
        local content
        content=$(cat "$config_file")
        if echo "$content" | grep -q "leankg"; then
            echo "LeanKG already configured in Cursor"
            return
        fi
    fi

    cat > "$config_file" <<EOF
{
  "mcpServers": {
    "leankg": {
      "command": "leanKG",
      "args": ["mcp-stdio", "--watch"]
    }
  }
}
EOF
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
    echo "Configuring LeanKG for Gemini CLI..."
    echo "Run this command manually:"
    echo "  gemini mcp add leankg leanKG mcp-stdio --watch"
    echo ""
    echo "Or add to your Gemini MCP config manually."
}

configure_antigravity() {
    local config_dir="$HOME/.antigravity"
    local config_file="$config_dir/mcp.json"

    mkdir -p "$config_dir"

    if [ -f "$config_file" ]; then
        local content
        content=$(cat "$config_file")
        if echo "$content" | grep -q "leankg"; then
            echo "LeanKG already configured in Anti Gravity"
            return
        fi
    fi

    cat > "$config_file" <<EOF
{
  "servers": {
    "leankg": {
      "command": "leanKG",
      "args": ["mcp-stdio", "--watch"]
    }
  }
}
EOF
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
        opencode|cursor|claude|gemini|antigravity)
            install_binary "$platform" "full"
            ;;
        *)
            echo "Unknown target: $target" >&2
            usage
            exit 1
            ;;
    esac

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

    echo ""
    echo "Installation complete!"
    echo "Run 'leanKG --help' to get started."
}

main "$@"
