#!/bin/bash
# LeanKG MCP HTTP Server - macOS LaunchAgent
# Auto-starts on login and restarts on crash

set -e

LABEL="com.leankg.mcp-http"
PLIST_DIR="$HOME/Library/LaunchAgents"
PLIST_PATH="$PLIST_DIR/$LABEL.plist"
LEANKG_DIR="/Users/linh.doan/work/harvey/freepeak/leankg"

echo "=== LeanKG MCP HTTP LaunchAgent Setup ==="
echo ""

# Stop existing service if running
echo "Stopping existing service (if any)..."
launchctl unload "$PLIST_PATH" 2>/dev/null || true

# Create LaunchAgents directory
mkdir -p "$PLIST_DIR"

# Get current binary path
BINARY_PATH="$LEANKG_DIR/target/release/leankg"
if [ ! -f "$BINARY_PATH" ]; then
    echo "⚠️  Binary not found at $BINARY_PATH"
    echo "   Building release binary..."
    cd "$LEANKG_DIR"
    cargo build --release
fi

# Create the launchd plist
cat > "$PLIST_PATH" << 'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.leankg.mcp-http</string>

    <key>ProgramArguments</key>
    <array>
        <string>/Users/linh.doan/work/harvey/freepeak/leankg/target/release/leankg</string>
        <string>mcp-http</string>
        <string>--watch</string>
        <string>--reuse</string>
    </array>

    <key>WorkingDirectory</key>
    <string>/Users/linh.doan/work/harvey/freepeak/leankg</string>

    <key>RunAtLoad</key>
    <true/>

    <key>KeepAlive</key>
    <true/>

    <key>StandardOutPath</key>
    <string>/tmp/leankg-mcp-http.log</string>

    <key>StandardErrorPath</key>
    <string>/tmp/leankg-mcp-http.error.log</string>

    <key>EnvironmentVariables</key>
    <dict>
        <key>RUST_LOG</key>
        <string>info</string>
    </dict>
</dict>
</plist>
EOF

echo "✓ Created $PLIST_PATH"
echo ""

# Load the service
echo "Loading service..."
launchctl load "$PLIST_PATH"

echo ""
echo "=== Setup Complete ==="
echo ""
echo "Service: $LABEL"
echo "Binary:  $BINARY_PATH"
echo "Logs:    /tmp/leankg-mcp-http*.log"
echo ""
echo "Commands:"
echo "  launchctl start $LABEL     # Start"
echo "  launchctl stop $LABEL      # Stop"
echo "  launchctl unload $PLIST_PATH # Remove"