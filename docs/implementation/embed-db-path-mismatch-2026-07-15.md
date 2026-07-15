# Embed HNSW After `leankg index` — Root Cause: DB Path Mismatch (2026-07-15)

## TL;DR

After `leankg index` completed against a project on RocksDB, `leankg embed`
silently produced **zero embedded vectors**. `semantic_search` continued to
work via the ontology-first fallback but never used the CozoDB HNSW
(`embedding_vectors:vec_idx`) path — defeating FR-HNSW-C (Docker OOTB
semantic) and FR-HNSW-D (default NL → HNSW → rerank → graph).

The bug was a **path-resolution mismatch** between
`central_project_storage_path` and how the `embed` / `semantic-context` /
`smoke-test` CLI commands constructed the DB path passed to `init_db`.

## Symptom

`leankg embed --project /workspace-be`:

```
Embed build complete (Incremental) in 0.51s
  Considered:    0
  Embedded:      0
  Skipped fresh: 0
  Orphans reaped: 0
  Index size:    0 vectors
```

`leankg mcp-http` (also reaching the same DB) `INFO` log:

```
Cozo storage = RocksDb at /data/leankg-rocksdb/projects/workspace-be-6917453a1780
```

`leankg embed` (also on the same workspace) `INFO` log:

```
Cozo storage = RocksDb at /data/leankg-rocksdb/projects/leankg-db-7be63682882f
```

**Two different paths. Two different databases. The embed opens a brand-new
empty database at `leankg-db-7be63682882f` while the indexer / MCP server
use `workspace-be-6917453a1780`.** The fresh DB has no `code_elements`, so
the embed step finds nothing to embed and exits cleanly — masking the bug
as "success with 0 vectors".

`MCP tools/call semantic_search` returns:

```json
{"method": "ontology+semantic(semantic+name_fallback)"}
```

instead of the expected:

```json
{"method": "hnsw+rerank"}
```

## Why Two Different Paths?

`src/db/schema.rs::central_project_storage_path(db_path)` decides the
project root by inspecting the **filename** of `db_path`:

```rust
let project_root = if db_path.file_name().and_then(|n| n.to_str()) == Some(".leankg") {
    db_path.parent().unwrap_or(db_path)   // .leankg -> <project>
} else {
    db_path                                // leankg.db -> <project>/.leankg/leankg.db
};
```

The hash is computed from the canonicalized `project_root`.

| Caller                       | `db_path` they pass                       | `file_name()`    | `project_root` becomes                | Hash   |
|------------------------------|-------------------------------------------|------------------|---------------------------------------|--------|
| `leankg index`               | `<project>/.leankg`                       | `.leankg`        | `<project>`                           | ✓      |
| `leankg mcp-http`            | `<project>/.leankg`                       | `.leankg`        | `<project>`                           | ✓      |
| `leankg embed`               | `<project>/.leankg/leankg.db`             | `leankg.db`      | `<project>/.leankg/leankg.db`         | **✗**  |
| `leankg semantic-context`    | `<project>/.leankg/leankg.db`             | `leankg.db`      | `<project>/.leankg/leankg.db`         | **✗**  |
| `leankg smoke-test`          | `<project>/.leankg/leankg.db`             | `leankg.db`      | `<project>/.leankg/leankg.db`         | **✗**  |

The hash mismatch is silent: both DBs coexist on the same RocksDB root, and
the `leankg` process never notices it is talking to a different project.

## Evidence

```text
sha256(/workspace-be)             = 6917453a1780...   (existing index path)
sha256(/workspace-be/.leankg/leankg.db) = 7be63682882f...   (where embed opened)
```

```text
ls /data/leankg-rocksdb/projects/
  be-food-notification-f714dea71f5e     (from earlier experiments)
  be-marketplace-a4c16aa1f396
  be-restaurant-209ac5463d01
  leankg-db-48291f1be4ca                 (orphan — created by the bug)
  leankg-db-7be63682882f                 (orphan — created by the bug)
  leankg-db-861294d69439                 (orphan — created by the bug)
  workspace-be-6917453a1780              (the real index)
  workspace-c52ddf65534b                 (the /workspace index)
  workspace-freepeak-1d4898664334
```

The `leankg-db-*` entries are all from the broken path-resolution code;
they contain the lone empty `embedding_state` row that the failed embed
upserted before exiting.

## Fix

`src/db/schema.rs::central_project_storage_path` now reduces `db_path` to
the project root through a single helper (`project_root_from_db_path`)
that handles both `.leankg` directory input and `leankg.db` file input:

```rust
fn project_root_from_db_path(db_path: &Path) -> std::path::PathBuf {
    let file_name = db_path.file_name().and_then(|n| n.to_str());
    if file_name == Some("leankg.db") {
        if let Some(leankg_dir) = db_path.parent() {
            if leankg_dir.file_name().and_then(|n| n.to_str()) == Some(".leankg") {
                if let Some(project) = leankg_dir.parent() {
                    return project.to_path_buf();
                }
            }
            return leankg_dir.to_path_buf();
        }
        return std::path::PathBuf::from(".");
    }
    if file_name == Some(".leankg") {
        return db_path.parent().unwrap_or(db_path).to_path_buf();
    }
    db_path.to_path_buf()
}
```

Tests added in `src/db/schema.rs::tests`:

* `central_project_storage_path_resolves_leankg_db_to_same_root_as_dot_leankg`
  — pins the contract that the directory and file forms resolve identically.
* `central_project_storage_path_handles_detached_db_file` — the
  gracefully-degrading fallback for `leankg.db` moved out of `.leankg/`.

## Verification

After the fix, `leankg embed --project /workspace-be` opens the correct
RocksDB project (`workspace-be-6917453a1780`) and the `Cozo storage =`
log matches the indexer:

```
Cozo storage = RocksDb at /data/leankg-rocksdb/projects/workspace-be-6917453a1780
```

`leankg embed` then reports the real element count and processes it:

```
embed batch done: running total 2400/401749 (chunk_size=256)
...
```

A 1-shot `leankg semantic-context` against the same DB after embed
confirms the HNSW path is live:

```
Query:   how does the reranker score documents
Reranker: active (bge-reranker-v2-m3)

Seeds (10):
   1. [class          ] ./src/embeddings/models.rs::RerankScore  (rerank=-0.5370)
   ...
```

`semantic_search` MCP tool returns the expected `method: hnsw+rerank` once
the `embedding_state` table is non-empty.

## Followups

1. The `leankg-db-*` orphan DBs created by the bug should be removed once
   the cleanup helper lands.
2. Add a startup health check that asserts the project root derived from
   `init_db` matches the path used by the running `leankg index` history
   (a `manifest` sidecar file is already created in the project dir).
3. Consider consolidating all `init_db` call sites to pass the `.leankg`
   directory (not the `leankg.db` file) so this class of mismatch cannot
   recur. The fix above is defensive — both forms now work — but the
   convention should be one or the other.
