# Embeddings

LeanKG embeds through one provider, chosen by `LEANKG_EMBED_PROVIDER`: `local` (a
llama.cpp sidecar or an attached endpoint, the default), `openai` (any OpenAI-compatible
API) or `deterministic` (tests only). Every variable is listed with an example in
[`.env.example`](../.env.example); the `leankg-embed` usage text names the same set.
The catalog rows (model id, pinned revision, dimensions, query and document prefixes)
live in `internal/embed/catalog.go`.

## Embedding providers

LeanKG embeds through one provider, chosen by `LEANKG_EMBED_PROVIDER`: `local` (a
llama.cpp sidecar or an attached endpoint, the default), `openai` (any OpenAI-compatible
API) or `deterministic` (tests only). Every vector writer is `ModelStamp`-guarded, so a
model change fails loudly instead of mixing vector spaces.

## EmbeddingGemma 2 with a Hugging Face Storage Bucket

`embeddinggemma-2` (Google, 768 dimensions, Matryoshka truncation to 512/256/128) is served
by llama.cpp. Bulk embedding is too slow on a laptop, so it is split:

- **Bulk (on a Hugging Face Job):** `scripts/hf-embed.sh` exports the pending texts with
  `leankg-embed export`, uploads them to your private Storage Bucket, runs
  `scripts/hf_embed_job.py` as an `hf jobs uv run` job that loads the sentence-transformers
  model from the bucket, downloads the vectors and imports them with `leankg-embed import`.
  The import stamps the collection exactly as the local provider would for the same
  `LEANKG_EMBED_*` environment (catalog revision, width, prefixes), so the next
  `leankg-embed run` needs no `full` rebuild. Each vector line may be the native 768-d width
  (reduced to the configured width) or the configured width; the export's `content_hash` is
  recorded, so an element edited after export is re-embedded by the next run.
- **Queries and hook backlogs (local):** the local llama.cpp sidecar serves query vectors and
  the small backlogs that `leankg index --hook` embeds. The session hook never starts a job.

Prerequisites:

- llama.cpp `llama-server` release b11454 or newer.
- The BF16 GGUF from [`ggml-org/embeddinggemma-2-GGUF`](https://huggingface.co/ggml-org/embeddinggemma-2-GGUF)
  for the local sidecar.
- The `hf` CLI, logged in (`hf auth login`), with HF Jobs credits (a job needs a positive balance).
- The sentence-transformers model files of
  [`google/embeddinggemma-2`](https://huggingface.co/google/embeddinggemma-2) at revision
  `914f7f89142e33e77833254d9c9b90c3cef7303b`, uploaded to your bucket
  (`<your-org>/<your-bucket>`).

Local sidecar (the query side):

```bash
llama-server -m /path/to/embeddinggemma-2-BF16.gguf --embeddings --pooling mean \
  -c 8192 -b 8192 -ub 8192 --parallel 1
```

Environment (placeholders only; the full list with comments is in `.env.example`):

```bash
export LEANKG_EMBED_PROVIDER=local
export LEANKG_EMBED_MODEL=embeddinggemma-2
export LEANKG_EMBED_DIMS=768                 # or 512, 256, 128 (re-normalized truncation)
export LEANKG_HF_BUCKET=<your-org>/<your-bucket>
export LEANKG_HF_MODEL_DIR=                  # model path inside the bucket; empty = root
export LEANKG_HF_WORK_DIR=leankg             # default
export LEANKG_HF_JOB_FLAVOR=l4x1             # default
export LEANKG_HF_JOB_TIMEOUT=30m             # default
export LEANKG_HF_MAX_USD=0.50                # default: budget for one run's worst case
export LEANKG_EMBED_BASE_URL=http://127.0.0.1:8080/v1  # running sidecar; export reads its n_ctx
export HF_TOKEN=                             # empty: use `hf auth login` instead
```

Commands (`scripts/hf-embed.sh [--dry-run] [--yes] [--limit N] [--keep] <project-dir>`):

```bash
scripts/hf-embed.sh <project-dir>                    # estimate and guards only; exits 1, uploads nothing
scripts/hf-embed.sh --dry-run <project-dir>          # same plan plus the exact commands; exits 0, starts nothing
scripts/hf-embed.sh --yes --limit 200 <project-dir>  # first run: export 200 elements, measure the rate
scripts/hf-embed.sh --yes <project-dir>              # export, upload, run the HF Job, download, import
```

`--keep` leaves the run directory in the bucket after a successful import; by default it is
removed. A failed job always keeps the run directory and imports nothing. `--dry-run` runs the
same guards, so a plan that would be refused exits 1 with the reason.

**Cost and safety.** Hugging Face Jobs bill per minute, and a personal account cannot set a
spending cap, so the script enforces one. The worst case of a run is the job timeout times the
hourly price of the flavor: `l4x1` at $0.80/h with the default 30m timeout is at most $0.40. The
script refuses to upload when that worst case exceeds `LEANKG_HF_MAX_USD` (default 0.50), and when
its own estimate (lines / `LEANKG_HF_EST_RATE` plus `LEANKG_HF_EST_OVERHEAD_MIN` for startup and
dependency install) exceeds the timeout; the refusal names the variable to raise. Dependency
installation inside the job is billed as well. Start with `--limit 200` to measure throughput,
then set `LEANKG_HF_EST_RATE` to the measured texts per second. Only send code you are permitted
to upload to a third-party service: the export is stored in your bucket and processed by a
Hugging Face Job.

Queries after the import use the local sidecar with the same model, so the query prefix and
the stamp must match the bulk run; `leankg-embed status` shows the stamp that the collection
was written under.