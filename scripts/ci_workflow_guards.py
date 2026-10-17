#!/usr/bin/env python3
"""Guard the invariants of .github/workflows/ci.yml that each cost a real,
hard-to-read CI incident on the repository's ONE self-hosted macOS runner
(labels `self-hosted,macOS,ARM64,leankg`).

The failure mode shared by all three is that it does not look like a failure:
a job waiting for the single runner slot reports `queued`, which is exactly
what a broken runner reports too, so every one of these was first diagnosed by
hand from runner state on the Mac.

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
import sys

import yaml

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
WORKFLOW = os.path.join(ROOT, ".github", "workflows", "ci.yml")

failures = []


def check(cond, label, why):
    if not cond:
        failures.append(f"{label}\n      {why}")


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

    if failures:
        print("ci.yml guards failed:", file=sys.stderr)
        for item in failures:
            print("  - " + item, file=sys.stderr)
        return 1

    print("ci.yml guards ok: concurrency cancels, every job is capped, npm cache is hosted-only")
    return 0


if __name__ == "__main__":
    sys.exit(main())
