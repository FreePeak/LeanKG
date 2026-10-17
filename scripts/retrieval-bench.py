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
    LEANKG_EMBED_MODEL=bge-small-en-v1.5-384 LEANKG_EMBED_DIMS=384 \
      python3 scripts/retrieval-bench.py --project <the indexed checkout> [--sweep] [--limit 30]

LEANKG_EMBED_MODEL must be a CATALOG id (`bge-small-en-v1.5-384`, the pinned
384-d entry in internal/embed/catalog.go), NOT the .gguf file name the sidecar
serves: an id the catalog does not know gets a different model_id and revision,
the stamp lookup finds no collection, and every query silently degrades to the
keyword rung — which reads as "the vector arm found nothing" instead of "the
vector arm never ran". The bench stops on that instead of reporting it.

--project must be the checkout the store was indexed from, not the worktree this
script lives in: a store with no elements answers everything with an empty list.
"""
import argparse
import json
import os
import re
import subprocess
import sys

CATALOG_MODEL_ID = "bge-small-en-v1.5-384"

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
        self.binary = binary
        self.p = subprocess.Popen(
            [binary, "serve", "--stdio", "--project", project, "--memory"],
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
            env=dict(os.environ), text=True, bufsize=1)
        self._id = 0
        self.call("initialize", {"protocolVersion": "2025-06-18", "capabilities": {},
                                 "clientInfo": {"name": "retrieval-bench", "version": "1"}})

    def call(self, method, params):
        self._id += 1
        self.p.stdin.write(json.dumps({"jsonrpc": "2.0", "id": self._id, "method": method, "params": params}) + "\n")
        self.p.stdin.flush()
        while True:
            line = self.p.stdout.readline()
            if not line:
                # The server exited (or was never alive). A silent EOF used to
                # spin here forever, which is why a dead server read as a bench
                # that printed nothing at all.
                raise SystemExit(f"the server {self.binary!r} stopped answering after "
                                 f"{self._id} call(s) — check the binary and --project")
            line = line.strip()
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


def in_store(server, qualified_name):
    """True when the store actually holds this element.

    A label pointing at an element the store does not have cannot be retrieved
    by any arm, so it measures the label rather than the fusion — and the fix is
    to repair the label, not to read the score.
    """
    try:
        msg = server.call("tools/call", {"name": "query", "arguments": {
            "query": qualified_name, "action": "element", "limit": 5}})
        text = msg["result"]["content"][0]["text"]
        hits = json.loads(text).get("hits") or []
    except Exception:
        # Never turn a probe failure into a stale-label verdict: a broken probe
        # must not silently reclassify a real retrieval miss.
        return True
    return any(h.get("qualified_name") == qualified_name for h in hits)


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
    ap.add_argument("--project", default=".", help="project directory the stdio server serves; it must be the indexed checkout, not the worktree the script lives in")
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
        # A cold store answers every question with an empty list, so every label
        # would read as STALE and the numbers would measure the wrong directory.
        # Name the real cause before printing any number.
        if a.get("freshness") == "cold":
            s.close()
            sys.exit(f"no elements in {args.project!r}: point --project at the indexed "
                     f"checkout (leankg index <dir>), not the worktree this script lives in")
        # A vector arm that degraded to the keyword rung is not a zero: it means
        # the query never reached the fusion, and every number below would be
        # reported as "the keyword arm alone" while looking like a real result.
        ret = a.get("retrieval") or {}
        why = ret.get("reason", "")
        if ret.get("rung") == "L2" and "degraded from L3" in why:
            s.close()
            # The reason already names the cause; only add the fix that fits it,
            # because a wrong model id and a stale collection want DIFFERENT
            # commands and guessing wrong sends the reader to the wrong place.
            if "stamp mismatch" in why or "chunker" in why:
                hint = "run `leankg-embed full` to rebuild the collection"
            elif "no vector collection" in why:
                hint = (f"the model id must be a catalog id such as {CATALOG_MODEL_ID}, "
                        f"not a .gguf file name")
            else:
                hint = "check the embedding provider before reading any number"
            sys.exit(f"the vector arm never ran: {why}\n{hint}")
        vec, kw = arms_of(a, args.limit)
        data.append((n, q, truth, vec, kw, served_order(a, args.limit)))
    # Membership is probed while the session is OPEN: asking a closed server
    # would fail and every label would read as present, which is how the first
    # version of this check silently never fired.
    in_store_map = {truth: in_store(s, truth) for _, _, truth, _, _, _ in data}
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

    # A label whose element the store does not have measures the LABEL, not the
    # fusion: it can never be retrieved by any arm. The label set's own rule is
    # "a label that returns 0 is replaced, not tolerated", so say so loudly
    # rather than reporting it as a retrieval miss. Two labels were stale the
    # first time this ran on today's tree (StandaloneDBPath and ftsQuery had
    # both MOVED files), which is exactly the drift this check catches.
    unindexed = [(num, q, truth) for num, q, truth, vec, kw, served in data
                 if score(vec, truth) is None and score(kw, truth) is None]
    if unindexed:
        # Distinguish "not in the store at all" from "in the store but neither
        # arm ranked it": the first is a stale label, the second is a finding.
        stale = [x for x in unindexed if not in_store_map.get(x[2], True)]
        if stale:
            print(f"\n{len(stale)} STALE label(s) — the element is not in this store, so the label measures nothing:")
            for num, q, truth in stale:
                print(f"  #{num:<3} {truth:<52} {q[:40]}")
            print("  Fix the label (it usually MOVED) before reading any number above.")

    missing = [(num, q, truth) for num, q, truth, vec, kw, served in data
               if score(vec, truth) is None and score(kw, truth) is None
               and in_store_map.get(truth, True)]
    if missing:
        print(f"\n{len(missing)} label(s) in NEITHER arm — these measure the indexer or the embedded text, not the fusion:")
        for num, q, truth in missing:
            print(f"  #{num:<3} {truth:<52} {q[:40]}")


if __name__ == "__main__":
    main()
