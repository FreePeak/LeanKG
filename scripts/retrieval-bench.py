#!/usr/bin/env python3
"""Retrieval bench: score the served L3 ranking against docs/retrieval-label-set.md.

Four arms, one metric function, one denominator:

    semantic            the order the server returned: query action=semantic, limit=10
    grep_element        `rg -i -n -F` once per question word over the indexed files;
                        each matching line is credited to the innermost candidate
                        element that contains it; candidates ranked by distinct words
    grep_element_names  the same candidates, ranked by question words in the element
                        NAME, then by distinct body words (a reader scanning for names)
    grep_file           the same word matches, ranked per file

Every arm is scored over the same questions: only those the server answered at
rung L3, and not STALE. A question at any other rung is listed and excluded. A
STALE label is one whose element is not in the store at all, so no arm can find
it; it is counted apart and never in a denominator.

The label set is parsed from the markdown so the questions, the ground truth
and the code never drift apart.

    go build -o <scratch>/leankg ./cmd/leankg
    LEANKG_EMBED_PROVIDER=... LEANKG_EMBED_BASE_URL=... LEANKG_EMBED_MODEL=... \
    LEANKG_EMBED_DIMS=... \
      python3 scripts/retrieval-bench.py --binary <scratch>/leankg --project <the indexed checkout> [--sweep]

The script does not set or check the embedding endpoint itself: the served `leankg`
binary reads LEANKG_EMBED_* (provider, base URL, model id, dims), and the bench only
reads what that server answers. Two catalog ids are known to internal/embed/catalog.go:
the default `bge-small-en-v1.5-384` (384-d) and `embeddinggemma-2` (768-d native,
512/256/128 selectable through LEANKG_EMBED_DIMS). For a catalog id, LEANKG_EMBED_DIMS
must be the native width or one of its Matryoshka widths; otherwise the server's
embed constructor fails (internal/embed/env.go).

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

A store with no rows in embedding_vectors is not an abort: its labels are scored
as "no-vector", listed apart with their project, and excluded from every arm.
--dump-json PATH writes the per-question ranks of every arm and every --sweep weight.
"""
import argparse
import collections
import json
import os
import re
import sqlite3
import subprocess
import sys
import urllib.parse

CATALOG_MODEL_ID = "bge-small-en-v1.5-384"

HERE = os.path.dirname(os.path.abspath(__file__))
LABELS_MD = os.path.join(HERE, "..", "docs", "retrieval-label-set.md")
ROW = re.compile(r"^\| (\d+) \| (.+?) \| `([^`]+)` \| (.+?) \|$", re.M)

# Container kinds are not answers: a whole file, class or doc section matches almost
# every word and is never the function a grep reader would name. They stay out of
# the candidates once they run past MAX_CANDIDATE_LINES.
CONTAINER_KINDS = frozenset({"file", "module", "package", "namespace", "class", "doc"})
MAX_CANDIDATE_LINES = 200
SERVED_DEPTH = 10  # how many hits the semantic arm is asked for and scored over
CUTOFFS = (1, 3, 5, 10)  # Hit@k cutoffs; MRR is read at the largest one
MRR_CUTOFF = 10
RG_BATCH = 400  # file paths per rg call, to bound argv length
# The label file quotes every question, so a hit in it is the question echoing
# itself, not retrieval. It is dropped from every arm, always, whatever --labels says.
DEFAULT_LABEL_FILE = "docs/retrieval-label-set.md"
# Extra hits asked for when a label-file hit is dropped: the freed slots are filled
# from the next ranks of that deeper answer. The top ranks keep the served order.
LABEL_REFILL = 10
MIN_WORD = 4
SWEEP_WEIGHTS = ["1,1", "1,2", "2,1", "1,3", "3,1", "5,1", "1.5,1", "1,1.5", "2,2", "1,0"]
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


def label_paths(labels_arg, project):
    """Repo-relative paths of the label files: hits in them never count.

    The default markdown is always excluded. A --labels JSON counts only when it
    lives under the project; outside it, no indexed hit can be in it.
    """
    out = {DEFAULT_LABEL_FILE}
    if labels_arg:
        rel = os.path.relpath(os.path.realpath(labels_arg), os.path.realpath(project))
        if not rel.startswith(".."):
            out.add(rel)
    return out


def served_file(hit):
    return hit.get("file_path") or (hit.get("element") or {}).get("file_path") or ""


def drop_label_served(answer, label_set):
    """(answer with its label-file hits removed, how many were removed)."""
    hits = answer.get("hits", [])
    kept = [h for h in hits if served_file(h) not in label_set]
    return dict(answer, hits=kept), len(hits) - len(kept)


def refill_hits(hits, deeper, limit, label_set):
    """hits (label-file hits already gone, served order), topped up to limit from the
    deeper answer's next ranks, skipping label-file hits and anything already present."""
    out = list(hits)
    have = {h["qualified_name"] for h in out}
    for h in deeper:
        if len(out) >= limit:
            break
        if served_file(h) in label_set or h["qualified_name"] in have:
            continue
        out.append(h)
        have.add(h["qualified_name"])
    return out


def without_label_hits(s, answer, question, args, label_set, weight=""):
    """(semantic answer at args.limit without label-file hits, how many were dropped).

    Only a drop triggers the deeper query, so an answer with no label-file hit is the
    server's own answer, unchanged.
    """
    kept, dropped = drop_label_served(answer, label_set)
    if dropped:
        deeper = s.query(question, args.limit + LABEL_REFILL, args.scope, weight=weight)
        kept["hits"] = refill_hits(kept["hits"], deeper.get("hits", []), args.limit, label_set)
    return kept, dropped


def drop_label_matches(matches, label_set):
    """({(path, line): words} without label-file lines, how many lines were dropped)."""
    kept = {loc: ws for loc, ws in matches.items() if loc[0] not in label_set}
    return kept, len(matches) - len(kept)


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


def is_candidate(kind, start, end):
    """Whether an indexed element may be named as the answer to a question."""
    # end < start has no line to explain a match and would divide by zero in the ranking.
    return end >= start and not (kind in CONTAINER_KINDS and end - start + 1 > MAX_CANDIDATE_LINES)


def candidate_spans(elements):
    """{key: (path, start, end)} of the candidates, from {key: (kind, path, start, end)}."""
    return {key: (path, start, end)
            for key, (kind, path, start, end) in elements.items()
            if is_candidate(kind, start, end)}


def _credit(spans, matches):
    """Credit each matching line to the innermost span containing it, and to nothing else.

    An outer scope never inherits a line its inner element already explains, so the
    innermost element is the one a reader reaches first. Returns (words, credits)
    per span key: the distinct words credited, and the word-matches credited.
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
    return words, credits


def rank_elements(spans, matches):
    """Rank candidate span keys by how well they explain a question's word matches.

    spans:   {key: (path, start_line, end_line)}, already filtered by candidate_spans
    matches: {(path, line): set of question words matched on that line}

    Ordered by distinct words matched (most first), then matches per span line,
    then shorter span, then key. Spans with no credit are omitted.
    """
    words, credits = _credit(spans, matches)

    def order(key):
        _, start, end = spans[key]
        lines = end - start + 1
        return (-len(words[key]), -credits[key] / lines, end - start, key)

    return sorted(words, key=order)


def rank_by_name(spans, matches, words):
    """Rank candidates the way a reader scanning grep output for names does.

    An element is ranked first by how many question words are a substring of one of
    its name tokens (the qualified name split on non-alphanumerics, lowercased), then
    by distinct body words, then matches per span line, then shorter span, then key.
    Elements with neither a name hit nor a body credit are omitted.
    """
    body, credits = _credit(spans, matches)

    def name_hits(key):
        tokens = re.findall(r"[a-z0-9]+", key.lower())
        return sum(1 for w in words if any(w in t for t in tokens))

    def order(key):
        _, start, end = spans[key]
        lines = end - start + 1
        return (-name_hits(key), -len(body[key]), -credits[key] / lines, end - start, key)

    return sorted((k for k in spans if name_hits(k) or body[k]), key=order)


def content_words(question):
    """The question's distinct content words: length >= MIN_WORD, stopwords removed."""
    out = []
    for w in re.findall(r"[a-z0-9_]+", question.lower()):
        if len(w) >= MIN_WORD and w not in STOPWORDS and w not in out:
            out.append(w)
    return out


def open_store_ro(project):
    """Read-only connection to <project>/.leankg/leankg.db; exits when there is none."""
    db = os.path.join(project, ".leankg", "leankg.db")
    if not os.path.exists(db):
        sys.exit(f"no SQLite store at {db}: the grep baselines read code_files and "
                 f"code_elements from it")
    # quote(): '#' would end the path as a fragment and '?' start a query, dropping mode=ro.
    return sqlite3.connect(f"file:{urllib.parse.quote(db)}?mode=ro", uri=True)


def store_vector_rows(project):
    """Rows in embedding_vectors; 0 only when that table does not exist (a store never embedded).

    Any other OperationalError (locked, unreadable) propagates: reading it as zero rows
    would exclude a store that has vectors as if it had none.
    """
    con = open_store_ro(project)
    try:
        has_table = con.execute("SELECT 1 FROM sqlite_master WHERE type = 'table' "
                                "AND name = 'embedding_vectors'").fetchone()
        if not has_table:
            return 0
        return con.execute("SELECT COUNT(*) FROM embedding_vectors").fetchone()[0]
    finally:
        con.close()


def no_vector_store(ret, vector_rows):
    """True when a degraded L2 answer is only the symptom of a store with no vectors.

    A store that HAS vectors and still degrades (wrong model, stale stamp) is not this:
    that one must still stop the run, because its numbers would be keyword-only.
    """
    return (ret.get("rung") == "L2" and "degraded from L3" in ret.get("reason", "")
            and vector_rows == 0)


def parse_rg(stdout):
    """(hits [(path, line)], notices) from `rg --null -n` output.

    A binary file prints `path: binary file matches ...` with no NUL separator; that is
    a notice, counted and skipped, never a hit.
    """
    found, notices = [], 0
    # split("\n"), not splitlines(): form feeds and unicode separators are content, and
    # splitlines() would drift the line numbers away from rg's.
    for line in stdout.split("\n"):
        if not line:
            continue
        if "\0" not in line:
            notices += 1
            continue
        path, rest = line.split("\0", 1)
        num = rest.split(":", 1)[0]
        if not num.isdigit():
            notices += 1
            continue
        found.append((path, int(num)))
    return found, notices


def rg_failure_is_tolerable(returncode, stderr):
    """rg exit 0 (hits) and 1 (no hit) are fine. Exit 2 is fine only when every error is
    a missing path: the matches in the paths that exist are still valid."""
    if returncode in (0, 1):
        return True
    lines = [l for l in stderr.splitlines() if l.strip()]
    return returncode == 2 and bool(lines) and all("No such file or directory" in l for l in lines)


class GrepIndex:
    """What the grep baselines read from one checkout's SQLite store, read-only.

    code_files bounds the search (rg runs only over the paths that exist on disk; the
    rest are counted in .missing); code_elements gives the candidate spans the matches
    are credited to (candidate_spans) and the full spans the file ranking reads.
    """

    def __init__(self, project):
        con = open_store_ro(project)
        try:
            listed = [p for (p,) in con.execute("SELECT path FROM code_files ORDER BY path")]
            elements = {qn: (kind, path, start, end) for qn, kind, path, start, end in con.execute(
                "SELECT qualified_name, element_type, file_path, line_start, line_end FROM code_elements")}
        finally:
            con.close()
        self.files = [p for p in listed if os.path.isfile(os.path.join(project, p))]
        self.missing = len(listed) - len(self.files)
        self.binary_notices = 0
        self.spans = {qn: (path, start, end) for qn, (_, path, start, end) in elements.items()}
        self.candidates = candidate_spans(elements)
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
                # -H: with one path in the batch rg drops the filename, and the NUL
                # separator with it, so the line would be read as a binary notice.
                cmd = ["rg", "-i", "-n", "-F", "-H", "--no-heading", "--null", "-e", word, "--",
                       *self.files[i:i + RG_BATCH]]
                r = subprocess.run(cmd, cwd=self.root, capture_output=True, text=True, errors="replace")
                if not rg_failure_is_tolerable(r.returncode, r.stderr):
                    sys.exit(f"rg failed on {word!r}: {r.stderr.strip()}")
                hits, notices = parse_rg(r.stdout)
                found.extend(hits)
                self.binary_notices += notices
            self._hits[word] = found
        return self._hits[word]


def grep_orders(index, question, label_set):
    """(element, element-by-name, file orders, dropped label-file lines) of the grep baselines."""
    words = content_words(question)
    matches, dropped = drop_label_matches(index.matches(words), label_set)
    element = rank_elements(index.candidates, matches)
    names = rank_by_name(index.candidates, matches, words)
    file_spans = {path: (path, 1, index.line_count(path)) for path, _ in matches}
    return element, names, rank_elements(file_spans, matches), dropped


def sweep_progress(row):
    """One line per finished question during --sweep: its rank at every weight tried."""
    return f"sweep #{row['num']} " + " ".join(
        f"{w}={fmt_rank(r)}" for w, r in row["sweep"].items())


def dump_records(rows):
    """Per-question ranks for every arm and every sweep weight, one record per label.

    ranks is None where the label is not in that arm's list; scored says whether the
    label is in the denominator (rung L3, not STALE).
    """
    return [{"id": r["num"], "project": r["project"], "question": r["question"],
             "label": r["truth"], "rung": r["rung"], "stale": r["stale"],
             "scored": r["rung"] == "L3" and not r["stale"],
             "ranks": {"semantic": score(r["served"], r["truth"]),
                       "grep_element": score(r["element"], r["truth"]),
                       "grep_element_names": score(r["names"], r["truth"]),
                       "grep_file": score_file(r["file"], r["truth"], r["spans"])},
             "sweep": dict(r["sweep"])}
            for r in rows]


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
    try:
        vector_rows = store_vector_rows(project)
    except sqlite3.OperationalError as e:
        sys.exit(f"cannot count embedding_vectors in {project!r}: {e}; the run is aborted "
                 f"before any label is scored (is another process holding the store?)")
    s = Session(binary, project)
    labels_set = label_paths(args.labels, project)
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
            # as a real result while measuring the keyword arm alone. A store with no
            # vectors at all is the one degrade that is expected: its labels are set aside.
            ret = a.get("retrieval") or {}
            why = ret.get("reason", "")
            no_vec = no_vector_store(ret, vector_rows)
            if ret.get("rung") == "L2" and "degraded from L3" in why and not no_vec:
                sys.exit(f"the vector arm never ran: {why}\n{degraded_hint(why)}")
            a, sem_dropped = without_label_hits(s, a, lab.question, args, labels_set)
            served = served_order(a, args.limit)
            element, names, file_, grep_dropped = grep_orders(grep, lab.question, labels_set)
            rows.append({"num": lab.num, "question": lab.question, "truth": lab.truth,
                         "project": project,
                         "rung": "no-vector" if no_vec else ret.get("rung", "?"),
                         "served": served,
                         "arms": arms_of(a, args.limit), "element": element, "names": names,
                         "file": file_,
                         "spans": grep.spans, "stale": False, "sweep": {},
                         "dropped": {"semantic": sem_dropped, "grep": grep_dropped}})
        # Membership is probed while the session is OPEN: asking a closed server
        # would fail and every label would read as present.
        for row in rows:
            if row["rung"] == "L3" and score(row["served"], row["truth"]) is None:
                row["stale"] = not in_store(s, row["truth"])
        if args.sweep:
            for row in rows:
                if row["rung"] != "L3" or row["stale"]:
                    continue
                for w in SWEEP_WEIGHTS:
                    a = s.query(row["question"], args.limit, args.scope, weight=w)
                    a, _ = without_label_hits(s, a, row["question"], args, labels_set, weight=w)
                    row["sweep"][w] = score(served_order(a, args.limit), row["truth"])
                # Flushed per question, so a timeout still leaves every finished row.
                print(sweep_progress(row), flush=True)
    finally:
        s.close()
    print(f"{project}: {grep.missing} indexed file(s) missing from the checkout skipped; "
          f"{grep.binary_notices} rg binary-file notice(s) skipped", flush=True)
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
    ap.add_argument("--dump-json", default="", metavar="PATH", help="write the per-question ranks of every arm and every --sweep weight to PATH")
    args = ap.parse_args()
    if args.limit < max(CUTOFFS):
        ap.error(f"--limit must be at least {max(CUTOFFS)} so Hit@{max(CUTOFFS)} is read from the served list")
    # Line-buffered, so a timeout that kills the run still leaves every line printed so far.
    sys.stdout.reconfigure(line_buffering=True)

    labels = load_labels(args.labels or None)
    groups = collections.OrderedDict()
    for lab in labels:
        groups.setdefault(lab.project or args.project, []).append(lab)
    rows = []
    for project, group in groups.items():
        rows.extend(run_project(args.binary, project, group, args))

    # The scored set is the same for every arm: answered at L3 and not stale.
    scored = [r for r in rows if r["rung"] == "L3" and not r["stale"]]
    not_l3 = [r for r in rows if r["rung"] not in ("L3", "no-vector")]
    no_vec = [r for r in rows if r["rung"] == "no-vector"]
    stale = [r for r in rows if r["rung"] == "L3" and r["stale"]]
    n_stale = len(stale)
    if args.dump_json:
        with open(args.dump_json, "w") as f:
            json.dump(dump_records(rows), f, indent=1)

    print(f"{len(rows)} labels from {args.labels or 'docs/retrieval-label-set.md'} over "
          f"{len(groups)} store(s), served depth {args.limit}, "
          f"semantic scope={args.scope or 'full corpus'}, grep arms: full store")
    print(f"scored {len(scored)} (rung L3, not stale); excluded {len(not_l3)} not at L3, "
          f"{len(no_vec)} no-vector, {n_stale} STALE")
    dropped = {arm: sum(r["dropped"][k] for r in rows)
               for arm, k in (("semantic", "semantic"), ("grep_element", "grep"),
                              ("grep_element_names", "grep"), ("grep_file", "grep"))}
    print("label-file hits dropped before ranking (the label file quotes every question): "
          + ", ".join(f"{arm} {n}" for arm, n in dropped.items()) + "\n")
    print_metrics([
        ("semantic", [score(r["served"], r["truth"]) for r in scored], n_stale),
        ("grep_element", [score(r["element"], r["truth"]) for r in scored], n_stale),
        ("grep_element_names", [score(r["names"], r["truth"]) for r in scored], n_stale),
        ("grep_file", [score_file(r["file"], r["truth"], r["spans"]) for r in scored], n_stale),
    ])

    print("\nsemantic hits by arm provenance (the label is in that arm's served list):")
    for idx, label in enumerate(("vector", "keyword", "name")):
        present = sum(1 for r in scored if r["truth"] in r["arms"][idx])
        print(f"  {label:<10} {present}/{len(scored)}")

    print(f"\n{'#':<4}{'rung':<6}{'semantic':>9}{'grep_el':>9}{'by_name':>9}{'grep_file':>10}  question")
    for r in rows:
        print(f"{r['num']:<4}{r['rung']:<6}{fmt_rank(score(r['served'], r['truth'])):>9}"
              f"{fmt_rank(score(r['element'], r['truth'])):>9}"
              f"{fmt_rank(score(r['names'], r['truth'])):>9}"
              f"{fmt_rank(score_file(r['file'], r['truth'], r['spans'])):>10}  {r['question'][:46]}")

    if not_l3:
        print(f"\n{len(not_l3)} question(s) NOT answered at L3, excluded from every arm:")
        for r in not_l3:
            print(f"  #{r['num']:<4} rung {r['rung']:<4} {r['question'][:50]}")
    if no_vec:
        print(f"\n{len(no_vec)} question(s) on store(s) with NO vectors (non-L3 / no-vector), excluded from every arm:")
        for r in no_vec:
            print(f"  #{r['num']:<4} {r['project'][-40:]:<42} {r['question'][:40]}")
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
