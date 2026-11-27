# Getting started

This guide takes a repository from nothing to an AI coding agent that can query
its code graph. It covers installation, first index, client wiring and the
first checks of a running deployment.

If you want to know why LeanKG is built this way, read
[Architecture](architecture.md). For every command and variable, see the
[CLI reference](cli-reference.md).

## Requirements

| | |
| --- | --- |
| Operating system | macOS or Linux |
| Go | 1.25+ (only when building from source) |
| Database | None. SQLite is the default and ships with the binary |
| PostgreSQL | Optional. Needed only for a shared, server-scale store |
| Docker | Optional. Needed only for the demo container |

## Install

### Prebuilt archives

[Releases](https://github.com/FreePeak/LeanKG/releases/latest) carry
`leankg-<os>-<arch>.tgz` for linux and darwin on amd64 and arm64. Each archive
holds both binaries at its root, with a `.sha256` file alongside.
`leankg update` follows the same channel.

### Go toolchain

```bash
go install github.com/FreePeak/LeanKG/cmd/leankg@latest         # server and CLI
go install github.com/FreePeak/LeanKG/cmd/leankg-embed@latest   # embedding pipeline
```

### From a checkout

Requires Go 1.25+ and git. Installs to `~/.local/bin` by default; pass
`PREFIX` to change it:

```bash
git clone https://github.com/FreePeak/LeanKG.git && cd LeanKG
scripts/install-go.sh                # or: make install-go
```

### Container

The image is a read-only demo graph, not a production template:

```bash
docker build -t leankg .
docker run --rm -p 8080:10000 -e PORT=10000 leankg   # dashboard and its API on :8080
```

## Index a repository

```bash
cd your-project
leankg index .
```

This creates `.leankg/leankg.db` (SQLite, WAL) and writes the code graph:
files, symbols, imports, calls and references. In a git repository root with
commits, the indexer reads the HEAD commit's objects, so uncommitted and
ignored files are not indexed.

Useful variants:

```bash
leankg index . --no-embed          # index only; skip the incremental embed
leankg index <dir> --hook          # session-start mode, with a time budget
leankg writer --project <dir>      # index once, then re-index on each new commit
```

`leankg index` embeds incrementally after indexing when an embedding provider
is configured. An embedding failure is reported and does not fail the index.

## Wire an AI client

```bash
leankg install --target <client>
```

| Target | Client |
| --- | --- |
| `claude-code` | Claude Code |
| `cursor` | Cursor |
| `codex` | Codex |
| `gemini` | Gemini CLI |
| `opencode` | OpenCode |
| `omp` | Oh My Pi |
| `xdev` | xdev (YAML config) |

The writer merges one `leankg` server entry into the client's config file
without disturbing sibling keys. The stdio entry is `serve --stdio --memory`,
which serves the code graph and the markdown memory layer in one process.
Restart the client so it picks up the new entry.

`leankg connect` is the same writer. Add `--register-cwd` to install an
asynchronous Claude Code `SessionStart` hook that indexes the current
repository when a session begins.

## Serve one graph to many clients

Run a single HTTP server instead of one process per client:

```bash
leankg serve --http 127.0.0.1:9699 --rest 127.0.0.1:8080 --memory
leankg install --target claude-code --http --url http://127.0.0.1:9699/mcp
```

- MCP is served at `/mcp`.
- `GET /health` returns 200 when the server is ready.
- The project is resolved from the process working directory, or from
  `--project DIR`.
- `LEANKG_PROJECT_DIRS` registers several repositories; every request then
  selects one with `?project=<dir or name>`.

A listener bound beyond loopback is refused unless a bearer token is
configured. See [Deployment and security](deployment.md) and the
[CLI reference](cli-reference.md#authentication-and-the-http-gate).

## Enable semantic search

Exact and keyword matches work with no extra setup. Semantic search (L3) needs
embeddings:

```bash
export LEANKG_EMBED_PROVIDER=local      # local llama.cpp sidecar (default)
export LEANKG_EMBED_MODEL=embeddinggemma-2
```

The full variable set, the OpenAI-compatible provider and the offline bulk
path are in [Embeddings](embedding.md). Every vector writer is model-stamp
guarded, so changing the model fails loudly instead of mixing vector spaces.

## Share memory across repositories

Claude Code's per-repository memory can be copied into one central store, so
preferences saved in one repository reach sessions in the others. After the
client is wired, see [Central agent memory](central-agent-memory.md) for the
one-time setup (`leankg memory migrate`, then
`leankg install --target claude-code --register-memory`) and for the rollback.

## Verify a deployment

```bash
leankg doctor                 # store path, element and file counts, watermark
leankg doctor --deep          # extended checks
leankg status                 # health, inventory, freshness, embed state
```

`doctor` exits 0 on pass and 2 on failure. Both commands accept
`--format text|json`.

## Troubleshooting

| Issue | Fix |
| --- | --- |
| Wrong project served | Start the server with `--project DIR` (`query` and `impact` also honor `LEANKG_PROJECT`) |
| Embeddings stale or missing | `leankg-embed status`, then `leankg-embed full`. Provider variables are `LEANKG_EMBED_*` |
| `LEANKG_ERROR_READ_ONLY` | The server runs with `--read-only`. Restart it without the flag, or send writes to a writable instance |
| Server refuses a non-loopback address | Bind `127.0.0.1` (the default), or configure a bearer token first with `leankg auth token create` |
| Query on a multi-project server is refused | Pass `project` (a directory or a project name) on each call |
| `LEANKG_ERROR_PROJECT_NOT_INITIALIZED` | Run `leankg index <path>` for that directory |

## Next steps

- [Architecture](architecture.md): how indexing, retrieval and transports fit together.
- [Flow diagrams](architecture-flow.md): component, data and request flows.
- [Deployment](deployment.md): security model, scale-out and known limits.
- [CLI reference](cli-reference.md): all commands and environment variables.
- [MCP tool contract](mcp-tool-contract.md): tools, arguments and stability tiers.
