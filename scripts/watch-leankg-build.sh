#!/bin/bash
# Auto-rebuild and restart LeanKG when source changes
# Watches for cargo build output and restarts the launchd service

set -e

LABEL="com.leankg.mcp-http"
BINARY="/Users/linh.doan/work/harvey/freepeak/leankg/target/release/leankg"
LEANKG_DIR="/Users/linh.doan/work/harvey/freepeak/leankg"
TIMEOUT=300

echo "=== LeanKG Auto-Restarter ==="
echo "Watching: $BINARY"
echo "Timeout: ${TIMEOUT}s after last build"
echo ""

# Function to restart service
restart_service() {
    echo "[$(date '+%H:%M:%S')] Restarting LeanKG MCP HTTP..."
    launchctl stop "$LABEL" 2>/dev/null || true
    sleep 0.5
    launchctl start "$LABEL" 2>/dev/null || true
    echo "[$(date '+%H:%M:%S')] Service restarted"
}

# Track last modification time
last_build=0

# Poll for binary changes
echo "Starting watch loop..."
while true; do
    if [ -f "$BINARY" ]; then
        current_mtime=$(stat -f %m "$BINARY" 2>/dev/null || stat -c %Y "$BINARY" 2>/dev/null)

        if [ "$current_mtime" -gt "$last_build" ] && [ "$last_build" -ne 0 ]; then
            echo "[$(date '+%H:%M:%S')] New build detected!"
            last_build=$current_mtime
            restart_service
        fi

        # Update last_build if not set
        if [ "$last_build" -eq 0 ]; then
            last_build=$current_mtime
            echo "[$(date '+%H:%M:%S')] Tracking binary (mtime: $last_build)"
        fi
    fi

    sleep 2
done