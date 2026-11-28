# CLI reference

`leankg` is the server, indexer and query client. `leankg-embed` is the
embedding pipeline. Full usage text is available from `leankg help` or
`leankg <command> --help`.

## Core workflow

```bash
leankg index .                          # index + incremental embed -> .leankg/leankg.db
leankg index . --no-embed               # index only
leankg index <dir> --hook               # session-start mode with a time budget
leankg writer --project <dir>           # index once, then re-index on each new commit
leankg query "parseConfig"              # name lookup (exact, then fuzzy), JSON output
leankg query "parseConfig" --compress   # one line per result
leankg impact src/main.go --depth 3     # blast radius of a file or element
leankg status                           # health, inventory, freshness, embed state
leankg doctor                           # store path, element and file counts, watermark
leankg doctor --deep                    # extended checks
```

`leankg index --hook` exits 0 without indexing when HEAD has more than
`LEANKG_HOOK_MAX_FILES` (default 10000) indexable files, or when another index
of the store is running. It runs under a time budget (`--hook-budget`, else
`LEANKG_HOOK_BUDGET`, default 10s) and resumes on the next run.

## Query actions

An empty action routes down the ladder: L1 exact identifier, L2 keyword, then
L3 semantic.

| Question | Call |
| --- | --- |
| Any identifier (default) | `query "parseConfig"` |
| Who calls X? | `query --action callers --to <qualified-name>` |
| Who does X call? | `query --action callees --to <qualified-name>` |
| Blast radius of a change | `leankg impact <file>` or `query --action impact --to <qualified-name>` |
| How does A reach B? | `query --action path --to <qualified-name>` |
| Element details | `query --action explain --to <qualified-name>` |
| Pattern search | `query --action pattern --pattern "func $_(...)"` |
| Read a file (compressed) | `query --action read --path src/main.go` |
| Session-start memory | `query` with `action=memory`, `args.command=session_recall` |
| Session-end retain | `import` with `action=memory`, `args.command=session_retain` |
| Health and freshness | `status` |

The full action vocabulary is listed by `leankg help` and in the
[MCP tool contract](mcp-tool-contract.md).

## Server

```bash
leankg serve --stdio                    # MCP over stdio (what harnesses spawn)
leankg serve --http 127.0.0.1:9699      # MCP over streamable HTTP (/mcp, /health)
leankg serve --rest 127.0.0.1:8080      # REST API (/health, /api/v1/*)
leankg serve --rpc 127.0.0.1:9090       # ConnectRPC (gRPC + gRPC-Web + JSON)
leankg serve --ui 127.0.0.1:8081        # embedded dashboard
leankg serve --read-only                # reader role: store opened read-only
leankg serve --memory                   # enable the agent memory layer
leankg serve --embed-provider local     # embedding provider for query vectors
```

With neither `--stdio` nor an address, the default is
`--http 127.0.0.1:9699`. `--project` defaults to the working directory.

## Authentication

```bash
leankg auth register --email EMAIL --password PW --name NAME
leankg auth admin init                  # create the site admin, write LEANKG_ADMIN_* to .env
leankg auth token create --name NAME --role admin|contributor|viewer [--ttl 24h]
leankg auth token list
leankg auth token revoke --token-id ID
```

### Authentication and the HTTP gate

A client calls the `authenticate` tool with `{"username": ..., "password": ...}`
and receives `{"token": "lkg_...", "role": "admin", "expires_at": "<RFC3339>"}`.
Later requests send `Authorization: Bearer <token>`.

With the gate on, a request with no `Authorization` header can reach only the
protocol handshake, `tools/list`, and `authenticate`. A request with an invalid
or expired token gets HTTP 401. The REST and ConnectRPC surfaces have no login
tool, so they need a token minted with `leankg auth token create`.

The gate is on when any of these holds:

- an env token is set (`LEANKG_TOKEN_ADMIN`, `LEANKG_TOKEN_CONTRIBUTOR`,
  `LEANKG_TOKEN_VIEWER`);
- a database token exists (`leankg auth token create`);
- a site-admin account exists (`leankg auth admin init`).

Project grants limit a token to named repositories:

```bash
leankg auth token create --name ci-alpha --role viewer --scopes project:alpha
```

A token with at least one `project:` scope reaches only the named projects. A
withheld project and an unknown name get the same refusal, so the response
never reveals which projects exist. A token with no `project:` scope is
unrestricted for projects.

stdio is not gated: it runs as a local subprocess started by the user, and
runs as Admin.

### Non-loopback binds

`leankg serve --http/--rest/--rpc <non-loopback>` exits with an error unless a
bearer token is configured. With no token and no site admin, every caller would
resolve to Admin, which would expose the project graph, the file reader and the
memory write path to the network.

Bind `127.0.0.1` (the default), or configure a token first with
`leankg auth token create` or `LEANKG_TOKEN_*`. `--ui` is the one deliberately
public listener; its `/api/file` reader answers 403 on a non-loopback bind when
the gate is off.

## Embedding pipeline

```bash
leankg-embed run                        # incremental embed
leankg-embed full                       # stamp-guarded full rebuild
leankg-embed status                     # stamp, coverage and drift
leankg-embed export | import            # NDJSON offsite flow (pairs with scripts/hf-embed.sh)
```

See [Embeddings](embedding.md) for providers, the EmbeddingGemma 2 bulk path
and the write cap.

## Knowledge and governance

```bash
leankg prd --source docs/prd.md         # ingest requirements
leankg prd-trace FR-3T-01               # trace a requirement to code
leankg incident add | list | show       # incidents
leankg note --target SERVICE --content TEXT
leankg env-conflicts --service NAME     # configuration conflicts across environments
leankg service-context --service NAME
leankg team-map                         # ownership map
leankg annotate | link | search-annotations
```

## Maintenance

```bash
leankg audit export | verify            # read and check the audit ledger
leankg metrics --session                # agent session metrics from the telemetry ledger
leankg dashboard                        # dashboard on 127.0.0.1:9701
leankg telemetry status | enable | disable | purge | link
leankg gc [DIR]                          # remove abandoned embedding collections
leankg vacuum [DIR]                      # reclaim store space
leankg migrate [--engine sqlite|postgres]
leankg refresh [PATH]                    # re-ingest an external source
leankg export [--format json|dot|mermaid]
leankg update [--check]                  # follow published GitHub releases
leankg version
```

Telemetry capture is off by default and needs explicit consent. See
[Telemetry](telemetry.md).

## Memory

`leankg memory` keeps a central copy of Claude Code's per-repository memory
files, so a preference saved in one repository can be recalled from another.
The guide is [Central agent memory](central-agent-memory.md). Placeholders below
are `<leankg-binary>` and `<project-dir>`.

```bash
leankg memory sync [--dry-run] [--rebuild] [--json]
leankg memory migrate [--project SLUG] [--dry-run] [--json]
leankg memory recall <query...> [--scope project|global|all] [--type KIND] [--limit N] [--dir DIR] [--json]
leankg memory hook                      # SessionStart hook; reads hook JSON on stdin
leankg install --target claude-code --register-memory [--hook-only]
```

The `memory` verb with no subcommand prints the usage and exits 1. An unknown
subcommand does the same.

### memory sync

Makes every `cc-` bank under the central root equal the Claude Code memory
files under `~/.claude/projects` (`CLAUDE_CONFIG_DIR/projects` when that is set).
It waits for a sync that is already running.

| Flag | Meaning |
| --- | --- |
| `--dry-run` | Print the plan; change no bank. On a missing central root it works in a temporary directory that it removes, so the root is not created. |
| `--rebuild` | Delete every `cc-` row, then sync from scratch. The only way to delete when the scan is empty. |
| `--json` | Print the result as JSON. |

Text output: a count line, then one line per bank. A withheld delete prints a
`WARNING` line. JSON result (fields as the code emits them):

```json
{
  "root": "<central root>",
  "projects": "<projects root>",
  "documents": 3,
  "upserted": 0,
  "deleted": 0,
  "skipped": 0,
  "deletes_withheld": false,
  "banks": [{"bank": "cc-global", "project": "", "rows": 2}]
}
```

`projects` is the projects root path, not a count. `deletes_withheld` is `true`
when deletes were held back (see Safety below).

Exit codes: 0 on success, 1 on error (for example an invalid deny-list line or
an unreadable central root).

### memory migrate

One explicit run that copies all existing Claude Code memory into the central
root. The Claude Code files are read, never modified. It runs the same apply path
as `memory sync`, then verifies that every bank holds exactly the desired rows.

| Flag | Meaning |
| --- | --- |
| `--project SLUG` | Migrate only this project slug. An unknown slug fails with `no memory found for project <slug>`. |
| `--dry-run` | Print the inventory and plan; write nothing. |
| `--json` | Print the result as JSON. |

JSON result:

```json
{
  "root": "<central root>",
  "projects": [{"slug": "<slug>", "bank": "cc-p-<hex>", "files": 2, "global": 1, "skipped": 0}],
  "documents": 3,
  "upserted": 3,
  "skipped": 0,
  "verified": true,
  "mismatches": []
}
```

`mismatches` entries are `{"bank", "document_id", "reason"}` with reason
`missing`, `extra` or `hash`. The JSON result has no `deleted` field.

Exit codes: 0 when verified, or on a dry run. 1 when verification fails
(mismatches are listed) or on error.

### memory recall

Ranks the central memories for a query. It calls the same function as the
`agent_recall` MCP command. On this CLI path no embedder is wired, so the ranking
is keyword (BM25) only.

| Flag | Meaning |
| --- | --- |
| `--scope project\|global\|all` | Default `project`. See [scope mapping](central-agent-memory.md#how-memory-types-map-to-scope). |
| `--type KIND` | Keep only this kind (`user`, `feedback`, `project`, `reference`, `unknown`). Repeatable. Applied before the limit. |
| `--limit N` | Maximum rows. Default 8. |
| `--dir DIR` | The caller's repository. Default: the current directory. |
| `--json` | Print the result as JSON. |

Text output is one block per memory: `N. [kind] name (project or global)`, the
description, and the first line of content (cut at 120 characters). With no
match it prints `recall: no memories match`.

JSON result (also the `agent_recall` result shape):

```json
{
  "command": "agent_recall",
  "scope": "global",
  "project": "<caller slug or empty>",
  "banks": ["cc-global"],
  "count": 1,
  "memories": [{"id": "ccm-...", "name": "...", "kind": "user", "project": "<slug>", "description": "...", "content": "...", "links": [], "timestamp": 0, "score": 1.5}]
}
```

Exit codes: 0 on success (including no match), 2 when no query is given (the
usage error), 1 on other errors. An unknown `--scope` exits 1 and lists the
valid scopes. When the central root has no index yet, it exits 1 with
`` run `leankg memory sync` once ``.

### memory hook

The `SessionStart` hook. It reads the hook JSON on stdin (`cwd` is used; stdin
is waited for at most 500 ms). Without a usable `cwd` it falls back to
`CLAUDE_PROJECT_DIR`, then to the working directory.

1. Sync the `cc-` banks at every session start. The sync takes a non-waiting
   lock; when another sync holds it, the sync step is skipped and the stored rows
   are used. The sync has a 2 s budget covering the file scan and the bank
   writes; a scan cut short by it writes and deletes nothing.
2. Print the cross-repository block for the caller: one context line, the
   newest `user` and `feedback` memories saved by other repositories as one line
   each (at most 30 lines and about 1,500 tokens), and one hint line that names
   `args.cwd`.

Nothing is printed when there is nothing to inject. The process always exits 0.
A failure writes at most a couple of lines on stderr. The run has a 3 s deadline
inside the process; `settings.json` allows 10 s.

### install --register-memory

`leankg install --target claude-code --register-memory` writes the leankg MCP
entry (as `install` always does) and merges one `SessionStart` command hook
(`<leankg-binary> memory hook`, `timeout` 10, no matcher) into the Claude Code
settings file: `$CLAUDE_CONFIG_DIR/settings.json` when `CLAUDE_CONFIG_DIR` is
set, else `~/.claude/settings.json`. Other hooks are preserved, and a second run changes
nothing. Only `claude-code` accepts the flag; any other target fails with an
error before anything is written. The target name is `claude-code`, not
`claude`, which the command rejects as unknown.

`--hook-only` (with `--register-memory`) merges the same hook and writes no MCP
config, so `~/.claude.json` is left as it is. Use it when you manage the MCP
entry yourself or already registered leankg at user scope. It needs
`--register-memory` and is a usage error (exit 2, nothing written) with
`--register-cwd`, `--http`, `--url` or `--project`. Its output is
`registered claude-code session-start memory hook (MCP config not written)`.

### Safety

- The memory verbs read `~/.claude/projects` and write only under the central
  root `~/.leankg/memory` (`GlobalRoot`). A dry run on a missing central root
  writes only to a temporary directory it removes. They never write into `~/.claude`.
- `install --register-memory` writes exactly one hook entry into the Claude Code
  settings file (`$CLAUDE_CONFIG_DIR/settings.json`, else `~/.claude/settings.json`),
  and the MCP entry that `install` always writes into `~/.claude.json`. With
  `--hook-only` only the hook entry is written.
- Deletes are withheld when the projects root is missing or holds no memory
  files while central rows exist. The run reports `deletes_withheld: true` and
  deletes nothing. `--rebuild` is the only way to clear those rows.
- `migrate` never modifies Claude Code's files. A dry run never creates the
  central root.
- Deny-list: `<central root>/scrub.txt` (`~/.leankg/memory/scrub.txt`). One
  regular expression per line; blank lines and lines starting with `#` are
  ignored. A match is replaced with `[SCRUBBED]` on write and on read. An invalid
  line fails the load with its line number. The hook then skips sync and injects
  from the stored rows; `memory sync` and `migrate` exit 1.

## Environment variables

| Variable | Purpose |
| --- | --- |
| `LEANKG_DB_ENGINE` | `sqlite` (default) or `postgres` |
| `LEANKG_PG_URL` | PostgreSQL URL when `LEANKG_DB_ENGINE=postgres` |
| `LEANKG_PROJECT_DIRS` | Comma-separated repositories served by one process |
| `LEANKG_PROJECT` | Default project for `query` and `impact` |
| `LEANKG_EMBED_PROVIDER` | `local` (default), `openai`, `deterministic` (tests) |
| `LEANKG_EMBED_*` | Model, dimensions, endpoint, prefixes and timeouts |
| `LEANKG_TOKEN_ADMIN` / `_CONTRIBUTOR` / `_VIEWER` | Static bearer tokens |
| `LEANKG_HOOK_MAX_FILES` | File-count gate for `index --hook` (default 10000) |
| `LEANKG_HOOK_MAX_EMBED` | Embedding backlog gate for `index --hook` (default 500) |
| `LEANKG_ADMIN_USERNAME` / `LEANKG_ADMIN_PASSWORD` | Credentials written by `auth admin init` |

The commented list with examples is in [`.env.example`](../.env.example).
PostgreSQL is used only when explicitly requested; unsetting both variables
returns to the SQLite default.
