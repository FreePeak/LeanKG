# Agent LeanKG MCP Misuse — Root Causes & Fix Proposals

**Date:** 2026-07-31  
**Workspace under review:** BE monorepo Cursor sessions (Cursor profile `Users-linh-doan-work-be`)  
**Method:** Transcript forensics (tool-call sequences; tool *results* are not stored in `.jsonl`)  
**Related:** `docs/analysis/fix-mcp-sse-discovery-preserve-project-2026-07-30.md`, skill `using-leankg`, rules `leankg-graph-first` / `skill-auto-invoke`

---

## Executive summary

Two Cursor agent sessions reviewed the same AUTH-VULN-06 security report. LeanKG HTTP on `:9699` was healthy and the `leankg-be` MCP entry was configured. Agents still navigated primarily with host `Grep` / `Read`.

| Session | Prompt LeanKG hint | MCP calls | Grep | Verdict |
|---------|--------------------|----------:|-----:|---------|
| `55c86289-…` | none | 2 | 23 | Token LeanKG, then abandon |
| `d7cdc895-…` | “use leankg to query first” | 8 + `GetMcpTools` | 26 | More attempts, many **wrong tool args** |

**This is not a down-server problem.** Root causes are agent workflow (race Grep with health), incomplete prefer-order, missing skill invocation, and **schema-incorrect `CallMcpTool` arguments** that make LeanKG look useless and drive Grep fallback.

---

## Evidence sources

| Session ID | Transcript |
|------------|------------|
| `55c86289-1709-49c1-9c88-99baa35c451d` | `~/.cursor/projects/Users-linh-doan-work-be/agent-transcripts/55c86289-…/55c86289-….jsonl` |
| `d7cdc895-3102-4cbb-ae3a-6e706cea2542` | `~/.cursor/projects/Users-linh-doan-work-be/agent-transcripts/d7cdc895-…/d7cdc895-….jsonl` |

Prompt delta in session 2 (end of user query):

> Review this security issue in my api **use leankg to query first**

Infrastructure checks (analyst session):

- `curl http://localhost:9699/health` → OK  
- `GetMcpTools(pattern="leankg*")` → `leankg-be` / `leankg-freepeak` ready  
- `~/.cursor/mcp.json` `leankg-be` URL includes container `?project=` (not a Mac host path)

---

## Session A — no LeanKG hint (`55c86289`)

### Tool mix

| Tool | Count |
|------|------:|
| `CallMcpTool` | 2 |
| `GetMcpTools` | 0 |
| Grep | 23 |
| Read | 26 |
| Glob | 9 |

### MCP sequence

1. `mcp_status` (`user-leankg-be`, args `{}`)  
2. `semantic_search` (NL query about skip list / login)

### Behavior

1. **Turn 1:** health check **in parallel with** Grep + Glob on host paths named in the ticket.  
2. **Turn 2:** `mcp_status` while already `Read`ing ticket files.  
3. **Turn 3+:** Grep/Read only — no further LeanKG.

### What was *not* wrong

- HTTP down  
- Using freepeak / default `/workspace` server id as primary  
- Passing Mac host path as `project=` on BE-bound calls

---

## Session B — “use leankg to query first” (`d7cdc895`)

### Tool mix

| Tool | Count |
|------|------:|
| `GetMcpTools` | 1 (`pattern=leankg-be`) |
| `CallMcpTool` | 8 |
| Grep | 26 |
| Read | 18 |
| Glob | 3 |

### MCP sequence (ordered)

1. `GetMcpTools(pattern="leankg-be")`  
2. `mcp_status`  
3. `semantic_search`  
4. `search_code` (`SkipCheckMerchantPermissionEndpoints`)  
5. `get_dependents` — **bad args**  
6. `get_dependents` — **bad args**  
7. `find_function` — **`function_name` instead of `name`**  
8. `find_function` — `name=` OK  
9. `shortest_path` — **`from`/`to` instead of `source`/`target`**

### Behavior

- Prompt improved **intent** (discover server, more MCP tools).  
- **Turn 1 still raced** health + `GetMcpTools` + Grep.  
- Never called `get_overview_context`, `concept_search`, or `get_context`.  
- Never opened `using-leankg` skill.  
- After early MCP attempts, Grep/Read again dominated.

### Schema mismatches (confirmed against live `leankg-be` tool schemas)

| Call | Arguments used | Schema requires |
|------|----------------|-----------------|
| `get_dependents` | `symbol=…` only | **`file`** (required); no `symbol` property |
| `get_dependents` | `file` + `symbol` | **`file` only** |
| `find_function` | `function_name=…` | **`name`** (required) |
| `shortest_path` | `from`, `to`, `max_hops` | **`source`**, **`target`** (`max_hops` OK) |

Wrong-arg calls fail or return errors → agent treats LeanKG as empty/unhelpful → Grep.

---

## Root causes (ranked)

### RC1 — Parallel Grep with health gate (primary workflow bug)

**Rule:** health → LeanKG only → Grep/Read only if empty/error.  

**Observed:** both sessions run Grep in the **same assistant turn** as `curl :9699/health`. LeanKG never owns discovery.

**Why it happens:** agents parallelize “independent” tools; ticket already names files, so Grep looks free.

### RC2 — Incorrect MCP argument names (primary technical bug in session B)

Agent invents familiar names (`function_name`, `from`/`to`, `symbol`) instead of calling `GetMcpTools` **per tool** (or caching schema) before `CallMcpTool`.

`GetMcpTools(pattern=…)` only lists tools; it does **not** substitute reading each tool’s `inputSchema` before invoke (Cursor guidance: fetch schema before call).

### RC3 — Prefer-order truncated

Mandatory discover chain for BE:

`mcp_status` → `get_overview_context` → `concept_search` → `semantic_search` → `search_code` / `find_function` → `get_context` / impact / deps.

Both sessions skipped overview, concept search, and **`get_context`** (the right follow-up after a hit). Session A stopped after one `semantic_search`.

### RC4 — Skill auto-invoke skipped

`using-leankg` exists and maps to “where is / find logic.” Neither session read it. Only session A later read `review-security` for the write-up.

Rules (`skill-auto-invoke`, `leankg-graph-first`) are present but **soft** — a one-line user hint does not harden them.

### RC5 — Ticket path short-circuit

Report cites exact paths (`constants.go`, `server.go:760`). Agents treat that as “open these files,” which competes with graph-first even when the user says use LeanKG.

### RC6 — Soft enforcement / no session latch

Nothing in the agent loop:

- Blocks Grep until `mcp_status` succeeds and graph looks like BE (large Go graph).  
- Requires `GetMcpTools(server, toolName)` before each new tool.  
- Records “LeanKG first satisfied” so later turns do not silently fall back.

### RC7 — Prompt hint insufficient (secondary)

“use leankg to query first” raised MCP volume and added `GetMcpTools`, but did **not** stop RC1 or RC2. Hint alone is not a fix.

### Non-causes (ruled out for these transcripts)

| Hypothesis | Status |
|------------|--------|
| LeanKG HTTP down | Ruled out (health checked; MCP called) |
| Wrong product server (freepeak for BE work) | Ruled out (`user-leankg-be`) |
| Mac host `project=` on BE tools | Ruled out (omit `project` on pre-bound server) |
| Missing MCP config | Ruled out (`leankg-be` ready, container `?project=`) |

**Note:** A separate class of bugs (SSE discovery stripping `?project=`) can still bind the wrong RocksDB project; that was not proven from these transcripts (no tool results). Always verify `mcp_status` shows a large BE/Go graph, not a small Rust self-repo.

---

## Fix proposals

Prioritize by leverage. Mix **agent-contract** fixes (fast) with **product** fixes (durable).

### P0 — Harden agent contract (skills / rules / hooks)

| Action | Detail |
|--------|--------|
| **Rewrite `using-leankg` gate** | Explicit BAN: do not call Grep/Glob/Read in the same turn as health. Sequence must be serial: health → `GetMcpTools` → `mcp_status` → discover tools → only then editor tools if empty. |
| **Mandatory schema fetch** | Before first use of each LeanKG tool name in a session: `GetMcpTools(server, toolName)` and copy property names from `inputSchema`. Add a cheat-sheet table in the skill for the top 10 tools (`name` not `function_name`; `source`/`target` not `from`/`to`; `get_dependents` needs `file`). |
| **Session latch checklist** | Skill requires agents to emit (or mentally hold) after status: graph size / sample path kind (Go BE vs Rust self-repo). Wrong graph → stop and fix MCP URL, do not Grep. |
| **Cursor hook (optional)** | Pre-tool hook: if workspace has `.leankg/` and health OK, refuse or warn on Grep until `mcp_status` ran this session. |
| **Prompt snippet for humans** | Short pasteable block stronger than one sentence — see appendix. |

### P1 — LeanKG product: fail loud + aliases

| Action | Detail |
|--------|--------|
| **Accept common aliases** | Map `function_name` → `name`; `from`/`to` → `source`/`target`; optional `symbol` on dependents → resolve via `find_function` then file. Reduces agent foot-guns without teaching every model. |
| **Structured arg errors** | On unknown/missing required fields, return JSON: `{"error":"invalid_args","expected":[…],"got":[…],"hint":"GetMcpTools…"}` instead of opaque failure. |
| **Tool descriptions** | Put required arg names in the first line of each tool description (models overweight description text). |
| **`orchestrate` / one-shot review tool** | For “review this vuln citing file X”, a single tool that does status + semantic + context for seeds reduces multi-call schema drift. |

### P2 — Prefer-order automation

| Action | Detail |
|--------|--------|
| **`kg_agent_bootstrap`** (new) | One call: health-equivalent status + overview + optional query seed. Agents that only remember one tool still get graph-first context. |
| **Empty-result envelope** | When `semantic_search` / `search_code` return empty, include `next_steps: ["concept_search","get_overview_context","do_not_grep_yet_unless…"]`. |

### P3 — Eval / regression

| Action | Detail |
|--------|--------|
| **Transcript harness** | Script over `agent-transcripts/*.jsonl`: fail if Grep appears before first successful LeanKG discover tool when health was OK; fail if CallMcpTool args ∉ schema. |
| **Golden prompt suite** | Same AUTH-VULN-style prompt with/without hint; assert MCP ≥ N and Grep-before-MCP = 0. |
| **CI for skill text** | Ensure `using-leankg` and BE rules stay aligned with live schemas (generated arg table from `tools/list`). |

---

## Recommended rollout (practical order)

1. **This week (P0):** Update `using-leankg` + BE `leankg-graph-first` with serial gate, schema cheat-sheet, and “no Grep same turn as health.” Sync Cursor user skill + any repo copy.  
2. **Same week (P1 aliases):** In LeanKG MCP handlers, accept alias keys for the four foot-guns seen in session B; add tests.  
3. **Next (P1 errors + P2 bootstrap):** Structured invalid-arg errors; optional `kg_agent_bootstrap`.  
4. **Ongoing (P3):** Transcript lint script on local agent logs; optional CI schema dump vs skill table.

---

## Success criteria

A future AUTH-VULN-style BE session should show:

1. Turn 1: health only (or health + `GetMcpTools` / `mcp_status`) — **no Grep/Read**.  
2. `mcp_status` confirms BE-scale graph before search.  
3. Discover tools use schema-correct args (`name`, `source`/`target`, `file` for dependents).  
4. At least one `get_context` (or equivalent) before bulk file reads.  
5. Grep/Read only after LeanKG empty/error, or for non-indexed artifacts (charts, raw curl to stage).  
6. User one-liner “use leankg” optional — default rules already enforce the path.

---

## Appendix A — Stronger human prompt (pasteable)

```text
Use LeanKG MCP first for all code navigation in this BE workspace.
1) curl :9699/health — if fail, then Grep/Read only.
2) GetMcpTools(pattern="leankg-be"); CallMcpTool server from that result.
3) mcp_status — confirm large Go/BE graph (not Rust self-repo). Do not pass Mac host project=.
4) GetMcpTools(server, toolName) before each new tool; use exact inputSchema property names.
5) Prefer: concept_search → semantic_search → search_code/find_function → get_context.
6) Do NOT Grep/Glob/Read in the same turn as health or before mcp_status + one discover call.
```

## Appendix B — Top tool arg cheat-sheet (session B failures)

| Tool | Required / common args |
|------|------------------------|
| `mcp_status` | `{}` (omit `project` on pre-bound BE server) |
| `semantic_search` | `query`, optional `limit` |
| `search_code` | `query` |
| `find_function` | **`name`** (not `function_name`), optional `file` |
| `get_dependents` | **`file`** (not `symbol`) |
| `get_context` | `file` and/or symbol fields per schema |
| `shortest_path` | **`source`**, **`target`**, optional `max_hops` |

## Appendix C — Comparison matrix

| Check | Session A | Session B |
|-------|-----------|-----------|
| Health checked | yes | yes |
| Grep same turn as health | yes | yes |
| `GetMcpTools` discover | no | yes |
| `mcp_status` | yes | yes |
| Prefer-order depth | shallow | medium |
| Schema-correct connection tools | n/a | mostly **no** |
| `get_context` | no | no |
| `using-leankg` read | no | no |
| Grep still primary | yes | yes |

---

*Last updated: 2026-07-31*
