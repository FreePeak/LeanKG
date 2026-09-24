#!/usr/bin/env bash
# dsh-watch.sh — start the DSH usage watcher manually, in watch mode.
#
# Watches every DSH session log (~/.dsh/sessions/**/session.v{3,4}.jsonl[.zstd]),
# classifies each LeanKG tool call, scores the alert-worthy ones with Laya, and
# serves the dashboard. Run it whenever you want to watch; Ctrl-C to stop.
#
#   ./scripts/dsh-watch.sh              # foreground
#   ./scripts/dsh-watch.sh --background # detached, log to /tmp/leankg-dsh-usage.out
#
# Notes
# - Requires the tagged build:  make go-build-dshusage   (the default binary ships a stub)
# - The first scan reads every existing log, so the dashboard stays empty for
#   ~1-2 min on a cold start, then the Needs-attention tab fills with a backlog.
# - INTERVAL must be longer than one scan (~100s on a large corpus); the default
#   8s is only sane once the scan is cached.
set -euo pipefail

ROOT="$(git rev-parse --show-toplevel)"
BIN="$ROOT/bin/leankg"
ADDR="${DSH_ADDR:-127.0.0.1:9710}"
INTERVAL="${DSH_INTERVAL:-180s}"
MIN_SEV="${DSH_MIN_SEVERITY:-high}"
LOG="${DSH_LOG:-/tmp/leankg-dsh-usage.out}"

if [[ ! -x "$BIN" ]]; then
  echo "building tagged binary (dsh-usage is default-off)..." >&2
  (cd "$ROOT" && make go-build-dshusage)
fi

# Refuse to start a second watcher on the same port.
if pgrep -f "dsh-usage --addr $ADDR" >/dev/null 2>&1; then
  echo "a watcher is already running on $ADDR — stop it first:" >&2
  pgrep -fl "dsh-usage --addr $ADDR" >&2
  exit 1
fi

# Laya is optional; only pass --laya-url when the sidecar is actually up.
LAYA=()
if curl -fsS -m 2 http://127.0.0.1:8091/health >/dev/null 2>&1; then
  LAYA=(--laya-url "${LAYA_URL:-http://127.0.0.1:8091}")
else
  echo "warning: Laya sidecar not up on :8091 — starting without scoring" >&2
fi

CMD=("$BIN" dsh-usage --addr "$ADDR" --watch --interval "$INTERVAL" \
     --min-severity "$MIN_SEV" --notify "${LAYA[@]}")

if [[ "${1:-}" == "--background" ]]; then
  nohup "${CMD[@]}" >"$LOG" 2>&1 &
  echo "watcher started (pid $!)"
  echo "  dashboard : http://$ADDR"
  echo "  log       : $LOG"
  echo "  stop      : pkill -f 'dsh-usage --addr $ADDR'"
else
  echo "starting watcher in the foreground — Ctrl-C to stop"
  echo "  dashboard : http://$ADDR"
  exec "${CMD[@]}"
fi
