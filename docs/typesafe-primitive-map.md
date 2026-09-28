# TypeSafe ↔ LeanKG Primitive Map

**Status: integration not planned.** TypeSafe Jev integration was
experimented and rejected (API key returns 401; user direction: no
value). This document is retained as a reference map of where the
primitives *would* have fitted, so future readers understand the
intentional absence. LeanKG's scoring is entirely native (BM25 +
cosine + trigram RRF).

Three scoring scales coexist: FTS5 BM25 (SQLite); cosine similarity + tsvector +
trigram RRF fusion (PostgreSQL). Hits carry a raw `score` with no shared meaning.
A Score over `{tangential, relevant, strong, exact}` replaces all three scales
with one typed position. Every consumer (`rungFuzzy` `:560`, `rungSemantic` `:627`,
`graphAction` results) applies one threshold instead of interpreting three
unrelated numbers.

### 🥈 Hybrid fusion weighting — `internal/store/pg_fts.go:113`

`FuseRRF` uses fixed `RRFK = 60` for all arms. `HybridHit` (`:145`) already carries
per-arm scores (`KeywordScore`, `TrigramScore`, `Similarity`) that go unused after
fusion. A Score over arm quality per hit lets a learned reranker adjust weights
instead of the static RRF formula — the cleanest insertion point for a model that
improves retrieval.

### 🥈 Memory hit ranking — `internal/memory/banks.go:432`

`RecallBanks` scores recall rows with native BM25 (Okapi) over tokenized
content — rare query terms rank higher and short documents are not unfairly
penalized (implemented 2026-09-19). `RankEntries` (`banks.go:594`) populates
each row's `Score float64` from that BM25 score. A typed tier over
`{tangential, somewhat_relevant, core_context, essential}` would still let
`SessionMemoryRead` (`core.go:1245`) decide how many entries to inject and
whether to compress them — the raw BM25 float is now available for that, per
US-SM-04 (RRF over memory stores remains open).

### 🥉 Freshness degree — `internal/store/backend.go:269`

Three buckets — `fresh` / `possibly_stale` / `cold` — with no gradation within them.
A Score over `{current, slightly_behind, stale, very_stale, cold}` (each defined by
watermark delta + element count thresholds) lets the `megaGraphBanner` check
(`core.go:303-308`) and query-response freshness act proportionally instead of
binary "fresh / not fresh".

### 🥉 Cluster labeling — `internal/web/clustering.go:223`

`generateClusterLabel` names clusters from a file path when Louvain finds none
(`fallbackFolderClustering` at `:190`). A Score over `{coincidental, thematic, core}`
over a cluster's member elements decides whether the folder-name fallback is good
enough or whether the cluster needs a semantic label from a model.

---

## Noul — Yes/no probability, where the degree of belief matters

### 🥇 Ontology fallback decision — `internal/ontology/query.go:528`

`ConceptSearch` sets `fallbackUsed = len(matched) == 0` — a binary "did the ontology
answer this?" decided by zero matches. A Noul *"Was this a genuine ontological
answer or a desperation fallback?"* propagates uncertainty downstream instead of a
false boolean. A caller seeing `noul: 0.4` knows concept results are thin and
should not be over-trusted.

### 🥇 L3 rung attempt gate — `internal/core/core.go:510`

Every query reaching L3 pays an embedding cost (HTTP round-trip to the sidecar).
The current ladder always tries L3 and degrades on failure. A Noul
*"Is this query likely to benefit from semantic matching?"* fed query text +
element count + freshness could skip L3 entirely when `noul < 0.3`, falling through
to L2 with a clear reason. The happy path is unchanged.

### 🥈 Mega-graph boundary judgment — `internal/ontology/discover.go:53`

`IsMegaGraph` is a hard boolean against `LEANKG_MAX_CACHE_ELEMENTS` (default 50000).
`RefuseFullScanIfMega` (`:80`) returns a refusal with a `hint` field — a manual
routing decision. A Noul *"Is this graph large enough that a full scan is risky?"*
softens the binary refusal into a graded advisory; the `recommended_tools` list can
be ranked by confidence rather than hardcoded.

### 🥈 Per-element re-embed decision — `internal/embed/run.go`

Incremental embed skips elements whose content hash matches. When imports restructure
or signatures move, the hash stays stable but the embedding context changes. A Noul
per element — *"Does the surrounding context suggest this embedding is stale despite
an unchanged hash?"* — triggers targeted re-embedding instead of full re-index.

### 🥉 Env-conflict severity — `internal/orgknowledge/conflicts.go:60`

`FindEnvConflicts` decides meaningful drift via exact-map equality
(`metadataEqual`/`canonicalMeta`). A Noul for "is this drift meaningful enough to
surface?" lets callers weigh it against other project risks instead of treating all
drifts equally.

---

## Choice — Pick one from a defined set

### 🥇 Auto-index decision — `internal/indexgate/indexgate.go:104`

`Decide` already returns a `Decision` enum with named reason vocabulary
(`ReadOnly`, `Disabled`, `NoGit`, `Index`, `Fresh`) — a 7-step decision table described
in its doc comment as "the decision half — designed for testable policy". It is already
Choice-shaped. Extending it to express uncertainty between `Fresh` and `Index` (the
hard cutoff at `lastCommit <= lastWrite+threshold`) is a low-risk improvement.

### 🥇 Compression mode selection — `internal/core/core.go:1140` / `internal/compress/modes.go:131`

`compressRead` accepts `args.mode` as a free string validated against 8 options
(adaptive, full, map, signatures, diff, aggressive, entropy, lines). `SelectAdaptive`
picks by extension + line count only. A Choice primitive over these 8 modes — with
criteria describing each tradeoff — lets the engine recommend the right mode from
file size, structure, and diff availability instead of requiring the caller to know
each mode.

### 🥈 Call resolution tier — `internal/lsp/hybrid.go:159`

`ResolveCall` returns `TypedHit{QualifiedName, Confidence}` with a tier table
(receiver-method 0.97, same-module cross-file 0.98, same-file 0.94,
unique-project-wide 0.92). The confidence comes from static tier selection, not from
the call-site context. A Choice over strategies — `{direct, inferred, speculative, ambiguous}` —
classifies the certainty of the resolution so callers can decide inline-show vs. review-flag.

### 🥈 File-to-extractor routing — `internal/index/specialists.go:18`

`SpecialistClaim` is a filename/extension switch deciding which extractor claims a
file; unclaimed files are dropped. This is inherently a Choice problem — which
extractor owns this file? — currently solved with string patterns. A Choice primitive
handles ambiguous extensions or mixed-marker files (e.g., a `.ts` file that is
actually a Python config).

### 🥉 Discovery strategy — `internal/ontology/discover.go:118`

`Discover` switches between `ontology+concept` and `semantic+name`/`semantic+name_fallback`.
The choice is currently fixed by code. A Choice primitive picks based on query
characteristics — specific known concept → ontology path; open-ended query → semantic.

### 🥉 Node type classification — `internal/convo/types.go:73`

`Classify` returns `KindProblem` / `KindDecision` / `KindMilestone` / `KindPreference` /
`KindGeneral` via keyword matching. Discrete categories from an unordered set —
textbook Choice. A Choice primitive assigns labels with calibrated confidence instead
of the first matching prefix.

---

## Priority matrix

| Priority | Primitive | File:Line | Surface change | Behavior change |
|---|---|---|---|---|
| 🥈 | **Score** | `memory/banks.go:432` | Populate Score field | ✅ Done 2026-09-19 — BM25 score in `RankEntries`; typed tier extraction open |
| 🥇 | **Noul** | `core.go:510` | Guard before L3 call | L3 skipped on unworthy queries; happy path unchanged |
| 🥈 | **Score** | `store/pg_fts.go:113` | Add weight params to FuseRRF | Learned reranking replaces fixed RRFK |
| 🥈 | **Score** | `memory/banks.go:432` | Populate Score field | First-turn injection becomes proportional |
| 🥈 | **Noul** | `ontology/query.go:528` | Replace `len(matched)==0` | Downstream callers get uncertainty signal |
| 🥉 | **Choice** | `indexgate/indexgate.go:104` | Extend Decision enum | Fresh/Index boundary becomes graded |
| 🥉 | **Choice** | `compress/modes.go:131` | Add recommendation layer | Mode selection from context |
| 🥉 | **Choice** | `convo/types.go:73` | Replace keyword classifier | Better event classification |

---

## Constraints from the codebase

1. **`internal/summarize` is the only LLM client** — follow its `Chat` interface + prompt/JSON validation pattern for any new AI judgment. Don't invent a second client.
2. **Most heuristics are deliberate parity ports** from the deleted Rust tree — comments cite `source_file:line`. Changing behavior may break documented contracts.
3. **`store.Backend` interface** is the boundary — any judgment service takes it as a dependency, never a concrete store (same pattern as `QueryEmbedder` in `core.go:54`).
4. **Freshness is a read-only claim** — the watermark belongs to writers, readers compare it. Any Score over freshness must not affect the watermark, only how the label is reported.
5. **Noul ≠ "should I do it"** — a Noul near 0.5 means genuine uncertainty, not medium intent. Use it only when the binary yes/no loses information vs. the probability itself.
6. **Score needs concrete level definitions** — "high quality" is meaningless; "≥45 lines AND cyclomatic complexity >10" is a level. Every Score must anchor its criteria to real thresholds from the codebase.
7. **All three primitives run in parallel** — per the System One pattern, independent questions over the same state fire together at no extra latency.
