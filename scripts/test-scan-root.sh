#!/usr/bin/env bash
# scripts/test-scan-root.sh - the served-fleet scope rule, proven.
#
# The self-host must serve ONLY ~/work/harvey/freepeak (the rule of
# 2026-10-13); ~/work/be is served by be-kg. The live wrapper scanned
# $HOME/work, so 231 of its 287 registered projects were ~/work/be company
# code that was never in scope.
#
# This test builds a fake HOME holding one repo inside the root and one repo
# outside it, then runs the REAL scripts against it. `leankg` itself is a stub
# that records its argv and the exported fleet, so no server is started and
# nothing outside the temp directory is touched.
#
# It FAILS if the default scan root is widened back to $HOME/work.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WRAPPER="$REPO_ROOT/scripts/leankg-serve-wrapper.sh"
SCAN="$REPO_ROOT/scripts/leankg-scan-repos.sh"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

assert_contains() {
  case "$2" in
    *"$1"*) ;;
    *) fail "$3 (wanted '$1' in: $2)" ;;
  esac
}

assert_not_contains() {
  case "$2" in
    *"$1"*) fail "$3 (found '$1' in: $2)" ;;
    *) ;;
  esac
}

# An argv entry that is exactly the wildcard form (":9700"), not a substring
# of a loopback address.
assert_no_wildcard_arg() {
  old_ifs="$IFS"
  IFS='
'
  # shellcheck disable=SC2086 # deliberate split of the recorded argv
  for arg in $3; do
    if [ "$arg" = ":$1" ]; then
      fail "the $2 bind must be loopback, not the wildcard ':$1' (argv: $3)"
    fi
  done
  IFS="$old_ifs"
}

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

HOME_DIR="$TMP/home"
INSIDE="$HOME_DIR/work/harvey/freepeak/inside-repo"
OUTSIDE="$HOME_DIR/work/be/outside-repo"
mkdir -p "$INSIDE/.git" "$OUTSIDE/.git" "$TMP/bin"

# The stub records what the wrapper would have started.
cat > "$TMP/bin/leankg" <<'STUB'
#!/bin/sh
printf '%s\n' "$@" > "$LEANKG_STUB_ARGS"
printf '%s\n' "$LEANKG_PROJECT_DIRS" > "$LEANKG_STUB_FLEET"
STUB
chmod +x "$TMP/bin/leankg"

run_wrapper() {
  HOME="$HOME_DIR" \
    LEANKG_BIN="$TMP/bin/leankg" \
    LEANKG_STUB_ARGS="$TMP/args" \
    LEANKG_STUB_FLEET="$TMP/fleet" \
    "$WRAPPER" >/dev/null
}

echo "== the default scope excludes ~/work/be =="
run_wrapper
FLEET="$(cat "$TMP/fleet")"
ARGS="$(cat "$TMP/args")"

assert_contains "$INSIDE" "$FLEET" "the in-scope repo must be registered"
assert_not_contains "$OUTSIDE" "$FLEET" "a repo outside the scope must NOT be registered"
assert_not_contains "/work/be/" "$FLEET" "no ~/work/be repo may be registered"
[ "$(printf '%s' "$FLEET" | awk -F, 'NF && length($0) {n++} END {print n+0}')" = "1" ] \
  || fail "the default fleet must hold the in-scope repo and nothing else (got: $FLEET)"
grep -q 'work/harvey/freepeak' "$WRAPPER" \
  || fail "the wrapper's default scan root must be \$HOME/work/harvey/freepeak"
grep -q 'work/harvey/freepeak' "$SCAN" \
  || fail "the scan script's default scan root must be \$HOME/work/harvey/freepeak"

echo "== the serve invocation binds loopback =="
assert_contains "--http" "$ARGS" "the MCP listener must be started"
assert_contains "127.0.0.1:9699" "$ARGS" "the MCP bind must be loopback"
assert_contains "127.0.0.1:9700" "$ARGS" "the REST bind must be loopback"
assert_contains "--memory" "$ARGS" "the memory layer must be started"
assert_contains "--embed-provider" "$ARGS" "the embed provider must be named"
assert_contains "--hindsight-compat" "$ARGS" "the Hindsight wire must be served"
assert_no_wildcard_arg 9699 MCP "$ARGS"
assert_no_wildcard_arg 9700 REST "$ARGS"

echo "== the standalone scan script agrees =="
SCAN_OUT="$(HOME="$HOME_DIR" "$SCAN")"
[ "$SCAN_OUT" = "$FLEET" ] || fail "leankg-scan-repos.sh and leankg-serve-wrapper.sh disagree ($SCAN_OUT vs $FLEET)"
assert_contains "$INSIDE" "$SCAN_OUT" "the in-scope repo must be listed"
assert_not_contains "$OUTSIDE" "$SCAN_OUT" "a repo outside the scope must NOT be listed"

echo "== LEANKG_SCAN_ROOT still widens the scope on purpose =="
HOME="$HOME_DIR" LEANKG_SCAN_ROOT="$HOME_DIR/work" "$SCAN" > "$TMP/wide"
assert_contains "$OUTSIDE" "$(cat "$TMP/wide")" "the override must widen the scan"

echo "SCAN-ROOT OK"
