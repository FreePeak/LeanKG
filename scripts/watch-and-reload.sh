#!/bin/bash
# Auto-rebuild and restart LeanKG when source changes
# Watches src/ for changes, rebuilds, and restarts launchd service

set -e

LABEL="com.leankg.mcp-http"
BINARY="/Users/linh.doan/work/harvey/freepeak/leankg/target/release/leankg"
LEANKG_DIR="/Users/linh.doan/work/harvey/freepeak/leankg"

echo "=== LeanKG Auto-Rebuild & Reload ==="
echo "Binary: $BINARY"
echo ""

restart_service() {
    echo "[$(date '+%H:%M:%S')] Restarting LeanKG..."
    launchctl stop "$LABEL" 2>/dev/null || true
    sleep 0.5
    launchctl start "$LABEL" 2>/dev/null || true
    echo "[$(date '+%H:%M:%S')] Done"
}

# Track binary mtime
get_mtime() {
    stat -f %m "$BINARY" 2>/dev/null || stat -c %Y "$BINARY" 2>/dev/null
}

if [ -f "$BINARY" ]; then
    last_mtime=$(get_mtime)
    echo "Tracking binary (mtime: $last_mtime)"
else
    last_mtime=0
    echo "Binary not found, will watch for creation"
fi

# Start cargo watch in background
echo "Starting cargo watch..."
cd "$LEANKG_DIR"
cargo watch -w src -w Cargo.toml -w Cargo.lock -p leankg --release &

WATCH_PID=$!
echo "Cargo watch running (PID: $WATCH_PID)"
echo ""
echo "Watching for binary changes..."

# Cleanup on exit
cleanup() {
    echo "Stopping cargo watch..."
    kill $WATCH_PID 2>/dev/null || true
    exit 0
}
trap cleanup SIGINT SIGTERM

# Poll for binary changes
while true; do
    sleep 1

    if [ -f "$BINARY" ]; then
        current_mtime=$(get_mtime)

        if [ "$current_mtime" -gt "$last_mtime" ] && [ "$last_mtime" -ne 0 ]; then
            echo ""
            echo "[$(date '+%H:%M:%S')] New build detected!"
            last_mtime=$current_mtime
            restart_service
            echo ""
        fi

        if [ "$last_mtime" -eq 0 ]; then
            last_mtime=$current_mtime
        fi
    fi
done