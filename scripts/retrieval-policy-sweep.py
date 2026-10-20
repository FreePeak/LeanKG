#!/usr/bin/env python3
"""Sweep fusion policies offline over the arms the engine already returns.

Wave 12 found that on behaviour-derived questions the FUSION IS WORSE THAN THE
VECTOR ARM ALONE (fused top-1 18/137 against vector 59/137), because a keyword
index cannot answer a question containing none of the document's words and
contributes noise at equal weight. It also found the fix is not free: (3,1)
buys behaviour labels and costs doc-derived ones.

This sweeps the POLICY space rather than a single constant, because a constant
cannot be right at both tails. Each policy reads only what the engine already
has — the two arms' ranked key lists — so any winner here is implementable
without a new query, a new index, or a new store round-trip:

  constant(wv, wk)      the current shape
  agree_boost(gamma)     the vector arm is scaled by gamma when the keyword
                         arm's top hit IS the vector arm's top hit (the two
                         corroborate each other), and left alone when they
                         disagree (one of them is guessing)
  agree_gate(theta)      the keyword arm is DROPPED entirely when its top hit
                         and the vector arm's top hit disagree — a keyword
                         index that cannot corroborate is a keyword index
                         guessing
  top_overlap(n)         a policy parameterised by how many of the keyword
                         arm's top-n are also in the vector arm's top-n

Run against any label set:  --labels docs/retrieval-labels-behaviour.json
"""
import argparse
import json
import os
import re
import subprocess
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
BENCH = os.path.join(HERE, "retrieval-bench.py")

RRFK = 60


def arms_from_answer(answer, limit):
    """The two arms' key lists in each arm's own rank order, plus the served
    order (what a single-arm engine returns)."""
    vec, kw = [], []
    for h in answer.get("hits", [])[:limit]:
        rk = h.get("ranks") or {}
        if rk.get("vector"):
            vec.append((rk["vector"], h["qualified_name"]))
        if rk.get("tsvector"):
            kw.append((rk["tsvector"], h["qualified_name"]))
    if not vec and not kw:
        kw = [(i + 1, h["qualified_name"]) for i, h in enumerate(answer.get("hits", [])[:limit])]
    return [k for _, k in sorted(vec)], [k for _, k in sorted(kw)]


def fuse(vec, kw, policy, params):
    """Return the fused key order under one policy."""
    wv, wk = params.get("wv", 1.0), params.get("wk", 1.0)
    kind = policy
    if kind == "constant":
        pass
    elif kind == "agree_boost":
        if vec and kw and vec[0] == kw[0]:
            wv *= params.get("gamma", 3.0)
    elif kind == "agree_gate":
        if vec and kw and vec[0] != kw[0]:
            kw = []  # the keyword arm cannot corroborate: drop it
    elif kind == "top_overlap":
        n = params.get("n", 5)
        if vec and kw:
            shared = len(set(vec[:n]) & set(kw[:n]))
            if shared == 0:
                kw = []
    else:
        raise ValueError(kind)
    score = {}
    for keys, w in ((vec, wv), (kw, wk)):
        for i, k in enumerate(keys):
            score[k] = score.get(k, 0.0) + w / (RRFK + i + 1)
    return sorted(score, key=lambda k: -score[k])


def load_labels(path):
    if path.endswith(".json"):
        rows = json.load(open(path))
        return [(i + 1, q.strip(), qn) for i, (qn, q) in enumerate(rows)]
    text = open(path).read()
    return [(int(n), q.strip(), qn.strip())
            for n, q, qn in re.findall(r"^\| (\d+) \| (.+?) \| `([^`]+)` \|", text, re.M)]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--binary", default="/tmp/leankg")
    ap.add_argument("--project", default=".")
    ap.add_argument("--labels", required=True)
    ap.add_argument("--limit", type=int, default=100)
    ap.add_argument("--scope", default="")
    args = ap.parse_args()

    labels = load_labels(args.labels)
    out = tempfile.NamedTemporaryFile("w", suffix=".json", delete=False)
    json.dump([[qn, q] for _, q, qn in labels], out)
    out.close()

    env = dict(os.environ)
    cmd = [sys.executable, BENCH, "--binary", args.binary, "--project", args.project,
           "--limit", str(args.limit), "--labels", out.name]
    if args.scope:
        cmd += ["--scope", args.scope]
    proc = subprocess.run(cmd, env=env, capture_output=True, text=True)
    if proc.returncode != 0:
        print(proc.stdout[-2000:], proc.stderr[-2000:])
        sys.exit(1)
    os.unlink(out.name)

    # Re-derive the arms from the SAME live queries the bench made, so the
    # policy sweep scores exactly the retrieval the bench scored.
    srv_args = {"query": "", "limit": args.limit}
    if args.scope:
        srv_args["args"] = {"scope": args.scope}
    p = subprocess.Popen([args.binary, "serve", "--stdio", "--project", args.project, "--memory"],
                         stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                         stderr=subprocess.DEVNULL, env=env, text=True, bufsize=1)
    pid = [0]

    def call(method, params):
        pid[0] += 1
        p.stdin.write(json.dumps({"jsonrpc": "2.0", "id": pid[0], "method": method, "params": params}) + "\n")
        p.stdin.flush()
        while True:
            line = p.stdout.readline().strip()
            if not line:
                continue
            try:
                m = json.loads(line)
            except json.JSONDecodeError:
                continue
            if m.get("id") == pid[0]:
                return m

    call("initialize", {"protocolVersion": "2025-06-18", "capabilities": {},
                        "clientInfo": {"name": "policy-sweep", "version": "1"}})
    data = []
    for _, q, truth in labels:
        a = dict(srv_args)
        a["query"] = q
        r = call("tools/call", {"name": "query", "arguments": a})
        ans = json.loads(r["result"]["content"][0]["text"])
        vec, kw = arms_from_answer(ans, args.limit)
        data.append((truth, vec, kw))
    p.terminate()

    n = len(data)
    print(f"{n} labels, depth {args.limit}\n")
    print(f"{'policy':<34} {'top-1':>9} {'top-3':>9} {'top-10':>9}")
    print("-" * 66)

    def score(policy, params):
        t1 = t3 = t10 = 0
        for truth, vec, kw in data:
            order = fuse(vec, kw, policy, params)
            if truth not in order:
                continue
            r = order.index(truth) + 1
            t1 += r == 1
            t3 += r <= 3
            t10 += r <= 10
        return t1, t3, t10

    rows = []
    for wv, wk in [(1, 1), (2, 1), (3, 1), (1, 2)]:
        rows.append((f"constant({wv},{wk})", score("constant", {"wv": wv, "wk": wk})))
    for g in (2.0, 3.0, 5.0):
        rows.append((f"agree_boost gamma={g}", score("agree_boost", {"gamma": g})))
    rows.append(("agree_gate (drop on disagree)", score("agree_gate", {})))
    for nn in (3, 5, 10):
        rows.append((f"top_overlap n={nn} (drop if 0)", score("top_overlap", {"n": nn})))
    rows.append(("vector only", (sum(1 for t, v, k in data if v and t in v and v.index(t) == 0),
                                 sum(1 for t, v, k in data if v and t in v and v.index(t) < 3),
                                 sum(1 for t, v, k in data if v and t in v and v.index(t) < 10))))
    for name, (a, b, c) in rows:
        print(f"{name:<34} {a:>4}/{n:<4} {b:>4}/{n:<4} {c:>4}/{n:<4}")


if __name__ == "__main__":
    main()
