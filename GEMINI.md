# LeanKG — AI Agent Context (Gemini)

The implementation is 100% Go at the repository root (module `github.com/FreePeak/LeanKG`).
The Rust tree is gone; any command mentioning `cargo`, `src/`, `target/`, or
CozoDB is historical and will not work. **[AGENTS.md](AGENTS.md) is the single
source of truth** — read it for layout, workflow and the full CLI reference.

## Commands that exist

```bash
make go-build        # CGO_ENABLED=0 binaries into bin/ (leankg, leankg-embed)
make go-test         # go test ./... -count=1
make go-vet          # go vet ./...
make go-build-tstree # the tree-sitter tier (-tags tstree, needs CGO)
make go-ui-assets    # build ui-v2 and sync it into internal/web/embed
make dual-engine     # sqlite + live-PostgreSQL acceptance gate
```

## Shape of the engine

- `cmd/leankg` is the single binary: serve / index / query / impact / writer /
  connect / install / setup / doctor / status / run / export / obsidian / …
  `cmd/leankg-embed` is the embedding pipeline.
- Exactly three MCP tools — `import`, `query`, `status` — plus REST, ConnectRPC
  and the embedded ui-v2 dashboard, all funnelling into `internal/core`.
- Storage sits behind `store.Backend` (SQLite default, PostgreSQL+pgvector
  opt-in via `LEANKG_DB_ENGINE=postgres` + `LEANKG_PG_URL`).
- `leankg version` (not `--version`) prints the engine version.
