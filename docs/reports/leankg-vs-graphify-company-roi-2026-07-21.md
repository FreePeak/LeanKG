# LeanKG vs Graphify — Company ROI Brief

**Date:** 2026-07-21  
**Audience:** Engineering managers choosing a knowledge-graph / AI-context stack  
**PRD:** [`docs/prd.md`](../prd.md) §1.1 · IDs `US-COST-01` / `FR-COST-01` / `REL-058`  
**Deep dive:** [`docs/analysis/graphify-vs-leankg-2026-07-20.md`](../analysis/graphify-vs-leankg-2026-07-20.md)

---

## Decision in one sentence

**Standardize on LeanKG for the company.** Graphify is a strong personal skill and shareable HTML map; LeanKG is the platform that cuts **AI agent spend** on real monorepos and gives teams **shared, ops-aware, multi-repo** context.

---

## Cost & efficiency (what shows up on the bill)

| Metric | LeanKG evidence | Why it matters at company scale |
|--------|-----------------|----------------------------------|
| Tokens vs grep/cat | ≥ **61%** reduction (A/B gate; measured −65% on vector-engine suite) | Every agent session × every developer |
| Tool calls vs grep/cat | ≥ **84%** reduction (measured −84.6%) | Fewer round-trips → lower latency and cost |
| Context delivery | &lt;100ms P95 surgical chunks + TOON compression | Agents stop dumping whole files |
| Shared index | One Docker RocksDB + MCP `:9699` for many mounts | Pay to index once; reuse across the team |
| Mega-graph | Keyed / frontier-local paths; mem budgets (6g MCP) | 100k–600k+ element monorepos stay queryable |

**Graphify** budgets NL subgraph queries well for individuals, but regenerating / committing `graph.json` and hitting a **5k-node HTML cap** does not replace a long-lived multi-project server for a large engineering org.

**Critical adoption lever:** Always-on graph-first install (`US-GF-17`) — if agents still grep first, none of the above savings appear. That is the top engineering follow-up after this brief.

---

## Capability comparison (company-relevant)

| Need | LeanKG | Graphify |
|------|--------|----------|
| Multi-repo team deploy | `LEANKG_PROJECT_DIRS` + Docker HTTP/REST | Shared HTTP over one `graph.json` |
| Agent tool depth | ~85 MCP tools (impact, ontology, services, Android, docs↔req, PR, reflect) | ~9–10 tools |
| Change risk | Severity-graded impact, incidents, env, service topology | PR community impact |
| Human explorer | Live ui-v2 (Force/Tree/Circles) over live index | Static `graph.html` |
| Edge honesty | In progress (P1: EXTRACTED/INFERRED labels) | Product-ready today |
| Install / force-use | Expanding (P1 always-on hooks) | Best-in-class 20+ platforms |

---

## What we are not buying

- Multimodal PDF/image/video graphs (Graphify strength; LeanKG out of scope)  
- Replacing our DB with NetworkX files  
- A 36-language grammar race  

We **are** closing Graphify’s packaging wins (honest edges, `GRAPH_REPORT.md`, HTML export, NL UI, always-on install) in the ordered P1 queue — see PRD §1.1.

---

## Recommended next actions

1. Approve LeanKG as the **company** AI context / KG standard.  
2. Fund the P1 adoption queue (install hooks + honest edges + report/HTML) so savings become automatic.  
3. Keep Graphify optional for individuals who want a one-shot HTML map of a small folder — not as the monorepo platform.

---

## References

- PRD §1.1 / §5.20 — `docs/prd.md`  
- Task order — `docs/prd-task-tracker.md` (Company adoption queue)  
- Full compare — `docs/analysis/graphify-vs-leankg-2026-07-20.md`  
- Token A/B — PRD Section 9 / vector-engine gate results  
