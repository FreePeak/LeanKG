# Conversation Mining (`mine-conversations`)

**IDs:** US-MP-03 / FR-MP-09..13 — MemPalace-inspired conversation mining.

Mines conversation exports (Claude, ChatGPT, Slack) into typed graph
elements, then persists them into the project's LeanKG graph. Raw verbatim
text is stored — no summarization.

## Usage

```bash
leankg mine-conversations --format <claude|chatgpt|slack> --project <path> --input <file-or-dir>
```

| Flag | Meaning |
|------|---------|
| `--format` | Export format: `claude`, `chatgpt`, or `slack` (required) |
| `--project` | Project root whose `.leankg/` graph receives the mined nodes (default `.`) |
| `--input` | Single export JSON file or a directory of export JSON files |

`--input` may be a file (one export) or a directory (every `*.json` in it
is attempted; files whose shape doesn't match the format are skipped with a
warning). Re-running on the same input is idempotent: nodes keyed by
qualified name, so duplicates collapse.

## What gets mined

Each message is classified by a deterministic keyword classifier into one
of:

| Element type | Signals |
|--------------|---------|
| `decision` | "decision:", "we decided", "we will", "we should" |
| `preference` | "preference:", "prefer", "preferred", "would like" |
| `milestone` | "milestone:", "goal", "deadline", "by end of" |
| `problem` | "problem:", "fails", "timeout", "broken", "error" |

Unmatched messages are skipped (no noise nodes). Mined nodes use the
qualified-name shape:

```
conversations/<project>/<kind>/<topic-slug>
```

with raw verbatim, source format, participants, and timestamp stored in
`metadata.verbatim` / `.source` / `.participants` / `.timestamp`.

## `decided_about` edges

When a mined `decision` message names code targets — backtick-quoted
paths, inline `file::symbol` (`src/gateway.rs::handle_auth`), or bare
`src/...` paths — the decision node is linked to each target via a
`decided_about` relationship (confidence 1.0, `confidence_label:
EXTRACTED`).

## Example

```bash
$ leankg mine-conversations --format claude --project . --input ~/chats/
Mined 3 items from 1 source(s) [decision, preference]: 3 elements, 1 relationships
  [decision] conversations/leankg/decision/rs256: Decision: adopt RS256 JWT in src/gateway.rs::handle_auth
  [preference] conversations/leankg/preference/async: prefer async/await style in the new handlers
Done.
```

## Supported export shapes

- **Claude:** `{ "conversations": [ { "messages": [ { role, content:
  string | [{ type: "text", text }] } ] } ] }`
- **ChatGPT:** `{ "mapping": { "<id>": { "message": { author.role,
  content.parts[] } } } }` (nested `mapping` subtrees walked recursively)
- **Slack:** channel export `{ "channel": {...}, "messages": [ { user,
  ts, text } ] }`

## Module layout

- `src/conversation_indexer/mod.rs` — mining orchestration + CLI entry
- `src/conversation_indexer/parsers.rs` — per-format export JSON parsers
- `src/conversation_indexer/types.rs` — `MinedItem` model, classifier,
  `decided_about` edge building
- `tests/conversation_mining_tests.rs` — TDD seams 1–4
- `tests/fixtures/conversations/` — one fixture export per format

Persistence reuses `GraphEngine::insert_elements` / `insert_relationships`;
no new schema tables.
