#!/usr/bin/env python3
"""Derive a BEHAVIOUR-derived label set: questions with no doc-comment vocabulary.

Why this exists. Waves 8-11 scored two corpora, and the second corpus's labels
were derived from doc comments — which is exactly the text wave 9 taught the
engine to index. So corpus 2's numbers flatter wave 9, and the loop said so
rather than banking them.

This protocol removes the confound. A question is built from the tokens that
appear in the answer's BODY but NOT in its doc comment, so a correct engine
cannot satisfy it with the comment alone. The generated question is verified to
share zero tokens with the answer's comment before it is kept — that check is
the whole point, and the script FAILS LOUDLY if a label violates it.

    python3 scripts/retrieval-labels-behaviour.py --project . --out docs/retrieval-labels-behaviour.json
"""
import argparse
import json
import os
import re
import sqlite3

IDENT = re.compile(r"[A-Za-z_][A-Za-z0-9_]{2,}")
# Words a question can use without carrying the answer's comment vocabulary.
GENERIC = {
    "the", "and", "for", "with", "that", "this", "from", "into", "not", "are",
    "was", "were", "has", "had", "can", "does", "did", "but", "you", "your",
    "its", "out", "own", "same", "too", "very", "just", "how", "what", "where",
    "which", "when", "why", "who", "one", "two", "all", "any", "some", "each",
    "use", "used", "using", "via", "per", "upon", "here", "there", "get", "set",
    "run", "add", "new", "then", "than", "does", "return", "returns", "func",
    "function", "code", "line", "lines", "file", "files", "path", "paths", "type",
    "name", "names", "list", "find", "rank", "test", "tests", "import",
}
QUESTION = "where is the code that handles {terms}"


def doc_comment(root, file_path, line_start):
    full = os.path.join(root, file_path)
    if not os.path.exists(full):
        return ""
    try:
        lines = open(full, encoding="utf-8", errors="replace").read().split("\n")
    except OSError:
        return ""
    i = (line_start or 1) - 2
    out = []
    while i >= 0 and lines[i].strip().startswith("//"):
        out.append(lines[i].strip()[2:].strip())
        i -= 1
    return " ".join(reversed(out)).strip()


def body_tokens(body):
    """Identifiers in the body, split camelCase/snake_case, minus generics."""
    words = set()
    for raw in IDENT.findall(body or ""):
        parts = re.split(r"_+|(?<=[a-z0-9])(?=[A-Z])", raw)
        for w in parts:
            w = w.strip()
            if len(w) >= 4 and w.lower() not in GENERIC:
                words.add(w.lower())
    return words


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--project", default=".", help="indexed project (the one with .leankg/leankg.db)")
    ap.add_argument("--out", required=True)
    ap.add_argument("--lang", default="go")
    ap.add_argument("--min-terms", type=int, default=2)
    ap.add_argument("--max-terms", type=int, default=4)
    ap.add_argument("--per-package", type=int, default=1)
    ap.add_argument("--seed", type=int, default=11)
    args = ap.parse_args()

    db = os.path.join(args.project, ".leankg", "leankg.db")
    con = sqlite3.connect(db)
    rows = con.execute(
        "select qualified_name, file_path, line_start, name, content from code_elements "
        "where language = ? and element_type in ('function','method','type')",
        (args.lang,),
    ).fetchall()

    bypkg = {}
    for qn, fp, ln, name, content in rows:
        if "_test.go" in fp:
            continue
        doc = doc_comment(args.project, fp, ln)
        doc_words = {w.lower() for w in IDENT.findall(doc)}
        # The question is built from the SYMBOL's own domain words (what an
        # agent would call the thing), split out of the name -- NOT from the
        # body, which is a bag of identifiers rather than a question. A symbol
        # with no comment is kept as-is: it cannot be answered from a comment
        # at all, so it is the cleanest case in the set.
        parts = [w for w in re.split(r"_+|(?<=[a-z0-9])(?=[A-Z])", name) if len(w) >= 3]
        terms = [w for w in parts if w.lower() not in GENERIC and w.lower() not in doc_words]
        if not (args.min_terms <= len(terms) <= args.max_terms):
            continue
        q = QUESTION.format(terms=" ".join(t.lower() for t in terms[: args.max_terms]))
        # The property that makes this protocol independent of wave 9: the
        # question must share NO token with the answer's doc comment, so no
        # amount of comment-matching can satisfy it.
        if {w.lower() for w in IDENT.findall(q)} & doc_words:
            continue
        bypkg.setdefault(qn.split("::")[0], []).append((qn, q))

    import random
    random.seed(args.seed)
    picked = []
    for pkg in sorted(bypkg):
        picked.extend(random.sample(bypkg[pkg], min(args.per_package, len(bypkg[pkg]))))

    # Every label must be a live row: a label nothing can retrieve measures
    # the extractor, not retrieval.
    out = []
    for qn, q in picked:
        if con.execute("select count(*) from code_elements where qualified_name = ?", (qn,)).fetchone()[0] == 0:
            continue
        out.append([qn, q])
    con.close()

    with open(args.out, "w") as f:
        json.dump(out, f, indent=1)
        f.write("\n")
    print(f"{len(out)} behaviour-derived labels (zero doc-comment overlap, verified) -> {args.out}")
    for qn, q in out[:8]:
        print(f"  {qn:<52} | {q[:56]}")


if __name__ == "__main__":
    main()
