#!/usr/bin/env python3
"""Guard the invariants of .github/workflows/release.yml that have each already
been broken once. Every one of those breaks produced a GREEN run that published a
release with no binaries — which is precisely what no CI status can catch.

    python3 scripts/release_workflow_guards.py

Exits non-zero with one readable message per violation.
"""

import os
import sys

import yaml

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
WORKFLOW = os.path.join(ROOT, ".github", "workflows", "release.yml")

failures = []


def check(cond, label, why):
    if not cond:
        failures.append(f"{label}\n      {why}")


def main():
    try:
        with open(WORKFLOW) as fh:
            doc = yaml.safe_load(fh)
    except OSError as exc:
        print(f"release.yml unreadable: {exc}", file=sys.stderr)
        return 1

    jobs = doc.get("jobs", {})
    if "release-please" not in yaml.dump(doc):
        print("release.yml no longer runs release-please; update this guard", file=sys.stderr)
        return 1

    # 1. Nothing may gate a job on the `inputs` context.
    #
    # On a workflow_dispatch run `${{ inputs.version }}` evaluates EMPTY inside a
    # job-level `if`, even though the same expression resolves correctly inside a
    # step's `env`. Runs 34805275505 and 34806151322 both went green with every
    # downstream job skipped for exactly this reason, leaving a release with no
    # assets. `inputs` is legal only in a step, where plan turns it into a step
    # output that the other jobs can compare.
    for name, job in jobs.items():
        check(
            "inputs." not in str(job.get("if", "")),
            f"job {name!r} gates on the inputs context",
            "inputs.* is empty in a job-level `if` on workflow_dispatch, so the whole "
            "asset chain skips and the run still reports success; gate on a needs-output.",
        )

    # The publish step's shell script. A `run: |` block is a single YAML scalar, so
    # the line-oriented checks below read the script text, not a re-dumped job.
    scripts = "\n".join(
        str(step.get("run", ""))
        for step in jobs.get("publish", {}).get("steps", [])
        if isinstance(step, dict)
    )
    check(bool(scripts), "publish exposes no shell script to inspect", "the guard cannot do its job")
    lines = scripts.splitlines()

    # 2. Publish must fail the run when the tarballs never landed.
    # v0.28.1, v0.29.0, v0.30.0 and v0.31.0 were each tagged with zero assets.
    # Shape to preserve: query the release's asset count, compare it, exit non-zero.
    # Matching bare "assets" would pass on a comment, so all three parts are needed.
    counts = [ln for ln in lines if "assets" in ln and "length" in ln]
    compares = [ln for ln in lines if "-lt" in ln]
    check(
        bool(counts) and bool(compares) and "exit 1" in scripts,
        "publish no longer asserts the release carries its assets",
        "a green run must be impossible when a release ends up with no binaries: query "
        "the release's asset count, compare it with -lt, and exit 1 below the number.",
    )

    # 3. `latest` may not be claimed unconditionally.
    # The workflow_dispatch path re-publishes an OLDER release; marking that
    # --latest moves the pointer `leankg update` follows backwards, silently
    # downgrading every client. The flag must ride on the compared latest_flag.
    draft_edits = [ln for ln in lines if "gh release edit" in ln and "--draft=false" in ln]
    check(
        bool(draft_edits) and all("latest_flag" in ln for ln in draft_edits),
        "publish claims `--latest` unconditionally",
        "re-publishing an older release must not steal releases/latest from the newest "
        "one; pass the flag through latest_flag, which is emptied when the version is "
        "older than the current latest.",
    )

    if failures:
        print("release.yml guards failed:", file=sys.stderr)
        for item in failures:
            print("  - " + item, file=sys.stderr)
        return 1

    print("release.yml guards ok: no inputs-gated jobs, asset count asserted, latest guarded")
    return 0


if __name__ == "__main__":
    sys.exit(main())
