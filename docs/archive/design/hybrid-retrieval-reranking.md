# Hybrid Retrieval and Re-ranking Design for LeanKG

## Overview

This document captures lessons from production RAG retrieval patterns and applies them to LeanKG's code knowledge graph. The core idea is simple: retrieval quality should not depend on one search strategy. Codebase questions often mix exact symbols, natural language, graph structure, environment scope, incidents, and team ownership.

LeanKG should evolve from tiered fallback retrieval into hybrid candidate generation, deterministic fusion, and graph-aware re-ranking.

**Reference material:**
- Towards Data Science summary: [Hybrid Search and Re-Ranking in Production RAG](https://app.daily.dev/posts/hybrid-search-and-re-ranking-in-production-rag-u32wjtkwm)
- Related production RAG retrieval overview: [Hybrid Search and Re-ranking in Production RAG: BM25, Dense Vectors, Cross-encoders, and Everything In Between](https://appscale.blog/en/blog/hybrid-search-and-reranking-production-rag-bm25-dense-cross-encoder-2026)

## 1. Problem

LeanKG already has search tools, graph traversal, environment fields, incidents, and token-bounded MCP responses. The weak point is ranking.

Current behavior has several risks:

- Exact-name search works well for known symbols, but fails on natural-language questions.
- Keyword-style semantic search can miss code concepts expressed through function names, file paths, comments, docs, or incident fields.
- A fallback chain stops too early: if exact or fuzzy search returns weak results, better graph or semantic candidates may never be considered.
- Search results do not explain why a candidate ranked highly.
- There is no retrieval evaluation loop for measuring whether ranking changes improve agent answers.

The planned PRD currently describes a 3-tier retrieval flow:

```text
Exact match -> fuzzy match -> semantic embed / keyword fallback
```

That should be changed to:

```text
metadata pre-filter -> parallel candidate retrieval -> fusion -> graph-aware re-ranking -> token-bounded response
```

## 2. Design Goals

- Improve recall for mixed queries like "how does the refund flow work" or "where is auth permission checked".
- Preserve precision for exact technical identifiers like function names, service IDs, incident IDs, schema names, and endpoint paths.
- Keep LeanKG lightweight. Avoid requiring hosted vector databases or external re-rank APIs for the first version.
- Make ranking explainable in MCP responses.
- Support environment-scoped and team-scoped retrieval before ranking.
- Add an evaluation harness so retrieval changes are measurable.

## 3. Proposed Architecture

### 3.1 Retrieval Pipeline

```text
User query
  |
  v
Query normalization
  |
  v
Metadata pre-filter
  |
  v
Parallel retrievers
  - exact symbol search
  - fuzzy name search
  - lexical/BM25-style search
  - graph neighborhood expansion
  - incident and documentation search
  - optional embedding search
  |
  v
Candidate fusion
  |
  v
Graph-aware re-ranking
  |
  v
Token-bounded MCP response with ranking diagnostics
```

### 3.2 Metadata Pre-filter

Filtering should happen before ranking whenever possible. This avoids ranking irrelevant candidates and prevents cross-scope leakage.

Supported filters:

| Filter | Purpose |
|---|---|
| `env` | Restrict to `local`, `staging`, or `production` graph data |
| `service` | Restrict to a service or service path |
| `repo` | Restrict to one repository in multi-repo deployments |
| `language` | Restrict to Go, Rust, TypeScript, Python, Kotlin, etc. |
| `element_type` | Restrict to files, functions, services, incidents, docs, schemas |
| `team` | Restrict to owned services or ownership metadata |
| `changed_only` | Restrict to recently changed files for review workflows |
| `exclude_worktrees` | Exclude `.worktrees/` and temporary agent worktrees by default |

### 3.3 Candidate Retrievers

LeanKG should generate candidates from multiple retrievers in parallel.

| Retriever | Best For | Example Query |
|---|---|---|
| Exact symbol | Known names and IDs | `get_impact_radius` |
| Fuzzy name | Partial names, typo-tolerant names | `impact radius` |
| Lexical search | Words in paths, names, signatures, docs, incidents | `auth permission checked` |
| Graph expansion | Related callers, callees, dependencies, tested-by links | `what breaks if schema changes` |
| Incident search | Known failures, trigger patterns, prevention | `similar payment timeout incident` |
| Documentation search | PRD, HLD, ERD, README concepts | `refund flow` |
| Embedding search | Broader natural-language similarity | `where do we validate access rights` |

The first version can ship without embeddings. Exact, fuzzy, lexical, graph, incident, and docs retrieval already fit LeanKG's current architecture.

## 4. Fusion Strategy

Use Reciprocal Rank Fusion (RRF) for the first implementation. It combines ranked lists without requiring calibrated scores.

```text
rrf_score(candidate) = sum(1 / (k + rank_in_retriever))
```

Recommended default:

```text
k = 60
```

Each retriever returns a bounded list of candidates:

```rust
struct RetrievalCandidate {
    qualified_name: String,
    name: String,
    element_type: String,
    file_path: String,
    line_start: u32,
    line_end: u32,
    env: String,
    retriever: String,
    rank: usize,
    raw_score: f64,
    matched_fields: Vec<String>,
}
```

Fusion groups candidates by stable identity:

```text
identity = qualified_name if present, otherwise file_path + line_start + element_type
```

## 5. Graph-aware Re-ranking

After fusion, LeanKG should re-rank the top candidates using code-graph features. This is the equivalent of a re-ranker in production RAG, but specialized for code intelligence.

### 5.1 Initial Deterministic Re-ranker

Start with a deterministic weighted score:

| Feature | Signal |
|---|---|
| Exact query match | Strong boost for exact symbol, service, endpoint, or incident ID |
| Field match | Name match > qualified name match > path match > metadata match |
| Element type | Boost if query intent implies file/function/service/incident/doc |
| Graph proximity | Boost candidates connected to already matched symbols |
| Call relationship | Boost callers/callees for "how does this flow work" |
| Dependency relationship | Boost dependents for impact-style queries |
| Tested-by relationship | Boost tests for validation/debug queries |
| Incident relationship | Boost known incidents for risky or production queries |
| Environment match | Boost active environment, penalize cross-env unless requested |
| Freshness | Boost recently changed or recently indexed code for review contexts |
| Cluster/service match | Boost candidates in the same service or cluster as top hits |

### 5.2 Later LLM or Cross-encoder Re-ranker

Once deterministic ranking is measured, optionally add an expensive re-rank step for the top `N` candidates only.

Candidate pattern:

```text
retrieve top 50 -> deterministic re-rank top 20 -> optional model re-rank top 10 -> return top K
```

This keeps latency bounded and avoids sending the full graph to an external model.

## 6. MCP Response Shape

Search responses should include ranking diagnostics so agents can reason about confidence and users can debug poor results.

Example:

```json
{
  "query": "where is auth permission checked",
  "env": "local",
  "results": [
    {
      "qualified_name": "./src/mcp/auth.rs::check_permission",
      "name": "check_permission",
      "type": "function",
      "file": "./src/mcp/auth.rs",
      "line": 106,
      "final_score": 0.91,
      "retrievers": ["lexical", "fuzzy", "graph"],
      "scores": {
        "rrf": 0.046,
        "rerank": 0.91
      },
      "matched_fields": ["name", "qualified_name"],
      "rank_reason": "Matched permission terms in function name and auth module; function is directly related to MCP authorization."
    }
  ],
  "diagnostics": {
    "candidate_count": 48,
    "filtered_count": 12,
    "latency_ms": 14,
    "fusion": "rrf",
    "reranker": "deterministic_graph_v1"
  }
}
```

## 7. Query Intent Hints

LeanKG should classify lightweight query intent before ranking. This does not need an LLM.

| Intent | Query Clues | Ranking Bias |
|---|---|---|
| Locate symbol | `where is`, exact code token, camelCase, snake_case | exact/fuzzy name |
| Explain flow | `how does`, `flow`, `path`, `trace` | call graph, service calls |
| Impact analysis | `what breaks`, `impact`, `dependents` | dependents, tests, schemas |
| Debug incident | `incident`, `outage`, `error`, `root cause` | incidents, services, recent changes |
| Test discovery | `test`, `covered`, `validation` | tested-by relationships |
| Documentation | `requirement`, `design`, `PRD`, `HLD`, `ERD` | docs and traceability links |

Intent should be returned in diagnostics:

```json
{
  "intent": "impact_analysis",
  "confidence": 0.78
}
```

## 8. Evaluation Harness

Retrieval changes should be measured with a small, versioned eval set.

### 8.1 Eval Data Format

Store evals under:

```text
docs/eval/retrieval-cases.yaml
```

Example:

```yaml
- id: locate-semantic-search
  query: "where is semantic_search implemented"
  expected:
    - "./src/mcp/handler.rs::semantic_search"
    - "./src/mcp/handler.rs::perform_semantic_search"
  tags: ["symbol", "mcp"]

- id: explain-impact-radius
  query: "what computes impact radius"
  expected:
    - "./src/mcp/handler.rs::get_impact_radius"
  tags: ["impact", "mcp"]

- id: auth-permission
  query: "where is auth permission checked"
  expected:
    - "./src/mcp/auth.rs::check_permission"
  tags: ["auth", "symbol"]
```

### 8.2 Metrics

| Metric | Meaning |
|---|---|
| `hit@1` | Expected result is ranked first |
| `hit@3` | Expected result appears in top 3 |
| `hit@10` | Expected result appears in top 10 |
| `mrr` | Mean reciprocal rank of first expected result |
| `candidate_count` | Number of candidates before final ranking |
| `latency_ms` | End-to-end retrieval latency |
| `token_count` | Response size after compression |

### 8.3 Acceptance Targets

Initial targets:

| Metric | Target |
|---|---|
| `hit@3` | >= 85% on repo eval set |
| `mrr` | >= 0.70 |
| p95 latency | < 50 ms without embeddings |
| p95 response size | Within configured MCP token budget |

## 9. Implementation Plan

### Phase 1: Retrieval Core

1. Add `src/search/` module.
2. Add `RetrievalCandidate`, `RetrievalRequest`, `RetrievalResponse`, and `RankingDiagnostics`.
3. Implement exact, fuzzy, lexical, graph, incident, and docs retriever traits.
4. Implement RRF fusion.
5. Implement deterministic graph-aware re-ranker.

### Phase 2: MCP Integration

1. Change `semantic_search` to call the hybrid retrieval core.
2. Add optional `debug_ranking` input to search MCP tools.
3. Add `hybrid_search` MCP tool if compatibility requires keeping `semantic_search` unchanged.
4. Enforce token budgets after ranking, not before candidate generation.

### Phase 3: Evaluation

1. Add `docs/eval/retrieval-cases.yaml`.
2. Add `cargo run -- eval retrieval` command or a test helper.
3. Track hit@k, MRR, latency, candidate counts, and token counts.
4. Add regression tests for key repository queries.

### Phase 4: Optional Embeddings

1. Add feature-gated embedding provider support.
2. Store embeddings in a separate relation or local sidecar index.
3. Run embedding retrieval as one candidate source, not as the only retrieval source.
4. Keep exact and lexical retrieval mandatory for code identifiers.

## 10. Required Code Touchpoints

| Area | Expected Change |
|---|---|
| `src/mcp/handler.rs` | Route `semantic_search` through hybrid retrieval; return diagnostics |
| `src/mcp/tools.rs` | Add input schema for ranking debug fields |
| `src/graph/query.rs` | Add bounded lexical/doc search helpers where DB-level filtering is possible |
| `src/db/models.rs` | Add search candidate structs only if shared beyond `src/search` |
| `docs/requirement/prd-leankg.md` | Replace fallback-only FR-08 with hybrid retrieval language |
| `tests/` | Add retrieval ranking tests and eval fixtures |

## 11. Risks and Mitigations

| Risk | Mitigation |
|---|---|
| Ranking becomes hard to reason about | Return diagnostics and matched fields |
| Latency grows with multiple retrievers | Run bounded retrieval, cap candidates, cache results |
| Worktree results pollute rankings | Apply `exclude_worktrees` filter by default |
| Graph expansion returns too much | Use bounded depth and relationship-type filters |
| Embeddings miss exact identifiers | Keep exact and lexical retrieval mandatory |
| Eval set overfits current repo | Tag eval cases by intent and add cases from real user failures |

## 12. Recommended PRD Change

Replace the current FR-08 description:

```text
For every query, LeanKG tries retrieval in order, stopping at the first tier that returns results.
```

With:

```text
For every natural-language or code search query, LeanKG applies metadata filters, gathers bounded candidates from exact, fuzzy, lexical, graph, documentation, incident, and optional embedding retrievers, fuses candidate rankings with RRF, then applies graph-aware re-ranking before returning token-bounded MCP output with ranking diagnostics.
```

## 13. Success Criteria

- `semantic_search` returns better results for natural-language codebase questions without losing exact symbol precision.
- Search responses explain ranking decisions.
- Retrieval evals run locally and fail on clear ranking regressions.
- Environment and worktree filters are applied before ranking.
- The design can run without external ML services.
- Embeddings remain optional and additive.

