# Technical Proposal: Hybrid Search and Re-Ranking in LeanKG

## Overview
Based on industry best practices for Production RAG (Retrieval-Augmented Generation), this document proposes an upgrade to LeanKG's retrieval engine. The goal is to move from primitive keyword heuristics to a multi-stage **Hybrid Search + Re-Ranking** pipeline to improve both Context Recall and Context Precision when agents query the codebase.

## Current Limitations
LeanKG's current `semantic_search` implementation (`src/mcp/handler.rs`) uses a basic keyword matching strategy:
- **Heuristic-based:** Arbitrary weights (+3.0 for name, +1.0 for qualified name).
- **No Vector Support:** Cannot find "conceptual" matches (e.g., searching for "persistence" won't find `database.rs` unless the word "persistence" is in the code).
- **No Statistical Ranking:** Does not use BM25 or TF-IDF, making it less effective than standard search engines for large codebases.

## Proposed Multi-Stage Pipeline

### Stage 1: Metadata Filtering (Pre-Retrieval)
Leverage LeanKG's existing Knowledge Graph structure to prune the search space.
- **Filters:** `element_type` (function/class/file), `language`, `environment` (production/staging).
- **Benefit:** Reduces noise and computational cost for subsequent stages.

### Stage 2: Hybrid Search (Recall Optimization)
Combine Sparse and Dense search results using **Relative Score Fusion (RSF)**.

#### A. Sparse Search (BM25)
- **Goal:** Exact term matching for identifiers (e.g., `handleOAuthCallback`).
- **Implementation:** Utilize CozoDB's full-text search capabilities or implement a statistical BM25 query in Datalog.

#### B. Dense Search (Vector Embeddings)
- **Goal:** Semantic similarity for conceptual queries (e.g., "how is auth handled?").
- **Implementation:** Integrate `fastembed-rs` or `candle` to generate local embeddings using a model like `all-MiniLM-L6-v2`.

#### C. Fusion
Combine scores using the **Alpha ($\alpha$) parameter**:
$$Score = \alpha \cdot VectorScore + (1 - \alpha) \cdot KeywordScore$$
- $\alpha = 0.0$: Pure Keyword (Code navigation)
- $\alpha = 1.0$: Pure Semantic (Abstract questions)
- $\alpha = 0.5$: Balanced (Default)

### Stage 3: Re-Ranking (Precision Optimization)
Re-score the top candidates to ensure the most relevant context is sent to the LLM.
- **Mechanism:** Take the top 50 results from Hybrid Search and pass them through a lightweight **Cross-Encoder** (e.g., `BGE-reranker-v2-m3`).
- **Benefit:** Drastically improves "Context Precision," ensuring the agent sees the right code first.

## Implementation Roadmap

### Phase 1: Statistical Sparse Search
1. Replace current keyword loops with a Datalog-based BM25 implementation.
2. Update `perform_semantic_search` to return statistical scores.

### Phase 2: Local Vector Integration
1. Add `fastembed` crate to `Cargo.toml`.
2. Implement a background indexing task to generate embeddings for `CodeElement` names and annotations.
3. Store vectors in CozoDB (leveraging its vector storage capabilities).

### Phase 3: Re-Ranking Tool
1. Create a new internal tool `rerank_results`.
2. Integrate a quantized Cross-Encoder model for local execution.
3. Update the `orchestrate` (`BS`) tool to automatically apply re-ranking on high-uncertainty queries.

## Metrics for Success
We will evaluate this implementation using the **RAGAS Framework**:
- **Context Recall:** Does Hybrid Search find relevant code that Keyword Search missed?
- **Context Precision:** Is the exact function the user needs in the top 3 results?
- **Token Efficiency:** Does better precision allow us to send fewer, more relevant files to the LLM?

## References
- [Hybrid Search and Re-Ranking in Production RAG (Towards Data Science)](https://towardsdatascience.com/hybrid-search-and-re-ranking-in-production-rag/)
- [CozoDB Vector Search Documentation](https://docs.cozodb.org/en/latest/vector.html)
- [FastEmbed-RS](https://github.com/Anirudh07/fastembed-rs)
