#!/usr/bin/env bash
# Tests for scripts/sync-npm-version.sh (plain bash asserts, no framework).
#
# Each case builds a sandbox that mirrors the repo layout:
#   <sandbox>/scripts/sync-npm-version.sh   (copy of the real script)
#   <sandbox>/Cargo.toml                    (fixture)
#   <sandbox>/npm/leankg/package.json       (fixture)
# The script resolves paths relative to itself, so running the sandbox copy
# exercises exactly what CI runs.
set -u

SCRIPT_UNDER_TEST="$(cd "$(dirname "$0")/../scripts" && pwd)/sync-npm-version.sh"

PASS=0
FAIL=0

assert_eq() { # assert_eq <desc> <expected> <actual>
  if [ "$2" = "$3" ]; then
    PASS=$((PASS + 1))
  else
    FAIL=$((FAIL + 1))
    echo "FAIL: $1"
    echo "  expected: [$2]"
    echo "  actual:   [$3]"
  fi
}

assert_contains() { # assert_contains <desc> <needle> <haystack>
  case "$3" in
  *"$2"*) PASS=$((PASS + 1)) ;;
  *)
    FAIL=$((FAIL + 1))
    echo "FAIL: $1"
    echo "  expected to contain: [$2]"
    echo "  actual:              [$3]"
    ;;
  esac
}

new_sandbox() { # new_sandbox <cargo_version_or_MALFORMED> <npm_version> -> prints sandbox dir
  local cargo_ver="$1" npm_ver="$2"
  local dir
  dir="$(mktemp -d "${TMPDIR:-/tmp}/sync-npm-test.XXXXXX")"
  mkdir -p "$dir/scripts" "$dir/npm/leankg"
  cp "$SCRIPT_UNDER_TEST" "$dir/scripts/"
  if [ "$cargo_ver" = "MALFORMED" ]; then
    printf '[package]\nname = "leankg"\nedition = "2021"\n' >"$dir/Cargo.toml"
  else
    printf '[package]\nname = "leankg"\nversion = "%s"\nedition = "2021"\n' \
      "$cargo_ver" >"$dir/Cargo.toml"
  fi
  if [ "$npm_ver" != "__MISSING__" ]; then
    printf '{\n  "name": "leankg",\n  "version": "%s",\n  "license": "Apache-2.0"\n}\n' \
      "$npm_ver" >"$dir/npm/leankg/package.json"
  fi
  echo "$dir"
}

pkg_version() { # pkg_version <sandbox> — read version back via python3/node
  local f="$1/npm/leankg/package.json"
  if command -v node >/dev/null 2>&1; then
    node -p "require('$f').version"
  else
    python3 -c "import json;print(json.load(open('$f'))['version'])"
  fi
}

# ---------------------------------------------------------------- case 1
# Equal versions -> no-op, exit 0, package.json untouched.
sb="$(new_sandbox "1.2.3" "1.2.3")"
out="$(bash "$sb/scripts/sync-npm-version.sh" 2>&1)"
rc=$?
assert_eq "equal versions exit code" 0 "$rc"
assert_eq "no-op leaves npm version" "1.2.3" "$(pkg_version "$sb")"
assert_contains "no-op output mentions sync state" "in sync" "$out"

# ---------------------------------------------------------------- case 2
# Drift -> package.json rewritten to crate version, exit 0, old->new printed.
sb="$(new_sandbox "4.5.6" "0.17.9")"
out="$(bash "$sb/scripts/sync-npm-version.sh" 2>&1)"
rc=$?
assert_eq "drift exit code" 0 "$rc"
assert_eq "drift updates npm version" "4.5.6" "$(pkg_version "$sb")"
assert_eq "other fields preserved" "leankg" "$(node -p "require('$sb/npm/leankg/package.json').name")"
assert_contains "prints old version" "0.17.9" "$out"
assert_contains "prints arrow separator" "->" "$out"
assert_contains "prints new version" "4.5.6" "$out"
# Idempotency: second run is a no-op.
out2="$(bash "$sb/scripts/sync-npm-version.sh" 2>&1)"
rc=$?
assert_eq "re-run after sync exits 0" 0 "$rc"
assert_eq "re-run keeps version" "4.5.6" "$(pkg_version "$sb")"

# ---------------------------------------------------------------- case 3
# Malformed Cargo.toml ([package] without version line) -> exit 1.
sb="$(new_sandbox "MALFORMED" "1.0.0")"
out="$(bash "$sb/scripts/sync-npm-version.sh" 2>&1)"
rc=$?
assert_eq "malformed Cargo.toml exit code" 1 "$rc"
assert_eq "malformed Cargo.toml leaves npm untouched" "1.0.0" "$(pkg_version "$sb")"

# Invalid semver in Cargo.toml is a parse failure too.
sb="$(mktemp -d "${TMPDIR:-/tmp}/sync-npm-test.XXXXXX")"
mkdir -p "$sb/scripts" "$sb/npm/leankg"
cp "$SCRIPT_UNDER_TEST" "$sb/scripts/"
printf '[package]\nname = "leankg"\nversion = "not.a.version"\n' >"$sb/Cargo.toml"
printf '{\n  "version": "1.0.0"\n}\n' >"$sb/npm/leankg/package.json"
bash "$sb/scripts/sync-npm-version.sh" >/dev/null 2>&1 </dev/null
rc=$?
assert_eq "invalid semver exit code" 1 "$rc"
assert_eq "invalid semver leaves npm untouched" "1.0.0" "$(pkg_version "$sb")"

# ---------------------------------------------------------------- case 4
# Missing package.json -> exit 1.
sb="$(new_sandbox "2.0.0" "__MISSING__")"
out="$(bash "$sb/scripts/sync-npm-version.sh" 2>&1)"
rc=$?
assert_eq "missing package.json exit code" 1 "$rc"
if [ -e "$sb/npm/leankg/package.json" ]; then
  FAIL=$((FAIL + 1))
  echo "FAIL: missing package.json must not be created"
else
  PASS=$((PASS + 1))
fi

# ---------------------------------------------------------------- case 5
# --check mode: drift fails loudly without writing; parity passes.
sb="$(new_sandbox "9.9.9" "9.9.8")"
bash "$sb/scripts/sync-npm-version.sh" --check >/dev/null 2>&1 </dev/null
rc=$?
assert_eq "--check flags drift (exit 1)" 1 "$rc"
assert_eq "--check does not rewrite" "9.9.8" "$(pkg_version "$sb")"
sb="$(new_sandbox "7.7.7" "7.7.7")"
bash "$sb/scripts/sync-npm-version.sh" --check >/dev/null 2>&1 </dev/null
assert_eq "--check passes on parity" 0 "$?"

echo
echo "passed=$PASS failed=$FAIL"
[ "$FAIL" -eq 0 ]
