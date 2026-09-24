#!/usr/bin/env bash
# dsh-watch.sh — start the DSH usage watcher manually, in watch mode.
#
# Watches every DSH session log (~/.dsh/sessions/**/session.v{3,4}.jsonl[.zstd]),
# classifies each LeanKG tool call, scores alert-worthy ones with Laya, and
# serves the dashboard. Run it whenever you want to watch; Ctrl-C to stop.
#
#   dsh-watch              # foreground
#   dsh-watch --background # detached, log to /tmp/leankg-dsh-usage.out
#
# It uses the persistent tagged binary ~/.local/bin/leankg-dsh (dsh-usage is
# default-off, so the normal leankg binary ships a stub). If that binary is
# missing, this script builds it from a worktree that contains the dsusage
# package and installs it to ~/.local/bin/leankg-dsh.
set -euo pipefail

BIN="${LEANKG_DSH_BIN:-$HOME/.local/bin/leankg-dsh}"
ADDR="${DSH_ADDR:-127.0.0.1:9710}"
INTERVAL="${DSH_INTERVAL:-180s}"
MIN_SEV="${DSH_MIN_SEVERITY:-high}"
LOG="${DSH_LOG:-/tmp/leankg-dsh-usage.out}"
SRC_WORKTREE="${LEANKG_DSH_SRC:-$HOME/work/harvey/freepeak/leankg/.worktrees/dsh-usage-v4}"

# Ensure a working tagged binary exists.
if [[ ! -x "$BIN" ]]; then
  if [[ -x "$SRC_WORKTREE/bin/leankg" ]]; then
    echo "installing tagged binary from $SRC_WORKTREE ..." >&2
    mkdir -p "$(dirname "$BIN")"
    cp "$SRC_WORKTREE/bin/leankg" "$BIN" && chmod +x "$BIN"
  elif [[ -d "$SRC_WORKTREE" ]]; then
    echo "building tagged binary in $SRC_WORKTREE (dsh-usage is default-off) ..." >&2
    (cd "$SRC_WORKTREE" && make go-build-dshusage)
    mkdir -p "$(dirname "$BIN")"
    cp "$SRC_WORKTREE/bin/leankg" "$BIN" && chmod +x "$BIN"
  else
    echo "no tagged binary and no source worktree at $SRC_WORKTREE" >&2
    echo "set LEANKG_DSH_SRC to a worktree containing internal/dsusage" >&2
    exit 1
  fi
fi

# Refuse a second watcher on the same port.
if pgrep -f "dsh-usage --addr $ADDR" >/dev/null 2>&1; then
  echo "a watcher is already running on $ADDR — stop it first:" >&2
  pgrep -fl "dsh-usage --addr $ADDR" >&2
  exit 1
fi

# Laya is optional; only pass --laya-url when the sidecar is actually up.
LAYA=()
if curl -fsS -m 2 "${LAYA_HEALTH:-http://127.0.0.1:8091/health}" >/dev/null 2>&1; then
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
