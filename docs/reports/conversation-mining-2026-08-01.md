# Conversation mining live evidence — 2026-08-01

## Environment

- leankg: `prd/conversation-mining` branch (worktree), release build `cargo build --release`
- Storage: CozoDB sqlite in a `mktemp -d` TempDir project (`.leankg/leankg.db`)
- CLI: `./target/release/leankg mine-conversations`

## Steps

1. Built release binary with the new `MineConversations` subcommand.
2. Created TempDir project `tmp.*/proj` with `.leankg/`.
3. Copied one fixture export per format (`tests/fixtures/conversations/`).
4. Ran `mine-conversations` for claude, slack, chatgpt (file + directory input).
5. Mined a decision naming `src/gateway.rs::handle_auth` and verified the
   `decided_about` edge in the exported graph and via `query --kind name`.

## Results

### Claude (single file)

```
Mined 3 items from 1 source(s) [decision, preference]: 3 elements, 0 relationships
  [decision] conversations/proj/decision/recommend: I recommend JWT with refresh tokens. We should go with RS256 for signing.
  [decision] conversations/proj/decision/rs256: OK, decision: we adopt RS256 JWT for the gateway auth service.
  [preference] conversations/proj/preference/good: Good. Also noting a preference: prefer async/await style in the new handlers.
Done.
```

### Slack (single file)

```
Mined 3 items from 1 source(s) [decision, preference, problem]: 3 elements, 0 relationships
  [decision] conversations/proj/decision/will: Decision: we will use gRPC for inter-service communication.
  [problem] conversations/proj/problem/batch: Problem: the batch job keeps timing out at 5 minutes.
  [preference] conversations/proj/preference/protobuf: Preference: prefer protobuf over JSON for internal APIs.
Done.
```

### ChatGPT (directory input, non-matching files skipped)

```
[mine-conversations] skipping .../claude_export.json: ...: not a ChatGPT export: missing field `mapping`
[mine-conversations] skipping .../slack_export.json: ...: not a ChatGPT export: missing field `mapping`
Mined 2 items from 1 source(s) [decision, milestone]: 2 elements, 0 relationships
  [decision] conversations/proj/decision/should: We should migrate the storage layer to PostgreSQL.
Done.
```

### decided_about edge (decision naming a code target)

Mine output: `Mined 1 item from 1 source(s) [decision]: 1 elements, 2 relationships`

Graph export (`leankg export --format json`):

```
decided_about edges: 1
  conversations/proj/decision/rs256 -> src/gateway.rs::handle_auth
```

Query (`leankg query --kind name RS256`):

```
Found 2 element(s) with name 'RS256':
  - RS256 (decision:1 1)
    File: conversations/claude/decision
```

## Pass/Fail vs AC

| AC | Result |
|----|--------|
| FR-MP-09 claude parser | PASS — 3 items mined from fixture |
| FR-MP-10 chatgpt parser | PASS — 2 items mined, nested mapping walked |
| FR-MP-11 slack parser | PASS — 3 items mined |
| FR-MP-12 typed extraction | PASS — decision/preference/milestone/problem element types persisted |
| FR-MP-13 CLI flags | PASS — `--format` / `--project` / `--input` all honored |
| US-MP-03 decided_about | PASS — edge verified live `decision -> src/gateway.rs::handle_auth` |

## Tracker

- Mark `US-MP-03`, `FR-MP-09`, `FR-MP-10`, `FR-MP-11`, `FR-MP-12`, `FR-MP-13`
  DONE after merge.
