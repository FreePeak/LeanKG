# /// script
# requires-python = ">=3.10"
# dependencies = [
#     "sentence-transformers==6.1.0",
#     "transformers==5.19.0",
#     "torch==2.14.1",
# ]
#
# [tool.uv]
# exclude-newer = "2026-10-07T00:00:00Z"
# ///
"""Embed a LeanKG export on a Hugging Face Job (EmbeddingGemma 2, offsite bulk path).

Input is the NDJSON `leankg-embed export` writes:
    {"qualified_name": ..., "content_hash": ..., "text": ...}
Each `text` already carries the model's document prefix and the live run's
length cap, so it is embedded EXACTLY as given: no prompt, no prompt_name.

Output is the NDJSON `leankg-embed import` reads, one line per input line, in order:
    {"qualified_name": ..., "content_hash": ..., "vec": [768 floats]}
content_hash is copied through when the input line has one. There is no header.

Run on a Hugging Face Job with the model and work files in a bucket mounted at /data:

    hf jobs uv run scripts/hf_embed_job.py \
        --model-dir /data/<model path> \
        --in /data/<work>/export.ndjson \
        --out /data/<work>/vectors.ndjson

The pins follow the model card's install section (`pip install -U sentence-transformers
transformers`) with versions that support the bucket's `embedding_gemma2` config:
transformers 5.19.0 registers that model type (5.18.0 does not), and
sentence-transformers 6.1.0 reads the bucket's modules.json layout.
"""

from __future__ import annotations

import argparse
import json
import math
import sys
from pathlib import Path
from typing import Callable

Encoder = Callable[[list[str]], list[list[float]]]


class InputError(ValueError):
    """A malformed export line; the message names its line number."""


def parse_line(raw: str, line_no: int) -> tuple[str, str | None, str]:
    """Return (qualified_name, content_hash or None, text) for one export line."""
    try:
        rec = json.loads(raw)
    except json.JSONDecodeError as e:
        raise InputError(f"line {line_no}: malformed JSON: {e.msg}") from None
    if not isinstance(rec, dict):
        raise InputError(f"line {line_no}: expected a JSON object")
    qn = rec.get("qualified_name")
    if not isinstance(qn, str) or not qn:
        raise InputError(f"line {line_no}: missing qualified_name")
    text = rec.get("text")
    if not isinstance(text, str):
        raise InputError(f"line {line_no}: missing text")
    content_hash = rec.get("content_hash")
    if content_hash is not None and not isinstance(content_hash, str):
        raise InputError(f"line {line_no}: content_hash must be a string")
    return qn, content_hash or None, text


def run(in_path: Path, out_path: Path, encode: Encoder, batch_size: int) -> int:
    """Stream in_path through encode in batches and write vector lines to out_path.

    Returns the number of vector lines written. Raises InputError on a malformed
    line, and ValueError when a vector is not finite (the card warns float16
    overflows to NaN; the bad line is named and nothing from its batch is written).
    """
    written = 0
    pending: list[tuple[str, str | None, str]] = []

    def flush(dst) -> None:
        nonlocal written
        vecs = encode([text for _, _, text in pending])
        if len(vecs) != len(pending):
            raise RuntimeError(f"encoder returned {len(vecs)} vectors for {len(pending)} texts")
        for (qn, content_hash, _), vec in zip(pending, vecs):
            if not all(math.isfinite(x) for x in vec):
                raise ValueError(f"{qn}: vector contains NaN/Inf")
            row = {"qualified_name": qn}
            if content_hash is not None:
                row["content_hash"] = content_hash
            row["vec"] = vec
            dst.write(json.dumps(row) + "\n")
        written += len(pending)
        pending.clear()
        print(f"hf_embed_job: embedded {written} lines", file=sys.stderr, flush=True)

    with open(in_path, encoding="utf-8") as src, open(out_path, "w", encoding="utf-8") as dst:
        for line_no, raw in enumerate(src, start=1):
            if not raw.strip():
                continue
            pending.append(parse_line(raw, line_no))
            if len(pending) >= batch_size:
                flush(dst)
        if pending:
            flush(dst)
    return written


def load_encoder(model_dir: str, dtype_name: str, max_seq_length: int) -> Encoder:
    """Load the SentenceTransformer once and return the batch encoder.

    The heavy imports live here so the I/O above imports without torch.
    """
    import torch
    from sentence_transformers import SentenceTransformer

    dtype = {"bfloat16": torch.bfloat16, "float32": torch.float32}[dtype_name]
    # Disable saved prompts: the texts already carry the document prefix, and
    # ST would otherwise apply a default prompt on top of it.
    model = SentenceTransformer(
        model_dir,
        model_kwargs={"dtype": dtype},
        prompts={"query": "", "document": ""},
    )
    model.max_seq_length = max_seq_length
    print(f"hf_embed_job: loaded {model_dir} dtype={dtype_name} max_seq_length={max_seq_length}",
          file=sys.stderr, flush=True)

    def encode(texts: list[str]) -> list[list[float]]:
        vecs = model.encode(
            texts,
            batch_size=len(texts),
            normalize_embeddings=True,
            convert_to_tensor=True,
        )
        return vecs.float().cpu().tolist()

    return encode


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description="Embed a LeanKG export NDJSON with EmbeddingGemma 2.")
    ap.add_argument("--model-dir", required=True, help="SentenceTransformer directory (bucket path)")
    ap.add_argument("--in", dest="in_path", required=True, type=Path, help="export NDJSON")
    ap.add_argument("--out", dest="out_path", required=True, type=Path, help="vector NDJSON to write")
    ap.add_argument("--dtype", choices=["bfloat16", "float32"], default="bfloat16",
                    help="inference dtype; float16 is not allowed (overflows on this model)")
    ap.add_argument("--batch-size", type=int, default=64)
    ap.add_argument("--max-seq-length", type=int, default=8192)
    args = ap.parse_args(argv)
    if args.batch_size < 1:
        ap.error("--batch-size must be at least 1")

    encode = load_encoder(args.model_dir, args.dtype, args.max_seq_length)
    try:
        n = run(args.in_path, args.out_path, encode, args.batch_size)
    except (InputError, ValueError) as e:
        print(f"hf_embed_job: {e}", file=sys.stderr)
        return 1
    print(f"hf_embed_job: wrote {n} vectors to {args.out_path}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
