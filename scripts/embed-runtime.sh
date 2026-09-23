#!/usr/bin/env bash
# embed-runtime.sh — on-demand control for the local embedding runtime.
#
# The runtime is NOT a container: it is a host `llama-server` supervised by
# launchd (com.freepeak.llama-embed). That job is RunAtLoad, so the model
# (~82 MB RSS) is resident from login forever even when nothing embeds.
#
# `leankg serve` never starts or stops it: the launchd wrapper exports
# LEANKG_EMBED_BASE_URL, so embed.StartProvider ATTACHES to whatever is on
# :9101 and returns a no-op release. With the runtime down, L3 semantic
# search degrades to L2 keyword with retrieval.reason "embedding provider
# failed" — visible as by_rung on the dsh-usage dashboard, never a crash.
#
# So the memory trade is explicit: keep the runtime up (L3 always available),
# or turn it on only around an embed run (L3 off the rest of the time).
#
# Usage: embed-runtime.sh {status|on|off}
#   status  report launchd + health state; exit 0 up, 1 down, 2 unmanaged
#   on      load the launchd job (no-op if already loaded)
#   off     boot the job out — NEVER SIGTERM, because KeepAlive would race
#           the exit and restart it.
set -euo pipefail

LABEL="${LEANKG_EMBED_LABEL:-com.freepeak.llama-embed}"
PLIST="${LEANKG_EMBED_PLIST:-$HOME/Library/LaunchAgents/$LABEL.plist}"
DOMAIN="gui/$(id -u)"
HEALTH="${LEANKG_EMBED_HEALTH:-http://127.0.0.1:9101/health}"

healthy() { curl -fsS -m 2 "$HEALTH" >/dev/null 2>&1; }
loaded()  { launchctl print "$DOMAIN/$LABEL" >/dev/null 2>&1; }

cmd="${1:-status}"
case "$cmd" in
  status)
    if ! loaded; then
      echo "embed runtime: not loaded ($DOMAIN/$LABEL)"
      [[ -f "$PLIST" ]] || echo "plist missing: $PLIST" >&2
      exit 1
    fi
    if healthy; then
      echo "embed runtime: loaded, healthy ($HEALTH)"
      exit 0
    fi
    echo "embed runtime: loaded but NOT healthy ($HEALTH) — L3 degrades to L2" >&2
    exit 1
    ;;
  on)
    if loaded; then
      echo "embed runtime: already loaded"
    else
      [[ -f "$PLIST" ]] || { echo "no plist at $PLIST" >&2; exit 2; }
      launchctl bootstrap "$DOMAIN" "$PLIST"
      echo "embed runtime: bootstrapped $LABEL"
    fi
    for _ in $(seq 1 60); do healthy && { echo "embed runtime: up"; exit 0; }; sleep 1; done
    echo "embed runtime: did not become healthy in 60s" >&2
    exit 1
    ;;
  off)
    if loaded; then
      launchctl bootout "$DOMAIN/$LABEL"
      echo "embed runtime: booted out $LABEL (L3 off; keyword L2 still answers)"
    else
      echo "embed runtime: already not loaded"
    fi
    exit 0
    ;;
  *)
    echo "usage: $(basename "$0") {status|on|off}" >&2
    exit 2
    ;;
esac
