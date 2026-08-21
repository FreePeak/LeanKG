# LeanKG PRD — Enterprise & Startup Edition

**Version:** 1.0 · **Date:** 2026-08-22 · **Status:** Approved for execution
**Roadmap alignment:** [`roadmap-2027.md`](roadmap-2027.md) · **Progress:** [`roadmap-tracker.md`](roadmap-tracker.md)

---

## 1. Product Vision

LeanKG gives engineering teams a **deterministic, auditable, self-hostable knowledge graph of their codebase**, served natively to every AI coding agent via MCP. Unlike embedding-only context engines, every answer is traceable to extracted code structure with provenance labels — making AI-assisted development trustworthy enough for regulated enterprises and fast enough for startups.

### 1.1 Target segments
| Segment | Profile | Willingness to pay | Key needs |
|---|---|---|---|
| Startup (PLG) | 5–50 devs, agent-heavy workflow | $25/dev/mo Team tier | Zero-config setup, speed, multi-repo, flat pricing |
| Mid-market | 50–500 devs, procurement starts | $15–25K/yr self-hosted | SSO, RBAC, audit logs, VPC deploy |
| Enterprise/regulated | 500+ devs, compliance-bound | Custom ($50K+) | Air-gap, residency, SOC2, SIEM export |

### 1.2 Personas
- **P1 Platform/DevEx lead (buyer):** owns tooling budget; needs governance + metrics.
- **P2 Staff engineer (champion):** wants deterministic impact analysis; hates black-box retrieval.
- **P3 Security/compliance reviewer (gatekeeper):** blocks deals without audit logs, SSO, self-host.

---

## 2. Requirements

Priority: P0 = launch-blocking for segment · P1 = competitive parity · P2 = differentiator.

### FR-ENT-* — Enterprise requirements (Q4'26–Q1'27)

| ID | Requirement | Priority | Acceptance criteria |
|----|-------------|----------|---------------------|
| ENT-1 | **Audit log foundation**: append-only ledger of every MCP/REST call (actor, agent-client, tool, project, args-hash, result-status, ts) | P0 | JSON-lines export; queryable by admin; <2ms write overhead; tamper-evident hash chain |
| ENT-2 | **RBAC v1**: roles admin/editor/reader; scoped per project/collection; enforced at MCP tool layer | P0 | Reader cannot invoke mutating tools (403); role changes take effect ≤60s |
| ENT-3 | **SSO OIDC**: Google WS, Okta, Entra ID; JWT validation on remote MCP + REST | P0 (Q1'27) | Login via IdP in ≤3 clicks; group→role mapping |
| ENT-4 | **SAML 2.0** + SCIM provisioning/deprovisioning | P1 (Q1'27) | Deprovisioned user loses access ≤5 min |
| ENT-5 | **Self-hosted deployment kit**: Helm chart, compose prod profile, upgrade guide | P0 (Q1'27) | Fresh cluster → serving in ≤30 min |
| ENT-6 | **Data residency pinning**: EU/US storage regions for hosted tier | P1 | Region choice at workspace creation; no cross-region replication |
| ENT-7 | **SIEM export**: webhook + syslog drain of audit stream | P1 | Splunk/Datadog-compatible field schema |
| ENT-8 | **Air-gap install**: offline license file, signed updates, zero outbound telemetry | P2 (Q2'27) | Runs fully offline ≥90 days; license expiry grace 14d |
| ENT-9 | **Provenance labels** on all relationship/graph responses (EXTRACTED/INFERRED/AMBIGUOUS) | P0 | Every edge in tool output carries confidence_label |
| ENT-10 | **SOC 2 controls automation**: evidence collection, access reviews | P1 (Q1'27 runway) | Type I readiness checklist complete |

### FR-PLG-* — Startup/self-serve requirements (Q4'26)

| ID | Requirement | Priority | Acceptance criteria |
|----|-------------|----------|---------------------|
| PLG-1 | **One-command connect**: `leankg connect claude-code\|cursor\|codex\|gemini` writes correct client config | P0 | Works for all 4 clients; idempotent; `--remove` flag |
| PLG-2 | **Official MCP registry listing** with verified publisher badge | P0 | Listed; install-from-registry path documented |
| PLG-3 | **Team server mode**: shared Postgres graph, N users, OAuth 2.1 remote MCP (Streamable HTTP) | P0 | 10 concurrent agents; per-user identity in audit log |
| PLG-4 | **Flat pricing enforcement**: seat-based licensing service (license key gen/validation) | P0 | Seat count enforced; over-seat → read-only grace |
| PLG-5 | **Stable tool contract**: semver'd tool registry; deprecation policy (2 minors notice) | P0 | Contract doc published; CI fails on unregistered breaking change |
| PLG-6 | **npm/crate version sync**: release automation publishes matching versions | P0 | npm version == crate version on every release |
| PLG-7 | **Quickstart < 5 min**: init+index+first-query measured on 10k-element repo | P0 | Median < 5 min in CI-timed smoke test |
| PLG-8 | **Usage dashboard**: tokens saved, queries/day, top tools (from existing context_metrics) | P1 | Per-user + per-project views; CSV export |

### FR-CORE-* — Core engine quality (Q3'26, prerequisite)

| ID | Requirement | Priority | Acceptance criteria |
|----|-------------|----------|---------------------|
| CORE-1 | **Complete Datalog removal**: SQL-first seam; delete translate.rs/fake.rs/mutability.rs | P0 | `rg "cozo::" src/` = 0 Datalog strings; parity harness green; fake.rs deleted |
| CORE-2 | **Tool consolidation**: 79→~70 tools; matrix test green | P0 | redundant_tools_matrix passes; contract doc updated |
| CORE-3 | **qualified_name uniqueness**: dedup strategy + UNIQUE constraint | P0 | Embed succeeds on real 371k-fn repo; collision report tool |
| CORE-4 | **Re-index reliability**: fix EEXIST on `leankg index` re-run | P0 | index→index→index idempotent in integration test |
| CORE-5 | **CI promotes PG integration suite** | P0 | tests/pg_* run as CI gate with Postgres service |
| CORE-6 | **Performance floor**: p95<150ms top-10 tools @100k elements | P1 | benchmark-unified report in repo; regression gate ±20% |

### Non-functional requirements
- **NFR-1** Zero telemetry by default; opt-in analytics only.
- **NFR-2** All secrets via env/config; no secrets in DB or logs.
- **NFR-3** Backward-compatible DB migrations; `leankg migrate` tested across ≥2 versions.
- **NFR-4** Single static binary remains the local-first default (stdio MCP).

---

## 3. Pricing & Packaging (locked)

| Tier | Price | Includes |
|------|-------|----------|
| OSS / Local | Free forever | stdio MCP, single-user, full graph engine, local embeddings |
| Team | $25/dev/mo flat | Shared team server, OAuth remote MCP, RBAC v1, usage dashboard, email support |
| Enterprise | $15–25K/yr | Self-hosted kit, SSO/SAML/SCIM, audit+SIEM, residency, 8×5 support, SLA |
| Air-gap | Custom | ENT-8, signed offline updates, CMMC-aligned checklist, named engineer |

Guarantee marketed: **"No token toll. No surprise bills."** (anti-metering positioning vs Augment/Greptile.)

---

## 4. Launch Plan

- **Milestone M1 (end Q3'26):** CORE-1..6 done → "Postgres-native, Datalog-free v0.28".
- **Milestone M2 (end Q4'26):** PLG-1..6 + ENT-1/2 → Team beta with 5 design partners.
- **Milestone M3 (end Q1'27):** ENT-3..7 → Enterprise pilot with 3 paid evaluations.
- **Milestone M4 (end Q2'27):** H-track platform features + air-gap tier → GA.

Each milestone exits only when tracker W-items are ✅ and acceptance criteria have live-test evidence links.

---

## 5. Open Questions (decided autonomously per mandate)
- License: recommend dual-scan in Q4'26 (Apache-2.0 core vs AGPL core); decision recorded in roadmap-tracker before Team launch. Default recommendation: **Apache-2.0 core + trademark + CLA**, matching Supabase/Neon playbook.
- Hosted cloud infra: defer to M2; start with single-region (US) + EU at M3.
