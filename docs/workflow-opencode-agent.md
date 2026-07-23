# LeanKG Development Workflow for OpenCode AI Agent

## Overview

This document defines the workflow pattern for OpenCode AI agent to implement features in LeanKG. Each feature implementation follows a structured process: **Update Docs → Implement → Test → Commit → Create PR → Review & Merge → Release**.

The release step (Step 8) is now automated through the
[`.github/workflows/release-please.yml`](../../.github/workflows/release-please.yml)
workflow once the change is merged to `main` and the CI quality checks pass.
Release Please is configured through two repository files,
[`release-please-config.json`](../../release-please-config.json) and
[`manifest.json`](../../manifest.json), at the repo root.
See [Automated Releases](#automated-releases) for the end-to-end process.

## Core Principle: One Feature Per Branch

Every distinct feature or fix should be:
1. Documented before implementation
2. Implemented in isolation on a dedicated branch
3. Tested
4. Committed with a clear message
5. Pushed and PR created via gh
6. Reviewed and merged via gh
7. Released as a new version after merge

---

## Automated Releases

LeanKG uses an automated semantic-version release pipeline driven by GitHub
Actions and the [Release Please](https://github.com/googleapis/release-please)
action. The pipeline replaces the manual version-bump + tag-push steps that
used to live in `Step 8`.

### Versioning policy

LeanKG follows [Semantic Versioning 2.0](https://semver.org/) `vX.Y.Z`:

| Bump | Trigger | Examples |
|------|---------|----------|
| Minor (`Y`) | `feat:` commits merged to `main` | `v1.8.3` → `v1.9.0` |
| Patch (`Z`) | `fix:`, `perf:`, or other release-eligible conventional commits | `v1.8.3` → `v1.8.4` |
| Major (`X`) | **Never auto-incremented.** A maintainer opens a release PR and updates `[package].version` in `Cargo.toml` to the next major by hand | `v1.8.3` → `v2.0.0` |

Release Please treats `feat:` as feature and `fix:`, `perf:`, `refactor:` as
patch-eligible. `docs:`, `chore:`, `test:`, `style:`, `ci:` commits do **not**
trigger a release by default; they are bundled into the next release PR that
contains a `feat:` or `fix:` commit.

### Pipeline at a glance

```text
push to main / merge of PR
   └── CI: cargo test --lib, cargo fmt --check, cargo clippy, ui-v2 build
        └── Release Please opens / updates release PR
             ├── bumps Cargo.toml version (minor or patch only)
             ├── appends an entry to CHANGELOG.md
             └── opens the GitHub Release on merge of the release PR
```

### How it works

1. **Conventional commits.** Every merge to `main` must use a
   [Conventional Commits](https://www.conventionalcommits.org/) prefix:
   `feat:`, `fix:`, `perf:`, `refactor:`, `docs:`, `chore:`, `test:`,
   `style:`, `ci:`, or `build:`. A scope (`feat(cli): ...`) is recommended but
   optional. Any commit whose body contains `BREAKING CHANGE:` is ignored by
   this pipeline — major releases are handled manually.
2. **Release Please runs on push to `main`** once CI is green. It scans the
   commits since the last `v*` tag, calculates the next minor or patch
   version, and opens (or updates) the **release PR**.
3. **Release PR.** The release PR updates `Cargo.toml`, `Cargo.lock`, and
   `CHANGELOG.md`. Reviewers check the version bump and changelog, then merge
   the release PR with squash.
4. **Release publication.** On merge of the release PR, Release Please creates
   an annotated `vX.Y.Z` tag and publishes a GitHub Release with the
   generated notes.

### Files touched by the pipeline

| File | Action | Source |
|------|--------|--------|
| `Cargo.toml` | bumps `[package].version` | Release Please via `cargo` strategy |
| `Cargo.lock` | refreshed via `cargo build` in the workflow | Release Please |
| `CHANGELOG.md` | appends a release section with categorized commits | Release Please |

### Release Please configuration files

Release Please v4 is configured through the files below, not through inline
workflow inputs. Editing these files is how the pipeline is customized.

| File | Purpose |
|------|---------|
| [`release-please-config.json`](../../release-please-config.json) | Top-level config: bump policy, package definitions, changelog sections, exclude types, release-PR branch / labels / body |
| [`manifest.json`](../../manifest.json) | Maps the repository path (`.`) to the **current semantic-version string** (e.g. `".": "0.19.4"`). Values are strings, not objects. Update this value whenever `Cargo.toml`'s `[package].version` is bumped outside of Release Please. |

The `.github/workflows/release-please.yml` workflow only declares supported
action inputs: `token`, `config-file`, `manifest-file`, and `target-branch`.
Inline inputs from earlier action revisions (`release-type`, `package-name`,
`version-file`, `draft`, `config`) are rejected by Release Please v4 and
must live in the config/manifest files instead.

### Required permissions and secrets

- Repository secret `GITHUB_TOKEN` (automatically provided by GitHub
  Actions) must allow `contents: write` and `pull-requests: write`. The
  workflow declares the permissions it needs explicitly and does not request
  any extra scopes.
- No additional secret is required for the default setup. If the release job
  needs to publish to crates.io later, add the `CARGO_REGISTRY_TOKEN` secret.

### Required repository settings

- Branch protection on `main` must require status checks from the `CI`
  workflow (`Test Suite`, `Format Check`, `Clippy Lints`, `UI v2 Typecheck`).
- The release PR is opened against `main`; `main` must accept squash merges
  so the linear history is preserved.

### Conventional-commit cheatsheet for LeanKG

| Commit type | Triggers release? | Allowed scopes |
|-------------|-------------------|----------------|
| `feat:` | Yes — minor bump | `cli`, `mcp`, `web`, `indexer`, `graph`, `db`, `embed`, `ui-v2`, `release` |
| `fix:` | Yes — patch bump | same as above |
| `perf:` | Yes — patch bump | same as above |
| `refactor:` | Yes — patch bump | same as above |
| `docs:` | No | `readme`, `prd`, `cli-reference`, `mcp-tools` |
| `chore:` | No | `deps`, `tooling` |
| `test:` | No | unit, integration, e2e |
| `ci:` | No | `ci`, `release` |
| `build:` | No | `cargo`, `docker` |
| `style:` | No | rustfmt, ui styling |

### Local verification before pushing

```bash
# Confirm the commit subject matches the convention
git log --oneline -5

# Run the same checks CI runs locally
cargo fmt --all -- --check
cargo clippy --all -- -D warnings
cargo test --lib

# Build the UI v2 assets (if changed)
(cd ui-v2 && npm ci && npm run build)
```

If a commit message is malformed, amend it (`git commit --amend`) before
pushing — once the change reaches `main`, Release Please will skip it.

### Handling a major release (manual)

1. Open a PR titled `feat!: start vX.0.0 development cycle` (or include
   `BREAKING CHANGE:` in the body) that updates `[package].version` in
   `Cargo.toml` to `X.0.0` and adds a new `## [X.0.0]` section to
   `CHANGELOG.md` summarizing the breaking changes.
2. Wait for the `CI` workflow to pass.
3. Merge the PR with squash. Release Please will detect the manual version
   bump, create the `vX.0.0` tag, and publish the GitHub Release without
   recomputing the version.

### Rollback or hotfix release

```bash
# Cherry-pick the fix on a branch off the existing tag
git checkout -b fix/<short-description> vX.Y.Z
git cherry-pick <commit-sha>

# Open a PR with the conventional prefix `fix:` or `perf:`
# Release Please will produce a vX.Y.(Z+1) release PR on merge to main
```

Do not rebase or rewrite tags that already exist on the remote; Release Please
will detect the divergence and refuse to publish until the inconsistency is
resolved.

### Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| Release Please opens no PR after a merge | The commits since the last tag only contain `docs:`, `chore:`, `test:`, `style:`, `ci:`, `build:` | Wait for the next `feat:` or `fix:` commit, or open the release manually |
| Release PR bumps the wrong version | A previous commit was rewritten or the tag is missing on `origin` | Confirm `git ls-remote --tags origin | grep vX.Y.Z` returns the expected tag; re-tag locally and push |
| `cargo build` fails in the release PR | The Cargo.lock change is out of sync with the workspace | Re-run `cargo build --release` locally, commit the lockfile, and push |
| Workflow fails with `403 Forbidden` on Release Please | The `GITHUB_TOKEN` lacks `contents: write` | Update the workflow's `permissions:` block and the repository settings |
| Release Please fails with `Unknown release type: cargo` or `Unexpected input(s) 'package-name', 'version-file', 'draft', 'config'` | The workflow uses inline action inputs that Release Please v4 rejects | Move `release-type`, `package-name`, `version-file`, changelog sections, and exclude types into `release-please-config.json` and `manifest.json`, and pass only `config-file` and `manifest-file` from the workflow |
| Release Please fails with `versionString.match is not a function` | `manifest.json` maps the package path to an object instead of the current version string | Make `manifest.json["."]` a string equal to `[package].version` in `Cargo.toml` (e.g. `".": "0.19.4"`); package metadata belongs in `release-please-config.json`, not in the manifest |

## Standard Feature Implementation Workflow

### Step 0: Understand the Task

Before doing anything:
1. Explore the codebase to understand current structure
2. Read existing relevant code and documentation
3. Understand the data models and relationships
4. Identify where changes need to be made

```bash
# Use explore agent for large-scale understanding
task(description="Explore LeanKG codebase", subagent_type="explore", prompt="...")

# Use Read/grep for targeted understanding
read(filePath="src/db/models.rs")
grep(pattern="BusinessLogic", path="src")
```

### Step 1: Update Documentation (PRD → HLD → README)

**Always update documentation BEFORE writing any code.**

#### 1.1 Update consolidated PRD+HLD (`docs/prd.md`)

Edit the single SoT document. Add/update user stories, FRs, and HLD sections (§6) as needed. Do **not** recreate `docs/requirement/prd-*.md` or `docs/design/hld-leankg.md`.

- Bump version number and update changelog
- Add new User Story (US-XX)
- Add new Functional Requirements (FR-XX)
- Update HLD diagrams / data flows in §6 when architecture changes
- Update roadmap if needed
- Add new terms to glossary

```markdown
**Changelog:**
- v1.X - New Feature: Feature name
  - US-XX: User story description
  - FR-XX to FR-XX: New functional requirements
```

#### 1.2 Update related docs

Update `docs/roadmap.md`, `docs/mcp-tools.md`, `docs/cli-reference.md`, and `README.md` when commands or tools change.

#### 1.3 Update README

- Add feature to Features table
- Add new CLI commands to CLI Commands table
- Add new MCP tools to MCP Tools table
- Update verification status table
- Update project structure if adding new modules

### Step 2: Implement the Feature

#### 2.1 For New Modules

```bash
# Create module directory
mkdir -p src/new_module/
```

Create `src/new_module/mod.rs` with:
- Data structures (models)
- Public API functions
- Integration with existing modules

#### 2.2 For Existing Modules

Follow existing code patterns:
- Use same error handling style
- Match naming conventions
- Follow existing function signatures

#### 2.3 Key Files to Modify

| File | Purpose |
|------|---------|
| `src/lib.rs` | Add `pub mod new_module;` |
| `Cargo.toml` | Add dependencies |
| `src/db/models.rs` | Add new data structures |
| `src/db/mod.rs` | Add database operations |
| `src/graph/query.rs` | Add graph query methods |
| `src/mcp/tools.rs` | Add MCP tool definitions |
| `src/mcp/handler.rs` | Add tool execution handlers |

### Step 3: Build and Test

```bash
# Build to catch compilation errors
cargo build 2>&1 | head -50

# If errors, fix them and rebuild
# Common issues:
# - Missing imports
# - Private field access (add getter methods to GraphEngine)
# - Type mismatches
# - Method not found errors

# Run tests
cargo test 2>&1 | tail -30

# Fix any failing tests
```

### Step 4: Commit with Clear Message

Follow conventional commit format:

```bash
git add -A
git commit -m "feat|fix|docs|chore: Brief description

Detailed explanation of what was done.
- Added new functionality X
- Fixed issue Y
- Updated Z"
```

**Commit types:**
- `feat:` New feature
- `fix:` Bug fix
- `docs:` Documentation only
- `chore:` Build/tooling changes

### Step 5: Create Branch and Push

```bash
# Create a new branch for this feature
git checkout -b feature/<ticket-id>-short-description

# Push the branch to origin
git push -u origin feature/<ticket-id>-short-description
```

### Step 6: Create Pull Request via gh

```bash
# Create PR to main branch
gh pr create --title "feat: Short description" --body "$(cat <<'EOF'
## Summary
- Brief description of what changed
- Key changes made

## Test Plan
- [ ] cargo build passes
- [ ] cargo test passes
- [ ] Manual verification steps (if applicable)

## Checklist
- [ ] Documentation updated (PRD, HLD, README)
- [ ] Code follows existing patterns
- [ ] No debug/placeholder code left in
EOF
)"
```

### Step 7: Review and Merge via gh

After PR is created:

```bash
# View PR details
gh pr view

# Check PR diff
gh pr diff

# Merge the PR (squash merge)
gh pr merge --squash --delete-branch

# Alternative: Merge with merge commit
# gh pr merge --admin --delete-branch
```

For a manual major release, follow the [Handling a major release (manual)](#handling-a-major-release-manual)
section instead of merging the release PR normally.

### Step 8: Release New Version

Releases are produced automatically by the
[`.github/workflows/release-please.yml`](../../.github/workflows/release-please.yml)
workflow on every push to `main`. The merge of the **release PR** is what
publishes the tag and the GitHub Release.

Manual actions required after merge of a feature PR:

1. Wait for the `CI` workflow to finish green on `main`.
2. Wait for Release Please to open (or update) a release PR with the new
   version in `Cargo.toml` and a new section in `CHANGELOG.md`.
3. Review the release PR: verify the version bump level (minor vs. patch) and
   the changelog entries are correct.
4. Merge the release PR with squash. Release Please will create the
   `vX.Y.Z` tag and the GitHub Release automatically.

For a major release, follow the [Handling a major release (manual)](#handling-a-major-release-manual)
section instead.

---

## LeanKG-Specific Patterns

### Adding a New Data Model

1. Add struct to `src/db/models.rs`:

```rust
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct NewModel {
    pub id: Option<String>,
    pub name: String,
    pub related_qualified: Option<String>,
    pub metadata: serde_json::Value,
}
```

2. Add database operations to `src/db/mod.rs`
3. Add query methods to `src/graph/query.rs`

### Adding a New Relationship Type

1. Store relationship with descriptive metadata:

```rust
relationships.push(Relationship {
    id: None,
    source_qualified: source,
    target_qualified: target,
    rel_type: "new_relationship".to_string(),
    metadata: serde_json::json!({
        "context": "description",
        "line": line_number,
    }),
});
```

### Adding a New MCP Tool

1. Define tool in `src/mcp/tools.rs`:

```rust
ToolDefinition {
    name: "new_tool".to_string(),
    description: "Description".to_string(),
    input_schema: json!({
        "type": "object",
        "properties": {
            "param": {"type": "string"}
        }
    }),
}
```

2. Add handler method in `src/mcp/handler.rs`:

```rust
fn new_tool(&self, args: &Value) -> Result<Value, String> {
    let param = args["param"].as_str().ok_or("Missing 'param'")?;
    // Implementation
    Ok(json!({ "result": result }))
}
```

3. Add match arm in `execute_tool`:

```rust
"new_tool" => self.new_tool(arguments),
```

### Adding CLI Commands

CLI commands are defined in `src/cli/mod.rs` using Clap. Follow existing command patterns.

---

## Handling Git Rebase Conflicts

When `git pull --rebase` shows conflicts:

```bash
# See conflicted files
git diff --name-only --diff-filter=U

# View conflict
git diff README.md | head -50

# Read file to see conflict markers
read(filePath="README.md", offset=100, limit=50)

# Edit to resolve conflict
edit(filePath="README.md", oldString="<<<<<<< HEAD\n=======\n<<<<<<< commit", newString="resolved content")

# Continue rebase
git add README.md
GIT_EDITOR="cat" git rebase --continue
```

---

## Quality Checklist

Before creating PR, verify:

- [ ] Documentation updated (PRD, HLD, README)
- [ ] Code compiles without errors
- [ ] Tests pass
- [ ] New code follows existing patterns
- [ ] No debug/placeholder code left in
- [ ] Commit message is clear
- [ ] Branch name follows convention (feature/<ticket>-description)
- [ ] PR created with clear title and description

Before merging, verify:
- [ ] PR title follows conventional commits (feat:, fix:, etc.)
- [ ] Review completed (self-review or code review)
- [ ] All checks pass

After merging, verify:
- [ ] Version bumped in Cargo.toml
- [ ] Tag created and pushed

---

## Example: Complete Feature Workflow

```bash
# 1. Understand
task(description="Explore db module", prompt="Explore src/db/ to understand data models...")

# 2. Update docs first
edit(filePath="docs/prd.md", oldString="...", newString="...")
# (optional) docs/roadmap.md, docs/mcp-tools.md, README.md

edit(filePath="README.md", oldString="...", newString="...")

# 3. Implement
write(content="...", filePath="src/new_module/mod.rs")
edit(filePath="src/lib.rs", oldString="...", newString="...")

# 4. Build and test
cargo build
cargo test

# 5. Commit
git add -A
git commit -m "feat: Add new feature

- Added new module for X
- Implemented Y functionality
- Added Z relationship type"

# 6. Create branch and push
git checkout -b feature/US-XX-new-feature
git push -u origin feature/US-XX-new-feature

# 7. Create PR
gh pr create --title "feat: Add new feature" --body "..."

# 8. Review and merge
gh pr merge --squash --delete-branch

# 9. Release
git checkout main
git pull origin main
# Edit Cargo.toml version
git add -A
git commit -m "release: Bump version to X.Y.Z"
git push origin main
git tag -a vX.Y.Z -m "Release vX.Y.Z"
git push origin vX.Y.Z
```

---

## Quick Reference Commands

```bash
# Build
cargo build 2>&1 | tail -20

# Test
cargo test 2>&1 | tail -30

# Full test with output
cargo test 2>&1

# Check git status
git status

# See recent commits
git log --oneline -5

# Stash changes
git stash

# Pop stash
git stash pop

# GitHub CLI (gh) Commands
gh pr create --title "feat: Description" --body "..."
gh pr view
gh pr diff
gh pr merge --squash --delete-branch
gh pr checkout <branch>   # Checkout PR branch locally
gh pr merge --admin       # Merge with merge commit
gh pr merge --rebase      # Merge with rebase
gh release list
gh release create vX.Y.Z --notes "Release notes"
```

---

## Document Revision

**Version:** 1.3  
**Date:** 2026-07-23  
**Change:** Clarified that `manifest.json` values must be version strings
(not objects), added the troubleshooting row for
`versionString.match is not a function`, and updated the manifest entry
description in the configuration table.  
**Based on:** LeanKG Phase 2 implementation session
