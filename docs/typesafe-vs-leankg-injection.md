# Why Jev Is Deterministic, LeanKG Is Primitive, and the Agent Keeps Injecting

**Date:** 2026-09-19

## TL;DR

- **Jev** (TypeSafe System One) was evaluated for routing/gating/ranking and
  **rejected**: the user direction is no TypeSafe dependency, and the probe API
  key already returns 401. `docs/typesafe-primitive-map.md` is retained only as
  a reference map of where the primitives would have fitted.
- **LeanKG** stores primitives (paths, element IDs, float32 vectors, watermarks)
  and dereferences them step by step. Its scores are native BM25 floats (memory
  recall, implemented 2026-09-19) and cosine distances / tsvector / trigram RRF
  (code search) — no external judgment service.
- **Agent injection** is a deterministic BM25-ranked JSONL loop, not an LLM
  decision. The agent writes via `session_retain`; LeanKG re-renders via
  `/inject` on the next turn.

## The Injection Loop

```
Agent turn
  → session_retain → POST /v1/default/banks/{bank}/memories
                        ↓
                  JSONL bank write (deterministic)
                        ↓
Next turn
  → session_recall → GET /banks/{bank}/inject → InjectBlock()
                                                       ↓
                                           BM25 rank → cap 8 → 5000 tok → markdown
```

No LLM involved. `InjectBlock` (`internal/memory/banks.go:542`) ranks by native
BM25, caps at `RecallLimit=8` and `InjectionTokenLimit=5000`
(`internal/memory/scope.go:80`), and renders `<memories>`.

## What Actually Uses ML in LeanKG

| Component | Role | Involved in injection? |
|---|---|---|
| **Embedding model** (bge-small-en-v1.5 sidecar) | L3 vector search (`core.go:574`) | No |
| **InjectBlock** | BM25 rank + budget cap + render | Yes |

No Jev: TypeSafe was rejected (see TL;DR). The embedder produces vectors for
L3 to search; neither touches `<memories>` injection.

## If You Want to Break the Loop

1. **Don't retain** — disable `hindsight` backend or set `LEANKG_MEMORY=false`.
2. **Skip the read** — `FirstTurnMemories` returns empty when the bank is empty.
3. **Cap harder** — lower `RecallLimit` / `InjectionTokenLimit` in `internal/memory/scope.go`.

## Related

- `docs/typesafe-primitive-map.md` — where Score/Noul/Choice would fit in LeanKG
- `docs/prd.md` §3.10 / §5.1 — self-host dogfood runbook
- `internal/memory/banks.go` — `InjectBlock`, `RankEntries`, `RecallBanks`
- `internal/rest/rest.go:258` — `/api/v1/memory/banks/{bank}/inject` route
