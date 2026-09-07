#!/bin/bash
# grep baseline for grep_vs_leankg/questions.json
# usage: scripts/grep_vs_leankg.sh > benchmark/grep_vs_leankg/baseline.json
cd "$(dirname "$0")/.." || exit 1
python3 - <<'PYEOF'
import json, subprocess, time, os

QUESTIONS = json.load(open("benchmark/grep_vs_leankg/questions.json"))
TOP_K = QUESTIONS["top_k"]
results = []

def grep_run(query):
    """Run ripgrep-ish grep over src, return up to TOP_K file:line matches + timing.
    grep is line-oriented: baseline = locate files/lines mentioning the query."""
    t0 = time.perf_counter()
    proc = subprocess.run(
        ["grep", "-rn", "--include=*.rs", "-m", "20", "-i", "-E",
         query if len(query.split()) == 1 else "|".join(query.lower().split()[-3:]),
         "src/"],
        capture_output=True, text=True, timeout=60,
    )
    dt = time.perf_counter() - t0
    lines = proc.stdout.splitlines()[: TOP_K * 6]
    hits = []
    for l in lines:
        if ":" not in l:
            continue
        path, rest = l.split(":", 1)
        hits.append(path)
    # dedupe preserving order, rank by path frequency (grep has no ranking)
    seen, ranked = set(), []
    for h in hits:
        if h not in seen:
            seen.add(h)
            ranked.append(h)
    return ranked[:TOP_K], dt, len(proc.stdout.splitlines())

def grep_context_run(query):
    """grep -C2 over the whole repo for concept questions (what a human greps)."""
    t0 = time.perf_counter()
    terms = [w for w in query.lower().split() if len(w) > 3][:3] or [query.lower()]
    proc = subprocess.run(
        ["grep", "-rln", "--include=*.rs", "-i", "-E", "|".join(terms), "src/"],
        capture_output=True, text=True, timeout=60,
    )
    dt = time.perf_counter() - t0
    files = proc.stdout.splitlines()[:TOP_K]
    return files, dt, len(proc.stdout.splitlines())

for q in QUESTIONS["questions"]:
    exact = len(q["question"].split()) == 1 and q["question"][:1].islower() and "_" in q["question"]
    if q["category"] == "exact":
        files, dt, total = grep_run(q["question"])
    else:
        files, dt, total = grep_context_run(q["question"])
    results.append({
        "id": q["id"],
        "category": q["category"],
        "question": q["question"],
        "tool": "grep",
        "elapsed_s": round(dt, 4),
        "raw_matches": total,
        "returned_files": files,
        "ground_truth": q["ground_truth"],
    })
    print(f"{q['id']:24s} {dt*1000:8.1f}ms  {len(files)} files  top: {files[0] if files else '—'}")

json.dump({"tool": "grep", "top_k": TOP_K, "results": results},
          open("benchmark/grep_vs_leankg/baseline.json", "w"), indent=1)
print(f"\nbaseline written: {len(results)} questions")
PYEOF
