# Graphify vs LeanKG — Deep Dive (2026-07-20)

**Sources:** local Graphify repo (v0.9.20) under freepeak polyrepo; LeanKG `docs/prd.md` v3.7.8; ui-v2; prior [`graphify-comparison-2026-07-13.md`](graphify-comparison-2026-07-13.md).  
**Purpose:** Manager-facing competitive case + ordered LeanKG improvement backlog (bound to PRD IDs).

---

## Verdict

**Graphify** wins as a **personal skill + artifact factory** (install matrix, `graph.html`, `GRAPH_REPORT.md`, honest edges).  
**LeanKG** wins as a **company platform**: shared RocksDB/MCP, mega-graph safety, ~85 tools, TOON economics, microservice/ops/traceability, live ui-v2.

**Recommendation:** Standardize on LeanKG for monorepos and team AI cost control; close Graphify packaging gaps in the **P1 company-adoption queue** (§1.1 of PRD). Do **not** chase multimodal or NetworkX.

---

## Company ROI (why LeanKG is worth more)

| Lever | LeanKG | Graphify | Company impact |
|-------|--------|----------|----------------|
| Token / tool-call reduction | ≥61% / ≥84% vs grep/cat (A/B gates) | Budgeted NL subgraph | Savings × developers × sessions/day |
| Shared index | Docker multi-project RocksDB | Per-clone `graph.json` | One index, many agents |
| Mega-graph | Keyed/frontier paths, mem budgets | 5k HTML cap, in-memory NetworkX | Real monorepos stay queryable |
| Ops / risk | Impact severity, incidents, env, service_calls | PR community impact | Change-risk conversations managers need |
| Depth | ~85 MCP tools | ~9–10 | Fewer reinvented agent workflows |

**Cost lever #1 to ship:** always-on graph-first install (`US-GF-17`) — without it, agents still grep and LeanKG’s economics never show up on the bill.

---

## Jul-13 corrections (MCP)

Many “Missing” rows in the Jul-13 matrix are **DONE** in LeanKG MCP/CLI: `shortest_path`, `explain_node`, `query_graph`, `get_god_nodes`, `get_graph_report`, PR impact, reflect, portable snapshot. Remaining gaps are mostly **packaging + UI**.

---

## UI compare (short)

| | Graphify | LeanKG ui-v2 |
|--|----------|--------------|
| Form | Static vis.js HTML | Live Sigma React (Force/Tree/Circles) |
| Share | Excellent (`graph.html`) | Weak → close with `US-GF-13` |
| Large repo | 5k cutoff | Mega-skip + path expand |
| Edge honesty in UI | Yes | No → `FR-GF-09` |
| Query | NL `query` | Raw Cozo FAB → NL via `US-UI2-06` |

---

## Ordered backlog (PRD Focus P1 waves → P2)

> **Updated 2026-07-21 (v3.7.12):** Wave **1a** MCP surface hard-delete + skills/rules/setup sync inserted before three-verb. Tracker SoT: [`prd-task-tracker.md`](../prd-task-tracker.md).

| Wave | IDs | Intent |
|-----:|-----|--------|
| **0a** | `US-COST-01` / `FR-COST-01` / `REL-058` | Manager ROI brief + README link |
| **0b** | `US-UI2-07` / `FR-UI2-09` / `REL-057` | ui-v2 cutover evidence closeout |
| **1a** | `US-SURF-06..07` / `FR-SURF-07..11` / `REL-062` | Hard-delete soft-deprecated tools + sync agent surfaces |
| **1b** | `US-GF-14` / `FR-GF-22` | Three-verb narrative |
| **1c** | `US-GF-17` / `FR-GF-24` | Always-on install/hooks (**cost lever #1**) |
| **2a** | `US-GF-04` / `FR-GF-07..09` / `REL-043` | Honest edges |
| **2b** | `US-GF-06` / `FR-GF-13` | Auto GRAPH_REPORT.md |
| **2c** | `US-GF-13` / `FR-GF-21` | HTML export |
| **3** | `US-UI2-06` / `FR-UI2-08` | NL Query FAB |
| **4** | `US-MG-02` / `FR-MG-03` | Single-repo expand |

**P2:** `US-GF-15`, `US-GF-16`, `US-UI2-08`, `US-UI2-09`, `FR-GF-16`, `FR-GF-23`, `FR-UI2-10..11`, demoted CBM/lang/REST leftovers

**P3 / Won't interrupt:** Track E 3D (`REL-041`); `FR-SURF-06` doc merge; multimodal; NetworkX primary; 36-lang race; vis.js-only UI.

---

## Tracker

All IDs live in [`docs/prd-task-tracker.md`](../prd-task-tracker.md) / [`.json`](../prd-task-tracker.json).  
PRD narrative: [`docs/prd.md`](../prd.md) §1.1, §3.10, §3.17, §5.9, §5.19, §5.20.
