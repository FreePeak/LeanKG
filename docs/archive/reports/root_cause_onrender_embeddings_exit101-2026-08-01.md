# Root Cause Analysis: OnRender deploy exit 101 on embeddings build (2026-08-01)

**Date:** 2026-08-01  
**Service:** `leankg` on Render (`render.yaml` → `./Dockerfile`, Free / Starter pipeline)  
**Failing step:**

```text
error: failed to solve: process "/bin/sh -c cargo build --release --features embeddings
    && strip target/release/leankg
    && cp target/release/leankg /usr/local/bin/leankg" did not complete successfully: exit code: 101
```

**Trigger window:** after `v0.19.27` / Swift+ObjC language support (#158) landed on `main`.

---

## Symptom

Render Docker build fails at the Rust builder stage. BuildKit surfaces only the shell exit code (**101** = cargo generic failure). Live `leankg.onrender.com` stays on the last successful image (stale UI / binary).

---

## Evidence

| Item | Value |
|------|-------|
| Render Starter pipeline | **2 CPU, 8 GB RAM** ([docs](https://render.com/docs/build-pipeline)) |
| Dockerfile RUN | `cargo build --release --features embeddings` |
| Prior OOM RCA | [`root_cause_render_build_oom_2026-07-21.md`](root_cause_render_build_oom_2026-07-21.md) — same step |
| Recent code delta | #158 adds `tree-sitter-swift` + `tree-sitter-objc` (extra native C parsers in the link unit) |
| Embeddings → OpenSSL | `embeddings` → `fastembed` → `hf-hub` → `native-tls` → `openssl-sys` |
| Dockerfile apt packages (before fix) | `clang` + `libclang-dev` only — **no `libssl-dev` / `pkg-config`** |
| Local reproduce | `cargo check --features embeddings` fails without `libssl-dev`; succeeds after installing it |

---

## Root causes (ranked)

### RC-1 — Missing OpenSSL headers in builder image (likely compile abort → exit 101)

`openssl-sys` needs `libssl-dev` + `pkg-config`. The Render `Dockerfile` never installed them. Local cloud VM also failed `embeddings` check until `libssl-dev` was added. Cargo reports this as a build-script error and exits **101** — matches the Render log line.

Cached layers from older images may have masked this until a full rebuild (version bump / Swift crates / cache clear).

### RC-2 — Compile RSS cliff after Swift/ObjC (#158) (contributing / alternate)

Same memory cliff as Jul-21 OOM RCA: `ort` + RocksDB + many `tree-sitter-*` under `--features embeddings`. Two new grammars increase peak RSS. On Starter (8 GB), OOM often surfaces as Docker RUN failure (exit 137 or cargo 101 depending on how the cgroup kill is reported). Mitigations already present (`CARGO_BUILD_JOBS=1`, `CARGO_PROFILE_RELEASE_LTO=false`) may no longer be enough alone.

### RC-3 — Stale `UI_EMBED_REV` / layer cache (ops)

`render.yaml` still pins `UI_EMBED_REV=2026-07-21-onrender-rca4`. Without bumping + **Clear build cache**, failed or partial rebuilds leave production on the last green image.

---

## Logic flow

```text
push main (v0.19.27 / #158)
  → Render Docker build (Starter: 8 GB)
  → ui stage (node) OK
  → builder: cargo build --release --features embeddings
       → hf-hub / native-tls needs openssl headers  ──┐
       → OR peak RSS > 8 GB with ort+swift+objc     ──┤→ exit 101
  → deploy fails → onrender stays on old image
```

---

## Fix (this PR)

1. **Dockerfile builder apt:** install `pkg-config` + `libssl-dev` (+ keep `clang` / `libclang-dev`).
2. **Extra memory guards:** `CARGO_INCREMENTAL=0`, `RUSTFLAGS=-C debuginfo=0`.
3. **Bump `UI_EMBED_REV`** in `Dockerfile` + `render.yaml` to bust cache.
4. Align `Dockerfile.rocksdb` apt line (Hub builds hit the same openssl-sys path).

### Ops checklist after merge

1. Render Dashboard → **Manual Deploy** → **Clear build cache**.
2. Confirm builder log shows openssl packages + completed `cargo build --features embeddings`.
3. Verify live: `GET /api/ui-build` and `/api/index/status`.

---

## Follow-ups (plan)

| # | Priority | Follow-up | Why |
|--:|----------|-----------|-----|
| F1 | **P0 / ops** | Land Dockerfile openssl + memory guards; clear Render cache; confirm green deploy | Unblocks `leankg.onrender.com` |
| F2 | P1 | CI job: `docker build` (or `cargo build --release --features embeddings` in slim Debian) on PRs that touch `Dockerfile` / `Cargo.toml` / `Cargo.lock` | Catch exit-101 before Render |
| F3 | P1 | Optional: GitHub Actions → GHCR/Hub image; Render pulls prebuilt (no compile on 8 GB) | Hardens against next tree-sitter / ort bump |
| F4 | P2 | Performance build pipeline (16 CPU / 64 GB) if Starter still OOMs after F1 | Paid escape hatch from Jul-21 RCA |
| F5 | P2 | Wave 4 single-repo expand (`US-MG-02` / `FR-MG-03`) — unchanged product queue | Not blocked by OnRender, but next P1 product item |

---

## Acceptance

- [ ] Render build green after cache clear
- [ ] `https://leankg.onrender.com/api/ui-build` shows new rev
- [ ] `/api/index/status` healthy
- [ ] Homepage serves current ui-v2 (incl. NL Query FAB assets when this branch is on main)
