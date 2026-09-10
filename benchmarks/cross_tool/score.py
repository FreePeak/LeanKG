#!/usr/bin/env python3
"""Judge-blind answer scorer for the cross-tool benchmark (FR-ZCP-08).

Scores answer quality for every valid run WITHOUT letting the judge see the
arm label. For each repo:

1. Collect the persisted answer texts (`<scratch>/claude.json.answer.txt`,
   written by run_one.sh) for valid runs of both arms.
2. Shuffle and relabel them A, B, C, ... The label->(repo, arm, run_idx)
   mapping lives only in this process and is applied AFTER scoring.
3. Ask the judge (`claude -p`, same CLI the agents use) to grade each
   anonymized answer on a fixed rubric, emitting strict JSON.
4. De-anonymize and append one row per scored run to
   `results/scores/scores.jsonl`.

The judge prompt is built exclusively from (question, answers): no arm names,
no metadata, randomized order. That is the "judge-blind" property — it holds
structurally, not by convention.

Rubric (0-2 each, max 6):
  correctness — does it describe the real data/control flow?
  grounding   — does it cite concrete files/symbols that plausibly exist?
  depth       — mechanism detail beyond a surface summary?

Usage:
  python3 score.py --results results/ --repos repos.yaml [--model sonnet]
  python3 score.py --selftest   # offline: anonymization + rubric parse only
"""
from __future__ import annotations

import argparse
import hashlib
import json
import random
import statistics
import subprocess
import sys
from pathlib import Path

try:
    import yaml
except ImportError:  # pragma: no cover - same prereq as the rest of the harness
    yaml = None

RUBRIC = """You are grading anonymized answers to one software-architecture
question about a specific codebase. Score EACH answer on three criteria,
0-2 points each (integers only):
  correctness: describes the real data/control flow of the asked mechanism
  grounding:   cites concrete files/modules/symbols that plausibly exist
  depth:       explains mechanism detail beyond a surface summary
Total 0-6 per answer. Be strict: vague or generic prose scores low on
grounding and depth. Respond with ONLY a JSON array:
[{"label": "A", "correctness": 0, "grounding": 0, "depth": 0}, ...]"""

JUDGE_MAX_ANSWER_CHARS = 8000  # keep the judge prompt bounded


def load_runs(results_root: Path) -> list[dict]:
    rows: list[dict] = []
    for jsonl in sorted(results_root.glob("runs/*/*/*/runs.jsonl")):
        for line in jsonl.read_text(encoding="utf-8").splitlines():
            line = line.strip()
            if line:
                try:
                    rows.append(json.loads(line))
                except json.JSONDecodeError:
                    continue
    return rows


def answer_text_for(results_root: Path, row: dict) -> str | None:
    """Locate the persisted answer for one run row.

    run_one.sh saves the envelope (and its .answer.txt sibling) under
    runs/<date>/<slug>/<arm>/scratch/<slug>/<arm>/run_<n>/.
    """
    pattern = (
        f"runs/*/{row['repo']}/{row['arm']}/scratch/"
        f"*/{row['arm']}/run_{row['run_idx']}/claude.json.answer.txt"
    )
    for text_file in sorted(results_root.glob(pattern)):
        return text_file.read_text(encoding="utf-8", errors="replace")
    return None


def anonymize(answers: list[tuple[str, str]], rng: random.Random):
    """Shuffle (key, text) pairs; return (labeled list, de-anon table).

    The judge sees only labels + text. `rng` is injectable for --selftest.
    """
    shuffled = list(answers)
    rng.shuffle(shuffled)
    labels = [chr(ord("A") + i) for i in range(len(shuffled))]
    labeled = [(label, text) for label, (_, text) in zip(labels, shuffled)]
    deanon = {label: key for label, (key, _) in zip(labels, shuffled)}
    return labeled, deanon


def build_judge_prompt(question: str, labeled: list[tuple[str, str]]) -> str:
    parts = [RUBRIC, "", f"QUESTION:\n{question}", ""]
    for label, text in labeled:
        clipped = text[:JUDGE_MAX_ANSWER_CHARS]
        parts.append(f"=== ANSWER {label} ===\n{clipped}\n")
    parts.append("Grade every answer. JSON array only.")
    return "\n".join(parts)


def parse_judge_output(raw: str) -> list[dict]:
    """Parse the judge's JSON array, tolerating prose fences."""
    raw = raw.strip()
    start, end = raw.find("["), raw.rfind("]")
    if start == -1 or end == -1 or end <= start:
        raise ValueError(f"no JSON array in judge output: {raw[:120]!r}")
    parsed = json.loads(raw[start : end + 1])
    if not isinstance(parsed, list):
        raise ValueError("judge output is not a list")
    out = []
    for item in parsed:
        if not isinstance(item, dict) or "label" not in item:
            continue
        out.append(
            {
                "label": str(item["label"]),
                "correctness": int(item.get("correctness", 0)),
                "grounding": int(item.get("grounding", 0)),
                "depth": int(item.get("depth", 0)),
            }
        )
    return out


def run_judge(prompt: str, model: str, claude_bin: str) -> str:
    argv = [claude_bin, "-p", "--output-format", "text"]
    if model:
        argv += ["--model", model]
    argv.append(prompt)
    proc = subprocess.run(argv, capture_output=True, text=True)
    if proc.returncode != 0:
        raise RuntimeError(f"judge claude -p failed: {proc.stderr[-200:]}")
    return proc.stdout


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--results", type=Path, default=Path("results"))
    parser.add_argument("--repos", type=Path, default=Path("repos.yaml"))
    parser.add_argument("--model", default="", help="judge model id (empty = claude default)")
    parser.add_argument("--claude-bin", default="claude")
    parser.add_argument("--seed", type=int, default=None)
    parser.add_argument(
        "--selftest",
        action="store_true",
        help="offline check: anonymization round-trip + rubric parse; no claude call",
    )
    args = parser.parse_args()

    if yaml is None:
        print("ERROR: PyYAML required", file=sys.stderr)
        return 2

    if args.selftest:
        rng = random.Random(7)
        answers = [(f"{arm}:{i}", f"text-{arm}-{i}") for arm in ("with", "without") for i in range(3)]
        labeled, deanon = anonymize(answers, rng)
        assert sorted(deanon.values()) == sorted(k for k, _ in answers)
        assert len(labeled) == 6 and all(lab in deanon for lab, _ in labeled)
        parsed = parse_judge_output(
            'Sure! Here you go:\n[{"label":"A","correctness":2,"grounding":1,"depth":0}]'
        )
        assert parsed == [{"label": "A", "correctness": 2, "grounding": 1, "depth": 0}]
        print("selftest: OK (anonymization round-trip, rubric parse)")
        return 0

    meta = yaml.safe_load(args.repos.read_text(encoding="utf-8")).get("repos", [])
    question_by_slug = {e["slug"]: e["prompt"] for e in meta}

    rows = [r for r in load_runs(args.results) if r.get("valid")]
    by_repo: dict[str, list[dict]] = {}
    for r in rows:
        by_repo.setdefault(r["repo"], []).append(r)

    scores_path = args.results / "scores" / "scores.jsonl"
    scores_path.parent.mkdir(parents=True, exist_ok=True)
    seed = args.seed if args.seed is not None else random.SystemRandom().randrange(2**31)
    rng = random.Random(seed)
    failures = 0

    for slug, runs in sorted(by_repo.items()):
        question = question_by_slug.get(slug)
        if not question:
            print(f"[skip] {slug}: no prompt in repos.yaml", file=sys.stderr)
            continue
        answers: list[tuple[str, str]] = []
        for r in runs:
            key = f"{r['arm']}:{r['run_idx']}"
            text = answer_text_for(args.results, r)
            if text and text.strip():
                answers.append((key, text))
        if not answers:
            print(f"[skip] {slug}: no persisted answers", file=sys.stderr)
            continue

        labeled, deanon = anonymize(answers, rng)
        prompt = build_judge_prompt(question, labeled)
        try:
            raw = run_judge(prompt, args.model, args.claude_bin)
            graded = parse_judge_output(raw)
        except (RuntimeError, ValueError) as exc:
            print(f"[fail] {slug}: {exc}", file=sys.stderr)
            failures += 1
            continue

        rubric_sha = hashlib.sha256(RUBRIC.encode()).hexdigest()[:12]
        for item in graded:
            key = deanon.get(item["label"])
            if key is None:
                continue
            arm, run_idx = key.split(":", 1)
            row = {
                "repo": slug,
                "arm": arm,
                "run_idx": int(run_idx),
                "judge_model": args.model or "default",
                "rubric_sha256": rubric_sha,
                "blind": True,
                "score": item["correctness"] + item["grounding"] + item["depth"],
                "correctness": item["correctness"],
                "grounding": item["grounding"],
                "depth": item["depth"],
            }
            with scores_path.open("a", encoding="utf-8") as fh:
                fh.write(json.dumps(row, ensure_ascii=False) + "\n")
        print(f"[scored] {slug}: {len(graded)}/{len(answers)} answers")

    if failures:
        print(f"{failures} repo(s) failed judging", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
