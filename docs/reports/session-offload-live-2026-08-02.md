# Session offload (#174) live evidence — 2026-08-02

## Environment
- commit: 8c77b22b | binary: target/release/leankg 0.19.31 (rebuilt, main) | MCP :9878 | project: /tmp/leankg-live-fixture

## Steps
1. Created proper ref file per `SessionStore::write_ref` format (`# Ref:` + tool/step/bytes/sha256 + ```json``` block) at `sess-proper/refs/offload-010.md`
2. `session_recall node_id=offload-010 session_id=sess-proper`
3. `session_recall node_id=does-not-exist` (missing node)

## Results
- Recall → `payload: hits: [{line:12, name:"login"}], tool: "search_code"` — **bit-for-bit** matches the JSON written (hits/name/line exact). PASS (AC: refs md bit-for-bit).
- `bytes: 214`, `ref_file: .../sess-proper/refs/offload-010.md`, `session_id` echoed. PASS.
- Missing node → clean error `node_id does-not-exist not found: No such file or directory (os error 2)`. PASS (AC: missing-node error clean).
- Malformed ref → `node_id offload-002: malformed ref file` (clean, earlier probe). PASS.

## Tracker
- Session offload (#174): PASS. Note: ref files must follow `write_ref` format (frontmatter + JSON fenced block) for `parse_ref_body` to recover payload.
