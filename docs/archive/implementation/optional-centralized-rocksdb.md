# Optional Centralized RocksDB Storage

LeanKG uses the existing per-project SQLite-backed CozoDB file by default. No extra setup is required for normal users:

```bash
leankg init
leankg index .
leankg mcp-http --port 9699
```

The centralized RocksDB backend is optional. It is useful when one long-running HTTP MCP server should avoid opening a separate SQLite file inside every project checkout. In this mode, LeanKG still uses each project's `.leankg/` directory as the project marker and config location, but graph storage is redirected to one central RocksDB root.

## Host Setup

Run the local binary with:

```bash
export LEANKG_DB_ENGINE=rocksdb
export LEANKG_ROCKSDB_ROOT="$HOME/.leankg-rocksdb"
cargo build --release
target/release/leankg mcp-http --port 9699 --project /path/to/project
```

Each project is stored below:

```text
$LEANKG_ROCKSDB_ROOT/projects/<project-name>-<stable-hash>/
```

If `LEANKG_DB_ENGINE` is not set, LeanKG opens the current SQLite path:

```text
<project>/.leankg/leankg.db
```

## Docker Setup

Use the optional compose file when you want the HTTP server and centralized RocksDB storage inside Docker:

```bash
docker compose -f docker-compose.rocksdb.yml up --build
```

The compose service:

- builds the LeanKG release binary
- starts `leankg mcp-http --port 9699 --project /workspace`
- mounts the current checkout at `/workspace`
- stores RocksDB data in the named Docker volume `leankg-rocksdb`

Stop it with:

```bash
docker compose -f docker-compose.rocksdb.yml down
```

Remove the optional centralized data volume with:

```bash
docker volume rm leankg_leankg-rocksdb
```

## Runtime Visibility

`mcp_status` includes both the project marker path and actual storage path:

```json
{
  "database": "/path/to/project/.leankg",
  "storage_engine": "rocksdb",
  "storage_path": "/Users/me/.leankg-rocksdb/projects/project-abc123..."
}
```

This makes it explicit whether the server is using default SQLite or optional centralized RocksDB.
