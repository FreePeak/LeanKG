#!/usr/bin/env python3
"""
Batch-embed a LeanKG embed-export file into an embed-import file.

This is the offsite half of LeanKG's isolated embedding workflow:

    host:  leankg embed --dry-run --export-file out/export.jsonl
    gpu:   python scripts/embed_batch.py --in out/export.jsonl --out out/import.jsonl
    host:  leankg embed --import out/import.jsonl

It reads the NDJSON produced by `leankg embed --dry-run`, runs each row's
`blob` through a sentence-transformers model (CUDA if available — ideal for a
Colab T4 GPU), and writes an NDJSON import file whose rows map 1:1 back to the
export rows by `qualified_name`.

Correctness contract (the "careful" points):
  1. ONE ROW PER QUERY — every input row produces exactly one output row, in
     the same order, keyed by `qualified_name`. We never merge, reorder, or
     drop rows. The input `i` sequence must be gap-free (0,1,2,...); we assert
     it and echo the same `i` out. `qualified_name` + `content_hash` are
     echoed unchanged so `leankg embed --import` can join them back to the
     graph and stamp `embedding_state` correctly.
  2. RESUME — pass `--checkpoint <path>`. Completed `qualified_name`s are
     appended to the checkpoint after every flushed batch; re-running the
     script loads the checkpoint, skips done rows, and appends the rest.
     Output is appended to `--out` (never rewritten), so a kill mid-run never
     corrupts already-written rows.

Backend: sentence-transformers only (per the offsite design choice). The
default model `BAAI/bge-small-en-v1.5` matches LeanKG's local ONNX default and
produces the same 384-dim vectors.

Dependencies: `pip install torch sentence-transformers`

Manual smoke test (needs the deps + a model download):
    python scripts/embed_batch.py --in export.jsonl --out import.jsonl \\
        --batch-size 8 --limit 16
"""

from __future__ import annotations

import argparse
import json
import os
import sys
from pathlib import Path


# ---------------------------------------------------------------------------
# NDJSON streaming helpers
# ---------------------------------------------------------------------------

def iter_export_rows(in_path: Path):
    """Yield (i, qualified_name, blob, content_hash) for each data row.

    Skips blank lines and any line whose JSON carries a truthy `_meta` field
    (the leading meta record written by `leankg embed --dry-run`). Asserts the
    `i` field is gap-free 0,1,2,... across every data row seen.
    """
    expected_i = 0
    with in_path.open("r", encoding="utf-8") as fh:
        for line_no, raw in enumerate(fh, start=1):
            line = raw.strip()
            if not line:
                continue
            obj = json.loads(line)  # raises on malformed input — intentional
            if obj.get("_meta"):
                continue
            i = obj["i"]
            if i != expected_i:
                raise SystemExit(
                    f"{in_path}:{line_no}: export row `i` is {i}, "
                    f"expected {expected_i} (gap detected — export must be "
                    f"gap-free 0..N). Re-run `leankg embed --dry-run`."
                )
            expected_i += 1
            yield i, obj["qualified_name"], obj["blob"], obj["content_hash"]


def read_export_meta(in_path: Path) -> dict:
    """Return the leading `_meta` object, or {} if absent."""
    with in_path.open("r", encoding="utf-8") as fh:
        for raw in fh:
            line = raw.strip()
            if not line:
                continue
            obj = json.loads(line)
            if obj.get("_meta"):
                return obj
            # First non-blank line is a data row → no meta present.
            return {}
    return {}


def load_checkpoint(ckpt_path: Path | None) -> set[str]:
    """Read the set of already-completed qualified_names from the checkpoint."""
    if ckpt_path is None or not ckpt_path.exists():
        return set()
    done: set[str] = set()
    with ckpt_path.open("r", encoding="utf-8") as fh:
        for raw in fh:
            line = raw.strip()
            if line:
                done.add(line)
    return done


# ---------------------------------------------------------------------------
# main
# ---------------------------------------------------------------------------

def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(
        description="Batch-embed a LeanKG embed-export file (offsite, GPU-friendly).",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=__doc__,
    )
    ap.add_argument("--in", dest="in_path", required=True, type=Path,
                    help="Input NDJSON from `leankg embed --dry-run`.")
    ap.add_argument("--out", dest="out_path", required=True, type=Path,
                    help="Output NDJSON for `leankg embed --import`.")
    ap.add_argument("--model", default="BAAI/bge-small-en-v1.5",
                    help="sentence-transformers model id (default matches "
                         "LeanKG's local ONNX default, 384-d).")
    ap.add_argument("--batch-size", type=int, default=64,
                    help="Texts per forward pass (default 64).")
    ap.add_argument("--device", default=None,
                    help="torch device override: 'cuda', 'cpu', 'cuda:1'... "
                         "(default: auto — CUDA if available).")
    ap.add_argument("--checkpoint", type=Path, default=None,
                    help="Optional resume checkpoint file. Completed "
                         "qualified_names are appended per batch; re-running "
                         "skips them. Output is appended, never rewritten.")
    ap.add_argument("--limit", type=int, default=None,
                    help="Stop after N data rows (testing).")
    ap.add_argument("--normalize", action="store_true", default=True,
                    help="L2-normalize output vectors (default on; matches "
                         "BGE + LeanKG cosine search).")
    args = ap.parse_args(argv)

    import torch  # local import so --help works without torch installed
    from sentence_transformers import SentenceTransformer

    if not args.in_path.exists():
        raise SystemExit(f"input not found: {args.in_path}")

    meta = read_export_meta(args.in_path)
    export_vec_dim = meta.get("vec_dim")
    item_count = meta.get("item_count")
    device = args.device or ("cuda" if torch.cuda.is_available() else "cpu")

    print(f"[embed_batch] device={device} model={args.model} "
          f"batch_size={args.batch_size}", file=sys.stderr)
    model = SentenceTransformer(args.model, device=device)
    model_dim = model.get_sentence_embedding_dimension()
    print(f"[embed_batch] model dim={model_dim}", file=sys.stderr)
    if export_vec_dim is not None and export_vec_dim != model_dim:
        raise SystemExit(
            f"dim mismatch: export file vec_dim={export_vec_dim} but model "
            f"'{args.model}' produces {model_dim}. Re-run `leankg embed "
            f"--dry-run` with LEANKG_EMBED_DIM={model_dim} or pick a matching "
            f"--model."
        )

    # Resume state.
    done = load_checkpoint(args.checkpoint)
    if done:
        print(f"[embed_batch] resuming: {len(done)} rows already in checkpoint",
              file=sys.stderr)

    # Output: append if it already exists (resume), else create + write meta.
    out_exists = args.out_path.exists() and args.out_path.stat().st_size > 0
    out_fh = args.out_path.open("a", encoding="utf-8")
    if not out_exists:
        import time
        out_meta = {
            "_meta": True,
            "format": "leankg-embed-import",
            "version": 1,
            "vec_dim": model_dim,
            "model": args.model,
            "source_export": args.in_path.name,
            "created_at": str(int(time.time())),
        }
        out_fh.write(json.dumps(out_meta, ensure_ascii=False) + "\n")
        out_fh.flush()

    ckpt_fh = (
        args.checkpoint.open("a", encoding="utf-8")
        if args.checkpoint is not None
        else None
    )

    # Stream rows, batch-embed, flush.
    batch_i: list[int] = []
    batch_qn: list[str] = []
    batch_blob: list[str] = []
    batch_hash: list[str] = []
    written = 0
    skipped_resume = 0
    seen = 0
    total = item_count if isinstance(item_count, int) else None

    def flush_batch() -> None:
        nonlocal written
        if not batch_blob:
            return
        vecs = model.encode(
            batch_blob,
            batch_size=len(batch_blob),
            show_progress_bar=False,
            normalize_embeddings=args.normalize,
            convert_to_numpy=True,
        )
        for k in range(len(batch_blob)):
            row = {
                "i": batch_i[k],
                "qualified_name": batch_qn[k],
                "vector": [float(x) for x in vecs[k].tolist()],
                "content_hash": batch_hash[k],
            }
            out_fh.write(json.dumps(row, ensure_ascii=False) + "\n")
        out_fh.flush()
        if ckpt_fh is not None:
            for qn in batch_qn:
                ckpt_fh.write(qn + "\n")
            ckpt_fh.flush()
        written += len(batch_blob)
        batch_i.clear()
        batch_qn.clear()
        batch_blob.clear()
        batch_hash.clear()

    try:
        for i, qn, blob, content_hash in iter_export_rows(args.in_path):
            seen += 1
            if args.limit is not None and seen > args.limit:
                break
            if qn in done:
                skipped_resume += 1
                continue
            batch_i.append(i)
            batch_qn.append(qn)
            batch_blob.append(blob)
            batch_hash.append(content_hash)
            if len(batch_blob) >= args.batch_size:
                flush_batch()
                prog = f"{written}/{total}" if total else f"{written}"
                print(f"[embed_batch] flushed batch; written={prog} "
                      f"(skipped_resume={skipped_resume})", file=sys.stderr)
        flush_batch()
    finally:
        out_fh.close()
        if ckpt_fh is not None:
            ckpt_fh.close()

    print(f"[embed_batch] done: written={written} skipped_resume="
          f"{skipped_resume} seen={seen} dim={model_dim} -> {args.out_path}",
          file=sys.stderr)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
