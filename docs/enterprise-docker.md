# LeanKG Enterprise Docker

Split the monolithic LeanKG image into two containers: a **cozoserver** that owns the
RocksDB-backed graph store, and a **leankg** sidecar that serves the MCP + REST APIs.

This unlocks:

- Independent scaling of the storage tier (more RAM for big graphs without paying
  for it on every API replica).
- Backup/restore orchestration that targets RocksDB alone (no MCP downtime).
- Future swap to a TiKV-backed cozoserver without changing LeanKG callers.

## What ships in this branch

| File                                  | Purpose                                                          |
| ------------------------------------- | ---------------------------------------------------------------- |
| `Dockerfile.cozoserver`               | Builds `freepeak/cozoserver` (CozoDB HTTP server + RocksDB).     |
| `docker-compose.enterprise.yml`       | Two-service compose wiring cozoserver + leankg.                  |
| `entrypoint.sh` (modified)            | Waits for cozoserver health when `LEANKG_COZO_ENDPOINT` is set.  |

## What does NOT ship (yet)

The Rust side does not yet consume `LEANKG_COZO_ENDPOINT`. That client lands in a
follow-up branch. For now:

- The `leankg` container still embeds SQLite (per-project) and optional RocksDB
  via `LEANKG_DB_ENGINE=rocksdb`. The two containers run side by side; the
  cozoserver's RocksDB is unused by LeanKG until the HTTP client lands.
- `LEANKG_COZO_ENDPOINT` is read only by `entrypoint.sh` for health-gating.

This is deliberate: it lets ops prove out the network, image builds, and
healthchecks today without waiting on a Rust refactor. When the HTTP client
arrives, no compose changes are needed — just a code change in
`src/db/schema.rs::init_db`.

## Deploy

```bash
# 1. Build both images locally (override freepeak/* tags via .dockerfile).
docker build -f Dockerfile.cozoserver -t freepeak/cozoserver:latest .
docker build -f Dockerfile.rocksdb    -t freepeak/leankg:latest     .

# 2. Bring up the stack.
docker compose -f docker-compose.enterprise.yml up -d

# 3. Tail logs.
docker compose -f docker-compose.enterprise.yml logs -f

# 4. Tear down (preserves named volumes).
docker compose -f docker-compose.enterprise.yml down

# 5. Wipe storage (DESTRUCTIVE — drops RocksDB).
docker compose -f docker-compose.enterprise.yml down -v
```

## Resource sizing

| Service      | mem_limit | mem_reservation | cpus | Notes                                      |
| ------------ | --------- | --------------- | ---- | ------------------------------------------ |
| `cozoserver` | 4g        | 2g              | 4    | Floor for single-node RocksDB; bump for >1M nodes. |
| `leankg`     | 4g        | 2g              | 4    | Smaller than the single-container deploy — graph state lives in cozoserver. |

Override per environment via `.dockerfile`:

```yaml
COZOSERVER_MEM_LIMIT: 8g
LEANKG_MEM_LIMIT: 6g
```

## Networking

Both services share the `enterprise` bridge network. `cozoserver` does **not**
publish port 9070 to the host — only `leankg` reaches it. To poke cozoserver
directly for debugging:

```bash
docker compose -f docker-compose.enterprise.yml exec cozoserver \
    curl -s -X POST http://127.0.0.1:9070/text-query \
        -H 'Content-Type: application/json' \
        -d '{"script":"::relations","params":{}}'
```

If you need cozoserver reachable from the host, add a `ports:` block to the
service in your override. Treat 9070 as a trusted internal API (CozoDB
explicitly warns that non-loopback binds generate an auth token, see
`Dockerfile.cozoserver` for the rationale).

## Backup / restore (manual)

```bash
# Stop writes (optional but recommended for snapshot consistency).
docker compose -f docker-compose.enterprise.yml stop leankg

# Snapshot the named volume.
docker run --rm \
    -v leankg_cozo-data:/data/cozo:ro \
    -v $PWD/backups:/backup \
    alpine tar czf /backup/cozo-$(date +%Y%m%d-%H%M%S).tar.gz -C /data/cozo .

# Restart.
docker compose -f docker-compose.enterprise.yml start leankg
```

For a hotter workflow, lean on cozoserver's own `/backup` endpoint:

```bash
docker compose -f docker-compose.enterprise.yml exec cozoserver \
    curl -s -X POST http://127.0.0.1:9070/backup \
        -H 'Content-Type: application/json' \
        -d '{}' > cozo-backup.json
```

## Upgrade paths

- **Need more concurrency than single-node RocksDB?** Switch `cozoserver`'s
  build to `cozo-bin -F storage-tikv` and stand up a PD + TiKV + cozoserver
  cluster. LeanKG callers are unchanged (same HTTP wire).
- **Need TLS + auth on the cozoserver hop?** Front the network with a reverse
  proxy (caddy / nginx) that terminates TLS and injects the `x-cozo-auth`
  header. LeanKG then needs a thin reqwest wrapper to send the header — add
  `LEANKG_COZO_AUTH_TOKEN` env var when implementing the HTTP client.
- **Need high availability?** Run two cozoserver instances backed by shared
  storage (NFS / EBS / cloud block), use a TCP load balancer in front.
  CozoDB does not replicate natively; this is the cheapest viable HA.