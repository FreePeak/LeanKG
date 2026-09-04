# LeanKG 1-Year Roadmap: Q3 2026 → Q2 2027

**Version:** 1.0 · **Date:** 2026-08-22 · **Owner:** FreePeak
**Positioning:** The deterministic, self-hostable, MCP-native code knowledge graph.
**Companion:** [`roadmap-tracker.md`](roadmap-tracker.md) (progress SoT) · [`prd-enterprise.md`](prd-enterprise.md) (requirements)

---

## 1. Market Context (research summary, Aug 2026)

### 1.1 Why now
- Knowledge-graph market ≈ $2B (2026) growing 20–33%/yr; GraphRAG-for-enterprise-AI is the cited driver.
- Engineering orgs spend $400–600K/yr on AI coding tools; context quality is the differentiator (Augment's Context Engine lifted agent output +70–80%; Meta's pre-computed context cut tool calls −40%).
- MCP won the protocol war: Linux Foundation governance, 97M+ monthly SDK downloads, every major agent/IDE speaks it. Enterprise MCP layer (registries, gateways, OAuth) is forming now — early movers define the standard.
- Trust crisis favors determinism: METR found AI coding slowed complex tasks 19%; EU AI Act obligations apply Aug 2026; only 14% of orgs fully approve deployed agents. Deterministic, provenance-labeled graph answers are the credible response.

### 1.2 Competitive landscape
| Competitor | Approach | Gap LeanKG exploits |
|---|---|---|
| Cursor ($29B val) | Merkle+embeddings, IDE-bound | No MCP surface, no self-host, embeddings fail call-chain queries |
| Augment Code ($977M val) | Cloud embedding engine, MCP GA | Cloud-only (+40% token toll), closed/black-box |
| Sourcegraph | SCIP platform, $16K/yr floor | Killed free tier; heavy platform; goodwill spent |
| GitNexus (45k★) | Local TS graph, PolyForm Noncommercial | Non-commercial license blocks adoption; no semantics depth |
| Tabnine CE | On-prem "context engine" | Closed; anti-KG messaging leaves KG category open |
| Stack Graphs / Bloop | Archived by GitHub/maintainers | OSS precise-code-nav slot is vacant |
| Zep/Graphiti, Mem0, Cognee | Agent memory KGs | Chat-domain, not code-structural |

**Empty quadrant we own:** local-first × deterministic graph × hybrid local semantics × permissive license × MCP-native.

### 1.3 Monetization model (decided)
- **Core:** open source (permissive core; AGPL option documented for cloud-resale protection — final license call in Q4'26 before Team launch).
- **Team cloud:** $25/dev/mo flat (undercuts Sourcegraph floor; matches Greptile/CodeRabbit band; "no token toll" vs Augment's +40%).
- **Enterprise self-hosted:** $15–25K/yr (SSO/RBAC/audit/residency). Air-gap tier custom pricing.
- **Anti-pattern avoided:** usage-based metering (category backlash: Greptile/Macroscope protests).

---

## 2. Quarterly Themes

### Q3 2026 — "Solid Foundation" (engineering credibility)
**Theme:** finish the Postgres era cleanly; make the project trustworthy to adopt.
- **E1. Complete legacy-engine/Datalog removal** (waves per the Datalog-removal SQL migration plan, see roadmap-tracker companion docs): SQL-first seam → convert 238 run_script sites → delete translate.rs (4.3k), fake.rs (1.4k), mutability.rs. Exit criteria: zero Datalog strings in src/, parity harness green, `run_raw_query` deprecated→removed or NL-only.
- **E2. Tool-surface discipline:** fix red matrix test; consolidation round 76→~70; publish stable-tool API contract (semver for tool names/schemas); auto-sync npm wrapper with crate version.
- **E3. Known-finding fixes:** qualified_name UNIQUE dedup strategy; `leankg index` EEXIST re-index bug.
- **E4. CI hardening:** promote PG integration suite into CI (Postgres service already configured); flake quarantine process.
- **E5. Docs truth sweep:** all generated docs/wiki say Postgres+pgvector; AGENTS.md updated; architecture.md refreshed.

### Q4 2026 — "Team Ready" (PLG wedge)
**Theme:** multi-user team server + distribution.
- **F1. Remote MCP with OAuth 2.1** (Streamable HTTP): hosted + self-hosted team mode; per-user identity on every tool call.
- **F2. RBAC v1:** roles (admin/editor/reader) scoped to projects/collections; tool-level permission bundles ("Virtual bundles" pattern from gateway vendors).
- **F3. Audit log foundation (ENT-1):** append-only who/which-agent/which-tool/which-project ledger with SIEM-friendly JSON export. *This is the enterprise procurement keystone — build the schema now even if UI comes later.*
- **F4. Official MCP registry listing + one-command setup** for Claude Code/Cursor/Codex/Gemini CLI (`leankg connect` writes client configs).
- **F5. License decision + CLA/trademark setup** (prereq for Team launch).
- **F6. Performance floor:** p95 < 150ms for top-10 tools at 100k-element repos; publish benchmark methodology.

### Q1 2027 — "Enterprise Grade" (procurement pass)
**Theme:** pass mid-market+ procurement without sales friction.
- **G1. SSO: SAML/OIDC** (Okta, Entra ID, Google WS) + SCIM provisioning.
- **G2. Audit log v2:** retention policies, tamper-evident hashing, admin UI.
- **G3. Deployment story:** Helm chart + Docker Compose prod profile + VPC/dedicated-tenant guide; data-residency pinning (EU/US).
- **G4. SOC 2 Type I → Type II runway** (controls automation; engage auditor when headcount triggers).
- **G5. Multi-repo org topology GA:** cross-repo service graphs, monorepo scale (1M+ elements) verified.
- **G6. Provenance everywhere:** EXTRACTED/INFERRED/AMBIGUOUS labels surfaced in every tool response schema (agent trust calibration = marketable differentiator).

### Q2 2027 — "Platform Play" (become substrate)
**Theme:** the code-intelligence layer other agents build on.
- **H1. Public query API + webhooks:** REST/gRPC read API with API keys, usage analytics; Backstage plugin.
- **H2. Embeddings marketplace:** pluggable model registry (local ONNX default; BYO remote models), per-model collections already shipped → productize.
- **H3. Bi-temporal code intelligence GA:** timeline queries, environment promotion (upcoming→staging→production) as first-class workflow; "what did this service look like when incident X happened".
- **H4. Agent-native features v2:** personas/diaries/SKILL.md generation promoted; reflection-driven ranking biasing.
- **H5. Channel partnerships:** MCP-gateway vendors (MintMCP, Kong, Traefik Hub) list LeanKG as governed server; DevEx/portal teams integration kit.
- **H6. Air-gap tier:** offline licensing, signed updates, zero-telemetry guarantee, NIST 800-171 alignment checklist.

---

## 3. North-Star Metrics & Targets

| Metric | Now (v0.26) | Q4'26 | Q2'27 |
|---|---|---|---|
| MCP tools (stable contract) | 79 unversioned | ~70 semver'd | ~70 + API v1 |
| Datalog remnants in src/ | 238 call sites | 0 | 0 |
| CI gates | lib-only | lib + PG integration | + e2e smoke |
| Index throughput | baseline | +30% (COPY path everywhere) | +50% |
| p95 top-10 tools @100k elems | unmeasured | <150ms | <75ms |
| GitHub stars / weekly installs | niche | 2k★ / 5k | 8k★ / 25k |
| Design partners (enterprise) | 0 | 5 | 20 |
| Paid tiers | none | Team beta | Team GA + Ent pilot |

## 4. Risks & Mitigations
| Risk | Mitigation |
|---|---|
| GitNexus velocity (45k★) | Win on license trust + Rust perf + semantics depth (ontology/traceability they lack) |
| SQL migration regression risk | Parity harness gates each wave; waves small; integration suite promoted to CI first (E4 before E1 waves) |
| License flip backlash | Decide once, early (Q4'26), communicate rationale; never retroactive |
| Metering backlash contagion | Flat published pricing forever; no per-query fees |
| Solo-maintainer bus factor | Subagent-parallel workflow documented; RFC process; good-first-issue pipeline |

## 5. Immediate Next Steps (this sprint)
See tracker §5: land refactor wave 1 (matrix test, doc-lies, dead code) → known-findings fixes → start SQL seam wave P0.
