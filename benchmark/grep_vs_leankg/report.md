# leankg 3-tool surface vs rg baseline — v2 (2026-09-07)

## Corpus + methodology (both arms)
- Corpus: this repo @ main `526135bf` (18,748 elements indexed, 9,522 vectors).
- **Excluded from BOTH arms:** `src/benchmark/**` (contains prior question text), `*.min.js`, `ui-v2/public/**`, `src/embed/**` (vendored assets), `.leankg/**`, `target/**`.
- Ground truth: independently verified `file::symbol` spans (not derived from either tool's answers). `import_relations` accepts BOTH `backend.rs` (PG) and `sqlite_backend.rs` (sqlite) implementations.
- Scoring: file-level hit@5 both arms (grep returns files; leankg QNs are stripped of the repo prefix).
- leankg impact questions drive `search_code` on the symbol (the action an agent would actually use), not the NL router.

| id | category | rg hit@5 | leankg hit@5 | leankg rung | rg ms | leankg ms |
|----|----------|---------|--------------|-------------|-------|-----------|
| exact-symbol | exact | True | True | keyword | 43 | 16915 |
| exact-audit | exact | True | True | keyword | 11 | 18461 |
| exact-scheduler | exact | True | True | keyword | 12 | 11834 |
| exact-hnsw | exact | True | False | vector | 12 | 16024 |
| concept-readonly | concept | False | True | keyword | 12 | 20900 |
| concept-embed-arm | concept | False | False | keyword | 14 | 14486 |
| concept-router | concept | False | False | keyword | 37 | 20032 |
| concept-upsert | concept | False | False | keyword | 13 | 15917 |
| structure-schema | structure | False | False | keyword | 14 | 18798 |
| structure-inventory | structure | False | True | vector | 11 | 19776 |
| structure-watcher | structure | False | False | keyword | 12 | 17156 |
| structure-hydration | structure | False | False | keyword | 14 | 21456 |
| impact-import | impact | True | True | search_code | 10 | 20 |
| impact-status | impact | True | True | search_code | 11 | 32 |
| concept-doctor | concept | False | False | keyword | 12 | 21773 |
| concept-roots | concept | False | True | keyword | 13 | 21477 |

**hit@5: rg 6/16, leankg 8/16** (rg wins 1, leankg wins 3, tie 12)
**latency: rg total 0.25s (~11ms/q), leankg total 255.1s (~16s/q for router questions; 20-32ms for direct search_code)**

## Findings

1. **L1-exact classification defect (#290) is the top leankg loss.** `create_hnsw_index` went to the vector rung and returned vendor `vis-network.min.js::Hx` despite the function being indexed and findable by `search_code` in <100ms.
2. **Keyword rung ranking is substring noise (#287-adjacent).** `indexer/mod.rs::index_files_parallel` outranks `start_watcher` for a watcher question; `api/mod.rs::ApiResponse` outranks `fuse_l2`; test names (`fr_zcp02_mcp_status_*`) outrank the real handler for `impact-status` (test-symbol pollution, see #288-adjacent dedupe/classification gap).
3. **Vendor pollution reaches the ANN path itself (#291).** Top vector hit for `create_hnsw_index` = `vis-network.min.js::Hx`.
4. **rg wins on literal tokens; leankg wins on NL intent** (`concept-readonly`, `concept-roots`, `structure-inventory` — rg's top files are session/benchmark noise). On concept questions rg returned session/mod.rs (the file that greps the word 'session') — classic keyword-recall without ranking.
5. **Direct `search_code` is fast (20-32ms) and accurate** — the ~16s router overhead is the per-request ONNX embedder reload (#292), not retrieval.

## Open issues filed from this run

- #290 router L1 classification
- #291 vendor pollution in ANN
- #292 per-request embedder reload (~16s/query)
- #293 test-symbol pollution in search_code ranking
- #294 keyword rung has no similarity ordering

## Summary correction
- The exact-* rows ran through the **router** (keyword/vector rungs), not `search_code` — only the impact rows used `search_code`. Exact lookups are 3/4 via router with the one loss being #290's misclassification; `search_code`'s fast+accurate result applies to the impact rows only.

## Limitations
- Impact rows use a different leankg protocol (`search_code` direct) than the 14 router rows — they are evidence for #292's latency contrast and for direct-mode accuracy, but the 8 vs 6 headline mixes two protocols. Router-mode impact queries are a separate question the router currently fails.
- Embedding quality is NOT validated by this run: only 2/16 questions reached the vector rung and both returned vendor noise (#291). A clean semantic evaluation requires re-indexing with the excludes above.
- Single-symbol file-level ground truth (no line spans); several questions legitimately have multiple answering files.
- 16 questions is small; win deltas of 1-2 are not significant.
- leankg latency includes per-query ONNX model load (#292) — not intrinsic retrieval cost.

## Post-fix validation (PR #298 branch build, live :9799)
- `create_hnsw_index` (exact-symbol query): was rung=vector + vendor top-hit → now **rung=exact, real symbol `src/embeddings/state.rs::create_hnsw_index`**, vendor absent. #290 confirmed fixed on the branch.

## Handoff notes (2026-09-08)
- PR #298 code-level CI verified on `e36822f3` (6/6 success). Docs-only commits after it did not trigger Actions runs (no runs created repo-wide since cf6223c1 — appears to be a queue/throttle, not a broken workflow; verify after merge).
- Remaining merge queue: #298 → #301 → #303 → #304 → #305 (independent; #305 diagnostics-only, merge first if you want crash evidence).
- After merging #301 + #304: re-index + re-embed this repo, then re-run the benchmark — first clean-corpus validation of embeddings (#291) and the latency delta for #292 are both captured then.
- Stash handoff: `stash@{0}` (b3402076) = another session's graft-docs WIP; restore on `docs/graft-vs-leankg-analysis`.
