#!/usr/bin/env python3
"""Guard the invariants of .github/workflows/ci.yml that each cost a real,
hard-to-read CI incident on the repository's ONE self-hosted macOS runner
(labels `self-hosted,macOS,ARM64,leankg`).

The failure mode shared by all of them is that it does not look like a
failure: a job waiting for the single runner slot reports `queued`, which is
exactly what a broken runner reports too, so every one of these was first
diagnosed by hand from runner state on the Mac. The last one is worse — a
push that never ran reports NOTHING at all.

From the repository root:

    python3 scripts/ci_workflow_guards.py

Like release_workflow_guards.py, this imports PyYAML, and CI runs on a runner
whose `python3` is Homebrew's, which does not carry it and refuses to install
into itself under PEP 668. The CI step builds a throwaway venv for the one
import rather than assuming a Python on PATH. Locally:

    python3 -m venv /tmp/guardvenv && /tmp/guardvenv/bin/pip install pyyaml
    /tmp/guardvenv/bin/python scripts/ci_workflow_guards.py

Exits non-zero with one readable message per violation.
"""

import os
import re
import sys

import yaml

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
WORKFLOW = os.path.join(ROOT, ".github", "workflows", "ci.yml")

# The files a release-please merge touches. A push to main that changes any
# of them must run the suite: `cmd/leankg/VERSION` is the version source of
# truth and `internal/mcp/server.go` carries the release-please version stamp
# the binary reports. Measured 2026-10-20: the v0.35.0 release commit
# (24e8dbb) changed exactly these four files, the push trigger path-filtered
# them out, and the release shipped untested.
RELEASE_METADATA_FILES = (
    "CHANGELOG.md",
    ".release-please-manifest.json",
    "cmd/leankg/VERSION",
    "internal/mcp/server.go",
)

# The check contexts repository ruleset 20681464 requires on every PR. A
# renamed job would leave the ruleset pointing at a context no run reports,
# and the merge would sit "Expected — Waiting for status to be reported"
# forever; this guard fails the build instead.
REQUIRED_CHECK_CONTEXTS = (
    "Dual Engine (sqlite + live pgvector)",
    "Go Engine (build + vet + test)",
    "Cold TTFV Gate (Go, < 300s)",
    "Frontend (ui-v2 e2e + dashboard unit)",
)

failures = []


def check(cond, label, why):
    if not cond:
        failures.append(f"{label}\n      {why}")


def gh_path_match(pattern, path):
    """GitHub path-filter glob: `*` does not cross `/`, `**` does."""
    regex = []
    i = 0
    while i < len(pattern):
        if pattern.startswith("**", i):
            regex.append(".*")
            i += 2
        elif pattern[i] == "*":
            regex.append("[^/]*")
            i += 1
        elif pattern[i] == "?":
            regex.append("[^/]")
            i += 1
        else:
            regex.append(re.escape(pattern[i]))
            i += 1
    return re.fullmatch("".join(regex), path) is not None


def pattern_covers(pattern, path):
    """True when the pattern matches the file itself or a directory holding it."""
    if gh_path_match(pattern, path):
        return True
    parts = path.split("/")
    for i in range(1, len(parts)):
        prefix = "/".join(parts[:i])
        # GitHub treats a pattern that matches a directory as covering every
        # file under it, with or without the trailing slash.
        if gh_path_match(pattern, prefix) or gh_path_match(pattern, prefix + "/"):
            return True
    return False


def main():
    try:
        with open(WORKFLOW) as fh:
            doc = yaml.safe_load(fh)
    except OSError as exc:
        print(f"ci.yml unreadable: {exc}", file=sys.stderr)
        return 1

    jobs = doc.get("jobs", {})
    if "frontend" not in jobs:
        print("ci.yml no longer has a frontend job; update this guard", file=sys.stderr)
        return 1

    # 1. Superseded pushes must not queue behind a running suite.
    #
    # Measured 2026-10-17: four runs (12 jobs, ~2.5 h of work) sat `queued`
    # behind one 4-job run on the single runner slot, and nothing on the GitHub
    # side distinguishes that from a wedged runner. A workflow-level concurrency
    # group with cancel-in-progress drops the superseded pushes instead.
    concurrency = doc.get("concurrency") or {}
    check(
        bool(concurrency.get("group")) and concurrency.get("cancel-in-progress") is True,
        "ci.yml has no cancel-in-progress concurrency group",
        "every job here needs the one self-hosted runner slot, so an ungrouped push "
        "queues a full extra run behind the live one and reads as a hung runner; add "
        "a workflow-level `concurrency: {group:, cancel-in-progress: true}`.",
    )

    # 2. Every job must be capped.
    #
    # An uncapped wedged job holds the single slot the whole repository shares;
    # `frontend` has no per-step timeout, and `go-engine` relies on its own
    # `-timeout 300s` for the tests but nothing for `npm ci`/playwright.
    for name, job in jobs.items():
        steps = job.get("steps") or []
        capped = bool(job.get("timeout-minutes")) or all(
            step.get("timeout-minutes") for step in steps if isinstance(step, dict)
        )
        check(
            capped,
            f"job {name!r} has no timeout-minutes",
            "one slot serves the whole repository; a hang with no cap leaves every "
            "later run `queued` forever. Set a job-level timeout-minutes.",
        )

    # 3. The self-hosted path must not restore setup-node's npm cache.
    #
    # Measured 2026-10-17: with `cache: npm`, setup-node spent 3m40s-4m38s
    # restoring a 216 MB tarball at ~1 MB/s (runs 38999592068, 39001709926,
    # 39002635228, 39002760777) against a `npm ci` that finishes in 24s from
    # the Mac's own ~/.npm; the post-job save is what hung dsh-mux's runner.
    # The cache stays for GitHub-hosted runners (fork PRs), where it is a win.
    for step in jobs["frontend"].get("steps", []):
        if not isinstance(step, dict) or not str(step.get("uses", "")).startswith("actions/setup-node"):
            continue
        cache_input = str((step.get("with") or {}).get("cache", ""))
        if "github-hosted" in cache_input:
            continue  # expression-guarded: off on this runner
        check(
            "github-hosted" in str(step.get("if", "")),
            "a setup-node step can run cache: npm on the self-hosted runner",
            "the 216 MB npm-cache restore costs minutes on the owner's Mac and its "
            "post-job save has hung a runner; keep that step behind "
            "`if: runner.environment == 'github-hosted'`.",
        )

    # 4. A push to main must be able to reach CI.
    #
    # Measured 2026-10-20: the push trigger carried a `paths-ignore` listing
    # exactly the four files a release-please merge touches, so the v0.35.0
    # release commit (24e8dbb) was filtered out of CI on its way to main
    # (`actions/runs?head_sha=24e8dbb` returns the Release run and no CI run)
    # and the release shipped untested. A filter on this trigger must not be
    # able to cover a release metadata file, and a positive `paths` filter
    # must be able to reach one.
    triggers = doc.get("on", doc.get(True)) or {}
    push = triggers.get("push") or {}
    if not isinstance(push, dict):
        push = {}
    push_branches = push.get("branches")
    push_ignored_branches = push.get("branches-ignore") or []
    check(
        (push_branches is None or "main" in push_branches)
        and "main" not in push_ignored_branches,
        "the push trigger no longer covers main",
        "a push to main is the only run that tests the commit main carries; "
        "keep `branches: [main]` (or no branch filter at all) on the push trigger.",
    )
    ignored = [str(p) for p in (push.get("paths-ignore") or [])]
    included = [str(p) for p in (push.get("paths") or [])]
    for name in RELEASE_METADATA_FILES:
        for pattern in ignored:
            check(
                not pattern_covers(pattern, name),
                f"the push trigger path-ignores {name!r}",
                "release metadata must never be filtered out of CI: a commit that "
                "changes only this file would never be tested, and v0.35.0 shipped "
                "exactly because all four release files were ignored (commit "
                "24e8dbb). Drop the paths-ignore.",
            )
        if included:
            check(
                any(pattern_covers(pattern, name) for pattern in included),
                f"the push trigger's paths filter excludes {name!r}",
                "a positive path filter only runs the workflow when a changed file "
                "matches it, so a commit that changes only this file would never "
                "reach CI.",
            )

    # 5. The jobs the repository ruleset requires must keep their names.
    #
    # Ruleset 20681464 requires these four contexts on every PR. GitHub
    # records a job's check context from the job's `name:`, so renaming one
    # leaves the ruleset waiting for a context no run will ever report.
    job_names = {str(job.get("name") or key) for key, job in jobs.items()}
    for context in REQUIRED_CHECK_CONTEXTS:
        check(
            context in job_names,
            f"no job reports the check context {context!r}",
            "repository ruleset 20681464 requires this context on every PR; a "
            "renamed job would leave the merge waiting for a status that is "
            "never reported.",
        )

    if failures:
        print("ci.yml guards failed:", file=sys.stderr)
        for item in failures:
            print("  - " + item, file=sys.stderr)
        return 1

    print(
        "ci.yml guards ok: concurrency cancels, every job is capped, npm cache is "
        "hosted-only, pushes to main cannot be path-filtered away, required check "
        "contexts are intact"
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
