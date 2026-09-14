# LeanKG — AI Agent Context

The implementation is 100% Go in `go/` (module `github.com/FreePeak/LeanKG/go`).
The Rust tree is gone; any command mentioning `cargo`, `src/`, `target/`, or
CozoDB is historical and will not work. **[AGENTS.md](AGENTS.md) is the single
source of truth** for layout, workflow and the CLI reference — read it first.

## Commands that exist

```bash
make go-build        # CGO_ENABLED=0 binaries into go/bin/ (leankg, leankg-embed)
make go-test         # go test ./... -count=1
make go-vet          # go vet ./...
make go-build-tstree # the tree-sitter tier (-tags tstree, needs CGO)
make go-ui-assets    # build ui-v2 and sync it into go/internal/web/embed
make dual-engine     # sqlite + live-PostgreSQL acceptance gate
```

## Facts worth knowing before you edit

- Version source of truth: `go/cmd/leankg/VERSION` (`go:embed`, overridable with
  `-X main.version=`). `internal/mcp/server.go` carries a second copy pinned by
  a drift test. `.github/workflows/release.yml` cuts the tag and publishes the
  four binaries in one run.
- The MCP surface is exactly three tools — `import`, `query`, `status` — with the
  action vocabulary resolved in `internal/core`.
- Storage goes through the `store.Backend` interface (SQLite default,
  PostgreSQL+pgvector opt-in). Consumers take `store.Backend`, never a concrete
  store.
- `.gitignore` gotcha: `internal/tstree/**` holds vendored generated C grammars
  and `proto/` is invisible to a global ignore pattern — check
  `git status --ignored` before concluding a file is committed.
