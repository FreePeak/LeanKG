# leankg 3-tool surface vs grep — comparison run (2026-09-07)

Dataset: `benchmark/grep_vs_leankg/questions.json` (16 questions: exact/concept/structure/impact).
Baseline: `scripts/grep_vs_leankg.sh` → `baseline.json` (grep -rn over src/, top-5 files, no ranking).
Subject: live sqlite server `:9799` via `scripts/leankg_vs_baseline.py` → `leankg.json` (router `get`, top-5 elements).
Scoring: hit@5 at file level (ground truth = file::symbol; a returned element counts if its file matches).

| id | category | grep hit@5 | leankg hit@5 | leankg rung | grep ms | leankg ms |
|----|----------|-----------|--------------|-------------|---------|-----------|
| exact-symbol | exact | True | True | keyword | 157 | 17159 |
| exact-audit | exact | True | True | keyword | 111 | 17626 |
| exact-scheduler | exact | True | True | keyword | 119 | 11021 |
| exact-hnsw | exact | True | False | vector | 115 | 14605 |
| concept-readonly | concept | False | True | keyword | 110 | 17858 |
| concept-embed-arm | concept | True | False | keyword | 95 | 13839 |
| concept-router | concept | False | False | keyword | 125 | 15493 |
| concept-upsert | concept | False | False | keyword | 98 | 16214 |
| structure-schema | structure | False | False | keyword | 114 | 20141 |
| structure-inventory | structure | False | True | vector | 69 | 16467 |
| structure-watcher | structure | False | False | keyword | 38 | 16614 |
| structure-hydration | structure | False | False | keyword | 95 | 20807 |
| impact-import | impact | True | False | keyword | 123 | 13547 |
| impact-status | impact | False | False | keyword | 121 | 22528 |
| concept-doctor | concept | False | False | keyword | 119 | 22087 |
| concept-roots | concept | False | True | keyword | 79 | 30894 |

**hit@5: grep 6/16, leankg 6/16** (leankg wins 3, grep wins 3)
**latency: grep total 1.7s, leankg total 286.9s** (~18s/query router overhead)

## Findings

1. **L1 exact rung is not selected for identifier-shaped queries.** `create_hnsw_index` (a single symbol, present in the index — direct `search_code` finds it in 80ms) was routed to the **vector** rung and answered with `vis-network.min.js::Hx`. The router's query classification is the top defect.
2. **Keyword rung ranking is substring noise.** `embed.rs::Assets` outranks `spawn_embed_idle_scheduler` for an embed-scheduler question; `api/mod.rs::ApiResponse` outranks `fuse_l2` for a router question. `fuzzy_find_elements` regex-matches `name` only, with no similarity ordering (known simplification, noted in code).
3. **Minified JS pollutes the vector space.** `src/embed/vis-network.min.js` + `ui-v2/public/vis-network.min.js` are indexed and embedded; they dominate ANN hits for technical terms (`Hx`, `cE::predefinedPosition`).
4. **grep wins where the query is a literal token** (exact-symbol/audit/scheduler, impact-import) — it has no ranking but the file set is small; it loses on concept questions (`readonly gate`, `roots`) where no literal token exists.
5. **Latency is 100x worse on leankg** (~18s vs ~0.1s/query): the router path loads the ONNX embedder per request. Direct `search_code` calls were <100ms.

## Recommended follow-ups (candidates for issues)

- Router: classify single-identifier queries to L1 exact before keyword/vector
- Exclude minified/vendor assets (`*.min.js`, `src/embed/`) from indexing/embedding
- Keyword rung: rank by name-similarity, not first-match
- Cache the embedder across requests (model reload per query dominates latency)
