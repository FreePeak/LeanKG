#!/bin/bash
# grep/rg baseline: word-match with extracted identifiers, ranked by first-match
# order (rg -n), vendor/benchmark paths excluded. usage: > baseline.json
cd "$(dirname "$0")/.." || exit 1
python3 - <<'PYEOF'
import json, subprocess, time

Q = json.load(open("benchmark/grep_vs_leankg/questions.json"))
K = Q["top_k"]
EX = []
for pat in Q["excludes"]:
    EX += ["--glob", "!" + pat]

def rg(query_terms):
    t0 = time.perf_counter()
    pat = "|".join(query_terms)
    proc = subprocess.run(
        ["rg", "-n", "--no-heading", "-i", "-w", "--glob", "!.leankg/**", "--glob", "!target/**"] + EX + [pat, "src/"],
        capture_output=True, text=True, timeout=60)
    dt = time.perf_counter() - t0
    files, seen = [], set()
    for line in proc.stdout.splitlines():
        path = line.split(":", 1)[0]
        if path not in seen:
            seen.add(path)
            files.append(path)
    return files[:K], dt, len(proc.stdout.splitlines())

results = []
for q in Q["questions"]:
    terms = [w for w in q["question"].lower().replace("?", "").split() if len(w) > 3]
    if q["category"] == "exact" or "leankg_symbol" in q:
        terms = [q.get("leankg_symbol", q["question"])]
    files, dt, total = rg(terms)
    results.append({"id": q["id"], "category": q["category"], "question": q["question"],
                    "tool": "rg", "elapsed_s": round(dt, 4), "raw_matches": total,
                    "returned_files": files, "terms": terms})
    print(f"{q['id']:24s} {dt*1000:7.1f}ms  {len(files)} files  top: {files[0] if files else '—'}")

json.dump({"tool": "rg", "top_k": K, "results": results},
          open("benchmark/grep_vs_leankg/baseline.json", "w"), indent=1)
print("baseline v2 written")
PYEOF
