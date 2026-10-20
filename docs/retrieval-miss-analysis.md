# Retrieval miss analysis — Gemma semantic search vs grep (20-question benchmark)

*Status: analysis complete; fix 1 (extractor word boundary) implemented. Last updated: 2026-10-18.*

## 1. What was measured

| Item | Value |
|---|---|
| Repos indexed and Gemma-embedded | 14 |
| Elements in corpus | 70,465 |
| Embedding model | `embeddinggemma-2`, 512 dims, provider `openai` through the local proxy (`127.0.0.1:9103`) |
| Questions | 20, one per repo-plus-topic, prose-phrased (how agents actually ask) |
| Baseline | `grep`: question tokenised into content words (len ≥ 4, stopword-filtered), `rg -c` per file, files ranked by match count desc |
| Comparison | LeanKG semantic retrieval: `leankg query -action semantic -limit 10 --project <repo>`, rung L3, RRF over `vector + fts5 (+ name)` |
| Ground truth | a symbol (`file::Name`) per question, with `gt_symbol_rows` / `gt_file_rows` counting index rows that match |
| Metrics | Hit@1/3/5, MRR, mean latency, per question |

Aggregate result (`/private/tmp/opencode/bench_metrics.json`):

| Method | Hit@1 | Hit@3 | Hit@5 | MRR | avg s |
|---|---|---|---|---|---|
| grep (file-level, rank among all matched files) | 0.15 | 0.25 | 0.40 | 0.262 | 0.07 |
| semantic (file-level rank of the GT file) | **0.60** | **0.75** | **1.00** | **0.730** | 0.64 |
| semantic (element-level rank of the GT symbol) | 0.35 | 0.55 | 0.70 | 0.495 | 0.64 |

Headline: semantic retrieval never misses the file (Hit@5 = 1.00 vs grep's 0.40), and is ~3× better at
putting it first. The element level is where the misses live, and the analysis below shows that a
meaningful share of those "misses" are defects in the benchmark or the index, not in the ranking.

## 2. Miss taxonomy

Six distinct causes, in descending order of how much of the miss count each explains.

### RC1 — Benchmark label defect: the element metric is undefined for 5 of 20 questions

`gt_symbol_rows = 0` for Q3, Q6, Q7, Q9, Q19, yet an element rank is still reported for them:

| Q | repo | gt_symbol_rows | gt_file_rows | reported elem rank | GT label |
|---|---|---|---|---|---|
| 3 | qa-agent | 0 | 29 | 7 | `internal/plan/builder_test.go::NormalizeACs` |
| 6 | db-mcp-server | 0 | 11 | 2 | `internal/delivery/mcp/tool_registry.go::RegisterAllTools` |
| 7 | xdev | 0 | 52 | 9 | `internal/agent/schedule.go::nextBatchLocked` |
| 9 | laya | 0 | 27 | 1 | `laya/agent.py::_encode_state` |
| 19 | x-trader | 0 | 147 | 1 | `internal/app/engine.go::exitThresholds` |

**Root cause: the labels are unqualified.** All five name a method without its receiver/class, but the
store's `qualified_name` carries it, so neither the label nor any exact-match lookup resolves:

| Q | harness label | the row that actually exists |
|---|---|---|
| 3 | `builder_test.go::NormalizeACs` | `builder_test.go::fakeIngestor.NormalizeACs` |
| 6 | `tool_registry.go::RegisterAllTools` | `tool_registry.go::ToolRegistry.RegisterAllTools` |
| 7 | `schedule.go::nextBatchLocked` | `schedule.go::ScheduleState.nextBatchLocked` |
| 9 | `agent.py::_encode_state` | `agent.py::Agent._encode_state` |
| 19 | `engine.go::exitThresholds` | `engine.go::Engine.exitThresholds` |

So the element rank for those five is not a retrieval outcome at all — it is noise from the harness's
name-only fallback. Two of them (Q9, Q19) are scored as *hits* at rank 1 and three (Q3, Q6, Q7) as
partial hits. Measured both ways, the element-level metric moves, and the honest denominator is the
15 valid labels:

| element metric | Hit@1 | Hit@3 | Hit@5 | MRR |
|---|---|---|---|---|
| all 20 questions (published) | 0.350 | 0.550 | 0.700 | 0.495 |
| 15 questions with a valid element label | 0.333 | 0.533 | 0.733 | 0.476 |

Neither figure may be tuned against until the labels are repaired.

**Fix:** qualify every symbol label (receiver included) and resolve each one against the index before
the run — a label that matches nothing is a broken label, not a hard question. The harness must then
refuse to score element recall for a question whose label does not resolve, so the denominator is
stated rather than assumed.


### RC2 — Extractor false positives: the TS/JS function regex has no word boundary

`internal/index/extract.go:48`:

```go
tsFuncRe = regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?(?:async\s+)?function\s*\*?\s*([A-Za-z_]\w*)`)
```

`function\s*` requires no boundary, so it matches the *inside* of any identifier that starts with
`function`. Reproduced in Go:

```
"      functionality: `tool choice type: ${toolChoice.type}`,"   -> captures "ality"
"  functionDefs.push(x)"                                        -> captures "Defs"
"  functionName = 'x'"                                           -> captures "Name"
```

This is not theoretical: it placed phantom elements inside the benchmark window for Q15 (opencode):

```
Q15 [opencode] elem_rank=3 file_rank=1:
  slot #1/10 = phantom name 'ality' (regex captured the tail of 'functionality')
  slot #6/10 = phantom name 'ality' (regex captured the tail of 'functionality')
```

Two of ten top slots in a *semantic* query were consumed by elements that do not exist, and the GT
symbol still landed at rank 3 only because the phantom shares its file. Each phantom is also embedded
and indexed, so it pollutes the vector space for every other query in that repo, and it inflates the
element count that "100% embedded" is computed against.

**Fix:** `function\b`. Verified: `functionality:`, `functionDefs`, `functionName` all stop matching,
while `function f()`, `export default async function main()`, and `function* gen()` are unchanged.

### RC3 — Stamp identity drift silently degrades a whole repo to keyword-only

Q20 (onegw) is the only question that did not run at L3. Verbatim reason:

```
stamp mismatch: collection differs from the live provider (provider "local", live revision
914f7f89142e33e77833254d9c9b90c3cef7303b); degraded from L3; run `leankg-embed full` to rebuild
```

`internal/core/core.go:991` compares the stored stamp against the live embedder identity as a whole
identity (`*stamp != want`) and degrades the entire query on any difference. onegw carries two stamps:

| model_id | provider | dims | chunker |
|---|---|---|---|
| `local` | local | 384 | (earlier chunker) |
| `embeddinggemma-2` | **local** | 512 | chunker3 |

The Gemma collection was written with `provider=local`, but the benchmark queried with
`LEANKG_EMBED_PROVIDER=openai` (the local OpenAI-compatible proxy). The vectors are fine — same model,
same 512 dims — but the stamped provider string differs, so all 10 similarities come back `null` and
the repo answers from FTS5 only. A repo that is 100% embedded stops being queryable at L3 for a reason
that has nothing to do with its vectors.

Analogous risk for every repo queried from a different provider alias than the one that embedded it.

**Fix:** decide the drift contract by what actually changes the vector space (model id, dims,
revision, chunker, prefixes) and treat a provider *transport alias* difference as informational, not
fatal — or normalise provider to a canonical identity at stamp-write time so the same backend always
stamps the same value. Either way the degradation must be visible in the coverage report, not only in
a per-query reason string.

### RC4 — Semantic windows admit rows with no vector evidence

A `-action semantic` query still fills its top-10 from the keyword arms. Rows that no vector arm could
place come back with `similarity = 0` and occupy slots ahead of better candidates:

| Q | repo | `sim = 0` slots | window |
|---|---|---|---|
| 7 | xdev | 4 | 10 |
| 14 | claude-code | 3 | 10 |
| 17 | ZCode | 2 | 10 |
| 18 | ZCode | 2 | 10 |
| 1 | rea | 1 | 10 |
| 5, 6 | db-mcp-server | 1 each | 10 |

Q7 loses 40% of its window to entries with no semantic evidence, which is consistent with its GT
element sitting at rank 9.

**Fix:** for the semantic rung, zero-vector-evidence rows must not outrank rows with a real cosine
score. Rank by vector evidence first, then use the keyword arms to order candidates that the vector
arm also saw, or reserve a small explicit budget for keyword-only rows (and label them as such).

### RC5 — Element-level dilution next to file-level strength

The GT *file* lands in the top 10 for every question, but the GT *element* does not in Q2, Q11 and
Q16 — the window is full of other files' rows even though the target file was correctly identified:

| Q | repo | file rank | elem rank | GT file entries in window |
|---|---|---|---|---|
| 2 | rea-go | 5 | **absent** | 0 |
| 11 | laya-proxy | 1 | **absent** | 0 |
| 16 | opencode | 1 | **absent** | 0 |

This is a correctness-of-granularity gap: retrieval is confidently pointing at the right file while
the element the question is about is not surfaced. A question answered at file granularity would be a
hit; at element granularity it is a miss.

**Fix:** once a file is strongly indicated, expand within it (adjacent/sibling elements of the same
file, then rank them by name/semantic evidence) rather than spending the remaining slots on other
files. That also makes the file-level strength visibly useful to the caller.

### RC6 — The name arm fires on none of the 20 questions

Every one of the 20 answers reports `reason = "reciprocal-rank fusion rrf(vector+fts5)"`. The
`ArmName` arm (`internal/store/hybrid_sqlite.go:118`, gated by `rareNameTokens`,
`internal/store/elements.go:361`) contributed on **zero** of them — even though 15 of the 20 questions
contain the target symbol's name literally, and the GT symbol is an exact-token match in most.

The arm was introduced to fix exactly this class of query, and the rarity gate is what is keeping it
silent. Either the bound is wrong for corpora of this size, or the token derivation does not recover
`nextBatchLocked` from a prose sentence containing it, or the arm fires but its contribution is folded
away by the vector arm before it can change an order.

**Fix:** instrument the arm (`--debug-arms` or a provenance field per hit) and re-tune on the label
set. Do not change the gate blind — the current value has a documented rationale that must be
re-measured, not overridden.

## 3. Best practices this points to

These are the durable rules; the numbered fixes above are instances of them.

1. **A metric is only as good as its labels.** Resolve every ground-truth label against the index
   before scoring, fail the run loudly on an unresolved label, and never report a metric whose label
   set is empty. `gt_symbol_rows > 0` is the precondition for an element rank.
2. **Score what you claim to score.** If the element metric is the headline, the harness must not
   report element ranks for questions that only have file labels.
3. **Anchored regexes need anchors on both ends of a word.** For source extraction, every keyword
   matcher (`function`, `class`, `def`, `func`) gets `\b`. A phantom element is worse than a missing
   one: it is embedded, indexed, ranked and counted in coverage.
4. **Coverage and queryability are different claims.** "N% embedded" must be computed per model
   namespace and re-verified by running a semantic query that returns non-null similarities in that
   repo. A per-model vector count proves the rows exist, not that the query path will use them.
5. **Degrade loudly, and proportionally.** Degrading a whole repo to keyword-only for a transport-alias
   mismatch is too blunt; but whatever the rule, the reason must surface in the standing coverage
   report, not only in a per-query envelope nobody reads.
6. **A semantic rung should not be filled by non-semantic evidence.** Keyword-only rows belong at the
   tail, labelled, or behind an explicit budget.
7. **Cross-check every retrieval claim against a mundane baseline.** grep found the file in the top 5
   for only 40% of these questions; the benchmark would have looked healthy without that column.
8. **Re-measure gates, don't trust their comments.** The name-arm rationale is a measurement on one
   10,885-element corpus; on a 70,465-element, 14-repo corpus it fires zero times. Gates need a
   standing benchmark that fails when their contribution goes to zero.

## 4. Fix plan

| # | Fix | Files | Effort | Status |
|---|---|---|---|---|
| 1 | `function\b` word boundary + regression test | `internal/index/extract.go`, `internal/index/extract_tsfunc_test.go` | S | **done** |
| 2 | Re-index the repos whose corpus contains phantoms; re-embed, and record corrected element counts | index/embed pipeline | M | pending |
| 3 | Qualify every symbol label (receiver included), resolve labels up front, and skip the element metric when a label does not resolve | benchmark harness, `scripts/retrieval-bench.py` | S | pending |
| 4 | Stamp drift: normalise provider identity (or exclude the transport alias from the drift contract) | `internal/core/core.go`, `internal/embed` | M | pending |
| 5 | Semantic rung: zero-vector-evidence rows cannot outrank scored rows | `internal/store/hybrid_sqlite.go`, `internal/store/pg_fts.go` | M | pending |
| 6 | Within-file expansion when a file is strongly indicated | `internal/store/hybrid_sqlite.go` | M | pending |
| 7 | Name-arm provenance/instrumentation, then re-tune the rarity gate on the label set | `internal/store/elements.go`, `internal/store/backend.go` | M | pending |
| 8 | Re-run the 20-question benchmark after each fix; publish the report with the same columns plus a defect column | harness + report generator | S | pending |

Ordering rationale: the extractor fix (1) changes the corpus, so it precedes any re-embedding (2);
the label guard (3) makes every later number trustworthy; (4) is a live correctness bug on a repo that
is otherwise fully embedded; (5)–(6) are ranking quality; (7) needs instrumentation before it can be
tuned honestly.

## 5. Verification contract

A fix is done when, on the same 20 questions and the same 14 repos:

- `docs/retrieval-label-set.md` labels resolve to at least one index row each (no `gt_symbol_rows = 0`);
- no benchmark window contains an element whose name is a proper suffix of the first identifier at its
  own start line (phantom count = 0);
- every `-action semantic` query in the run returns rung L3 (no stamp-drift degradation);
- the reported element-level metrics are computed only over questions with a valid element label, and
  the report states the denominator;
- the aggregate table is published with grep, semantic file-level and semantic element-level side by
  side, so a regression in one column cannot hide behind the others.
