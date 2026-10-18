#!/usr/bin/env python3
"""Retrieval bench: score the served L3 ranking against docs/retrieval-label-set.md.

Three arms, one metric function, one denominator:

    semantic      the order the server returned: query action=semantic, limit=10
    grep_element  `rg -i -n -F` once per question word over the indexed files;
                  each matching line is credited to the innermost indexed element
                  that contains it, elements ranked by distinct words matched
    grep_file     the same word matches, ranked per file

Every arm is scored over the same questions: only those the server answered at
rung L3, and not STALE. A question at any other rung is listed and excluded. A
STALE label is one whose element is not in the store at all, so no arm can find
it; it is counted apart and never in a denominator.

The label set is parsed from the markdown so the questions, the ground truth
and the code never drift apart.

    go build -o <scratch>/leankg ./cmd/leankg
    LEANKG_EMBED_PROVIDER=local LEANKG_EMBED_BASE_URL=http://127.0.0.1:9101/v1 \
    LEANKG_EMBED_MODEL=bge-small-en-v1.5-384 LEANKG_EMBED_DIMS=384 \
      python3 scripts/retrieval-bench.py --binary <scratch>/leankg --project <the indexed checkout> [--sweep]

--labels FILE.json takes rows of [qualified_name, question] or
[qualified_name, question, project]; a row's own project overrides --project, so
one run can span several stores.

LEANKG_EMBED_MODEL must be a CATALOG id (`bge-small-en-v1.5-384`, the pinned
384-d entry in internal/embed/catalog.go), NOT the .gguf file name the sidecar
serves: an id the catalog does not know gets a different model_id and revision,
the stamp lookup finds no collection, and every query silently degrades to the
keyword rung — which reads as "the vector arm found nothing" instead of "the
vector arm never ran". The bench stops on that instead of reporting it.

--project must be the checkout the store was indexed from, not the worktree this
script lives in: a store with no elements answers everything with an empty list.
The grep arms read <project>/.leankg/leankg.db (SQLite, read-only); --scope
narrows the semantic arm only, the grep arms always search the whole store.
"""
import argparse
import collections
import json
import os
import re
import sqlite3
import subprocess
import sys

CATALOG_MODEL_ID = "bge-small-en-v1.5-384"

HERE = os.path.dirname(os.path.abspath(__file__))
LABELS_MD = os.path.join(HERE, "..", "docs", "retrieval-label-set.md")
ROW = re.compile(r"^\| (\d+) \| (.+?) \| `([^`]+)` \| (.+?) \|$", re.M)

SERVED_DEPTH = 10  # how many hits the semantic arm is asked for and scored over
CUTOFFS = (1, 3, 5, 10)  # Hit@k cutoffs; MRR is read at the largest one
MRR_CUTOFF = 10
RG_BATCH = 400  # file paths per rg call, to bound argv length
MIN_WORD = 4
SWEEP_WEIGHTS = ["1,1", "1,2", "2,1", "1,3", "3,1", "1.5,1", "1,1.5", "2,2"]
STOPWORDS = frozenset("""
    about above after again against also another because been before being
    between both could does doing down each even every from have here into
    just like make many more most much must only other over same should some
    such than that their them then there these they this those through under
    until used very what when where which while will with within without would
    your
""".split())

# The rank-list label the server gives each arm in hit.ranks. fts5 is SQLite's
# keyword arm; tsvector, trigram and ilike are PostgreSQL's.
ARM_LIST = {"vector": "vec", "name": "name", "fts5": "kw", "tsvector": "kw",
            "trigram": "kw", "ilike": "kw"}

Label = collections.namedtuple("Label", "num question truth project")


def load_labels(path=None):
    """Load the label set as Label(num, question, truth, project) rows.

    Default: the LeanKG markdown table (question | ground truth | file), whose
    rows use --project. With --labels: a JSON list of [qualified_name, question]
    or [qualified_name, question, project] rows; the optional third field lets
    one run span several stores.
    """
    if path:
        with open(path) as f:
            rows = json.load(f)
        labels = [Label(i + 1, row[1].strip(), row[0], row[2] if len(row) > 2 else None)
                  for i, row in enumerate(rows)]
    else:
        with open(LABELS_MD) as f:
            text = f.read()
        labels = [Label(int(n), q.strip(), qn.strip(), None)
                  for n, q, qn, _where in ROW.findall(text)]
    if not labels:
        sys.exit("no labels in " + (path or LABELS_MD))
    return labels


def serve_argv(binary, project):
    """The stdio server argv. --read-only: the bench only queries, so the store is
    opened RO (no Migrate, no auto-index); a RW serve could write the store it measures.
    No --memory: it opens the project's memory DB read-write, which the bench never uses."""
    return [binary, "serve", "--stdio", "--read-only", "--project", project]


class Session:
    """One stdio MCP server; the semantic arm is the server's own fused answer."""

    def __init__(self, binary, project):
        self.binary = binary
        self.p = subprocess.Popen(
            serve_argv(binary, project),
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

    def query(self, q, limit, scope="", weight=""):
        """The semantic arm: query pinned to action=semantic, the server's fused order."""
        arguments = {"query": q, "action": "semantic", "limit": limit}
        args = {}
        if scope:
            args["scope"] = scope
        if weight:
            args["weight"] = weight
        if args:
            arguments["args"] = args
        r = self.call("tools/call", {"name": "query", "arguments": arguments})
        return json.loads(r["result"]["content"][0]["text"])

    def close(self):
        self.p.terminate()


def arms_of(answer, limit):
    """(vector keys, keyword keys, name keys) in each arm's own rank order.

    Reads every arm the server labels in hit.ranks (ARM_LIST). A binary that
    predates the fused `ranks` field (origin/main, before wave 1) serves the
    keyword rung alone, so its hits carry no per-arm provenance. That is not a
    zero: score the single list as the keyword arm, which is what that binary
    actually answers with. Without the fallback a before/after A/B against the
    pre-wave-1 engine reads 0 on both columns and the regression looks
    catastrophic when nothing changed.
    """
    lists = {"vec": [], "kw": [], "name": []}
    hits = answer.get("hits", [])[:limit]
    for h in hits:
        for arm, rank in (h.get("ranks") or {}).items():
            if arm in ARM_LIST:
                lists[ARM_LIST[arm]].append((rank, h["qualified_name"]))
    if hits and not any(lists.values()):
        lists["kw"] = [(i + 1, h["qualified_name"]) for i, h in enumerate(hits)]
    return [k for _, k in sorted(lists["vec"])], [k for _, k in sorted(lists["kw"])], \
        [k for _, k in sorted(lists["name"])]


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
    """1-based rank of truth in order, matched by exact qualified name; None if absent."""
    return order.index(truth) + 1 if truth in order else None


def score_file(file_order, truth, spans):
    """Rank of the label's file among the ranked files; None if the element is unknown."""
    return score(file_order, spans[truth][0]) if truth in spans else None


def evaluate(ranks, stale=0):
    """Hit@k and MRR@10 over the scored labels; ranks holds a 1-based rank or None each.

    The denominator is len(ranks), every scored label. stale is reported beside
    it and is never part of it.
    """
    n = len(ranks)
    hits = {k: sum(1 for r in ranks if r is not None and r <= k) for k in CUTOFFS}
    mrr = sum(1 / r for r in ranks if r is not None and r <= MRR_CUTOFF)
    return {"n": n, "stale": stale, "hits": hits, "mrr10": mrr / n if n else 0.0}


def rank_elements(spans, matches):
    """Rank span keys by how well they explain a question's word matches.

    spans:   {key: (path, start_line, end_line)}
    matches: {(path, line): set of question words matched on that line}

    Each matching line is credited to the innermost span that contains it. Spans
    are ordered by distinct words matched (most first), then density (credits per
    span line), then shorter span, then key. Spans with no credit are omitted.
    """
    by_path = collections.defaultdict(list)
    for key, (path, start, end) in spans.items():
        by_path[path].append((end - start, key, start, end))
    words = collections.defaultdict(set)
    credits = collections.Counter()
    for (path, line), ws in matches.items():
        inside = [(length, key) for length, key, start, end in by_path.get(path, ())
                  if start <= line <= end]
        if not inside:
            continue
        _, key = min(inside)
        words[key] |= ws
        credits[key] += len(ws)

    def order(key):
        _, start, end = spans[key]
        lines = end - start + 1
        return (-len(words[key]), -credits[key] / lines, end - start, key)

    return sorted(words, key=order)


def content_words(question):
    """The question's distinct content words: length >= MIN_WORD, stopwords removed."""
    out = []
    for w in re.findall(r"[a-z0-9_]+", question.lower()):
        if len(w) >= MIN_WORD and w not in STOPWORDS and w not in out:
            out.append(w)
    return out


class GrepIndex:
    """What the grep baselines read from one checkout's SQLite store, read-only.

    code_files bounds the search (rg runs only over those paths); code_elements
    gives the spans the matches are credited to.
    """

    def __init__(self, project):
        db = os.path.join(project, ".leankg", "leankg.db")
        if not os.path.exists(db):
            sys.exit(f"no SQLite store at {db}: the grep baselines read code_files and "
                     f"code_elements from it")
        con = sqlite3.connect(f"file:{db}?mode=ro", uri=True)
        try:
            self.files = [p for (p,) in con.execute("SELECT path FROM code_files ORDER BY path")]
            self.spans = {qn: (path, start, end) for qn, path, start, end in con.execute(
                "SELECT qualified_name, file_path, line_start, line_end FROM code_elements")}
        finally:
            con.close()
        self.root = project
        self._hits = {}
        self._lines = {}

    def matches(self, words):
        """{(path, line): words matched on that line} over the indexed files."""
        out = collections.defaultdict(set)
        for w in words:
            for loc in self._word_hits(w):
                out[loc].add(w)
        return out

    def line_count(self, path):
        if path not in self._lines:
            with open(os.path.join(self.root, path), "rb") as f:
                self._lines[path] = sum(1 for _ in f)
        return self._lines[path]

    def _word_hits(self, word):
        if word not in self._hits:
            found = []
            for i in range(0, len(self.files), RG_BATCH):
                cmd = ["rg", "-i", "-n", "-F", "--no-heading", "--null", "-e", word, "--",
                       *self.files[i:i + RG_BATCH]]
                r = subprocess.run(cmd, cwd=self.root, capture_output=True, text=True, errors="replace")
                if r.returncode not in (0, 1):  # 1 means no match
                    sys.exit(f"rg failed on {word!r}: {r.stderr.strip()}")
                for line in r.stdout.splitlines():
                    path, rest = line.split("\0", 1)
                    found.append((path, int(rest.split(":", 1)[0])))
            self._hits[word] = found
        return self._hits[word]


def grep_orders(index, question):
    """(element order, file order) of the grep baselines for one question."""
    matches = index.matches(content_words(question))
    element = rank_elements(index.spans, matches)
    file_spans = {path: (path, 1, index.line_count(path)) for path, _ in matches}
    return element, rank_elements(file_spans, matches)


def degraded_hint(why):
    # The reason already names the cause; only add the fix that fits it,
    # because a wrong model id and a stale collection want DIFFERENT
    # commands and guessing wrong sends the reader to the wrong place.
    if "stamp mismatch" in why or "chunker" in why:
        return "run `leankg-embed full` to rebuild the collection"
    if "no vector collection" in why:
        return (f"the model id must be a catalog id such as {CATALOG_MODEL_ID}, "
                f"not a .gguf file name")
    return "check the embedding provider before reading any number"


def run_project(binary, project, labels, args):
    """Score the labels that name this project. Returns one dict per label."""
    grep = GrepIndex(project)
    s = Session(binary, project)
    rows = []
    try:
        for lab in labels:
            a = s.query(lab.question, args.limit, args.scope)
            # A cold store answers every question with an empty list, so every label
            # would read as STALE and the numbers would measure the wrong directory.
            # Name the real cause before printing any number.
            if a.get("freshness") == "cold":
                sys.exit(f"no elements in {project!r}: point --project at the indexed "
                         f"checkout (leankg index <dir>), not the worktree this script lives in")
            # A vector arm that degraded to the keyword rung is not a zero: it means
            # the query never reached the fusion, and every number would be reported
            # as a real result while measuring the keyword arm alone.
            ret = a.get("retrieval") or {}
            why = ret.get("reason", "")
            if ret.get("rung") == "L2" and "degraded from L3" in why:
                sys.exit(f"the vector arm never ran: {why}\n{degraded_hint(why)}")
            served = served_order(a, args.limit)
            element, file_ = grep_orders(grep, lab.question)
            rows.append({"num": lab.num, "question": lab.question, "truth": lab.truth,
                         "rung": ret.get("rung", "?"), "served": served,
                         "arms": arms_of(a, args.limit), "element": element, "file": file_,
                         "spans": grep.spans, "stale": False, "sweep": {}})
        # Membership is probed while the session is OPEN: asking a closed server
        # would fail and every label would read as present.
        for row in rows:
            if score(row["served"], row["truth"]) is None:
                row["stale"] = not in_store(s, row["truth"])
        if args.sweep:
            for row in rows:
                if row["rung"] != "L3" or row["stale"]:
                    continue
                for w in SWEEP_WEIGHTS:
                    a = s.query(row["question"], args.limit, args.scope, weight=w)
                    row["sweep"][w] = score(served_order(a, args.limit), row["truth"])
    finally:
        s.close()
    return rows


def print_metrics(entries):
    """entries: (name, ranks, stale). One row per arm, every cell a count over its denominator."""
    print(f"{'arm':<16}" + "".join(f"{'Hit@' + str(k):>9}" for k in CUTOFFS)
          + f"{'MRR@10':>9}{'denom':>7}{'stale':>7}")
    print("-" * (16 + 9 * len(CUTOFFS) + 23))
    for name, ranks, stale in entries:
        e = evaluate(ranks, stale)
        cells = "".join(f"{str(e['hits'][k]) + '/' + str(e['n']):>9}" for k in CUTOFFS)
        print(f"{name:<16}{cells}{e['mrr10']:>9.3f}{e['n']:>7}{e['stale']:>7}")


def fmt_rank(r):
    return "-" if r is None else str(r)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--binary", default="/tmp/leankg")
    ap.add_argument("--project", default=".", help="project directory the stdio server serves; it must be the indexed checkout, not the worktree the script lives in")
    ap.add_argument("--limit", type=int, default=SERVED_DEPTH, help="hits the semantic arm answers with; the metrics read Hit@1/3/5/10 and MRR@10 from them")
    ap.add_argument("--sweep", action="store_true", help="also score each fusion weight pair the server applies (args.weight)")
    ap.add_argument("--scope", default="", help="args.scope passed to every semantic query (code|prod|all)")
    ap.add_argument("--labels", default="", help="JSON label file: rows of [qualified_name, question] or [qualified_name, question, project]; default is the LeanKG markdown table")
    args = ap.parse_args()
    if args.limit < max(CUTOFFS):
        ap.error(f"--limit must be at least {max(CUTOFFS)} so Hit@{max(CUTOFFS)} is read from the served list")

    labels = load_labels(args.labels or None)
    groups = collections.OrderedDict()
    for lab in labels:
        groups.setdefault(lab.project or args.project, []).append(lab)
    rows = []
    for project, group in groups.items():
        rows.extend(run_project(args.binary, project, group, args))

    # The scored set is the same for every arm: answered at L3 and not stale.
    scored = [r for r in rows if r["rung"] == "L3" and not r["stale"]]
    not_l3 = [r for r in rows if r["rung"] != "L3"]
    stale = [r for r in rows if r["rung"] == "L3" and r["stale"]]
    n_stale = len(stale)

    print(f"{len(rows)} labels from {args.labels or 'docs/retrieval-label-set.md'} over "
          f"{len(groups)} store(s), served depth {args.limit}, "
          f"semantic scope={args.scope or 'full corpus'}, grep arms: full store")
    print(f"scored {len(scored)} (rung L3, not stale); excluded {len(not_l3)} not at L3, "
          f"{n_stale} STALE\n")
    print_metrics([
        ("semantic", [score(r["served"], r["truth"]) for r in scored], n_stale),
        ("grep_element", [score(r["element"], r["truth"]) for r in scored], n_stale),
        ("grep_file", [score_file(r["file"], r["truth"], r["spans"]) for r in scored], n_stale),
    ])

    print("\nsemantic hits by arm provenance (the label is in that arm's served list):")
    for idx, label in enumerate(("vector", "keyword", "name")):
        present = sum(1 for r in scored if r["truth"] in r["arms"][idx])
        print(f"  {label:<10} {present}/{len(scored)}")

    print(f"\n{'#':<4}{'rung':<6}{'semantic':>9}{'grep_el':>9}{'grep_file':>10}  question")
    for r in rows:
        print(f"{r['num']:<4}{r['rung']:<6}{fmt_rank(score(r['served'], r['truth'])):>9}"
              f"{fmt_rank(score(r['element'], r['truth'])):>9}"
              f"{fmt_rank(score_file(r['file'], r['truth'], r['spans'])):>10}  {r['question'][:46]}")

    if not_l3:
        print(f"\n{len(not_l3)} question(s) NOT answered at L3, excluded from every arm:")
        for r in not_l3:
            print(f"  #{r['num']:<4} rung {r['rung']:<4} {r['question'][:50]}")
    if stale:
        print(f"\n{n_stale} STALE label(s): the element is not in its store, so no arm can find it; excluded from every arm:")
        for r in stale:
            print(f"  #{r['num']:<4} {r['truth']:<52} {r['question'][:40]}")
        print("  Fix the label (it usually MOVED) before reading any number above.")

    if args.sweep:
        print("\nfusion weights (vec,kw), semantic arm, same denominator:")
        print_metrics([(f"w {w}", [r["sweep"].get(w) for r in scored], n_stale)
                       for w in SWEEP_WEIGHTS])


if __name__ == "__main__":
    main()
