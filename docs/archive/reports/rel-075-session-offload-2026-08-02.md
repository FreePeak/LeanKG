# REL-075 — Session memory offload smoke report (US-SM-01 / FR-SM-01..03)

**Date:** 2026-08-02
**Tracker:** `REL-075` PARTIAL → DONE (US-SM-01 / FR-SM-01..03 shipped; US-SM-02 auto-recall is PR-21, not in scope)
**PRD:** §1.3 / §3.28 US-SM-01 / §5.32 FR-SM-01..03, REL-075
**Analysis:** [`docs/analysis/tencentdb-agent-memory-vs-leankg-2026-07-31.md`](../analysis/tencentdb-agent-memory-vs-leankg-2026-07-31.md)

## Summary

Session memory offload implemented: verbose MCP/tool payloads persist to
`.leankg/sessions/<id>/refs/<node_id>.md` (markdown per node, FR-SM-01), a
compact canvas (Mermaid + node index table, FR-SM-02) stays in context, and
`session_recall(node_id=…, session_id=…, project=…)` recovers the original
payload bit-for-bit (FR-SM-03). US-SM-01 acceptance met: offload trigger,
bit-for-bit recall, ≥30% token drop on the fixture.

## Code

| File | Change |
|------|--------|
| `src/session/mod.rs` | NEW — `SessionStore` (write/read refs + canvas), `Canvas`/`NodeEntry`, stable `offload-<NNN>` node_id scheme, `offload_step` (stateless, node_id derives from canvas on disk), sha256 front-matter fingerprint, 10 unit tests |
| `src/lib.rs` / `src/main.rs` | `pub mod session;` |
| `src/mcp/tools.rs` | ADD `session_recall` tool definition (thin) |
| `src/mcp/handler.rs` | ADD dispatch arm + ~25-line `session_recall` handler (thin: read ref, return payload) |
| `tests/mcp_tools_redundancy_tests.rs` | ADD `session_offload` module: registry assertion + offload→`session_recall` round trip via `ToolHandler` + missing-node error |
| `docs/mcp-tools.md` | Context Tools table row for `session_recall` |

Not in scope (PR-21): auto-recall into `get_overview_context`, lessons ranking, RRF.

## Gate outputs (worktree `prd/session-offload`)

```
cargo fmt --all -- --check        PASS
cargo clippy --all -- -D warnings PASS (0 warnings)
cargo test --lib                  PASS — 754 passed, 0 failed
cargo test session                PASS — 10 unit + 2 integration, 0 failed
cargo build --release             PASS (8m02s, 0.19.30)
```

## Acceptance evidence (US-SM-01)

### 1. Offload trigger + markdown per node

Fixture: 4 verbose `search_code` payloads (15 elements each, ~2.9 KB JSON each)
offloaded via `offload_step` (budget 2000 chars) → `sess-fixture`:

```
.leankg/sessions/sess-fixture/
├── canvas.md                     # Mermaid + node index (FR-SM-02)
└── refs/
    ├── offload-001.md
    ├── offload-002.md
    ├── offload-003.md
    └── offload-004.md
```

Ref file shape (FR-SM-01 stable scheme `offload-<NNN>`):

```markdown
# Ref: offload-001

- tool: search_code
- step: 1
- bytes: 3675
- sha256: <12-hex fingerprint>

```json
{ …full payload… }
```
```

Node_id validation: `../evil` rejected; `session_id` must be path-safe.

### 2. Bit-for-bit recall

- Unit: `write_ref` → `read_ref` returns `Value` equal to original (`recall_is_bit_for_bit`).
- MCP: `session_recall` via `ToolHandler::execute_tool` returns `payload` equal to
  original JSON (`offload_then_recall_via_mcp_round_trips`).
- Missing node → error `node_id offload-999 not found: …` (never empty success).

### 3. Token drop fixture ≥30%

`fixture_offloaded_context_drops_30_percent_tokens`: 4 offloaded payloads
(50/60/70/55 elements) vs accumulated compact JSON canvas.

- Inline tokens: 8951 (estimate: chars/4, exact chars 35807)
- Compact tokens: 315 (exact chars 1262)
- **Drop: 96.5%** ≥ 30% — PASS

(Live smoke measured 94.0% on a similar fixture shape; see below.)

## Live smoke (Docker MCP `:9699`)

Docker MCP at `localhost:9699` was healthy but serves the **previous release
binary** (89 tools listed, `session_recall` absent — tool count 89 vs 86 on the
new build; new-binary list excludes embedding-gated tools). Per campaign
instructions, smoke ran against the **new release binary** instead:

```
./target/release/leankg mcp-http --port 9876 --project /tmp/sm-sess-demo
tools/list  → session_recall present (86 tools)
```

Live calls (JSON-RPC over `POST /mcp`):

| Check | Result |
|-------|--------|
| ref body == original payload text | `REF_BODY_MATCHES_ORIGINAL: True` |
| `session_recall(offload-004)` returns full 15-hit payload | all 15 `qualified_name` unique hits present in response |
| response includes `bytes:` + `node_id` + `payload` | True |
| canvas token drop (4 refs vs canvas.md) | **94.0%** (inline 2956 tok → canvas 178 tok) ≥ 30% PASS |

Note: HTTP server wraps all tool results in TOON format
(`src/mcp/server.rs:2835` `use_toon = true`); the ref **file** stores the exact
JSON (`REF_BODY_MATCHES_ORIGINAL: True`), and TOON recall preserved every field
of the payload. Bit-for-bit guarantee lives at the ref-file seam.

## Commands to reproduce

```bash
cargo test --lib session::                 # unit: offload/recall/canvas/drop
cargo test session                         # + MCP integration
cargo test --test mcp_tools_redundancy_tests session_offload
# live smoke (after cargo build --release):
./target/release/leankg mcp-http --port 9876 --project <demo-project> &
# POST /mcp {method:"tools/call", params:{name:"session_recall",
#   arguments:{node_id:"offload-001", session_id:"<id>", project:"<dir>"}}}
```

## Follow-ups (PR-21)

- Auto-recall (`US-SM-02` / FR-SM-04..06): ranked lessons index, overview
  enrichment (opt-in), recall timeout — NOT implemented here.
- Retention/GC for `refs/` (`US-SM-07`).
