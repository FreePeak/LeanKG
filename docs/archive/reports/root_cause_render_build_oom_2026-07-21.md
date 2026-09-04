# Root Cause Analysis: Render deploy OOM on v0.19.3

**Date:** 2026-07-21  
**Failed deploy:** commit `9672c45` (`chore: bump version to v0.19.3`)  
**Error:** `Ran out of memory (used over 8GB) while building your code.`  
**Service:** `leankg` on Render (`render.yaml` → `./Dockerfile`, Hobby / Starter pipeline)

---

## Symptom

Auto-deploy after merging PR #95 + version bump failed during Docker build. Prior OnRender symptom was **stale April-2026 UI** ([`root_cause_onrender_legacy_ui.md`](root_cause_onrender_legacy_ui.md)) — the service never picked up ui-v2 because builds were not completing.

---

## Evidence

| Item | Value |
|------|-------|
| Render Starter pipeline | **2 CPU, 8 GB RAM** ([Build Pipeline docs](https://render.com/docs/build-pipeline)) |
| OOM threshold | Build cancelled when RSS **> 8 GB** |
| Failing step | `RUN cargo build --release --features embeddings` in `Dockerfile` |
| Trigger commit | `9672c45` only bumped version; real Dockerfile change was `7f310e9` (PR #95) |
| PR #95 fix | `COPY benches ./benches` — unblocked manifest parse; build then reached rustc |

### Dockerfile before fix (single-stage)

```dockerfile
FROM rust:1-bookworm
# nodejs + npm ci + ui-v2 build in SAME image
RUN cargo build --release --features embeddings
CMD ["leankg", "web"]
```

### Heavy compile surface (`--features embeddings`)

From `Cargo.toml`:

- `ort` (ONNX Runtime bindings) — large C++/Rust link unit
- `fastembed`, `tokenizers`, `ndarray`
- `cozo` with `storage-rocksdb`
- 10+ `tree-sitter-*` language grammars
- `lto = "thin"` in `.cargo/config.toml` — raises peak RSS at link time

UI embed (`src/embed/`, ~1.1 MB) is negligible vs ONNX + RocksDB compile.

---

## Root causes (ranked)

### RC-1 — Release build exceeds Render Starter 8 GB cap (primary)

`cargo build --release --features embeddings` is the memory cliff. This is expected on constrained CI when compiling ONNX + RocksDB + many native deps. The version-bump commit did not introduce new code; it merely triggered a rebuild that finally ran past the earlier manifest error.

### RC-2 — Single-stage Dockerfile inflates peak RSS (contributing)

Old `Dockerfile` kept **Node.js, `node_modules`, UI dist, and full Rust toolchain** resident while `rustc` linked `ort` and `rocksdb`. `Dockerfile.rocksdb` already used multi-stage builds for Hub images; Render `Dockerfile` did not.

### RC-3 — Unbounded / parallel codegen + thin LTO (contributing)

Default `CARGO_BUILD_JOBS` uses all builder CPUs. Even at 2 CPUs, **thin LTO** (`[profile.release] lto = "thin"` in `.cargo/config.toml`) increases linker memory. No Docker-specific override existed.

### RC-4 — PR #95 benches copy is a red herring for OOM (not a cause)

`COPY benches ./benches` fixes `Cargo.toml` `[[bench]]` manifest parse. Benches are **not** compiled during `cargo build --release`. They do not materially change compile memory.

### RC-5 — Live site stuck on old image (downstream symptom)

Failed or skipped builds since ui-v2 / embeddings Dockerfile changes left production on a pre–ui-v2 image. OOM and stale UI share the same upstream cause: **Render never produced a fresh image**.

---

## Logic flow

```text
push main (9672c45)
  → Render Docker build (Starter: 8 GB)
  → npm ci + ui-v2 build          (~1–2 GB with node in image)
  → cargo build --release --features embeddings
       → compile ort, rocksdb, tree-sitter×N in parallel
       → thin LTO link of leankg
  → RSS > 8 GB → build killed
  → deploy fails → leankg.onrender.com stays on old image
```

---

## Recommended fix (implemented)

**Multi-stage `Dockerfile` + build memory guards** (free on Hobby tier):

1. **Stage `ui`:** `node:20-bookworm` — `npm ci` + `npm run build` only.
2. **Stage `builder`:** `rust:1-bookworm` — copy `ui/dist` into `src/embed/`, no Node in this stage.
3. **Stage `runtime`:** `debian:bookworm-slim` — copy stripped `leankg` binary only.
4. **Builder env:**
   - `CARGO_BUILD_JOBS=1` — cap parallel rustc (biggest win on 8 GB).
   - `CARGO_PROFILE_RELEASE_LTO=false` — disable LTO for Docker builds only (keeps `.cargo/config.toml` thin LTO for local release).
5. Bump `UI_EMBED_REV` in `render.yaml` to bust Docker layer cache after deploy.

### Ops checklist after merge

1. Push Dockerfile + `render.yaml` changes.
2. Render Dashboard → **Manual Deploy** → **Clear build cache**.
3. Confirm build completes under 8 GB (build logs should show multi-stage steps).
4. Verify live: `GET /api/ui-build` reports `ui=ui-v2`, `/` title is `LeanKG`.

---

## Alternatives (if Starter still OOMs)

| Option | Cost | Notes |
|--------|------|-------|
| **Performance build pipeline** | Pro + $25/1K min | 16 CPU / **64 GB RAM** — ops-only, no code change |
| **Prebuilt image from Docker Hub** | Free/cheap | Point Render at `freepeak/leankg:latest` (built via `Dockerfile.rocksdb` on arm64 CI); override CMD to `leankg web` |
| **GitHub Actions → registry** | CI minutes | Build on 7+ GB runners with cache; Render pulls image (no compile on Render) |
| **Drop `embeddings` on Render** | Free | Smaller build, but hosted demo loses `semantic_search` — not recommended |

---

## Local validation (optional)

Simulate Render memory cap:

```bash
docker build --memory=8g --memory-swap=8g -f Dockerfile -t leankg-render-test .
```

Expect ~15–40 min cold build. Success ⇒ Render Starter should pass.

---

## Acceptance

- [ ] Render build green on Starter pipeline (no OOM)
- [ ] `https://leankg.onrender.com/api/ui-build` shows ui-v2 rev
- [ ] `/api/index/status` healthy
- [ ] Homepage title `LeanKG` (not legacy `ui`)
