# Conversation mining (#175) live evidence — 2026-08-02

## Environment
- commit: 8c77b22b | binary: target/release/leankg 0.19.31 (local) | project: /tmp/leankg-live-fixture
- embeddings feature: yes

## Steps
1. `mine-conversations --format claude --project . --input tests/fixtures/conversations/claude_export.json`
2. Re-run same command (idempotency)
3. `mine-conversations --format claude --project . --input /tmp/leankg-conv/targeted.json` (conversation with `file::symbol` refs)

## Results
- Claude export → `Mined 3 items from 1 source(s) [decision, preference]: 3 elements, 0 relationships` — PASS: decisions + preferences mined.
- Re-run → same 3 items, no duplicates — PASS idempotent.
- Targeted export (decision referencing `src/backend/gateway.rs::call_payments`) → `Mined 1 item [decision]: 1 elements, 2 relationships`; `query "decided_about" --kind rel` → 2 edges:
  - `conversations/project/decision/fetch_orders -> src/backend/gateway.rs (decided_about)`
  - `conversations/project/decision/fetch_orders -> src/backend/gateway.rs::call_payments (decided_about)`
  - PASS: `decided_about` edges created (src/conversation_indexer/types.rs:246-280 — decision nodes link to `code_targets` extracted from backtick / `file::symbol` / `src/...` patterns).

## Tracker
- Conversation mining (#175): PASS. Note: `decided_about` edges only when the mined decision references code elements (backtick / `file::symbol` / `src/...` patterns per `extract_code_targets`); decision-only text without code refs yields elements but 0 edges (expected).
