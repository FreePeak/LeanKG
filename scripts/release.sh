#!/bin/bash
set -e

REPO="FreePeak/LeanKG"
CARGO_TOML="Cargo.toml"
DRY_RUN=false

usage() {
    cat <<EOF
LeanKG Release Script

Usage: ./scripts/release.sh <version> [--dry-run]

Arguments:
  version    Semantic version (e.g., 0.1.4, 1.0.0)

Options:
  --dry-run  Show what would be done without making changes

Examples:
  ./scripts/release.sh 0.1.5
  ./scripts/release.sh 1.0.0 --dry-run
EOF
}

log() {
    echo "[release] $1"
}

get_current_version() {
    grep -m1 '^version = ' "$CARGO_TOML" | tr -d '"' | awk '{print $3}'
}

update_version() {
    local new_version="$1"
    log "Updating version in $CARGO_TOML: $(get_current_version) -> $new_version"
    sed -i '' "/^\[package\]/,/^\[/ s/^version = \".*\"/version = \"$new_version\"/" "$CARGO_TOML"
}

build_release() {
    log "Building release binaries..."
    cargo build --release --quiet
}

commit_changes() {
    local version="$1"
    log "Committing changes..."
    git add Cargo.toml Cargo.lock
    git commit -m "release v$version"
}

create_tag() {
    local version="$1"
    log "Creating tag v$version"
    git tag "v$version"
}

push_release() {
    local version="$1"
    log "Pushing to origin..."
    git push origin main
    git push origin "v$version"
}

main() {
    local version=""

    if [ $# -eq 0 ]; then
        usage
        exit 1
    fi

    if [ "$1" = "--help" ] || [ "$1" = "-h" ]; then
        usage
        exit 0
    fi

    version="$1"
    shift

    if [ "$1" = "--dry-run" ]; then
        DRY_RUN=true
    fi

    local current_version
    current_version=$(get_current_version)

    log "Current version: $current_version"
    log "New version: $version"

    if [ "$DRY_RUN" = true ]; then
        log "[DRY RUN] Would update $CARGO_TOML to version $version"
        log "[DRY RUN] Would build release binaries"
        log "[DRY RUN] Would commit with message 'release v$version'"
        log "[DRY RUN] Would create tag v$version"
        log "[DRY RUN] Would push to origin"
        return
    fi

    update_version "$version"

    log "Building release binaries to verify..."
    build_release

    log "Updating Cargo.lock with new version..."
    cargo fetch --quiet

    commit_changes "$version"
    create_tag "$version"
    push_release "$version"

    log ""
    log "Release v$version published successfully!"
    log "CI/CD will now:"
    log "  1. Build binaries for Linux, macOS, Windows"
    log "  2. Create GitHub Release with binaries"
    log "  3. Publish to crates.io"
}

main "$@"