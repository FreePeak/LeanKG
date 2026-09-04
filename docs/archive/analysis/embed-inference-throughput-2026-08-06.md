# Embed Inference Throughput Investigation (2026-08-06)

## Conclusion: hardware-bound, ~60-80 v/s ceiling on this Mac

Pure-inference benchmark (`examples/bench_embed_infer`, no DB) on this 10-core
Apple Silicon host — the same CPU the Docker containers share:

| Model | workers | intra_threads | batch | rate v/s |
|-------|---------|---------------|-------|----------|
| bge-q (BGE-small int8) | 4 | 1 | 128 | 58.5 |
| bge-q | 2 | 4 | 128 | **77.8** |
| bge-q | 2 | 8 | 128 | 63.9 |
| minilm (all-MiniLM-L6-v2) | 4 | 1 | 128 | 48.4 |
| minilm | 2 | 4 | 128 | 69.3 |
| minilm | 1 | 8 | 128 | 65.8 |
| bge-q | 2 | 4 | 512 | 30.5 |

## Key findings

1. **Model choice doesn't matter** — bge-q int8 and minilm both plateau ~70 v/s.
   MiniLM-L6 is FP32; the int8 quantization of BGE is the real speed lever,
   not model size. The user's assumption "Qdrant/all-MiniLM-L6-v2 is faster"
   is **wrong on this hardware** (48 vs 58 v/s at the same config).
2. **`intra_threads=4` beats `=1`** (77.8 vs 58.5 v/s) — the hardcoded
   `intra_threads=1` default (models.rs comment claiming "max throughput on
   10c") undershoots. `LEANKG_EMBED_DIRECT_INTRA=4` + 2 workers is the best
   measured config.
3. **Bigger batch hurts** (512: 30 v/s) — ONNX batch > ~256 degrades.
4. The models.rs:250 comment claiming "~600 vec/sec (4 workers, batch=128,
   intra_threads=1)" does **not** reproduce on this host (58 v/s). Either the
   comment was from a different CPU or stale.

## For later investigation

- **Why 600 v/s was claimed vs 58 measured**: verify the original benchmark
  conditions (CPU model, ORT version, batch packing). The DirectEmbedder was
  built to beat fastembed's ~120 v/s; 58 v/s is *below* that — suspicious.
- **ORT graph optimization**: check `session.add_pre_optimized_models` /
  graph-level optimizations (`ORT_ENABLE_ALL`). The int8 model may not be
  using a fused graph.
- **Alternative executors**: `onnxruntime` on this M-series may benefit from
  CoreML EP (`ort` + `coreml`). Not wired in the crate today.
- **Tokenize+build overhead**: profile the non-ONNX portion of `embed()`
  (tokenizer, flat-array build) — at batch 128, per-batch overhead could
  dominate. `bench_embed_infer` includes it, so the 77.8 v/s already
  accounts for it, but splitting the phases would show the real ONNX-only
  rate.
- **GPU/MPS**: Apple MPS (Metal) for ONNX is a potential 3-5x. Requires
  testing `ort` with the MPS execution provider.

## Decision (per user 2026-08-06)

Hardware cannot reach 200-500 v/s with the current stack. Ship the
function-only embed scope (the incremental dirty-collect fix already treats
never-state'd `function` elements as dirty). Revisit inference speed when
MPS/GPU or a different host is available.
