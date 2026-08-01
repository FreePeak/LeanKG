# LeanKG vs TencentDB Agent Memory — what to steal

**Date:** 2026-07-31 (deepened 2026-08-01)  
**Upstream:** [TencentCloud/TencentDB-Agent-Memory](https://github.com/TencentCloud/TencentDB-Agent-Memory) (local clone under Freepeak polyrepo)  
**Product IDs:** `US-SM-01..07` / `FR-SM-*` / `REL-075` (PRD §1.3 / §3.28 / §5.32); closes/extends `US-GE-05` / `FR-GE-05`. Do **not** displace company-adoption P1.

## Thesis

TencentDB Agent Memory is a **conversation + persona + session-offload** hub for general agents (OpenClaw / Hermes). LeanKG is a **typed code/knowledge graph + MCP retrieval** layer for coding agents.

Steal their **memory architecture patterns** (layering, symbolic short-term, recoverable evidence, auto capture/recall, hybrid ranking, typed L1 atoms, retention). Do **not** turn LeanKG into a chat-memory product or an agent harness (already Won’t Do in PRD §1.2).

## Fit summary

| Tencent capability | Fit | LeanKG today | Adapt? |
|--------------------|-----|--------------|--------|
| Short-term Mermaid offload + `node_id` → `refs/*.md` | **Missing** (highest token ROI) | Response compression (`ctx_read`, RTK/TOON); no session tool-log canvas | **Yes — session offload over MCP results** (`US-SM-01`) |
| L0 chat → L1 atom → L2 scene → L3 persona pyramid | Partial / different domain | `load_layer` L0–L3 = **code** context, not conversation memory | Reuse *pattern* for agent artifacts only — **do not rename** LeanKG layers |
| Typed L1 atoms (`persona` / `episodic` / `instruction`) + priority | Partial | Free-form `add_knowledge` types; diary JSONL untyped | **Yes — typed agent memory kinds** (`US-SM-03`) |
| Auto-capture + scheduled L1→L2→L3 pipeline (warmup 1→2→4…) | Partial | Manual: `add_knowledge`, `agent_diary_*`, `report_query_outcome` | **Yes — close US-GE-05 via US-SM-02** |
| Auto-recall with timeout + char budgets + tools guide | Partial | Agent must remember to call tools | **Yes — enrich `get_overview_context`** |
| Hybrid BM25 + vector + RRF (`k=60`) | Partial | Strong for **code**; knowledge/diary/lessons mostly keyword | **Yes — RRF over agent memory** (`US-SM-04`) |
| L1 batch dedup / conflict (vector→FTS→skip) | Weak | Writes can spam LESSONS / knowledge | **Yes — dedup before durable write** |
| White-box Markdown + heat-ranked scene nav | Partial | `identity.md`, cluster `SKILL.md`, diary JSONL | **Yes — MEMORY_INDEX + heat** (`US-SM-05`) |
| Provenance chain (persona → scene → atom → raw) | Weak | Writes often lack `source_ids` / stable drill-down IDs | **Yes** (`US-SM-03`) |
| Skill/SOP distillation from traces | Partial | `add_ontology_workflow`, `get_cluster_skill` | **Yes — promote successful tool paths** (`US-SM-06`) |
| HostAdapter (OpenClaw / Hermes / Gateway) | Out of scope as product | MCP-first already | Optional Cursor/OpenCode *hooks* only |
| SQLite + sqlite-vec / Tencent VDB backends | Out of scope | CozoDB + embeddings | Keep |
| Retention / reclaim (`l0l1RetentionDays`, offload reclaim) | Partial | Graph GC exists; diary/knowledge/session refs unbounded | **Yes** (`US-SM-07`) |
| Become chat-memory SoT / Mem0 competitor | **Out of scope** | Code graph SoT | Positioning only |

## Verdict

- **Adapt** layering + symbolization + auto write-back as **agent session memory on top of the code graph**.
- **Do not** rebuild Tencent’s conversation L0–L3 pyramid as LeanKG’s core product.
- **Highest ROI (ordered):**
  1. Session MCP-result offload with `node_id` drill-down (`US-SM-01`)
  2. Auto-recall of lessons/diary at session start (`US-SM-02` → closes `US-GE-05`)
  3. Provenance + typed kinds + hybrid RRF over knowledge/diary/lessons (`US-SM-03` / `US-SM-04`)

## Explicit non-goals

- Competing with Mem0 / Tencent on long-term **chat** persona memory.
- Binding LeanKG to OpenClaw, Hermes, or Tencent Vector DB.
- Replacing CozoDB’s typed graph with Mermaid as the primary knowledge store (Mermaid = session compression UI only).
- Owning a multi-agent planner/harness (US-GF-17 install/hooks only).
- Renaming LeanKG `load_layer` L0–L3 to match Tencent’s conversation pyramid (name collision — keep code-context vocabulary).

---

## Upstream architecture (deep dive 2026-08-01)

Two pillars from their README + source under `src/core/` and `src/offload/`:

### 1. Memory layering + progressive disclosure

| Layer | Role | Storage |
|-------|------|---------|
| Short-term bottom | Raw tool outputs | `refs/*.md` |
| Short-term mid | Step summaries | JSONL (`offload-*.jsonl`) |
| Short-term top | Task state | Mermaid canvas + `node_id` (`NNN-N#`) |
| Long-term L0 | Raw dialogue | Conversation store (+ optional vectors) |
| Long-term L1 | Atomic facts | Typed records + FTS + vectors |
| Long-term L2 | Scenarios / scenes | Markdown scene blocks + index |
| Long-term L3 | Persona | `persona.md` (white-box) |

**Rule they enforce:** lower layers preserve evidence; upper layers preserve structure. Compression must remain expandable via deterministic IDs (`node_id`, `result_ref`, `source_message_ids`).

### 2. Symbolic short-term memory (context offload)

Verbose tool logs leave the context window; the agent keeps a small Mermaid map and recovers by `node_id`. Injection is marker-based (`_mmdContextMessage`) so L3 compression can skip the canvas. L2 Mermaid regenerates independently when enough `node_id=null` offload entries accumulate or a timeout fires — not chained blindly off every L1.

Reported gains (their benches, continuous long sessions): up to ~61% fewer tokens / ~52% relative pass-rate lift on WideSearch; PersonaMem 48% → 76%.

### 3. Long-term pipeline mechanics (production-grade)

From `pipeline-manager.ts`, `auto-capture.ts`, `auto-recall.ts`, `l1-extraction.ts`, `l1-dedup.ts`:

| Mechanic | Detail | Steal for LeanKG? |
|----------|--------|-------------------|
| **Warm-up schedule** | New sessions extract at 1→2→4→…→N turns, then steady `everyNConversations` | Yes — early lessons land fast; mature sessions cost less |
| **Idle flush** | L1 on idle timeout; L2 downward-only timer (never postpones) | Yes for session offload flush |
| **Typed L1 atoms** | Only `persona` / `episodic` / `instruction`; priority scoring; “宁缺毋滥” | Yes — map to knowledge types + diary tags |
| **Batch conflict dedup** | Vector recall → FTS fallback → skip if neither; one LLM batch judge | Yes before `report_query_outcome` / `add_knowledge` spam |
| **Recall timeout** | Default 5s; on timeout skip injection (never block turn) | **Mandatory** for overview enrichment |
| **Char budgets** | Per-memory + total recall caps | Align with TOON/RTK budgets |
| **Tools guide injection** | Cap active search tools (e.g. ≤3/turn) + drill-down hints | Skill/overview footer: prefer `search_knowledge` then `session_recall` |
| **RRF merge** | `score = 1/(60+rank)` across FTS + vector lists | Shared helper for agent-memory search |
| **HostAdapter** | Core never imports OpenClaw/Hermes — adapters only | Keep MCP as LeanKG’s host boundary |
| **Reclaim** | Retention days ≥3; delete orphan refs / stale MMD / prune registry | Session `refs/` GC (`US-SM-07`) |

### 4. White-box debuggability

Artifacts live as readable files under the plugin data dir (persona, scenes, MMD, refs). Debugging is a walk: Persona → Scenario → Atom → Conversation / `refs/<node_id>.md` — not “stare at vector scores.”

LeanKG already has white-box code artifacts (`GRAPH_REPORT.md`, cluster `SKILL.md`, `LESSONS.md`). Gap is **session** white-box + a **single heat-ranked index** for agent memory.

---

## LeanKG today (relevant surfaces)

| Concern | Existing surface |
|---------|------------------|
| Session start overview | `get_overview_context` (prefer over bare `load_layer(L0)`) |
| Progressive code layers | `load_layer` L0–L3 (**code** identity/facts/cluster/search) |
| Agent persona / diary | `agent_focus`, `agent_diary_write`, `agent_diary_read` |
| Reflect loop | `report_query_outcome` → `.leankg/reflections/LESSONS.md` |
| Free-form + domain memory | `add_knowledge` / `search_knowledge`, ontology concepts & workflows |
| Token compression | `ctx_read`, RTK/TOON, `orchestrate` cache |
| Cluster skills | `get_cluster_skill` |
| Self-improve gap | **US-GE-05** — outcome → durable artifact → next plan (Partial / PENDING) |

**Vocabulary warning:** LeanKG’s L0–L3 **names collide** with Tencent’s but mean different things. In docs/skills, say “code-context layers” vs “session-memory pyramid.”

---

## Recommended improvements (priority) → PRD `US-SM-*`

### P2 Must — Session tool-output offload + `node_id` (`US-SM-01`)

1. After N MCP tool calls (or context-ratio threshold), write full payloads under `.leankg/sessions/<session_id>/refs/<node_id>.md`.
2. Maintain compact canvas (Mermaid or graph JSON) listing steps + `node_id`s.
3. Inject only the canvas (+ last few turns); recover via `ctx_read` / `session_recall(node_id=…)`.

**AC sketch:** auto-trigger; lossless recovery; ≥30% token reduction on multi-tool fixture.

### P2 Must — Auto-recall at session start (`US-SM-02` / closes `US-GE-05`)

1. Enrich `get_overview_context` (or sibling `get_memory_context`) with top-K ranked lessons + recent diary tags — **opt-in** until measured.
2. Ranked lessons index (not only append-only Markdown); dedup before write.
3. Recall timeout + char budgets; never block MCP.

### P2 Should — Provenance + typed kinds (`US-SM-03`)

Require/encourage `source_ids` / `node_id` / tool-call refs on `add_knowledge`, `add_ontology_*`, `agent_diary_write`, `report_query_outcome`. Prefer typed kinds aligned to `persona|episodic|instruction` (or LeanKG synonyms: preference / decision / standing_rule).

### P2 Should — Hybrid RRF over agent memory (`US-SM-04`)

Extend `search_knowledge` or add `search_agent_memory`: RRF-merge knowledge + diary + LESSONS + dynamic ontology. Guards: `maxResults`, score threshold, char budgets, timeout.

### P2 Could — Heat-ranked `MEMORY_INDEX.md` (`US-SM-05`)

White-box index of hot lessons / diary tags / ontology concepts by hit count. Absolute paths for `ctx_read`.

### P2 Could — Promote successful traces → workflows (`US-SM-06`)

Repeated successful multi-tool sequences → propose `add_ontology_workflow` with `code_refs`. YAML remains SoT.

### P3 — Retention / GC (`US-SM-07`)

Retention days + pinned/high-heat exceptions for diary, lessons index, session `refs/`. Reclaim orphan offload files.

---

## Mapping: Tencent pattern → LeanKG artifact

| Tencent | LeanKG analogue (proposed or existing) |
|---------|----------------------------------------|
| Mermaid task canvas | `.leankg/sessions/<id>/canvas.mmd` (or graph JSON) |
| `refs/*.md` | `.leankg/sessions/<id>/refs/<node_id>.md` |
| L1 atoms (`persona`/`episodic`/`instruction`) | Knowledge entries + diary notes with typed kind + provenance |
| L2 scenes | Ontology workflow steps **or** scene Markdown under `.leankg/sessions/` |
| L3 persona | `.leankg/agents/<name>.json` + distilled diary summary (not chat persona) |
| Scene heat | Hit counts from `report_query_outcome` / search |
| `tdai_memory_search` | Hybrid `search_agent_memory` / extended `search_knowledge` |
| Auto-recall prepend | Enrich `get_overview_context` |
| RRF hybrid | Shared RRF helper (`k=60`) over FTS + HNSW for memory stores |
| Warm-up pipeline | Session offload / lesson flush schedule |
| Offload reclaim | `US-SM-07` GC for `.leankg/sessions/` |

---

## What LeanKG already wins (do not regress)

- Typed code graph at monorepo / mega-graph scale (CozoDB, multi-project Docker).
- Surgical MCP prefer-order: `concept_search` → `semantic_search` → `search_code` → connection verbs.
- Ontology + procedural workflows as durable **team** knowledge (not only personal chat memory).
- Measured agent economics (TOON/RTK, budgeted tools) as company platform vs personal skill.

Tencent’s strength is **session continuity for general agents**. LeanKG’s strength is **shared structural memory for codebases**. The win is to add session continuity *around* the graph, not instead of it.

---

## Suggested next steps

1. PRD slice landed: §1.3 / §3.28 / §5.32 + tracker `US-SM-*` / `FR-SM-*` / `REL-075` (this revision).
2. Wire `US-GE-05` ACs to auto-recall + ranked lessons index (`US-SM-02`).
3. Implement order after P1 Wave 4: `US-SM-01` → `US-SM-02` → `US-SM-03`/`04` → `05`/`06` → `07`.

## See also

- [`docs/prd.md`](../prd.md) §1.1 token economics, §1.3 TencentDB, §1.2 / US-GE-05, §3.28 / §5.32  
- [`docs/analysis/graph-engineering-roadmap-vs-leankg-2026-07-21.md`](graph-engineering-roadmap-vs-leankg-2026-07-21.md)  
- [`docs/analysis/graphify-vs-leankg-2026-07-20.md`](graphify-vs-leankg-2026-07-20.md)  
- Upstream README + `src/core/{hooks,record,store,prompts}` + `src/offload/`  
