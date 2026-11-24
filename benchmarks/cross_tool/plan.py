#!/usr/bin/env python3
"""Dry-run plan and per-arm readiness gate for the cross-tool benchmark.

Validates repos.yaml, repos.lock.yaml and tools.lock.yaml, checks the binaries
and clones, and prints the plan (arms x repos x prompts x trials). It runs no
agents and makes no API call.

  python3 plan.py --repos-dir repos --leankg-bin ../../bin/leankg --n 4 --model <id>
  python3 plan.py --require-ready --arm leankg ...   # exit 3 while a blocker applies
  python3 plan.py --selftest                         # offline checks, incl. arm wiring

Exit codes: 0 plan printed; 1 invalid configuration; 3 a blocker applies to the
arm named by --require-ready.
"""
from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

try:
    import yaml
except ImportError:  # pragma: no cover - same prereq as the rest of the harness
    yaml = None

HERE = Path(__file__).resolve().parent
ARMS = ("baseline", "context-mode", "leankg")
SHA_RE = re.compile(r"^[0-9a-f]{40}$")
PLACEHOLDER = "PLACEHOLDER"
CONFIG_SCOPE = "config"  # a blocker that applies to every arm


def load_yaml(path: Path) -> dict:
    return yaml.safe_load(path.read_text(encoding="utf-8")) or {}


def node_version() -> str:
    try:
        out = subprocess.run(["node", "--version"], capture_output=True, text=True, check=True)
        return out.stdout.strip()
    except (OSError, subprocess.CalledProcessError):
        return ""


def parse_semver(text: str) -> tuple[int, ...] | None:
    m = re.match(r"v?(\d+)\.(\d+)\.(\d+)", text.strip())
    return tuple(int(x) for x in m.groups()) if m else None


def git_head(path: Path) -> str | None:
    if not path.is_dir():
        return None
    try:
        out = subprocess.run(["git", "-C", str(path), "rev-parse", "HEAD"], capture_output=True, text=True, check=True)
        return out.stdout.strip()
    except (OSError, subprocess.CalledProcessError):
        return None


def assess(args: argparse.Namespace, env: dict[str, str], node_text: str) -> tuple[list[str], list[tuple[str, str]], list[dict]]:
    """Return (errors, blockers, rows). A blocker is (scope, message) where scope
    is CONFIG_SCOPE or the arm it stops."""
    errors: list[str] = []
    blockers: list[tuple[str, str]] = []
    repos = load_yaml(args.repos).get("repos", [])
    slugs = [r.get("slug") for r in repos]
    if len(set(slugs)) != len(slugs):
        errors.append("repos.yaml has duplicate slugs")

    if not args.lock.is_file():
        blockers.append((CONFIG_SCOPE, "repos.lock.yaml missing (run: make lock)"))
        lock: dict = {}
    else:
        lock = load_yaml(args.lock)
    tools = load_yaml(args.tools) if args.tools.is_file() else {}
    if not args.tools.is_file():
        blockers.append((CONFIG_SCOPE, "tools.lock.yaml missing"))

    rows: list[dict] = []
    for repo in repos:
        slug, ref = repo.get("slug", "?"), repo.get("ref", "")
        info = lock.get(slug) or {}
        sha = info.get("sha", "")
        if not info:
            blockers.append((CONFIG_SCOPE, f"{slug}: no entry in repos.lock.yaml (run: make lock)"))
            status = "no lock"
        elif info.get("ref") != ref:
            errors.append(f"{slug}: repos.lock.yaml ref {info.get('ref')!r} != repos.yaml {ref!r} (run: make lock)")
            status = "stale lock"
        elif not SHA_RE.match(sha):
            blockers.append((CONFIG_SCOPE, f"{slug}: ref {ref!r} unresolved ({info.get('error', 'no sha')})"))
            status = "UNRESOLVED"
        else:
            status = "locked"
        head = git_head(args.repos_dir / slug)
        if head is None:
            blockers.append((CONFIG_SCOPE, f"{slug}: not cloned (run: make setup)"))
            clone = "not cloned"
        elif SHA_RE.match(sha) and head != sha:
            blockers.append((CONFIG_SCOPE, f"{slug}: clone HEAD {head[:10]} != locked {sha[:10]}"))
            clone = "HEAD drift"
        else:
            clone = "at locked sha" if SHA_RE.match(sha) else "present"
        rows.append({"slug": slug, "ref": ref, "sha": sha, "prompt_version": repo.get("prompt_version", 1),
                     "status": status, "clone": clone})

    model_pin = str((tools.get("model") or {}).get("id", "") or "")
    if not args.model:
        blockers.append((CONFIG_SCOPE, "MODEL not set; pass MODEL=<pinned id from tools.lock.yaml>"))
    elif args.model != model_pin:
        blockers.append((CONFIG_SCOPE, f"MODEL={args.model} != tools.lock.yaml model.id ({model_pin or 'unset'})"))
    if model_pin in ("", PLACEHOLDER):
        blockers.append((CONFIG_SCOPE, "tools.lock.yaml model.id is PLACEHOLDER"))
    if str((tools.get("claude_code") or {}).get("version", "") or "") in ("", PLACEHOLDER):
        blockers.append((CONFIG_SCOPE, "tools.lock.yaml claude_code.version is PLACEHOLDER"))
    if not shutil.which(env.get("CLAUDE_BIN") or "claude"):
        blockers.append((CONFIG_SCOPE, "claude CLI not on PATH"))

    leankg_bin = Path(args.leankg_bin)
    if not (leankg_bin.is_file() and os.access(leankg_bin, os.X_OK)):
        blockers.append(("leankg", f"leankg binary not executable at {leankg_bin} (run: make go-build at the repo root)"))

    cm = tools.get("context_mode") or {}
    if not SHA_RE.match(str(cm.get("sha", ""))):
        blockers.append(("context-mode", "tools.lock.yaml context_mode.sha is not a 40-hex commit"))
    server = env.get("CONTEXT_MODE_SERVER", "")
    if not (server and Path(server).is_file()):
        blockers.append(("context-mode", "CONTEXT_MODE_SERVER is not an existing server.mjs (see RUNNING.md)"))
    need = parse_semver(str(cm.get("node_min", "")))
    have = parse_semver(node_text)
    if need and (have is None or have < need):
        blockers.append(("context-mode", f"node {node_text or 'not found'} < required {cm.get('node_min')}"))
    return errors, blockers, rows


def gate_blocks(blockers: list[tuple[str, str]], arm: str) -> list[str]:
    scopes = set(ARMS) if arm == "all" else {arm}
    return [msg for scope, msg in blockers if scope == CONFIG_SCOPE or scope in scopes]


def print_plan(args: argparse.Namespace, errors: list[str], blockers: list[tuple[str, str]], rows: list[dict]) -> None:
    arms = ", ".join(ARMS)
    invocations = len(ARMS) * len(rows) * args.n
    print("cross_tool plan (dry run: no agents, no API calls)")
    print(f"arms:      {arms}")
    print(f"trials:    N={args.n} per arm per repo (scorer floor: 3)")
    print(f"model:     {args.model or 'UNSET'}")
    print(f"repos:     {len(rows)}   prompts: one per repo (repos.yaml prompt, prompt_version)")
    print("")
    print(f"{'repo':<12}{'ref':<22}{'locked sha':<13}{'pv':<4}{'status':<12}clone")
    for r in rows:
        print(f"{r['slug']:<12}{str(r['ref']):<22}{(r['sha'][:10] or '-'):<13}{str(r['prompt_version']):<4}{r['status']:<12}{r['clone']}")
    print("")
    print(f"invocations: {len(ARMS)} arms x {len(rows)} repos x {args.n} trials = {invocations} claude -p runs")
    print(f"judge:       {len(rows)} claude -p calls (score.py, one per repo, judge-blind)")
    print("cost:        per-run cost is not measured here; multiply the invocation count by the")
    print("             measured median from a pilot run before approving (see RUNNING.md)")
    for e in errors:
        print(f"ERROR: {e}")
    print(f"blockers ({len(blockers)}):" if blockers else "blockers: none")
    for scope, msg in blockers:
        print(f"  [{scope}] {msg}")
    print(f"ready for a real run: {'NO' if blockers or errors else 'YES'}")


def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--repos", type=Path, default=HERE / "repos.yaml")
    p.add_argument("--lock", type=Path, default=HERE / "repos.lock.yaml")
    p.add_argument("--tools", type=Path, default=HERE / "tools.lock.yaml")
    p.add_argument("--repos-dir", type=Path, default=HERE / "repos")
    p.add_argument("--leankg-bin", default=str(HERE / ".." / ".." / "bin" / "leankg"))
    p.add_argument("--model", default="")
    p.add_argument("--n", type=int, default=4)
    p.add_argument("--arm", default="all", choices=("all",) + ARMS)
    p.add_argument("--require-ready", action="store_true",
                   help="exit 3 while a blocker applies to --arm (used by the real-run targets)")
    p.add_argument("--selftest", action="store_true", help="offline checks; no network, no agents")
    return p


def selftest() -> int:
    if yaml is None:
        print("ERROR: PyYAML required (pip install pyyaml). If make's python3 lacks it, rerun with PYTHON=<python that has PyYAML>; see RUNNING.md section 1.", file=sys.stderr)
        return 2
    with tempfile.TemporaryDirectory() as tmp:
        root = Path(tmp)
        exe = root / "leankg"
        exe.write_text("#!/bin/sh\n", encoding="utf-8")
        exe.chmod(0o755)
        server = root / "server.mjs"
        server.write_text("// stub\n", encoding="utf-8")

        # 1. Every arm name is accepted by the bash wiring and yields the right MCP shape.
        env = dict(os.environ, LEANKG_BIN=str(exe), CONTEXT_MODE_SERVER=str(server))
        expect = {"baseline": set(), "context-mode": {"context-mode"}, "leankg": {"leankg"}}
        for arm in ARMS:
            out = root / f"mcp-{arm}.json"
            res = subprocess.run(["bash", str(HERE / "install_leankg_mcp.sh"), str(out), arm],
                                 env=env, capture_output=True, text=True)
            assert res.returncode == 0, f"install_leankg_mcp.sh {arm}: {res.stderr.strip()}"
            servers = set(json.loads(out.read_text(encoding="utf-8"))["mcpServers"])
            assert servers == expect[arm], f"{arm}: mcpServers {servers} != {expect[arm]}"
        bad = subprocess.run(["bash", str(HERE / "install_leankg_mcp.sh"), str(root / "x.json"), "with"],
                             env=env, capture_output=True, text=True)
        assert bad.returncode != 0, "retired arm name 'with' must be refused"

        # 2. Pins: a stale lock, an unresolved ref and a placeholder model are each caught.
        repos = root / "repos.yaml"
        repos.write_text(yaml.safe_dump({"repos": [
            {"slug": "gin", "url": "u", "ref": "v1.10.0", "prompt": "p", "prompt_version": 1}]}), encoding="utf-8")
        tools = root / "tools.lock.yaml"
        tools.write_text(yaml.safe_dump({"context_mode": {"sha": "a" * 40, "node_min": "24.15.0"},
                                         "claude_code": {"version": PLACEHOLDER},
                                         "model": {"id": PLACEHOLDER}}), encoding="utf-8")
        lock = root / "repos.lock.yaml"
        ns = build_parser().parse_args(["--repos", str(repos), "--lock", str(lock), "--tools", str(tools),
                                        "--repos-dir", str(root / "none"), "--leankg-bin", str(exe),
                                        "--model", "m-x", "--n", "4"])

        lock.write_text(yaml.safe_dump({"gin": {"url": "u", "ref": "v1.9.0", "sha": "b" * 40}}), encoding="utf-8")
        errors, blockers, _ = assess(ns, env, "v24.15.0")
        assert any("stale" in e or "!=" in e for e in errors), f"stale lock not reported as error: {errors}"

        lock.write_text(yaml.safe_dump({"gin": {"url": "u", "ref": "v1.10.0", "sha": "UNRESOLVED",
                                                "error": "no ref"}}), encoding="utf-8")
        errors, blockers, _ = assess(ns, env, "v24.15.0")
        msgs = " | ".join(m for _, m in blockers)
        assert "unresolved" in msgs and "PLACEHOLDER" in msgs and "model" in msgs, msgs
        assert gate_blocks(blockers, "baseline"), "placeholder pins must block the baseline arm"

        # 3. A resolved, cloned repo with a real model pin clears the config blockers.
        lock.write_text(yaml.safe_dump({"gin": {"url": "u", "ref": "v1.10.0", "sha": "c" * 40}}), encoding="utf-8")
        tools.write_text(yaml.safe_dump({"context_mode": {"sha": "a" * 40, "node_min": "24.15.0"},
                                         "claude_code": {"version": "2.1.201"},
                                         "model": {"id": "m-x"}}), encoding="utf-8")
        no_clone = assess(ns, env, "v24.15.0")[1]
        assert any("not cloned" in m for _, m in no_clone), no_clone
        # 4. The context-mode arm is blocked by a too-old node, and only that arm.
        ns_cm = build_parser().parse_args(["--repos", str(repos), "--lock", str(lock), "--tools", str(tools),
                                           "--repos-dir", str(root / "none"), "--leankg-bin", str(exe),
                                           "--model", "m-x", "--n", "4", "--arm", "context-mode"])
        old_node = assess(ns_cm, env, "v22.23.2")[1]
        assert any("node v22.23.2" in m for _, m in old_node), old_node
        assert not any("node v" in m for _, m in assess(ns_cm, env, "v24.16.0")[1]), "node 24.16 must pass"
        assert gate_blocks(old_node, "baseline") == [m for scope, m in old_node if scope == CONFIG_SCOPE]
        assert not any("node v" in m for m in gate_blocks(old_node, "leankg")), "node floor is context-mode only"

    print("selftest: OK (arm wiring for baseline/context-mode/leankg, retired 'with' refused, "
          "stale/unresolved/placeholder pins, arm-scoped gate, node floor)")
    return 0


def main() -> int:
    args = build_parser().parse_args()
    if args.selftest:
        return selftest()
    if yaml is None:
        print("ERROR: PyYAML is required for plan.py (pip install pyyaml). If make's python3 lacks it, rerun with PYTHON=<python that has PyYAML>; see RUNNING.md section 1.", file=sys.stderr)
        return 2
    env = dict(os.environ)
    errors, blockers, rows = assess(args, env, node_version())
    print_plan(args, errors, blockers, rows)
    if errors:
        return 1
    if args.require_ready and gate_blocks(blockers, args.arm):
        return 3
    return 0


if __name__ == "__main__":
    sys.exit(main())
