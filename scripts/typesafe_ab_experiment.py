#!/usr/bin/env python3
"""TypeSafe Noul A/B experiment — rerun, capture results, emit HTML report."""

import json
import os
import sys
import time
import urllib.error
import urllib.request

API_URL = "https://api.typesafe.ai/v1/systemone"
MODEL = "jev-1.13.0"

# 8 queries representative of LeanKG code search.
QUERIES = [
    ("how does authentication work", ["auth", "session", "jwt", "login", "token"]),
    ("database connection pooling", ["pool", "connection", "db", "store", "open"]),
    ("error handling strategy", ["error", "fail", "retry", "fallback", "degrade"]),
    ("embedding model configuration", ["embed", "model", "bge", "vector", "stamp"]),
    ("graph traversal algorithm", ["traverse", "graph", "edge", "neighbor", "bfs"]),
    ("how to index a repository", ["index", "scan", "ingest", "crawl", "parse"]),
    ("caching invalidation policy", ["cache", "stale", "invalidate", "refresh", "watermark"]),
    ("multi-tenant isolation", ["tenant", "project", "scope", "isolate", "namespace"]),
]

# 30 candidates: 20 related (keyword-fingerprinted), 10 unrelated (off-topic).
def make_candidates(query, keywords):
    related = [f"Document about {keywords[i % len(keywords)]} handling in {query}" for i in range(20)]
    unrelated = ["Server rack cooling settings and HVAC monitoring"] * 5
    unrelated += ["Office supply inventory management system"] * 5
    return related + unrelated


def call_typesafe(state, key):
    body = json.dumps({
        "state": state, "model": MODEL,
        "questions": {"relevant": {
            "type": "noul",
            "instructions": f"Does this candidate directly answer: {state['query']}",
            "criteria": {"true": "Direct, specific answer", "false": "Tangential or different topic"},
        }},
    }).encode()
    req = urllib.request.Request(API_URL, data=body,
        headers={"Content-Type": "application/json", "Authorization": f"Bearer {key}"})
    resp = urllib.request.urlopen(req, timeout=30)
    data = json.loads(resp.read())
    return data["answers"]["relevant"]["noul"], data.get("usage", {})


def bm25_score(candidate, keywords):
    return sum(1 for kw in keywords if kw in candidate.lower())


def run():
    key = os.environ["TYPESAFE_API_KEY"]
    all_results = {}
    total_calls = 0

    for qi, (query, keywords) in enumerate(QUERIES):
        candidates = make_candidates(query, keywords)
        nouls = {}
        t0 = time.time()
        for ci, cand in enumerate(candidates):
            try:
                noul, usage = call_typesafe({"query": query, "candidate": cand}, key)
                nouls[f"c{ci}"] = noul
                total_calls += 1
            except Exception as e:
                print(f"  ERROR Q{qi} c{ci}: {e}", file=sys.stderr)
                nouls[f"c{ci}"] = 0.0
                total_calls += 1
        elapsed = time.time() - t0

        # Rank by Noul
        sorted_noul = sorted(nouls.items(), key=lambda x: x[1], reverse=True)
        top5_noul = [k for k, _ in sorted_noul[:5]]
        # Rank by BM25
        sorted_bm25 = sorted(nouls.keys(), key=lambda k: bm25_score(candidates[int(k[1:])], keywords), reverse=True)
        top5_bm25 = sorted_bm25[:5]
        overlap = len(set(top5_noul) & set(top5_bm25))

        rel = [nouls[f"c{i}"] for i in range(20)]
        unrel = [nouls[f"c{i}"] for i in range(20, 30)]
        separation = min(rel) > max(unrel)

        all_results[qi] = {
            "query": query, "keywords": keywords, "candidates": candidates,
            "nouls": nouls, "top5_noul": top5_noul, "top5_bm25": top5_bm25,
            "overlap": overlap, "separation": separation, "elapsed": elapsed,
            "min_related": min(rel), "max_unrelated": max(unrel),
        }
        print(f"Q{qi} \"{query}\" ({elapsed:.1f}s) overlap={overlap}/5 sep={separation}")

    return all_results, total_calls


def html_report(results, total_calls):
    sep_count = sum(1 for r in results.values() if r["separation"])
    order_count = sum(1 for r in results.values() if r["top5_noul"] != r["top5_bm25"])
    avg_overlap = sum(r["overlap"] for r in results.values()) / len(results)
    total_time = sum(r["elapsed"] for r in results.values())

    html = f"""<!DOCTYPE html>
<html><head><meta charset="utf-8">
<title>TypeSafe Noul A/B Report</title>
<style>
body {{ font-family: -apple-system, Segoe UI, Roboto, sans-serif; max-width: 1200px; margin: 2rem auto; padding: 0 1rem; color: #222; }}
h1 {{ border-bottom: 2px solid #555; padding-bottom: .4rem; }}
h2 {{ margin-top: 2rem; color: #333; }}
table {{ border-collapse: collapse; width: 100%; margin: 1rem 0; font-size: .9rem; }}
th, td {{ border: 1px solid #ccc; padding: .4rem .6rem; text-align: left; }}
th {{ background: #f0f0f0; }}
.pass {{ color: #1a7; }} .fail {{ color: #c00; }} .warn {{ color: #aa0; }}
.bar {{ background: #4a9; height: 14px; display: inline-block; vertical-align: middle; }}
.bar-rel {{ background: #16a; }} .bar-unrel {{ background: #c44; }}
.query-block {{ background: #f8f8f8; border: 1px solid #ddd; border-radius: 6px; padding: 1rem; margin: 1rem 0; }}
.summary {{ background: #eef; border: 1px solid #99c; border-radius: 6px; padding: 1.2rem; margin: 1rem 0; }}
.rank-table td {{ font-family: monospace; font-size: .85rem; }}
</style></head><body>
<h1>&#x1F50E; TypeSafe Noul Re-rank — A/B Experiment Report</h1>
<p><strong>Date:</strong> {time.strftime("%Y-%m-%d %H:%M:%S")} | <strong>Model:</strong> {MODEL} | <strong>API:</strong> live api.typesafe.ai | <strong>API calls:</strong> {total_calls}</p>

<div class="summary">
<h2 style="margin-top:0;">Summary</h2>
<table>
<tr><td>Queries</td><td>{len(results)}</td></tr>
<tr><td>API calls</td><td>{total_calls}</td></tr>
<tr><td>Total wall time</td><td>{total_time:.1f}s</td></tr>
<tr><td>Avg Noul/BM25 top-5 overlap</td><td>{avg_overlap:.1f}/5</td></tr>
<tr><td>Noul changes top-5 vs BM25</td><td class="pass">{order_count}/{len(results)}</td></tr>
<tr><td>Full related/unrelated separation</td><td class="pass">{sep_count}/{len(results)}</td></tr>
<tr><td>Related noul range</td><td>{min(r['min_related'] for r in results.values()):.3f} – {max(r['min_related'] for r in results.values()):.3f}</td></tr>
<tr><td>Unrelated noul range</td><td>{min(r['max_unrelated'] for r in results.values()):.3f} – {max(r['max_unrelated'] for r in results.values()):.3f}</td></tr>
</table>
</div>
"""

    # Per-query detail blocks
    for qi, r in results.items():
        nouls = r["nouls"]
        # Sort candidates by noul descending for the rank table
        all_candidates = sorted(nouls.items(), key=lambda x: x[1], reverse=True)

        html += f"""<div class="query-block">
<h2>Query {qi}: "{r['query']}"</h2>
<p><strong>Keywords:</strong> {', '.join(r['keywords'])} | <strong>Time:</strong> {r['elapsed']:.1f}s |
<strong>Separation:</strong> <span class="pass">{'PASS' if r['separation'] else 'FAIL'}</span> |
<strong>Top5 overlap:</strong> {r['overlap']}/5</p>

<table class="rank-table">
<tr><th>Rank (Noul)</th><th>Candidate</th><th>Noul</th><th>BM25 rank</th><th>Noul bar</th></tr>
"""
        noul_max = max(nouls.values()) if nouls else 1.0
        for rank_noul, (ck, nv) in enumerate(all_candidates, 1):
            ci = int(ck[1:])
            bm25_rank = r["top5_bm25"].index(ck) + 1 if ck in r["top5_bm25"] else "–"
            rel_class = "bar-rel" if ci < 20 else "bar-unrel"
            bar_w = max(1, int(nv / noul_max * 200))
            c = r["candidates"][ci]
            html += f"""<tr><td>{rank_noul}</td><td>{c[:80]}</td><td>{nv:.3f}</td>
<td>{bm25_rank}</td><td><span class="bar {rel_class}" style="width:{bar_w}px"></span></td></tr>
"""
        html += "</table></div>\n"

    html += """</body></html>"""
    return html


if __name__ == "__main__":
    if "TYPESAFE_API_KEY" not in os.environ:
        print("ERROR: TYPESAFE_API_KEY not set", file=sys.stderr)
        sys.exit(1)
    print("Running TypeSafe Noul A/B experiment...")
    results, total_calls = run()
    print(f"\nGenerating HTML report...")
    html = html_report(results, total_calls)
    path = "/Users/linh.doan/work/harvey/freepeak/leankg/typesafe_ab_report.html"
    with open(path, "w") as f:
        f.write(html)
    print(f"Report written to {path}")

    # Also save JSON data
    json_path = "/Users/linh.doan/work/harvey/freepeak/leankg/typesafe_ab_data.json"
    with open(json_path, "w") as f:
        json.dump(results, f, indent=2, default=str)
    print(f"Data saved to {json_path}")
