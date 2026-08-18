#!/bin/bash
# Run `leankg mcp-http` directly on macOS (no Docker) with CPU + memory
# ceilings and an RSS watchdog that restarts the server when it leaks.
#
# Problem this solves: a long-running `leankg mcp-http` in a plain terminal
# grows resident memory without bound (observed 2GB -> 6GB). A lone
# `ulimit -v` is not enough on macOS: it caps VIRTUAL address space, which a
# process that mmaps heavily can exceed while RSS is fine, or let RSS climb
# past the intent. So:
#   - `ulimit -t` caps CPU time (runaway-loop backstop).
#   - An RSS watchdog polls `ps -o rss` every few seconds; when resident
#     memory exceeds the ceiling it kills + restarts the server.
#
# Prefer launchd if you want a login auto-start (it uses the kernel
# JetsamMemoryLimit, which is stricter than any userspace poller):
#   scripts/install-leankg-mcp-launchd.sh
#
# Usage:  scripts/run-mcp-capped.sh [port]         (default 9699)
# Env:    LEANKG_MCP_MAX_MB       RSS ceiling, default 4096 (4 GiB)
#         LEANKG_MCP_MAX_CPU_SECS CPU-seconds ulimit, default 3600
#         LEANKG_BIN              binary path, default `leankg` (on PATH)
set -u

PORT="${1:-9699}"
MAX_MB="${LEANKG_MCP_MAX_MB:-4096}"
CPU_SECS="${LEANKG_MCP_MAX_CPU_SECS:-3600}"
BINARY="${LEANKG_BIN:-leankg}"
POLL_SECS=5

log() { echo "$(date -Iseconds) $*"; }

ulimit -t "$CPU_SECS" 2>/dev/null
log "ceiling: RSS<=${MAX_MB}MB (watchdog) cpu<=${CPU_SECS}s (ulimit -t) port=:${PORT}"

while true; do
  log "starting leankg mcp-http on :$PORT"
  "$BINARY" mcp-http --port "$PORT" &
  PID=$!

  last_peak=0
  while kill -0 "$PID" 2>/dev/null; do
    RSS_KB=$(ps -o rss= -p "$PID" 2>/dev/null | tr -d ' ')
    if [ -n "$RSS_KB" ] && [ "$RSS_KB" -gt 0 ] 2>/dev/null; then
      RSS_MB=$((RSS_KB / 1024))
      if [ "$RSS_MB" -gt "$last_peak" ]; then last_peak=$RSS_MB; fi
      if [ "$RSS_MB" -gt "$MAX_MB" ]; then
        log "RSS ${RSS_MB}MB > ${MAX_MB}MB ceiling — killing PID $PID (leak guard)"
        kill "$PID" 2>/dev/null
        sleep 1
        kill -9 "$PID" 2>/dev/null
        break
      fi
    fi
    sleep "$POLL_SECS"
  done
  wait "$PID" 2>/dev/null
  code=$?
  log "server exited code=$code (peak RSS ~${last_peak}MB) — restarting in 3s"
  sleep 3
done
