# FR-SM-10 / FR-SM-11 live-ish evidence — 2026-08-02

## Environment
- leankg version: 0.19.30 (worktree `prd/session-heat-promote`, HEAD `ef8c0360`)
- MCP: not used (pure lib seam; TempDir integration only)
- project=: TempDir fixture

## Steps
1. TDD: failing integration tests first (`tests/session_heat_promote_tests.rs`, 12 tests), then lib seam in `src/session/mod.rs`
2. Deterministic heat score: `heat_score(recalls, last_recalled_epoch_secs, now_epoch_secs) = log(1+recalls) + 0.5^(age/86400)`
3. Top-K promote: `MemoryIndex::top_k(k, now)` ranks by heat desc, tie-break key
4. Proposal shape: `SequenceMiner::propose(traces, positive_terms)` emits `WorkflowProposal` (name/description/steps/occurrences/confidence) — the `add_ontology_workflow` payload shape
5. No-ontology-write test: fixture `ontology/concepts.yaml` + `ontology/workflows.yaml` byte-identical after refresh + proposal write

## Results

| Gate | Result |
|------|--------|
| `cargo test --test session_heat_promote_tests` | 12 passed, 0 failed |
| `cargo test --lib` | 796 passed, 0 failed |
| `cargo test session` (filtered) | 53 passed, 0 failed (all suites) |
| `cargo fmt --all -- --check` | pass |
| `cargo clippy --all -- -D warnings` | pass, 0 warnings |

## Sample proposal (from `SequenceMiner` on 3x repeated trace)

```json
{
  "content_key": "9d1f3a2c7e4b",
  "proposal": {
    "name": "search_code_get_context_query_file",
    "description": "Repeated 3 times across sessions: search_code → get_context → query_file",
    "steps": ["search_code", "get_context", "query_file"],
    "occurrences": 3,
    "confidence": 0.75,
    "created_epoch_secs": 1754083200
  }
}
```

Written to `.leankg/proposals/workflows.jsonl` only. `ontology/workflows.yaml` untouched (asserted byte-for-byte in test).

## Sample MEMORY_INDEX.md render (top-K heat-ranked)

```markdown
# MEMORY_INDEX

- generated: 1754083200 (epoch secs; deterministic score: log(1+recalls) + 0.5^(age/86400))
- tracked: 2 | promoted (top-K): 2

## Hot sessions

| rank | key | recalls | last recall (age s) | heat | source |
|---|---|---|---|---|---|
| 1 | load_layer | 2 | 0 | 2.098612 | src/mcp/handler.rs |
| 2 | open_conversation | 1 | 0 | 1.693147 | src/conversation_indexer/mod.rs |

## Detail

- **load_layer** — recalls=2, heat=2.098612 (freq=1.098612, recency=1.000000)
  load_layer expands a layer
- **open_conversation** — recalls=1, heat=1.693147 (freq=0.693147, recency=1.000000)
  open_conversation parses Claude export JSON
```

## Tracker
- Mark US-SM-05, US-SM-06, FR-SM-10, FR-SM-11 DONE after merge (conductor owns tracker file).
