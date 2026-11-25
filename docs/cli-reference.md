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
