#!/usr/bin/env bash
# sync-npm-version.sh — keep npm/leankg/package.json in lockstep with the crate.
#
# Reads the crate version from the root Cargo.toml ([package] version line),
# validates it as semver, and rewrites the "version" field of
# npm/leankg/package.json to match. No network access required.
#
# Usage:
#   scripts/sync-npm-version.sh            # sync (no-op + exit 0 when equal)
#   scripts/sync-npm-version.sh --check    # verify only; exit 1 on drift
#
# Exit codes:
#   0 — in parity (or successfully synced)
#   1 — parse/validation failure, missing files, or (--check) version drift
set -euo pipefail

usage() {
  echo "usage: $0 [--check]" >&2
}

CHECK=0
case "${1:-}" in
"") ;;
--check) CHECK=1 ;;
*)
  usage
  exit 1
  ;;
esac
[ $# -le 1 ] || { usage; exit 1; }

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
CARGO_TOML="$ROOT/Cargo.toml"
PKG_JSON="$ROOT/npm/leankg/package.json"

die() {
  echo "ERROR: $*" >&2
  exit 1
}

# --- read crate version ------------------------------------------------------
# Scope to the [package] table so dependency "version =" lines never match.
[ -r "$CARGO_TOML" ] || die "cannot read $CARGO_TOML"
cargo_version="$(awk '
    /^\[/          { in_pkg = ($0 ~ /^\[package\]/); next }
    in_pkg && /^version[[:space:]]*=/ {
      sub(/^[^"]*"/, ""); sub(/".*$/, ""); print; exit
    }
  ' "$CARGO_TOML")"
[ -n "$cargo_version" ] || die "no [package] version found in $CARGO_TOML"

# Semver: MAJOR.MINOR.PATCH with optional pre-release / build metadata.
semver_re='^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$'
printf '%s' "$cargo_version" | grep -Eq "$semver_re" ||
  die "crate version '$cargo_version' is not valid semver"

# --- read/write package.json (node preferred, python3 fallback) --------------
if command -v node >/dev/null 2>&1; then
  read_pkg_version() {
    node -e 'process.stdout.write(require(process.argv[1]).version ?? "")' "$PKG_JSON"
  }
  write_pkg_version() {
    node -e '
      const fs = require("fs");
      const f = process.argv[1];
      const pkg = JSON.parse(fs.readFileSync(f, "utf8"));
      pkg.version = process.argv[2];
      fs.writeFileSync(f, JSON.stringify(pkg, null, 2) + "\n");
    ' "$PKG_JSON" "$cargo_version"
  }
elif command -v python3 >/dev/null 2>&1; then
  read_pkg_version() {
    python3 -c 'import json,sys;
try: print(json.load(open(sys.argv[1])).get("version",""))
except Exception: pass' "$PKG_JSON"
  }
  write_pkg_version() {
    python3 -c 'import json,sys
f, v = sys.argv[1], sys.argv[2]
pkg = json.load(open(f))
pkg["version"] = v
json.dump(pkg, open(f, "w"), indent=2)
open(f, "a").write("\n")' "$PKG_JSON" "$cargo_version"
  }
else
  die "neither node nor python3 available to rewrite package.json"
fi

[ -r "$PKG_JSON" ] || die "cannot read $PKG_JSON (npm wrapper missing?)"
npm_version="$(read_pkg_version || true)"
[ -n "$npm_version" ] || die "could not parse version from $PKG_JSON"

if [ "$npm_version" = "$cargo_version" ]; then
  if [ "$CHECK" -eq 1 ]; then
    echo "OK: npm wrapper ${npm_version} == crate ${cargo_version}"
  else
    echo "already in sync at ${cargo_version} (no change)"
  fi
  exit 0
fi

if [ "$CHECK" -eq 1 ]; then
  echo "DRIFT: npm wrapper ${npm_version} != crate ${cargo_version}" >&2
  exit 1
fi

write_pkg_version
echo "npm/leankg/package.json: ${npm_version} -> ${cargo_version}"
