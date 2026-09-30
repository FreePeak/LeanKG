#!/usr/bin/env python3
"""Retrieval bench: score the L3 fusion against docs/retrieval-label-set.md.

Reports per-arm and fused in-pool / top-k, because "the fused answer was bad"
does not say WHICH arm to fix:

    in-pool 0 on both arms  -> upstream: what text is embedded, or what the
                               indexer extracted
    in-pool on one arm      -> fusion: a weight may fix it
    in-pool, wrong rank     -> fusion, or the model's recall

The label set is parsed from the markdown so the questions, the ground truth
and the code never drift apart.

    go build -o /tmp/leankg ./cmd/leankg
    LEANKG_EMBED_PROVIDER=local LEANKG_EMBED_BASE_URL=http://127.0.0.1:9101/v1 \
    LEANKG_EMBED_MODEL=bge-small-en-v1.5-f16.gguf LEANKG_EMBED_DIMS=384 \
      python3 scripts/retrieval-bench.py [--sweep] [--limit 30]
"""
import argparse
import json
import os
import re
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
LABELS_MD = os.path.join(HERE, "..", "docs", "retrieval-label-set.md")
RRFK = 60
ROW = re.compile(r"^\| (\d+) \| (.+?) \| `([^`]+)` \| (.+?) \|$", re.M)


def load_labels(path=None):
    """Load the label set.

    Default: the LeanKG markdown table (question | ground truth | file).
    With --labels: a JSON file of [qualified_name, question] pairs — the form
    corpus 2 uses, because a second corpus cannot live in this repo's docs.
    """
    if path:
        import json
        rows = json.load(open(path))
        labels = [(i + 1, q.strip(), qn, qn.split("::")[0]) for i, (qn, q) in enumerate(rows)]
        if not labels:
            sys.exit("no labels in " + path)
        return labels
    text = open(LABELS_MD).read()
    labels = [(int(n), q.strip(), qn.strip(), where.strip()) for n, q, qn, where in ROW.findall(text)]
    if not labels:
        sys.exit("no labels parsed from " + LABELS_MD)
    return labels


class Session:
    """One stdio MCP server; the arms come from the engine's own per-arm ranks."""

    def __init__(self, binary, project):
        env = dict(os.environ)
        self.p = subprocess.Popen(
            [binary, "serve", "--stdio", "--project", project, "--memory"],
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
            env=env, text=True, bufsize=1)
        self._id = 0
        self.call("initialize", {"protocolVersion": "2025-06-18", "capabilities": {},
                                 "clientInfo": {"name": "retrieval-bench", "version": "1"}})

    def call(self, method, params):
        self._id += 1
        self.p.stdin.write(json.dumps({"jsonrpc": "2.0", "id": self._id, "method": method, "params": params}) + "\n")
        self.p.stdin.flush()
        while True:
            line = self.p.stdout.readline().strip()
            if not line:
                continue
            try:
                msg = json.loads(line)
            except json.JSONDecodeError:
                continue
            if msg.get("id") == self._id:
                return msg

    def query(self, q, limit, scope=""):
        args = {"query": q, "limit": limit}
        if scope:
            args["args"] = {"scope": scope}
        r = self.call("tools/call", {"name": "query", "arguments": args})
        return json.loads(r["result"]["content"][0]["text"])

    def close(self):
        self.p.terminate()


def arms_of(answer, limit):
    """(vector keys, keyword keys) in each arm's own rank order.

    A binary that predates the fused `ranks` field (origin/main, before wave 1)
    serves the keyword rung alone, so its hits carry no per-arm provenance. That
    is not a zero: score the single list as the keyword arm and leave the vector
    arm empty, which is what that binary actually answers with. Without the
    fallback a before/after A/B against the pre-wave-1 engine reads 0/84 on both
    columns and the regression looks catastrophic when nothing changed.
    """
    vec, kw = [], []
    hits = answer.get("hits", [])[:limit]
    for h in hits:
        rk = h.get("ranks") or {}
        if rk.get("vector"):
            vec.append((rk["vector"], h["qualified_name"]))
        if rk.get("tsvector"):
            kw.append((rk["tsvector"], h["qualified_name"]))
    if not vec and not kw and hits:
        kw = [(i + 1, h["qualified_name"]) for i, h in enumerate(hits)]
    return [k for _, k in sorted(vec)], [k for _, k in sorted(kw)]


def served_order(answer, limit):
    """The order the server actually answered with, whatever arms it has."""
    return [h["qualified_name"] for h in answer.get("hits", [])[:limit]]


def score(order, truth):
    for i, k in enumerate(order):
        if truth in k:
            return i + 1
    return None


def rrf(vector, keyword, wv=1.0, wk=1.0):
    total, first = {}, {}
    for i, k in enumerate(vector):
        total[k] = total.get(k, 0.0) + wv / (RRFK + i + 1)
        first.setdefault(k, i)
    for i, k in enumerate(keyword):
        total[k] = total.get(k, 0.0) + wk / (RRFK + i + 1)
        first.setdefault(k, i)
    return sorted(total, key=lambda k: (-total[k], first[k]))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--binary", default="/tmp/leankg")
    ap.add_argument("--project", default=".")
    ap.add_argument("--limit", type=int, default=30, help="per-arm depth fetched and scored")
    ap.add_argument("--sweep", action="store_true", help="score every (vector, keyword) weight pair")
    ap.add_argument("--scope", default="", help="args.scope passed to every query (code|prod|all)")
    ap.add_argument("--labels", default="", help="JSON label file [[qualified_name, question], ...]; default is the LeanKG markdown table")
    args = ap.parse_args()

    labels = load_labels(args.labels or None)
    s = Session(args.binary, args.project)
    data = []
    for n, q, truth, where in labels:
        a = s.query(q, args.limit, args.scope)
        vec, kw = arms_of(a, args.limit)
        data.append((n, q, truth, vec, kw, served_order(a, args.limit)))
    s.close()

    n = len(data)
    print(f"{n} labels from {args.labels or 'docs/retrieval-label-set.md'}, per-arm depth {args.limit}, "
          f"scope={args.scope or 'full corpus'}\n")

    def tally(order_fn):
        out = {}
        for tag in ("vector", "keyword", "fused"):
            inpool = t1 = t3 = t10 = 0
            for _, _, truth, vec, kw, served in data:
                if tag == "fused" and not vec:
                    # A pre-wave-1 engine serves one arm; its answer IS that
                    # arm's ranking, and fusing it with itself would invent
                    # agreement the server never expressed.
                    order = served
                elif tag == "fused":
                    order = order_fn(tag, vec, kw)
                else:
                    order = order_fn(tag, vec, kw)
                r = score(order, truth)
                if r is None:
                    continue
                inpool += 1
                t1 += r == 1
                t3 += r <= 3
                t10 += r <= 10
            out[tag] = (inpool, t1, t3, t10)
        return out

    res = tally(lambda tag, v, k: v if tag == "vector" else k if tag == "keyword" else rrf(v, k))
    # When only one arm exists (a pre-wave-1 engine), the fused order IS that
    # arm's order — fusing a list with itself would invent agreement.
    print(f"{'arm':<10} {'in-pool':>8} {'top-1':>8} {'top-3':>8} {'top-10':>8}")
    print("-" * 48)
    for tag in ("vector", "keyword", "fused"):
        p, a, b, c = res[tag]
        print(f"{tag:<10} {p:>4}/{n:<3} {a:>4}/{n:<3} {b:>4}/{n:<3} {c:>4}/{n:<3}")

    print(f"\n{'#':<3} {'in vec':>7} {'in kw':>6} {'fused rank':>12}  question")
    for num, q, truth, vec, kw, served in data:
        inv, ink = score(vec, truth), score(kw, truth)
        r = score(rrf(vec, kw) if vec else served, truth)
        print(f"{num:<3} {str(inv or '-'):>7} {str(ink or '-'):>6} {str(r or '-'):>12}  {q[:46]}")

    if args.sweep:
        print(f"\n{'weights (vec,kw)':<18} {'top-1':>8} {'top-3':>8} {'top-10':>8}")
        print("-" * 46)
        for wv, kw in [(1, 1), (1, 2), (2, 1), (1, 3), (3, 1), (1.5, 1), (1, 1.5), (2, 2)]:
            t1 = t3 = t10 = 0
            for _, _, truth, vec, kwv, _served in data:
                r = score(rrf(vec, kwv, wv, kw), truth) if vec else score(_served, truth)
                if r is None:
                    continue
                t1 += r == 1
                t3 += r <= 3
                t10 += r <= 10
            print(f"{f'{wv},{kw}':<18} {t1:>4}/{n:<3} {t3:>4}/{n:<3} {t10:>4}/{n:<3}")

    missing = [(num, q, truth) for num, q, truth, vec, kw, served in data
               if score(vec, truth) is None and score(kw, truth) is None]
    if missing:
        print(f"\n{len(missing)} label(s) in NEITHER arm — these measure the indexer or the embedded text, not the fusion:")
        for num, q, truth in missing:
            print(f"  #{num:<3} {truth:<52} {q[:40]}")


if __name__ == "__main__":
    main()
