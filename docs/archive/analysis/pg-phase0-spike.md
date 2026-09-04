# Phase 0 Spike: pgvector Distance Parity + HNSW (CozoDB -> Postgres)

**Date:** 2026-08-04
**Status:** PASS — all T0.x acceptance criteria met
**Plan ref:** `docs/plan-migrate-cozo-to-postgres-pgvector.md` §4 Phase 0 (T0.1–T0.3)
**Test source:** `tests/pg_phase0_spike.rs` (commit `c1b4e013`), `docker-compose.postgres.yml`
**Stack:** `pgvector/pgvector:pg18` image → PostgreSQL 18.4 + pgvector **0.8.6** (aarch64)

---

## 1. Starting the Phase 0 Postgres

The compose file is isolated from the production compose project (own project name
`leankg-pg`, own network `leankg-pg_default`, own container `leankg-pg-phase0`).
Host port **5433 -> container 5432** — host 5432 stays free and nothing collides with
the live `leankg-leankg-1` / `leankg-enterprise-cozoserver-1` containers.

```bash
docker compose -p leankg-pg -f docker-compose.postgres.yml up -d

# one-time extension bootstrap (already applied to this container):
docker exec leankg-pg-phase0 psql -U postgres -d leankg -c "CREATE EXTENSION IF NOT EXISTS vector;"
```

Verify:

```bash
docker exec leankg-pg-phase0 psql -U postgres -d leankg -c "SELECT extversion FROM pg_extension WHERE extname='vector';"
# 0.8.6
```

Test data lives in DB `leankg` (user `postgres` / password `postgres`). Connection string:
`postgresql://postgres:postgres@localhost:5433/leankg` (the `postgres` crate's
`postgres::Client::connect` uses it; override with `LEANKG_PG_URL`).

## 2. Running the spike test

```bash
cargo test --release --test pg_phase0_spike          # unit tests (math, no DB)
cargo test --release --test pg_phase0_spike -- --ignored --test-threads=1   # DB tests, container required
```

`#[ignore]` marks the three DB-backed tests; `--test-threads=1` because each test
DROP/CREATEs the shared `embedding_vectors` table (also serialized by a `Mutex`).

Dataset: **10,000** random dim-384 unit vectors (deterministic seed `0xDEAD_BEEF`,
hand-rolled xoshiro-style PRNG — zero extra deps), one query vector. Table:
`embedding_vectors(qualified_name TEXT PRIMARY KEY, vec vector(384))`.

## 3. Results (measured 2026-08-04, three runs — values stable)

| Metric | Value |
|---|---|
| HNSW index build (`m=16, ef_construction=200`, 10k x dim-384) | **2599 ms** (2569–3073 ms across runs) |
| HNSW top-k query (`k=50, ef=100`) | **4 ms** |
| HNSW recall @50 (Jaccard vs brute-force top-k set) | **1.0000 (100%)** — requirement was >= 98% |
| Top-5 HNSW vs brute force | identical order: `v02608, v08326, v08432, v00227, v04097` |
| Parity (set, order, distance) | PASS — same names, identical order, distance diff < 1e-5 |
| REINDEX CONCURRENTLY | **2777 ms**, reads never blocked |
| Concurrent reads during REINDEX (60 x `SELECT ... ORDER BY <->` at 100 ms cadence) | avg **14.8 ms**, min 4 ms, max 30 ms — **no error, no timeout** |
| Brute-force exact top-k (seq scan) | consistent with HNSW result set (recall 100%) |

REINDEX detail: REINDEX started after ~5 reads and finished in ~2.8 s while 55 more
SELECTs ran — none errored, none exceeded 30 ms (warm ~10–24 ms). No lock wait,
no `REINDEX` visibility gap for readers (pgvector keeps the old index until the
new one is ready).

## 4. Distance semantics — `pgvector <->` vs cozo cosine distance

**Cozo HNSW (`~embedding_vectors:vec_idx { ..., bind_distance: dist }`)** returns
**cosine distance** `1 - cos(θ)`.

**pgvector operators** on type `vector`:

| Op | Distance | Formula |
|---|---|---|
| `<->` | Euclidean (L2) | `sqrt(Σ(aᵢ - bᵢ)²)` |
| `<#>` | negative inner product | `-Σ aᵢbᵢ` |
| `<=>` | cosine distance | `1 - (a·b)/(‖a‖·‖b‖)` |

For **unit (L2-normalized) vectors**, `‖a‖ = ‖b‖ = 1`, so:

```
<a->b>  = sqrt(Σ(aᵢ-bᵢ)²) = sqrt(‖a‖² + ‖b‖² - 2·a·b) = sqrt(2 - 2·cos_angle)
```

`sqrt(2 - 2·x)` is **strictly monotone** in `x` on `[-1, 1]`, so ordering by `<->`
is **identical** to ordering by cosine distance `1 - cos_angle`. Mapping used in the
test (and valid for the spike assertion):

```
cosine_distance = 1 - dot(a,b)  =  <a->b>² / 2        (unit vectors)
```

BGE-small-en-v1.5 embeddings are L2-normalized (fastembed output, dim 384), and cozo
stores them normalized — so both sides of the migration compare the same quantity.
The test asserts `(pgvector_dist²/2 - brute_force_cosine_dist) < 1e-5` for all k=50
rows, and it passed.

**Phase 2+ recommendation:** when the translator is written (plan T3.3), `<=>` is the
more literal operator (returns cosine distance directly, no `²/2` conversion), but
`<->` also works on normalized vectors; the spike validates `<->` (cheapest, index-backed).

**ef_search → LEANKG_HNSW_EF mapping (plan T4.6):** cozo's `resolve_ef()` = `max(k*2, 50)`,
override `LEANKG_HNSW_EF`. pgvector equivalent: `SET LOCAL hnsw.ef_search = <n>` per
transaction (test uses `SET LOCAL hnsw.ef_search = 100` inside a transaction, then the
SELECT; `SET LOCAL` cannot take a bind parameter — literal only). `k=50` here is the
top of LeanKG's `adaptive_k()` 50–300 range.

## 5. Dimension mismatch behavior

`vector(384)` column vs inserting a 385-dim literal:

```sql
INSERT INTO embedding_vectors (qualified_name, vec) VALUES ('x', '[0.1,0.2,...385 vals...]');
-- ERROR:  expected 384 dimensions, not 385
```

Verified on dim-3 column with a 4-dim literal: `ERROR: expected 3 dimensions, not 4`
(and the insert is rejected — row not stored). This is a clean, typed error surfaced
at the driver level (`postgres::Error::Db` with `SqlState(22000)`, message
`expected 384 dimensions, not 385`). The `postgres` crate needs the param sent as
TEXT + cast: `$1::text::vector` (or `::vector`) — a String can't be bound directly to
a `vector` param (crate has no `vector` type registered), which is why the spike
queries use `$n::text::vector`.

Implication for the migration: a dim mismatch (e.g. wrong embedder) fails loudly on
the **first insert/upsert** — never silently truncates or pads. Cozo `::hnsw create
{dim: 384}` also rejects mismatched dims, so behavior is parallel.

## 6. Deviations / notes

- `postgres` crate (v0.19.14) used as **dev-dependency only** — plan D1 says sqlx for
  the real client; that lands in Phase 2+. Spike deliberately minimal.
- `SET LOCAL hnsw.ef_search` cannot take a bind parameter (syntax error) — literal
  format string used.
- REINDEX CONCURRENTLY needs a **separate connection** from the reader loop (test
  opens a second `postgres::Client`).
- `query()` with a raw string goes through wire `Parse` (extended protocol) — a bare
  `$1` next to the `<->` operator cannot be type-inferred (server reports "could not
  determine data type of parameter $1") — always cast the param (`::text::vector`,
  `::int8` for LIMIT).
- `embedding_vectors` is recreated (DROP + CREATE + index) by each DB test; the
  HNSW index is built by the `hnsw_index_build_time` test and left in place.

## 7. Acceptance check (plan §4 Phase 0)

- [x] T0.1 Postgres 18 + pgvector up (Docker, isolated project)
- [x] T0.2 `ORDER BY vec <-> $q LIMIT k` on 10k-row sample returns same top-k as
      brute-force cosine distance (set, order, distance 1e-5)
- [x] T0.3 HNSW build 2.6 s; `REINDEX CONCURRENTLY` works while reads run
      (60 concurrent reads, none blocked/errored); recall 100% >= 98%
- [x] Exit: distance semantics parity confirmed (`<->` on normalized vectors ≡ cosine
      distance ordering); no ADR needed — `<->`/`<=>` equivalence documented here.
