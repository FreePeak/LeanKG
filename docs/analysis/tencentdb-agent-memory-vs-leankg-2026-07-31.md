# LeanKG vs TencentDB Agent Memory — what to steal

**Date:** 2026-07-31  
**Upstream:** [TencentCloud/TencentDB-Agent-Memory](https://github.com/TencentCloud/TencentDB-Agent-Memory) (local clone under Freepeak polyrepo for reading)  
**Product IDs:** overlaps `US-GE-05` / `FR-GE-05` (self-improve write-back), token-economics mission in [`docs/prd.md`](../prd.md) §1.1; do **not** displace company-adoption P1.

## Thesis

TencentDB Agent Memory is a **conversation + persona + session-offload** hub for general agents (OpenClaw / Hermes). LeanKG is a **typed code/knowledge graph + MCP retrieval** layer for coding agents.

Steal their **memory architecture patterns** (layering, symbolic short-term, recoverable evidence, auto capture/recall, hybrid ranking). Do **not** turn LeanKG into a chat-memory product or an agent harness (already Won’t Do in PRD §1.2).

## Fit summary

| Tencent capability | Fit | LeanKG today | Adapt? |
|--------------------|-----|--------------|--------|
| Short-term Mermaid offload + `node_id` → `refs/*.md` | **Missing** (highest token ROI) | Response compression (`ctx_read`, `compress_response`, RTK/TOON); no session tool-log canvas | **Yes — session offload over MCP results** |
| L0 chat → L1 atom → L2 scene → L3 persona pyramid | Partial / different domain | `load_layer` L0–L3 = **code** context (identity / facts / cluster / search), not conversation memory | Reuse *pattern* for agent artifacts only |
| Auto-capture + auto-recall hooks | Partial | Manual: `add_knowledge`, `agent_diary_*`, `report_query_outcome` → `LESSONS.md` | **Yes — close US-GE-05** |
| Hybrid BM25 + vector + RRF recall | Partial | Strong for **code** (concept → semantic → keyword); knowledge/diary/lessons mostly keyword | **Yes — RRF over agent memory stores** |
| White-box Markdown + heat-ranked scene nav | Partial | `identity.md`, `critical_facts.md`, cluster `SKILL.md`, diary JSONL | **Yes — MEMORY_INDEX + heat** |
| Provenance chain (persona → scene → atom → raw) | Weak | Writes often lack `source_ids` / stable drill-down IDs | **Yes — provenance on durable writes** |
| Skill/SOP distillation from traces | Partial | `add_ontology_workflow`, `get_cluster_skill` | Promote successful tool paths → workflows |
| HostAdapter (OpenClaw / Hermes / Gateway) | Out of scope as product | MCP-first already | Optional Cursor/OpenCode *hooks* only |
| SQLite + sqlite-vec / Tencent VDB backends | Out of scope | CozoDB + embeddings | Keep |
| Retention / reclaim (`l0l1RetentionDays`) | Partial | Graph GC exists; diary/knowledge can grow unbounded | GC for agent memory artifacts |
| Become chat-memory SoT / Mem0 competitor | **Out of scope** | Code graph SoT | Positioning only |

## Verdict

- **Adapt** layering + symbolization + auto write-back as **agent session memory on top of the code graph**.
- **Do not** rebuild Tencent’s conversation L0–L3 pyramid as LeanKG’s core product.
- **Highest ROI:** (1) session MCP-result offload with `node_id` drill-down, (2) auto-recall of lessons/diary at session start (US-GE-05), (3) provenance + hybrid recall over knowledge/diary/lessons.

## Explicit non-goals

- Competing with Mem0 / Tencent on long-term **chat** persona memory.
- Binding LeanKG to OpenClaw, Hermes, or Tencent Vector DB.
- Replacing CozoDB’s typed graph with Mermaid as the primary knowledge store (Mermaid = session compression UI only).
- Owning a multi-agent planner/harness (US-GF-17 install/hooks only).

---

## Upstream architecture (compressed)

Two pillars from their README:

### 1. Memory layering + progressive disclosure

| Layer | Role | Storage |
|-------|------|---------|
| Short-term bottom | Raw tool outputs | `refs/*.md` |
| Short-term mid | Step summaries | JSONL |
| Short-term top | Task state | Mermaid canvas + `node_id` |
| Long-term L0 | Raw dialogue | Conversation store |
| Long-term L1 | Atomic facts | Indexed records + vectors |
| Long-term L2 | Scenarios / scenes | Markdown scene blocks |
| Long-term L3 | Persona | `persona.md` |

**Rule they enforce:** lower layers preserve evidence; upper layers preserve structure. Compression must remain expandable via deterministic IDs.

### 2. Symbolic short-term memory

Verbose tool logs leave the context window; the agent keeps a small Mermaid map and greps/`read_file` by `node_id` when detail is needed. Reported gains (their benches, continuous long sessions): up to ~61% fewer tokens / ~52% relative pass-rate lift on WideSearch; PersonaMem 48% → 76%.

### 3. Production mechanics worth noting

- Zero-config local `SQLite + sqlite-vec`; optional remote embeddings.
- Auto-capture → pipeline schedule (`everyNConversations`, idle timeouts) → L1 extract + dedup → L2 scenes → L3 persona.
- Auto-recall injects L1 hits + persona + scene navigation with char budgets and timeout so recall never blocks the turn.
- Hybrid recall: keyword (BM25) + embedding + **RRF** (`k=60`).
- White-box paths under `~/.openclaw/memory-tdai/` for human/agent inspection.
- Retention / reclaim for L0–L1 and offload data.

---

## LeanKG today (relevant surfaces)

| Concern | Existing surface |
|---------|------------------|
| Session start overview | `get_overview_context` (prefer over bare `load_layer(L0)`) |
| Progressive code layers | `load_layer` L0–L3 |
| Agent persona / diary | `agent_focus`, `agent_diary_write`, `agent_diary_read` |
| Reflect loop | `report_query_outcome` → `.leankg/reflections/LESSONS.md` |
| Free-form + domain memory | `add_knowledge` / `search_knowledge`, ontology concepts & workflows |
| Token compression | `ctx_read`, `compress_response`, RTK/TOON, `orchestrate` cache |
| Cluster skills | `get_cluster_skill` |
| Self-improve gap | **US-GE-05** — outcome → durable artifact → next plan (Partial) |

LeanKG’s L0–L3 **names collide** with Tencent’s but mean different things. Do not merge vocabularies without an explicit rename (e.g. “session memory L0–L3” vs “code context L0–L3”).

---

## Recommended improvements (priority)

### P0 — Session tool-output offload + `node_id` drill-down

**Problem:** Long agent sessions burn tokens replaying large MCP/tool payloads. LeanKG already compresses individual responses; it does not maintain a **session-level** symbolic map.

**Proposal:**

1. After N MCP tool calls (or when estimated context exceeds a ratio), write full payloads under `.leankg/sessions/<session_id>/refs/<node_id>.md`.
2. Maintain a compact canvas (Mermaid or existing graph JSON) listing steps + `node_id`s.
3. Inject only the canvas (+ last few turns) into agent context; recover via `ctx_read` / a thin `session_recall(node_id=…)` tool.

**Why this is on-brand:** directly serves “Stop Burning Tokens”; operates on **MCP/code-tool** artifacts, not chat transcripts.

**Acceptance sketch:**

- [ ] Offload triggers without user action when budget threshold hit.
- [ ] Canvas + `node_id` recoverable to original payload bit-for-bit (or lossless reference).
- [ ] Measurable token reduction on a fixed multi-tool fixture (≥30% vs baseline replay).

### P0 — Auto-recall at session start (close US-GE-05)

**Problem:** Useful lessons die in `LESSONS.md` / diary unless the agent remembers to call tools.

**Proposal:**

1. Optional hook / MCP bootstrap: after `mcp_status`, auto-inject top-K lessons + recent diary tags into `get_overview_context` (or a sibling `get_memory_context`).
2. Auto-append `report_query_outcome` into a ranked lessons index (not only append-only Markdown).
3. Dedup / conflict check before write (vector or hash), mirroring their L1 dedup.

**Acceptance sketch:**

- [ ] New session without manual diary/knowledge calls still receives ranked prior lessons.
- [ ] Duplicate useful/dead_end reports do not spam the index.
- [ ] Documented off-switch (zero-config default vs opt-in — prefer opt-in until measured).

### P1 — Provenance on durable agent writes

**Problem:** Knowledge / diary / ontology / LESSONS often lack a chain back to evidence.

**Proposal:** Require (or strongly encourage) `source_ids` / `node_id` / tool-call refs on:

- `add_knowledge`, `add_ontology_concept`, `add_ontology_workflow`
- `agent_diary_write`
- `report_query_outcome`

Drill-down path: summary artifact → mid index → raw ref / code element / MCP payload.

### P1 — Hybrid RRF over agent memory stores

**Problem:** Code search is dual-path; agent memory search is not.

**Proposal:** One retrieval path (extend `search_knowledge` or add `search_agent_memory`) that RRF-merges:

- knowledge entries  
- diary notes  
- LESSONS / reflections  
- dynamic ontology concepts  

Guards to copy from Tencent: `maxResults`, score threshold, per-item / total char budgets, recall timeout.

### P2 — Heat-ranked `MEMORY_INDEX.md`

White-box index listing hot lessons, recent diary tags, and ontology concepts by hit count from outcomes + search telemetry. Absolute paths so agents can `ctx_read` without path guessing.

### P2 — Promote successful traces → ontology workflows

When a multi-tool sequence succeeds repeatedly (or a workflow is traced often), propose / auto-write `add_ontology_workflow` with `code_refs`. Reuse existing ontology SoT — do not invent a second skill store (aligns with their skill-layering roadmap and LeanKG’s `get_cluster_skill`).

### P3 — Retention / GC for agent memory artifacts

Apply retention days + `pinned` / high-heat exceptions to diary, lessons index, and session `refs/`. Reclaim stale offload files.

---

## Mapping: Tencent pattern → LeanKG artifact

| Tencent | LeanKG analogue (proposed or existing) |
|---------|----------------------------------------|
| Mermaid task canvas | `.leankg/sessions/<id>/canvas.mmd` (or graph JSON) |
| `refs/*.md` | `.leankg/sessions/<id>/refs/<node_id>.md` |
| L1 atoms | Knowledge entries + diary notes with provenance |
| L2 scenes | Scene-like Markdown blocks **or** ontology workflow steps |
| L3 persona | `.leankg/agents/<name>.json` + distilled diary summary (not chat persona) |
| Scene heat | Hit counts from `report_query_outcome` / search |
| `tdai_memory_search` | Hybrid `search_agent_memory` / extended `search_knowledge` |
| Auto-recall prepend | Enrich `get_overview_context` |
| RRF hybrid | Shared RRF helper over FTS + HNSW for memory stores |

---

## What LeanKG already wins (do not regress)

- Typed code graph at monorepo / mega-graph scale (CozoDB, multi-project Docker).
- Surgical MCP prefer-order: `concept_search` → `semantic_search` → `search_code` → connection verbs.
- Ontology + procedural workflows as durable team knowledge (not only personal chat memory).
- Measured agent economics (TOON/RTK, budgeted tools) as company platform vs personal skill.

Tencent’s strength is **session continuity for general agents**. LeanKG’s strength is **shared structural memory for codebases**. The win is to add session continuity *around* the graph, not instead of it.

---

## Suggested next doc / PRD steps

1. Add a short PRD slice (e.g. `US-SM-01` session offload, `US-SM-02` auto memory recall) under Focus P2 — do not displace §1.1 P1 packaging.
2. Wire `US-GE-05` acceptance criteria to auto-recall + ranked lessons index.
3. Keep competitive positioning: LeanKG = graph memory under agents; Tencent = conversation/persona memory for agent hosts.

## See also

- [`docs/prd.md`](../prd.md) §1.1 token economics, §1.2 / US-GE-05 self-improve loop  
- [`docs/analysis/graph-engineering-roadmap-vs-leankg-2026-07-21.md`](graph-engineering-roadmap-vs-leankg-2026-07-21.md) — related “self-improve / harness boundary” analysis  
- [`docs/analysis/graphify-vs-leankg-2026-07-20.md`](graphify-vs-leankg-2026-07-20.md) — company vs personal graph tooling  
- Upstream README: progressive disclosure + Mermaid offload architecture  
