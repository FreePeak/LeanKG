# LeanKG — AI Agent Context

The implementation is 100% Go at the repository root (module `github.com/FreePeak/LeanKG`).
The Rust tree is gone; any command mentioning `cargo`, `src/`, `target/`, or
CozoDB is historical and will not work. **[AGENTS.md](AGENTS.md) is the single
source of truth** for layout, workflow and the CLI reference — read it first.

## Commands that exist

```bash
make go-build        # CGO_ENABLED=0 binaries into bin/ (leankg, leankg-embed)
make go-test         # go test ./... -count=1
make go-vet          # go vet ./...
make go-build-tstree # the tree-sitter tier (-tags tstree, needs CGO)
make go-ui-assets    # build ui-v2 and sync it into internal/web/embed
make dual-engine     # sqlite + live-PostgreSQL acceptance gate
```

## Facts worth knowing before you edit

- Version source of truth: `cmd/leankg/VERSION` (`go:embed`, overridable with
  `-X main.version=`). `internal/mcp/server.go` carries a second copy pinned by
  a drift test. `.github/workflows/release.yml` cuts the tag and publishes the
  four binaries in one run.
- The MCP surface is exactly four tools — `import`, `query`, `status`,
  `authenticate` — with the action vocabulary resolved in `internal/core`. The
  auth model (login, HTTP bearer gate, admin bootstrap) is in
  [AGENTS.md](AGENTS.md#auth-and-the-http-gate).
- Storage goes through the role interfaces in `internal/store/roles.go`
  (SQLite default, PostgreSQL+pgvector opt-in); `store.Backend` composes them.
  Consumers depend on the narrowest role they use, only composition roots
  (`cmd/*`, `core.New`, `store.OpenBackend`) hold a `store.Backend`, and no
  concrete store type is used outside `internal/store`.
- `.gitignore` gotcha: `internal/tstree/**` holds vendored generated C grammars
  and `proto/` is invisible to a global ignore pattern — check
  `git status --ignored` before concluding a file is committed.

## pgvector test fixture: stop it when done

- Any work that starts the fixture (`make dual-engine`, the PG store tests, any `LEANKG_DB_ENGINE=postgres` run) must stop it afterwards.
- From the repo root: `docker compose -f scripts/pgvector.compose.yml down`.
- The container is `leankg-pg-500mb` on host port 5433; the data volume `leankg-pgdata-500mb` survives `down`. Add `-v` only when a clean database is wanted.
- CI jobs and scripts that start it must tear it down in the same unit of work (see `.github/workflows/ci.yml`).
- Confirm with `docker ps --filter name=leankg-pg-500mb` (nothing listed) before calling the task done.
