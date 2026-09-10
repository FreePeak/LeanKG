# LeanKG A/B Benchmark Report — Rust 0.30.0 vs Go engine (v4.6.0)

**Executed:** 2026-09-10, macOS arm64 (Apple M2 Pro), repository-mandated bounded build.
**Provenance:**
- **Rust arm:** release binary `leankg 0.30.0` built fresh from pre-removal commit `4b890a90` (`CARGO_BUILD_JOBS=2 cargo build --release --bin leankg`, 4m09s), binary at `~/.cache/cargo-target/leankg-target/release/leankg` (152 MB).
- **Go arm:** `go/` @ `df18c2d1`, `go build ./cmd/leankg` (CGO_ENABLED=0), binary ~12 MB.
- **Corpus:** 100 synthetic Go files (`/tmp/ab-corpus`, 3 symbols each: 2 funcs + 1 type), fresh store per cold-index run, 5/3/3 timed runs (L1/impact/index), medians reported.
- **Method:** process wall-time (CLI invocation), which includes binary startup for both arms — the in-process microbenchmarks live in `go/benchmark/ab/ab_test.go` and are reported separately below.

## Results

| Metric | Rust 0.30.0 | Go v4.6.0 | Ratio |
|---|---|---|---|
| Cold index, 100 files (process) | 0.10 s | 0.11 s | ≈1.0 |
| L1 exact query (process) | 53 ms | 50 ms | ≈1.0 |
| Impact radius, depth 2 (process) | 101 ms | 51 ms | Go 2.0× |
| L1 exact, in-process (go bench, warm) | not measured¹ | 137 µs | — |
| L2 fuzzy FTS5, in-process | not measured¹ | 375 µs | — |
| Vector search 1k×384 top-10, in-process | not measured¹ | 4.4 ms (exact cosine scan²) | — |
| L3 semantic end-to-end | not measured³ | not measured³ | — |
| Binary size | 152 MB | ~12 MB | Go 12.7× smaller |
| Index extraction yield (same corpus) | 400–503 elements | ~300 elements | Rust extracts consts/extra kinds⁴ |

**Footnotes:**
1. In-process Rust microbenches would require reviving the Rust bench harness (cargo benches removed with the Rust tree; historical cross-tool results live in `benchmark/results/`, July-era, different corpus — not comparable, marked *not measured* per the no-extrapolation rule).
2. Go L3 is an **exact O(n) cosine scan** (`internal/store/vectors.go` ponytail ceiling); Rust sqlite/pgvector used HNSW ANN. Cells must never be labeled "ANN" for Go.
3. L3 end-to-end requires the local ONNX model download on the Rust arm and a provider on the Go arm — out of scope for this report; both engines' L3 paths are covered by unit/live tests instead.
4. Extraction yield difference is the documented W2 ceiling (Go regex extractor until tree-sitter; `go/internal/index` package doc). Rust counts include const/var kinds the Go extractor does not emit.

## Raw runs

```
rust index (3 cold runs):  0.11 0.10 0.10
go   index (3 cold runs):  0.81 0.11 0.11   (first run includes filesystem cold-cache)
rust L1   (5 runs):        0.053 0.052 0.054 0.053 0.051
go   L1   (5 runs):        0.050 0.051 0.049 0.051 0.049
rust impact (3 runs):      0.101 0.100 0.103
go   impact (3 runs):      0.050 0.052 0.051
```

## How to reproduce

- Go arm: `make go-build && go/benchmark/ab` (in-process) + `leankg index/query` on any corpus (process).
- Rust arm: `git worktree add /tmp/rust-arm 4b890a90 && cd /tmp/rust-arm && CARGO_BUILD_JOBS=2 cargo build --release --bin leankg` (commit predates the Rust removal in `f7624143`; the baseline stays recoverable from git history).
